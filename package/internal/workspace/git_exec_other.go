//go:build windows

package workspace

import "os/exec"

// The workspace filesystem implementation is Linux-oriented. Keep the Git
// wrapper compiling on Windows; the process API there has no portable Unix
// process-group equivalent in this package, so cancellation still closes the
// explicit output pipes and relies on os/exec's process termination.
func configureGitProcess(command *exec.Cmd) {}
func terminateGitProcess(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	_ = command.Process.Kill()
}
func killGitProcess(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	_ = command.Process.Kill()
}
func gitProcessGroupExists(command *exec.Cmd) bool {
	return command.Process != nil
}
