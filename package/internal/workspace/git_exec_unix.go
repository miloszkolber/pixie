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
	if command.Process == nil {
		return
	}
	// The child is the process-group leader. A negative pid addresses the whole
	// managed group and never an unrelated user process.
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
}

func killGitProcess(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
}

func gitProcessGroupExists(command *exec.Cmd) bool {
	return command.Process != nil && syscall.Kill(-command.Process.Pid, 0) == nil
}
