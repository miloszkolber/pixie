package workspace_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

func TestProjectsCWDAdmitsUngroupedSessionsWithoutProject(t *testing.T) {
	mount := t.TempDir()
	work := filepath.Join(mount, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	policy, err := workspace.NewPathPolicy([]string{mount}, false)
	if err != nil {
		t.Fatal(err)
	}
	projects := workspace.NewProjects(persist.Store{Dir: t.TempDir()}, policy)

	admitted, err := projects.AssertCWD("", work)
	if err != nil {
		t.Fatalf("ungrouped cwd was not admitted: %v", err)
	}
	if admitted != work {
		t.Fatalf("ungrouped cwd was not preserved: %q", admitted)
	}
	if _, err := projects.AssertCWD("", ""); err == nil {
		t.Fatal("ungrouped session accepted a missing cwd")
	}
	if _, err := projects.AssertCWD("", outside); err == nil {
		t.Fatal("ungrouped session admitted a directory outside its mount")
	}
	if _, err := projects.AssertCWD("missing-project", work); err == nil {
		t.Fatal("grouped cwd accepted an unknown project")
	}
}

func TestProjectsCWDSeparatesAdmissionFromGrouping(t *testing.T) {
	mount := t.TempDir()
	first, second := filepath.Join(mount, "first"), filepath.Join(mount, "second")
	sub := filepath.Join(first, "sub")
	for _, path := range []string{sub, second} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	outside := t.TempDir()
	policy, err := workspace.NewPathPolicy([]string{mount}, false)
	if err != nil {
		t.Fatal(err)
	}
	projects := workspace.NewProjects(persist.Store{Dir: t.TempDir()}, policy)
	firstProject, err := projects.Open(first)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := projects.AssertCWD(firstProject.ID, sub); err != nil {
		t.Fatalf("cwd inside the project root was rejected: %v", err)
	}
	if _, err := projects.AssertCWD(firstProject.ID, second); err == nil || !strings.Contains(err.Error(), "outside the project root") {
		t.Fatalf("sibling mount directory was not rejected as grouping: %v", err)
	}
	if _, err := projects.AssertCWD(firstProject.ID, outside); err == nil || strings.Contains(err.Error(), "outside the project root") {
		t.Fatalf("directory outside admission did not fail at admission: %v", err)
	}
}

func TestProjectsAssertRootAlwaysAdmits(t *testing.T) {
	mount := t.TempDir()
	first, second := filepath.Join(mount, "first"), filepath.Join(mount, "second")
	for _, path := range []string{first, second} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	outside := t.TempDir()
	policy, err := workspace.NewPathPolicy([]string{mount}, false)
	if err != nil {
		t.Fatal(err)
	}
	projects := workspace.NewProjects(persist.Store{Dir: t.TempDir()}, policy)
	project, err := projects.Open(first)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := projects.AssertRoot(project.ID, project.Roots[0]); err != nil {
		t.Fatalf("exact admitted root was rejected: %v", err)
	}
	if _, err := projects.AssertRoot(project.ID, second); err == nil {
		t.Fatal("foreign project root was accepted")
	}
	if _, err := projects.AssertRoot(project.ID, ""); err == nil {
		t.Fatal("empty project root was accepted")
	}
	if _, err := projects.AssertRoot(project.ID, outside); err == nil {
		t.Fatal("root outside admission was accepted")
	}
	if _, err := projects.AssertRoot("missing-project", project.Roots[0]); err == nil {
		t.Fatal("unknown project root was accepted")
	}
}
