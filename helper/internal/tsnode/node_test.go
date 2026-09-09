package tsnode

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/protocol"
	"tailscale.com/ipn"
)

func ptr[T any](v T) *T { return &v }

func TestStructuredEnrollment(t *testing.T) {
	login := "https://login.tailscale.com/a/synthetic"
	for _, tc := range []struct {
		name          string
		key           string
		interactive   bool
		notifications []ipn.Notify
		want          error
		urls          int
		up            bool
	}{
		{name: "persisted", notifications: []ipn.Notify{{State: ptr(ipn.Running)}}, up: true},
		{name: "key", key: "synthetic-secret", notifications: []ipn.Notify{{State: ptr(ipn.NeedsLogin)}, {BrowseToURL: &login}, {State: ptr(ipn.Running)}}, up: true},
		{name: "required", notifications: []ipn.Notify{{State: ptr(ipn.NeedsLogin)}}, want: protocol.AuthRequired},
		{name: "url-without-opt-in", notifications: []ipn.Notify{{BrowseToURL: &login}}, want: protocol.AuthRequired},
		{name: "interactive", interactive: true, notifications: []ipn.Notify{{State: ptr(ipn.NeedsLogin)}, {BrowseToURL: &login}, {BrowseToURL: &login}, {State: ptr(ipn.Running)}}, urls: 1, up: true},
		{name: "raw-error", notifications: []ipn.Notify{{ErrMessage: ptr("raw-secret")}}, want: protocol.AuthFailed},
		{name: "invalid-url", interactive: true, notifications: []ipn.Notify{{BrowseToURL: ptr("http://unsafe.test")}}, want: protocol.AuthFailed},
		{name: "watch-EOF", want: protocol.AuthFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i, urls := 0, 0
			up := false
			err := wait(context.Background(), protocol.Options{AuthKey: tc.key, Interactive: tc.interactive}, func() (ipn.Notify, error) {
				if i >= len(tc.notifications) {
					return ipn.Notify{}, io.EOF
				}
				n := tc.notifications[i]
				i++
				return n, nil
			}, func(context.Context) error { up = true; return nil }, func(u string) error {
				if u != login {
					t.Fatal("wrong auth URL")
				}
				urls++
				return nil
			})
			if err != tc.want || urls != tc.urls || up != tc.up {
				t.Fatalf("error=%v urls=%d up=%v", err, urls, up)
			}
		})
	}
}

func TestEnrollmentCancellationAndReadyFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	err := wait(ctx, protocol.Options{}, func() (ipn.Notify, error) { cancel(); return ipn.Notify{}, errors.New("secret") }, nil, nil)
	if err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
	err = wait(context.Background(), protocol.Options{}, func() (ipn.Notify, error) { return ipn.Notify{State: ptr(ipn.Running)}, nil }, func(context.Context) error { return errors.New("secret") }, nil)
	if err != protocol.AuthFailed {
		t.Fatalf("readiness failure: %v", err)
	}
	err = wait(context.Background(), protocol.Options{Interactive: true}, func() (ipn.Notify, error) { return ipn.Notify{BrowseToURL: ptr("https://login.test/a")}, nil }, nil, func(string) error { return protocol.Cancelled })
	if err != protocol.Cancelled {
		t.Fatalf("callback cancellation: %v", err)
	}
}

func TestExplicitServerOptions(t *testing.T) {
	o := protocol.Options{Hostname: "test-bridge", AuthKey: "synthetic-secret", Ephemeral: true}
	n := New(o, "/private/test-state").(*node)
	if n.server.Dir != "/private/test-state" || n.server.Hostname != o.Hostname || n.server.AuthKey != o.AuthKey || !n.server.Ephemeral || n.server.Tun != nil {
		t.Fatal("explicit options not applied")
	}
	if n.server.ClientSecret != "" || n.server.ClientID != "" || n.server.Logf == nil || n.server.UserLogf == nil {
		t.Fatal("unexpected credentials or loggers")
	}
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
}
