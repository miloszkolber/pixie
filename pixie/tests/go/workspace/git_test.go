package workspace_test

import (
	"context"
	"errors"
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
	history, err := service.ListCommits(ctx, project.ID, repository)
	commits, ok := history["commits"].([]workspace.GitCommit)
	if err != nil || !ok || len(commits) != 2 {
		t.Fatalf("commit history: %#v, %v", history, err)
	}

	for fileName, content := range map[string]string{
		"binary": "before\x00after",
		"large":  strings.Repeat("x", 1024*1024+1),
	} {
		if err := os.WriteFile(filepath.Join(repository, fileName), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		preview, err := service.DiffFile(ctx, project.ID, repository, fileName, workspace.GitDiffScope{})
		if err != nil || !preview.Unavailable || preview.Original != "" || preview.Modified != "" {
			t.Fatalf("%s preview: %#v, %v", fileName, preview, err)
		}
		if fileName == "binary" && !preview.Binary || fileName == "large" && !preview.TooLarge {
			t.Fatalf("%s preview reason: %#v", fileName, preview)
		}
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
