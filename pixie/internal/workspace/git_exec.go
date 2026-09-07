package workspace

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const gitOutputLimit = 1024 * 1024

type gitResult struct {
	ok      bool
	out     string
	err     string
	failure string
}

func runGit(parent context.Context, directory string, args []string, limit int) gitResult {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	base := []string{"-c", "core.pager=cat", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-c", "core.attributesFile=/dev/null", "-c", "core.excludesFile=/dev/null", "--no-pager", "-C", directory}
	command := exec.CommandContext(ctx, "git", append(base, args...)...)
	pathValue := os.Getenv("PATH")
	if pathValue == "" {
		pathValue = "/usr/local/bin:/usr/bin:/bin"
	}
	temporary := os.TempDir()
	command.Env = []string{"PATH=" + pathValue, "HOME=" + temporary, "TMPDIR=" + temporary, "LANG=C", "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_NO_LAZY_FETCH=1", "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0", "PAGER=cat"}
	var overflow sync.Once
	stop := func() { overflow.Do(cancel) }
	stdout, stderr := &boundedGitOutput{limit: limit, stop: stop}, &boundedGitOutput{limit: 64 * 1024, stop: stop}
	command.Stdout, command.Stderr = stdout, stderr
	err := command.Run()
	if stdout.overflow || stderr.overflow {
		return gitResult{err: "Git command exceeded its output limit", failure: "output-limit"}
	}
	if ctx.Err() != nil {
		return gitResult{err: "Git command timed out", failure: "timeout"}
	}
	return gitResult{ok: err == nil, out: stdout.String(), err: strings.TrimSpace(stderr.String())}
}

type boundedGitOutput struct {
	bytes.Buffer
	limit    int
	overflow bool
	stop     func()
}

func (b *boundedGitOutput) Write(value []byte) (int, error) {
	if b.Len()+len(value) > b.limit {
		remaining := max(0, b.limit-b.Len())
		_, _ = b.Buffer.Write(value[:remaining])
		b.overflow = true
		b.stop()
		return len(value), nil
	}
	return b.Buffer.Write(value)
}
