//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package workspace

import (
	"os/exec"
	"syscall"
)

func configureGitProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateGitProcess(command *exec.Cmd) {
	if command.Process == nil || command.Process.Pid <= 1 {
		return
	}
	// The child is the process-group leader. A negative pid addresses the whole
	// managed group and never an unrelated user process. Pids at or below 1
	// are never signaled: Kill(-1, ...) would address every process the
	// caller may signal instead of the managed group.
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
}

func killGitProcess(command *exec.Cmd) {
	if command.Process == nil || command.Process.Pid <= 1 {
		return
	}
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
}

func gitProcessGroupExists(command *exec.Cmd) bool {
	if command.Process == nil || command.Process.Pid <= 1 {
		return false
	}
	return syscall.Kill(-command.Process.Pid, 0) == nil
}
