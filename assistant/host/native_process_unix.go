//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package host

import (
	"os"
	"os/exec"
	"syscall"
)

func configureNativeProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func interruptNativeProcess(process *os.Process) {
	if process == nil {
		return
	}
	if err := syscall.Kill(-process.Pid, syscall.SIGINT); err == nil {
		return
	}
	_ = process.Signal(os.Interrupt)
}

func killNativeProcess(process *os.Process) {
	if process == nil {
		return
	}
	if err := syscall.Kill(-process.Pid, syscall.SIGKILL); err == nil {
		return
	}
	_ = process.Kill()
}
