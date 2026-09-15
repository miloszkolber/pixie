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

func TestMigrationApplyRejectsUnsafeOrUnmanagedNamesBeforeFilesystemChanges(t *testing.T) {
	for _, test := range []struct {
		name string
		file string
	}{
		{name: "empty", file: ""},
		{name: "absolute", file: "absolute-victim.json"},
		{name: "parent traversal", file: "../victim.json"},
		{name: "nested traversal", file: "nested/../../victim.json"},
		{name: "nested file", file: "nested/victim.json"},
		{name: "unmanaged file", file: "custom-sidecar.json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "data")
			victim := filepath.Join(root, "victim.json")
			if err := os.WriteFile(victim, []byte("{\"victim\":\"prior\"}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			name := test.file
			if test.name == "absolute" {
				name = filepath.Join(root, name)
			}
			plan := persist.NewStagedPlan(dir, dir, "host-identity", "v1/pi", map[string]string{}, persist.PhasePrepared)
			_, outcome, err := persist.ApplyStagedMigration(dir, plan, map[string][]byte{name: []byte("{\"candidate\":true}\n")}, persist.MigrationFaults{})
			if err == nil {
				t.Fatal("unsafe or unmanaged migration file must be rejected")
			}
			if outcome.Kind != persist.OutcomeKnownUncommitted || outcome.Stage != persist.StageValidate || outcome.PrimaryVisible || outcome.MustReconcileLedger() {
				t.Fatalf("unsafe or unmanaged file must not publish: %#v", outcome)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("rejected migration must not create its data directory: %v", err)
			}
			raw, err := os.ReadFile(victim)
			if err != nil || string(raw) != "{\"victim\":\"prior\"}\n" {
				t.Fatalf("rejected migration altered the victim: %q %v", raw, err)
			}
			for _, suffix := range []string{".bak", ".migration-staged"} {
				if _, err := os.Stat(victim + suffix); !os.IsNotExist(err) {
					t.Fatalf("rejected migration created victim%s: %v", suffix, err)
				}
			}
		})
	}
}

func TestMigrationReadFailuresAbortBeforeBackupStagingOrPrimary(t *testing.T) {
	for _, kind := range []string{"non-regular", "unreadable"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "unreadable" && os.Geteuid() == 0 {
				t.Skip("root can read mode-000 files")
			}
			dir := t.TempDir()
			mcpPrior := "{\"modules\":\"prior\"}\n"
			migrationWriteJSON(t, dir, "mcp-modules.json", mcpPrior, 0o600)
			configPath := filepath.Join(dir, "config.json")
			configPrior := "{\"config\":\"prior\"}\n"
			if kind == "non-regular" {
				if err := os.Mkdir(configPath, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				migrationWriteJSON(t, dir, "config.json", configPrior, 0o600)
				if err := os.Chmod(configPath, 0); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(configPath, 0o600)
			}

			records, err := persist.BackupMigrationInputs(dir, []string{"mcp-modules.json", "config.json"})
			if err == nil || len(records) != 0 {
				t.Fatalf("input read failure must abort backup without records: %#v %v", records, err)
			}
			plan := persist.NewStagedPlan(dir, dir, "host-identity", "v1/pi", map[string]string{}, persist.PhasePrepared)
			_, outcome, err := persist.ApplyStagedMigration(dir, plan, map[string][]byte{
				"config.json":      []byte("{\"config\":\"candidate\"}\n"),
				"mcp-modules.json": []byte("{\"modules\":\"candidate\"}\n"),
			}, persist.MigrationFaults{})
			if err == nil {
				t.Fatal("input read failure must abort migration")
			}
			if outcome.Kind != persist.OutcomeKnownUncommitted || outcome.Stage != persist.StageValidate || outcome.PrimaryVisible || outcome.MustReconcileLedger() {
				t.Fatalf("input read failure must be known-uncommitted before writes: %#v", outcome)
			}
			for _, name := range []string{
				"config.json.bak", "mcp-modules.json.bak",
				"config.json.migration-staged", "mcp-modules.json.migration-staged",
				persist.MigrationReceiptName,
			} {
				if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
					t.Fatalf("input read failure must not create %s: %v", name, err)
				}
			}
			mcp, err := os.ReadFile(filepath.Join(dir, "mcp-modules.json"))
			if err != nil || string(mcp) != mcpPrior {
				t.Fatalf("input read failure altered the readable primary: %q %v", mcp, err)
			}
			if kind == "non-regular" {
				info, err := os.Stat(configPath)
				if err != nil || !info.IsDir() {
					t.Fatalf("non-regular primary was altered: %#v %v", info, err)
				}
				return
			}
			if err := os.Chmod(configPath, 0o600); err != nil {
				t.Fatal(err)
			}
			config, err := os.ReadFile(configPath)
			if err != nil || string(config) != configPrior {
				t.Fatalf("unreadable primary was altered: %q %v", config, err)
			}
		})
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

func TestMigrationApplyBlocksMissingPrimaryWithRetainedOrUnreadableBackup(t *testing.T) {
	for _, test := range []struct {
		name                  string
		makeBackup            func(t *testing.T, path string)
		wantError             string
		assertBackupUntouched func(t *testing.T, path string)
	}{
		{
			name: "retained backup",
			makeBackup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("{\"version\":1,\"jobs\":{},\"operations\":[]}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantError: "backup remains",
			assertBackupUntouched: func(t *testing.T, path string) {
				t.Helper()
				raw, err := os.ReadFile(path)
				if err != nil || string(raw) != "{\"version\":1,\"jobs\":{},\"operations\":[]}\n" {
					t.Fatalf("retained backup changed: %q %v", raw, err)
				}
			},
		},
		{
			name: "unreadable backup",
			makeBackup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			wantError: "backup is unreadable",
			assertBackupUntouched: func(t *testing.T, path string) {
				t.Helper()
				info, err := os.Stat(path)
				if err != nil || !info.IsDir() {
					t.Fatalf("unreadable backup was altered: %#v %v", info, err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			primary := filepath.Join(dir, "schedules.json")
			backup := primary + ".bak"
			migrationWriteJSON(t, dir, "schedules.json", "{\"version\":1,\"jobs\":{},\"operations\":[]}\n", 0o600)
			test.makeBackup(t, backup)
			backupBefore, err := os.Lstat(backup)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(primary); err != nil {
				t.Fatal(err)
			}

			plan := persist.NewStagedPlan(dir, dir, "host-identity", "v1/pi", map[string]string{}, persist.PhasePrepared)
			_, outcome, err := persist.ApplyStagedMigration(dir, plan, map[string][]byte{
				"schedules.json": []byte("{\"version\":1,\"jobs\":{\"candidate\":{}},\"operations\":[]}\n"),
			}, persist.MigrationFaults{})
			if err == nil || !strings.Contains(err.Error(), "migration recovery conflict") || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("missing primary with %s must conflict: %v", test.name, err)
			}
			if outcome.Kind != persist.OutcomeKnownUncommitted || outcome.Stage != persist.StageValidate || outcome.PrimaryVisible || outcome.MustReconcileLedger() {
				t.Fatalf("recovery conflict must be known-uncommitted before writes: %#v", outcome)
			}
			for _, name := range []string{"schedules.json", "schedules.json.migration-staged", persist.MigrationReceiptName} {
				if _, statErr := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(statErr) {
					t.Fatalf("recovery conflict must not create %s: %v", name, statErr)
				}
			}
			test.assertBackupUntouched(t, backup)
			backupAfter, err := os.Lstat(backup)
			if err != nil || !os.SameFile(backupBefore, backupAfter) {
				t.Fatalf("recovery conflict must not replace the backup: %#v %#v %v", backupBefore, backupAfter, err)
			}
		})
	}
}

func TestMigrationApplyValidatesWholeBatchBeforeBackupOrStaging(t *testing.T) {
	dir := t.TempDir()
	prior := "{\"ok\":\"prior\"}\n"
	migrationWriteJSON(t, dir, "config.json", prior, 0o600)
	plan := persist.NewStagedPlan(dir, dir, "host-identity", "v1/pi", map[string]string{}, persist.PhasePrepared)
	_, outcome, err := persist.ApplyStagedMigration(dir, plan, map[string][]byte{
		"config.json":    []byte("{\"ok\":\"candidate\"}\n"),
		"schedules.json": []byte("not json"),
	}, persist.MigrationFaults{})
	if err == nil {
		t.Fatal("an invalid later staged file must reject the whole batch")
	}
	if outcome.Kind != persist.OutcomeKnownUncommitted || outcome.Stage != persist.StageValidate || outcome.PrimaryVisible || outcome.MustReconcileLedger() {
		t.Fatalf("invalid staging must be known-uncommitted before backup: %#v", outcome)
	}
	primary, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil || string(primary) != prior {
		t.Fatalf("invalid batch must preserve the primary: %q %v", primary, err)
	}
	for _, name := range []string{"config.json.bak", "config.json.migration-staged", "schedules.json.migration-staged"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("invalid batch must not leave %s: %v", name, err)
		}
	}
}

func TestMigrationApplySecondPrimaryFailureIsDurabilityUncertain(t *testing.T) {
	dir := t.TempDir()
	configPrior := "{\"config\":\"prior\"}\n"
	schedulePrior := "{\"schedule\":\"prior\"}\n"
	migrationWriteJSON(t, dir, "config.json", configPrior, 0o600)
	migrationWriteJSON(t, dir, "schedules.json", schedulePrior, 0o600)
	plan := persist.NewStagedPlan(dir, dir, "host-identity", "v1/pi", map[string]string{}, persist.PhasePrepared)
	_, outcome, err := persist.ApplyStagedMigration(dir, plan, map[string][]byte{
		"config.json":    []byte("{\"config\":\"candidate\"}\n"),
		"schedules.json": []byte("{\"schedule\":\"candidate\"}\n"),
	}, persist.MigrationFaults{FailPrimaryAt: 2, FailPrimary: errors.New("injected second primary failure")})
	if err == nil {
		t.Fatal("second primary failure must report an error")
	}
	if outcome.Kind != persist.OutcomeDurabilityUncertain || outcome.Stage != persist.StagePrimary || !outcome.PrimaryVisible || !outcome.MustReconcileLedger() {
		t.Fatalf("partial primary publication must be uncertain: %#v", outcome)
	}
	if outcome.MayDispatch() {
		t.Fatal("partial primary publication must block dependent work")
	}
	config, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil || string(config) != "{\"config\":\"candidate\"}\n" {
		t.Fatalf("first primary must remain visible for reconciliation: %q %v", config, err)
	}
	schedule, err := os.ReadFile(filepath.Join(dir, "schedules.json"))
	if err != nil || string(schedule) != schedulePrior {
		t.Fatalf("failed second primary must keep its prior value: %q %v", schedule, err)
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
