package controller_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

func TestRuntimeMigratesLegacyProjectRootsAndDurableSessionState(t *testing.T) {
	mount := t.TempDir()
	first, nested := filepath.Join(mount, "project"), filepath.Join(mount, "project", "nested")
	for _, path := range []string{filepath.Join(first, "work"), filepath.Join(nested, "work")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := workspace.NewPathPolicy([]string{mount}, false)
	if err != nil {
		t.Fatal(err)
	}
	store := persist.Store{Dir: t.TempDir()}
	writeLegacyProjectRoots(t, store, "legacy", first, nested)
	records := controller.NewSessionRecords(store)
	for _, record := range []controller.ProjectSessionRecord{
		{ProjectID: "legacy", SessionID: "first", CWD: filepath.Join(first, "work")},
		{ProjectID: "legacy", SessionID: "extra", CWD: filepath.Join(nested, "work")},
		// Associations cover archived sessions too. The archived status itself is owned by Pi.
		{ProjectID: "legacy", SessionID: "archived", CWD: filepath.Join(nested, "work")},
	} {
		if err := records.Record(record); err != nil {
			t.Fatal(err)
		}
	}
	writeLegacyQueues(t, store, "legacy", "first", "extra", "archived")
	deletions := controller.NewSessionDeletions(store)
	binding := "sha256:" + strings.Repeat("a", 64)
	if err := deletions.Request("legacy", "extra", binding); err != nil {
		t.Fatal(err)
	}
	if err := deletions.Request("legacy", "archived", binding); err != nil {
		t.Fatal(err)
	}
	if err := deletions.Confirm("legacy", "archived"); err != nil {
		t.Fatal(err)
	}
	objectives := controller.NewObjectives(store)
	for _, sessionID := range []string{"first", "extra", "archived"} {
		goal := "Preserve " + sessionID
		tasks := []controller.SessionTask{{ID: "task-" + sessionID, Text: "Keep state", Status: "pending"}}
		if _, err := objectives.Update("legacy", sessionID, &goal, &tasks); err != nil {
			t.Fatal(err)
		}
	}

	runtime := newMigrationRuntime(t, store.Dir, policy)
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	normalized := workspace.NewProjects(store, policy)
	projects, err := normalized.List(true)
	if err != nil || len(projects) != 2 {
		t.Fatalf("normalized projects: %#v, %v", projects, err)
	}
	extraID := ""
	for _, project := range projects {
		if len(project.Roots) != 1 {
			t.Fatalf("project retained multiple roots: %#v", project)
		}
		if project.Roots[0] == nested {
			extraID = project.ID
		}
	}
	if extraID == "" || extraID == "legacy" {
		t.Fatalf("missing split nested project: %#v", projects)
	}
	bySession := make(map[string]controller.ProjectSessionRecord)
	migratedRecords, err := records.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range migratedRecords {
		bySession[record.SessionID] = record
	}
	if bySession["first"].ProjectID != "legacy" || bySession["extra"].ProjectID != extraID || bySession["archived"].ProjectID != extraID {
		t.Fatalf("session associations were not migrated by most-specific root: %#v", bySession)
	}
	queues := controller.NewSessionQueues(store)
	for sessionID, projectID := range map[string]string{"first": "legacy", "extra": extraID, "archived": extraID} {
		queue, found, err := queues.Get(projectID, sessionID)
		if err != nil || !found || len(queue.FollowUp) != 1 || queue.FollowUp[0].Text != "preserve "+sessionID {
			t.Fatalf("queue migration for %s: %#v, found=%v, err=%v", sessionID, queue, found, err)
		}
		goal, err := objectives.Get(projectID, sessionID)
		if err != nil || goal.Goal == nil || *goal.Goal != "Preserve "+sessionID || len(goal.Tasks) != 1 {
			t.Fatalf("objective migration for %s: %#v, %v", sessionID, goal, err)
		}
	}
	if stale, err := objectives.Get("legacy", "extra"); err != nil || stale.Goal != nil || len(stale.Tasks) != 0 {
		t.Fatalf("source objective remained after migration: %#v, %v", stale, err)
	}
	pendingDeletions, err := deletions.List()
	if err != nil || len(pendingDeletions) != 2 {
		t.Fatalf("migrated deletion journal: %#v, %v", pendingDeletions, err)
	}
	for _, deletion := range pendingDeletions {
		if deletion.ProjectID != extraID || (deletion.SessionID == "extra" && deletion.Phase != "requested") || (deletion.SessionID == "archived" && deletion.Phase != "confirmed") {
			t.Fatalf("deletion phase or ownership was not migrated: %#v", deletion)
		}
	}
	if _, err := os.Stat(filepath.Join(store.Dir, "project-root-migration.json")); !os.IsNotExist(err) {
		t.Fatalf("migration journal remained after completion: %v", err)
	}

	// Restarting after completion is a no-op and preserves every target association.
	restarted := newMigrationRuntime(t, store.Dir, policy)
	if err := restarted.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	migratedRecords, err = records.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range migratedRecords {
		if record.SessionID != "first" && record.ProjectID != extraID {
			t.Fatalf("restart changed migrated association: %#v", record)
		}
	}
	pendingDeletions, err = deletions.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, deletion := range pendingDeletions {
		if deletion.ProjectID != extraID {
			t.Fatalf("restart changed migrated deletion ownership: %#v", deletion)
		}
	}
}

func TestRuntimeLeavesOutsideLegacySessionsDiagnosableAndUnnormalized(t *testing.T) {
	mount := t.TempDir()
	first, second, outside := filepath.Join(mount, "first"), filepath.Join(mount, "second"), filepath.Join(mount, "outside")
	for _, path := range []string{first, second, outside} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := workspace.NewPathPolicy([]string{mount}, false)
	if err != nil {
		t.Fatal(err)
	}
	store := persist.Store{Dir: t.TempDir()}
	writeLegacyProjectRoots(t, store, "legacy", first, second)
	records := controller.NewSessionRecords(store)
	if err := records.Record(controller.ProjectSessionRecord{ProjectID: "legacy", SessionID: "outside", CWD: outside}); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.NewRuntime(controller.RuntimeConfig{DataDir: store.Dir, StaticDir: t.TempDir(), Policy: policy, Getenv: func(string) string { return "" }}); err == nil {
		t.Fatal("accepted an outside legacy session association")
	}
	var persisted []struct {
		Roots []string `json:"roots"`
	}
	raw, err := os.ReadFile(filepath.Join(store.Dir, "projects.json"))
	if err != nil || json.Unmarshal(raw, &persisted) != nil || len(persisted) != 1 || len(persisted[0].Roots) != 2 {
		t.Fatalf("outside record allowed irreversible project normalization: %s, %v", raw, err)
	}
	listed, err := records.List()
	if err != nil || len(listed) != 1 || listed[0].ProjectID != "legacy" {
		t.Fatalf("outside record was changed: %#v, %v", listed, err)
	}
	var journal struct {
		Unresolved []json.RawMessage `json:"unresolved"`
	}
	journalRaw, err := os.ReadFile(filepath.Join(store.Dir, "project-root-migration.json"))
	if err != nil || json.Unmarshal(journalRaw, &journal) != nil || len(journal.Unresolved) != 1 {
		t.Fatalf("outside record was not retained for diagnosis: %s, %v", journalRaw, err)
	}
}

func TestRuntimeRejectsRequestedDeletionWithoutLegacySessionAssociation(t *testing.T) {
	mount := t.TempDir()
	first, second := filepath.Join(mount, "first"), filepath.Join(mount, "second")
	for _, path := range []string{first, second} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := workspace.NewPathPolicy([]string{mount}, false)
	if err != nil {
		t.Fatal(err)
	}
	store := persist.Store{Dir: t.TempDir()}
	writeLegacyProjectRoots(t, store, "legacy", first, second)
	deletions := controller.NewSessionDeletions(store)
	if err := deletions.Request("legacy", "orphan-request", "sha256:"+strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.NewRuntime(controller.RuntimeConfig{DataDir: store.Dir, StaticDir: t.TempDir(), Policy: policy, Getenv: func(string) string { return "" }}); err == nil {
		t.Fatal("accepted a requested deletion without a session association")
	}
	journalPath := filepath.Join(store.Dir, "project-root-migration.json")
	journalRaw, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	var legacyMigrating map[string]any
	if json.Unmarshal(journalRaw, &legacyMigrating) != nil {
		t.Fatal("could not decode prepared migration journal")
	}
	legacyMigrating["phase"] = "migrating"
	delete(legacyMigrating, "confirmedOrphans")
	if err := persist.Write(store, "project-root-migration.json", legacyMigrating, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.NewRuntime(controller.RuntimeConfig{DataDir: store.Dir, StaticDir: t.TempDir(), Policy: policy, Getenv: func(string) string { return "" }}); err == nil {
		t.Fatal("restart accepted a backfilled migrating journal with an unresolved deletion")
	}
	var projects []struct {
		Roots []string `json:"roots"`
	}
	raw, err := os.ReadFile(filepath.Join(store.Dir, "projects.json"))
	if err != nil || json.Unmarshal(raw, &projects) != nil || len(projects) != 1 || len(projects[0].Roots) != 2 {
		t.Fatalf("requested orphan allowed project normalization: %s, %v", raw, err)
	}
	pending, err := deletions.List()
	if err != nil || len(pending) != 1 || pending[0].ProjectID != "legacy" || pending[0].Phase != "requested" {
		t.Fatalf("requested orphan deletion changed: %#v, %v", pending, err)
	}
	var journal struct {
		Unresolved []struct {
			SessionID string `json:"sessionId"`
			Reason    string `json:"reason"`
		} `json:"unresolved"`
	}
	journalRaw, err = os.ReadFile(journalPath)
	if err != nil || json.Unmarshal(journalRaw, &journal) != nil || len(journal.Unresolved) != 1 || journal.Unresolved[0].SessionID != "orphan-request" || journal.Unresolved[0].Reason == "" {
		t.Fatalf("requested orphan was not retained for diagnosis: %s, %v", journalRaw, err)
	}
}

func TestRuntimeResumesProjectRootMigrationAfterSessionStoreCommit(t *testing.T) {
	mount := t.TempDir()
	first, second := filepath.Join(mount, "first"), filepath.Join(mount, "second")
	for _, path := range []string{filepath.Join(first, "work"), filepath.Join(second, "work")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := workspace.NewPathPolicy([]string{mount}, false)
	if err != nil {
		t.Fatal(err)
	}
	store := persist.Store{Dir: t.TempDir()}
	writeLegacyProjectRoots(t, store, "legacy", first, second)
	// Determine the stable derived extra ID without durable session state, then
	// restore the legacy project document to model a crash after record migration.
	runtime := newMigrationRuntime(t, store.Dir, policy)
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	projectList, err := workspace.NewProjects(store, policy).List(true)
	if err != nil {
		t.Fatal(err)
	}
	extraID := ""
	for _, project := range projectList {
		if project.Roots[0] == second {
			extraID = project.ID
		}
	}
	if extraID == "" {
		t.Fatal("missing derived extra project id")
	}
	writeLegacyProjectRoots(t, store, "legacy", first, second)
	records := controller.NewSessionRecords(store)
	if err := records.Record(controller.ProjectSessionRecord{ProjectID: extraID, SessionID: "extra", CWD: filepath.Join(second, "work")}); err != nil {
		t.Fatal(err)
	}
	writeLegacyQueues(t, store, "legacy", "extra", "orphan")
	deletions := controller.NewSessionDeletions(store)
	binding := "sha256:" + strings.Repeat("b", 64)
	if err := deletions.Request("legacy", "extra", binding); err != nil {
		t.Fatal(err)
	}
	if err := deletions.Confirm("legacy", "extra"); err != nil {
		t.Fatal(err)
	}
	if err := deletions.Request("legacy", "orphan", binding); err != nil {
		t.Fatal(err)
	}
	if err := deletions.Confirm("legacy", "orphan"); err != nil {
		t.Fatal(err)
	}
	goal := "Resume objective"
	if _, err := controller.NewObjectives(store).Update("legacy", "extra", &goal, nil); err != nil {
		t.Fatal(err)
	}
	orphanGoal := "Clean orphan objective"
	if _, err := controller.NewObjectives(store).Update("legacy", "orphan", &orphanGoal, nil); err != nil {
		t.Fatal(err)
	}
	journal := map[string]any{
		"version": 1,
		"phase":   "migrating",
		"roots": []any{
			map[string]any{"sourceProjectId": "legacy", "root": first, "targetProjectId": "legacy"},
			map[string]any{"sourceProjectId": "legacy", "root": second, "targetProjectId": extraID},
		},
		"moves":      []any{map[string]any{"sourceProjectId": "legacy", "targetProjectId": extraID, "sessionId": "extra", "cwd": filepath.Join(second, "work")}},
		"unresolved": []any{},
	}
	if err := persist.Write(store, "project-root-migration.json", journal, nil); err != nil {
		t.Fatal(err)
	}
	resumed := newMigrationRuntime(t, store.Dir, policy)
	queue, found, err := controller.NewSessionQueues(store).Get(extraID, "extra")
	if err != nil || !found || len(queue.FollowUp) != 1 {
		t.Fatalf("partial queue migration did not resume: %#v, found=%v, err=%v", queue, found, err)
	}
	migrated, err := controller.NewObjectives(store).Get(extraID, "extra")
	if err != nil || migrated.Goal == nil || *migrated.Goal != goal {
		t.Fatalf("partial objective migration did not resume: %#v, %v", migrated, err)
	}
	pendingDeletions, err := deletions.List()
	if err != nil || len(pendingDeletions) != 1 || pendingDeletions[0].ProjectID != extraID || pendingDeletions[0].Phase != "confirmed" {
		t.Fatalf("partial deletion migration did not resume: %#v, %v", pendingDeletions, err)
	}
	if _, found, err := controller.NewSessionQueues(store).Get("legacy", "orphan"); err != nil || found {
		t.Fatalf("confirmed orphan did not clean old queue before project normalization: found=%v, err=%v", found, err)
	}
	if orphan, err := controller.NewObjectives(store).Get("legacy", "orphan"); err != nil || orphan.Goal != nil || len(orphan.Tasks) != 0 {
		t.Fatalf("confirmed orphan did not clean old objective before project normalization: %#v, %v", orphan, err)
	}
	if _, err := resumed.Start(); err != nil {
		t.Fatal(err)
	}
	if err := resumed.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if records, err := records.List(); err != nil || len(records) != 0 {
		t.Fatalf("confirmed deletion did not clean migrated association: %#v, %v", records, err)
	}
	if _, found, err := controller.NewSessionQueues(store).Get(extraID, "extra"); err != nil || found {
		t.Fatalf("confirmed deletion did not clean migrated queue: found=%v, err=%v", found, err)
	}
	if goal, err := controller.NewObjectives(store).Get(extraID, "extra"); err != nil || goal.Goal != nil || len(goal.Tasks) != 0 {
		t.Fatalf("confirmed deletion did not clean migrated objective: %#v, %v", goal, err)
	}
	if pending, err := deletions.List(); err != nil || len(pending) != 0 {
		t.Fatalf("confirmed deletion did not finish migrated journal: %#v, %v", pending, err)
	}
	restartedCleanup := newMigrationRuntime(t, store.Dir, policy)
	if err := restartedCleanup.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pending, err := deletions.List(); err != nil || len(pending) != 0 {
		t.Fatalf("restart restored a cleaned confirmed orphan deletion: %#v, %v", pending, err)
	}
}

func writeLegacyProjectRoots(t *testing.T, store persist.Store, id string, roots ...string) {
	t.Helper()
	value := []map[string]any{{"id": id, "name": "Legacy", "roots": roots, "slug": "legacy", "lastOpened": 1}}
	if err := persist.Write(store, "projects.json", value, nil); err != nil {
		t.Fatal(err)
	}
}

func writeLegacyQueues(t *testing.T, store persist.Store, projectID string, sessions ...string) {
	t.Helper()
	records := make([]any, 0, len(sessions))
	for _, sessionID := range sessions {
		records = append(records, map[string]any{"projectId": projectID, "sessionId": sessionID, "revision": "queue-" + sessionID, "followUp": []any{map[string]any{"id": "item-" + sessionID, "text": "preserve " + sessionID}}, "handled": []any{}})
	}
	if err := persist.Write(store, "pi-session-queues.json", map[string]any{"version": 1, "engine": "pi", "records": records}, nil); err != nil {
		t.Fatal(err)
	}
}

func newMigrationRuntime(t *testing.T, dataDir string, policy *workspace.PathPolicy) *controller.Runtime {
	t.Helper()
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, err := controller.NewRuntime(controller.RuntimeConfig{Host: "127.0.0.1", Port: port, DataDir: dataDir, StaticDir: staticDir, Policy: policy, Getenv: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}
