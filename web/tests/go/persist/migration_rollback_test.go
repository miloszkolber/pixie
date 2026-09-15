package persist_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

func migrationQueueSummary(recordCount, followUps, handled, uncertain int, sessions []string) controller.QueueLedgerSummary {
	return controller.QueueLedgerSummary{
		Present: true, Version: 1, SchemaSupported: true,
		RecordCount: recordCount, FollowUpCount: followUps,
		HandledCount: handled, UncertainCount: uncertain, Sessions: sessions,
	}
}

func migrationScheduleSummary(ids []string, runs, operations int) controller.ScheduleLedgerSummary {
	return controller.ScheduleLedgerSummary{
		Present: true, Version: 1, SchemaSupported: true,
		JobCount: len(ids), IDs: ids, Timezones: []string{"Europe/Warsaw"},
		RunCount: runs, OperationCount: operations,
	}
}

func migrationDeletionSummary(requested, confirmed int, tombstones []controller.DeletionTombstone) controller.DeletionLedgerSummary {
	return controller.DeletionLedgerSummary{
		Present: true, Version: 1, SchemaSupported: true,
		Requested: requested, Confirmed: confirmed, Tombstones: tombstones,
	}
}

func TestMigrationQueueRollbackNeverRestoresRunnableAuthority(t *testing.T) {
	backup := migrationQueueSummary(1, 1, 0, 0, []string{"agent/session/opaque"})
	current := migrationQueueSummary(1, 2, 1, 1, []string{"agent/session/opaque"})
	assessment, err := controller.AssessQueueRollback(backup, current)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.AllowedAsRunnable || !assessment.DispatchDisabled || !assessment.RequiresReconciliation {
		t.Fatalf("accepted prompt after backup must block runnable restore: %#v", assessment)
	}
	if len(assessment.RetainedEffects) == 0 || len(assessment.Unresolved) == 0 {
		t.Fatalf("post-backup queue effects must be retained and unresolved: %#v", assessment)
	}
	identical, err := controller.AssessQueueRollback(backup, backup)
	if err != nil || !identical.AllowedAsRunnable || identical.DispatchDisabled {
		t.Fatalf("identical queue inputs must allow runnable resume: %#v %v", identical, err)
	}
	newer := current
	newer.SchemaSupported = false
	newer.Version = 99
	blocked, err := controller.AssessQueueRollback(backup, newer)
	if err != nil || blocked.AllowedAsRunnable || !blocked.DispatchDisabled {
		t.Fatalf("unknown newer queue schema must block runnable restore: %#v %v", blocked, err)
	}
	emptyBackup := controller.QueueLedgerSummary{SchemaSupported: true}
	emptyCurrent := controller.QueueLedgerSummary{SchemaSupported: true}
	noop, err := controller.AssessQueueRollback(emptyBackup, emptyCurrent)
	if err != nil || !noop.AllowedAsRunnable {
		t.Fatalf("empty queue rollback must be a no-op allow: %#v %v", noop, err)
	}
}

func TestMigrationScheduleRollbackNeverDispatchesMissedJobs(t *testing.T) {
	backup := migrationScheduleSummary([]string{"job"}, 0, 0)
	current := migrationScheduleSummary([]string{"job"}, 1, 1)
	assessment, err := controller.AssessScheduleRollback(backup, current)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.AllowedAsRunnable || !assessment.DispatchDisabled {
		t.Fatalf("post-backup schedule runs must block runnable restore: %#v", assessment)
	}
	foundMonotonic := false
	for _, effect := range assessment.RetainedEffects {
		if strings.Contains(effect, "monotonic") || strings.Contains(effect, "never replayed") || strings.Contains(effect, "missed") {
			foundMonotonic = true
		}
	}
	if !foundMonotonic {
		t.Fatalf("schedule rollback must name monotonic claims and no replay: %#v", assessment)
	}
	identical, err := controller.AssessScheduleRollback(backup, backup)
	if err != nil || !identical.AllowedAsRunnable {
		t.Fatalf("identical schedule must allow: %#v %v", identical, err)
	}
	diverged := migrationScheduleSummary([]string{"other"}, 0, 0)
	blocked, err := controller.AssessScheduleRollback(backup, diverged)
	if err != nil || blocked.AllowedAsRunnable {
		t.Fatalf("invented schedule identities must block: %#v %v", blocked, err)
	}
	newer := current
	newer.SchemaSupported = false
	newer.Version = 99
	newerBlocked, err := controller.AssessScheduleRollback(backup, newer)
	if err != nil || newerBlocked.AllowedAsRunnable {
		t.Fatalf("newer schedule schema must block mutations: %#v %v", newerBlocked, err)
	}
}

