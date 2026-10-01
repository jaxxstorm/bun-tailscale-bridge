package httpproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func startProxy(t *testing.T, dial DialFunc) *Server {
	t.Helper()
	p, err := Start(context.Background(), dial)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func address(p *Server) string { d := p.Proxy(); return net.JoinHostPort(d.Host, strconv.Itoa(d.Port)) }
func auth(p *Server) string {
	d := p.Proxy()
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(d.Username+":"+d.Password))
}

func client(t *testing.T, p *Server) *http.Client {
	t.Helper()
	d := p.Proxy()
	u := &url.URL{Scheme: "http", Host: address(p), User: url.UserPassword(d.Username, d.Password)}
	tr := &http.Transport{Proxy: http.ProxyURL(u), DisableKeepAlives: true}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func raw(t *testing.T, p *Server, request string) (int, http.Header, string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", address(p), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.WriteString(c, request); err != nil {
		t.Fatal(err)
	}
	r, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return r.StatusCode, r.Header, string(b)
}

func TestCloseBeforeServeClosesListener(t *testing.T) {
	ln, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	if err := ln.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// Hold Serve back entirely: Close must own cleanup before it is scheduled.
	p := &Server{listener: ln, server: &http.Server{}, transport: &http.Transport{}, cancel: cancel}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if c, err := ln.Accept(); !errors.Is(err, net.ErrClosed) {
		if c != nil {
			c.Close()
		}
		t.Fatalf("listener not synchronously closed: %v", err)
	}
	if err := p.server.Serve(&listener{Listener: ln, proxy: p}); !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("late Serve did not remain closed: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthenticationBeforeDial(t *testing.T) {
	var calls atomic.Int32
	p := startProxy(t, func(context.Context, string, string) (net.Conn, error) {
		calls.Add(1)
		return nil, errors.New("upstream-secret")
	})
	for _, target := range []string{"GET http://service.test/path", "CONNECT service.test:443"} {
		for name, header := range map[string]string{
			"missing": "", "application-auth": "Authorization: " + auth(p) + "\r\n",
			"wrong-password": "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("tsnet:wrong")) + "\r\n",
			"wrong-user":     "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("wrong:"+p.Proxy().Password)) + "\r\n",
			"bearer":         "Proxy-Authorization: Bearer " + p.Proxy().Password + "\r\n",
			"invalid-base64": "Proxy-Authorization: Basic %%%\r\n",
			"no-colon":       "Proxy-Authorization: Basic dHNuZXQ=\r\n",
			"duplicate":      "Proxy-Authorization: " + auth(p) + "\r\nProxy-Authorization: " + auth(p) + "\r\n",
		} {
			t.Run(target+"/"+name, func(t *testing.T) {
				status, h, b := raw(t, p, target+" HTTP/1.1\r\nHost: service.test\r\nConnection: close\r\n"+header+"\r\n")
				if status != 407 || h.Get("Proxy-Authenticate") == "" || b != "PROXY_AUTH_REQUIRED\n" {
					t.Fatal("incorrect authentication rejection")
				}
				if h.Get("Access-Control-Allow-Origin") != "" {
					t.Fatal("proxy enabled CORS")
				}
			})
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unauthenticated request dialed")
	}
}

func TestInvalidRequestsBeforeDial(t *testing.T) {
	var calls atomic.Int32
	p := startProxy(t, func(context.Context, string, string) (net.Conn, error) {
		calls.Add(1)
		return nil, errors.New("secret")
	})
	for name, request := range map[string]string{
		"origin-form": "GET /localapi/v0/status", "asterisk": "OPTIONS *",
		"https-absolute": "GET https://service.test/path", "url-userinfo": "GET http://user:pass@service.test/path",
		"fragment": "GET http://service.test/path#fragment", "invalid-port": "GET http://service.test:0/path",
		"connect-path": "CONNECT service.test:443/path", "connect-query": "CONNECT service.test:443?q=x",
		"connect-fragment": "CONNECT service.test:443#fragment", "connect-userinfo": "CONNECT user:pass@service.test:443",
		"connect-no-port": "CONNECT service.test", "connect-zero": "CONNECT service.test:0",
		"connect-big-port": "CONNECT service.test:65536", "connect-scheme": "CONNECT https://service.test:443",
	} {
		t.Run(name, func(t *testing.T) {
			status, _, _ := raw(t, p, request+" HTTP/1.1\r\nHost: service.test\r\nProxy-Authorization: "+auth(p)+"\r\nConnection: close\r\n\r\n")
			if status != 400 {
				t.Fatalf("status=%d", status)
			}
		})
	}
	for _, header := range []string{"Origin: https://evil.test", "Origin:", "Sec-Fetch-Site: cross-site", "Upgrade: websocket", "Connection: Upgrade"} {
		for _, request := range []string{"GET http://service.test/path", "CONNECT service.test:443"} {
			status, _, _ := raw(t, p, request+" HTTP/1.1\r\nHost: service.test\r\nProxy-Authorization: "+auth(p)+"\r\nConnection: close\r\n"+header+"\r\n\r\n")
			if status != 400 {
				t.Fatalf("browser/upgrade accepted: status=%d", status)
			}
		}
	}
	status, _, _ := raw(t, p, "CONNECT service.test:443 HTTP/1.1\r\nHost: service.test:443\r\nProxy-Authorization: "+auth(p)+"\r\nConnection: close\r\nContent-Length: 1\r\n\r\nx")
	if status != 400 {
		t.Fatal("CONNECT body accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("invalid request dialed")
	}
}

func TestAuthorityValidation(t *testing.T) {
	for _, v := range []string{"service.test:80", "service:443", "100.100.10.1:8080", "8.8.8.8:80", "127.0.0.1:80", "[fd7a:115c:a1e0::1]:443", "[::1]:1"} {
		if !validAuthority(v, true) {
			t.Errorf("valid authority rejected: %s", v)
		}
	}
	for _, v := range []string{"service.test", "localhost", "[::1]"} {
		if !validAuthority(v, false) {
			t.Errorf("HTTP authority rejected: %s", v)
		}
	}
	for _, v := range []string{"", ":80", "service:", "service:-1", "service:65536", "service:0", "user@service:80", "service:80/path", "service:80?x", "service:80#x", "service\\name:80", "service%00:80", "[fe80::1%lo0]:80", "[127.0.0.1]:80", "[bad]:80", "service\n:80", "service :80", "::1:80"} {
		if validAuthority(v, true) || validAuthority(v, false) {
			t.Errorf("invalid authority accepted: %q", v)
		}
	}
}

func TestHTTPForwardingAndCredentialStripping(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "service.test:8080" || r.Method != "POST" || r.RequestURI != "/a%2Fb?q=x%2Fy&raw=1;2" {
			t.Error("request target/method changed")
		}
		if r.Header.Get("Authorization") != "Bearer application-secret" || r.Header.Get("Cookie") != "session=synthetic" {
			t.Error("application credentials changed")
		}
		for _, key := range []string{"Proxy-Authorization", "Proxy-Connection", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Custom", "X-Real-IP", "X-Hop"} {
			if r.Header.Get(key) != "" {
				t.Errorf("upstream received %s", key)
			}
		}
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if len(r.Trailer) != 0 {
			t.Error("request trailers forwarded")
		}
		w.Header().Set("Proxy-Authorization", "response-secret")
		w.Header().Set("Proxy-Authenticate", "Basic secret")
		w.Header().Set("Forwarded", "secret")
		w.Header().Set("X-Forwarded-For", "secret")
		w.Header().Set("Trailer", "Proxy-Authorization, X-Application-Trailer")
		w.Header().Set("X-Application", "preserved")
		w.WriteHeader(429)
		w.Write(b)
		w.Header().Set("Proxy-Authorization", "trailer-secret")
		w.Header().Set("X-Application-Trailer", "trailer")
	}))
	defer upstream.Close()
	var calls atomic.Int32
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	p := startProxy(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		calls.Add(1)
		if network != "tcp" || address != "service.test:8080" {
			t.Error("dial destination changed")
		}
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	})
	body := strings.Repeat("large-streamed-data", 100000)
	r, _ := http.NewRequest("POST", "http://service.test:8080/a%2Fb?q=x%2Fy&raw=1;2", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer application-secret")
	r.Header.Set("Cookie", "session=synthetic")
	for _, key := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Custom", "X-Real-IP", "X-Hop"} {
		r.Header.Set(key, "caller-controlled")
	}
	r.Header.Set("Connection", "X-Hop")
	r.Header.Set("Proxy-Connection", "keep-alive")
	r.Trailer = http.Header{"Proxy-Authorization": []string{"trailer-secret"}, "X-Other": []string{"trailer"}}
	r.ContentLength = -1
	response, err := client(t, p).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	b, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 429 || string(b) != body || response.Header.Get("X-Application") != "preserved" {
		t.Fatal("application response changed")
	}
	for _, key := range []string{"Proxy-Authorization", "Proxy-Authenticate", "Forwarded", "X-Forwarded-For"} {
		if response.Header.Get(key) != "" || response.Trailer.Get(key) != "" {
			t.Errorf("response leaked %s", key)
		}
	}
	if calls.Load() != 1 || p.transport.Proxy != nil || !p.transport.DisableKeepAlives {
		t.Fatal("unexpected routing/replay configuration")
	}
}

