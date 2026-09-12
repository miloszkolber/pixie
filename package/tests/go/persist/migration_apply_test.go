package persist_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/persist"
)

func migrationWriteJSON(t *testing.T, dir, name, payload string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(payload), perm); err != nil {
		t.Fatal(err)
	}
}

func migrationListNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func migrationFileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestMigrationCheckpointsInDefinedOrder(t *testing.T) {
	ordered := persist.OrderedMigrationCheckpoints()
	want := []persist.MigrationCheckpoint{
		persist.CheckpointInventory,
		persist.CheckpointBackup,
		persist.CheckpointStaging,
		persist.CheckpointPrimary,
		persist.CheckpointSync,
		persist.CheckpointReceipt,
	}
	if len(ordered) != len(want) {
		t.Fatalf("checkpoint order length: %#v", ordered)
	}
	for i := range want {
		if ordered[i] != want[i] {
			t.Fatalf("checkpoint order diverged: %#v", ordered)
		}
	}
	for _, checkpoint := range want {
		if err := persist.ValidateMigrationCheckpoint(checkpoint); err != nil {
			t.Fatalf("valid checkpoint rejected: %s", checkpoint)
		}
	}
	if err := persist.ValidateMigrationCheckpoint("response"); err == nil {
		t.Fatal("unknown checkpoint must be rejected")
	}
}

