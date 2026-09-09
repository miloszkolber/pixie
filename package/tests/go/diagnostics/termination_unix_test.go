//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package diagnostics_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestDiagnosticsHelperChildOutlivingDeadline verifies bounded TERM/KILL
// escalation for a managed process group: a descendant that ignores SIGTERM
// must still be reaped via SIGKILL within the caller-visible bound, and the
// group leader's Wait must return. This mirrors the Git helper wrapper
// (internal/workspace/git_exec.go + git_exec_unix.go) without calling its
// unexported helpers so the Unix-only Setpgid setup stays out of portable
// packages.
func TestDiagnosticsHelperChildOutlivingDeadline(t *testing.T) {
	temporary := t.TempDir()
	marker := filepath.Join(temporary, "child-pid")
	helper := filepath.Join(temporary, "helper.sh")
	script := "#!/bin/sh\n( trap '' TERM; sleep 30 ) &\nchild=$!\nprintf '%s' \"$child\" > '" + strings.ReplaceAll(marker, "'", "'\\''") + "'\nwait \"$child\"\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(helper)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()

	child := waitForChildMarker(t, marker)
	started := time.Now()

	// TERM first: the descendant traps TERM, so it must still be alive.
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
	time.Sleep(150 * time.Millisecond)
	if err := syscall.Kill(child, 0); err != nil {
		t.Fatalf("TERM-ignoring child exited before KILL: pid %d: %v", child, err)
	}

	// Finite escalation: KILL the managed group, never an unrelated pid.
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	waitForPIDGone(t, child, 2*time.Second)
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		t.Fatal("group leader Wait was not bounded after KILL")
	}
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("helper-child escalation was not bounded: %s", elapsed)
	}
}

// TestDiagnosticsPipeRetainingChild verifies finite inherited-pipe draining:
// a group leader may exit while a descendant retains the output pipe. The
// leader's Wait must still return promptly, closing the explicit read end
// must unblock a drainer, and a bounded group escalation must reap the
// descendant. This mirrors runGit's parent-write-end close, drain timeout,
// and descendant grace without importing unexported workspace helpers.
func TestDiagnosticsPipeRetainingChild(t *testing.T) {
	temporary := t.TempDir()
	marker := filepath.Join(temporary, "child-pid")
	helper := filepath.Join(temporary, "helper.sh")
	script := "#!/bin/sh\nsleep 30 &\nprintf '%s' \"$!\" > '" + strings.ReplaceAll(marker, "'", "'\\''") + "'\nexit 0\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer func() {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
	}()

	command := exec.Command(helper)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// The background sleep inherits this pipe, so the leader's exit does not
	// produce EOF until the descendant is reaped or the read end closes.
	command.Stdout = stdoutWriter
	if err := command.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	// The child owns its dup of the write end; closing the parent copy makes
	// EOF observable solely through the managed group.
	_ = stdoutWriter.Close()

	child := waitForChildMarker(t, marker)
	started := time.Now()
	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("group leader Wait: %v", err)
		}
	case <-time.After(2 * time.Second):
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		t.Fatal("group leader Wait was not bounded after quick exit")
	}

	// The descendant still holds the pipe: a drainer must stay blocked until
	// the explicit read end closes, proving EOF is not assumed from Wait.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		buffer := make([]byte, 32*1024)
		for {
			_, readErr := stdoutReader.Read(buffer)
			if readErr != nil {
				return
			}
		}
	}()
	select {
	case <-drained:
		t.Fatal("pipe drained while a descendant still held the write end")
	case <-time.After(200 * time.Millisecond):
	}
	_ = stdoutReader.Close()
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("closing the read end did not unblock the drainer")
	}

	// Bounded descendant escalation after the leader already exited.
	if syscall.Kill(-command.Process.Pid, 0) == nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
		deadline := time.Now().Add(100 * time.Millisecond)
		for syscall.Kill(-command.Process.Pid, 0) == nil && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	waitForPIDGone(t, child, 2*time.Second)
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("pipe-retaining cleanup was not bounded: %s", elapsed)
	}
}

func waitForChildMarker(t *testing.T, marker string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(marker)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(contents)))
			if parseErr == nil && pid > 1 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("helper did not record its child pid in %s", marker)
	return 0
}

func waitForPIDGone(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant %d survived bounded group cleanup", pid)
}
