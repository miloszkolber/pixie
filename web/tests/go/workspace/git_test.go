package workspace_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

type codedError interface {
	ErrorCode() string
}

func runGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	command.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

func newGitFixture(t *testing.T) (*workspace.Git, workspace.Project, string) {
	t.Helper()
	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "main")
	runGit(t, repository, "config", "user.name", "Pixie test")
	runGit(t, repository, "config", "user.email", "test@pixie.test")
	policy, err := workspace.NewPathPolicy([]string{repository}, false)
	if err != nil {
		t.Fatal(err)
	}
	projects := workspace.NewProjects(persist.Store{Dir: t.TempDir()}, policy)
	project, err := projects.Open(repository)
	if err != nil {
		t.Fatal(err)
	}
	return workspace.NewGit(projects, policy), project, repository
}

func TestGitLinkedWorktreeKeepsRepositoryAndDiffScopeBoundaries(t *testing.T) {
	_, _, repository := newGitFixture(t)
	name := "shared.txt"
	if err := os.WriteFile(filepath.Join(repository, name), []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "--", name)
	runGit(t, repository, "commit", "-m", "initial")
	base := runGit(t, repository, "rev-parse", "HEAD")

	worktree := t.TempDir()
	runGit(t, repository, "worktree", "add", "-b", "linked", worktree)
	if err := os.WriteFile(filepath.Join(worktree, name), []byte("linked commit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, worktree, "add", "--", name)
	runGit(t, worktree, "commit", "-m", "linked change")
	commit := runGit(t, worktree, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(worktree, name), []byte("working tree\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	policy, err := workspace.NewPathPolicy([]string{repository, worktree}, false)
	if err != nil {
		t.Fatal(err)
	}
	projects := workspace.NewProjects(persist.Store{Dir: t.TempDir()}, policy)
	project, err := projects.Open(worktree)
	if err != nil {
		t.Fatal(err)
	}
	service := workspace.NewGit(projects, policy)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	list, err := service.ListRepositories(ctx, project.ID)
	if err != nil || !list.Complete || len(list.Repositories) != 1 {
		t.Fatalf("linked worktree discovery: %#v, %v", list, err)
	}
	if got := list.Repositories[0]; got.Root != project.Roots[0] || got.Head.Kind != "branch" || got.Head.Name != "linked" || got.Clean {
		t.Fatalf("linked worktree identity: %#v", got)
	}
	for _, check := range []struct {
		scope    workspace.GitDiffScope
		modified string
	}{
		{workspace.GitDiffScope{Kind: "commit", SHA: commit}, "linked commit\n"},
		{workspace.GitDiffScope{Kind: "pinned", BaseRef: base}, "working tree\n"},
		{workspace.GitDiffScope{Kind: "branch", BaseRef: "refs/heads/main"}, "linked commit\n"},
	} {
		status, err := service.Status(ctx, project.ID, worktree, check.scope)
		if err != nil || len(status.Changes) != 1 || status.Changes[0].Path != name {
			t.Fatalf("%s status: %#v, %v", check.scope.Kind, status, err)
		}
		preview, err := service.DiffFile(ctx, project.ID, worktree, name, check.scope)
		if err != nil || preview.Unavailable || preview.Original != "original\n" || preview.Modified != check.modified {
			t.Fatalf("%s preview: %#v, %v", check.scope.Kind, preview, err)
		}
	}
	if _, err := service.DiffFile(ctx, project.ID, repository, name, workspace.GitDiffScope{}); err == nil {
		t.Fatal("linked-worktree project exposed the primary checkout")
	}
}

func TestGitSubmoduleMetadataInspectionDoesNotRunNestedFilters(t *testing.T) {
	service, project, repository := newGitFixture(t)
	nested := filepath.Join(repository, "modules", "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, nested, "init", "-b", "main")
	runGit(t, nested, "config", "user.name", "Pixie test")
	runGit(t, nested, "config", "user.email", "test@pixie.test")
	if err := os.WriteFile(filepath.Join(nested, ".gitattributes"), []byte("*.txt filter=pixie-marker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "tracked.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, nested, "add", "--", ".gitattributes", "tracked.txt")
	runGit(t, nested, "commit", "-m", "initial")
	nestedHead := runGit(t, nested, "rev-parse", "HEAD")
	runGit(t, repository, "update-index", "--add", "--cacheinfo", "160000,"+nestedHead+",modules/nested")
	runGit(t, repository, "commit", "-m", "add submodule")
	if err := os.WriteFile(filepath.Join(nested, "tracked.txt"), []byte("committed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, nested, "add", "--", "tracked.txt")
	runGit(t, nested, "commit", "-m", "nested change")

	markerDir := t.TempDir()
	marker := filepath.Join(markerDir, "marker")
	helper := filepath.Join(markerDir, "helper.sh")
	quoteShell := func(value string) string {
		return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
	}
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf x >> "+quoteShell(marker)+"\nexit 73\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, nested, "config", "filter.pixie-marker.clean", helper)
	runGit(t, nested, "config", "filter.pixie-marker.required", "true")
	runGit(t, nested, "config", "core.fsmonitor", helper)
	if err := os.WriteFile(filepath.Join(nested, ".git", "info", "attributes"), []byte("*.txt filter=pixie-marker\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	list, err := service.ListRepositories(ctx, project.ID)
	if err != nil || len(list.Repositories) != 2 {
		t.Fatalf("submodule repository discovery: %#v, %v", list, err)
	}
	if status, err := service.Status(ctx, project.ID, repository, workspace.GitDiffScope{}); err != nil || len(status.Changes) != 1 || status.Changes[0].Path != "modules/nested" {
		t.Fatalf("changed submodule status: %#v, %v", status, err)
	}
	if status, err := service.Status(ctx, project.ID, nested, workspace.GitDiffScope{}); err != nil || !status.Clean || len(status.Changes) != 0 {
		t.Fatalf("clean nested submodule: %#v, %v", status, err)
	}
	if err := os.WriteFile(filepath.Join(nested, "tracked.txt"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(ctx, project.ID, nested, workspace.GitDiffScope{})
	if err != nil || len(status.Changes) != 1 || status.Changes[0].Path != "tracked.txt" {
		t.Fatalf("nested raw status: %#v, %v", status, err)
	}
	contents, err := os.ReadFile(marker)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(contents) != 0 {
		t.Fatalf("nested repository helper executed: %q", contents)
	}
}

func TestGitDiscoversMultipleRepositoriesBelowOneProjectRoot(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "services", "first"), filepath.Join(root, "tools", "second")
	for _, repository := range []string{first, second} {
		if err := os.MkdirAll(repository, 0o700); err != nil {
			t.Fatal(err)
		}
		runGit(t, repository, "init", "-b", "main")
	}
	policy, err := workspace.NewPathPolicy([]string{root}, false)
	if err != nil {
		t.Fatal(err)
	}
	projects := workspace.NewProjects(persist.Store{Dir: t.TempDir()}, policy)
	project, err := projects.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	list, err := workspace.NewGit(projects, policy).ListRepositories(context.Background(), project.ID)
	if err != nil || !list.Complete || len(list.Repositories) != 2 {
		t.Fatalf("nested repository discovery: %#v, %v", list, err)
	}
	if list.Repositories[0].Root != first || list.Repositories[1].Root != second {
		t.Fatalf("nested repository roots: %#v", list.Repositories)
	}
}

func TestGitPreservesOddPathsAndRejectsUnsafeOrUnreadablePreviews(t *testing.T) {
	service, project, repository := newGitFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	name := "odd\tname\nline.txt"
	if err := os.WriteFile(filepath.Join(repository, name), []byte("one\ntwo\nthree\nfour\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(ctx, project.ID, repository, workspace.GitDiffScope{})
	if err != nil || status.Head.Kind != "unborn" || len(status.Changes) != 1 || status.Changes[0].Path != name {
		t.Fatalf("unborn odd-path status: %#v, %v", status, err)
	}
	_, err = service.Status(ctx, project.ID, repository, workspace.GitDiffScope{Kind: "branch", BaseRef: "refs/heads/main"})
	var code codedError
	if !errors.As(err, &code) || code.ErrorCode() != "UNBORN_HEAD" {
		t.Fatalf("unborn branch comparison: %v", err)
	}

	runGit(t, repository, "add", "--", name)
	runGit(t, repository, "commit", "-m", "initial")
	original := name
	name = "renamed\tfile\nline.txt"
	runGit(t, repository, "mv", "--", original, name)
	if err := os.WriteFile(filepath.Join(repository, name), []byte("one\ntwo\nthree\nchanged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err = service.Status(ctx, project.ID, repository, workspace.GitDiffScope{})
	if err != nil || len(status.Changes) != 1 || status.Changes[0].Path != name || status.Changes[0].OriginalPath != original {
		t.Fatalf("rename status: %#v, %v", status, err)
	}
	preview, err := service.DiffFile(ctx, project.ID, repository, name, workspace.GitDiffScope{})
	if err != nil || preview.OriginalPath != original || preview.Original != "one\ntwo\nthree\nfour\n" || preview.Modified != "one\ntwo\nthree\nchanged\n" {
		t.Fatalf("rename preview: %#v, %v", preview, err)
	}
	runGit(t, repository, "add", "--", name)
	runGit(t, repository, "commit", "-m", "rename")
	if err := os.Symlink(name, filepath.Join(repository, "inside")); err != nil {
		t.Fatal(err)
	}
	preview, err = service.DiffFile(ctx, project.ID, repository, "inside", workspace.GitDiffScope{})
	if err != nil || preview.Unavailable || preview.Modified != name {
		t.Fatalf("internal symlink preview: %#v, %v", preview, err)
	}
	if err := os.Mkdir(filepath.Join(repository, "linked-dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("linked-dir", filepath.Join(repository, "dir-link")); err != nil {
		t.Fatal(err)
	}
	preview, err = service.DiffFile(ctx, project.ID, repository, "dir-link", workspace.GitDiffScope{})
	if err != nil || preview.Unavailable || preview.Modified != "linked-dir" {
		t.Fatalf("internal directory symlink preview: %#v, %v", preview, err)
	}
	if err := os.Symlink("missing-target", filepath.Join(repository, "dangling")); err != nil {
		t.Fatal(err)
	}
	preview, err = service.DiffFile(ctx, project.ID, repository, "dangling", workspace.GitDiffScope{})
	if err != nil || preview.Unavailable || preview.Modified != "missing-target" {
		t.Fatalf("dangling symlink preview: %#v, %v", preview, err)
	}
	if err := os.Symlink(string([]byte{0xff, 'x'}), filepath.Join(repository, "invalid-link")); err != nil {
		t.Fatal(err)
	}
	preview, err = service.DiffFile(ctx, project.ID, repository, "invalid-link", workspace.GitDiffScope{})
	if err != nil || !preview.Unavailable || !strings.Contains(preview.Message, "UTF-8") {
		t.Fatalf("invalid UTF-8 symlink preview: %#v, %v", preview, err)
	}
	history, err := service.ListCommits(ctx, project.ID, repository)
	commits, ok := history["commits"].([]workspace.GitCommit)
	if err != nil || !ok || len(commits) != 2 {
		t.Fatalf("commit history: %#v, %v", history, err)
	}

	for fileName, content := range map[string]string{
		"binary":       "before\x00after",
		"large":        strings.Repeat("x", 1024*1024+1),
		"invalid-utf8": string([]byte{0xff, 'x'}),
	} {
		if err := os.WriteFile(filepath.Join(repository, fileName), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		preview, err := service.DiffFile(ctx, project.ID, repository, fileName, workspace.GitDiffScope{})
		if err != nil || !preview.Unavailable || preview.Original != "" || preview.Modified != "" {
			t.Fatalf("%s preview: %#v, %v", fileName, preview, err)
		}
		if fileName == "binary" && !preview.Binary || fileName == "large" && !preview.TooLarge || fileName == "invalid-utf8" && !strings.Contains(preview.Message, "UTF-8") {
			t.Fatalf("%s preview reason: %#v", fileName, preview)
		}
	}
	runGit(t, repository, "add", "--", "invalid-utf8")
	runGit(t, repository, "commit", "-m", "invalid object")
	objectCommit := runGit(t, repository, "rev-parse", "HEAD")
	objectPreview, err := service.DiffFile(ctx, project.ID, repository, "invalid-utf8", workspace.GitDiffScope{Kind: "commit", SHA: objectCommit})
	if err != nil || !objectPreview.Unavailable || !strings.Contains(objectPreview.Message, "UTF-8") {
		t.Fatalf("invalid UTF-8 object preview: %#v, %v", objectPreview, err)
	}
	unicodeContent := strings.Repeat("λ", 1024*1024/len("λ"))
	if err := os.WriteFile(filepath.Join(repository, "unicode"), []byte(unicodeContent), 0o600); err != nil {
		t.Fatal(err)
	}
	preview, err = service.DiffFile(ctx, project.ID, repository, "unicode", workspace.GitDiffScope{})
	if err != nil || preview.Unavailable || preview.Modified != unicodeContent {
		t.Fatalf("exact UTF-8 byte-limit preview: %#v, %v", preview, err)
	}

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "private"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "private"), filepath.Join(repository, "outside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repository, "inside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "private"), filepath.Join(repository, "inside")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DiffFile(ctx, project.ID, repository, "inside", workspace.GitDiffScope{}); err == nil {
		t.Fatal("path-swapped Git symlink exposed an external preview")
	}
	if _, err := service.DiffFile(ctx, project.ID, repository, "outside", workspace.GitDiffScope{}); err == nil {
		t.Fatal("accepted a preview symlink escaping the repository")
	}
	if err := os.Mkdir(filepath.Join(repository, "actual"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".", filepath.Join(repository, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../private", filepath.Join(repository, "actual", "nested-outside")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DiffFile(ctx, project.ID, repository, "alias/actual/nested-outside", workspace.GitDiffScope{}); err == nil {
		t.Fatal("accepted an escaping preview symlink through a shallower resolved parent")
	}
	if err := os.Symlink(outside, filepath.Join(repository, "escape-dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("escape-dir/private", filepath.Join(repository, "chained-outside")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DiffFile(ctx, project.ID, repository, "chained-outside", workspace.GitDiffScope{}); err == nil {
		t.Fatal("accepted a preview symlink whose target chain escapes the repository")
	}
	if _, err := service.DiffFile(ctx, project.ID, repository, "../escape", workspace.GitDiffScope{}); err == nil {
		t.Fatal("accepted a lexical repository escape")
	}
}

func TestGitInspectionDoesNotRunRepositoryFiltersOrConfigHelpers(t *testing.T) {
	service, project, repository := newGitFixture(t)
	markerDir := t.TempDir()
	marker := filepath.Join(markerDir, "marker")
	helper := filepath.Join(markerDir, "helper.sh")
	quoteShell := func(value string) string {
		return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
	}
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf x >> "+quoteShell(marker)+"\nexit 73\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".gitattributes"), []byte("* filter=pixie-wildcard\n*.txt filter=pixie-marker\n*.proc filter=pixie-process\n*.probe diff=pixie-marker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "tracked.proc"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "tracked.probe"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "--", ".gitattributes", "tracked.txt", "tracked.proc", "tracked.probe")
	runGit(t, repository, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("committed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "tracked.probe"), []byte("committed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "--", "tracked.txt", "tracked.probe")
	runGit(t, repository, "commit", "-m", "second")
	secondCommit := runGit(t, repository, "rev-parse", "HEAD")
	include := filepath.Join(markerDir, "included.gitconfig")
	config := fmt.Sprintf("[filter \"pixie-wildcard\"]\n\tclean = %s\n\tprocess = %s\n\trequired = true\n[filter \"pixie-marker\"]\n\tclean = %s\n\trequired = true\n[filter \"pixie-process\"]\n\tprocess = %s\n\trequired = true\n[diff \"pixie-marker\"]\n\ttextconv = %s\n\tcommand = %s\n[core]\n\tfsmonitor = %s\n\thooksPath = %s\n[diff]\n\texternal = %s\n", quoteShell(helper), quoteShell(helper), quoteShell(helper), quoteShell(helper), quoteShell(helper), quoteShell(helper), quoteShell(helper), quoteShell(markerDir), quoteShell(helper))
	if err := os.WriteFile(include, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "config", "--local", "include.path", include)
	if err := os.WriteFile(filepath.Join(repository, ".git", "info", "attributes"), []byte("* filter=pixie-marker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "tracked.proc"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "new.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmpDir := filepath.Join(repository, "tmp")
	if err := os.Mkdir(tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmpDir)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	commitScope := workspace.GitDiffScope{Kind: "commit", SHA: secondCommit}
	commitStatus, err := service.Status(ctx, project.ID, repository, commitScope)
	if err != nil || len(commitStatus.Changes) != 2 || commitStatus.Changes[0].Path != "tracked.probe" || commitStatus.Changes[1].Path != "tracked.txt" {
		t.Fatalf("safe commit status: %#v, %v", commitStatus, err)
	}
	branchStatus, err := service.Status(ctx, project.ID, repository, workspace.GitDiffScope{Kind: "branch", BaseRef: "refs/heads/main"})
	if err != nil || len(branchStatus.Changes) != 0 {
		t.Fatalf("safe branch status: %#v, %v", branchStatus, err)
	}
	commitPreview, err := service.DiffFile(ctx, project.ID, repository, "tracked.txt", commitScope)
	if err != nil || commitPreview.Unavailable || commitPreview.Message != "" || commitPreview.Original != "before\n" || commitPreview.Modified != "committed\n" {
		t.Fatalf("safe commit preview: %#v, %v", commitPreview, err)
	}
	if _, err := service.ListRepositories(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(ctx, project.ID, repository, workspace.GitDiffScope{})
	if err != nil || len(status.Changes) != 3 {
		t.Fatalf("safe status: %#v, %v", status, err)
	}
	if status.Changes[0].Path != "new.txt" || status.Changes[0].Status != "untracked" || status.Changes[0].Added == nil || *status.Changes[0].Added != 1 || status.Changes[1].Path != "tracked.proc" || status.Changes[1].Status != "modified" || status.Changes[1].Added == nil || *status.Changes[1].Added != 1 || status.Changes[1].Removed == nil || *status.Changes[1].Removed != 1 || status.Changes[2].Path != "tracked.txt" || status.Changes[2].Status != "modified" || status.Changes[2].Added == nil || *status.Changes[2].Added != 1 || status.Changes[2].Removed == nil || *status.Changes[2].Removed != 1 {
		t.Fatalf("safe status details: %#v", status.Changes)
	}
	preview, err := service.DiffFile(ctx, project.ID, repository, "tracked.txt", workspace.GitDiffScope{})
	if err != nil || preview.Unavailable || preview.Original != "committed\n" || preview.Modified != "after\n" || preview.Message == "" {
		t.Fatalf("safe preview: %#v, %v", preview, err)
	}
	if _, err := service.ListCommits(ctx, project.ID, repository); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListBranches(ctx, project.ID, repository); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(marker)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(contents) != 0 {
		t.Fatalf("repository helper executed: %q", contents)
	}
}

func TestGitRawFileCountsCannotDiscoverRepositoryFromTempDirectory(t *testing.T) {
	service, project, repository := newGitFixture(t)
	markerDir := t.TempDir()
	marker := filepath.Join(markerDir, "marker")
	helper := filepath.Join(markerDir, "helper.sh")
	quoteShell := func(value string) string {
		return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
	}
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf x >> "+quoteShell(marker)+"\nexit 73\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".gitattributes"), []byte("* filter=pixie-wildcard\n*.txt filter=pixie-clean\n*.proc filter=pixie-process\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tracked.txt", "tracked.proc"} {
		if err := os.WriteFile(filepath.Join(repository, name), []byte("before\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, repository, "add", "--", ".gitattributes", "tracked.txt", "tracked.proc")
	runGit(t, repository, "commit", "-m", "initial")
	include := filepath.Join(markerDir, "included.gitconfig")
	config := fmt.Sprintf("[filter \"pixie-wildcard\"]\n\tclean = %s\n\tprocess = %s\n\trequired = true\n[filter \"pixie-clean\"]\n\tclean = %s\n\trequired = true\n[filter \"pixie-process\"]\n\tprocess = %s\n\trequired = true\n[diff]\n\texternal = %s\n", quoteShell(helper), quoteShell(helper), quoteShell(helper), quoteShell(helper), quoteShell(helper))
	if err := os.WriteFile(include, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "config", "--local", "include.path", include)
	if err := os.WriteFile(filepath.Join(repository, ".git", "info", "attributes"), []byte("* filter=pixie-wildcard\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tracked.txt", "tracked.proc"} {
		if err := os.WriteFile(filepath.Join(repository, name), []byte("after\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tmpDir := filepath.Join(repository, "tmp")
	if err := os.Mkdir(tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmpDir)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, err := service.Status(ctx, project.ID, repository, workspace.GitDiffScope{})
	if err != nil || len(status.Changes) != 2 {
		t.Fatalf("raw status from repository temp directory: %#v, %v", status, err)
	}
	for _, change := range status.Changes {
		if change.Status != "modified" || change.Added == nil || *change.Added != 1 || change.Removed == nil || *change.Removed != 1 {
			t.Fatalf("raw numstat from repository temp directory: %#v", status.Changes)
		}
	}
	preview, err := service.DiffFile(ctx, project.ID, repository, "tracked.txt", workspace.GitDiffScope{})
	if err != nil || preview.Unavailable || preview.Original != "before\n" || preview.Modified != "after\n" || preview.Message == "" {
		t.Fatalf("raw preview from repository temp directory: %#v, %v", preview, err)
	}
	contents, err := os.ReadFile(marker)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(contents) != 0 {
		t.Fatalf("repository filter helper executed from rawFileCounts: %q", contents)
	}
}

func TestGitRawStatusPreservesIndexAndWorktreeSemantics(t *testing.T) {
	service, project, repository := newGitFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, name := range []string{"staged-delete", "rename-source", "mode", "assume", "skip"} {
		if err := os.WriteFile(filepath.Join(repository, name), []byte("base\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, repository, "add", "--", "staged-delete", "rename-source", "mode", "assume", "skip")
	runGit(t, repository, "commit", "-m", "initial")

	if err := os.WriteFile(filepath.Join(repository, "staged-add"), []byte("added\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "--", "staged-add")
	runGit(t, repository, "rm", "--cached", "staged-delete")
	if err := os.WriteFile(filepath.Join(repository, "staged-delete"), []byte("restored but untracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "mv", "--", "rename-source", "rename-destination")
	if err := os.WriteFile(filepath.Join(repository, "rename-destination"), []byte("renamed and changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runGit(t, repository, "config", "core.fileMode", "false")
	if err := os.Chmod(filepath.Join(repository, "mode"), 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "update-index", "--assume-unchanged", "assume")
	if err := os.Remove(filepath.Join(repository, "assume")); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "update-index", "--skip-worktree", "skip")
	if err := os.WriteFile(filepath.Join(repository, "skip"), []byte("ignored skip-worktree bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	status, err := service.Status(ctx, project.ID, repository, workspace.GitDiffScope{})
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Changes) != 3 {
		t.Fatalf("index/worktree status: %#v", status)
	}
	changes := map[string]workspace.GitFileChange{}
	for _, change := range status.Changes {
		changes[change.Path] = change
	}
	if changes["staged-add"].Status != "added" || changes["staged-delete"].Status != "deleted" {
		t.Fatalf("staged add/delete status: %#v", changes)
	}
	rename := changes["rename-destination"]
	if rename.Status != "renamed" || rename.OriginalPath != "rename-source" {
		t.Fatalf("staged rename status: %#v", rename)
	}
	deletedPreview, err := service.DiffFile(ctx, project.ID, repository, "staged-delete", workspace.GitDiffScope{})
	if err != nil || deletedPreview.Unavailable || deletedPreview.Original != "base\n" || deletedPreview.Modified != "" {
		t.Fatalf("staged deletion preview: %#v, %v", deletedPreview, err)
	}
	for _, name := range []string{"mode", "assume", "skip"} {
		if _, ok := changes[name]; ok {
			t.Fatalf("native-suppressed path reported as changed: %s (%#v)", name, changes[name])
		}
	}
	runGit(t, repository, "config", "core.fileMode", "true")
	status, err = service.Status(ctx, project.ID, repository, workspace.GitDiffScope{})
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range status.Changes {
		if change.Path == "mode" && change.Status == "modified" {
			return
		}
	}
	t.Fatalf("core.fileMode=true did not report executable-bit change: %#v", status.Changes)
}

func TestGitRawStatusDoesNotInferRenamesForUntrackedDestinations(t *testing.T) {
	service, project, repository := newGitFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := os.WriteFile(filepath.Join(repository, "tracked"), []byte("same bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "--", "tracked")
	runGit(t, repository, "commit", "-m", "initial")
	if err := os.Remove(filepath.Join(repository, "tracked")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "untracked-destination"), []byte("same bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	status, err := service.Status(ctx, project.ID, repository, workspace.GitDiffScope{})
	if err != nil || len(status.Changes) != 2 {
		t.Fatalf("deleted plus untracked status: %#v, %v", status, err)
	}
	changes := map[string]workspace.GitFileChange{}
	for _, change := range status.Changes {
		changes[change.Path] = change
	}
	if changes["tracked"].Status != "deleted" || changes["untracked-destination"].Status != "untracked" {
		t.Fatalf("deleted plus untracked status details: %#v", changes)
	}
	preview, err := service.DiffFile(ctx, project.ID, repository, "untracked-destination", workspace.GitDiffScope{})
	if err != nil || preview.OriginalPath != "" || preview.Modified != "same bytes\n" {
		t.Fatalf("untracked destination preview: %#v, %v", preview, err)
	}
}

func TestGitRawStatusReportsBoundedComparisonWarnings(t *testing.T) {
	service, project, repository := newGitFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	large := strings.Repeat("x", 4*1024*1024+1)
	if err := os.WriteFile(filepath.Join(repository, "large"), []byte(large), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "--", "large")
	runGit(t, repository, "commit", "-m", "large")

	status, err := service.Status(ctx, project.ID, repository, workspace.GitDiffScope{})
	if err != nil || status.Clean || len(status.Changes) != 1 || status.Changes[0].Path != "large" {
		t.Fatalf("bounded raw status: %#v, %v", status, err)
	}
	if len(status.Warnings) != 2 || !strings.Contains(strings.Join(status.Warnings, "\n"), "4 MiB") || !strings.Contains(strings.Join(status.Warnings, "\n"), "64 MiB") || !strings.Contains(strings.Join(status.Warnings, "\n"), "Git clean/process") {
		t.Fatalf("bounded raw warning: %#v", status.Warnings)
	}
}

func TestGitRawStatusIncludesStagedContentOnlyModeOnlyAndGitlink(t *testing.T) {
	service, project, repository := newGitFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := os.WriteFile(filepath.Join(repository, "staged-content"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "staged-mode"), []byte("mode\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "--", "staged-content", "staged-mode")
	runGit(t, repository, "commit", "-m", "initial")

	nested := filepath.Join(repository, "modules", "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, nested, "init", "-b", "main")
	runGit(t, nested, "config", "user.name", "Pixie test")
	runGit(t, nested, "config", "user.email", "test@pixie.test")
	if err := os.WriteFile(filepath.Join(nested, "file"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, nested, "add", "--", "file")
	runGit(t, nested, "commit", "-m", "old")
	oldNestedHead := runGit(t, nested, "rev-parse", "HEAD")
	runGit(t, repository, "update-index", "--add", "--cacheinfo", "160000,"+oldNestedHead+",modules/nested")
	runGit(t, repository, "commit", "-m", "submodule")
	if err := os.WriteFile(filepath.Join(nested, "file"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, nested, "add", "--", "file")
	runGit(t, nested, "commit", "-m", "new")
	newNestedHead := runGit(t, nested, "rev-parse", "HEAD")
	runGit(t, repository, "update-index", "--add", "--cacheinfo", "160000,"+newNestedHead+",modules/nested")
	// Leave the checkout at the old gitlink commit. The parent index is the only
	// place containing the staged new gitlink value.
	runGit(t, nested, "checkout", "--detach", oldNestedHead)

	if err := os.WriteFile(filepath.Join(repository, "staged-content"), []byte("index-staged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "--", "staged-content")
	if err := os.WriteFile(filepath.Join(repository, "staged-content"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(repository, "staged-mode"), 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "config", "core.fileMode", "false")
	runGit(t, repository, "update-index", "--chmod=+x", "--", "staged-mode")
	if err := os.Chmod(filepath.Join(repository, "staged-mode"), 0o600); err != nil {
		t.Fatal(err)
	}

	status, err := service.Status(ctx, project.ID, repository, workspace.GitDiffScope{})
	if err != nil {
		t.Fatal(err)
	}
	changes := map[string]workspace.GitFileChange{}
	for _, change := range status.Changes {
		changes[change.Path] = change
	}
	for _, name := range []string{"staged-content", "staged-mode", "modules/nested"} {
		if change, ok := changes[name]; !ok || change.Status != "modified" {
			t.Fatalf("staged-only %s status: %#v", name, status)
		}
	}
	preview, err := service.DiffFile(ctx, project.ID, repository, "staged-content", workspace.GitDiffScope{})
	if err != nil || preview.Unavailable || preview.Original != "base\n" || preview.Modified != "index-staged\n" {
		t.Fatalf("staged index content preview: %#v, %v", preview, err)
	}
	modePreview, err := service.DiffFile(ctx, project.ID, repository, "staged-mode", workspace.GitDiffScope{})
	if err != nil || modePreview.Unavailable || modePreview.Original != "mode\n" || modePreview.Modified != "mode\n" {
		t.Fatalf("staged index mode preview: %#v, %v", modePreview, err)
	}
}

func TestGitSubmoduleGitfileMetadataIsValidatedWithoutNetwork(t *testing.T) {
	service, project, repository := newGitFixture(t)
	source := t.TempDir()
	runGit(t, source, "init", "-b", "main")
	runGit(t, source, "config", "user.name", "Pixie test")
	runGit(t, source, "config", "user.email", "test@pixie.test")
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "--", "file")
	runGit(t, source, "commit", "-m", "source")
	nested := filepath.Join(repository, "modules", "nested")
	gitDir := filepath.Join(repository, ".git", "modules", "nested")
	if err := os.MkdirAll(filepath.Dir(gitDir), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "clone", "--separate-git-dir", gitDir, source, nested)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git clone --separate-git-dir: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(nested, ".git"), []byte("gitdir: ../../.git/modules/nested\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nestedHead := runGit(t, nested, "rev-parse", "HEAD")
	runGit(t, repository, "update-index", "--add", "--cacheinfo", "160000,"+nestedHead+",modules/nested")
	runGit(t, repository, "commit", "-m", "submodule")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, err := service.Status(ctx, project.ID, repository, workspace.GitDiffScope{})
	if err != nil || !status.Clean || len(status.Changes) != 0 {
		t.Fatalf("valid gitfile submodule status: %#v, %v", status, err)
	}
	if len(status.Warnings) != 1 || status.Warnings[0] != "Showing raw worktree bytes; Git clean/process and LFS conversion are not applied" {
		t.Fatalf("valid gitfile submodule warnings: %#v", status.Warnings)
	}
	nestedStatus, err := service.Status(ctx, project.ID, nested, workspace.GitDiffScope{})
	if err != nil || !nestedStatus.Clean || len(nestedStatus.Changes) != 0 {
		t.Fatalf("valid gitfile nested repository status: %#v, %v", nestedStatus, err)
	}
	if err := os.WriteFile(filepath.Join(nested, ".git"), []byte("gitdir: /outside/not-admitted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err = service.Status(ctx, project.ID, repository, workspace.GitDiffScope{})
	if err != nil || status.Clean || len(status.Changes) != 1 || status.Changes[0].Path != "modules/nested" {
		t.Fatalf("invalid gitfile submodule status: %#v, %v", status, err)
	}
	if len(status.Warnings) != 2 || !strings.Contains(strings.Join(status.Warnings, "\n"), "submodule metadata was missing or invalid") || !strings.Contains(strings.Join(status.Warnings, "\n"), "Git clean/process") {
		t.Fatalf("invalid gitfile submodule warning: %#v", status.Warnings)
	}
}
