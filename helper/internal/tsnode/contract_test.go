package tsnode

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Review the credential/routing wiring of the pinned dependency without
// starting tsnet or contacting a control server. This is source-contract
// evidence, not a live LocalAPI authentication or enrollment test.
func TestPinnedLoopbackCredentialContract(t *testing.T) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Version}}\n{{.Dir}}", "tailscale.com")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal("cannot locate pinned Tailscale source")
	}
	version, dir, ok := strings.Cut(strings.TrimSpace(string(out)), "\n")
	if !ok || version != "v1.96.5" {
		t.Fatal("review this contract before changing the Tailscale pin")
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(dir, "tsnet", "tsnet.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	expr := func(node ast.Node) string {
		var b bytes.Buffer
		if err := printer.Fprint(&b, fset, node); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	var loopback *ast.FuncDecl
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "Loopback" {
			loopback = fn
		}
	}
	if loopback == nil {
		t.Fatal("public Loopback API missing")
	}
	body := expr(loopback.Body)
	for _, expected := range []string{
		`net.Listen("tcp", "127.0.0.1:0")`,
		`crand.Read(proxyCred[:])`, `crand.Read(cred[:])`,
		`lah.RequiredPassword = s.localAPICred`,
		`return lbAddr.String(), s.proxyCred, s.localAPICred, nil`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("pinned Loopback contract changed: %s", expected)
		}
	}
	socksFound := false
	ast.Inspect(loopback.Body, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || expr(lit.Type) != "socks5.Server" {
			return true
		}
		socksFound = true
		fields := map[string]string{}
		for _, elt := range lit.Elts {
			kv := elt.(*ast.KeyValueExpr)
			fields[expr(kv.Key)] = expr(kv.Value)
		}
		if fields["Username"] != `"tsnet"` || fields["Password"] != "s.proxyCred" || fields["Dialer"] != "s.dialer.UserDial" {
			t.Error("built-in SOCKS credentials or routing changed")
		}
		return true
	})
	if !socksFound {
		t.Fatal("built-in SOCKS implementation missing")
	}
	// The adapter must discard the separate LocalAPI credential at the call.
	adapter, err := parser.ParseFile(fset, "node.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ignored := false
	ast.Inspect(adapter, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 || len(assign.Lhs) != 4 {
			return true
		}
		if expr(assign.Rhs[0]) == "n.server.Loopback()" {
			ignored = expr(assign.Lhs[2]) == "_"
		}
		return true
	})
	if !ignored {
		t.Fatal("adapter must discard the LocalAPI credential")
	}
}
