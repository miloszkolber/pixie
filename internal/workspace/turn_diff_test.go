package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitDiffHeadFallbackReturnsPatch(t *testing.T) {
	repository := t.TempDir()
	gitForTest(t, repository, "init", "-b", "main")
	gitForTest(t, repository, "config", "user.name", "Pixie test")
	gitForTest(t, repository, "config", "user.email", "test@pixie.test")
	name := "file.txt"
	if err := os.WriteFile(filepath.Join(repository, name), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitForTest(t, repository, "add", "--", name)
	gitForTest(t, repository, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(repository, name), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := (&Git{}).gitDiffHeadFallback(context.Background(), repository, name)
	if err != nil || !result.Available || !result.Fallback || !strings.Contains(result.Patch, "after") {
		t.Fatalf("Git diff HEAD fallback = %#v, %v", result, err)
	}
}

func TestGitDiffHeadFallbackRendersFailureAsBoundedResult(t *testing.T) {
	repository := t.TempDir()
	wrapper := filepath.Join(t.TempDir(), "git-failure.sh")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexit 73\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(gitExecutableEnv, wrapper)

	result, err := (&Git{}).gitDiffHeadFallback(context.Background(), repository, "file.txt")
	if err != nil {
		t.Fatalf("Git failure surfaced as an error: %v", err)
	}
	if result.Available || !result.Fallback || result.Message == "" {
		t.Fatalf("Git failure did not render an unavailable result: %#v", result)
	}
	if strings.Contains(result.Message, repository) || strings.Contains(result.Message, wrapper) {
		t.Fatalf("fallback message leaked a path: %q", result.Message)
	}
}
