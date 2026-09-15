package persist_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/miloszkolber/pixie/internal/persist"
)

// Failure-mode matrix for single-file publication. Each row injects one
// realistic storage failure and pins the outcome kind, the commit stage and
// whether the primary becomes visible. A pre-rename failure must stay
// known-uncommitted with the prior primary intact; a post-rename durability
// failure must stay durability-uncertain with the candidate visible for
// reconciliation. Every row must also leave no atomic temp file behind.
func TestWriteWithOutcomeInjectedStorageFaultMatrix(t *testing.T) {
	type row struct {
		name          string
		faults        persist.PublishFaults
		wantKind      persist.OutcomeKind
		wantStage     persist.PublishStage
		wantPrimary   string // "prior" or "candidate"
		wantPublished bool
		wantErr       error
	}
	rows := []row{
		{
			name:        "disk-full temp create",
			faults:      persist.PublishFaults{PrimaryReplace: persist.ReplaceFaults{FailCreateTemp: syscall.ENOSPC}},
			wantKind:    persist.OutcomeKnownUncommitted,
			wantStage:   persist.StagePrimary,
			wantPrimary: "prior",
			wantErr:     syscall.ENOSPC,
		},
		{
			name:        "disk-full write",
			faults:      persist.PublishFaults{PrimaryReplace: persist.ReplaceFaults{FailWrite: syscall.ENOSPC}},
			wantKind:    persist.OutcomeKnownUncommitted,
			wantStage:   persist.StagePrimary,
			wantPrimary: "prior",
			wantErr:     syscall.ENOSPC,
		},
		{
			name:        "short write",
			faults:      persist.PublishFaults{PrimaryReplace: persist.ReplaceFaults{ShortWrite: 4}},
			wantKind:    persist.OutcomeKnownUncommitted,
			wantStage:   persist.StagePrimary,
			wantPrimary: "prior",
			wantErr:     io.ErrShortWrite,
		},
		{
			name:        "permission denied temp create",
			faults:      persist.PublishFaults{PrimaryReplace: persist.ReplaceFaults{FailCreateTemp: os.ErrPermission}},
			wantKind:    persist.OutcomeKnownUncommitted,
			wantStage:   persist.StagePrimary,
			wantPrimary: "prior",
			wantErr:     os.ErrPermission,
		},
		{
			name:        "permission denied rename",
			faults:      persist.PublishFaults{PrimaryReplace: persist.ReplaceFaults{FailRename: os.ErrPermission}},
			wantKind:    persist.OutcomeKnownUncommitted,
			wantStage:   persist.StagePrimary,
			wantPrimary: "prior",
			wantErr:     os.ErrPermission,
		},
		{
			name:        "file fsync failure",
			faults:      persist.PublishFaults{PrimaryReplace: persist.ReplaceFaults{FailFileSync: syscall.EIO}},
			wantKind:    persist.OutcomeKnownUncommitted,
			wantStage:   persist.StagePrimary,
			wantPrimary: "prior",
			wantErr:     syscall.EIO,
		},
		{
			name:        "backup disk-full",
			faults:      persist.PublishFaults{BackupReplace: persist.ReplaceFaults{FailWrite: syscall.ENOSPC}},
			wantKind:    persist.OutcomeKnownUncommitted,
			wantStage:   persist.StageBackup,
			wantPrimary: "prior",
			wantErr:     syscall.ENOSPC,
		},
		{
			name:          "directory fsync failure",
			faults:        persist.PublishFaults{FailDirSync: syscall.EIO},
			wantKind:      persist.OutcomeDurabilityUncertain,
			wantStage:     persist.StageDirSync,
			wantPrimary:   "candidate",
			wantPublished: true,
		},
		{
			name:          "reply lost",
			faults:        persist.PublishFaults{FailReply: syscall.EIO},
			wantKind:      persist.OutcomeDurabilityUncertain,
			wantStage:     persist.StageAcknowledge,
			wantPrimary:   "candidate",
			wantPublished: true,
		},
	}
	prior := outcomeDoc{Note: "prior"}
	candidate := outcomeDoc{Note: "candidate"}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			store := persist.Store{Dir: t.TempDir()}
			mustWriteOutcome(t, store, "doc.json", prior)
			path := filepath.Join(store.Dir, "doc.json")
			before := mustReadFile(t, path)

			outcome, err := persist.WriteWithOutcome(store, "doc.json", candidate, nil, tc.faults)
			if err == nil {
				t.Fatalf("injected failure must return an error: %#v", outcome)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("error %v does not wrap %v", err, tc.wantErr)
			}
			if outcome.Kind != tc.wantKind || outcome.Stage != tc.wantStage {
				t.Fatalf("outcome must be %s/%s: %#v", tc.wantKind, tc.wantStage, outcome)
			}
			if outcome.MustReconcileLedger() != (tc.wantKind == persist.OutcomeDurabilityUncertain) {
				t.Fatalf("reconcile flag does not match %s: %#v", tc.wantKind, outcome)
			}
			if outcome.MayDispatch() {
				t.Fatalf("failed publish must never allow dependent work: %#v", outcome)
			}
			switch tc.wantPrimary {
			case "prior":
				if got := mustReadFile(t, path); got != before {
					t.Fatalf("pre-rename failure changed the primary: %q", got)
				}
				if outcome.PrimaryVisible || len(outcome.Published) != 0 {
					t.Fatalf("pre-rename failure must not report a visible primary: %#v", outcome)
				}
			case "candidate":
				got := mustReadFile(t, path)
				if !strings.Contains(got, `"candidate"`) {
					t.Fatalf("post-rename failure must leave the candidate visible: %q", got)
				}
				if !outcome.PrimaryVisible || len(outcome.Published) != 1 || outcome.Published[0] != "doc.json" {
					t.Fatalf("post-rename failure must name the visible primary: %#v", outcome)
				}
			}
			if tc.wantPublished && len(outcome.Published) == 0 {
				t.Fatal("visible primary must be named in Published")
			}
			assertNoAtomicTemp(t, store.Dir)
		})
	}
}