func TestMigrationDryRunRedactsRootsAndPerformsNoWrites(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	migrationWriteJSON(t, source, "config.json", "{\"ok\":true}\n", 0o600)
	migrationWriteJSON(t, source, "custom-sidecar.json", "{\"custom\":true}\n", 0o600)
	beforeSource := migrationListNames(t, source)
	beforeTarget := migrationListNames(t, target)
	inspection, err := persist.InspectStagedMigration(source, target, "host-identity", "v1/pi", persist.PhasePrepared, []string{"config.json", "schedules.json"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(inspection.RedactedSource, "[redacted]") || !strings.Contains(inspection.RedactedTarget, "[redacted]") {
		t.Fatalf("dry-run must redact roots: %#v", inspection)
	}
	if strings.Contains(inspection.RedactedSource, source) || strings.Contains(inspection.RedactedTarget, target) {
		t.Fatalf("dry-run leaked absolute roots: %#v", inspection)
	}
	if inspection.SourceIdentity != "host-identity" || inspection.TargetSchema != "v1/pi" {
		t.Fatalf("dry-run lost identity: %#v", inspection)
	}
	if len(inspection.InputHashes) == 0 || inspection.InputHashes["config.json"] == "" {
		t.Fatalf("dry-run must report input hashes: %#v", inspection)
	}
	foundBackup := false
	for _, backup := range inspection.Backups {
		if strings.Contains(backup, "config.json.bak") && strings.Contains(backup, "0600") {
			foundBackup = true
		}
	}
	if !foundBackup {
		t.Fatalf("dry-run must report restrictive backups: %#v", inspection.Backups)
	}
	afterSource := migrationListNames(t, source)
	afterTarget := migrationListNames(t, target)
	if len(beforeSource) != len(afterSource) || len(beforeTarget) != len(afterTarget) {
		t.Fatal("dry-run inspect must perform no writes")
	}
	raw, err := os.ReadFile(filepath.Join(source, "custom-sidecar.json"))
	if err != nil || string(raw) != "{\"custom\":true}\n" {
		t.Fatal("dry-run must leave unknown files untouched")
	}
}

func TestMigrationBackupsAreRestrictiveWithHashes(t *testing.T) {
	dir := t.TempDir()
	migrationWriteJSON(t, dir, "config.json", "{\"ok\":true}\n", 0o644)
	records, err := persist.BackupMigrationInputs(dir, []string{"config.json", "schedules.json"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].File != "config.json" || records[0].Hash == "" {
		t.Fatalf("backup must record hashes: %#v", records)
	}
	backupPath := filepath.Join(dir, "config.json.bak")
	raw, err := os.ReadFile(backupPath)
	if err != nil || string(raw) != "{\"ok\":true}\n" {
		t.Fatalf("backup must preserve bytes: %q %v", string(raw), err)
	}
	if mode := migrationFileMode(t, backupPath); mode != 0o600 {
		t.Fatalf("backups must be restrictive 0600, got %o", mode)
	}
	fingerprint, err := persist.FingerprintFile(filepath.Join(dir, "config.json"))
	if err != nil || fingerprint.Hash != records[0].Hash {
		t.Fatalf("backup hash must match fingerprint: %#v %#v", fingerprint, records)
	}
}

func TestMigrationApplyPublishesInOrderWithReceipt(t *testing.T) {
	dir := t.TempDir()
	plan := persist.NewStagedPlan(dir, dir, "host-identity", "v1/pi", map[string]string{}, persist.PhasePrepared)
	staged := map[string][]byte{
		"config.json":    []byte("{\"ok\":true}\n"),
		"schedules.json": []byte("{\"version\":1,\"jobs\":{},\"operations\":[]}\n"),
	}
	receipt, outcome, err := persist.ApplyStagedMigration(dir, plan, staged, persist.MigrationFaults{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != persist.OutcomeInstalled || !outcome.PrimaryVisible || outcome.MustReconcileLedger() {
		t.Fatalf("apply must be installed: %#v", outcome)
	}
	if err := receipt.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(receipt.OutputHashes) != 2 {
		t.Fatalf("receipt must carry output hashes: %#v", receipt)
	}
	for name, payload := range staged {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(raw) != string(payload) {
			t.Fatalf("primary %s not published: %q %v", name, string(raw), err)
		}
		if mode := migrationFileMode(t, filepath.Join(dir, name)); mode != 0o600 {
			t.Fatalf("primary %s must be restrictive 0600, got %o", name, mode)
		}
	}
	receiptPath := filepath.Join(dir, persist.MigrationReceiptName)
	if mode := migrationFileMode(t, receiptPath); mode != 0o600 {
		t.Fatalf("receipt must be restrictive 0600, got %o", mode)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json.migration-staged")); !os.IsNotExist(err) {
		t.Fatal("staging files must be cleaned after installed publish")
	}
	// Re-running identical staged content is idempotent.
	repeatPlan := persist.NewStagedPlan(dir, dir, "host-identity", "v1/pi", map[string]string{}, persist.PhasePrepared)
	if _, _, err := persist.ApplyStagedMigration(dir, repeatPlan, staged, persist.MigrationFaults{}); err != nil {
		t.Fatalf("identical re-apply must stay idempotent: %v", err)
	}
}

func TestMigrationApplyConflictsOnChangedInputs(t *testing.T) {
	dir := t.TempDir()
	migrationWriteJSON(t, dir, "schedules.json", "{\"version\":1,\"jobs\":{},\"operations\":[]}\n", 0o600)
	plan := persist.NewStagedPlan(dir, dir, "host-identity", "v1/pi", map[string]string{"schedules.json": "stale-hash"}, persist.PhasePrepared)
	staged := map[string][]byte{"schedules.json": []byte("{\"version\":1,\"jobs\":{},\"operations\":[]}\n")}
	_, outcome, err := persist.ApplyStagedMigration(dir, plan, staged, persist.MigrationFaults{})
	if err == nil {
		t.Fatal("changed inputs must conflict instead of merging opportunistically")
	}
	if outcome.Kind != persist.OutcomeKnownUncommitted || outcome.PrimaryVisible {
		t.Fatalf("conflict must stay known-uncommitted: %#v", outcome)
	}
}

func TestMigrationApplyInterruptionBeforeAfterEachPhase(t *testing.T) {
	prior := "{\"ok\":\"prior\"}\n"
	candidate := "{\"ok\":\"candidate\"}\n"
	preRename := map[string]persist.MigrationFaults{
		"BeforeInventory": {FailBeforeInventory: errors.New("inventory unavailable")},
		"AfterInventory":  {FailAfterInventory: errors.New("inventory lost")},
		"BeforeBackup":    {FailBeforeBackup: errors.New("backup unavailable")},
		"AfterBackup":     {FailAfterBackup: errors.New("backup lost")},
		"BeforeStaging":   {FailBeforeStaging: errors.New("staging unavailable")},
		"AfterStaging":    {FailAfterStaging: errors.New("staging lost")},
		"BeforePrimary":   {FailBeforePrimary: errors.New("primary unavailable")},
	}
	for name, faults := range preRename {
		t.Run("KnownUncommitted/"+name, func(t *testing.T) {
			dir := t.TempDir()
			migrationWriteJSON(t, dir, "config.json", prior, 0o600)
			plan := persist.NewStagedPlan(dir, dir, "host-identity", "v1/pi", map[string]string{}, persist.PhasePrepared)
			staged := map[string][]byte{"config.json": []byte(candidate)}
			_, outcome, err := persist.ApplyStagedMigration(dir, plan, staged, faults)
			if err == nil {
				t.Fatalf("%s must fail", name)
			}
			if outcome.Kind != persist.OutcomeKnownUncommitted || outcome.PrimaryVisible || outcome.MustReconcileLedger() {
				t.Fatalf("%s must stay known-uncommitted: %#v", name, outcome)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
			if err != nil || string(raw) != prior {
				t.Fatalf("%s changed the primary: %q", name, string(raw))
			}
		})
	}
	postRename := map[string]persist.MigrationFaults{
		"AfterPrimary":     {FailAfterPrimary: errors.New("primary uncertain")},
		"BeforeSync":       {FailBeforeSync: errors.New("sync unavailable")},
		"AfterSync":        {FailAfterSync: errors.New("sync lost")},
		"BeforeReceipt":    {FailBeforeReceipt: errors.New("receipt unavailable")},
		"AfterReceipt":     {FailAfterReceipt: errors.New("receipt lost")},
		"ResponseDelivery": {FailResponseDelivery: errors.New("response lost")},
	}
	for name, faults := range postRename {
		t.Run("DurabilityUncertain/"+name, func(t *testing.T) {
			dir := t.TempDir()
			migrationWriteJSON(t, dir, "config.json", prior, 0o600)
			plan := persist.NewStagedPlan(dir, dir, "host-identity", "v1/pi", map[string]string{}, persist.PhasePrepared)
			staged := map[string][]byte{"config.json": []byte(candidate)}
			_, outcome, err := persist.ApplyStagedMigration(dir, plan, staged, faults)
			if err == nil {
				t.Fatalf("%s must report an error", name)
			}
			if outcome.Kind != persist.OutcomeDurabilityUncertain || !outcome.PrimaryVisible || !outcome.MustReconcileLedger() {
				t.Fatalf("%s must stay durability-uncertain: %#v", name, outcome)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
			if err != nil || string(raw) != candidate {
				t.Fatalf("%s must leave the visible candidate for reconciliation: %q", name, string(raw))
			}
			if name == "AfterReceipt" || name == "ResponseDelivery" {
				if _, err := os.Stat(filepath.Join(dir, persist.MigrationReceiptName)); err != nil {
					t.Fatalf("%s must leave the receipt for reconciliation: %v", name, err)
				}
			}
		})
	}
}

func TestMigrationLostReceiptAndPartialStaging(t *testing.T) {
	dir := t.TempDir()
	plan := persist.NewStagedPlan(dir, dir, "host-identity", "v1/pi", map[string]string{}, persist.PhasePrepared)
	staged := map[string][]byte{"config.json": []byte("{\"ok\":true}\n")}
	if _, _, err := persist.ApplyStagedMigration(dir, plan, staged, persist.MigrationFaults{}); err != nil {
		t.Fatal(err)
	}
	// Lost receipt leaves primaries visible but unacknowledged.
	if err := os.Remove(filepath.Join(dir, persist.MigrationReceiptName)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil || string(raw) != "{\"ok\":true}\n" {
		t.Fatal("lost receipt must not remove the validated primary")
	}
	// Re-applying identical inputs restores the receipt without changing primaries.
	if _, outcome, err := persist.ApplyStagedMigration(dir, plan, staged, persist.MigrationFaults{}); err != nil || outcome.Kind != persist.OutcomeInstalled {
		t.Fatalf("identical re-apply must restore the receipt: %v %#v", err, outcome)
	}
	// Partial staging is never published as authority.
	if err := persist.StageConvertedFiles(dir, map[string][]byte{"config.json": []byte("{\"ok\":true}\n")}, []string{"config.json", "schedules.json"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "schedules.json")); !os.IsNotExist(err) {
		t.Fatal("partial staging must not create an unpublished primary")
	}
	if err := persist.StageConvertedFiles(dir, map[string][]byte{"unknown.json": []byte("{}")}, []string{"config.json"}); err == nil {
		t.Fatal("staging must refuse undeclared files")
	}
}
