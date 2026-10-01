// Package httpproxy exposes an authenticated HTTP forward/CONNECT proxy.
// Routing is delegated unchanged to the node's dialer, not an egress policy.
package httpproxy

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/http/httputil"
	"net/netip"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"

	"tailscale.com/net/connectproxy"

	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/protocol"
)

type DialFunc func(context.Context, string, string) (net.Conn, error)

type requestContextKey struct{}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type Server struct {
	proxy       protocol.Proxy
	server      *http.Server
	listener    net.Listener
	transport   *http.Transport
	cancel      context.CancelFunc
	done        chan error
	mu          sync.Mutex
	closed      bool
	connections map[*connection]bool
}

func Start(ctx context.Context, dial DialFunc) (*Server, error) {
	if dial == nil || ctx.Err() != nil {
		return nil, protocol.HelperFailed
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, protocol.HelperFailed
	}
	var secret [16]byte
	_, _ = rand.Read(secret[:])
	ctx, cancel := context.WithCancel(ctx)
	p := &Server{
		listener:    ln,
		proxy:       protocol.Proxy{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, Username: "tsnet", Password: hex.EncodeToString(secret[:])},
		cancel:      cancel,
		done:        make(chan error, 1),
		connections: make(map[*connection]bool),
	}
	trackedDial := func(requestCtx context.Context, network, address string) (net.Conn, error) {
		dialCtx, cancel := context.WithTimeout(requestCtx, 30*time.Second)
		defer cancel()
		stop := context.AfterFunc(ctx, cancel)
		defer stop()
		// Transport detaches dial cancellation for connection pooling. We do
		// not reuse connections, so bind dialing back to the incoming request.
		if incoming, ok := requestCtx.Value(requestContextKey{}).(context.Context); ok {
			stopRequest := context.AfterFunc(incoming, cancel)
			defer stopRequest()
		}
		c, err := dial(dialCtx, network, address)
		if err != nil {
			return nil, err
		}
		return p.track(c)
	}
	p.transport = &http.Transport{
		Proxy:       nil,
		DialContext: trackedDial,
		// Go retries some requests on reused connections. Never reuse one.
		DisableKeepAlives:     true,
		DisableCompression:    true,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	forward := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out = pr.Out.WithContext(context.WithValue(pr.Out.Context(), requestContextKey{}, pr.In.Context()))
			pr.Out.Host = pr.In.URL.Host
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			stripHeaders(pr.Out.Header)
			// Trailer values arrive after the headers have been authenticated.
			// Do not permit proxy credentials to be smuggled in later trailers.
			pr.Out.Trailer = nil
		},
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			// Install outside ReverseProxy's trace so filtering precedes its
			// automatic forwarding of informational response headers.
			ctx := httptrace.WithClientTrace(r.Context(), &httptrace.ClientTrace{
				Got1xxResponse: func(_ int, h textproto.MIMEHeader) error { stripHeaders(http.Header(h)); return nil },
			})
			return p.transport.RoundTrip(r.WithContext(ctx))
		}),
		FlushInterval: -1,
		ErrorLog:      log.New(io.Discard, "", 0),
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			status := http.StatusBadGateway
			var timeout net.Error
			if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
				status = http.StatusGatewayTimeout
			}
			http.Error(w, "PROXY_ERROR", status)
		},
		ModifyResponse: func(r *http.Response) error {
			if r.StatusCode == http.StatusSwitchingProtocols {
				return protocol.HelperFailed
			}
			stripHeaders(r.Header)
			// No response trailer can introduce proxy authentication either.
			r.Trailer = nil
			r.Body = &withoutTrailers{ReadCloser: r.Body, response: r}
			return nil
		},
	}
	connect := &connectproxy.Handler{Dial: trackedDial, Logf: func(string, ...any) {}}
	userHash := sha256.Sum256([]byte(p.proxy.Username))
	passwordHash := sha256.Sum256([]byte(p.proxy.Password))
	p.server = &http.Server{
		DisableGeneralOptionsHandler: true,
		ReadHeaderTimeout:            15 * time.Second,
		IdleTimeout:                  30 * time.Second,
		MaxHeaderBytes:               protocol.MaxFrameBytes,
		ErrorLog:                     log.New(io.Discard, "", 0),
		BaseContext:                  func(net.Listener) context.Context { return ctx },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			values := r.Header.Values("Proxy-Authorization")
			valid := false
			if len(values) == 1 {
				scheme, value, ok := strings.Cut(values[0], " ")
				if ok && strings.EqualFold(scheme, "Basic") {
					decoded, err := base64.StdEncoding.DecodeString(value)
					user, password, found := strings.Cut(string(decoded), ":")
					uh, ph := sha256.Sum256([]byte(user)), sha256.Sum256([]byte(password))
					matches := subtle.ConstantTimeCompare(uh[:], userHash[:]) & subtle.ConstantTimeCompare(ph[:], passwordHash[:])
					valid = err == nil && found && matches == 1
				}
			}
			if !valid {
				w.Header().Set("Proxy-Authenticate", `Basic realm="tailscale-bridge"`)
				http.Error(w, "PROXY_AUTH_REQUIRED", http.StatusProxyAuthRequired)
				return
			}
			for key := range r.Header {
				if strings.EqualFold(key, "Origin") || strings.EqualFold(key, "Upgrade") || strings.HasPrefix(strings.ToLower(key), "sec-fetch-") {
					http.Error(w, "INVALID_REQUEST", http.StatusBadRequest)
					return
				}
			}
			for _, value := range r.Header.Values("Connection") {
				for _, token := range strings.Split(value, ",") {
					if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
						http.Error(w, "INVALID_REQUEST", http.StatusBadRequest)
						return
					}
				}
			}
			if r.Method == http.MethodConnect {
				if !validAuthority(r.RequestURI, true) || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
					http.Error(w, "INVALID_REQUEST", http.StatusBadRequest)
					return
				}
				stripHeaders(r.Header)
				connect.ServeHTTP(w, r)
				return
			}
			if r.URL.Scheme != "http" || r.URL.User != nil || r.URL.Opaque != "" || !validAuthority(r.URL.Host, false) || r.URL.Fragment != "" || strings.Contains(r.RequestURI, "#") || !strings.HasPrefix(r.RequestURI, "http://") {
				http.Error(w, "INVALID_REQUEST", http.StatusBadRequest)
				return
			}
			forward.ServeHTTP(w, r)
		}),
	}
	go func() {
		err := p.server.Serve(&listener{Listener: ln, proxy: p})
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			err = protocol.HelperFailed
		} else {
			err = nil
		}
		p.done <- err
	}()
	return p, nil
}

