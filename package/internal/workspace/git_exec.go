package workspace

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	gitOutputLimit      = 1024 * 1024
	gitCommandTimeout   = 10 * time.Second
	gitTerminateGrace   = 2 * time.Second
	gitKillWait         = 500 * time.Millisecond
	gitPipeDrainTimeout = 500 * time.Millisecond
	gitExecutableEnv    = "PIXIE_GIT_EXECUTABLE"
)

// gitReadOnlyConfig is applied to every Git subprocess. Repository config is
// still parsed by Git (there is no general local-config opt-out), so every
// setting that can select a helper is overridden here. Worktree comparisons
// must not rely on Git's conversion machinery; callers use raw descriptors for
// those paths instead.
var gitReadOnlyConfig = []string{
	"-c", "core.pager=cat",
	"-c", "core.fsmonitor=false",
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.attributesFile=/dev/null",
	"-c", "core.excludesFile=/dev/null",
	"-c", "diff.external=",
	"-c", "diff.trustExitCode=false",
	"-c", "diff.submodule=short",
	"-c", "color.ui=false",
	"-c", "log.showSignature=false",
}

type gitResult struct {
	ok      bool
	out     string
	err     string
	failure string
}

func runGit(parent context.Context, directory string, args []string, limit int) gitResult {
	ctx, cancel := context.WithTimeout(parent, gitCommandTimeout)
	defer cancel()
	absoluteDirectory, err := filepath.Abs(directory)
	if err != nil {
		return gitResult{err: "Could not resolve Git sandbox", failure: "environment"}
	}
	executable, err := resolveGitExecutable()
	if err != nil {
		return gitResult{err: err.Error(), failure: "environment"}
	}
	// Git normally walks parent directories while discovering a repository. Stop
	// at the parent of the requested cwd so a --no-index scratch directory that
	// happens to live below a repository cannot inherit its attributes/config.
	ceiling := filepath.Dir(absoluteDirectory)
	base := append(append([]string(nil), gitReadOnlyConfig...), "--no-pager", "--no-optional-locks", "-C", absoluteDirectory)
	command := exec.Command(executable, append(base, args...)...)
	configureGitProcess(command)
	temporary, err := os.MkdirTemp(os.TempDir(), "pixie-git-home-")
	if err != nil {
		return gitResult{err: "Could not create Git sandbox", failure: "environment"}
	}
	defer os.RemoveAll(temporary)
	// Do not inherit Git's helper, config, tracing, credential, or repository
	// selection environment. In particular, GIT_CONFIG_GLOBAL/SYSTEM only
	// replace those two config layers; local config is constrained by the
	// explicit settings above and worktree reads stay outside Git's conversion
	// machinery.
	command.Env = []string{
		"PATH=" + gitProcessPath(executable),
		"HOME=" + temporary,
		"XDG_CONFIG_HOME=" + temporary,
		"TMPDIR=" + temporary,
		"LANG=C",
		"LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_COUNT=0",
		"GIT_CEILING_DIRECTORIES=" + ceiling,
		"GIT_NO_LAZY_FETCH=1",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_EXTERNAL_DIFF=",
		"GIT_DIFF_OPTS=",
		"GIT_PAGER=cat",
		"GIT_TERMINAL_PROMPT=0",
		"PAGER=cat",
	}
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return gitResult{err: "Could not create Git output pipe", failure: "environment"}
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutReader.Close()
		return gitResult{err: "Could not create Git error pipe", failure: "environment"}
	}
	command.Stdout = stdoutWriter
	command.Stderr = stderrWriter
	var overflow sync.Once
	stop := func() { overflow.Do(cancel) }
	stdout := &boundedGitOutput{limit: limit, stop: stop}
	stderr := &boundedGitOutput{limit: 64 * 1024, stop: stop}
	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})
	// Start the drainers before Start so a very chatty command cannot fill a
	// pipe before the process is being supervised. They block only on the pipe;
	// the bounded output writer stops the process once its limit is crossed.
	go drainGitOutput(stdoutReader, stdout, stop, stdoutDone)
	go drainGitOutput(stderrReader, stderr, stop, stderrDone)
	if ctx.Err() != nil {
		_ = stdoutReader.Close()
		_ = stderrReader.Close()
		_ = stdoutWriter.Close()
		_ = stderrWriter.Close()
		<-stdoutDone
		<-stderrDone
		return gitResult{err: "Git command timed out", failure: "timeout"}
	}
	if err := command.Start(); err != nil {
		_ = stdoutReader.Close()
		_ = stderrReader.Close()
		_ = stdoutWriter.Close()
		_ = stderrWriter.Close()
		<-stdoutDone
		<-stderrDone
		return gitResult{err: "Could not start Git", failure: "environment"}
	}
	// The child owns its dup of the write ends. Closing the parent copies keeps
	// the drainers' EOF and makes inherited descriptors observable solely through
	// the managed child group.
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()
	waitErr, waitComplete := waitGitProcess(ctx, command, waitDone, stdoutReader, stderrReader)
	if ctx.Err() != nil && waitComplete && gitProcessGroupExists(command) {
		// The group leader may have honored TERM while a descendant retained the
		// output pipe. The leader's exit is not proof that its managed group is
		// gone; finish cancellation with a group kill before returning.
		terminateGitProcess(command)
		killGitProcess(command)
	}
	if !waitComplete {
		// The explicit read ends are closed here so a descendant that escaped the
		// process group cannot keep a drain goroutine blocked forever.
		_ = stdoutReader.Close()
		_ = stderrReader.Close()
		select {
		case waitErr = <-waitDone:
		case <-time.After(gitKillWait):
			if waitErr == nil {
				waitErr = fmt.Errorf("Git process did not exit after termination")
			}
		}
	}
	stdoutDrained := waitForGitOutput(stdoutDone, stdoutReader)
	stderrDrained := waitForGitOutput(stderrDone, stderrReader)
	if !stdoutDrained || !stderrDrained {
		if gitProcessGroupExists(command) {
			terminateGitProcess(command)
			killGitProcess(command)
		}
		return gitResult{err: "Git command output could not be drained", failure: "timeout"}
	}
	if stdout.overflow || stderr.overflow {
		return gitResult{err: "Git command exceeded its output limit", failure: "output-limit"}
	}
	if ctx.Err() != nil {
		return gitResult{err: "Git command timed out", failure: "timeout"}
	}
	return gitResult{ok: waitComplete && waitErr == nil, out: stdout.String(), err: strings.TrimSpace(stderr.String())}
}

