// TEST ONLY: installed-package verification, never shipped as a helper.
package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/bridge"
	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/process"
	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/protocol"
	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/testhelper/simnode"
)

type fixture struct {
	bridge.Node
	options protocol.Options
	dir     string
}

func (n *fixture) Up(ctx context.Context, auth func(string) error) error {
	if n.options.Hostname == "failure" {
		return protocol.AuthFailed
	}
	marker := filepath.Join(n.dir, "synthetic-authorized")
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		if err := auth("https://login.tailscale.com/a/synthetic-package-fixture"); err != nil {
			return err
		}
		if n.options.Hostname == "hang" {
			<-ctx.Done()
			return ctx.Err()
		}
		if err := os.WriteFile(marker, []byte("synthetic, not a Tailscale identity\n"), 0600); err != nil {
			return protocol.HelperFailed
		}
	}
	return n.Node.Up(ctx, auth)
}

func main() {
	process.Main(func(o protocol.Options, dir string) bridge.Node {
		return &fixture{Node: simnode.New(o, dir), options: o, dir: dir}
	})
}
