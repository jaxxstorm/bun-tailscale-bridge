package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

const startFrame = `{"type":"start","version":1,"options":{"hostname":"test-bridge","ephemeral":true,"startupTimeoutMs":60000,"interactive":false}}`

func TestSharedFixtures(t *testing.T) {
	b, err := os.ReadFile("../../../fixtures/protocol.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Version                int `json:"version"`
		MaxFrameBytes          int `json:"maxFrameBytes"`
		Valid, Invalid, Events []json.RawMessage
	}
	if err := json.Unmarshal(b, &fixtures); err != nil {
		t.Fatal(err)
	}
	if fixtures.Version != Version || fixtures.MaxFrameBytes != MaxFrameBytes {
		t.Fatal("fixture constants differ")
	}
	for _, frame := range fixtures.Valid {
		if _, err := Parse(frame); err != nil {
			t.Errorf("valid fixture rejected: %v", err)
		}
	}
	for _, frame := range fixtures.Invalid {
		if _, err := Parse(frame); err == nil {
			t.Error("invalid fixture accepted")
		}
	}
	for _, frame := range fixtures.Events {
		var event Event
		if err := json.Unmarshal(frame, &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "ready" && event.HTTPProxy == nil {
			t.Fatal("shared ready fixture must include the new httpProxy descriptor")
		}
		var out bytes.Buffer
		if err := Write(&out, event); err != nil {
			t.Fatal(err)
		}
		var got, want any
		_ = json.Unmarshal(frame, &want)
		_ = json.Unmarshal(out.Bytes(), &got)
		wantJSON, _ := json.Marshal(want)
		gotJSON, _ := json.Marshal(got)
		if !bytes.Equal(wantJSON, gotJSON) {
			t.Fatal("event differs from shared fixture")
		}
	}
}

func TestFrames(t *testing.T) {
	for name, frame := range map[string]string{
		"empty": "", "null": "null", "array": "[]", "truncated": `{"type":`,
		"trailing-json": startFrame + `{}`, "version": strings.Replace(startFrame, `"version":1`, `"version":2`, 1),
		"duplicate":        strings.Replace(startFrame, `"version":1`, `"version":1,"version":1`, 1),
		"nested-duplicate": strings.Replace(startFrame, `"ephemeral":true`, `"ephemeral":true,"ephemeral":false`, 1),
		"case":             strings.Replace(startFrame, `"hostname"`, `"Hostname"`, 1),
		"unknown":          strings.Replace(startFrame, `"hostname"`, `"other"`, 1),
		"missing-bool":     strings.Replace(startFrame, `,"interactive":false`, "", 1),
		"null-bool":        strings.Replace(startFrame, `"interactive":false`, `"interactive":null`, 1),
		"wrong-bool":       strings.Replace(startFrame, `"interactive":false`, `"interactive":"false"`, 1),
		"null-options":     `{"type":"start","version":1,"options":null}`,
		"extra-close":      `{"type":"close","version":1,"options":{}}`,
		"null-close":       `{"type":"close","version":1,"options":null}`,
		"unknown-type":     `{"type":"ping","version":1}`,
		"float-timeout":    strings.Replace(startFrame, "60000", "1.5", 1),
		"utf8":             startFrame + string([]byte{0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(frame)); err != ProtocolError {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestFrameBoundAndEOF(t *testing.T) {
	frame := `{"type":"close","version":1}`
	for _, size := range []int{MaxFrameBytes - 1, MaxFrameBytes, MaxFrameBytes + 1} {
		line := frame + strings.Repeat(" ", size-len(frame)) + "\n"
		r := NewReader(strings.NewReader(line))
		_, err := r.Next()
		if size <= MaxFrameBytes && err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if size > MaxFrameBytes && err != ProtocolError {
			t.Fatal("oversized frame accepted")
		}
	}
	r := NewReader(strings.NewReader(startFrame + "\n" + frame + "\n"))
	for range 2 {
		if _, err := r.Next(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Next(); err != io.EOF {
		t.Fatalf("EOF = %v", err)
	}
	if _, err := NewReader(strings.NewReader(frame)).Next(); err != ProtocolError {
		t.Fatal("unterminated frame accepted")
	}
	if _, err := NewReader(strings.NewReader("\n")).Next(); err != ProtocolError {
		t.Fatal("blank frame accepted")
	}
	if _, err := NewReader(strings.NewReader(frame + "\r\n")).Next(); err != nil {
		t.Fatal("CRLF rejected")
	}
}

func TestOptions(t *testing.T) {
	valid := Options{Hostname: "test-bridge", Ephemeral: true, StartupTimeoutMS: 60000}
	for _, change := range []func(*Options){
		func(o *Options) { o.Hostname = "" }, func(o *Options) { o.Hostname = "-bad" },
		func(o *Options) { o.Hostname = "bad-" }, func(o *Options) { o.Hostname = "bad.name" },
		func(o *Options) { o.Hostname = strings.Repeat("a", 64) }, func(o *Options) { o.Hostname = "bad name" },
		func(o *Options) { o.StartupTimeoutMS = 0 }, func(o *Options) { o.StartupTimeoutMS = -1 },
		func(o *Options) { o.StartupTimeoutMS = 2147483648 }, func(o *Options) { o.StateDir = "/tmp/state" },
		func(o *Options) { o.Ephemeral = false }, func(o *Options) { o.Ephemeral = false; o.StateDir = "relative" },
		func(o *Options) { o.Ephemeral = false; o.StateDir = "/tmp/\x00state" },
		func(o *Options) { o.AuthKey = " secret" }, func(o *Options) { o.AuthKey = "secret\nkey" },
	} {
		o := valid
		change(&o)
		if err := o.Validate(); err != InvalidOptions {
			t.Errorf("invalid option accepted: %v", err)
		}
	}
	for _, change := range []func(*Options){
		func(o *Options) {}, func(o *Options) { o.Hostname = "A" },
		func(o *Options) { o.Hostname = strings.Repeat("a", 63) },
		func(o *Options) { o.StartupTimeoutMS = 2147483647 },
		func(o *Options) { o.Ephemeral = false; o.StateDir = "/private/state" },
		func(o *Options) { o.AuthKey = "synthetic-key"; o.Interactive = true },
	} {
		o := valid
		change(&o)
		if err := o.Validate(); err != nil {
			t.Errorf("valid option rejected: %v", err)
		}
	}
	for _, addition := range []string{`,"authKey":""`, `,"stateDir":""`, `,"stateDir":"/tmp/x"`} {
		frame := strings.TrimSuffix(startFrame, "}}") + addition + "}}"
		if _, err := Parse([]byte(frame)); err != InvalidOptions {
			t.Fatalf("present optional: %v", err)
		}
	}
}

func TestEventsAndSanitization(t *testing.T) {
	for _, raw := range []string{"http://login.test/a", "https:///a", "https://user:secret@login.test/a", "not a URL", "https://login.test/\n"} {
		if ValidAuthURL(raw) {
			t.Error("unsafe login URL accepted")
		}
	}
	if !ValidAuthURL("https://login.tailscale.com/a/synthetic") {
		t.Fatal("valid URL rejected")
	}
	if SafeCode(errors.New("synthetic-secret")) != HelperFailed || SafeCode(Code("synthetic-secret")) != HelperFailed {
		t.Fatal("raw error escaped")
	}
	proxy := Proxy{Host: "127.0.0.1", Port: 1234, Username: "tsnet", Password: strings.Repeat("a", 32)}
	for _, e := range []Event{
		{Type: "ready", Proxy: &proxy, URL: "https://secret.test"},
		{Type: "ready"}, {Type: "unknown"}, {Type: "error", Code: Code("secret")},
		{Type: "auth_required", URL: "https://login.test/" + strings.Repeat("x", MaxFrameBytes)},
	} {
		var out bytes.Buffer
		if Write(&out, e) != ProtocolError || out.Len() != 0 {
			t.Fatal("invalid event written")
		}
	}
	httpProxy := Proxy{Host: "127.0.0.1", Port: 1235, Username: "tsnet", Password: strings.Repeat("b", 32)}
	if err := Write(shortWriter{}, Event{Type: "ready", Proxy: &proxy, HTTPProxy: &httpProxy}); err != HelperFailed {
		t.Fatal("short write ignored")
	}
	for _, mutate := range []func(*Proxy){func(p *Proxy) { p.Host = "localhost" }, func(p *Proxy) { p.Port = 0 }, func(p *Proxy) { p.Port = 65536 }, func(p *Proxy) { p.Username = "other" }, func(p *Proxy) { p.Password = strings.Repeat("a", 64) }} {
		p := proxy
		mutate(&p)
		if p.Valid() {
			t.Fatal("invalid proxy accepted")
		}
	}
}

func TestDualProxyReadyShape(t *testing.T) {
	socks := Proxy{Host: "127.0.0.1", Port: 1234, Username: "tsnet", Password: strings.Repeat("a", 32)}
	httpProxy := Proxy{Host: "127.0.0.1", Port: 1235, Username: "tsnet", Password: strings.Repeat("b", 32)}
	var out bytes.Buffer
	if err := Write(&out, Event{Type: "ready", Proxy: &socks, HTTPProxy: &httpProxy}); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 4 || fields["proxy"] == nil || fields["httpProxy"] == nil {
		t.Fatal("incorrect ready schema")
	}
	invalid := httpProxy
	invalid.Password = "secret"
	samePort := httpProxy
	samePort.Port = socks.Port
	samePassword := httpProxy
	samePassword.Password = socks.Password
	for _, event := range []Event{
		{Type: "ready", Proxy: &socks}, {Type: "ready", HTTPProxy: &httpProxy},
		{Type: "ready", Proxy: &socks, HTTPProxy: &invalid},
		{Type: "ready", Proxy: &socks, HTTPProxy: &samePort},
		{Type: "ready", Proxy: &socks, HTTPProxy: &samePassword},
		{Type: "ready", Proxy: &socks, HTTPProxy: &socks},
		{Type: "auth_required", URL: "https://login.test/a", HTTPProxy: &httpProxy},
		{Type: "error", Code: HelperFailed, HTTPProxy: &httpProxy},
	} {
		out.Reset()
		if Write(&out, event) != ProtocolError || out.Len() != 0 {
			t.Fatal("invalid dual-proxy event accepted")
		}
	}
}

type shortWriter struct{}

func (shortWriter) Write(b []byte) (int, error) { return len(b) - 1, nil }

func FuzzParse(f *testing.F) {
	f.Add(startFrame)
	f.Add(`{"type":"close","version":1}`)
	f.Fuzz(func(t *testing.T, s string) {
		c, err := Parse([]byte(s))
		if err == nil && (c.Version != Version || (c.Type != "start" && c.Type != "close")) {
			t.Fatal("invalid successful command")
		}
	})
}
