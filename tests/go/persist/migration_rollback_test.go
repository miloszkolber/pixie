package persist_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/persist"
)

func TestMigrationPersistRollbackNoRewindNoOpAndInterrupted(t *testing.T) {
	dir := t.TempDir()
	// No-op rollback with no backups restores nothing but succeeds.
	receipt, outcome, err := persist.ApplyMigrationRollback(dir, []string{"config.json"}, persist.MigrationRollbackFaults{}, false)
	if err != nil || outcome.Kind != persist.OutcomeInstalled {
		t.Fatalf("no-op rollback must succeed: %v %#v", err, outcome)
	}
	if len(receipt.RestoredFiles) != 0 {
		t.Fatalf("no-op rollback must restore nothing: %#v", receipt)
	}
	foundNoOp := false
	for _, effect := range receipt.RetainedEffects {
		if strings.Contains(effect, "no-op") {
			foundNoOp = true
		}
	}
	if !foundNoOp {
		t.Fatalf("no-op rollback must be recorded: %#v", receipt)
	}
	// Authority rollback without explicit reconciliation never rewinds.
	migrationWriteJSON(t, dir, "pi-session-queues.json", "{\"version\":1,\"engine\":\"pi\",\"records\":[]}\n", 0o600)
	if _, _, err := persist.ApplyStagedMigration(dir, persist.NewStagedPlan(dir, dir, "host-identity", "v1/pi", map[string]string{}, persist.PhasePrepared), map[string][]byte{"pi-session-queues.json": []byte("{\"version\":1,\"engine\":\"pi\",\"records\":[]}\n")}, persist.MigrationFaults{}); err != nil {
		t.Fatal(err)
	}
	migrationWriteJSON(t, dir, "pi-session-queues.json", "{\"version\":1,\"engine\":\"pi\",\"records\":[{\"projectId\":\"project\",\"sessionId\":\"agent/session/opaque\",\"revision\":\"second\",\"followUp\":[{\"id\":\"one\",\"text\":\"first\"},{\"id\":\"two\",\"text\":\"second\"}],\"handled\":[]}]}}\n", 0o600)
	plan, err := persist.PlanMigrationRollback(dir, []string{"pi-session-queues.json"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.AuthorityBlocked) != 1 || !plan.DispatchDisabled || !plan.RequiresReconcile {
		t.Fatalf("advanced queue must be blocked from runnable restore: %#v", plan)
	}
	before, err := os.ReadFile(filepath.Join(dir, "pi-session-queues.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := persist.ApplyMigrationRollback(dir, []string{"pi-session-queues.json"}, persist.MigrationRollbackFaults{}, false); err == nil {
		t.Fatal("authority rollback without reconciliation must fail closed")
	}
	after, err := os.ReadFile(filepath.Join(dir, "pi-session-queues.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("blocked rollback must never overwrite the visible primary with its older backup")
	}
	// Interrupted rollback stays unresolved until primaries are reread.
	if _, outcome, err := persist.ApplyMigrationRollback(dir, []string{"config.json"}, persist.MigrationRollbackFaults{FailBeforeRestore: errors.New("restore unavailable")}, true); err == nil || outcome.Kind != persist.OutcomeKnownUncommitted {
		t.Fatalf("interrupted rollback must stay known-uncommitted: %#v %v", outcome, err)
	}
	migrationWriteJSON(t, dir, "config.json", "{\"ok\":true}\n", 0o600)
	if _, err := persist.BackupMigrationInputs(dir, []string{"config.json"}); err != nil {
		t.Fatal(err)
	}
	if _, outcome, err := persist.ApplyMigrationRollback(dir, []string{"config.json"}, persist.MigrationRollbackFaults{FailAfterRestore: errors.New("restore lost")}, true); err == nil || outcome.Kind != persist.OutcomeDurabilityUncertain {
		t.Fatalf("post-restore interruption must stay durability-uncertain: %#v %v", outcome, err)
	}
}

func TestMigrationRollbackNeverAllowsAuthorityBackupReplay(t *testing.T) {
	dir := t.TempDir()
	files := []string{"pi-session-deletions.json", "schedules.json"}
	deletionBefore := "{\"version\":1,\"engine\":\"pi\",\"records\":[{\"sessionId\":\"session\",\"phase\":\"requested\"}]}\n"
	scheduleBefore := "{\"version\":1,\"jobs\":{\"daily\":{\"runs\":[]}},\"operations\":[]}\n"
	migrationWriteJSON(t, dir, "pi-session-deletions.json", deletionBefore, 0o600)
	migrationWriteJSON(t, dir, "schedules.json", scheduleBefore, 0o600)
	if _, err := persist.BackupMigrationInputs(dir, files); err != nil {
		t.Fatal(err)
	}
	deletionAfter := "{\"version\":1,\"engine\":\"pi\",\"records\":[{\"sessionId\":\"session\",\"phase\":\"confirmed\"}]}\n"
	scheduleAfter := "{\"version\":1,\"jobs\":{\"daily\":{\"runs\":[{\"id\":\"later\",\"status\":\"completed\"}]}},\"operations\":[{\"key\":\"later\"}]}\n"
	migrationWriteJSON(t, dir, "pi-session-deletions.json", deletionAfter, 0o600)
	migrationWriteJSON(t, dir, "schedules.json", scheduleAfter, 0o600)

	_, outcome, err := persist.ApplyMigrationRollback(dir, files, persist.MigrationRollbackFaults{}, true)
	if err == nil {
		t.Fatal("a caller assertion must not authorize authority backup replay")
	}
	if outcome.Kind != persist.OutcomeKnownUncommitted || outcome.PrimaryVisible || outcome.MustReconcileLedger() {
		t.Fatalf("blocked authority rollback must leave current primaries untouched: %#v", outcome)
	}
	for name, want := range map[string]string{
		"pi-session-deletions.json": deletionAfter,
		"schedules.json":            scheduleAfter,
	} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(raw) != want {
			t.Fatalf("authority rollback must retain later state in %s: %q %v", name, raw, err)
		}
	}
}

func TestMigrationRollbackSecondRestoreFailureIsDurabilityUncertain(t *testing.T) {
	dir := t.TempDir()
	files := []string{"config.json", "mcp-modules.json"}
	migrationWriteJSON(t, dir, "config.json", "{\"config\":\"backup\"}\n", 0o600)
	migrationWriteJSON(t, dir, "mcp-modules.json", "{\"modules\":\"backup\"}\n", 0o600)
	if _, err := persist.BackupMigrationInputs(dir, files); err != nil {
		t.Fatal(err)
	}
	migrationWriteJSON(t, dir, "config.json", "{\"config\":\"current\"}\n", 0o600)
	if err := os.Remove(filepath.Join(dir, "mcp-modules.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "mcp-modules.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	receipt, outcome, err := persist.ApplyMigrationRollback(dir, files, persist.MigrationRollbackFaults{}, false)
	if err == nil {
		t.Fatal("second restore rename collision must report an error")
	}
	if outcome.Kind != persist.OutcomeDurabilityUncertain || outcome.Stage != persist.StagePrimary || !outcome.PrimaryVisible || !outcome.MustReconcileLedger() {
		t.Fatalf("partial rollback must be durability-uncertain: %#v", outcome)
	}
	if len(receipt.RestoredFiles) != 1 || receipt.RestoredFiles[0] != "config.json" {
		t.Fatalf("partial rollback receipt must name the visible restored primary: %#v", receipt)
	}
	config, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil || string(config) != "{\"config\":\"backup\"}\n" {
		t.Fatalf("first restored primary must remain visible for reconciliation: %q %v", config, err)
	}
}
