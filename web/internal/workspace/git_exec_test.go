package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunGitDisablesRepositoryFsmonitorAndFilterExecution(t *testing.T) {
	repository := t.TempDir()
	marker := filepath.Join(t.TempDir(), "marker")
	helper := filepath.Join(t.TempDir(), "helper.sh")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf x >> "+shellQuoteForGitTest(marker)+"\nexit 73\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	gitForTest(t, repository, "init", "-b", "main")
	gitForTest(t, repository, "config", "user.name", "Pixie test")
	gitForTest(t, repository, "config", "user.email", "test@pixie.test")
	if err := os.WriteFile(filepath.Join(repository, ".gitattributes"), []byte("*.txt filter=pixie-marker\n*.proc filter=pixie-process\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "tracked.proc"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForTest(t, repository, "add", "--", ".gitattributes", "tracked.txt", "tracked.proc")
	gitForTest(t, repository, "commit", "-m", "initial")
	gitForTest(t, repository, "config", "filter.pixie-marker.clean", helper)
	gitForTest(t, repository, "config", "filter.pixie-marker.required", "true")
	gitForTest(t, repository, "config", "filter.pixie-process.process", helper)
	gitForTest(t, repository, "config", "filter.pixie-process.required", "true")
	gitForTest(t, repository, "config", "core.fsmonitor", helper)
	if err := os.WriteFile(filepath.Join(repository, ".git", "info", "attributes"), []byte("*.txt filter=pixie-marker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "tracked.proc"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.fsmonitor")
	t.Setenv("GIT_CONFIG_VALUE_0", helper)
	t.Setenv("GIT_EXTERNAL_DIFF", helper)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "not-the-repository"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	result := runGit(context.Background(), repository, []string{"status", "--short"}, gitOutputLimit)
	if !result.ok || !strings.Contains(result.out, "tracked.txt") || !strings.Contains(result.out, "tracked.proc") {
		t.Fatalf("safe Git status: %#v", result)
	}
	contents, err := os.ReadFile(marker)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(contents) != 0 {
		t.Fatalf("repository helper executed: %q", contents)
	}
}

func TestRunGitDoesNotDiscoverAParentRepositoryForScratchDirectories(t *testing.T) {
	repository := t.TempDir()
	gitForTest(t, repository, "init", "-b", "main")
	scratch := filepath.Join(repository, "scratch")
	if err := os.Mkdir(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	result := runGit(context.Background(), scratch, []string{"rev-parse", "--show-toplevel"}, gitOutputLimit)
	if result.ok {
		t.Fatalf("scratch directory discovered parent repository: %#v", result)
	}
}

func TestRunGitCancellationTerminatesHelperChildren(t *testing.T) {
	repository := t.TempDir()
	marker := filepath.Join(t.TempDir(), "child-pid")
	helper := filepath.Join(t.TempDir(), "git-helper.sh")
	quotedMarker := shellQuoteForGitTest(marker)
	script := "#!/bin/sh\n( trap '' TERM; while :; do sleep 1; done ) &\nchild=$!\nprintf '%s' \"$child\" > " + quotedMarker + "\nwait \"$child\"\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(gitExecutableEnv, helper)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	result := runGit(ctx, repository, []string{"status"}, gitOutputLimit)
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("Git helper cancellation was not bounded: %s (%#v)", elapsed, result)
	}
	if result.failure != "timeout" {
		t.Fatalf("Git helper cancellation result: %#v", result)
	}
	pidBytes, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("helper child marker: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil || pid <= 0 {
		t.Fatalf("helper child pid: %q", pidBytes)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Git helper child survived process-group cleanup: %d", pid)
}

func TestRunGitOutputLimitTerminatesHelperChildren(t *testing.T) {
	repository := t.TempDir()
	marker := filepath.Join(t.TempDir(), "child-pid")
	helper := filepath.Join(t.TempDir(), "git-helper.sh")
	quotedMarker := shellQuoteForGitTest(marker)
	script := "#!/bin/sh\n( trap '' TERM; while :; do sleep 1; done ) &\nchild=$!\nprintf '%s' \"$child\" > " + quotedMarker + "\ndd if=/dev/zero bs=1048576 count=2 2>/dev/null\nwait \"$child\"\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(gitExecutableEnv, helper)
	started := time.Now()
	result := runGit(context.Background(), repository, []string{"status"}, gitOutputLimit)
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("Git output-limit cleanup was not bounded: %s (%#v)", elapsed, result)
	}
	if result.failure != "output-limit" {
		t.Fatalf("Git output-limit result: %#v", result)
	}
	pidBytes, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("helper child marker: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil || pid <= 0 {
		t.Fatalf("helper child pid: %q", pidBytes)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Git output-limit helper child survived cleanup: %d", pid)
}

func TestRunGitRejectsPATHShadowUnlessExplicitlySelected(t *testing.T) {
	repository := t.TempDir()
	marker := filepath.Join(t.TempDir(), "shadow-marker")
	shadow := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\nprintf x >> " + shellQuoteForGitTest(marker) + "\nexit 73\n"
	if err := os.WriteFile(shadow, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	pathValue := filepath.Dir(shadow) + string(os.PathListSeparator) + os.Getenv("PATH")
	t.Setenv("PATH", pathValue)
	t.Setenv(gitExecutableEnv, "")
	result := runGit(context.Background(), repository, []string{"--version"}, gitOutputLimit)
	if !result.ok || !strings.Contains(result.out, "git version") {
		t.Fatalf("PATH shadow changed Git selection: %#v", result)
	}
	if contents, err := os.ReadFile(marker); err != nil && !errors.Is(err, os.ErrNotExist) || len(contents) != 0 {
		t.Fatalf("PATH shadow executed: %q", contents)
	}
	t.Setenv(gitExecutableEnv, shadow)
	result = runGit(context.Background(), repository, []string{"--version"}, gitOutputLimit)
	if result.ok {
		t.Fatalf("explicit Git executable unexpectedly succeeded: %#v", result)
	}
	contents, err := os.ReadFile(marker)
	if err != nil || len(contents) == 0 {
		t.Fatalf("explicit Git executable was not selected: %v %q", err, contents)
	}
}

func gitForTest(t *testing.T, repository string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, arguments...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}

func shellQuoteForGitTest(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
