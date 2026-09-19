package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

func TestProjectsRemovePreservesConversationsAndDraftsByKey(t *testing.T) {
	mount := t.TempDir()
	alpha, beta := filepath.Join(mount, "alpha"), filepath.Join(mount, "beta")
	for _, path := range []string{alpha, beta} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := workspace.NewPathPolicy([]string{mount}, false)
	if err != nil {
		t.Fatal(err)
	}
	projects := workspace.NewProjects(persist.Store{Dir: t.TempDir()}, policy)
	alphaProject, err := projects.Open(alpha)
	if err != nil {
		t.Fatal(err)
	}
	betaProject, err := projects.Open(beta)
	if err != nil {
		t.Fatal(err)
	}
	alphaCWD, err := projects.AssertCWD(alphaProject.ID, alpha)
	if err != nil {
		t.Fatal(err)
	}
	betaCWD, err := projects.AssertCWD(betaProject.ID, beta)
	if err != nil {
		t.Fatal(err)
	}

	alphaKey, err := persist.ProjectScopedDraftKey(alphaProject.ID, "session-alpha")
	if err != nil {
		t.Fatal(err)
	}
	betaKey, err := persist.ProjectScopedDraftKey(betaProject.ID, "session-beta")
	if err != nil {
		t.Fatal(err)
	}
	legacy := map[string]string{alphaKey: "unsent alpha", betaKey: "unsent beta"}
	migrated, err := persist.MigrateDraftsBySessionKey(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if migrated["session-alpha"] != "unsent alpha" || migrated["session-beta"] != "unsent beta" {
		t.Fatalf("drafts did not migrate by session key: %#v", migrated)
	}

	removed, err := projects.Remove(alphaProject.ID)
	if err != nil || removed.ID != alphaProject.ID {
		t.Fatalf("remove project: %#v, %v", removed, err)
	}
	if _, err := projects.Get(alphaProject.ID); err == nil {
		t.Fatal("removed project is still listed as grouped state")
	}
	if _, err := projects.Get(betaProject.ID); err != nil {
		t.Fatalf("removing one project disturbed another grouping: %v", err)
	}
	listed, err := projects.List(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range listed {
		if project.ID == alphaProject.ID {
			t.Fatal("removed project still appears in the catalog")
		}
	}

	// Conversations survive as ungrouped sessions: filesystem admission is
	// independent of the deleted grouping, and drafts stay keyed by session.
	if _, err := policy.Directory(alphaCWD, "Session directory"); err != nil {
		t.Fatalf("conversation directory lost admission after project removal: %v", err)
	}
	if _, err := projects.AssertCWD("", alphaCWD); err != nil {
		t.Fatalf("conversation did not survive as ungrouped: %v", err)
	}
	if _, err := projects.AssertCWD(betaProject.ID, betaCWD); err != nil {
		t.Fatalf("surviving project lost its cwd: %v", err)
	}
	if migrated["session-alpha"] != "unsent alpha" {
		t.Fatal("draft for the removed project was deleted with its grouping")
	}
	if _, err := projects.Remove(alphaProject.ID); err == nil {
		t.Fatal("second removal of the same project succeeded")
	}
}

func TestPersistProjectGroupingValidatesNullableGrouping(t *testing.T) {
	if !persist.IsUngroupedProjectID("") {
		t.Fatal("empty project id must mean ungrouped")
	}
	if persist.IsUngroupedProjectID("alpha") {
		t.Fatal("grouped project id reported as ungrouped")
	}
	for _, id := range []string{"", "alpha", "550e8400-e29b-41d4-a716-446655440000"} {
		if err := persist.ValidateOptionalProjectID(id); err != nil {
			t.Fatalf("valid optional project id rejected: %q: %v", id, err)
		}
	}
	for _, id := range []string{"a/b", "a\\b", " padded ", "a\x00b"} {
		if err := persist.ValidateOptionalProjectID(id); err == nil {
			t.Fatalf("invalid project id accepted: %q", id)
		}
	}
	if _, err := persist.DraftKeyForSession(""); err == nil {
		t.Fatal("draft key accepted an empty session")
	}
	key, err := persist.DraftKeyForSession("session-one")
	if err != nil || key != "session-one" {
		t.Fatalf("draft key is not session-scoped: %q, %v", key, err)
	}

	first, err := persist.ProjectScopedDraftKey("project-a", "session-dup")
	if err != nil {
		t.Fatal(err)
	}
	second, err := persist.ProjectScopedDraftKey("project-b", "session-dup")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persist.MigrateDraftsBySessionKey(map[string]string{first: "same", second: "same"}); err != nil {
		t.Fatalf("identical duplicates should fold: %v", err)
	}
	if _, err := persist.MigrateDraftsBySessionKey(map[string]string{first: "one", second: "two"}); err == nil {
		t.Fatal("conflicting drafts merged instead of conflicting")
	}
}