func TestNoRedirectFollowingOrRequestReplay(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://elsewhere.test/secret")
		w.WriteHeader(302)
	}))
	defer upstream.Close()
	p := startProxy(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		calls.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	})
	response, err := client(t, p).Get("http://service.test/path")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 302 || response.Header.Get("Location") != "http://elsewhere.test/secret" || calls.Load() != 1 {
		t.Fatal("helper followed or changed redirect")
	}
	for _, method := range []string{"GET", "POST"} {
		t.Run(method, func(t *testing.T) {
			var attempts atomic.Int32
			p := startProxy(t, func(context.Context, string, string) (net.Conn, error) {
				attempts.Add(1)
				a, b := net.Pipe()
				b.Close()
				return a, nil
			})
			r, _ := http.NewRequest(method, "http://service.test/path", strings.NewReader("synthetic-body"))
			r.Header.Set("Idempotency-Key", "synthetic-key")
			resp, err := client(t, p).Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != 502 || string(body) != "PROXY_ERROR\n" || attempts.Load() != 1 {
				t.Fatal("request replayed or failure unsanitized")
			}
		})
	}
}

func TestHTTPStreamingAndCancellation(t *testing.T) {
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
	p := startProxy(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	})
	c := client(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", "http://service.test/stream", nil)
	resp, err := c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatal("SSE buffered")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			r, err := c.Get("http://service.test/other")
			if err != nil {
				t.Error(err)
				return
			}
			defer r.Body.Close()
			b, _ := io.ReadAll(r.Body)
			if string(b) != "concurrent" {
				t.Error("concurrent request failed")
			}
		})
	}
	wg.Wait()
	cancel()
	resp.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("client cancellation not propagated")
	}
}

