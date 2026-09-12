package controller

// FIX-12/X03 bounded-termination progress (2026-09-09).
//
// Current behavior after this change:
//   - Child-group termination for Git helpers lives in
//     internal/workspace/git_exec_unix.go: the child is started with
//     Setpgid=true and cancellation uses Kill(-pid, SIGTERM) followed by
//     Kill(-pid, SIGKILL) with gitTerminateGrace=2s between them. A leader
//     that already exited still gets a bounded descendant escalation
//     (terminateGitGroup in git_exec.go: TERM, 100ms grace polling the
//     group, then KILL) so a descendant retaining the output pipe cannot
//     outlive cancellation. The Windows fallback only kills the single
//     process. Group signalling never addresses pids at or below 1, so
//     Kill(-1, ...) can never reach unrelated processes.
//   - Pipe draining for Git helpers lives in internal/workspace/git_exec.go:
//     two os.Pipe drainer goroutines start before Start, the parent closes
//     its write-end copies after Start, and cancellation closes the explicit
//     read ends so an escaped descendant cannot block a drainer forever.
//     Bounds are gitCommandTimeout=10s, gitTerminateGrace=2s,
//     gitKillWait=500ms, gitPipeDrainTimeout=500ms, plus the 100ms
//     descendant grace above.
//   - Docker entrypoint reaping: every stage now runs under tini. The final
//     pixie stage uses ENTRYPOINT ["/usr/bin/tini", "-s", "--", "/app/pixie"]
//     so PID 1 reaps orphaned managed-group descendants instead of leaving
//     zombies. See package/tests/go/diagnostics/termination_test.go.
//   - Runtime drain in this package (BrowserPanels.CloseAll) sets draining,
//     stops retries, and closes matching panels through the caller context;
//     Open while draining is rejected.
//
// Residuals left for later (explicitly out of scope here):
//   - Process-group escalation for controller-spawned children (if any).
//
// This file keeps narrow regressions that document the controller-side
// bounded-drain contract without changing runtime semantics. Managed-group
// subprocess regressions (helper child ignoring TERM, pipe-retaining child)
// live in package/tests/go/diagnostics/termination_unix_test.go so the
// Unix-only process-group setup does not break Windows builds of this
// package.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRuntimeDrainRejectsOpenAfterCloseAll documents that CloseAll is a
// one-way drain: once draining starts, Open is rejected and repeated
// CloseAll calls stay bounded.
func TestRuntimeDrainRejectsOpenAfterCloseAll(t *testing.T) {
	panels := NewBrowserPanels(AuthConfig{}, nil)
	if _, err := panels.Open("client-a", "project-a"); err != nil {
		t.Fatalf("Open before drain: %v", err)
	}

	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	panels.CloseAll(ctx)
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("CloseAll was not bounded: %s", elapsed)
	}

	if _, err := panels.Open("client-b", "project-b"); err == nil {
		t.Fatal("Open succeeded while panels were draining")
	} else if !strings.Contains(err.Error(), "shutting down") {
		t.Fatalf("Open during drain error = %q, want shutting-down", err)
	}

	// A second drain must not hang or panic.
	second, secondCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer secondCancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		panels.CloseAll(second)
	}()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("second CloseAll was not bounded")
	}
}

// TestRuntimeDrainPipeCloseUnblocksReader documents the pipe-drain pattern
// relied on by bounded termination: closing the explicit read end unblocks a
// goroutine blocked in Read, so a descendant holding the write end cannot
// keep the drainer blocked forever.
func TestRuntimeDrainPipeCloseUnblocksReader(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer func() {
		_ = reader.Close()
		_ = writer.Close()
	}()

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		buffer := make([]byte, 32*1024)
		for {
			_, readErr := reader.Read(buffer)
			if readErr != nil {
				return
			}
		}
	}()

	// Give the drainer a moment to block in Read, then close the read end.
	// This mirrors waitGitProcess/waitForGitOutput closing the explicit pipe
	// ends after gitKillWait/gitPipeDrainTimeout.
	time.Sleep(50 * time.Millisecond)
	_ = reader.Close()

	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("pipe drain was not unblocked by closing the read end")
	}
}

// TestRuntimeDrainTimeoutBoundsUnclosedPipe documents the finite drain bound:
// a reader with no EOF and no close must surface a timeout instead of
// blocking forever, and a later close still unblocks the drainer. This
// mirrors waitForGitOutput's gitPipeDrainTimeout followed by closing the
// explicit read end. Portable: no subprocesses, no process groups.
func TestRuntimeDrainTimeoutBoundsUnclosedPipe(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	// The writer stays open for the whole test so the reader never sees EOF,
	// simulating a descendant retaining the output pipe.
	defer func() {
		_ = reader.Close()
		_ = writer.Close()
	}()

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		buffer := make([]byte, 32*1024)
		for {
			_, readErr := reader.Read(buffer)
			if readErr != nil {
				return
			}
		}
	}()

	started := time.Now()
	select {
	case <-drained:
		t.Fatal("pipe drain returned without EOF or close")
	case <-time.After(200 * time.Millisecond):
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("pipe drain timeout was not bounded: %s", elapsed)
	}

	_ = reader.Close()
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("pipe drain was not unblocked by closing the read end after timeout")
	}
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("pipe drain close was not bounded: %s", elapsed)
	}
}
