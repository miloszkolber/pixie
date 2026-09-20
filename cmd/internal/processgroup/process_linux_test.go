//go:build linux

package processgroup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestProcessGroupGracefulHelper is a managed child that handles SIGTERM and
// exits 0, so a caller can prove the group is stopped gracefully before any
// SIGKILL escalation.
func TestProcessGroupGracefulHelper(t *testing.T) {
	if os.Getenv("PIXIE_PROCESS_GROUP_GRACEFUL") != "1" {
		return
	}
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	<-term
	os.Exit(0)
}

func TestProcessGroupHelper(t *testing.T) {
	if os.Getenv("PIXIE_PROCESS_GROUP_HELPER") != "1" {
		return
	}
	pidPath := os.Getenv("PIXIE_PROCESS_GROUP_CHILD_PID")
	child := exec.Command(os.Args[0], "-test.run=TestProcessGroupHelper", "--")
	child.Env = append(os.Environ(), "PIXIE_PROCESS_GROUP_HELPER=child")
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
		os.Exit(3)
	}
	select {}
}

func TestRunDrainsDescendantProcessGroup(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "descendant.pid")
	signals := make(chan os.Signal, 1)
	result := make(chan struct {
		code int
		err  error
	}, 1)
	go func() {
		code, err := Run(Invocation{
			Path: os.Args[0],
			Args: []string{"-test.run=TestProcessGroupHelper", "--"},
			Environment: append(
				os.Environ(),
				"PIXIE_PROCESS_GROUP_HELPER=1",
				"PIXIE_PROCESS_GROUP_CHILD_PID="+pidPath,
			),
		}, signals)
		result <- struct {
			code int
			err  error
		}{code, err}
	}()
	var pid int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(pidPath)
		if err == nil {
			pid, err = strconv.Atoi(string(raw))
			if err == nil && pid > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("helper never started its descendant")
	}
	signals <- syscall.SIGTERM
	select {
	case outcome := <-result:
		if outcome.err == nil {
			t.Fatal("terminated child was reported as a clean command result")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("process group was not reaped after SIGTERM")
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant %d remained after group drain", pid)
}

type runOutcome struct {
	code int
	err  error
}

func runAsync(invocation Invocation, signals <-chan os.Signal) <-chan runOutcome {
	result := make(chan runOutcome, 1)
	go func() {
		code, err := Run(invocation, signals)
		result <- runOutcome{code: code, err: err}
	}()
	return result
}

// A failed readiness probe must abort the goroutine and drain the owned child
// group instead of leaving it running. The launcher distinguishes this from the
// child's own exit status so it can print actionable remediation.
func TestRunFailsClosedWhenReadinessFails(t *testing.T) {
	signals := make(chan os.Signal, 1)
	result := runAsync(Invocation{
		Path: "/bin/sh",
		Args: []string{"-c", "sleep 30"},
		Ready: func(context.Context) error {
			return errors.New("assistant never became ready")
		},
	}, signals)
	select {
	case outcome := <-result:
		var readyErr ReadyError
		if !errors.As(outcome.err, &readyErr) {
			t.Fatalf("readiness failure = %v, want ReadyError", outcome.err)
		}
		if !strings.Contains(outcome.err.Error(), "never became ready") {
			t.Fatalf("readiness error = %q", outcome.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after a failed readiness probe")
	}
}

// A child that exits during the readiness probe must be reported as its own
// exit status, not as a readiness failure, and must not deadlock Run.
func TestRunReadinessAbortsOnEarlyChildExit(t *testing.T) {
	signals := make(chan os.Signal, 1)
	result := runAsync(Invocation{
		Path: "/bin/sh",
		Args: []string{"-c", "exit 7"},
		Ready: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}, signals)
	select {
	case outcome := <-result:
		var readyErr ReadyError
		if errors.As(outcome.err, &readyErr) {
			t.Fatalf("early child exit reported as readiness failure: %v", outcome.err)
		}
		if outcome.code != 7 {
			t.Fatalf("exit code = %d, want 7", outcome.code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after the child exited during readiness")
	}
}

// A successful readiness probe keeps the normal wait/signal behavior.
func TestRunReadinessPassesThroughToNormalWait(t *testing.T) {
	signals := make(chan os.Signal, 1)
	readyCalled := make(chan struct{})
	result := runAsync(Invocation{
		Path: "/bin/sh",
		Args: []string{"-c", "sleep 0.05; exit 0"},
		Ready: func(context.Context) error {
			close(readyCalled)
			return nil
		},
	}, signals)
	select {
	case <-readyCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("readiness probe was not invoked")
	}
	select {
	case outcome := <-result:
		if outcome.code != 0 || outcome.err != nil {
			t.Fatalf("Run = code %d, err %v", outcome.code, outcome.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after a ready child exited")
	}
}

// The launcher may supply its own sink (for example a line-buffering and
// redacting ring) instead of inheriting os.Stderr. The provided writer must
// receive the complete child stream.
func TestRunRoutesChildStderrToProvidedWriter(t *testing.T) {
	var captured bytes.Buffer
	code, err := Run(Invocation{
		Path:   "/bin/sh",
		Args:   []string{"-c", "printf 'first-half-' >&2; printf 'second-half\\n' >&2"},
		Stderr: &captured,
	}, make(chan os.Signal, 1))
	if err != nil || code != 0 {
		t.Fatalf("Run = code %d, err %v", code, err)
	}
	if got := captured.String(); got != "first-half-second-half\n" {
		t.Fatalf("child stderr = %q", got)
	}
}

func TestRunInteractiveWithoutTerminalStillInheritsNormalExecution(t *testing.T) {
	t.Setenv("PI_PIXIE_TEST", "preserved")
	code, err := Run(Invocation{
		Path:        "/bin/sh",
		Args:        []string{"-c", "test -n \"$PWD\" && test \"$PI_PIXIE_TEST\" = preserved"},
		Interactive: true,
	}, make(chan os.Signal, 1))
	if err != nil || code != 0 {
		t.Fatalf("Run = code %d, err %v", code, err)
	}
}

// Run this fixture as a session leader with a controlling PTY, then launch
// another process group in the background. Its attempted TUI handoff must fail
// promptly and reap its child rather than waiting for the shutdown deadline.
func TestInteractiveHandoffFailureHelper(t *testing.T) {
	switch os.Getenv("PIXIE_HANDOFF_HELPER") {
	case "leader":
		background := exec.Command(os.Args[0], "-test.run=^TestInteractiveHandoffFailureHelper$")
		background.Env = append(os.Environ(), "PIXIE_HANDOFF_HELPER=background")
		background.Stdin = os.Stdin
		background.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if output, err := background.CombinedOutput(); err != nil {
			t.Fatalf("background launcher: %v\n%s", err, output)
		}
	case "background":
		code, err := Run(Invocation{
			Path: "/bin/sleep", Args: []string{"30"}, Interactive: true,
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "not the terminal foreground") {
			t.Fatalf("handoff error = %v", err)
		}
		if code != -1 {
			t.Fatalf("child exit code = %d, want reaped signal exit", code)
		}
	}
}

func TestRunReapsChildWhenTerminalHandoffFails(t *testing.T) {
	master, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(master)
	if err := unix.IoctlSetPointerInt(master, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(master, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile("/dev/pts/"+strconv.Itoa(number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer slave.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestInteractiveHandoffFailureHelper$")
	command.Env = append(os.Environ(), "PIXIE_HANDOFF_HELPER=leader")
	command.Stdin = slave
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("handoff fixture: %v\n%s", err, output)
	}
}

// A child that handles SIGTERM must be allowed to exit on its own; the group
// is only escalated to SIGKILL after the grace window. A -1 exit code would
// mean SIGKILL, so a clean code proves graceful stop ran first.
func TestRunStopsGracefullyBeforeKilling(t *testing.T) {
	signals := make(chan os.Signal, 1)
	result := runAsync(Invocation{
		Path:        os.Args[0],
		Args:        []string{"-test.run=TestProcessGroupGracefulHelper", "--"},
		Environment: append(os.Environ(), "PIXIE_PROCESS_GROUP_GRACEFUL=1"),
	}, signals)
	time.Sleep(100 * time.Millisecond)
	signals <- syscall.SIGTERM
	select {
	case outcome := <-result:
		if outcome.code != 0 {
			t.Fatalf("graceful stop exit code = %d, want 0 (SIGKILL leaves -1)", outcome.code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("graceful stop did not return")
	}
}