// First-install behavior is deliberate only when neither the primary nor a
// retained backup exists. A missing primary with a backup, a dangling backup,
// or an unreadable primary must fail closed without an in-place write.
func TestWriteWithOutcomeFirstInstallRequiresAbsentPrimaryAndBackup(t *testing.T) {
	candidate := outcomeDoc{Note: "candidate"}

	t.Run("empty directory installs", func(t *testing.T) {
		store := persist.Store{Dir: t.TempDir()}
		outcome, err := persist.WriteWithOutcome(store, "doc.json", candidate, nil, persist.PublishFaults{})
		if err != nil || outcome.Kind != persist.OutcomeInstalled {
			t.Fatalf("absent primary and backup must install: %v %#v", err, outcome)
		}
		info, err := os.Stat(filepath.Join(store.Dir, "doc.json"))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("first install must be 0600: %#v %v", info, err)
		}
		if _, err := os.Stat(filepath.Join(store.Dir, "doc.json.bak")); !os.IsNotExist(err) {
			t.Fatalf("first install must not create a backup: %v", err)
		}
	})

	t.Run("retained backup fails closed", func(t *testing.T) {
		store := persist.Store{Dir: t.TempDir()}
		backupPath := filepath.Join(store.Dir, "doc.json.bak")
		backup := `{"note":"retained"}`
		if err := os.WriteFile(backupPath, []byte(backup), 0o600); err != nil {
			t.Fatal(err)
		}
		outcome, err := persist.WriteWithOutcome(store, "doc.json", candidate, nil, persist.PublishFaults{})
		if err == nil || outcome.Kind != persist.OutcomeKnownUncommitted || outcome.Stage != persist.StageValidate {
			t.Fatalf("missing primary with a backup must fail closed: %v %#v", err, outcome)
		}
		if _, err := os.Stat(filepath.Join(store.Dir, "doc.json")); !os.IsNotExist(err) {
			t.Fatalf("fail-closed first-install must not create the primary: %v", err)
		}
		if got := mustReadFile(t, backupPath); got != backup {
			t.Fatalf("retained backup must stay untouched: %q", got)
		}
	})

	t.Run("dangling backup fails closed", func(t *testing.T) {
		store := persist.Store{Dir: t.TempDir()}
		if err := os.Symlink(filepath.Join(store.Dir, "missing-target"), filepath.Join(store.Dir, "doc.json.bak")); err != nil {
			t.Fatal(err)
		}
		outcome, err := persist.WriteWithOutcome(store, "doc.json", candidate, nil, persist.PublishFaults{})
		if err == nil || outcome.Kind != persist.OutcomeKnownUncommitted || outcome.Stage != persist.StageValidate {
			t.Fatalf("dangling backup must fail closed: %v %#v", err, outcome)
		}
		if _, err := os.Stat(filepath.Join(store.Dir, "doc.json")); !os.IsNotExist(err) {
			t.Fatalf("fail-closed first-install must not create the primary: %v", err)
		}
	})

	t.Run("unreadable primary fails closed", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root can read mode-000 files")
		}
		store := persist.Store{Dir: t.TempDir()}
		mustWriteOutcome(t, store, "doc.json", outcomeDoc{Note: "prior"})
		path := filepath.Join(store.Dir, "doc.json")
		before := mustReadFile(t, path)
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(path, 0o600)
		outcome, err := persist.WriteWithOutcome(store, "doc.json", candidate, nil, persist.PublishFaults{})
		if err == nil || outcome.Kind != persist.OutcomeKnownUncommitted || outcome.Stage != persist.StageValidate {
			t.Fatalf("unreadable primary must fail closed: %v %#v", err, outcome)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := mustReadFile(t, path); got != before {
			t.Fatalf("unreadable primary must not be replaced: %q", got)
		}
	})

	t.Run("invalid primary is preserved as backup", func(t *testing.T) {
		store := persist.Store{Dir: t.TempDir()}
		path := filepath.Join(store.Dir, "doc.json")
		if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		outcome, err := persist.WriteWithOutcome(store, "doc.json", candidate, nil, persist.PublishFaults{})
		if err != nil || outcome.Kind != persist.OutcomeInstalled {
			t.Fatalf("invalid primary is a repair, not a first install: %v %#v", err, outcome)
		}
		if got := mustReadFile(t, filepath.Join(store.Dir, "doc.json.bak")); got != "not json" {
			t.Fatalf("invalid prior bytes must be preserved as backup: %q", got)
		}
		if got := mustReadFile(t, path); !strings.Contains(got, `"candidate"`) {
			t.Fatalf("repair must publish the candidate: %q", got)
		}
	})
}

