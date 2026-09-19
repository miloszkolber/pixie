package persist

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MigrationApplyVersion versions the additive MIG-01 staged-conversion engine.
// It never changes any ledger schema; it only orders checkpoints, backups,
// staging, publication, synchronization and receipts.
const MigrationApplyVersion = 1

// MigrationReceiptName is the durable receipt file written last. Losing it
// leaves primaries visible but unacknowledged, which stays
// durability-uncertain until the validated primaries are reconciled.
const MigrationReceiptName = "migration-receipt.json"

// managedMigrationLedgerFiles is the closed set of controller-ledger
// primaries migration may read, stage, back up, publish, or restore. Migration
// receives file names from plans and maps, so callers never extend this set.
var managedMigrationLedgerFiles = map[string]struct{}{
	"config.json":                 {},
	"projects.json":               {},
	"pi-project-sessions.json":    {},
	"pi-session-queues.json":      {},
	"schedules.json":              {},
	"pi-session-deletions.json":   {},
	"pi-pairing-authority.json":   {},
	"project-root-migration.json": {},
	"mcp-modules.json":            {},
}

// validateMigrationFile rejects user-controlled paths before migration joins
// them to its data directory. Managed ledgers are flat, exact file names.
func validateMigrationFile(name string) error {
	if name == "" {
		return fmt.Errorf("migration file name is required")
	}
	if filepath.IsAbs(name) {
		return fmt.Errorf("migration file name must be relative")
	}
	if name == "." || name == ".." || filepath.Base(name) != name || strings.Contains(name, "\\") {
		return fmt.Errorf("migration file name must be a flat file name")
	}
	if _, ok := managedMigrationLedgerFiles[name]; !ok {
		return fmt.Errorf("migration file is not a managed ledger: %s", name)
	}
	return nil
}

func validateMigrationFiles(files []string) error {
	for _, name := range files {
		if err := validateMigrationFile(name); err != nil {
			return err
		}
	}
	return nil
}

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
//
// Failure-mode matrix for the injected storage faults (all pre-rename, so all
// known-uncommitted; the rename is the only visibility point):
//
//	injection                              outcome stage   outcome kind
//	BackupReplace.FailCreateTemp           backup          known-uncommitted
//	BackupReplace.FailWrite (ENOSPC)       backup          known-uncommitted
//	BackupReplace.FailFileSync             backup          known-uncommitted
//	BackupReplace.FailRename               backup          known-uncommitted
//	StagingReplace.FailCreateTemp          backup          known-uncommitted
//	StagingReplace.FailWrite (ENOSPC)      backup          known-uncommitted
//	StagingReplace.ShortWrite              backup          known-uncommitted
//	StagingReplace.FailFileSync            backup          known-uncommitted
//	StagingReplace.FailRename              backup          known-uncommitted
//	PrimaryReplace.FailCreateTemp          primary         known-uncommitted
//	PrimaryReplace.FailWrite (ENOSPC)      primary         known-uncommitted
//	PrimaryReplace.ShortWrite              primary         known-uncommitted
//	PrimaryReplace.FailFileSync            primary         known-uncommitted
//	PrimaryReplace.FailRename              primary         known-uncommitted
//	FailPrimaryAt/ FailPrimary             primary         durability-uncertain when a prior primary published
//	FailAfterPrimary / FailBeforeSync      primary/dir-sync durability-uncertain
//	FailAfterSync / FailBeforeReceipt      dir-sync         durability-uncertain
//	FailAfterReceipt / FailResponseDelivery acknowledge      durability-uncertain
//
// Faults before the primary rename leave prior primaries intact and are
// known-uncommitted. This is injected-failure behavior, not measured
// power-loss atomicity across multiple renames.
type MigrationFaults struct {
	FailBeforeInventory error
	FailAfterInventory  error
	FailBeforeBackup    error
	FailAfterBackup     error
	FailBeforeStaging   error
	FailAfterStaging    error
	FailBeforePrimary   error
	// FailPrimaryAt and FailPrimary inject a failed primary rename before the
	// one-based file position. A failure after an earlier primary was published
	// is durability-uncertain, not a failed-no-change result.
	FailPrimaryAt        int
	FailPrimary          error
	FailAfterPrimary     error
	FailBeforeSync       error
	FailAfterSync        error
	FailBeforeReceipt    error
	FailAfterReceipt     error
	FailResponseDelivery error

	// BackupReplace, StagingReplace and PrimaryReplace inject filesystem
	// failures into the three write sequences. See the matrix above.
	// PrimaryReplaceAt limits PrimaryReplace to one one-based publication
	// position; zero applies it to every primary. It lets a test fail a later
	// primary after an earlier one published.
	BackupReplace    ReplaceFaults
	StagingReplace   ReplaceFaults
	PrimaryReplace   ReplaceFaults
	PrimaryReplaceAt int
}

