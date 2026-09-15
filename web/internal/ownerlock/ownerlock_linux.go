//go:build linux

// Package ownerlock serializes Pixie's supported owners of one Pi agent
// directory. It deliberately uses a kernel advisory lock rather than a PID
// file: closing the descriptor releases it when an owner exits or crashes.
package ownerlock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	// ContentionExitCode is the sysexits-compatible temporary-unavailable
	// status used when another supported Pi owner still holds the lock.
	ContentionExitCode = 73

	// HandoffGuidance is intentionally free of paths, PIDs, and environment
	// values. It is safe to show to an operator on a shared terminal.
	HandoffGuidance = "Pi is already owned by another Pixie session; stop that owner before retrying, or use a different PI_CODING_AGENT_DIR. Pixie does not attach to an active TUI session."
)

// ContentionError reports an active advisory owner without exposing its
// diagnostic record. The record is advisory only and never decides ownership.
type ContentionError struct{}

func (ContentionError) Error() string { return HandoffGuidance }

// IsContended identifies a refusal that must retain the special handoff exit
// status instead of the general launcher failure status.
func IsContended(err error) bool {
	var contention ContentionError
	return errors.As(err, &contention)
}

// ExitCode returns the launcher status appropriate for an owner-lock error.
func ExitCode(err error) int {
	if IsContended(err) {
		return ContentionExitCode
	}
	return 1
}

// Lock owns an advisory lock until Close. It is not a PID file and callers
// must retain it for the lifetime of the process tree they launch.
type Lock struct {
	file *os.File
	path string
}

// Path returns the canonical lock path for diagnostics and tests.
func (lock *Lock) Path() string { return lock.path }

// Close releases this process's advisory lock. A second Close is harmless.
func (lock *Lock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	file := lock.file
	lock.file = nil
	unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	closeErr := file.Close()
	return errors.Join(unlockErr, closeErr)
}

type diagnostic struct {
	Product   string `json:"product"`
	StartedAt string `json:"startedAt"`
}

// ResolveAgentDir chooses the supplied service configuration only when the
// native PI_CODING_AGENT_DIR override is absent. The native value and HOME are
// required to be absolute; silently guessing a state directory could lock a
// different Pi owner.
func ResolveAgentDir(configAgentDir string, lookup func(string) (string, bool)) (string, error) {
	selected := ""
	if value, ok := lookup("PI_CODING_AGENT_DIR"); ok {
		selected = strings.TrimSpace(value)
	}
	if selected == "" {
		selected = strings.TrimSpace(configAgentDir)
	}
	if selected == "" {
		home, ok := lookup("HOME")
		home = strings.TrimSpace(home)
		if !ok || home == "" || !filepath.IsAbs(home) {
			return "", errors.New("HOME must be an absolute path when PI_CODING_AGENT_DIR is unset")
		}
		selected = filepath.Join(home, ".pi", "agent")
	}
	if !filepath.IsAbs(selected) {
		return "", errors.New("PI_CODING_AGENT_DIR and service agentDir must be absolute paths")
	}
	selected = filepath.Clean(selected)
	if err := os.MkdirAll(selected, 0o700); err != nil {
		return "", fmt.Errorf("create Pi agent directory: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(selected)
	if err != nil || !filepath.IsAbs(canonical) {
		return "", errors.New("could not canonicalize Pi agent directory")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", errors.New("canonical Pi agent directory is unavailable")
	}
	return canonical, nil
}

// Acquire takes one nonblocking Linux advisory lock at the canonical Pi agent
// directory. It records only a product label and start time, mode 0600, after
// the kernel has granted ownership.
func Acquire(agentDir, product string) (*Lock, error) {
	if strings.TrimSpace(product) == "" {
		return nil, errors.New("owner lock product is required")
	}
	if !filepath.IsAbs(agentDir) {
		return nil, errors.New("canonical Pi agent directory must be absolute")
	}
	canonical, err := filepath.EvalSymlinks(agentDir)
	if err != nil || !filepath.IsAbs(canonical) {
		return nil, errors.New("could not canonicalize Pi agent directory")
	}
	lockDirectory := filepath.Join(canonical, "pixie")
	if err := os.MkdirAll(lockDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create Pi owner-lock directory: %w", err)
	}
	path := filepath.Join(lockDirectory, "owner.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Pi owner lock: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure Pi owner lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ContentionError{}
		}
		return nil, fmt.Errorf("lock Pi owner: %w", err)
	}
	record, err := json.Marshal(diagnostic{
		Product:   product,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err == nil {
		if err = file.Truncate(0); err == nil {
			_, err = file.WriteAt(append(record, '\n'), 0)
		}
	}
	if err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, fmt.Errorf("write Pi owner diagnostic: %w", err)
	}
	return &Lock{file: file, path: path}, nil
}
