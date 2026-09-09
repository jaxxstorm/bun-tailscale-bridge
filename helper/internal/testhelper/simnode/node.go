// Package simnode is TEST ONLY. It reuses Tailscale's SOCKS implementation
// without starting or enrolling a Tailscale node. Production never imports it.
package simnode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"strconv"
	"sync"

	"tailscale.com/net/socks5"

	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/bridge"
	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/protocol"
)

type Node struct {
	mu          sync.Mutex
	listener    net.Listener
	connections map[*connection]bool
	closed      bool
	cancel      context.CancelFunc
	ctx         context.Context
	// Dials reports the original SOCKS destination, before simulated DNS.
	Dials chan string
}

func New(protocol.Options, string) bridge.Node { return &Node{} }

func (n *Node) Up(ctx context.Context, _ func(string) error) error { return ctx.Err() }

func (n *Node) Loopback() (protocol.Proxy, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed || n.listener != nil {
		return protocol.Proxy{}, protocol.HelperFailed
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return protocol.Proxy{}, protocol.HelperFailed
	}
	n.listener = ln
	n.connections = make(map[*connection]bool)
	ctx, cancel := context.WithCancel(context.Background())
	n.ctx = ctx
	n.cancel = cancel
	var password [16]byte
	_, _ = rand.Read(password[:])
	proxy := protocol.Proxy{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, Username: "tsnet", Password: hex.EncodeToString(password[:])}
	server := &socks5.Server{
		Username: proxy.Username,
		Password: proxy.Password,
		Logf:     func(string, ...any) {},
		Dialer:   n.Dial,
	}
	go func() { _ = server.Serve(&listener{Listener: ln, node: n}) }()
	return proxy, nil
}

func (n *Node) Dial(dialCtx context.Context, network, addr string) (net.Conn, error) {
	n.mu.Lock()
	ctx, closed := n.ctx, n.closed
	n.mu.Unlock()
	if ctx == nil || closed {
		return nil, net.ErrClosed
	}
	select {
	case n.Dials <- addr:
	default:
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, protocol.HelperFailed
	}
	// A .test name proves the client passed a hostname to SOCKS,
	// rather than attempting a host DNS lookup before connecting.
	if host == "upstream.test" || host == "localhost" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, protocol.HelperFailed
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return nil, protocol.HelperFailed
	}
	dialCtx, cancel := context.WithCancel(dialCtx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	return (&net.Dialer{}).DialContext(dialCtx, network, net.JoinHostPort(host, port))
}

func (n *Node) Close() error {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return nil
	}
	n.closed = true
	if n.cancel != nil {
		n.cancel()
	}
	if n.listener != nil {
		_ = n.listener.Close()
	}
	connections := make([]*connection, 0, len(n.connections))
	for c := range n.connections {
		connections = append(connections, c)
	}
	n.mu.Unlock()
	for _, c := range connections {
		_ = c.Close()
	}
	return nil
}

type listener struct {
	net.Listener
	node *Node
}

func (l *listener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.node.mu.Lock()
	defer l.node.mu.Unlock()
	if l.node.closed {
		_ = c.Close()
		return nil, net.ErrClosed
	}
	tracked := &connection{Conn: c, node: l.node}
	l.node.connections[tracked] = true
	return tracked, nil
}

type connection struct {
	net.Conn
	node *Node
}

func (c *connection) Close() error {
	err := c.Conn.Close()
	c.node.mu.Lock()
	delete(c.node.connections, c)
	c.node.mu.Unlock()
	return err
}
