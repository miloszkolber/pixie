//go:build linux

// Package processgroup starts one archive-owned child in an isolated Linux
// process group and drains the whole group on cancellation. It never signals a
// caller-owned group.
package processgroup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
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
}

type child struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

// Run inherits standard streams, cwd and (when Environment is nil) all native
// Pi environment values. A received SIGINT/SIGTERM is forwarded to the owned
// group, then the child is boundedly reaped before Run returns its exit code.
func Run(invocation Invocation, signals <-chan os.Signal) (int, error) {
	command := exec.Command(invocation.Path, invocation.Args...)
	command.Env = invocation.Environment
	command.Dir = invocation.Directory
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return 0, err
	}
	started := &child{cmd: command, done: make(chan struct{})}
	go func() {
		started.err = command.Wait()
		close(started.done)
	}()
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