func TestHTTPStreamingUpload(t *testing.T) {
	first := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 5)
		if _, err := io.ReadFull(r.Body, b); err != nil || string(b) != "first" {
			t.Error("missing prefix")
		}
		close(first)
		b, err := io.ReadAll(r.Body)
		if err != nil || string(b) != "last" {
			t.Error("missing suffix")
		}
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	p := startProxy(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	})
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	done := make(chan error, 1)
	c := client(t, p)
	go func() {
		resp, err := c.Post("http://service.test/upload", "application/octet-stream", r)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		done <- err
	}()
	io.WriteString(w, "first")
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("upload buffered")
	}
	io.WriteString(w, "last")
	w.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func tunnel(t *testing.T, p *Server, target, initial string) (net.Conn, *bufio.Reader) {
	t.Helper()
	c, err := net.DialTimeout("tcp", address(p), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n%s", target, target, auth(p), initial)
	reader := bufio.NewReader(c)
	resp, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil || resp.StatusCode != 200 {
		c.Close()
		t.Fatal("CONNECT failed")
	}
	return c, reader
}

func TestConnectBufferedBytesAndClose(t *testing.T) {
	remoteDone := make(chan struct{})
	p := startProxy(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "service.test:443" || network != "tcp" {
			t.Error("CONNECT authority changed")
		}
		a, b := net.Pipe()
		go func() { defer b.Close(); defer close(remoteDone); io.Copy(b, b) }()
		return a, nil
	})
	c, r := tunnel(t, p, "service.test:443", "initial-tunnel-bytes")
	defer c.Close()
	b := make([]byte, len("initial-tunnel-bytes"))
	if _, err := io.ReadFull(r, b); err != nil || string(b) != "initial-tunnel-bytes" {
		t.Fatal("buffered CONNECT bytes lost")
	}
	io.WriteString(c, "second")
	b = make([]byte, 6)
	if _, err := io.ReadFull(r, b); err != nil || string(b) != "second" {
		t.Fatal("bidirectional tunnel failed")
	}
	p.Close()
	p.Close()
	c.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := r.ReadByte(); err == nil {
		t.Fatal("hijacked connection survived close")
	}
	select {
	case <-remoteDone:
	case <-time.After(time.Second):
		t.Fatal("upstream CONNECT survived close")
	}
	if c, err := net.DialTimeout("tcp", address(p), time.Second); err == nil {
		c.Close()
		t.Fatal("closed listener accepted")
	}
	select {
	case <-p.Done():
	case <-time.After(time.Second):
		t.Fatal("HTTP server did not exit")
	}
}