type boundedGitOutput struct {
	bytes.Buffer
	limit    int
	overflow bool
	stop     func()
}

func (b *boundedGitOutput) append(value []byte) {
	if b.Len()+len(value) > b.limit {
		remaining := max(0, b.limit-b.Len())
		_, _ = b.Buffer.Write(value[:remaining])
		b.overflow = true
		if b.stop != nil {
			b.stop()
		}
		return
	}
	_, _ = b.Buffer.Write(value)
}

func (b *boundedGitOutput) Write(value []byte) (int, error) {
	b.append(value)
	return len(value), nil
}

func drainGitOutput(reader io.ReadCloser, output *boundedGitOutput, stop func(), done chan<- struct{}) {
	defer close(done)
	defer reader.Close()
	buffer := make([]byte, 32*1024)
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			output.append(buffer[:count])
			if output.overflow {
				stop()
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func waitGitProcess(ctx context.Context, command *exec.Cmd, waitDone <-chan error, stdout, stderr io.Closer) (error, bool) {
	select {
	case err := <-waitDone:
		return err, true
	case <-ctx.Done():
		terminateGitProcess(command)
	}
	termination := time.NewTimer(gitTerminateGrace)
	defer termination.Stop()
	select {
	case err := <-waitDone:
		return err, true
	case <-termination.C:
		killGitProcess(command)
	}
	killWait := time.NewTimer(gitKillWait)
	defer killWait.Stop()
	select {
	case err := <-waitDone:
		return err, true
	case <-killWait.C:
		_ = stdout.Close()
		_ = stderr.Close()
		return fmt.Errorf("Git process cleanup timed out"), false
	}
}

func waitForGitOutput(done <-chan struct{}, reader io.Closer) bool {
	timer := time.NewTimer(gitPipeDrainTimeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		_ = reader.Close()
		select {
		case <-done:
			return false
		case <-time.After(gitKillWait):
			return false
		}
	}
}

func resolveGitExecutable() (string, error) {
	if configured := strings.TrimSpace(os.Getenv(gitExecutableEnv)); configured != "" {
		return validateGitExecutable(configured, true)
	}
	// PATH is an operator-controlled environment and may contain a repository
	// shadow named "git". Prefer the normal system locations, and only accept a
	// PATH result when it resolves to one of those locations. A custom install
	// is an explicit operator choice through PIXIE_GIT_EXECUTABLE.
	trusted := []string{"/usr/bin/git", "/bin/git", "/usr/local/bin/git", "/opt/homebrew/bin/git"}
	for _, candidate := range trusted {
		if resolved, err := validateGitExecutable(candidate, true); err == nil && trustedGitExecutable(resolved) {
			return resolved, nil
		}
	}
	if lookedUp, err := exec.LookPath("git"); err == nil {
		if candidate, candidateErr := validateGitExecutable(lookedUp, false); candidateErr == nil && trustedGitExecutable(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("Git executable is unavailable; set %s to an absolute executable", gitExecutableEnv)
}

func validateGitExecutable(path string, requireAbsolute bool) (string, error) {
	if requireAbsolute && !filepath.IsAbs(path) {
		return "", fmt.Errorf("Git executable must be an absolute path")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("could not resolve Git executable")
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("Git executable does not exist")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("Git executable is not a regular executable")
	}
	return canonical, nil
}

func trustedGitExecutable(path string) bool {
	for _, directory := range []string{"/usr/local/bin", "/usr/bin", "/bin", "/opt/homebrew/bin"} {
		if filepath.Dir(path) == directory {
			return true
		}
	}
	return false
}

func gitProcessPath(executable string) string {
	entries := []string{filepath.Dir(executable), "/usr/local/bin", "/usr/bin", "/bin", "/opt/homebrew/bin"}
	seen := make(map[string]struct{}, len(entries))
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		if _, ok := seen[entry]; ok {
			continue
		}
		seen[entry] = struct{}{}
		result = append(result, entry)
	}
	return strings.Join(result, string(os.PathListSeparator))
}
