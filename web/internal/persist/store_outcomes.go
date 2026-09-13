package persist

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// PublishOutcomeHelperVersion versions these additive FIX-13/X04 typed
// publication helpers. It never changes the persisted JSON schemas.
const PublishOutcomeHelperVersion = 1

// OutcomeKind distinguishes a known pre-publication failure from an
// installed commit and a durability/outcome-uncertain result.
type OutcomeKind string

const (
	// OutcomeKnownUncommitted means the primary was not published. The prior
	// committed state is preserved and the caller must not reconcile.
	OutcomeKnownUncommitted OutcomeKind = "known-uncommitted"
	// OutcomeInstalled means the primary was published and directory
	// durability was confirmed.
	OutcomeInstalled OutcomeKind = "installed"
	// OutcomeDurabilityUncertain means the primary may already be visible
	// with unconfirmed durability. Callers must retain mutation identity
	// and reconcile the validated primary before dependent mutations or
	// unsafe effects. Never restore an old backup over the visible primary.
	OutcomeDurabilityUncertain OutcomeKind = "durability-uncertain"
)

// PublishStage names the declared commit boundary where publication stopped.
type PublishStage string

const (
	StageValidate    PublishStage = "validate"
	StageReserve     PublishStage = "reserve"
	StageBackup      PublishStage = "backup"
	StagePrimary     PublishStage = "primary"
	StageDirSync     PublishStage = "dir-sync"
	StageAcknowledge PublishStage = "acknowledge"
)

// PublishOutcome carries enough internal outcome information for callers to
// distinguish a known pre-publication failure from an installed-but-
// unconfirmed result.
type PublishOutcome struct {
	Kind           OutcomeKind
	Stage          PublishStage
	PrimaryVisible bool
	MustReconcile  bool
}

// MayDispatch reports whether the caller may trigger dependent mutations or
// unsafe effects. Only an installed outcome allows it.
func (outcome PublishOutcome) MayDispatch() bool {
	return outcome.Kind == OutcomeInstalled
}

// MustReconcileLedger reports whether the caller must reconcile the validated
// primary before accepting dependent mutations.
func (outcome PublishOutcome) MustReconcileLedger() bool {
	return outcome.MustReconcile
}

// PublishFaults injects deterministic storage failures at each
// publication/durability step for X04 coverage. A nil entry disables that
// fault. Faults before the primary rename stay known-uncommitted; faults at
// or after the primary rename report durability-uncertain.
type PublishFaults struct {
	FailReserve error
	FailBackup  error
	FailPrimary error
	FailDirSync error
	FailReply   error
}

// MarshalValidated performs the validate/reserve/stage-first check shared by
// publication: marshal, bound and re-validate before anything is published.
func MarshalValidated[T any](name string, value T, validate func(T) error) ([]byte, error) {
	serialized, err := json.MarshalIndent(value, "", "\t")
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", name, err)
	}
	serialized = append(serialized, '\n')
	if len(serialized) > maxJSONBytes {
		return nil, fmt.Errorf("persisted JSON exceeds the %d-byte limit", maxJSONBytes)
	}
	var checked T
	if err := Decode(serialized, &checked, validate); err != nil {
		return nil, fmt.Errorf("invalid persisted shape for %s: %w", name, err)
	}
	return serialized, nil
}

// WriteWithOutcome validates, reserves, stages and publishes one JSON file,
// classifying the result as known-uncommitted, installed or
// durability-uncertain. The primary file is the declared commit point:
// backup work happens before it, directory synchronization after it. A
// directory-sync or reply failure therefore returns an error with a
// durability-uncertain outcome while the new primary stays visible for
// reconciliation. It never overwrites a visible primary with an old backup.
func WriteWithOutcome[T any](s Store, name string, value T, validate func(T) error, faults PublishFaults) (PublishOutcome, error) {
	serialized, err := MarshalValidated(name, value, validate)
	if err != nil {
		return PublishOutcome{Kind: OutcomeKnownUncommitted, Stage: StageValidate}, err
	}
	target := filepath.Join(s.Dir, name)
	directoryPath := filepath.Dir(target)
	if faults.FailReserve != nil {
		return PublishOutcome{Kind: OutcomeKnownUncommitted, Stage: StageReserve}, fmt.Errorf("persist reserve for %s: %w", name, faults.FailReserve)
	}
	if err := os.MkdirAll(directoryPath, 0o700); err != nil {
		return PublishOutcome{Kind: OutcomeKnownUncommitted, Stage: StageReserve}, err
	}
	old, mode, readErr := ReadFile(target)
	var checked T
	hasValidPrior := readErr == nil && Decode(old, &checked, validate) == nil
	if hasValidPrior {
		if faults.FailBackup != nil {
			return PublishOutcome{Kind: OutcomeKnownUncommitted, Stage: StageBackup}, fmt.Errorf("persist backup for %s: %w", name, faults.FailBackup)
		}
		if err := AtomicReplace(target+".bak", old, mode); err != nil {
			return PublishOutcome{Kind: OutcomeKnownUncommitted, Stage: StageBackup}, err
		}
	} else {
		mode = 0o600
	}
	if faults.FailPrimary != nil {
		return PublishOutcome{Kind: OutcomeKnownUncommitted, Stage: StagePrimary}, fmt.Errorf("persist primary for %s: %w", name, faults.FailPrimary)
	}
	if err := AtomicReplace(target, serialized, mode); err != nil {
		return PublishOutcome{Kind: OutcomeKnownUncommitted, Stage: StagePrimary}, err
	}
	if faults.FailDirSync != nil {
		return PublishOutcome{Kind: OutcomeDurabilityUncertain, Stage: StageDirSync, PrimaryVisible: true, MustReconcile: true}, fmt.Errorf("persist dir-sync for %s: %w", name, faults.FailDirSync)
	}
	directory, err := os.Open(directoryPath)
	if err != nil {
		return PublishOutcome{Kind: OutcomeDurabilityUncertain, Stage: StageDirSync, PrimaryVisible: true, MustReconcile: true}, err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return PublishOutcome{Kind: OutcomeDurabilityUncertain, Stage: StageDirSync, PrimaryVisible: true, MustReconcile: true}, err
	}
	if faults.FailReply != nil {
		return PublishOutcome{Kind: OutcomeDurabilityUncertain, Stage: StageAcknowledge, PrimaryVisible: true, MustReconcile: true}, fmt.Errorf("persist reply for %s: %w", name, faults.FailReply)
	}
	return PublishOutcome{Kind: OutcomeInstalled, Stage: StageAcknowledge, PrimaryVisible: true}, nil
}

// ReconcilePrimary rereads and validates only the primary file after a
// durability-uncertain outcome. It never falls back to the backup generation
// and never writes: an unreadable or invalid primary keeps the outcome
// uncertain until durability/state is established.
func ReconcilePrimary[T any](s Store, name string, validate func(T) error) (T, error) {
	var zero T
	raw, _, err := ReadFile(filepath.Join(s.Dir, name))
	if err != nil {
		return zero, err
	}
	var value T
	if err := Decode(raw, &value, validate); err != nil {
		return zero, err
	}
	return value, nil
}