// Multi-file migration matrix. Pre-rename storage failures leave every prior
// primary intact (known-uncommitted); a failure after an earlier primary
// published is durability-uncertain and names the visible primary. Staging
// before publication is not authority: a crash between staging and publication
// leaves staged residue but no changed primary, and a retry is idempotent.
func TestApplyStagedMigrationInjectedStorageFaultMatrix(t *testing.T) {
	type row struct {
		name          string
		faults        persist.MigrationFaults
		wantKind      persist.OutcomeKind
		wantStage     persist.PublishStage
		wantConfig    string
		wantSchedule  string
		wantPublished []string
		wantStaged    bool
	}
	rows := []row{
		{
			name:         "backup disk-full",
			faults:       persist.MigrationFaults{BackupReplace: persist.ReplaceFaults{FailWrite: syscall.ENOSPC}},
			wantKind:     persist.OutcomeKnownUncommitted,
			wantStage:    persist.StageBackup,
			wantConfig:   "prior",
			wantSchedule: "prior",
		},
		{
			name:         "staging disk-full",
			faults:       persist.MigrationFaults{StagingReplace: persist.ReplaceFaults{FailWrite: syscall.ENOSPC}},
			wantKind:     persist.OutcomeKnownUncommitted,
			wantStage:    persist.StageBackup,
			wantConfig:   "prior",
			wantSchedule: "prior",
		},
		{
			name:         "staging short write",
			faults:       persist.MigrationFaults{StagingReplace: persist.ReplaceFaults{ShortWrite: 3}},
			wantKind:     persist.OutcomeKnownUncommitted,
			wantStage:    persist.StageBackup,
			wantConfig:   "prior",
			wantSchedule: "prior",
		},
		{
			name:         "primary rename denied",
			faults:       persist.MigrationFaults{PrimaryReplace: persist.ReplaceFaults{FailRename: os.ErrPermission}},
			wantKind:     persist.OutcomeKnownUncommitted,
			wantStage:    persist.StagePrimary,
			wantConfig:   "prior",
			wantSchedule: "prior",
			wantStaged:   true,
		},
		{
			name: "second primary disk-full",
			faults: persist.MigrationFaults{
				PrimaryReplace:   persist.ReplaceFaults{FailWrite: syscall.ENOSPC},
				PrimaryReplaceAt: 2,
			},
			wantKind:      persist.OutcomeDurabilityUncertain,
			wantStage:     persist.StagePrimary,
			wantConfig:    "candidate",
			wantSchedule:  "prior",
			wantPublished: []string{"config.json"},
			wantStaged:    true,
		},
		{
			name:         "crash after staging",
			faults:       persist.MigrationFaults{FailAfterStaging: errors.New("crash after staging")},
			wantKind:     persist.OutcomeKnownUncommitted,
			wantStage:    persist.StageBackup,
			wantConfig:   "prior",
			wantSchedule: "prior",
			wantStaged:   true,
		},
		{
			name:         "crash before primary",
			faults:       persist.MigrationFaults{FailBeforePrimary: errors.New("crash before primary")},
			wantKind:     persist.OutcomeKnownUncommitted,
			wantStage:    persist.StagePrimary,
			wantConfig:   "prior",
			wantSchedule: "prior",
			wantStaged:   true,
		},
		{
			name:          "directory fsync failure",
			faults:        persist.MigrationFaults{FailAfterSync: errors.New("dir sync lost")},
			wantKind:      persist.OutcomeDurabilityUncertain,
			wantStage:     persist.StageDirSync,
			wantConfig:    "candidate",
			wantSchedule:  "candidate",
			wantPublished: []string{"config.json", "schedules.json"},
			wantStaged:    true,
		},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.json")
			schedulePath := filepath.Join(dir, "schedules.json")
			writePersistTestFile(t, configPath, "{\"config\":\"prior\"}\n")
			writePersistTestFile(t, schedulePath, "{\"schedule\":\"prior\"}\n")
			staged := map[string][]byte{
				"config.json":    []byte("{\"config\":\"candidate\"}\n"),
				"schedules.json": []byte("{\"schedule\":\"candidate\"}\n"),
			}
			plan := persist.NewStagedPlan(dir, dir, "host-identity", "v1/pi", map[string]string{}, persist.PhasePrepared)
			_, outcome, err := persist.ApplyStagedMigration(dir, plan, staged, tc.faults)
			if err == nil {
				t.Fatalf("injected failure must return an error: %#v", outcome)
			}
			if outcome.Kind != tc.wantKind || outcome.Stage != tc.wantStage {
				t.Fatalf("outcome must be %s/%s: %#v", tc.wantKind, tc.wantStage, outcome)
			}
			if outcome.MayDispatch() {
				t.Fatalf("failed migration must block dependent work: %#v", outcome)
			}
			wantConfig, wantSchedule := "prior", "prior"
			if tc.wantConfig == "candidate" {
				wantConfig = "candidate"
			}
			if tc.wantSchedule == "candidate" {
				wantSchedule = "candidate"
			}
			if got := mustReadFile(t, configPath); !strings.Contains(got, `"`+wantConfig+`"`) {
				t.Fatalf("config primary must be %s: %q", wantConfig, got)
			}
			if got := mustReadFile(t, schedulePath); !strings.Contains(got, `"`+wantSchedule+`"`) {
				t.Fatalf("schedules primary must be %s: %q", wantSchedule, got)
			}
			if len(outcome.Published) != len(tc.wantPublished) {
				t.Fatalf("published set %v does not match %v", outcome.Published, tc.wantPublished)
			}
			for i, name := range tc.wantPublished {
				if outcome.Published[i] != name {
					t.Fatalf("published[%d] = %q, want %q", i, outcome.Published[i], name)
				}
			}
			_, stagedErr := os.Stat(filepath.Join(dir, "config.json.migration-staged"))
			if tc.wantStaged && stagedErr != nil {
				t.Fatalf("staging residue must remain for retry: %v", stagedErr)
			}
			if !tc.wantStaged && !os.IsNotExist(stagedErr) {
				t.Fatalf("cleanup case must not leave staging residue: %v", stagedErr)
			}
			assertNoAtomicTemp(t, dir)
		})
	}
}

