//go:build darwin || linux

// Package state owns private identity directories and lifetime advisory locks.
package state

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"github.com/jaxxstorm/bun-tailscale-bridge/helper/internal/protocol"
	"golang.org/x/sys/unix"
)

type State struct {
	Dir       string
	ephemeral bool
	lock      *os.File
	once      sync.Once
	err       error
}

func Open(o protocol.Options) (*State, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	s := &State{ephemeral: o.Ephemeral}
	var err error
	if o.Ephemeral {
		s.Dir, err = os.MkdirTemp("", "bun-tailscale-bridge-")
	} else {
		s.Dir, err = validatePath(o.StateDir)
		if err == nil {
			err = os.MkdirAll(s.Dir, 0700)
		}
	}
	if err != nil {
		return nil, protocol.StateUnsafe
	}
	fail := func(code protocol.Code) (*State, error) {
		_ = s.Close()
		return nil, code
	}
	fi, err := os.Lstat(s.Dir)
	if err != nil || !safeInfo(fi) || !fi.IsDir() {
		return fail(protocol.StateUnsafe)
	}
	resolved, err := validatePath(s.Dir)
	if err != nil {
		return fail(protocol.StateUnsafe)
	}
	s.Dir = resolved
	dirfd, err := unix.Open(s.Dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fail(protocol.StateUnsafe)
	}
	defer unix.Close(dirfd)
	fd, err := unix.Openat(dirfd, ".bridge.lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return fail(protocol.StateUnsafe)
	}
	s.lock = os.NewFile(uintptr(fd), ".bridge.lock")
	info, err := s.lock.Stat()
	if err != nil || !safeInfo(info) || !info.Mode().IsRegular() {
		return fail(protocol.StateUnsafe)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return fail(protocol.StateLocked)
		}
		return fail(protocol.StateUnsafe)
	}
	if err := filepath.WalkDir(s.Dir, func(path string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return protocol.StateUnsafe
		}
		info, err := os.Lstat(path)
		if err != nil || !safeInfo(info) {
			return protocol.StateUnsafe
		}
		return nil
	}); err != nil {
		return fail(protocol.StateUnsafe)
	}
	return s, nil
}

// validatePath checks existing components before any directories are created.
// Only the exact root-owned Darwin system aliases may redirect traversal.
func validatePath(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", protocol.StateUnsafe
	}
	path := "/"
	parts := strings.Split(strings.TrimPrefix(dir, "/"), "/")
	for i := -1; i < len(parts); i++ {
		if i >= 0 {
			path = filepath.Join(path, parts[i])
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return dir, nil
		}
		if err != nil {
			return "", protocol.StateUnsafe
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return "", protocol.StateUnsafe
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if runtime.GOOS != "darwin" || st.Uid != 0 || (path != "/var" && path != "/tmp") || path == dir {
				return "", protocol.StateUnsafe
			}
			target, err := os.Readlink(path)
			if err != nil || target != "private"+path {
				return "", protocol.StateUnsafe
			}
			return validatePath("/private" + dir)
		}
		if path == dir {
			if !info.IsDir() || !safeInfo(info) {
				return "", protocol.StateUnsafe
			}
		} else if !info.IsDir() || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) ||
			(info.Mode().Perm()&0022 != 0 && (st.Uid != 0 || info.Mode()&os.ModeSticky == 0)) {
			// Only root-owned sticky directories may be writable by others.
			return "", protocol.StateUnsafe
		}
	}
	return dir, nil
}

func safeInfo(fi os.FileInfo) bool {
	if fi == nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) {
		return false
	}
	if fi.IsDir() {
		return fi.Mode().Perm() == 0700 && fi.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0
	}
	return fi.Mode().IsRegular() && fi.Mode().Perm() == 0600 && st.Nlink == 1 && fi.Mode()&(os.ModeSetuid|os.ModeSetgid) == 0
}

func (s *State) Close() error {
	s.once.Do(func() {
		if s.lock != nil {
			// Never unlink the lock: waiters must all lock the same inode.
			if err := s.lock.Close(); err != nil {
				s.err = protocol.StateUnsafe
			}
		}
		if s.ephemeral && s.Dir != "" {
			if err := os.RemoveAll(s.Dir); err != nil {
				s.err = protocol.StateUnsafe
			}
		}
	})
	return s.err
}
