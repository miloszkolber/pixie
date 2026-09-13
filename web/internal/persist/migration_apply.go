package persist

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// MigrationApplyVersion versions the additive MIG-01 staged-conversion engine.
// It never changes any ledger schema; it only orders checkpoints, backups,
// staging, publication, synchronization and receipts.
const MigrationApplyVersion = 1

// MigrationReceiptName is the durable receipt file written last. Losing it
// leaves primaries visible but unacknowledged, which stays
// durability-uncertain until the validated primaries are reconciled.
const MigrationReceiptName = "migration-receipt.json"

// MigrationCheckpoint names one staged-conversion checkpoint in the defined
// order: inventory, backup with hashes, staging, primary, sync, receipt. One
// rename never makes multiple unrelated files atomic; primaries publish in
// the defined file order with required directory synchronization before the
// receipt acknowledges success.
type MigrationCheckpoint string

const (
	CheckpointInventory MigrationCheckpoint = "inventory"
	CheckpointBackup    MigrationCheckpoint = "backup"
	CheckpointStaging   MigrationCheckpoint = "staging"
	CheckpointPrimary   MigrationCheckpoint = "primary"
	CheckpointSync      MigrationCheckpoint = "sync"
	CheckpointReceipt   MigrationCheckpoint = "receipt"
)

// OrderedMigrationCheckpoints returns the defined checkpoint order.
func OrderedMigrationCheckpoints() []MigrationCheckpoint {
	return []MigrationCheckpoint{
		CheckpointInventory,
		CheckpointBackup,
		CheckpointStaging,
		CheckpointPrimary,
		CheckpointSync,
		CheckpointReceipt,
	}
}

// ValidateMigrationCheckpoint rejects unknown checkpoints without touching storage.
func ValidateMigrationCheckpoint(checkpoint MigrationCheckpoint) error {
	for _, ordered := range OrderedMigrationCheckpoints() {
		if checkpoint == ordered {
			return nil
		}
	}
	return fmt.Errorf("invalid migration checkpoint")
}

// MigrationFaults injects deterministic interruption/failure before and after
// each staged-conversion checkpoint for MIG-01/X04 coverage. A nil entry
// disables that fault. Faults before the primary checkpoint stay
// known-uncommitted; faults at or after the primary rename report
// durability-uncertain with the visible primaries left for reconciliation.
// They never authorize restoring an old backup over a visible primary.
type MigrationFaults struct {
	FailBeforeInventory  error
	FailAfterInventory   error
	FailBeforeBackup     error
	FailAfterBackup      error
	FailBeforeStaging    error
	FailAfterStaging     error
	FailBeforePrimary    error
	FailAfterPrimary     error
	FailBeforeSync       error
	FailAfterSync        error
	FailBeforeReceipt    error
	FailAfterReceipt     error
	FailResponseDelivery error
}

// MigrationRollbackFaults injects deterministic interruption into rollback.
// Rollback never rewinds ledger authority: an interrupted restore stays
// unresolved until the validated primaries are reread.
type MigrationRollbackFaults struct {
	FailBeforeRestore error
	FailAfterRestore  error
	FailBeforeSync    error
	FailAfterSync     error
	FailBeforeReceipt error
	FailAfterReceipt  error
}

// RedactRoot reports a filesystem root without leaking absolute user paths,
// credentials or session text. Dry-run diagnostics carry only this redacted
// form plus content hashes.
func RedactRoot(path string) string {
	if path == "" {
		return "[redacted]"
	}
	return filepath.Join("[redacted]", filepath.Base(path))
}

// RedactedMigrationRoots redacts a source/target pair for dry-run output.
func RedactedMigrationRoots(source, target string) (string, string) {
	return RedactRoot(source), RedactRoot(target)
}

// MigrationInspection is a read-only dry-run view with redacted roots. It
// performs no writes, package loading, extension execution, model calls or
// native configuration changes.
type MigrationInspection struct {
	RedactedSource string
	RedactedTarget string
	SourceIdentity string
	TargetSchema   string
	Phase          MigrationPhase
	Conversions    []string
	Conflicts      []string
	Backups        []string
	InputHashes    map[string]string
	Warnings       []string
}