func TestHTTPSOpaqueTunnelAndOriginalTLSIdentity(t *testing.T) {
	sni := make(chan string, 8)
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer application-secret" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("TLS application credentials changed")
		}
		io.WriteString(w, "secure")
	}))
	upstream.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) { sni <- hello.ServerName; return nil, nil }}
	upstream.Config.ErrorLog = log.New(io.Discard, "", 0)
	upstream.StartTLS()
	defer upstream.Close()
	name := upstream.Certificate().DNSNames[0]
	_, port, _ := net.SplitHostPort(upstream.Listener.Addr().String())
	p := startProxy(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != net.JoinHostPort(name, port) {
			t.Error("TLS target changed")
		}
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	})
	c := client(t, p)
	target := "https://" + net.JoinHostPort(name, port) + "/path"
	if resp, err := c.Get(target); err == nil {
		resp.Body.Close()
		t.Fatal("untrusted certificate accepted")
	}
	tr := c.Transport.(*http.Transport)
	tr.TLSClientConfig = upstream.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	req, _ := http.NewRequest("GET", target, nil)
	req.Header.Set("Authorization", "Bearer application-secret")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(b) != "secure" {
		t.Fatal("TLS tunnel changed response")
	}
	for range 2 {
		if got := <-sni; got != name {
			t.Fatal("original SNI lost")
		}
	}
	tr.TLSClientConfig = tr.TLSClientConfig.Clone()
	tr.TLSClientConfig.ServerName = "wrong.test"
	if resp, err := c.Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("hostname mismatch accepted")
	}
}

func TestFailureSanitizationAndDialCancellation(t *testing.T) {
	p := startProxy(t, func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("raw-secret-destination")
	})
	status, _, body := raw(t, p, "CONNECT service.test:443 HTTP/1.1\r\nHost: service.test:443\r\nProxy-Authorization: "+auth(p)+"\r\nConnection: close\r\n\r\n")
	if status != 502 || strings.Contains(body, "secret") {
		t.Fatal("CONNECT error leaked")
	}
	p2 := startProxy(t, func(context.Context, string, string) (net.Conn, error) { return nil, context.DeadlineExceeded })
	resp, err := client(t, p2).Get("http://service.test")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 504 {
		t.Fatal("HTTP dial timeout not mapped")
	}
	for _, mode := range []string{"disconnect", "close"} {
		t.Run(mode, func(t *testing.T) {
			started, done := make(chan struct{}), make(chan struct{})
			p := startProxy(t, func(ctx context.Context, _, _ string) (net.Conn, error) {
				close(started)
				<-ctx.Done()
				close(done)
				return nil, ctx.Err()
			})
			c, err := net.DialTimeout("tcp", address(p), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			fmt.Fprintf(c, "CONNECT service.test:443 HTTP/1.1\r\nHost: service.test:443\r\nProxy-Authorization: %s\r\n\r\n", auth(p))
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("dial not started")
			}
			if mode == "disconnect" {
				c.Close()
			} else {
				p.Close()
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("dial not cancelled")
			}
		})
	}
}

func TestStartAndIndependentCredentials(t *testing.T) {
	if p, err := Start(context.Background(), nil); err == nil {
		p.Close()
		t.Fatal("nil dialer accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p, err := Start(ctx, func(context.Context, string, string) (net.Conn, error) { return nil, nil }); err == nil {
		p.Close()
		t.Fatal("cancelled startup accepted")
	}
	dial := func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("not used") }
	a, b := startProxy(t, dial), startProxy(t, dial)
	if !a.Proxy().Valid() || !b.Proxy().Valid() || a.Proxy().Password == b.Proxy().Password || a.Proxy().Port == b.Proxy().Port {
		t.Fatal("instances are not independent")
	}
	status, _, _ := raw(t, a, "GET http://service.test HTTP/1.1\r\nHost: service.test\r\nProxy-Authorization: "+auth(b)+"\r\nConnection: close\r\n\r\n")
	if status != 407 {
		t.Fatal("another instance's credentials accepted")
	}
}