// A crash between staging and publication leaves no changed primary and a
// retry publishes idempotently instead of treating staging files as authority.
func TestApplyStagedMigrationRetryAfterStagingCrash(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	writePersistTestFile(t, configPath, "{\"config\":\"prior\"}\n")
	staged := map[string][]byte{"config.json": []byte("{\"config\":\"candidate\"}\n")}
	plan := persist.NewStagedPlan(dir, dir, "host-identity", "v1/pi", map[string]string{}, persist.PhasePrepared)

	_, outcome, err := persist.ApplyStagedMigration(dir, plan, staged, persist.MigrationFaults{FailAfterStaging: errors.New("crash")})
	if err == nil || outcome.Kind != persist.OutcomeKnownUncommitted {
		t.Fatalf("staging crash must be known-uncommitted: %v %#v", err, outcome)
	}
	if got := mustReadFile(t, configPath); !strings.Contains(got, `"prior"`) {
		t.Fatalf("staging crash must not publish: %q", got)
	}

	receipt, outcome, err := persist.ApplyStagedMigration(dir, plan, staged, persist.MigrationFaults{})
	if err != nil || outcome.Kind != persist.OutcomeInstalled {
		t.Fatalf("retry after staging crash must publish: %v %#v", err, outcome)
	}
	if len(receipt.OutputHashes) != 1 {
		t.Fatalf("retry receipt must carry output hashes: %#v", receipt)
	}
	if got := mustReadFile(t, configPath); !strings.Contains(got, `"candidate"`) {
		t.Fatalf("retry must publish the candidate: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json.migration-staged")); !os.IsNotExist(err) {
		t.Fatalf("installed publish must clean staging: %v", err)
	}
}

