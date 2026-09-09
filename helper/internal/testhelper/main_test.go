package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/protocol"
	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/state"
	"golang.org/x/net/proxy"
)

func buildHelper(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "bridge-test-helper")
	build := exec.Command("go", "build", "-tags=ts_omit_webclient", "-o", binary, ".")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	return binary
}

func TestExecutableSOCKSAndDisposal(t *testing.T) {
	binary := buildHelper(t)
	for _, end := range []string{"close", "eof", "sigterm", "sigint", "sighup"} {
		t.Run(end, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary)
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				stdin.Close()
				if cmd.ProcessState == nil {
					cmd.Process.Kill()
					cmd.Wait()
				}
			})
			o := protocol.Options{Hostname: "test-bridge", StateDir: t.TempDir(), StartupTimeoutMS: 1000}
			if err := os.Chmod(o.StateDir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := json.NewEncoder(stdin).Encode(protocol.Command{Type: "start", Version: 1, Options: &o}); err != nil {
				t.Fatal(err)
			}
			reader := bufio.NewReader(stdout)
			line, err := reader.ReadBytes('\n')
			if err != nil {
				t.Fatal(err)
			}
			var e protocol.Event
			if err := json.Unmarshal(line, &e); err != nil {
				t.Fatal(err)
			}
			if e.Type != "ready" || e.Proxy == nil || !e.Proxy.Valid() || e.HTTPProxy == nil || !e.HTTPProxy.Valid() {
				t.Fatal("invalid executable readiness")
			}
			if e.Proxy.Password == e.HTTPProxy.Password || e.Proxy.Port == e.HTTPProxy.Port {
				t.Fatal("proxy endpoints are not independent")
			}
			upstream, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer upstream.Close()
			remoteDone := make(chan struct{})
			go func() {
				c, err := upstream.Accept()
				if err == nil {
					defer c.Close()
					io.WriteString(c, "hello")
					io.Copy(io.Discard, c)
				}
				close(remoteDone)
			}()
			p := e.Proxy
			dial, err := proxy.SOCKS5("tcp", net.JoinHostPort(p.Host, strconv.Itoa(p.Port)), &proxy.Auth{User: p.Username, Password: p.Password}, &net.Dialer{Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			_, port, _ := net.SplitHostPort(upstream.Addr().String())
			tunnel, err := dial.(proxy.ContextDialer).DialContext(ctx, "tcp", "upstream.test:"+port)
			if err != nil {
				t.Fatal(err)
			}
			defer tunnel.Close()
			var greeting [5]byte
			if _, err := io.ReadFull(tunnel, greeting[:]); err != nil || string(greeting[:]) != "hello" {
				t.Fatal("real executable SOCKS tunnel failed")
			}
			// Keep a separate HTTP CONNECT tunnel open across each disposal mode.
			httpUpstream, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer httpUpstream.Close()
			httpRemoteDone := make(chan struct{})
			go func() {
				c, err := httpUpstream.Accept()
				if err == nil {
					defer c.Close()
					io.Copy(io.Discard, c)
				}
				close(httpRemoteDone)
			}()
			httpAddress := net.JoinHostPort(e.HTTPProxy.Host, strconv.Itoa(e.HTTPProxy.Port))
			httpTunnel, err := net.DialTimeout("tcp", httpAddress, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer httpTunnel.Close()
			httpTunnel.SetDeadline(time.Now().Add(3 * time.Second))
			_, httpPort, _ := net.SplitHostPort(httpUpstream.Addr().String())
			req, _ := http.NewRequest("CONNECT", "http://upstream.test:"+httpPort, nil)
			req.SetBasicAuth(e.HTTPProxy.Username, e.HTTPProxy.Password)
			fmt.Fprintf(httpTunnel, "CONNECT upstream.test:%s HTTP/1.1\r\nHost: upstream.test:%s\r\nProxy-Authorization: %s\r\n\r\n", httpPort, httpPort, req.Header.Get("Authorization"))
			httpReader := bufio.NewReader(httpTunnel)
			response, err := http.ReadResponse(httpReader, &http.Request{Method: "CONNECT"})
			if err != nil || response.StatusCode != 200 {
				t.Fatal("real executable CONNECT tunnel failed")
			}
			switch end {
			case "close":
				fmt.Fprintln(stdin, `{"type":"close","version":1}`)
			case "eof":
				stdin.Close()
			case "sigterm":
				cmd.Process.Signal(syscall.SIGTERM)
			case "sigint":
				cmd.Process.Signal(syscall.SIGINT)
			case "sighup":
				cmd.Process.Signal(syscall.SIGHUP)
			}
			// Drain protocol before Wait closes the pipe. There is no close frame.
			remaining, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
			if len(remaining) != 0 || stderr.Len() != 0 {
				t.Fatal("unexpected shutdown output")
			}
			tunnel.SetReadDeadline(time.Now().Add(time.Second))
			var b [1]byte
			if _, err := tunnel.Read(b[:]); err == nil {
				t.Fatal("tunnel survived helper exit")
			}
			select {
			case <-remoteDone:
			case <-time.After(time.Second):
				t.Fatal("upstream survived helper exit")
			}
			if _, err := httpReader.ReadByte(); err == nil {
				t.Fatal("HTTP tunnel survived helper exit")
			}
			select {
			case <-httpRemoteDone:
			case <-time.After(time.Second):
				t.Fatal("HTTP upstream survived helper exit")
			}
			if c, err := net.DialTimeout("tcp", httpAddress, time.Second); err == nil {
				c.Close()
				t.Fatal("HTTP listener survived helper exit")
			}
			s, err := state.Open(o)
			if err != nil {
				t.Fatalf("state lock survived exit: %v", err)
			}
			s.Close()
		})
	}
}

func TestNativeBunHTTPAndHTTPS(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("Bun not installed; Go proxy tests still run")
	}
	version, err := exec.Command(bun, "--version").Output()
	if err != nil || strings.TrimSpace(string(version)) != "1.4.2" {
		t.Skip("native integration requires Bun 1.4.2")
	}
	binary := buildHelper(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: first\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		serverName := ""
		if r.TLS != nil {
			serverName = r.TLS.ServerName
		}
		json.NewEncoder(w).Encode(map[string]string{"host": r.Host, "path": r.RequestURI, "method": r.Method, "authorization": r.Header.Get("Authorization"), "proxyAuthorization": r.Header.Get("Proxy-Authorization"), "body": string(body), "serverName": serverName})
	})
	plain := httptest.NewServer(handler)
	defer plain.Close()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"upstream.test"}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	secure := httptest.NewUnstartedServer(handler)
	secure.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	secure.Config.ErrorLog = log.New(io.Discard, "", 0)
	secure.StartTLS()
	defer secure.Close()
	_, plainPort, _ := net.SplitHostPort(plain.Listener.Addr().String())
	_, securePort, _ := net.SplitHostPort(secure.Listener.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "testdata/native-fetch.ts", binary, plainPort, securePort)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "TS_") || strings.HasPrefix(upper, "TSNET_") || strings.HasPrefix(upper, "TAILSCALE_") || strings.HasSuffix(upper, "_PROXY") {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "BRIDGE_TEST_CA="+string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
	if _, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native Bun proxy checks failed: %v (output omitted to protect proxy credentials)", err)
	}
}