func TestAbsoluteAuthorityAndAuthenticationOnReusedConnection(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "service.test:8080" || r.RequestURI != "/path" {
			t.Error("absolute authority not preserved")
		}
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	p := startProxy(t, func(ctx context.Context, network, destination string) (net.Conn, error) {
		calls.Add(1)
		if destination != "service.test:8080" {
			t.Error("caller Host changed dial destination")
		}
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	})
	c, err := net.DialTimeout("tcp", address(p), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	r := bufio.NewReader(c)
	fmt.Fprintf(c, "GET http://service.test:8080/path HTTP/1.1\r\nHost: caller-supplied.test\r\nProxy-Authorization: %s\r\n\r\n", auth(p))
	response, err := http.ReadResponse(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(body) != "ok" {
		t.Fatal("first proxy request failed")
	}
	io.WriteString(c, "GET http://service.test:8080/path HTTP/1.1\r\nHost: service.test:8080\r\nConnection: close\r\n\r\n")
	response, err = http.ReadResponse(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 407 || calls.Load() != 1 {
		t.Fatal("connection reuse bypassed proxy authentication")
	}
}

func TestHTTPDialDisconnectAndLateCompletion(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(strconv.FormatBool(late), func(t *testing.T) {
			started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			remoteClosed := make(chan struct{})
			p := startProxy(t, func(ctx context.Context, _, _ string) (net.Conn, error) {
				close(started)
				<-ctx.Done()
				close(cancelled)
				if !late {
					return nil, ctx.Err()
				}
				<-release
				a, b := net.Pipe()
				go func() { defer b.Close(); io.Copy(io.Discard, b); close(remoteClosed) }()
				return a, nil
			})
			c, err := net.DialTimeout("tcp", address(p), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			fmt.Fprintf(c, "GET http://service.test/path HTTP/1.1\r\nHost: service.test\r\nProxy-Authorization: %s\r\n\r\n", auth(p))
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("HTTP dial not started")
			}
			if late {
				p.Close()
			} else {
				c.Close()
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("HTTP dial cancellation detached")
			}
			if late {
				close(release)
				select {
				case <-remoteClosed:
				case <-time.After(time.Second):
					t.Fatal("late connection survived proxy close")
				}
			}
		})
	}
}

func TestCloseActiveHTTPStream(t *testing.T) {
	remoteDone := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(remoteDone)
	}))
	defer upstream.Close()
	p := startProxy(t, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	})
	r, err := client(t, p).Get("http://service.test/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	reader := bufio.NewReader(r.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != "first\n" {
		t.Fatal("stream never started")
	}
	p.Close()
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("HTTP stream survived close")
	}
	select {
	case <-remoteDone:
	case <-time.After(time.Second):
		t.Fatal("upstream HTTP stream survived close")
	}
}

