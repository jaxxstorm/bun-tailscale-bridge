package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/protocol"
	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/state"
)

var testProxy = protocol.Proxy{Host: "127.0.0.1", Port: 12345, Username: "tsnet", Password: strings.Repeat("a", 32)}

type fakeNode struct {
	up    func(context.Context, func(string) error) error
	loop  func() (protocol.Proxy, error)
	close func() error
}

func (n *fakeNode) Up(ctx context.Context, auth func(string) error) error {
	if n.up != nil {
		return n.up(ctx, auth)
	}
	return nil
}
func (n *fakeNode) Loopback() (protocol.Proxy, error) {
	if n.loop != nil {
		return n.loop()
	}
	return testProxy, nil
}
func (n *fakeNode) Close() error {
	if n.close != nil {
		return n.close()
	}
	return nil
}

func (n *fakeNode) Dial(context.Context, string, string) (net.Conn, error) {
	return nil, protocol.HelperFailed
}

type harness struct {
	in       *io.PipeWriter
	events   chan protocol.Event
	done     chan int
	readDone chan struct{}
	cancel   context.CancelFunc
}

func launch(t *testing.T, factory Factory, cleanup time.Duration) *harness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	h := &harness{in: inW, events: make(chan protocol.Event, 16), done: make(chan int, 1), readDone: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(h.readDone)
		defer close(h.events)
		defer outR.Close()
		scanner := bufio.NewScanner(outR)
		for scanner.Scan() {
			var e protocol.Event
			if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
				t.Error("non-JSON stdout")
				return
			}
			h.events <- e
		}
	}()
	go func() { code := run(ctx, inR, outW, factory, cleanup); outW.Close(); h.done <- code }()
	t.Cleanup(func() { cancel(); inW.Close(); outR.Close() })
	return h
}

