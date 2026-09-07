package workspace_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

func TestProjectWatchTracksItsSoleRootAndInvalidatesGitDiscovery(t *testing.T) {
	mount := t.TempDir()
	first := filepath.Join(mount, "first")
	for _, path := range []string{filepath.Join(first, "existing")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := workspace.NewPathPolicy([]string{mount}, false)
	if err != nil {
		t.Fatal(err)
	}
	projects := workspace.NewProjects(persist.Store{Dir: t.TempDir()}, policy)
	project, err := projects.Open(first)
	if err != nil {
		t.Fatal(err)
	}
	first = project.Roots[0]
	git := workspace.NewGit(projects, policy)
	if repositories, err := git.ListRepositories(context.Background(), project.ID); err != nil || len(repositories.Repositories) != 0 {
		t.Fatalf("initial repository discovery: %#v, %v", repositories, err)
	}
	events := make(chan workspace.ProjectFsChanged, 64)
	watches := workspace.NewProjectWatches(projects, git, func(channel string, value any) {
		if channel == "project.fsChanged" {
			events <- value.(workspace.ProjectFsChanged)
		}
	})
	defer watches.Close()
	if started, err := watches.Ensure(project.ID); err != nil || !started {
		t.Fatalf("watch start: %v, %v", started, err)
	}
	if started, err := watches.Ensure(project.ID); err != nil || started {
		t.Fatalf("duplicate watch: %v, %v", started, err)
	}

	waitFor := func(matches func(workspace.ProjectFsChange) bool) {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case event := <-events:
				if event.ProjectID != project.ID || len(event.Changes) > 500 {
					t.Fatalf("invalid filesystem event: %#v", event)
				}
				for _, change := range event.Changes {
					if matches(change) {
						return
					}
				}
			case <-timer.C:
				t.Fatal("timed out waiting for a filesystem event")
			}
		}
	}
	if err := os.WriteFile(filepath.Join(first, "existing", "deep.txt"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(func(change workspace.ProjectFsChange) bool {
		return change.Root == first && change.Path == "existing/deep.txt"
	})
	runGit(t, first, "init", "-b", "main")
	waitFor(func(change workspace.ProjectFsChange) bool {
		return change.Root == first && (change.Path == ".git" || strings.HasPrefix(change.Path, ".git/"))
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		repositories, err := git.ListRepositories(context.Background(), project.ID)
		if err == nil && len(repositories.Repositories) == 1 && repositories.Repositories[0].Root == first {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Git discovery stayed stale after topology event: %#v, %v", repositories, err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := os.WriteFile(filepath.Join(first, "kept.txt"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			for _, change := range event.Changes {
				if change.Root == first && change.Path == "kept.txt" {
					watches.Stop(project.ID)
					return
				}
			}
		case <-timer.C:
			t.Fatal("kept root stopped emitting after reconciliation")
		}
	}
}