func validAuthority(authority string, requirePort bool) bool {
	if authority == "" || strings.ContainsAny(authority, "/\\?#@%") {
		return false
	}
	for _, ch := range authority {
		if ch <= 32 || ch >= 127 {
			return false
		}
	}
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		if requirePort {
			return false
		}
		host = authority
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			host = host[1 : len(host)-1]
		} else if strings.ContainsAny(host, ":[]") {
			return false
		}
	} else {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return false
		}
	}
	if host == "" {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Zone() == "" && (!strings.ContainsAny(authority, "[]") || ip.Is6())
	}
	if strings.ContainsAny(authority, "[]") || len(host) > 253 {
		return false
	}
	for _, ch := range host {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '-' || ch == '_') {
			return false
		}
	}
	return true
}

func stripHeaders(h http.Header) {
	for _, value := range h.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			h.Del(strings.TrimSpace(token))
		}
	}
	for _, key := range []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade", "Forwarded", "X-Real-IP"} {
		h.Del(key)
	}
	for key := range h {
		if strings.HasPrefix(strings.ToLower(key), "x-forwarded-") {
			delete(h, key)
		}
	}
}

func (p *Server) Proxy() protocol.Proxy { return p.proxy }
func (p *Server) Done() <-chan error    { return p.done }

func (p *Server) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	connections := make([]*connection, 0, len(p.connections))
	for c := range p.connections {
		connections = append(connections, c)
	}
	p.mu.Unlock()
	p.cancel()
	_ = p.server.Close()
	// Serve may not have registered the listener when startup fails immediately.
	_ = p.listener.Close()
	for _, c := range connections {
		_ = c.Close()
	}
	p.transport.CloseIdleConnections()
	return nil
}

func (p *Server) track(c net.Conn) (net.Conn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		_ = c.Close()
		return nil, net.ErrClosed
	}
	tracked := &connection{Conn: c, proxy: p}
	p.connections[tracked] = true
	return tracked, nil
}

type listener struct {
	net.Listener
	proxy *Server
}

func (l *listener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return l.proxy.track(c)
}

type connection struct {
	net.Conn
	proxy *Server
}

func (c *connection) Close() error {
	err := c.Conn.Close()
	c.proxy.mu.Lock()
	delete(c.proxy.connections, c)
	c.proxy.mu.Unlock()
	return err
}

type withoutTrailers struct {
	io.ReadCloser
	response *http.Response
}

func (b *withoutTrailers) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.response.Trailer = nil
	}
	return n, err
}
