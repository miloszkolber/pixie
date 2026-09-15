package workspace_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/workspace"
)

// A write/edit tool call gets a per-turn diff from the path-scoped comparison,
// and no write authority is added: the diff only reads the worktree and index.
func TestTurnDiffForToolReturnsPathScopedWriteEditDiff(t *testing.T) {
	service, project, repository := newGitFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	name := "notes.txt"
	if err := os.WriteFile(filepath.Join(repository, name), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "--", name)
	runGit(t, repository, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(repository, name), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tool := range []string{"write", "edit"} {
		diff, err := service.TurnDiffForTool(ctx, project.ID, repository, tool, name, workspace.GitDiffScope{})
		if err != nil || !diff.Available || diff.Original != "before\n" || diff.Modified != "after\n" || diff.Fallback {
			t.Fatalf("%s turn diff: %#v, %v", tool, diff, err)
		}
	}
	if _, err := service.TurnDiffForTool(ctx, project.ID, repository, "delete", name, workspace.GitDiffScope{}); err == nil {
		t.Fatal("non write/edit tool was accepted")
	}
	if _, err := service.TurnDiffForTool(ctx, project.ID, repository, "edit", filepath.Join(repository, "..", "escape.txt"), workspace.GitDiffScope{}); !workspace.IsTraversal(err) {
		t.Fatalf("escaping turn diff = %v, want typed traversal", err)
	}
}

// A Git execution failure must render as a bounded result, not an error or a 500.
func TestTurnDiffForToolRendersGitFailureAsResult(t *testing.T) {
	service, project, repository := newGitFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	name := "file.txt"
	if err := os.WriteFile(filepath.Join(repository, name), []byte("content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Prime the repository discovery cache while real Git still works.
	if _, err := service.ListRepositories(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(t.TempDir(), "git-failure.sh")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexit 73\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIXIE_GIT_EXECUTABLE", wrapper)

	diff, err := service.TurnDiffForTool(ctx, project.ID, repository, "edit", name, workspace.GitDiffScope{})
	if err != nil {
		t.Fatalf("Git failure surfaced as an error: %v", err)
	}
	if diff.Available || diff.Message == "" {
		t.Fatalf("Git failure did not render an unavailable result: %#v", diff)
	}
	for _, forbidden := range []string{repository, wrapper, "exit 73"} {
		if strings.Contains(diff.Message, forbidden) {
			t.Fatalf("turn diff message leaked %q: %q", forbidden, diff.Message)
		}
	}
}