func (h *harness) send(t *testing.T, s string) {
	t.Helper()
	if _, err := io.WriteString(h.in, s+"\n"); err != nil {
		t.Fatal(err)
	}
}
func (h *harness) event(t *testing.T) protocol.Event {
	t.Helper()
	select {
	case e, ok := <-h.events:
		if !ok {
			t.Fatal("missing event")
		}
		return e
	case <-time.After(3 * time.Second):
		t.Fatal("event timeout")
	}
	return protocol.Event{}
}
func (h *harness) exit(t *testing.T, want int) {
	t.Helper()
	select {
	case code := <-h.done:
		if code != want {
			t.Fatalf("exit=%d want=%d", code, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exit timeout")
	}
	select {
	case <-h.readDone:
	case <-time.After(time.Second):
		t.Fatal("reader leak")
	}
}
func frame(o protocol.Options) string {
	b, _ := json.Marshal(protocol.Command{Type: "start", Version: 1, Options: &o})
	return string(b)
}
func opts() protocol.Options {
	return protocol.Options{Hostname: "test-bridge", Ephemeral: true, StartupTimeoutMS: 1000}
}

func TestValidationBeforeNode(t *testing.T) {
	for _, input := range []string{`{`, `{"type":"ping","version":1}`, strings.Replace(frame(opts()), "test-bridge", "-invalid", 1), strings.Repeat("x", protocol.MaxFrameBytes+1)} {
		h := launch(t, func(protocol.Options, string) Node { t.Error("node created for invalid input"); return &fakeNode{} }, time.Second)
		// An oversized frame may be rejected before its writer finishes.
		_, _ = io.WriteString(h.in, input+"\n")
		e := h.event(t)
		if e.Type != "error" {
			t.Fatal("missing validation error")
		}
		h.exit(t, 1)
	}
	o := opts()
	o.Ephemeral = false
	o.StateDir = t.TempDir()
	if err := os.Chmod(o.StateDir, 0755); err != nil {
		t.Fatal(err)
	}
	h := launch(t, func(protocol.Options, string) Node { t.Error("node created for unsafe state"); return &fakeNode{} }, time.Second)
	h.send(t, frame(o))
	if e := h.event(t); e.Code != protocol.StateUnsafe {
		t.Fatalf("error=%v", e.Code)
	}
	h.exit(t, 1)
}

func TestReadyCloseEOFCancelAndProtocolFailure(t *testing.T) {
	for _, end := range []string{"close", "eof", "cancel", "duplicate-start", "malformed", "unknown", "version"} {
		t.Run(end, func(t *testing.T) {
			closed := atomic.Int32{}
			dir := ""
			h := launch(t, func(_ protocol.Options, d string) Node {
				dir = d
				return &fakeNode{close: func() error { closed.Add(1); return nil }}
			}, time.Second)
			h.send(t, frame(opts()))
			e := h.event(t)
			if e.Type != "ready" || e.Version != 1 || e.Proxy == nil || !e.Proxy.Valid() || e.HTTPProxy == nil || !e.HTTPProxy.Valid() {
				t.Fatal("invalid readiness")
			}
			if e.Proxy.Password == e.HTTPProxy.Password || e.Proxy.Port == e.HTTPProxy.Port {
				t.Fatal("HTTP and SOCKS must be independent")
			}
			httpAddress := net.JoinHostPort(e.HTTPProxy.Host, strconv.Itoa(e.HTTPProxy.Port))
			want := 0
			switch end {
			case "close":
				h.send(t, `{"type":"close","version":1}`)
			case "eof":
				h.in.Close()
			case "cancel":
				h.cancel()
			case "duplicate-start":
				h.send(t, frame(opts()))
				want = 1
			case "malformed":
				h.send(t, `{"secret":`)
				want = 1
			case "unknown":
				h.send(t, `{"type":"ping","version":1}`)
				want = 1
			case "version":
				h.send(t, `{"type":"close","version":2}`)
				want = 1
			}
			h.exit(t, want)
			if c, err := net.DialTimeout("tcp", httpAddress, time.Second); err == nil {
				c.Close()
				t.Fatal("HTTP listener survived disposal")
			}
			if closed.Load() != 1 {
				t.Fatal("node not closed exactly once")
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("temporary state not cleaned")
			}
			if want == 1 {
				e := h.event(t)
				if e.Type != "error" || e.Code != protocol.ProtocolError {
					t.Fatal("wrong post-ready protocol error")
				}
			}
		})
	}
}

func TestStartupDisposalAndTimeout(t *testing.T) {
	for _, end := range []string{"eof", "close", "cancel", "timeout"} {
		t.Run(end, func(t *testing.T) {
			started := make(chan struct{})
			closed := make(chan struct{})
			dir := ""
			h := launch(t, func(_ protocol.Options, d string) Node {
				dir = d
				return &fakeNode{
					up: func(ctx context.Context, _ func(string) error) error { close(started); <-ctx.Done(); return ctx.Err() },
					loop: func() (protocol.Proxy, error) {
						t.Error("proxy opened during cancelled startup")
						return testProxy, nil
					},
					close: func() error { close(closed); return nil },
				}
			}, time.Second)
			o := opts()
			if end == "timeout" {
				o.StartupTimeoutMS = 30
			}
			h.send(t, frame(o))
			<-started
			want := 0
			switch end {
			case "eof":
				h.in.Close()
			case "close":
				h.send(t, `{"type":"close","version":1}`)
			case "cancel":
				h.cancel()
			case "timeout":
				want = 1
			}
			h.exit(t, want)
			select {
			case <-closed:
			default:
				t.Fatal("partial node leaked")
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("partial state leaked")
			}
			if want == 1 {
				if e := h.event(t); e.Code != protocol.StartupTimeout {
					t.Fatalf("timeout error=%v", e.Code)
				}
			}
		})
	}
}

func TestAuthAndStartupFailures(t *testing.T) {
	for _, scenario := range []string{"interactive", "not-interactive", "invalid-url", "up-error", "loop-error", "invalid-proxy"} {
		t.Run(scenario, func(t *testing.T) {
			closed := atomic.Bool{}
			h := launch(t, func(protocol.Options, string) Node {
				return &fakeNode{
					up: func(_ context.Context, auth func(string) error) error {
						switch scenario {
						case "interactive", "not-interactive":
							return auth("https://login.test/a/synthetic")
						case "invalid-url":
							return auth("http://secret.test")
						case "up-error":
							return errors.New("raw synthetic secret")
						}
						return nil
					},
					loop: func() (protocol.Proxy, error) {
						if scenario == "loop-error" {
							return protocol.Proxy{}, errors.New("raw synthetic secret")
						}
						if scenario == "invalid-proxy" {
							return protocol.Proxy{Password: "secret"}, nil
						}
						return testProxy, nil
					},
					close: func() error { closed.Store(true); return nil },
				}
			}, time.Second)
			o := opts()
			o.Interactive = scenario != "not-interactive"
			o.AuthKey = "synthetic-key"
			h.send(t, frame(o))
			e := h.event(t)
			if scenario == "interactive" {
				if e.Type != "auth_required" || e.URL != "https://login.test/a/synthetic" {
					t.Fatal("missing structured auth")
				}
				if e = h.event(t); e.Type != "ready" {
					t.Fatal("auth did not precede readiness")
				}
				h.in.Close()
				h.exit(t, 0)
			} else {
				want := protocol.HelperFailed
				switch scenario {
				case "not-interactive":
					want = protocol.AuthRequired
				case "invalid-url":
					want = protocol.AuthFailed
				case "invalid-proxy":
					want = protocol.ProtocolError
				}
				if e.Type != "error" || e.Code != want || e.URL != "" || e.Proxy != nil || e.HTTPProxy != nil {
					t.Fatalf("unsanitized or wrong event: type=%s code=%s", e.Type, e.Code)
				}
				h.exit(t, 1)
			}
			if !closed.Load() {
				t.Fatal("startup resources not closed")
			}
		})
	}
}

func TestReadyOutlivesStartupDeadline(t *testing.T) {
	h := launch(t, func(protocol.Options, string) Node { return &fakeNode{} }, time.Second)
	o := opts()
	o.StartupTimeoutMS = 40
	h.send(t, frame(o))
	if h.event(t).Type != "ready" {
		t.Fatal("not ready")
	}
	time.Sleep(80 * time.Millisecond)
	select {
	case <-h.done:
		t.Fatal("startup deadline killed ready node")
	default:
	}
	h.in.Close()
	h.exit(t, 0)
}

func TestUninterruptibleStartKeepsLock(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})
	closed := make(chan struct{})
	h := launch(t, func(protocol.Options, string) Node {
		return &fakeNode{
			up:    func(context.Context, func(string) error) error { close(started); <-unblock; return nil },
			close: func() error { close(closed); return nil },
		}
	}, 20*time.Millisecond)
	o := opts()
	o.Ephemeral = false
	o.StateDir = filepath.Join(t.TempDir(), "state")
	o.StartupTimeoutMS = 30
	h.send(t, frame(o))
	<-started
	if h.event(t).Code != protocol.StartupTimeout {
		t.Fatal("timeout missing")
	}
	h.exit(t, 1)
	if s, err := state.Open(o); err != protocol.StateLocked {
		if s != nil {
			s.Close()
		}
		t.Fatal("live startup lock released")
	}
	close(unblock)
	<-closed
	deadline := time.Now().Add(time.Second)
	for {
		s, err := state.Open(o)
		if err == nil {
			s.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lock not released after startup stopped")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestInitialDisposal(t *testing.T) {
	for _, sendClose := range []bool{false, true} {
		h := launch(t, func(protocol.Options, string) Node { t.Fatal("unexpected node creation"); return nil }, time.Second)
		if sendClose {
			h.send(t, `{"type":"close","version":1}`)
		} else {
			h.in.Close()
		}
		h.exit(t, 0)
		if _, ok := <-h.events; ok {
			t.Fatal("unexpected disposal status frame")
		}
	}
}

type blockedOutput struct {
	started chan struct{}
	unblock chan struct{}
	done    chan struct{}
}

func (w *blockedOutput) Write(b []byte) (int, error) {
	close(w.started)
	<-w.unblock
	close(w.done)
	return len(b), nil
}

func TestBlockedOutputDoesNotBlockDisposal(t *testing.T) {
	for _, mode := range []string{"eof", "cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			input, parent := io.Pipe()
			defer parent.Close()
			out := &blockedOutput{started: make(chan struct{}), unblock: make(chan struct{}), done: make(chan struct{})}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			closed := atomic.Bool{}
			finished := make(chan int, 1)
			go func() {
				finished <- run(ctx, input, out, func(protocol.Options, string) Node {
					return &fakeNode{close: func() error { closed.Store(true); return nil }}
				}, time.Second)
			}()
			o := opts()
			if mode == "timeout" {
				o.StartupTimeoutMS = 30
			}
			if _, err := io.WriteString(parent, frame(o)+"\n"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-out.started:
			case <-time.After(time.Second):
				t.Fatal("write never started")
			}
			switch mode {
			case "eof":
				parent.Close()
			case "cancel":
				cancel()
			}
			select {
			case code := <-finished:
				want := 0
				if mode == "timeout" {
					want = 1
				}
				if code != want || !closed.Load() {
					t.Fatal("stalled stdout blocked disposal")
				}
			case <-time.After(time.Second):
				t.Fatal("disposal blocked by stdout")
			}
			close(out.unblock)
			<-out.done
		})
	}
}

