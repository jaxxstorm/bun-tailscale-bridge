//go:build darwin || linux

package state

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/protocol"
)

func options(dir string) protocol.Options {
	return protocol.Options{Hostname: "test-bridge", StateDir: dir, StartupTimeoutMS: 1000}
}

func TestPersistentLockAndPreservation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "identity")
	s, err := Open(options(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := Open(options(dir)); err != protocol.StateLocked {
		t.Fatalf("second lock: %v", err)
	}
	identity := filepath.Join(dir, "tailscaled.state")
	if err := os.WriteFile(identity, []byte("synthetic identity"), 0600); err != nil {
		t.Fatal(err)
	}
	lockBefore, err := os.Stat(filepath.Join(dir, ".bridge.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(identity)
	if err != nil || string(b) != "synthetic identity" {
		t.Fatal("identity not preserved")
	}
	s2, err := Open(options(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	lockAfter, err := os.Stat(filepath.Join(dir, ".bridge.lock"))
	if err != nil || !os.SameFile(lockBefore, lockAfter) {
		t.Fatal("lock inode replaced")
	}
	fi, _ := os.Stat(dir)
	if fi.Mode().Perm() != 0700 {
		t.Fatal("directory is not private")
	}
}

func TestUnsafeState(t *testing.T) {
	for _, kind := range []string{"directory-permissions", "file-permissions", "file-symlink", "dir-symlink", "lock-symlink", "hardlink", "fifo", "file-as-dir", "writable-parent"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			dir := filepath.Join(base, "identity")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			external := filepath.Join(base, "external")
			if err := os.WriteFile(external, []byte("do not change"), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "directory-permissions":
				err = os.Chmod(dir, 0755)
			case "file-permissions":
				err = os.WriteFile(filepath.Join(dir, "tailscaled.state"), nil, 0644)
			case "file-symlink":
				err = os.Symlink(external, filepath.Join(dir, "tailscaled.state"))
			case "lock-symlink":
				err = os.Symlink(external, filepath.Join(dir, ".bridge.lock"))
			case "dir-symlink":
				link := filepath.Join(base, "link")
				err = os.Symlink(dir, link)
				dir = link + "/"
			case "hardlink":
				err = os.Link(external, filepath.Join(dir, "tailscaled.state"))
			case "fifo":
				err = syscall.Mkfifo(filepath.Join(dir, "fifo"), 0600)
			case "file-as-dir":
				dir = external
			case "writable-parent":
				err = os.Chmod(base, 0777)
			}
			if err != nil {
				t.Fatal(err)
			}
			if s, err := Open(options(dir)); err != protocol.StateUnsafe {
				if s != nil {
					s.Close()
				}
				t.Fatalf("unsafe state: %v", err)
			}
			b, err := os.ReadFile(external)
			if err != nil || string(b) != "do not change" {
				t.Fatal("caller file changed")
			}
		})
	}
}

func TestOwnershipValidation(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !safeInfo(fi) {
		t.Fatal("ownership test fixture is not otherwise safe")
	}
	st := *fi.Sys().(*syscall.Stat_t)
	st.Uid = uint32(os.Geteuid()) + 1
	if safeInfo(changedOwner{FileInfo: fi, stat: &st}) {
		t.Fatal("wrong owner accepted")
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(dir, 1, -1); err != nil {
			t.Fatal(err)
		}
		defer os.Chown(dir, 0, -1)
		if _, err := Open(options(dir)); err != protocol.StateUnsafe {
			t.Fatal("actual wrong owner accepted")
		}
	} else {
		t.Log("actual chown case requires root; ownership predicate tested with foreign uid")
	}
}

type changedOwner struct {
	os.FileInfo
	stat *syscall.Stat_t
}

func (f changedOwner) Sys() any { return f.stat }

func TestEphemeralCleanup(t *testing.T) {
	caller := t.TempDir()
	t.Setenv("TMPDIR", caller)
	o := options("")
	o.Ephemeral = true
	s, err := Open(o)
	if err != nil {
		t.Fatal(err)
	}
	dir := s.Dir
	if err := os.WriteFile(filepath.Join(dir, "identity"), []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("temporary state left behind")
	}
	if _, err := os.Stat(caller); err != nil {
		t.Fatal("caller directory deleted")
	}
	o.StateDir = caller
	if _, err := Open(o); err != protocol.InvalidOptions {
		t.Fatal("conflicting state modes accepted")
	}
}

func TestMacOSVarAlias(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS system alias")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(real) < len("/private/var/") || real[:len("/private/var/")] != "/private/var/" {
		t.Skip("temp directory is not below /private/var")
	}
	alias := real[len("/private"):]
	s, err := Open(options(alias))
	if err != nil {
		t.Fatalf("system /var alias rejected: %v", err)
	}
	s.Close()
}

func TestLockAcrossProcesses(t *testing.T) {
	if dir := os.Getenv("BRIDGE_LOCK_TEST_DIR"); dir != "" {
		if os.Getenv("BRIDGE_LOCK_TEST_OWNER") == "1" {
			if _, err := Open(options(dir)); err != nil {
				os.Exit(2)
			}
			// Deliberately bypass State.Close to test kernel lock release.
			os.Exit(0)
		}
		if s, err := Open(options(dir)); err != protocol.StateLocked {
			if s != nil {
				s.Close()
			}
			os.Exit(2)
		}
		os.Exit(0)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := Open(options(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockAcrossProcesses$")
	cmd.Env = append(os.Environ(), "BRIDGE_LOCK_TEST_DIR="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cross-process lock: %v %s", err, out)
	}
	s.Close()
	cmd = exec.Command(os.Args[0], "-test.run=^TestLockAcrossProcesses$")
	cmd.Env = append(os.Environ(), "BRIDGE_LOCK_TEST_DIR="+dir, "BRIDGE_LOCK_TEST_OWNER=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("exiting lock owner: %v %s", err, out)
	}
	after, err := Open(options(dir))
	if err != nil {
		t.Fatalf("lock remained after owner exit: %v", err)
	}
	after.Close()
}