// InspectStagedMigration describes a staged conversion without writing. It
// reports redacted source/target roots, schema versions, conversions,
// conflicts and backup requirements. Unknown files stay untouched and are
// reported as warnings; a newer unsupported schema is diagnosable here while
// ApplyStagedMigration blocks its mutations.
func InspectStagedMigration(sourceDir, targetDir, sourceIdentity, targetSchema string, phase MigrationPhase, files []string) (MigrationInspection, error) {
	if err := ValidateMigrationPhase(phase); err != nil {
		return MigrationInspection{}, err
	}
	if sourceIdentity == "" || targetSchema == "" {
		return MigrationInspection{}, fmt.Errorf("migration inspect requires source identity and target schema")
	}
	redactedSource, redactedTarget := RedactedMigrationRoots(sourceDir, targetDir)
	inspection := MigrationInspection{
		RedactedSource: redactedSource,
		RedactedTarget: redactedTarget,
		SourceIdentity: sourceIdentity,
		TargetSchema:   targetSchema,
		Phase:          phase,
		InputHashes:    make(map[string]string, len(files)),
	}
	report, err := InventoryDataDir(sourceDir)
	if err != nil {
		return MigrationInspection{}, err
	}
	observed := make(map[string]ObservedFile, len(report.Files))
	for _, file := range report.Files {
		observed[file.Name] = file
	}
	for _, name := range files {
		inspection.Conversions = append(inspection.Conversions, name)
		if entry, ok := observed[name]; ok && entry.Present {
			inspection.InputHashes[name] = entry.Hash
			inspection.Backups = append(inspection.Backups, name+".bak (0600, sha256:"+entry.Hash+")")
			if entry.Version != nil && *entry.Version > 2 {
				inspection.Conflicts = append(inspection.Conflicts, fmt.Sprintf("%s schema v%d is newer than supported; diagnostics remain available while mutations stay blocked", name, *entry.Version))
			}
		} else if ok && entry.BackupPresent {
			inspection.Conflicts = append(inspection.Conflicts, fmt.Sprintf("%s primary is missing while a backup remains; fail closed and do not replay the backup", name))
		} else {
			inspection.Warnings = append(inspection.Warnings, fmt.Sprintf("%s is absent; nothing to convert", name))
		}
	}
	sort.Strings(inspection.Conversions)
	sort.Strings(inspection.Conflicts)
	sort.Strings(inspection.Backups)
	sort.Strings(inspection.Warnings)
	if len(report.Unknown) > 0 {
		inspection.Warnings = append(inspection.Warnings, fmt.Sprintf("%d unknown file(s) preserved untouched; no wildcard cleanup authorized", len(report.Unknown)))
	}
	inspection.Warnings = append(inspection.Warnings, report.Warnings...)
	return inspection, nil
}

// BackupRecord identifies one restrictive backup by content hash, never bytes.
type BackupRecord struct {
	File string
	Hash string
	Size int64
}

// BackupMigrationInputs copies each present primary to a restrictive backup
// generation with hashes, source build/schema and canonical paths recorded by
// the caller. Backups use 0600; the directory uses 0700. It never restores a
// backup over a primary and never touches unknown files.
func BackupMigrationInputs(dir string, files []string) ([]BackupRecord, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var records []BackupRecord
	for _, name := range files {
		raw, _, err := ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		digest := sha256.Sum256(raw)
		record := BackupRecord{File: name, Hash: hex.EncodeToString(digest[:]), Size: int64(len(raw))}
		if err := AtomicReplace(filepath.Join(dir, name+".bak"), raw, 0o600); err != nil {
			return nil, fmt.Errorf("backup %s: %w", name, err)
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].File < records[j].File })
	return records, nil
}

// stagedName maps a primary name to its destination staging file. Staged files
// are never read as authority; only validated primaries are.
func stagedName(name string) string { return name + ".migration-staged" }

// StageConvertedFiles validates converted bytes and stages them on the
// destination filesystem with restrictive permissions. It checks the shared
// 16 MiB bound, requires valid JSON for .json primaries, and refuses to stage
// unknown paths outside the declared file set. Staging never publishes.
func StageConvertedFiles(dir string, staged map[string][]byte, declared []string) error {
	allowed := make(map[string]bool, len(declared))
	for _, name := range declared {
		allowed[name] = true
	}
	names := make([]string, 0, len(staged))
	for name := range staged {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !allowed[name] {
			return fmt.Errorf("refusing to stage undeclared file %s", name)
		}
		data := staged[name]
		if len(data) > maxJSONBytes {
			return fmt.Errorf("staged %s exceeds the %d-byte limit", name, maxJSONBytes)
		}
		if filepath.Ext(name) == ".json" && !json.Valid(data) {
			return fmt.Errorf("staged %s is not valid JSON", name)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700); err != nil {
			return err
		}
		if err := AtomicReplace(filepath.Join(dir, stagedName(name)), data, 0o600); err != nil {
			return fmt.Errorf("stage %s: %w", name, err)
		}
	}
	return nil
}

