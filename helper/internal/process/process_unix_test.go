//go:build darwin || linux

package process

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/bridge"
	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/protocol"
	"tailscale.com/envknob"
)

func TestClearEnvironment(t *testing.T) {
	for _, key := range []string{"TS_AUTHKEY", "TS_AUTH_KEY", "TS_CLIENT_SECRET", "TS_CLIENT_ID", "TS_ID_TOKEN", "TS_AUDIENCE", "TSNET_FORCE_LOGIN", "TAILSCALE_TEST_SECRET"} {
		t.Setenv(key, "synthetic-secret")
	}
	t.Setenv("BRIDGE_KEEP", "keep")
	cached := envknob.RegisterString("TS_TEST_CACHED_SECRET")
	envknob.Setenv("TS_TEST_CACHED_SECRET", "synthetic-secret")
	clearEnvironment()
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "TS_") || strings.HasPrefix(entry, "TSNET_") || strings.HasPrefix(entry, "TAILSCALE_") {
			t.Fatal("ambient tailscale environment retained")
		}
	}
	if cached() != "" || os.Getenv("BRIDGE_KEEP") != "keep" {
		t.Fatal("cache or unrelated environment incorrect")
	}
}

type noisyNode struct{}

func (noisyNode) Up(context.Context, func(string) error) error {
	fmt.Fprintln(os.Stdout, "raw synthetic stdout secret")
	fmt.Fprintln(os.Stderr, "raw synthetic stderr secret")
	return protocol.AuthRequired
}
func (noisyNode) Loopback() (protocol.Proxy, error) { return protocol.Proxy{}, protocol.HelperFailed }
func (noisyNode) Dial(context.Context, string, string) (net.Conn, error) {
	return nil, protocol.HelperFailed
}
func (noisyNode) Close() error { return nil }

func TestProcessStdoutIsolation(t *testing.T) {
	if os.Getenv("BRIDGE_PROCESS_TEST") == "1" {
		Main(func(protocol.Options, string) bridge.Node {
			if os.Getenv("TS_AUTHKEY") != "" {
				return nil
			}
			return noisyNode{}
		})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessStdoutIsolation$")
	cmd.Env = append(os.Environ(), "BRIDGE_PROCESS_TEST=1", "TS_AUTHKEY=synthetic-ambient-secret")
	// Keep stdin open until the node reports failure; EOF otherwise wins startup.
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintln(in, `{"type":"start","version":1,"options":{"hostname":"test","ephemeral":true,"startupTimeoutMs":1000,"interactive":false}}`)
	if err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	in.Close()
	if err == nil {
		t.Fatal("expected auth-required exit")
	}
	if stdout.String() != "{\"type\":\"error\",\"version\":1,\"code\":\"AUTH_REQUIRED\"}\n" || stderr.Len() != 0 {
		t.Fatal("raw logs or invalid control output escaped")
	}
}
