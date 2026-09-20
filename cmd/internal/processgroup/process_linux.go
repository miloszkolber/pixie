//go:build linux

// Package processgroup starts one archive-owned child in an isolated Linux
// process group and drains the whole group on cancellation. It never signals a
// caller-owned group.
package processgroup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	termGrace  = 24 * time.Second
	reapWindow = time.Second
)

// Invocation preserves the native launch boundary. A nil Environment means
// inherit the process environment unchanged; a nil directory inherits cwd.
type Invocation struct {
	Path        string
	Args        []string
	Environment []string
	Directory   string
	// Interactive gives the child process group the controlling terminal while
	// it runs. This is required for a TUI child: a new background process group
	// is otherwise stopped by the terminal on its first read (SIGTTIN).
	Interactive bool
	// Stderr receives the child's standard error. A nil Stderr inherits
	// os.Stderr so the native Pi command boundary is unchanged. A launcher may
	// supply a line-buffering sink when it needs per-line handling.
	Stderr io.Writer
	// Ready, when set, is probed after the child starts and before Run begins
	// its normal wait. It must return promptly once ctx is canceled; Run
	// cancels ctx when the child exits or a signal arrives, and drains the
	// child group when Ready returns an error.
	Ready func(ctx context.Context) error
}

// ReadyError reports that a managed child started but never became ready. It is
// distinct from the child's own exit status so a launcher can fail closed with
// remediation instead of mirroring an opaque signal.
type ReadyError struct{ Err error }

func (err ReadyError) Error() string { return err.Err.Error() }
func (err ReadyError) Unwrap() error { return err.Err }

type child struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

type terminalForeground struct {
	fd   int
	pgid int
}

func (foreground *terminalForeground) restore() error {
	if foreground == nil {
		return nil
	}
	if err := unix.IoctlSetPointerInt(foreground.fd, unix.TIOCSPGRP, foreground.pgid); err != nil {
		if errors.Is(err, unix.ENOTTY) || errors.Is(err, unix.ESRCH) {
			return nil
		}
		return fmt.Errorf("restore terminal foreground process group: %w", err)
	}
	return nil
}

// giveTerminalToChild transfers the controlling terminal only when stdin is a
// terminal already owned by this launcher. Non-interactive services and pipes
// remain unchanged. The returned state restores the launcher's process group.
func giveTerminalToChild(pgid int) (*terminalForeground, error) {
	fd := int(os.Stdin.Fd())
	current, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	if err != nil {
		if errors.Is(err, unix.ENOTTY) || errors.Is(err, unix.EBADF) {
			return nil, nil
		}
		return nil, fmt.Errorf("read terminal foreground process group: %w", err)
	}
	launcher := unix.Getpgrp()
	if current != launcher {
		return nil, errors.New("launcher is not the terminal foreground process group")
	}
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, pgid); err != nil {
		if errors.Is(err, unix.ESRCH) {
			// A short-lived child may have exited before the handoff. Its normal
			// wait path will report that result and the terminal stays unchanged.
			return nil, nil
		}
		return nil, fmt.Errorf("give terminal foreground to child process group: %w", err)
	}
	// If the child attempted a read before the handoff it may already be
	// stopped by SIGTTIN. Resuming the group is harmless for a running child.
	if err := unix.Kill(-pgid, unix.SIGCONT); err != nil && !errors.Is(err, unix.ESRCH) {
		_ = (&terminalForeground{fd: fd, pgid: launcher}).restore()
		return nil, fmt.Errorf("resume child process group: %w", err)
	}
	return &terminalForeground{fd: fd, pgid: launcher}, nil
}

// Run inherits standard streams, cwd and (when Environment is nil) all native
// Pi environment values. An interactive child also receives the terminal
// foreground while it runs. A received SIGINT/SIGTERM is forwarded to the
// owned group, then the child is boundedly reaped before Run returns its exit
// code.
func Run(invocation Invocation, signals <-chan os.Signal) (int, error) {
	command := exec.Command(invocation.Path, invocation.Args...)
	command.Env = invocation.Environment
	command.Dir = invocation.Directory
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	if invocation.Stderr != nil {
		command.Stderr = invocation.Stderr
	} else {
		command.Stderr = os.Stderr
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return 0, err
	}
	started := &child{cmd: command, done: make(chan struct{})}
	// Every post-start failure path drains this child, including terminal
	// handoff failure. Start the sole reaper before any such path can run.
	go func() {
		started.err = command.Wait()
		close(started.done)
	}()
	var foreground *terminalForeground
	if invocation.Interactive {
		var handoffErr error
		foreground, handoffErr = giveTerminalToChild(command.Process.Pid)
		if handoffErr != nil {
			_ = drain(started, syscall.SIGTERM)
			return exitCode(command), handoffErr
		}
		defer func() { _ = foreground.restore() }()
	}
	if invocation.Ready != nil {
		readyCtx, cancelReady := context.WithCancel(context.Background())
		ready := make(chan error, 1)
		go func() { ready <- invocation.Ready(readyCtx) }()
		select {
		case err := <-ready:
			cancelReady()
			if err != nil {
				_ = drain(started, syscall.SIGTERM)
				return exitCode(command), ReadyError{Err: err}
			}
		case <-started.done:
			cancelReady()
			return exitCode(command), started.err
		case received := <-signals:
			cancelReady()
			if err := drain(started, operatorSignal(received)); err != nil {
				return exitCode(command), err
			}
			return exitCode(command), started.err
		}
	}
	select {
	case <-started.done:
		return exitCode(command), started.err
	case received := <-signals:
		if err := drain(started, operatorSignal(received)); err != nil {
			return exitCode(command), err
		}
		return exitCode(command), started.err
	}
}

func exitCode(command *exec.Cmd) int {
	if command.ProcessState == nil {
		return 1
	}
	return command.ProcessState.ExitCode()
}

func operatorSignal(received os.Signal) syscall.Signal {
	if received == syscall.SIGINT {
		return syscall.SIGINT
	}
	return syscall.SIGTERM
}

func drain(started *child, initial syscall.Signal) error {
	var signalErrors []error
	if err := signalGroup(started, initial); err != nil {
		signalErrors = append(signalErrors, err)
	}
	if wait(started, termGrace) {
		return errors.Join(signalErrors...)
	}
	if err := signalGroup(started, syscall.SIGKILL); err != nil {
		signalErrors = append(signalErrors, err)
	}
	if wait(started, reapWindow) {
		return errors.Join(signalErrors...)
	}
	return errors.Join(errors.Join(signalErrors...), errors.New("archive child process group did not exit during shutdown"))
}

func signalGroup(started *child, signal syscall.Signal) error {
	if err := syscall.Kill(-started.cmd.Process.Pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("signal archive child process group: %w", err)
	}
	return nil
}

func wait(started *child, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-started.done:
		return true
	case <-timer.C:
		return false
	}
}