func TestMigrationDeletionRollbackPreservesTombstones(t *testing.T) {
	binding := "sha256:" + strings.Repeat("a", 64)
	backup := migrationDeletionSummary(1, 0, []controller.DeletionTombstone{{ProjectID: "project", SessionID: "s1", Binding: binding, Phase: "requested"}})
	current := migrationDeletionSummary(0, 1, []controller.DeletionTombstone{{ProjectID: "project", SessionID: "s1", Binding: binding, Phase: "confirmed"}})
	assessment, err := controller.AssessDeletionRollback(backup, current)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.AllowedAsRunnable || !assessment.DispatchDisabled {
		t.Fatalf("confirmed deletion after backup must block rewind: %#v", assessment)
	}
	foundTombstone := false
	for _, effect := range assessment.RetainedEffects {
		if strings.Contains(effect, "tombstone") || strings.Contains(effect, "never resurrected") {
			foundTombstone = true
		}
	}
	if !foundTombstone {
		t.Fatalf("deletion rollback must name tombstones: %#v", assessment)
	}
	identical, err := controller.AssessDeletionRollback(backup, backup)
	if err != nil || !identical.AllowedAsRunnable {
		t.Fatalf("identical deletion must allow: %#v %v", identical, err)
	}
	newer := current
	newer.SchemaSupported = false
	newer.Version = 99
	newerBlocked, err := controller.AssessDeletionRollback(backup, newer)
	if err != nil || newerBlocked.AllowedAsRunnable {
		t.Fatalf("newer deletion schema must block: %#v %v", newerBlocked, err)
	}
}

func TestMigrationRollbackAcrossBothTopologies(t *testing.T) {
	queueBackup := migrationQueueSummary(1, 1, 0, 0, []string{"agent/session/opaque"})
	queueCurrent := migrationQueueSummary(1, 2, 0, 0, []string{"agent/session/opaque"})
	queueAssessment, err := controller.AssessQueueRollback(queueBackup, queueCurrent)
	if err != nil {
		t.Fatal(err)
	}
	scheduleBackup := migrationScheduleSummary([]string{"job"}, 0, 0)
	scheduleCurrent := migrationScheduleSummary([]string{"job"}, 0, 0)
	scheduleAssessment, err := controller.AssessScheduleRollback(scheduleBackup, scheduleCurrent)
	if err != nil {
		t.Fatal(err)
	}
	binding := "sha256:" + strings.Repeat("b", 64)
	deletionBackup := migrationDeletionSummary(1, 0, []controller.DeletionTombstone{{ProjectID: "project", SessionID: "s1", Binding: binding, Phase: "requested"}})
	deletionCurrent := deletionBackup
	deletionAssessment, err := controller.AssessDeletionRollback(deletionBackup, deletionCurrent)
	if err != nil {
		t.Fatal(err)
	}
	for _, direction := range []struct {
		from controller.MigrationTopology
		to   controller.MigrationTopology
	}{
		{controller.TopologyDockerPlusAssistant, controller.TopologyCombinedHost},
		{controller.TopologyCombinedHost, controller.TopologyDockerPlusAssistant},
	} {
		receipt, err := controller.PlanControllerRollback(direction.from, direction.to, queueAssessment, scheduleAssessment, deletionAssessment)
		if err != nil {
			t.Fatalf("rollback %s->%s: %v", direction.from, direction.to, err)
		}
		if !receipt.DispatchDisabled || !receipt.RequiresReconciliation {
			t.Fatalf("rollback %s->%s with post-backup queue effects must keep dispatch disabled: %#v", direction.from, direction.to, receipt)
		}
		if len(receipt.RetainedEffects) == 0 || len(receipt.Unresolved) == 0 {
			t.Fatalf("rollback %s->%s must name retained effects and unresolved work: %#v", direction.from, direction.to, receipt)
		}
		if len(receipt.RestoredFiles) != 0 {
			t.Fatalf("blocked rollback %s->%s must not claim restored authority: %#v", direction.from, direction.to, receipt)
		}
	}
	cleanQueue, err := controller.AssessQueueRollback(queueBackup, queueBackup)
	if err != nil {
		t.Fatal(err)
	}
	cleanReceipt, err := controller.PlanControllerRollback(controller.TopologyDockerPlusAssistant, controller.TopologyCombinedHost, cleanQueue, scheduleAssessment, deletionAssessment)
	if err != nil {
		t.Fatal(err)
	}
	if cleanReceipt.DispatchDisabled || cleanReceipt.RequiresReconciliation {
		t.Fatalf("clean rollback must allow resume without disabled dispatch: %#v", cleanReceipt)
	}
}

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
