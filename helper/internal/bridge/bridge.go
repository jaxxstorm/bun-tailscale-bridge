// Package bridge manages one helper lifetime. The executable exiting is the
// final boundary for all accepted SOCKS connections, including host routes.
package bridge

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/httpproxy"
	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/protocol"
	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/state"
)

type Node interface {
	Up(context.Context, func(string) error) error
	Loopback() (protocol.Proxy, error)
	Dial(context.Context, string, string) (net.Conn, error)
	Close() error
}

type Factory func(protocol.Options, string) Node

// Run takes ownership of input. Close/EOF is normal disposal, including during
// startup. The caller must exit the process after Run returns.
func Run(ctx context.Context, input io.ReadCloser, output io.Writer, factory Factory) int {
	return run(ctx, input, output, factory, 4*time.Second)
}

func run(ctx context.Context, input io.ReadCloser, output io.Writer, factory Factory, cleanupTimeout time.Duration) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer input.Close()
	type readResult struct {
		command protocol.Command
		err     error
	}
	commands := make(chan readResult)
	go func() {
		r := protocol.NewReader(input)
		for {
			command, err := r.Next()
			select {
			case commands <- readResult{command, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	beginWrite := func(e protocol.Event) <-chan error {
		done := make(chan error, 1)
		go func() { done <- protocol.Write(output, e) }()
		return done
	}
	var writing <-chan error
	fail := func(err error) int {
		// Never interleave a diagnostic with a pending frame. A stalled
		// parent cannot prevent cancellation; error reporting is also bounded.
		timer := time.NewTimer(250 * time.Millisecond)
		defer timer.Stop()
		if writing != nil {
			select {
			case writeErr := <-writing:
				if writeErr != nil {
					return 1
				}
				writing = nil
			case <-ctx.Done():
				return 1
			case <-timer.C:
				return 1
			}
		}
		select {
		case <-beginWrite(protocol.Event{Type: "error", Code: protocol.SafeCode(err)}):
		case <-ctx.Done():
		case <-timer.C:
		}
		return 1
	}
	var first readResult
	select {
	case <-ctx.Done():
		return 0
	case first = <-commands:
	}
	if first.err == io.EOF || (first.err == nil && first.command.Type == "close") {
		return 0
	}
	if first.err != nil {
		return fail(first.err)
	}
	if first.command.Type != "start" || first.command.Options == nil {
		return fail(protocol.ProtocolError)
	}
	o := *first.command.Options
	startup, stopStartup := context.WithTimeout(ctx, time.Duration(o.StartupTimeoutMS)*time.Millisecond)
	defer stopStartup()
	events := make(chan protocol.Event)
	finished := make(chan error, 1)
	go func() {
		finished <- serve(ctx, startup, o, factory, func(e protocol.Event) error {
			select {
			case events <- e:
				return nil
			case <-startup.Done():
				return startup.Err()
			}
		})
	}()
	defer func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(cleanupTimeout):
			// A synchronous tsnet.Start cannot be closed concurrently. Leave
			// its state locked until process exit rather than deleting live state.
		}
	}()
	ready := false
	startupDone := startup.Done()
	var pending protocol.Event
	var incoming <-chan protocol.Event = events
	for {
		select {
		case <-ctx.Done():
			return 0
		case <-startupDone:
			if ctx.Err() != nil {
				return 0
			}
			return fail(protocol.StartupTimeout)
		case next := <-commands:
			if next.err == io.EOF || (next.err == nil && next.command.Type == "close") {
				return 0
			}
			if next.err != nil {
				return fail(next.err)
			}
			return fail(protocol.ProtocolError)
		case err := <-finished:
			// Keep the cleanup defer from waiting twice.
			finished <- err
			if ctx.Err() != nil {
				return 0
			}
			if errors.Is(err, context.DeadlineExceeded) || startup.Err() == context.DeadlineExceeded {
				return fail(protocol.StartupTimeout)
			}
			if err == nil {
				err = protocol.HelperFailed
			}
			return fail(err)
		case e := <-incoming:
			if startup.Err() != nil {
				if ctx.Err() != nil {
					return 0
				}
				return fail(protocol.StartupTimeout)
			}
			if e.Type == "auth_required" && (!o.Interactive || ready) {
				return fail(protocol.ProtocolError)
			}
			pending = e
			writing = beginWrite(e)
			incoming = nil
		case err := <-writing:
			writing = nil
			incoming = events
			if err != nil {
				return 1
			}
			if pending.Type == "ready" {
				ready = true
				startupDone = nil
				stopStartup()
			}
		}
	}
}

func serve(lifetime, startup context.Context, o protocol.Options, factory Factory, emit func(protocol.Event) error) (err error) {
	s, err := state.Open(o)
	if err != nil {
		return err
	}
	defer func() {
		if e := s.Close(); err == nil {
			err = e
		}
	}()
	if err := startup.Err(); err != nil {
		return err
	}
	node := factory(o, s.Dir)
	if node == nil {
		return protocol.HelperFailed
	}
	defer func() {
		if e := node.Close(); err == nil {
			err = e
		}
	}()
	if err := node.Up(startup, func(url string) error {
		if !o.Interactive {
			return protocol.AuthRequired
		}
		if !protocol.ValidAuthURL(url) {
			return protocol.AuthFailed
		}
		return emit(protocol.Event{Type: "auth_required", URL: url})
	}); err != nil {
		return err
	}
	if err := startup.Err(); err != nil {
		return err
	}
	proxy, err := node.Loopback()
	if err != nil {
		return err
	}
	if !proxy.Valid() {
		return protocol.ProtocolError
	}
	httpServer, err := httpproxy.Start(lifetime, node.Dial)
	if err != nil {
		return err
	}
	defer httpServer.Close()
	httpProxy := httpServer.Proxy()
	if err := emit(protocol.Event{Type: "ready", Proxy: &proxy, HTTPProxy: &httpProxy}); err != nil {
		return err
	}
	select {
	case <-lifetime.Done():
		return nil
	case <-httpServer.Done():
		return protocol.HelperFailed
	}
}
