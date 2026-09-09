//go:build darwin || linux

// Package process establishes the private helper process boundary.
package process

import (
	"context"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"tailscale.com/envknob"

	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/bridge"
)

// Main must be called once, before starting a node or application goroutines.
func Main(factory bridge.Factory) {
	// Retain only the protocol writer. Third-party stdout/stderr, standard
	// logs, and runtime diagnostics must not become control frames or secrets.
	fd, err := unix.Dup(unix.Stdout)
	if err != nil {
		os.Exit(1)
	}
	unix.CloseOnExec(fd)
	output := os.NewFile(uintptr(fd), "control-output")
	quiet, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		os.Exit(1)
	}
	if unix.Dup2(int(quiet.Fd()), unix.Stdout) != nil || unix.Dup2(int(quiet.Fd()), unix.Stderr) != nil {
		os.Exit(1)
	}
	_ = quiet.Close()
	log.SetOutput(io.Discard)
	clearEnvironment()
	// Files created by upstream libraries also remain owner-only.
	unix.Umask(0077)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	code := bridge.Run(ctx, os.Stdin, output, factory)
	stop()
	_ = output.Close()
	os.Exit(code)
}

func clearEnvironment() {
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "TS_") || strings.HasPrefix(name, "TSNET_") || strings.HasPrefix(name, "TAILSCALE_") {
			// Also clear envknob's registered caches, not just os.Getenv.
			envknob.Setenv(name, "")
			_ = os.Unsetenv(name)
		}
	}
}