func TestDualStartupFailureCleanup(t *testing.T) {
	for _, mode := range []string{"cancel-before-http", "ready-write-fails"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			closed := false
			dir := ""
			httpAddress := ""
			err := serve(ctx, context.Background(), opts(), func(_ protocol.Options, d string) Node {
				dir = d
				return &fakeNode{
					loop: func() (protocol.Proxy, error) {
						if mode == "cancel-before-http" {
							cancel()
						}
						return testProxy, nil
					},
					close: func() error { closed = true; return nil },
				}
			}, func(e protocol.Event) error {
				if e.HTTPProxy == nil {
					t.Fatal("partial readiness emitted")
				}
				httpAddress = net.JoinHostPort(e.HTTPProxy.Host, strconv.Itoa(e.HTTPProxy.Port))
				return protocol.ProtocolError
			})
			if err == nil || !closed {
				t.Fatal("partial node not cleaned")
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("partial state not cleaned")
			}
			if mode == "cancel-before-http" && httpAddress != "" {
				t.Fatal("readiness emitted after cancellation")
			}
			if mode == "ready-write-fails" {
				if httpAddress == "" {
					t.Fatal("HTTP listener was never established")
				}
				if c, err := net.DialTimeout("tcp", httpAddress, time.Second); err == nil {
					c.Close()
					t.Fatal("HTTP listener survived readiness failure")
				}
			}
		})
	}
}