// A partial multi-file commit that leaves an authority ledger visible while
// its dependent stays prior must report exactly the authority file, so the
// caller reconciles the authority primary and never replays a backup.
func TestApplyStagedMigrationPartialAuthorityPublicationIsExplicit(t *testing.T) {
	dir := t.TempDir()
	authorityPath := filepath.Join(dir, "pi-pairing-authority.json")
	dependentPath := filepath.Join(dir, "pi-project-sessions.json")
	writePersistTestFile(t, authorityPath, "{\"generation\":\"prior\"}\n")
	writePersistTestFile(t, dependentPath, "{\"records\":\"prior\"}\n")
	writePersistTestFile(t, authorityPath+".bak", "{\"generation\":\"older-backup\"}\n")
	writePersistTestFile(t, dependentPath+".bak", "{\"records\":\"older-backup\"}\n")

	staged := map[string][]byte{
		"pi-pairing-authority.json": []byte("{\"generation\":\"candidate\"}\n"),
		"pi-project-sessions.json":  []byte("{\"records\":\"candidate\"}\n"),
	}
	plan := persist.NewStagedPlan(dir, dir, "host-identity", "v1/pi", map[string]string{}, persist.PhasePrepared)
	_, outcome, err := persist.ApplyStagedMigration(dir, plan, staged, persist.MigrationFaults{
		PrimaryReplace:   persist.ReplaceFaults{FailWrite: syscall.ENOSPC},
		PrimaryReplaceAt: 2,
	})
	if err == nil {
		t.Fatalf("partial authority publication must error: %#v", outcome)
	}
	if outcome.Kind != persist.OutcomeDurabilityUncertain || outcome.Stage != persist.StagePrimary {
		t.Fatalf("partial authority publication must be durability-uncertain: %#v", outcome)
	}
	if len(outcome.Published) != 1 || outcome.Published[0] != "pi-pairing-authority.json" {
		t.Fatalf("partial publication must name only the visible authority: %#v", outcome.Published)
	}
	if got := mustReadFile(t, authorityPath); !strings.Contains(got, `"candidate"`) {
		t.Fatalf("visible authority must hold the candidate: %q", got)
	}
	if got := mustReadFile(t, dependentPath); !strings.Contains(got, `"prior"`) {
		t.Fatalf("dependent must stay prior until it publishes: %q", got)
	}

	// Reconciliation is driven by Published and reads only the authority
	// primary; the retained backups are never replayed.
	reconciled, err := persist.ReconcileMigrationPrimaries(dir, outcome.Published)
	if err != nil {
		t.Fatal(err)
	}
	if string(reconciled["pi-pairing-authority.json"]) != "{\"generation\":\"candidate\"}\n" {
		t.Fatalf("reconcile must read the visible authority primary: %q", reconciled["pi-pairing-authority.json"])
	}
}

