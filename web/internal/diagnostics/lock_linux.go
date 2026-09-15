//go:build linux

package diagnostics

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// OwnerLockHeld reports whether another Pixie owner currently holds the
// advisory lock for agentDir. It is read-only: it never creates the lock file
// or its directory. Unknown (nil) is returned when the file exists but cannot
// be inspected, so absence of evidence is never reported as "free".
func OwnerLockHeld(agentDir string) *bool {
	if agentDir == "" {
		return nil
	}
	// ownerlock.Acquire owns this exact layout; this probe only reads it.
	path := filepath.Join(agentDir, "pixie", "owner.lock")
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			free := false
			return &free
		}
		return nil
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			held := true
			return &held
		}
		return nil
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	free := false
	return &free
}
