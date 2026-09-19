package controller_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

func turnDiffGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}

// turnDiffControllerFixture builds a real read-only Git repository and the
// browser handler over it. No write path is added.
func turnDiffControllerFixture(t *testing.T) (controller.CoreHandler, workspace.Project, string) {
	t.Helper()
	repository := t.TempDir()
	turnDiffGit(t, repository, "init", "-b", "main")
	turnDiffGit(t, repository, "config", "user.name", "Pixie test")
	turnDiffGit(t, repository, "config", "user.email", "test@pixie.test")
	policy, err := workspace.NewPathPolicy([]string{repository}, false)
	if err != nil {
		t.Fatal(err)
	}
	projects := workspace.NewProjects(persist.Store{Dir: t.TempDir()}, policy)
	project, err := projects.Open(repository)
	if err != nil {
		t.Fatal(err)
	}
	return controller.CoreHandler{Git: workspace.NewGit(projects, policy)}, project, repository
}

func turnDiffParams(t *testing.T, projectID, repository, toolName, path string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"projectId":  projectID,
		"repository": repository,
		"toolName":   toolName,
		"path":       path,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// AUX-21: the browser route exposes the per-turn diff for write/edit tool calls
// and a containment denial fails closed with the shared typed code.
func TestGitTurnDiffRouteReturnsResultAndTypedTraversal(t *testing.T) {
	handler, project, repository := turnDiffControllerFixture(t)
	name := "notes.txt"
	if err := os.WriteFile(filepath.Join(repository, name), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	turnDiffGit(t, repository, "add", "--", name)
	turnDiffGit(t, repository, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(repository, name), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := handler.Handle(t.Context(), "git.turnDiff", turnDiffParams(t, project.ID, repository, "edit", name), "client")
	if err != nil {
		t.Fatalf("turn diff route failed: %v", err)
	}
	diff, ok := result.(workspace.TurnDiff)
	if !ok || !diff.Available || diff.Original != "before\n" || diff.Modified != "after\n" {
		t.Fatalf("turn diff result = %#v", result)
	}

	_, err = handler.Handle(t.Context(), "git.turnDiff", turnDiffParams(t, project.ID, repository, "edit", "../escape.txt"), "client")
	if err == nil || !workspace.IsTraversal(err) {
		t.Fatalf("escaping turn diff = %v, want typed traversal", err)
	}
	var coded interface{ ErrorCode() string }
	if !errors.As(err, &coded) || coded.ErrorCode() != "PATH_ESCAPES_PROJECT_ROOT" {
		t.Fatalf("traversal code = %v", err)
	}
}

// AUX-21: a Git execution failure is returned as a bounded unavailable result,
// never as a request error that would surface to the browser as a 500.
func TestGitTurnDiffRouteRendersGitFailureAsResult(t *testing.T) {
	handler, project, repository := turnDiffControllerFixture(t)
	name := "file.txt"
	if err := os.WriteFile(filepath.Join(repository, name), []byte("content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Prime repository discovery while real Git still works.
	if _, err := handler.Handle(t.Context(), "git.listRepositories", json.RawMessage(`{"projectId":"`+project.ID+`"}`), "client"); err != nil {
		t.Fatalf("prime Git discovery: %v", err)
	}
	wrapper := filepath.Join(t.TempDir(), "git-failure.sh")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexit 73\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIXIE_GIT_EXECUTABLE", wrapper)

	result, err := handler.Handle(t.Context(), "git.turnDiff", turnDiffParams(t, project.ID, repository, "edit", name), "client")
	if err != nil {
		t.Fatalf("Git failure surfaced as a request error: %v", err)
	}
	diff, ok := result.(workspace.TurnDiff)
	if !ok || diff.Available || diff.Message == "" {
		t.Fatalf("Git failure did not render an unavailable result: %#v", result)
	}
	for _, forbidden := range []string{repository, wrapper, "exit 73"} {
		if strings.Contains(diff.Message, forbidden) {
			t.Fatalf("turn diff message leaked %q: %q", forbidden, diff.Message)
		}
	}
}