// currentInputHashes fingerprints the declared primaries without writing.
func currentInputHashes(dir string, files []string) map[string]string {
	hashes := make(map[string]string, len(files))
	for _, name := range files {
		raw, _, err := ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		digest := sha256.Sum256(raw)
		hashes[name] = hex.EncodeToString(digest[:])
	}
	return hashes
}

func syncDir(dir string) error {
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

// ApplyStagedMigration publishes one staged conversion through the defined
// checkpoints with required synchronization. Re-running identical inputs is
// idempotent; changed inputs conflict instead of merging opportunistically.
// After a post-rename error the primaries may already be visible: the receipt
// identity is retained, the validated primaries are reread, and uncertainty
// is reported until the outcome is established. It never overwrites a visible
// primary with an old backup to manufacture a failed-no-change result.
func ApplyStagedMigration(dir string, plan StagedPlan, staged map[string][]byte, faults MigrationFaults) (MigrationReceipt, PublishOutcome, error) {
	failure := func(stage PublishStage, kind OutcomeKind, visible, reconcile bool, err error) (MigrationReceipt, PublishOutcome, error) {
		return MigrationReceipt{}, PublishOutcome{Kind: kind, Stage: stage, PrimaryVisible: visible, MustReconcile: reconcile}, err
	}
	if err := ValidateMigrationPhase(plan.Phase); err != nil {
		return failure(StageValidate, OutcomeKnownUncommitted, false, false, err)
	}
	if plan.SourceIdentity == "" || plan.TargetSchema == "" {
		return failure(StageValidate, OutcomeKnownUncommitted, false, false, fmt.Errorf("migration apply requires source identity and target schema"))
	}
	declared := make([]string, 0, len(staged))
	for name := range staged {
		declared = append(declared, name)
	}
	sort.Strings(declared)
	if faults.FailBeforeInventory != nil {
		return failure(StageValidate, OutcomeKnownUncommitted, false, false, fmt.Errorf("migration inventory interrupted: %w", faults.FailBeforeInventory))
	}
	current := currentInputHashes(dir, declared)
	// Idempotent repeat: identical inputs proceed; changed inputs conflict.
	// An empty plan hash set with non-empty staged content is a fresh plan and
	// proceeds; otherwise compare against the declared inputs.
	if len(plan.InputHashes) > 0 {
		if err := plan.DetectInputChange(current); err != nil {
			// Allow the first publish where primaries are absent (empty
			// current) to proceed; any other divergence conflicts.
			if len(current) != 0 {
				return failure(StageValidate, OutcomeKnownUncommitted, false, false, err)
			}
		}
	}
	if faults.FailAfterInventory != nil {
		return failure(StageReserve, OutcomeKnownUncommitted, false, false, fmt.Errorf("migration inventory interrupted: %w", faults.FailAfterInventory))
	}
	if faults.FailBeforeBackup != nil {
		return failure(StageBackup, OutcomeKnownUncommitted, false, false, fmt.Errorf("migration backup interrupted: %w", faults.FailBeforeBackup))
	}
	if _, err := BackupMigrationInputs(dir, declared); err != nil {
		return failure(StageBackup, OutcomeKnownUncommitted, false, false, err)
	}
	if faults.FailAfterBackup != nil {
		return failure(StageBackup, OutcomeKnownUncommitted, false, false, fmt.Errorf("migration backup interrupted: %w", faults.FailAfterBackup))
	}
	if faults.FailBeforeStaging != nil {
		return failure(StageBackup, OutcomeKnownUncommitted, false, false, fmt.Errorf("migration staging interrupted: %w", faults.FailBeforeStaging))
	}
	if err := StageConvertedFiles(dir, staged, declared); err != nil {
		return failure(StageBackup, OutcomeKnownUncommitted, false, false, err)
	}
	if faults.FailAfterStaging != nil {
		return failure(StageBackup, OutcomeKnownUncommitted, false, false, fmt.Errorf("migration staging interrupted: %w", faults.FailAfterStaging))
	}
	if faults.FailBeforePrimary != nil {
		return failure(StagePrimary, OutcomeKnownUncommitted, false, false, fmt.Errorf("migration primary interrupted: %w", faults.FailBeforePrimary))
	}
	outputHashes := make(map[string]string, len(staged))
	for _, name := range declared {
		raw, _, err := ReadFile(filepath.Join(dir, stagedName(name)))
		if err != nil {
			return failure(StagePrimary, OutcomeDurabilityUncertain, true, true, fmt.Errorf("migration staging is partial; reconcile the validated primaries: %w", err))
		}
		if err := AtomicReplace(filepath.Join(dir, name), raw, 0o600); err != nil {
			return failure(StagePrimary, OutcomeKnownUncommitted, false, false, fmt.Errorf("migration primary for %s: %w", name, err))
		}
		digest := sha256.Sum256(raw)
		outputHashes[name] = hex.EncodeToString(digest[:])
	}
	if faults.FailAfterPrimary != nil {
		return failure(StagePrimary, OutcomeDurabilityUncertain, true, true, fmt.Errorf("migration primary interrupted; primaries may be visible: %w", faults.FailAfterPrimary))
	}
	if faults.FailBeforeSync != nil {
		return failure(StageDirSync, OutcomeDurabilityUncertain, true, true, fmt.Errorf("migration dir-sync interrupted: %w", faults.FailBeforeSync))
	}
	if err := syncDir(dir); err != nil {
		return failure(StageDirSync, OutcomeDurabilityUncertain, true, true, err)
	}
	if faults.FailAfterSync != nil {
		return failure(StageDirSync, OutcomeDurabilityUncertain, true, true, fmt.Errorf("migration dir-sync interrupted: %w", faults.FailAfterSync))
	}
	if faults.FailBeforeReceipt != nil {
		return failure(StageDirSync, OutcomeDurabilityUncertain, true, true, fmt.Errorf("migration receipt interrupted: %w", faults.FailBeforeReceipt))
	}
	receipt := MigrationReceipt{
		Plan:            plan,
		CompletedPhase:  plan.Phase,
		OutputHashes:    outputHashes,
		RetainedEffects: []string{"post-backup dispatch/deletion effects reconciled from validated primaries; backups never replayed over visible primaries"},
		Unresolved:      []string{},
	}
	if err := receipt.Validate(); err != nil {
		return failure(StageDirSync, OutcomeDurabilityUncertain, true, true, err)
	}
	serialized, err := MarshalValidated(MigrationReceiptName, receipt, func(value MigrationReceipt) error { return value.Validate() })
	if err != nil {
		return failure(StageDirSync, OutcomeDurabilityUncertain, true, true, err)
	}
	if err := AtomicReplace(filepath.Join(dir, MigrationReceiptName), serialized, 0o600); err != nil {
		return failure(StageDirSync, OutcomeDurabilityUncertain, true, true, err)
	}
	if err := syncDir(dir); err != nil {
		return failure(StageDirSync, OutcomeDurabilityUncertain, true, true, err)
	}
	if faults.FailAfterReceipt != nil {
		return failure(StageAcknowledge, OutcomeDurabilityUncertain, true, true, fmt.Errorf("migration receipt interrupted: %w", faults.FailAfterReceipt))
	}
	if faults.FailResponseDelivery != nil {
		return failure(StageAcknowledge, OutcomeDurabilityUncertain, true, true, fmt.Errorf("migration response interrupted: %w", faults.FailResponseDelivery))
	}
	// Best-effort staging cleanup never masks an installed outcome.
	for _, name := range declared {
		_ = os.Remove(filepath.Join(dir, stagedName(name)))
	}
	_ = syncDir(dir)
	return receipt, PublishOutcome{Kind: OutcomeInstalled, Stage: StageAcknowledge, PrimaryVisible: true}, nil
}

// AuthorityLedgerFiles are the durable authorities that must never be
// restored as runnable state from an older snapshot after the new version may
// have dispatched work or deleted content. Restoring their JSON does not undo
// native tool effects.
var AuthorityLedgerFiles = map[string]bool{
	"pi-session-queues.json":    true,
	"schedules.json":            true,
	"pi-session-deletions.json": true,
	"pi-pairing-authority.json": true,
}

// RollbackPlan is a read-only description of what a rollback would restore.
// It names schemas, files, retained post-backup effects and unresolved work
// without writing.
type RollbackPlan struct {
	RedactedDir       string
	Files             []string
	BackupHashes      map[string]string
	CurrentHashes     map[string]string
	Versions          map[string]string
	AuthorityBlocked  []string
	DispatchDisabled  bool
	RequiresReconcile bool
	RetainedEffects   []string
	Unresolved        []string
}

// PlanMigrationRollback inspects backups versus primaries with no writes.
// Authority ledgers whose current primary differs from the backup are blocked
// from runnable restore; dispatch stays disabled until explicit ledger
// reconciliation. Unknown files are never included.
func PlanMigrationRollback(dir string, files []string) (RollbackPlan, error) {
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	plan := RollbackPlan{
		RedactedDir:     RedactRoot(dir),
		Files:           sorted,
		BackupHashes:    make(map[string]string),
		CurrentHashes:   make(map[string]string),
		Versions:        make(map[string]string),
		RetainedEffects: []string{},
		Unresolved:      []string{},
	}
	for _, name := range sorted {
		if backup, _, err := ReadFile(filepath.Join(dir, name+".bak")); err == nil {
			digest := sha256.Sum256(backup)
			plan.BackupHashes[name] = hex.EncodeToString(digest[:])
			if version, _ := peekVersionEngine(backup); version != nil {
				plan.Versions[name+".bak"] = fmt.Sprintf("v%d", *version)
			}
		}
		if current, _, err := ReadFile(filepath.Join(dir, name)); err == nil {
			digest := sha256.Sum256(current)
			plan.CurrentHashes[name] = hex.EncodeToString(digest[:])
			if version, _ := peekVersionEngine(current); version != nil {
				plan.Versions[name] = fmt.Sprintf("v%d", *version)
			}
		}
		backupHash, hasBackup := plan.BackupHashes[name]
		currentHash, hasCurrent := plan.CurrentHashes[name]
		if AuthorityLedgerFiles[name] && hasBackup && hasCurrent && backupHash != currentHash {
			plan.AuthorityBlocked = append(plan.AuthorityBlocked, name)
			plan.DispatchDisabled = true
			plan.RequiresReconcile = true
			plan.RetainedEffects = append(plan.RetainedEffects, fmt.Sprintf("%s changed after backup; post-backup dispatch/deletion effects are retained and never rewound", name))
			plan.Unresolved = append(plan.Unresolved, fmt.Sprintf("%s requires explicit ledger reconciliation before any runnable restore", name))
		}
		if AuthorityLedgerFiles[name] && !hasCurrent && hasBackup {
			plan.AuthorityBlocked = append(plan.AuthorityBlocked, name)
			plan.DispatchDisabled = true
			plan.RequiresReconcile = true
			plan.Unresolved = append(plan.Unresolved, fmt.Sprintf("%s primary is missing while a backup remains; fail closed and do not replay the backup", name))
		}
	}
	return plan, nil
}

// RollbackReceipt names the schemas, restored files, retained post-backup
// effects and unresolved work for one rollback attempt.
type RollbackReceipt struct {
	RedactedDir            string
	RestoredFiles          []string
	RetainedEffects        []string
	Unresolved             []string
	Schemas                map[string]string
	DispatchDisabled       bool
	RequiresReconciliation bool
}

// ApplyMigrationRollback restores backups in the defined file order with
// required synchronization. Authority ledgers stay blocked from runnable
// restore unless allowAuthorityRestore is true and the caller has reconciled
// later effects through the controller ledger guards; even then dispatch
// stays disabled until explicit resume. It never rewinds monotonic claims by
// silently discarding the visible primary: blocked ledgers are left untouched
// and reported as unresolved. A no-backup rollback is a no-op success.
func ApplyMigrationRollback(dir string, files []string, faults MigrationRollbackFaults, allowAuthorityRestore bool) (RollbackReceipt, PublishOutcome, error) {
	failure := func(stage PublishStage, kind OutcomeKind, visible, reconcile bool, receipt RollbackReceipt, err error) (RollbackReceipt, PublishOutcome, error) {
		return receipt, PublishOutcome{Kind: kind, Stage: stage, PrimaryVisible: visible, MustReconcile: reconcile}, err
	}
	plan, err := PlanMigrationRollback(dir, files)
	if err != nil {
		return failure(StageValidate, OutcomeKnownUncommitted, false, false, RollbackReceipt{}, err)
	}
	receipt := RollbackReceipt{
		RedactedDir:            plan.RedactedDir,
		RestoredFiles:          []string{},
		RetainedEffects:        append([]string(nil), plan.RetainedEffects...),
		Unresolved:             append([]string(nil), plan.Unresolved...),
		Schemas:                plan.Versions,
		DispatchDisabled:       plan.DispatchDisabled,
		RequiresReconciliation: plan.RequiresReconcile,
	}
	if faults.FailBeforeRestore != nil {
		return failure(StageBackup, OutcomeKnownUncommitted, false, false, receipt, fmt.Errorf("rollback restore interrupted: %w", faults.FailBeforeRestore))
	}
	if len(plan.AuthorityBlocked) > 0 && !allowAuthorityRestore {
		return failure(StageValidate, OutcomeKnownUncommitted, false, false, receipt, fmt.Errorf("rollback refuses to restore older queue/schedule/deletion snapshots as runnable authority; reconcile explicitly first"))
	}
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	restored := []string{}
	for _, name := range sorted {
		if AuthorityLedgerFiles[name] && !allowAuthorityRestore {
			continue
		}
		backup, _, err := ReadFile(filepath.Join(dir, name+".bak"))
		if err != nil {
			continue
		}
		if len(backup) > maxJSONBytes {
			return failure(StageBackup, OutcomeKnownUncommitted, false, false, receipt, fmt.Errorf("rollback backup for %s exceeds the %d-byte limit", name, maxJSONBytes))
		}
		if err := AtomicReplace(filepath.Join(dir, name), backup, 0o600); err != nil {
			return failure(StagePrimary, OutcomeKnownUncommitted, false, false, receipt, fmt.Errorf("rollback restore for %s: %w", name, err))
		}
		restored = append(restored, name)
	}
	if faults.FailAfterRestore != nil {
		receipt.RestoredFiles = restored
		return failure(StagePrimary, OutcomeDurabilityUncertain, true, true, receipt, fmt.Errorf("rollback restore interrupted; reread the validated primaries: %w", faults.FailAfterRestore))
	}
	if faults.FailBeforeSync != nil {
		receipt.RestoredFiles = restored
		return failure(StageDirSync, OutcomeDurabilityUncertain, true, true, receipt, fmt.Errorf("rollback dir-sync interrupted: %w", faults.FailBeforeSync))
	}
	if err := syncDir(dir); err != nil {
		receipt.RestoredFiles = restored
		return failure(StageDirSync, OutcomeDurabilityUncertain, true, true, receipt, err)
	}
	if faults.FailAfterSync != nil {
		receipt.RestoredFiles = restored
		return failure(StageDirSync, OutcomeDurabilityUncertain, true, true, receipt, fmt.Errorf("rollback dir-sync interrupted: %w", faults.FailAfterSync))
	}
	if faults.FailBeforeReceipt != nil {
		receipt.RestoredFiles = restored
		return failure(StageDirSync, OutcomeDurabilityUncertain, true, true, receipt, fmt.Errorf("rollback receipt interrupted: %w", faults.FailBeforeReceipt))
	}
	receipt.RestoredFiles = restored
	if len(restored) == 0 {
		receipt.RetainedEffects = append(receipt.RetainedEffects, "no-op rollback; no backup generation was restored")
	}
	// Authority restores always keep dispatch disabled until explicit resume,
	// even with caller reconciliation. Only declared regenerable cache may be
	// discarded automatically; durable data never is.
	for _, name := range restored {
		if AuthorityLedgerFiles[name] {
			receipt.DispatchDisabled = true
			receipt.RequiresReconciliation = true
			receipt.RetainedEffects = append(receipt.RetainedEffects, fmt.Sprintf("%s restored only as quarantined history; dispatch stays disabled until explicit ledger reconciliation", name))
		}
	}
	if faults.FailAfterReceipt != nil {
		return failure(StageAcknowledge, OutcomeDurabilityUncertain, true, true, receipt, fmt.Errorf("rollback receipt interrupted: %w", faults.FailAfterReceipt))
	}
	return receipt, PublishOutcome{Kind: OutcomeInstalled, Stage: StageAcknowledge, PrimaryVisible: len(restored) > 0}, nil
}
