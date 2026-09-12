//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package host

import (
	"os"
	"os/exec"
	"syscall"
)

func lockNativeFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlockNativeFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

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