// MigrationRollbackFaults injects deterministic interruption into rollback.
// Rollback never rewinds ledger authority: an interrupted restore stays
// unresolved until the validated primaries are reread. RestoreReplace injects
// a pre-rename storage failure; the first restore failure stays
// known-uncommitted and a later one is durability-uncertain.
type MigrationRollbackFaults struct {
	FailBeforeRestore error
	FailAfterRestore  error
	FailBeforeSync    error
	FailAfterSync     error
	FailBeforeReceipt error
	FailAfterReceipt  error
	RestoreReplace    ReplaceFaults
	RestoreReplaceAt  int
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
	if err := validateMigrationFiles(files); err != nil {
		return MigrationInspection{}, err
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
	return backupMigrationInputsWithFaults(dir, files, ReplaceFaults{})
}

// backupMigrationInputsWithFaults is BackupMigrationInputs with injected
// pre-rename storage failures. It reads and hashes the whole batch before the
// first backup write, so a read failure leaves no backup generation. A backup
// write failure is pre-rename and therefore cannot change a primary.
func backupMigrationInputsWithFaults(dir string, files []string, faults ReplaceFaults) ([]BackupRecord, error) {
	if err := validateMigrationFiles(files); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	type backupInput struct {
		name string
		raw  []byte
	}
	inputs := make([]backupInput, 0, len(files))
	for _, name := range files {
		raw, _, err := ReadFile(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read migration primary %s: %w", name, err)
		}
		inputs = append(inputs, backupInput{name: name, raw: raw})
	}
	var records []BackupRecord
	for _, input := range inputs {
		name, raw := input.name, input.raw
		digest := sha256.Sum256(raw)
		record := BackupRecord{File: name, Hash: hex.EncodeToString(digest[:]), Size: int64(len(raw))}
		if err := atomicReplaceWithFaults(filepath.Join(dir, name+".bak"), raw, 0o600, faults); err != nil {
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

// validateConvertedFiles checks the entire declared staging batch before the
// first staging or backup write. A malformed later member must not leave an
// earlier staged file or backup that looks like a prepared conversion.
func validateConvertedFiles(staged map[string][]byte, declared []string) ([]string, error) {
	if err := validateMigrationFiles(declared); err != nil {
		return nil, err
	}
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
		if err := validateMigrationFile(name); err != nil {
			return nil, err
		}
		if !allowed[name] {
			return nil, fmt.Errorf("refusing to stage undeclared file %s", name)
		}
		data := staged[name]
		if len(data) > maxJSONBytes {
			return nil, fmt.Errorf("staged %s exceeds the %d-byte limit", name, maxJSONBytes)
		}
		if filepath.Ext(name) == ".json" && !json.Valid(data) {
			return nil, fmt.Errorf("staged %s is not valid JSON", name)
		}
	}
	return names, nil
}

// StageConvertedFiles validates converted bytes and stages them on the
// destination filesystem with restrictive permissions. It checks the shared
// 16 MiB bound, requires valid JSON for .json primaries, and refuses to stage
// unknown paths outside the declared file set. Staging never publishes.
func StageConvertedFiles(dir string, staged map[string][]byte, declared []string) error {
	return stageConvertedFilesWithFaults(dir, staged, declared, ReplaceFaults{})
}

// stageConvertedFilesWithFaults is StageConvertedFiles with injected
// pre-rename storage failures. The whole declared batch is validated before
// the first staged write, and staging never publishes a primary.
func stageConvertedFilesWithFaults(dir string, staged map[string][]byte, declared []string, faults ReplaceFaults) error {
	names, err := validateConvertedFiles(staged, declared)
	if err != nil {
		return err
	}
	for _, name := range names {
		data := staged[name]
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700); err != nil {
			return err
		}
		if err := atomicReplaceWithFaults(filepath.Join(dir, stagedName(name)), data, 0o600, faults); err != nil {
			return fmt.Errorf("stage %s: %w", name, err)
		}
	}
	return nil
}

// currentInputHashes fingerprints the declared primaries without writing. A
// missing primary is absent input only when no backup entry exists. A retained
// or unreadable backup means a prior publication may be unresolved, so it
// blocks migration before backup, staging, or primary publication.
func currentInputHashes(dir string, files []string) (map[string]string, error) {
	if err := validateMigrationFiles(files); err != nil {
		return nil, err
	}
	hashes := make(map[string]string, len(files))
	for _, name := range files {
		raw, _, err := ReadFile(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			backupPath := filepath.Join(dir, name+".bak")
			_, _, backupErr := ReadFile(backupPath)
			if backupErr == nil {
				return nil, fmt.Errorf("migration recovery conflict: primary %s is missing while a backup remains; re-audit validated state before migration", name)
			}
			if !errors.Is(backupErr, os.ErrNotExist) {
				return nil, fmt.Errorf("migration recovery conflict: primary %s is missing while backup is unreadable; re-audit validated state before migration: %w", name, backupErr)
			}
			// ReadFile follows symlinks, so distinguish an absent backup from a
			// retained dangling link. Either can name unresolved publication.
			if _, lstatErr := os.Lstat(backupPath); lstatErr == nil {
				return nil, fmt.Errorf("migration recovery conflict: primary %s is missing while backup is unreadable; re-audit validated state before migration", name)
			} else if !errors.Is(lstatErr, os.ErrNotExist) {
				return nil, fmt.Errorf("migration recovery conflict: primary %s is missing while backup is unreadable; re-audit validated state before migration: %w", name, lstatErr)
			}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read migration primary %s: %w", name, err)
		}
		digest := sha256.Sum256(raw)
		hashes[name] = hex.EncodeToString(digest[:])
	}
	return hashes, nil
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
	// publishedFiles accumulates the primaries this run made visible so a
	// partial multi-file publication reports exactly which authority/dependent
	// files callers must reconcile.
	var publishedFiles []string
	failure := func(stage PublishStage, kind OutcomeKind, visible, reconcile bool, err error) (MigrationReceipt, PublishOutcome, error) {
		return MigrationReceipt{}, PublishOutcome{Kind: kind, Stage: stage, PrimaryVisible: visible, MustReconcile: reconcile}, err
	}
	failureVisible := func(stage PublishStage, kind OutcomeKind, err error) (MigrationReceipt, PublishOutcome, error) {
		return MigrationReceipt{}, PublishOutcome{Kind: kind, Stage: stage, PrimaryVisible: true, MustReconcile: true, Published: append([]string(nil), publishedFiles...)}, err
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
	if _, err := validateConvertedFiles(staged, declared); err != nil {
		return failure(StageValidate, OutcomeKnownUncommitted, false, false, err)
	}
	if faults.FailBeforeInventory != nil {
		return failure(StageValidate, OutcomeKnownUncommitted, false, false, fmt.Errorf("migration inventory interrupted: %w", faults.FailBeforeInventory))
	}
	current, err := currentInputHashes(dir, declared)
	if err != nil {
		return failure(StageValidate, OutcomeKnownUncommitted, false, false, err)
	}
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
	if _, err := backupMigrationInputsWithFaults(dir, declared, faults.BackupReplace); err != nil {
		return failure(StageBackup, OutcomeKnownUncommitted, false, false, err)
	}
	if faults.FailAfterBackup != nil {
		return failure(StageBackup, OutcomeKnownUncommitted, false, false, fmt.Errorf("migration backup interrupted: %w", faults.FailAfterBackup))
	}
	if faults.FailBeforeStaging != nil {
		return failure(StageBackup, OutcomeKnownUncommitted, false, false, fmt.Errorf("migration staging interrupted: %w", faults.FailBeforeStaging))
	}
	if err := stageConvertedFilesWithFaults(dir, staged, declared, faults.StagingReplace); err != nil {
		return failure(StageBackup, OutcomeKnownUncommitted, false, false, err)
	}
	if faults.FailAfterStaging != nil {
		return failure(StageBackup, OutcomeKnownUncommitted, false, false, fmt.Errorf("migration staging interrupted: %w", faults.FailAfterStaging))
	}
	if faults.FailBeforePrimary != nil {
		return failure(StagePrimary, OutcomeKnownUncommitted, false, false, fmt.Errorf("migration primary interrupted: %w", faults.FailBeforePrimary))
	}
	outputHashes := make(map[string]string, len(staged))
	primaryFailure := func(err error) (MigrationReceipt, PublishOutcome, error) {
		if len(publishedFiles) > 0 {
			return failureVisible(StagePrimary, OutcomeDurabilityUncertain, err)
		}
		return failure(StagePrimary, OutcomeKnownUncommitted, false, false, err)
	}
	for index, name := range declared {
		raw, _, err := ReadFile(filepath.Join(dir, stagedName(name)))
		if err != nil {
			return primaryFailure(fmt.Errorf("migration staging is partial; reconcile the validated primaries: %w", err))
		}
		if faults.FailPrimary != nil && faults.FailPrimaryAt == index+1 {
			return primaryFailure(fmt.Errorf("migration primary for %s: %w", name, faults.FailPrimary))
		}
		primaryReplace := faults.PrimaryReplace
		if faults.PrimaryReplaceAt != 0 && faults.PrimaryReplaceAt != index+1 {
			primaryReplace = ReplaceFaults{}
		}
		if err := atomicReplaceWithFaults(filepath.Join(dir, name), raw, 0o600, primaryReplace); err != nil {
			return primaryFailure(fmt.Errorf("migration primary for %s: %w", name, err))
		}
		publishedFiles = append(publishedFiles, name)
		digest := sha256.Sum256(raw)
		outputHashes[name] = hex.EncodeToString(digest[:])
	}
	if faults.FailAfterPrimary != nil {
		return failureVisible(StagePrimary, OutcomeDurabilityUncertain, fmt.Errorf("migration primary interrupted; primaries may be visible: %w", faults.FailAfterPrimary))
	}
	if faults.FailBeforeSync != nil {
		return failureVisible(StageDirSync, OutcomeDurabilityUncertain, fmt.Errorf("migration dir-sync interrupted: %w", faults.FailBeforeSync))
	}
	if err := syncDir(dir); err != nil {
		return failureVisible(StageDirSync, OutcomeDurabilityUncertain, err)
	}
	if faults.FailAfterSync != nil {
		return failureVisible(StageDirSync, OutcomeDurabilityUncertain, fmt.Errorf("migration dir-sync interrupted: %w", faults.FailAfterSync))
	}
	if faults.FailBeforeReceipt != nil {
		return failureVisible(StageDirSync, OutcomeDurabilityUncertain, fmt.Errorf("migration receipt interrupted: %w", faults.FailBeforeReceipt))
	}
	receipt := MigrationReceipt{
		Plan:            plan,
		CompletedPhase:  plan.Phase,
		OutputHashes:    outputHashes,
		RetainedEffects: []string{"post-backup dispatch/deletion effects reconciled from validated primaries; backups never replayed over visible primaries"},
		Unresolved:      []string{},
	}
	if err := receipt.Validate(); err != nil {
		return failureVisible(StageDirSync, OutcomeDurabilityUncertain, err)
	}
	serialized, err := MarshalValidated(MigrationReceiptName, receipt, func(value MigrationReceipt) error { return value.Validate() })
	if err != nil {
		return failureVisible(StageDirSync, OutcomeDurabilityUncertain, err)
	}
	if err := AtomicReplace(filepath.Join(dir, MigrationReceiptName), serialized, 0o600); err != nil {
		return failureVisible(StageDirSync, OutcomeDurabilityUncertain, err)
	}
	if err := syncDir(dir); err != nil {
		return failureVisible(StageDirSync, OutcomeDurabilityUncertain, err)
	}
	if faults.FailAfterReceipt != nil {
		return failureVisible(StageAcknowledge, OutcomeDurabilityUncertain, fmt.Errorf("migration receipt interrupted: %w", faults.FailAfterReceipt))
	}
	if faults.FailResponseDelivery != nil {
		return failureVisible(StageAcknowledge, OutcomeDurabilityUncertain, fmt.Errorf("migration response interrupted: %w", faults.FailResponseDelivery))
	}
	// Best-effort staging cleanup never masks an installed outcome.
	for _, name := range declared {
		_ = os.Remove(filepath.Join(dir, stagedName(name)))
	}
	_ = syncDir(dir)
	return receipt, PublishOutcome{Kind: OutcomeInstalled, Stage: StageAcknowledge, PrimaryVisible: true, Published: append([]string(nil), publishedFiles...)}, nil
}

// ReconcileMigrationPrimaries rereads and validates every declared primary
// after a durability-uncertain multi-file publication. It reads only the
// primaries named by the caller, never a backup or a staged file, and performs
// no writes. A missing, unreadable or invalid primary keeps reconciliation
// unresolved so recovery cannot prefer a stale generation. Callers should pass
// PublishOutcome.Published so only primaries that may have changed are read.
func ReconcileMigrationPrimaries(dir string, files []string) (map[string][]byte, error) {
	if err := validateMigrationFiles(files); err != nil {
		return nil, err
	}
	validated := make(map[string][]byte, len(files))
	for _, name := range files {
		raw, _, err := ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("reconcile migration primary %s: %w", name, err)
		}
		if filepath.Ext(name) == ".json" && !json.Valid(raw) {
			return nil, fmt.Errorf("reconcile migration primary %s is not valid JSON", name)
		}
		verified := make([]byte, len(raw))
		copy(verified, raw)
		validated[name] = verified
	}
	return validated, nil
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
	if err := validateMigrationFiles(files); err != nil {
		return RollbackPlan{}, err
	}
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

// ApplyMigrationRollback restores non-authority backups in the defined file
// order with required synchronization. Authority ledger backups are never
// restored: a Boolean caller assertion cannot prove that a later schedule run
// was not dispatched or a later deletion was not confirmed. The final Boolean
// is retained for source compatibility and is intentionally ignored. It never
// rewinds monotonic claims by silently discarding the visible primary: blocked
// ledgers are left untouched and reported as unresolved. A no-backup rollback
// is a no-op success. RestoreReplaceAt limits RestoreReplace to one one-based
// restore position; zero applies it to every restore.
// Failure-mode matrix for the restore storage faults:
//
//	RestoreReplace.FailCreateTemp/FailWrite/FailFileSync/FailRename
//	  first selected restore  -> known-uncommitted (no primary replaced)
//	  later selected restore  -> durability-uncertain with Published naming
//	                             the already-restored primaries
//	FailAfterRestore/FailBeforeSync/FailAfterSync/FailBeforeReceipt/FailAfterReceipt
//	  -> durability-uncertain once at least one primary was restored
//	blocked authority ledger -> known-uncommitted, primaries untouched
func ApplyMigrationRollback(dir string, files []string, faults MigrationRollbackFaults, _ bool) (RollbackReceipt, PublishOutcome, error) {
	failure := func(stage PublishStage, kind OutcomeKind, visible, reconcile bool, receipt RollbackReceipt, err error) (RollbackReceipt, PublishOutcome, error) {
		return receipt, PublishOutcome{Kind: kind, Stage: stage, PrimaryVisible: visible, MustReconcile: reconcile, Published: append([]string(nil), receipt.RestoredFiles...)}, err
	}
	if err := validateMigrationFiles(files); err != nil {
		return failure(StageValidate, OutcomeKnownUncommitted, false, false, RollbackReceipt{}, err)
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
	if len(plan.AuthorityBlocked) > 0 {
		return failure(StageValidate, OutcomeKnownUncommitted, false, false, receipt, fmt.Errorf("rollback refuses to restore older queue/schedule/deletion snapshots; retain the current authority and reconcile it explicitly"))
	}
	sorted := append([]string(nil), plan.Files...)
	// Validate the whole selected backup batch before the first restore so a
	// malformed later member cannot leave a partial runnable restore. An
	// oversized or non-JSON backup is rejected known-uncommitted, before any
	// primary is replaced, and never replayed as authority.
	type restoreInput struct {
		name string
		raw  []byte
	}
	restores := make([]restoreInput, 0, len(sorted))
	for _, name := range sorted {
		if AuthorityLedgerFiles[name] {
			continue
		}
		backup, _, err := ReadFile(filepath.Join(dir, name+".bak"))
		if err != nil {
			continue
		}
		if len(backup) > maxJSONBytes {
			return failure(StageBackup, OutcomeKnownUncommitted, false, false, receipt, fmt.Errorf("rollback backup for %s exceeds the %d-byte limit", name, maxJSONBytes))
		}
		if filepath.Ext(name) == ".json" && !json.Valid(backup) {
			return failure(StageBackup, OutcomeKnownUncommitted, false, false, receipt, fmt.Errorf("rollback backup for %s is not valid JSON; refuse to restore a stale or corrupt generation", name))
		}
		restores = append(restores, restoreInput{name: name, raw: backup})
	}
	restored := []string{}
	for index, input := range restores {
		restoreReplace := faults.RestoreReplace
		if faults.RestoreReplaceAt != 0 && faults.RestoreReplaceAt != index+1 {
			restoreReplace = ReplaceFaults{}
		}
		if err := atomicReplaceWithFaults(filepath.Join(dir, input.name), input.raw, 0o600, restoreReplace); err != nil {
			if len(restored) > 0 {
				receipt.RestoredFiles = append([]string(nil), restored...)
				return failure(StagePrimary, OutcomeDurabilityUncertain, true, true, receipt, fmt.Errorf("rollback restore for %s: %w", input.name, err))
			}
			return failure(StagePrimary, OutcomeKnownUncommitted, false, false, receipt, fmt.Errorf("rollback restore for %s: %w", input.name, err))
		}
		restored = append(restored, input.name)
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
	if faults.FailAfterReceipt != nil {
		return failure(StageAcknowledge, OutcomeDurabilityUncertain, true, true, receipt, fmt.Errorf("rollback receipt interrupted: %w", faults.FailAfterReceipt))
	}
	return receipt, PublishOutcome{Kind: OutcomeInstalled, Stage: StageAcknowledge, PrimaryVisible: len(restored) > 0}, nil
}
