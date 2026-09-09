// Package tsnode is the production adapter for Tailscale's documented SOCKS API.
package tsnode

import (
	"context"
	"net"
	"strconv"

	"tailscale.com/ipn"
	"tailscale.com/tsnet"

	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/bridge"
	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/protocol"
)

type node struct {
	server  *tsnet.Server
	options protocol.Options
	started bool
}

func New(o protocol.Options, dir string) bridge.Node {
	quiet := func(string, ...any) {}
	return &node{
		options: o,
		server: &tsnet.Server{
			Dir:       dir,
			Hostname:  o.Hostname,
			AuthKey:   o.AuthKey,
			Ephemeral: o.Ephemeral,
			Logf:      quiet,
			UserLogf:  quiet,
		},
	}
}

func (n *node) Up(ctx context.Context, auth func(string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Start is synchronous and cannot race Close. The lifecycle worker owns
	// both calls; its supervisor bounds waiting and ultimately exits the process.
	if err := n.server.Start(); err != nil {
		return protocol.AuthFailed
	}
	n.started = true
	lc, err := n.server.LocalClient()
	if err != nil {
		return protocol.AuthFailed
	}
	w, err := lc.WatchIPNBus(ctx, ipn.NotifyInitialState)
	if err != nil {
		return protocol.AuthFailed
	}
	defer w.Close()
	return wait(ctx, n.options, w.Next, func(ctx context.Context) error {
		_, err := n.server.Up(ctx)
		return err
	}, auth)
}

func wait(ctx context.Context, o protocol.Options, next func() (ipn.Notify, error), up func(context.Context) error, auth func(string) error) error {
	lastURL := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := next()
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil || n.ErrMessage != nil {
			return protocol.AuthFailed
		}
		if n.State != nil && *n.State == ipn.Running {
			if err := up(ctx); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return protocol.AuthFailed
			}
			return nil
		}
		// NeedsLogin can be transient while an explicit key is being used.
		// Do not replace that attempt with interactive enrollment.
		if o.AuthKey != "" {
			continue
		}
		needsAuth := n.State != nil && *n.State == ipn.NeedsLogin
		url := ""
		if n.BrowseToURL != nil {
			url = *n.BrowseToURL
		}
		if !o.Interactive && (needsAuth || url != "") {
			return protocol.AuthRequired
		}
		if url != "" && url != lastURL {
			if !protocol.ValidAuthURL(url) {
				return protocol.AuthFailed
			}
			if err := auth(url); err != nil {
				return err
			}
			lastURL = url
		}
	}
}

func (n *node) Loopback() (protocol.Proxy, error) {
	// The third return value grants LocalAPI access. Never put it on the wire.
	addr, password, _, err := n.server.Loopback()
	if err != nil {
		return protocol.Proxy{}, protocol.HelperFailed
	}
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return protocol.Proxy{}, protocol.HelperFailed
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return protocol.Proxy{}, protocol.HelperFailed
	}
	return protocol.Proxy{Host: host, Port: port, Username: "tsnet", Password: password}, nil
}

func (n *node) Close() error {
	if !n.started {
		return nil
	} // failed Start cleans its own partially built subsystems
	n.started = false
	if err := n.server.Close(); err != nil {
		return protocol.HelperFailed
	}
	return nil
}

func (n *node) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	return n.server.Dial(ctx, network, address)
}
