package simnode

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/protocol"
	"golang.org/x/net/proxy"
)

func start(t *testing.T) (*Node, protocol.Proxy) {
	t.Helper()
	n := &Node{Dials: make(chan string, 32)}
	p, err := n.Loopback()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.Close() })
	return n, p
}

func dialer(t *testing.T, p protocol.Proxy, auth *proxy.Auth) proxy.ContextDialer {
	t.Helper()
	d, err := proxy.SOCKS5("tcp", net.JoinHostPort(p.Host, strconv.Itoa(p.Port)), auth, &net.Dialer{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return d.(proxy.ContextDialer)
}

func client(t *testing.T, p protocol.Proxy) *http.Client {
	t.Helper()
	d := dialer(t, p, &proxy.Auth{User: p.Username, Password: p.Password})
	tr := &http.Transport{DialContext: d.DialContext, DisableKeepAlives: true}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 3 * time.Second}
}

func TestSOCKSAuthenticationBeforeDial(t *testing.T) {
	n, p := start(t)
	for _, auth := range []*proxy.Auth{nil, {User: p.Username, Password: "wrong"}, {User: "wrong", Password: p.Password}} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		c, err := dialer(t, p, auth).DialContext(ctx, "tcp", "upstream.test:80")
		cancel()
		if c != nil {
			c.Close()
		}
		if err == nil {
			t.Fatal("unauthenticated SOCKS accepted")
		}
	}
	select {
	case <-n.Dials:
		t.Fatal("authentication failure dialed upstream")
	default:
	}
	// Also reject SOCKS4 and unauthenticated SOCKS5 greetings before dialing.
	for _, greeting := range [][]byte{{4, 1, 0}, {5, 1, 0}} {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(p.Host, strconv.Itoa(p.Port)), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		c.SetDeadline(time.Now().Add(time.Second))
		c.Write(greeting)
		var reply [2]byte
		io.ReadFull(c, reply[:])
		c.Close()
		if reply[1] != 255 {
			t.Fatalf("invalid greeting accepted: %v", reply)
		}
	}
}

func TestSOCKSRemoteDNSAndHTTPBytes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Host, "upstream.test:") || r.RequestURI != "/a%2Fb?q=x%2Fy" || r.Method != "POST" {
			t.Error("request changed")
		}
		if r.Header.Get("Authorization") != "Bearer application-secret" || r.Header.Get("Cookie") != "session=synthetic" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("credential scoping changed")
		}
		w.Header().Set("X-Upstream", "yes")
		w.WriteHeader(429)
		io.Copy(w, r.Body)
	}))
	defer upstream.Close()
	_, port, _ := net.SplitHostPort(upstream.Listener.Addr().String())
	n, p := start(t)
	body := strings.Repeat("streamed-data", 100000)
	r, err := http.NewRequest("POST", "http://upstream.test:"+port+"/a%2Fb?q=x%2Fy", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer application-secret")
	r.Header.Set("Cookie", "session=synthetic")
	response, err := client(t, p).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	b, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 429 || response.Header.Get("X-Upstream") != "yes" || string(b) != body {
		t.Fatal("HTTP bytes not preserved")
	}
	if got := <-n.Dials; got != "upstream.test:"+port {
		t.Fatalf("SOCKS hostname not passed to dialer: %s", got)
	}
}

func TestSOCKSStreamingCancellationAndConcurrency(t *testing.T) {
	cancelled := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: first\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			cancelled <- struct{}{}
			return
		}
		io.WriteString(w, "concurrent")
	}))
	defer upstream.Close()
	_, port, _ := net.SplitHostPort(upstream.Listener.Addr().String())
	_, p := start(t)
	c := client(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", "http://upstream.test:"+port+"/stream", nil)
	response, err := c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatal("SSE was buffered")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			r, err := c.Get("http://upstream.test:" + port + "/other")
			if err != nil {
				t.Error(err)
				return
			}
			defer r.Body.Close()
			b, err := io.ReadAll(r.Body)
			if err != nil || string(b) != "concurrent" {
				t.Error("concurrent request failed")
			}
		})
	}
	wg.Wait()
	cancel()
	response.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("upstream disconnect not propagated")
	}
}