// Recovery after a durability-uncertain publication reads only the named
// primaries, never a backup or staged generation, and fails closed on a
// missing or invalid primary.
func TestReconcileMigrationPrimariesReadsOnlyPrimaries(t *testing.T) {
	dir := t.TempDir()
	writePersistTestFile(t, filepath.Join(dir, "config.json"), "{\"source\":\"primary\"}\n")
	writePersistTestFile(t, filepath.Join(dir, "config.json.bak"), "{\"source\":\"backup\"}\n")
	writePersistTestFile(t, filepath.Join(dir, "config.json.migration-staged"), "{\"source\":\"staged\"}\n")

	validated, err := persist.ReconcileMigrationPrimaries(dir, []string{"config.json"})
	if err != nil {
		t.Fatal(err)
	}
	if string(validated["config.json"]) != "{\"source\":\"primary\"}\n" {
		t.Fatalf("reconcile must read the committed primary: %q", validated["config.json"])
	}

	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := persist.ReconcileMigrationPrimaries(dir, []string{"config.json"}); err == nil {
		t.Fatal("corrupt primary must stay unresolved instead of replaying backup or staging")
	}

	if err := os.Remove(filepath.Join(dir, "config.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := persist.ReconcileMigrationPrimaries(dir, []string{"config.json"}); err == nil {
		t.Fatal("missing primary must stay unresolved while a backup remains")
	}

	for _, name := range []string{"../victim.json", "custom-sidecar.json", "nested/config.json", ""} {
		if _, err := persist.ReconcileMigrationPrimaries(dir, []string{name}); err == nil {
			t.Fatalf("reconcile must reject unmanaged path %q", name)
		}
	}
}

// Rollback validates the whole selected backup batch before replacing any
// primary, so a corrupt later backup cannot leave a partial runnable restore
// or overwrite a committed primary with a stale generation.
func TestApplyMigrationRollbackValidatesWholeBatchBeforeRestore(t *testing.T) {
	dir := t.TempDir()
	writePersistTestFile(t, filepath.Join(dir, "config.json"), "{\"config\":\"current\"}\n")
	writePersistTestFile(t, filepath.Join(dir, "config.json.bak"), "{\"config\":\"backup\"}\n")
	writePersistTestFile(t, filepath.Join(dir, "mcp-modules.json"), "{\"modules\":\"current\"}\n")
	writePersistTestFile(t, filepath.Join(dir, "mcp-modules.json.bak"), "not json")

	before := mustReadFile(t, filepath.Join(dir, "config.json"))
	_, outcome, err := persist.ApplyMigrationRollback(dir, []string{"config.json", "mcp-modules.json"}, persist.MigrationRollbackFaults{}, false)
	if err == nil {
		t.Fatalf("a corrupt backup must reject the whole batch: %#v", outcome)
	}
	if outcome.Kind != persist.OutcomeKnownUncommitted || outcome.Stage != persist.StageBackup || outcome.PrimaryVisible {
		t.Fatalf("invalid batch must stay known-uncommitted: %#v", outcome)
	}
	if len(outcome.Published) != 0 {
		t.Fatalf("known-uncommitted batch must publish nothing: %#v", outcome)
	}
	if got := mustReadFile(t, filepath.Join(dir, "config.json")); got != before {
		t.Fatalf("valid earlier backup must not be restored before validation: %q", got)
	}
	if got := mustReadFile(t, filepath.Join(dir, "mcp-modules.json")); !strings.Contains(got, `"current"`) {
		t.Fatalf("corrupt backup must not replace the primary: %q", got)
	}
	assertNoAtomicTemp(t, dir)
}

// Rollback storage faults: the first selected restore failure is
// known-uncommitted with no primary replaced; a later restore failure is
// durability-uncertain and names the already-restored primaries.
func TestApplyMigrationRollbackInjectedStorageFaultMatrix(t *testing.T) {
	for _, tc := range []struct {
		name          string
		faults        persist.MigrationRollbackFaults
		wantKind      persist.OutcomeKind
		wantStage     persist.PublishStage
		wantPublished []string
		wantConfig    string
	}{
		{
			name:       "first restore rename denied",
			faults:     persist.MigrationRollbackFaults{RestoreReplace: persist.ReplaceFaults{FailRename: os.ErrPermission}},
			wantKind:   persist.OutcomeKnownUncommitted,
			wantStage:  persist.StagePrimary,
			wantConfig: "current",
		},
		{
			name:          "second restore disk-full",
			faults:        persist.MigrationRollbackFaults{RestoreReplace: persist.ReplaceFaults{FailWrite: syscall.ENOSPC}, RestoreReplaceAt: 2},
			wantKind:      persist.OutcomeDurabilityUncertain,
			wantStage:     persist.StagePrimary,
			wantPublished: []string{"config.json"},
			wantConfig:    "backup",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writePersistTestFile(t, filepath.Join(dir, "config.json"), "{\"config\":\"current\"}\n")
			writePersistTestFile(t, filepath.Join(dir, "config.json.bak"), "{\"config\":\"backup\"}\n")
			writePersistTestFile(t, filepath.Join(dir, "mcp-modules.json"), "{\"modules\":\"current\"}\n")
			writePersistTestFile(t, filepath.Join(dir, "mcp-modules.json.bak"), "{\"modules\":\"backup\"}\n")

			_, outcome, err := persist.ApplyMigrationRollback(dir, []string{"config.json", "mcp-modules.json"}, tc.faults, false)
			if err == nil {
				t.Fatalf("injected restore failure must error: %#v", outcome)
			}
			if outcome.Kind != tc.wantKind || outcome.Stage != tc.wantStage {
				t.Fatalf("outcome must be %s/%s: %#v", tc.wantKind, tc.wantStage, outcome)
			}
			if len(outcome.Published) != len(tc.wantPublished) {
				t.Fatalf("published %v does not match %v", outcome.Published, tc.wantPublished)
			}
			for i, name := range tc.wantPublished {
				if outcome.Published[i] != name {
					t.Fatalf("published[%d] = %q, want %q", i, outcome.Published[i], name)
				}
			}
			if got := mustReadFile(t, filepath.Join(dir, "config.json")); !strings.Contains(got, `"`+tc.wantConfig+`"`) {
				t.Fatalf("config primary must be %s: %q", tc.wantConfig, got)
			}
			if got := mustReadFile(t, filepath.Join(dir, "mcp-modules.json")); !strings.Contains(got, `"current"`) {
				t.Fatalf("failed second restore must keep the prior primary: %q", got)
			}
			assertNoAtomicTemp(t, dir)
		})
	}
}

func writePersistTestFile(t *testing.T, path, payload string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func assertNoAtomicTemp(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("atomic replacement left temp files: %v", matches)
	}
}