func TestResponseConnectionNominatedHeaders(t *testing.T) {
	p := startProxy(t, func(context.Context, string, string) (net.Conn, error) {
		a, b := net.Pipe()
		go func() {
			defer b.Close()
			r, err := http.ReadRequest(bufio.NewReader(b))
			if err != nil {
				return
			}
			r.Body.Close()
			// Use a wire fixture: http.Server rewrites Connection when the
			// client's transport requests close, obscuring the nominated name.
			io.WriteString(b, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: X-Response-Hop\r\nX-Response-Hop: secret\r\nX-End-To-End: preserved\r\n\r\nok")
		}()
		return a, nil
	})
	r, err := client(t, p).Get("http://service.test/path")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil || string(b) != "ok" || r.Header.Get("X-Response-Hop") != "" || r.Header.Get("X-End-To-End") != "preserved" {
		t.Fatal("response hop removal failed")
	}
}

func TestGoConnectionCloseMetadataLimitation(t *testing.T) {
	// Evidence for the documented Go 1.26 limitation, not a proxy guarantee.
	r, err := http.ReadResponse(bufio.NewReader(strings.NewReader("HTTP/1.1 200 OK\r\nConnection: X-Nominated, close\r\nX-Nominated: value\r\nContent-Length: 0\r\n\r\n")), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.Header.Get("Connection") != "" || r.Header.Get("X-Nominated") == "" {
		t.Fatal("Go changed response metadata handling; revisit the documented limitation")
	}
	t.Log("Go removes Connection: close before proxy hooks, including other nominated names")
}

func TestConnectionCloseResponseFilteringEndToEnd(t *testing.T) {
	for _, closeToken := range []bool{false, true} {
		t.Run(strconv.FormatBool(closeToken), func(t *testing.T) {
			connectionHeader, wantNominated := "X-Nominated", ""
			if closeToken {
				connectionHeader, wantNominated = "close, X-Nominated", "hop-value"
			}
			p := startProxy(t, func(context.Context, string, string) (net.Conn, error) {
				a, b := net.Pipe()
				go func() {
					defer b.Close()
					request, err := http.ReadRequest(bufio.NewReader(b))
					if err != nil {
						return
					}
					request.Body.Close()
					if request.Header.Get("Proxy-Authorization") != "" {
						t.Error("upstream received local proxy credentials")
					}
					// Raw responses preserve the exact Connection metadata on the
					// wire; net/http remains responsible for parsing it in the proxy.
					headers := "Connection: " + connectionHeader + "\r\nX-Nominated: hop-value\r\nProxy-Authorization: Basic dHNuZXQ6c3ludGhldGlj\r\n"
					io.WriteString(b, "HTTP/1.1 103 Early Hints\r\n"+headers+"Link: </style.css>; rel=preload\r\n\r\n"+
						"HTTP/1.1 200 OK\r\n"+headers+"Content-Length: 2\r\nX-End-To-End: preserved\r\n\r\nok")
				}()
				return a, nil
			})
			var hints atomic.Int32
			ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
				Got1xxResponse: func(code int, h textproto.MIMEHeader) error {
					if code != 103 || h.Get("Link") == "" || h.Get("X-Nominated") != wantNominated {
						t.Error("unexpected informational response metadata")
					}
					if h.Get("Proxy-Authorization") != "" || h.Get("Connection") != "" {
						t.Error("informational response leaked proxy authentication or Connection")
					}
					hints.Add(1)
					return nil
				},
			})
			request, _ := http.NewRequestWithContext(ctx, "GET", "http://service.test/path", nil)
			response, err := client(t, p).Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != 200 || string(body) != "ok" || response.Header.Get("X-End-To-End") != "preserved" {
				t.Fatal("application response changed")
			}
			if response.Header.Get("X-Nominated") != wantNominated {
				t.Fatal("unexpected handling of Connection-nominated response header")
			}
			if response.Header.Get("Proxy-Authorization") != "" || response.Header.Get("Connection") != "" {
				t.Fatal("final response leaked proxy authentication or Connection")
			}
			if hints.Load() != 1 {
				t.Fatal("informational response was not forwarded")
			}
		})
	}
}

func TestInformationalResponseHeaderFiltering(t *testing.T) {
	p := startProxy(t, func(context.Context, string, string) (net.Conn, error) {
		a, b := net.Pipe()
		go func() {
			defer b.Close()
			r, err := http.ReadRequest(bufio.NewReader(b))
			if err != nil {
				return
			}
			r.Body.Close()
			io.WriteString(b, "HTTP/1.1 103 Early Hints\r\nProxy-Authorization: secret\r\nConnection: X-Hop\r\nX-Hop: secret\r\nX-Forwarded-For: secret\r\nLink: </style.css>; rel=preload\r\n\r\nHTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
		}()
		return a, nil
	})
	var hints atomic.Int32
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{Got1xxResponse: func(code int, h textproto.MIMEHeader) error {
		if code != 103 || h.Get("Link") == "" || h.Get("Proxy-Authorization") != "" || h.Get("X-Hop") != "" || h.Get("X-Forwarded-For") != "" {
			t.Error("informational headers were not filtered")
		}
		hints.Add(1)
		return nil
	}})
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://service.test/path", nil)
	r, err := client(t, p).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	io.Copy(io.Discard, r.Body)
	if hints.Load() != 1 {
		t.Fatal("informational response not forwarded")
	}
}