func TestSOCKSStreamingUpload(t *testing.T) {
	first := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 5)
		if _, err := io.ReadFull(r.Body, b); err != nil || string(b) != "first" {
			t.Error("missing upload prefix")
		}
		close(first)
		b, err := io.ReadAll(r.Body)
		if err != nil || string(b) != "last" {
			t.Error("missing upload suffix")
		}
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	_, port, _ := net.SplitHostPort(upstream.Listener.Addr().String())
	_, p := start(t)
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	done := make(chan error, 1)
	c := client(t, p)
	go func() {
		response, err := c.Post("http://upstream.test:"+port, "application/octet-stream", r)
		if err == nil {
			_, err = io.ReadAll(response.Body)
			response.Body.Close()
		}
		done <- err
	}()
	if _, err := io.WriteString(w, "first"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("upload buffered before reaching upstream")
	}
	io.WriteString(w, "last")
	w.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSOCKSTLSStaysEndToEnd(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "secure") }))
	defer upstream.Close()
	_, p := start(t)
	c := client(t, p)
	if r, err := c.Get(upstream.URL); err == nil {
		r.Body.Close()
		t.Fatal("untrusted certificate accepted")
	}
	// The SOCKS layer is unchanged; only the test HTTP client's trust store changes.
	tr := c.Transport.(*http.Transport)
	tr.TLSClientConfig = upstream.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	r, err := c.Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if !bytes.Equal(b, []byte("secure")) {
		t.Fatal("TLS tunnel changed bytes")
	}
	tr.TLSClientConfig = &tls.Config{RootCAs: tr.TLSClientConfig.RootCAs, ServerName: "wrong.test"}
	if r, err := c.Get(upstream.URL); err == nil {
		r.Body.Close()
		t.Fatal("hostname mismatch accepted")
	}
}

func TestSOCKSCloseAndFailure(t *testing.T) {
	n, p := start(t)
	d := dialer(t, p, &proxy.Auth{User: p.Username, Password: p.Password})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if c, err := d.DialContext(ctx, "tcp", "not-allowed.test:80"); err == nil {
		c.Close()
		t.Fatal("test-only resolver accepted unknown name")
	}
	if got := <-n.Dials; got != "not-allowed.test:80" {
		t.Fatal("unexpected dial")
	}
	select {
	case <-n.Dials:
		t.Fatal("SOCKS request was retried")
	default:
	}
	upstream, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	remoteClosed := make(chan struct{})
	go func() {
		c, err := upstream.Accept()
		if err == nil {
			defer c.Close()
			io.Copy(io.Discard, c)
		}
		close(remoteClosed)
	}()
	c, err := d.DialContext(ctx, "tcp", upstream.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	n.Close()
	n.Close()
	c.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err := c.Read(b[:]); err == nil {
		t.Fatal("active tunnel survived close")
	}
	select {
	case <-remoteClosed:
	case <-time.After(time.Second):
		t.Fatal("upstream tunnel survived close")
	}
	if c, err := net.DialTimeout("tcp", net.JoinHostPort(p.Host, strconv.Itoa(p.Port)), time.Second); err == nil {
		c.Close()
		t.Fatal("closed listener accepted connection")
	}
}

func TestSOCKSIPv6Literal(t *testing.T) {
	upstream, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip("IPv6 loopback unavailable")
	}
	defer upstream.Close()
	go func() {
		c, err := upstream.Accept()
		if err == nil {
			defer c.Close()
			io.WriteString(c, "ipv6")
		}
	}()
	n, p := start(t)
	d := dialer(t, p, &proxy.Auth{User: p.Username, Password: p.Password})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := d.DialContext(ctx, "tcp", upstream.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	b, err := io.ReadAll(c)
	if err != nil || string(b) != "ipv6" {
		t.Fatal("IPv6 SOCKS failed")
	}
	if got := <-n.Dials; got != upstream.Addr().String() {
		t.Fatal("IPv6 address changed")
	}
}
