package persist_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

func TestPersistInventoryListsOwnedStoresAndPreservesUnknown(t *testing.T) {
	dir := t.TempDir()
	store := persist.Store{Dir: dir}
	if _, err := controller.NewSettings(store, nil).SetModelVisibility("provider", "model", true); err != nil {
		t.Fatal(err)
	}
	unknownPath := filepath.Join(dir, "custom-sidecar.json")
	if err := os.WriteFile(unknownPath, []byte(`{"custom":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(unknownPath)
	if err != nil {
		t.Fatal(err)
	}
	report, err := persist.InventoryDataDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	foundConfig := false
	for _, file := range report.Files {
		if file.Name == "config.json" && file.Present {
			foundConfig = true
		}
	}
	if !foundConfig {
		t.Fatalf("inventory missed controller config: %#v", report.Files)
	}
	foundUnknown := false
	for _, name := range report.Unknown {
		if name == "custom-sidecar.json" {
			foundUnknown = true
		}
	}
	if !foundUnknown {
		t.Fatalf("unknown source file was not reported as preserved: %#v", report.Unknown)
	}
	after, err := os.ReadFile(unknownPath)
	if err != nil || string(after) != string(before) {
		t.Fatal("inventory pass must never modify unknown source files")
	}
	entries := persist.KnownControllerInventory()
	need := map[string]bool{
		"config.json":                  false,
		"projects.json":                false,
		"pi-project-sessions.json":     false,
		"pi-session-queues.json":       false,
		"schedules.json":               false,
		"pi-session-deletions.json":    false,
		"mcp-modules.json":             false,
		"browser-panels-*.json":        false,
		"native sessions/**/*.jsonl":   false,
		"agentDir/pixie/sessions.json": false,
	}
	for _, entry := range entries {
		if _, ok := need[entry.File]; ok {
			need[entry.File] = true
		}
		if entry.Owner == "" || entry.Handling == "" || entry.Schema == "" {
			t.Fatalf("inventory entry %q requires owner, handling and schema", entry.File)
		}
	}
	for file, seen := range need {
		if !seen {
			t.Fatalf("inventory missed required row %q", file)
		}
	}
}

func TestPersistInventoryFailClosedOnMissingPrimary(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "pi-session-queues.json")
	backup := primary + ".bak"
	payload := `{"version":1,"engine":"pi","records":[]}`
	if err := os.WriteFile(backup, []byte(payload+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := persist.InventoryDataDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	warned := false
	for _, warning := range report.Warnings {
		if strings.Contains(warning, "pi-session-queues.json") && strings.Contains(warning, "backup remains") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("missing primary with backup must warn fail-closed: %#v", report.Warnings)
	}
	if _, err := os.Stat(primary); !os.IsNotExist(err) {
		t.Fatal("inventory must not restore a backup over a missing primary")
	}
}

func TestPersistStagedPlanIsRepeatableAndConflictsOnChange(t *testing.T) {
	hashes := map[string]string{"schedules.json": "aaa", "pi-session-queues.json": "bbb"}
	plan := persist.NewStagedPlan("src", "dst", "host-identity", "v1/pi", hashes, persist.PhasePrepared)
	if err := persist.ValidateMigrationPhase(plan.Phase); err != nil {
		t.Fatal(err)
	}
	if !plan.InputsUnchanged(map[string]string{"schedules.json": "aaa", "pi-session-queues.json": "bbb"}) {
		t.Fatal("identical inputs must be idempotent")
	}
	if plan.DetectInputChange(map[string]string{"schedules.json": "aaa", "pi-session-queues.json": "bbb"}) != nil {
		t.Fatal("identical inputs must not conflict")
	}
	if plan.DetectInputChange(map[string]string{"schedules.json": "changed", "pi-session-queues.json": "bbb"}) == nil {
		t.Fatal("changed inputs must conflict instead of merging")
	}
	receipt := persist.MigrationReceipt{Plan: plan, CompletedPhase: persist.PhasePrepared, OutputHashes: map[string]string{}, RetainedEffects: []string{}, Unresolved: []string{}}
	if err := receipt.Validate(); err != nil {
		t.Fatal(err)
	}
}

func writeLegacySchedule(t *testing.T, dir string) {
	t.Helper()
	job := controller.Schedule{ID: "job", ProjectID: "project", Root: "/project", Prompt: "Review", Cron: "0 9 * * *", Timezone: "Europe/Warsaw", NextRun: time.Now().Add(time.Hour), Runs: []controller.ScheduleRun{}}
	if err := persist.Write(persist.Store{Dir: dir}, "schedules.json", map[string]controller.Schedule{"job": job}, nil); err != nil {
		t.Fatal(err)
	}
}

func fingerprint(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestScheduleInspectPreservesLedgerWithoutDispatch(t *testing.T) {
	dir := t.TempDir()
	store := persist.Store{Dir: dir}
	summary, err := controller.InspectScheduleLedger(store)
	if err != nil || summary.Present {
		t.Fatalf("empty ledger inspect: %#v %v", summary, err)
	}
	plan, err := controller.PlanScheduleMigration(summary)
	if err != nil || plan.MutationsBlocked {
		t.Fatalf("empty ledger plan: %#v %v", plan, err)
	}
	writeLegacySchedule(t, dir)
	before := fingerprint(t, filepath.Join(dir, "schedules.json"))
	summary, err = controller.InspectScheduleLedger(store)
	if err != nil {
		t.Fatal(err)
	}
	if summary.JobCount != 1 || len(summary.IDs) != 1 || summary.IDs[0] != "job" {
		t.Fatalf("schedule IDs lost: %#v", summary)
	}
	if len(summary.Timezones) != 1 || summary.Timezones[0] != "Europe/Warsaw" {
		t.Fatalf("schedule timezone lost: %#v", summary)
	}
	plan, err = controller.PlanScheduleMigration(summary)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.DispatchBlocked || plan.MutationsBlocked {
		t.Fatalf("schedule migration must never dispatch missed jobs: %#v", plan)
	}
	found := false
	for _, preserved := range plan.Preserved {
		if preserved == "native session links" {
			found = true
		}
	}
	if !found {
		t.Fatalf("schedule plan must preserve session links: %#v", plan.Preserved)
	}
	after := fingerprint(t, filepath.Join(dir, "schedules.json"))
	if before != after {
		t.Fatal("inspect/plan must perform no writes and never dispatch")
	}
	if err := controller.ValidateSchedulePreservation(summary, summary); err != nil {
		t.Fatal(err)
	}
	changed := summary
	changed.IDs = []string{"other"}
	if err := controller.ValidateSchedulePreservation(summary, changed); err == nil {
		t.Fatal("invented schedule identities must conflict")
	}
}

func TestScheduleInspectKeepsDiagnosticsOnNewerSchema(t *testing.T) {
	dir := t.TempDir()
	store := persist.Store{Dir: dir}
	payload := `{"version":99,"jobs":{},"operations":[]}`
	if err := os.WriteFile(filepath.Join(dir, "schedules.json"), []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	summary, err := controller.InspectScheduleLedger(store)
	if err != nil {
		t.Fatalf("newer schema must stay diagnosable: %v", err)
	}
	if summary.SchemaSupported || summary.Version != 99 {
		t.Fatalf("newer schema not reported: %#v", summary)
	}
	plan, err := controller.PlanScheduleMigration(summary)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.MutationsBlocked || !plan.DispatchBlocked {
		t.Fatalf("newer schema must block mutations: %#v", plan)
	}
}

func TestQueueInspectPreservesDeliveryAuthorityFailClosed(t *testing.T) {
	dir := t.TempDir()
	store := persist.Store{Dir: dir}
	summary, err := controller.InspectQueueLedger(store)
	if err != nil || summary.Present {
		t.Fatalf("empty queue inspect: %#v %v", summary, err)
	}
	type queuedItem struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	type queueRecord struct {
		ProjectID string       `json:"projectId"`
		SessionID string       `json:"sessionId"`
		Revision  string       `json:"revision"`
		FollowUp  []queuedItem `json:"followUp"`
		Handled   []any        `json:"handled"`
	}
	type queueStore struct {
		Version int           `json:"version"`
		Engine  string        `json:"engine"`
		Records []queueRecord `json:"records"`
	}
	first := queueStore{Version: 1, Engine: "pi", Records: []queueRecord{{ProjectID: "project", SessionID: "agent/session/opaque", Revision: "first", FollowUp: []queuedItem{{ID: "one", Text: "first"}}, Handled: []any{}}}}
	second := queueStore{Version: 1, Engine: "pi", Records: []queueRecord{{ProjectID: "project", SessionID: "agent/session/opaque", Revision: "second", FollowUp: []queuedItem{{ID: "one", Text: "first"}, {ID: "two", Text: "second"}}, Handled: []any{}}}}
	if err := persist.Write(store, "pi-session-queues.json", first, nil); err != nil {
		t.Fatal(err)
	}
	if err := persist.Write(store, "pi-session-queues.json", second, nil); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "pi-session-queues.json"))
	if err != nil {
		t.Fatal(err)
	}
	summary, err = controller.InspectQueueLedger(store)
	if err != nil {
		t.Fatal(err)
	}
	if summary.RecordCount != 1 || summary.FollowUpCount != 2 || len(summary.Sessions) != 1 {
		t.Fatalf("queue delivery state lost: %#v", summary)
	}
	after, err := os.ReadFile(filepath.Join(dir, "pi-session-queues.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("queue inspect must never write or replay state")
	}
	if err := controller.ValidateQueuePreservation(summary, summary); err != nil {
		t.Fatal(err)
	}
	changed := summary
	changed.HandledCount++
	if err := controller.ValidateQueuePreservation(summary, changed); err == nil {
		t.Fatal("lost mutation identities must conflict")
	}
	if err := os.WriteFile(filepath.Join(dir, "pi-session-queues.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.InspectQueueLedger(store); err == nil {
		t.Fatal("corrupt queue primary must fail closed instead of replaying its older backup")
	}
	if err := os.Remove(filepath.Join(dir, "pi-session-queues.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.InspectQueueLedger(store); err == nil {
		t.Fatal("missing queue primary must fail closed while a backup remains")
	}
}

func TestQueueProjectPlanConvertsOnlyVerifiedAssociations(t *testing.T) {
	associations := []controller.QueueAssociation{{ProjectID: "old", SessionID: "s1"}, {ProjectID: "old", SessionID: "s2"}}
	moves := []controller.QueueProjectMove{{SourceProjectID: "old", TargetProjectID: "new", SessionID: "s1"}, {SourceProjectID: "old", TargetProjectID: "new", SessionID: "s2"}}
	verified := map[string]bool{"old\x00s1": true}
	planned, retained, _, err := controller.PlanQueueProjectMigration(associations, moves, verified)
	if err != nil {
		t.Fatal(err)
	}
	if len(planned) != 2 || len(retained) != 1 || retained[0].SessionID != "s2" {
		t.Fatalf("unverified queue must be retained, not fabricated: planned=%#v retained=%#v", planned, retained)
	}
	for _, item := range planned {
		if item.SessionID == "s1" && item.ProjectID != "new" {
			t.Fatalf("verified queue not converted: %#v", planned)
		}
		if item.SessionID == "s2" && item.ProjectID != "old" {
			t.Fatalf("unverified queue must keep its project: %#v", planned)
		}
	}
	repeat, _, _, err := controller.PlanQueueProjectMigration(associations, moves, verified)
	if err != nil || len(repeat) != len(planned) {
		t.Fatal("identical queue inputs must be idempotent")
	}
	conflicting := []controller.QueueProjectMove{{SourceProjectID: "old", TargetProjectID: "new", SessionID: "s1"}, {SourceProjectID: "old", TargetProjectID: "other", SessionID: "s1"}}
	if _, _, _, err := controller.PlanQueueProjectMigration(associations, conflicting, verified); err == nil {
		t.Fatal("changed queue inputs must conflict")
	}
}

func TestDeletionInspectPreservesTombstonesFailClosed(t *testing.T) {
	dir := t.TempDir()
	store := persist.Store{Dir: dir}
	deletions := controller.NewSessionDeletions(store)
	binding := "sha256:" + strings.Repeat("a", 64)
	opaque := "agent/session/" + strings.Repeat("opaque", 32)
	if err := deletions.Request("project", opaque, binding); err != nil {
		t.Fatal(err)
	}
	if err := deletions.Confirm("project", opaque); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "pi-session-deletions.json"))
	if err != nil {
		t.Fatal(err)
	}
	summary, err := controller.InspectDeletionLedger(store)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Confirmed != 1 || len(summary.Tombstones) != 1 || summary.Tombstones[0].Phase != "confirmed" {
		t.Fatalf("deletion tombstone lost: %#v", summary)
	}
	after, err := os.ReadFile(filepath.Join(dir, "pi-session-deletions.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("deletion inspect must never write or resurrect state")
	}
	if err := controller.ValidateDeletionPreservation(summary, summary); err != nil {
		t.Fatal(err)
	}
	dropped := summary
	dropped.Tombstones = nil
	dropped.Confirmed = 0
	if err := controller.ValidateDeletionPreservation(summary, dropped); err == nil {
		t.Fatal("discarded tombstones must conflict")
	}
	if err := os.WriteFile(filepath.Join(dir, "pi-session-deletions.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.InspectDeletionLedger(store); err == nil {
		t.Fatal("corrupt deletion primary must fail closed instead of replaying its older backup")
	}
}

func TestDeletionPlanQuarantinesUnverifiedRecoveryBlocked(t *testing.T) {
	tombstones := []controller.DeletionTombstone{{ProjectID: "old", SessionID: "s1", Binding: "sha256:" + strings.Repeat("a", 64), Phase: "confirmed"}}
	moves := []controller.DeletionProjectMove{{SourceProjectID: "old", TargetProjectID: "new", SessionID: "s1"}}
	verifiedAssociations := map[string]bool{"old\x00s1": true}
	verifiedBindings := map[string]bool{"s1": true}
	decisions, err := controller.PlanDeletionMigration(tombstones, moves, verifiedAssociations, verifiedBindings)
	if err != nil || len(decisions) != 1 || decisions[0].Decision != controller.DeletionDecisionMigrate || decisions[0].ProjectID != "new" {
		t.Fatalf("verified deletion not migrated: %#v %v", decisions, err)
	}
	if decisions[0].Phase != "confirmed" {
		t.Fatalf("deletion phase not preserved: %#v", decisions[0])
	}
	blocked, err := controller.PlanDeletionMigration(tombstones, moves, verifiedAssociations, map[string]bool{})
	if err != nil || len(blocked) != 1 || blocked[0].Decision != controller.DeletionDecisionBlocked {
		t.Fatalf("unverifiable delete must stay recovery-blocked: %#v %v", blocked, err)
	}
	if blocked[0].ProjectID != "old" {
		t.Fatalf("blocked tombstone must keep its project: %#v", blocked[0])
	}
	repeat, err := controller.PlanDeletionMigration(tombstones, moves, verifiedAssociations, verifiedBindings)
	if err != nil || repeat[0].Decision != decisions[0].Decision {
		t.Fatal("identical deletion inputs must be idempotent")
	}
}

func TestArchiveAssociationsStayVisibleWithoutSynthesizedTranscripts(t *testing.T) {
	dir := t.TempDir()
	store := persist.Store{Dir: dir}
	records := controller.NewSessionRecords(store)
	opaque := "agent/session/" + strings.Repeat("opaque", 32)
	if err := records.Record(controller.ProjectSessionRecord{ProjectID: "project", SessionID: opaque, CWD: "/project", ParentSessionID: "", Title: "kept"}); err != nil {
		t.Fatal(err)
	}
	report, err := persist.InventoryDataDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, file := range report.Files {
		if file.Name == "pi-project-sessions.json" && file.Present {
			found = true
			if file.Version == nil || *file.Version != 2 {
				t.Fatalf("session association schema not reported: %#v", file)
			}
		}
	}
	if !found {
		t.Fatal("archive/parent/catalog associations must remain inventoried and visible")
	}
	raw, err := json.Marshal(map[string]any{"version": 99, "engine": "pi", "records": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pi-project-sessions.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// A newer unsupported association schema must fail its mutations closed
	// without synthesizing a transcript or falling back to an older backup.
	if _, err := records.List(); err == nil {
		t.Fatal("newer unsupported session associations must block mutations while staying diagnosable")
	}
	reportAfter, err := persist.InventoryDataDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	diagnosed := false
	for _, file := range reportAfter.Files {
		if file.Name == "pi-project-sessions.json" && file.Present && file.Version != nil && *file.Version == 99 {
			diagnosed = true
		}
	}
	if !diagnosed {
		t.Fatalf("newer association schema must stay diagnosable: %#v", reportAfter.Files)
	}
}
