//go:build windows || plan9

package host

import (
	"os"
	"os/exec"
)

func configureNativeProcess(_ *exec.Cmd) {}

func interruptNativeProcess(process *os.Process) {
	if process != nil {
		_ = process.Signal(os.Interrupt)
	}
}

func killNativeProcess(process *os.Process) {
	if process != nil {
		_ = process.Kill()
	}
}
