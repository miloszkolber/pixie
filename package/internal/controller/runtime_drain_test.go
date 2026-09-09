package controller

// FIX-12/X03 bounded-termination observation (docs only, no runtime change).
//
// Observed current behavior (2026-09-09, read-only inspection):
//   - Child-group termination for Git helpers lives in
//     internal/workspace/git_exec_unix.go: the child is started with
//     Setpgid=true and cancellation uses Kill(-pid, SIGTERM) followed by
//     Kill(-pid, SIGKILL). The Windows fallback only kills the single
//     process.
//   - Pipe draining for Git helpers lives in internal/workspace/git_exec.go:
//     two os.Pipe drainer goroutines start before Start, the parent closes
//     its write-end copies after Start, and cancellation closes the explicit
//     read ends so an escaped descendant cannot block a drainer forever.
//     Bounds are gitCommandTimeout=10s, gitTerminateGrace=2s,
//     gitKillWait=500ms, gitPipeDrainTimeout=500ms.
//   - Docker entrypoint reaping: the browser-packages stage uses
//     ENTRYPOINT ["/usr/bin/tini", "-s", "--"], but the final pixie stage
//     uses ENTRYPOINT ["/app/pixie"] with no init. PID 1 reaping for the
//     published controller image is therefore an open question.
//   - Runtime drain in this package (BrowserPanels.CloseAll) sets draining,
//     stops retries, and closes matching panels through the caller context;
//     Open while draining is rejected.
//
// Residuals left for later (explicitly out of scope here):
//   - Process-group escalation for controller-spawned children (if any).
//   - Entrypoint/init change for PID 1 reaping in the published image.
//
// This file adds only narrow regressions that document the controller-side
// bounded-drain contract without changing runtime semantics.

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
