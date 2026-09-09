// Package protocol defines the private, versioned NDJSON control channel.
package protocol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const Version = 1
const MaxFrameBytes = 65536

type Code string

func (c Code) Error() string { return string(c) }

const (
	InvalidOptions Code = "INVALID_OPTIONS"
	ProtocolError  Code = "PROTOCOL_ERROR"
	AuthRequired   Code = "AUTH_REQUIRED"
	AuthFailed     Code = "AUTH_FAILED"
	StateUnsafe    Code = "STATE_UNSAFE"
	StateLocked    Code = "STATE_LOCKED"
	StartupTimeout Code = "STARTUP_TIMEOUT"
	Cancelled      Code = "CANCELLED"
	HelperFailed   Code = "HELPER_FAILED"
)

// SafeCode never serializes arbitrary error text, even from an injected node.
func SafeCode(err error) Code {
	var code Code
	if errors.As(err, &code) {
		switch code {
		case InvalidOptions, ProtocolError, AuthRequired, AuthFailed, StateUnsafe, StateLocked, StartupTimeout, Cancelled, HelperFailed:
			return code
		}
	}
	return HelperFailed
}

type Options struct {
	Hostname         string `json:"hostname"`
	StateDir         string `json:"stateDir,omitempty"`
	Ephemeral        bool   `json:"ephemeral"`
	AuthKey          string `json:"authKey,omitempty"`
	StartupTimeoutMS int64  `json:"startupTimeoutMs"`
	Interactive      bool   `json:"interactive"`
}

var hostnamePattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
var passwordPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func (o Options) Validate() error {
	if !hostnamePattern.MatchString(o.Hostname) || o.StartupTimeoutMS < 1 || o.StartupTimeoutMS > 2147483647 {
		return InvalidOptions
	}
	if o.Ephemeral {
		if o.StateDir != "" {
			return InvalidOptions
		}
	} else if !filepath.IsAbs(o.StateDir) || strings.ContainsRune(o.StateDir, 0) {
		return InvalidOptions
	}
	if strings.TrimSpace(o.AuthKey) != o.AuthKey || strings.ContainsAny(o.AuthKey, "\r\n\x00") {
		return InvalidOptions
	}
	return nil
}

type Command struct {
	Type    string   `json:"type"`
	Version int      `json:"version"`
	Options *Options `json:"options,omitempty"`
}

type Proxy struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (p Proxy) Valid() bool {
	return p.Host == "127.0.0.1" && p.Port > 0 && p.Port <= 65535 && p.Username == "tsnet" && passwordPattern.MatchString(p.Password)
}

type Event struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	Proxy     *Proxy `json:"proxy,omitempty"`
	HTTPProxy *Proxy `json:"httpProxy,omitempty"`
	URL       string `json:"url,omitempty"`
	Code      Code   `json:"code,omitempty"`
}

func ValidAuthURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && !strings.ContainsAny(raw, "\r\n\x00")
}

func Write(w io.Writer, e Event) error {
	e.Version = Version
	switch e.Type {
	case "ready":
		if e.Proxy == nil || !e.Proxy.Valid() || e.HTTPProxy == nil || !e.HTTPProxy.Valid() || e.URL != "" || e.Code != "" {
			return ProtocolError
		}
	case "auth_required":
		if !ValidAuthURL(e.URL) || e.Proxy != nil || e.HTTPProxy != nil || e.Code != "" {
			return ProtocolError
		}
	case "error":
		if e.Code == "" || SafeCode(e.Code) != e.Code || e.Proxy != nil || e.HTTPProxy != nil || e.URL != "" {
			return ProtocolError
		}
	default:
		return ProtocolError
	}
	b, err := json.Marshal(e)
	if err != nil || len(b) > MaxFrameBytes {
		return ProtocolError
	}
	b = append(b, '\n')
	n, err := w.Write(b)
	if err != nil || n != len(b) {
		return HelperFailed
	}
	return nil
}

type Reader struct{ r *bufio.Reader }

func NewReader(r io.Reader) *Reader {
	return &Reader{bufio.NewReaderSize(r, MaxFrameBytes+1)}
}

func (r *Reader) Next() (Command, error) {
	line, err := r.r.ReadSlice('\n')
	if err == io.EOF && len(line) == 0 {
		return Command{}, io.EOF
	}
	if err != nil {
		return Command{}, ProtocolError
	}
	return Parse(line[:len(line)-1])
}

func Parse(line []byte) (Command, error) {
	var c Command
	if len(line) > MaxFrameBytes || !utf8.Valid(line) || !json.Valid(line) {
		return c, ProtocolError
	}
	d := json.NewDecoder(bytes.NewReader(line))
	if err := uniqueKeys(d, 0); err != nil {
		return c, ProtocolError
	}
	if err := object(line, &c, []string{"type", "version"}, []string{"options"}); err != nil {
		return c, ProtocolError
	}
	if c.Version != Version {
		return c, ProtocolError
	}
	switch c.Type {
	case "close":
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(line, &fields)
		if _, ok := fields["options"]; ok {
			return c, ProtocolError
		}
	case "start":
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(line, &fields)
		var o Options
		if err := object(fields["options"], &o, []string{"hostname", "ephemeral", "startupTimeoutMs", "interactive"}, []string{"stateDir", "authKey"}); err != nil {
			return c, ProtocolError
		}
		// Present-but-empty optional strings are not equivalent to omission.
		var options map[string]json.RawMessage
		_ = json.Unmarshal(fields["options"], &options)
		if _, ok := options["stateDir"]; ok && (o.StateDir == "" || o.Ephemeral) {
			return c, InvalidOptions
		}
		if _, ok := options["authKey"]; ok && o.AuthKey == "" {
			return c, InvalidOptions
		}
		if err := o.Validate(); err != nil {
			return c, err
		}
		c.Options = &o
	default:
		return c, ProtocolError
	}
	return c, nil
}

func object(b []byte, dst any, required, optional []string) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil || fields == nil {
		return ProtocolError
	}
	allowed := make(map[string]bool, len(required)+len(optional))
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return ProtocolError
		}
		allowed[key] = true
	}
	for _, key := range optional {
		allowed[key] = true
	}
	for key, value := range fields {
		if !allowed[key] || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ProtocolError
		}
	}
	return json.Unmarshal(b, dst)
}

// encoding/json accepts duplicate keys; the private wire format does not.
func uniqueKeys(d *json.Decoder, depth int) error {
	if depth > 16 {
		return ProtocolError
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	switch t {
	case json.Delim('{'):
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return ProtocolError
			}
			seen[s] = true
			if err := uniqueKeys(d, depth+1); err != nil {
				return err
			}
		}
		_, err = d.Token()
	case json.Delim('['):
		for d.More() {
			if err := uniqueKeys(d, depth+1); err != nil {
				return err
			}
		}
		_, err = d.Token()
	}
	return err
}
