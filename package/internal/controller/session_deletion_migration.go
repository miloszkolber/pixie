package controller

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/miloszkolber/pixie/internal/persist"
)

// DeletionMigrationHelperVersion versions these additive MIG-01 deletion
// inspect/plan helpers. It never changes the pi-session-deletions.json schema.
const DeletionMigrationHelperVersion = 1

// Repeatable deletion migration phases mirror the project-root journal:
// prepared inspects and stages, migrating publishes through checkpoints.
const (
	DeletionMigrationPhasePrepared  = "prepared"
	DeletionMigrationPhaseMigrating = "migrating"
)

// Deletion migration decisions. Tombstones are never discarded and a delete is
// never replayed against a new endpoint.
const (
	DeletionDecisionPreserve = "preserve"
	DeletionDecisionMigrate  = "migrate"
	DeletionDecisionBlocked  = "retain-recovery-blocked"
)

// DeletionTombstone is the exportable project/session/phase/binding view of
// one deletion record. Confirmed entries are tombstones that must survive
// every migration and rollback.
type DeletionTombstone struct {
	ProjectID string
	SessionID string
	Binding   string
	Phase     string
}

// DeletionLedgerSummary is a read-only dry-run view of
// pi-session-deletions.json with no writes and no destructive effects.
type DeletionLedgerSummary struct {
	Present         bool
	Version         int
	SchemaSupported bool
	Requested       int
	Confirmed       int
	Tombstones      []DeletionTombstone
}

// InspectDeletionLedger reads the deletion journal with no writes. The primary
// file is the only deletion authority: a missing or corrupt primary fails
// closed and never resurrects a completed delete from the older backup.
func InspectDeletionLedger(store persist.Store) (DeletionLedgerSummary, error) {
	name := filepath.Join(store.Dir, "pi-session-deletions.json")
	raw, _, err := persist.ReadFile(name)
	if os.IsNotExist(err) {
		if _, _, backupErr := persist.ReadFile(name + ".bak"); os.IsNotExist(backupErr) {
			return DeletionLedgerSummary{SchemaSupported: true}, nil
		}
		return DeletionLedgerSummary{}, fmt.Errorf("session deletion journal is missing while a backup remains")
	}
	if err != nil {
		return DeletionLedgerSummary{}, fmt.Errorf("session deletion journal is unreadable")
	}
	var value storedSessionDeletions
	if persist.Decode(raw, &value, validateStoredSessionDeletions) != nil {
		return DeletionLedgerSummary{}, fmt.Errorf("session deletion journal is unreadable")
	}
	summary := DeletionLedgerSummary{Present: true, Version: value.Version, SchemaSupported: value.Version == 1}
	for _, record := range value.Records {
		tombstone := DeletionTombstone{ProjectID: record.ProjectID, SessionID: record.SessionID, Binding: record.AgentBinding, Phase: record.Phase}
		summary.Tombstones = append(summary.Tombstones, tombstone)
		switch record.Phase {
		case deletionRequested:
			summary.Requested++
		case deletionConfirmed:
			summary.Confirmed++
		}
	}
	sort.Slice(summary.Tombstones, func(i, j int) bool {
		if summary.Tombstones[i].SessionID != summary.Tombstones[j].SessionID {
			return summary.Tombstones[i].SessionID < summary.Tombstones[j].SessionID
		}
		return summary.Tombstones[i].ProjectID < summary.Tombstones[j].ProjectID
	})
	return summary, nil
}

// DeletionProjectMove proposes one project reassociation for a deletion
// record. The binding itself never changes during a project move.
type DeletionProjectMove struct {
	SourceProjectID string
	TargetProjectID string
	SessionID       string
}

// DeletionMigrationDecision records the planned treatment for one deletion
// record. Unverifiable destructive recovery stays quarantined as
// retain-recovery-blocked with its tombstone preserved.
type DeletionMigrationDecision struct {
	ProjectID string
	SessionID string
	Phase     string
	Decision  string
	Reason    string
}

// PlanDeletionMigration is a pure, repeatable dry-run: re-running identical
// inputs yields identical decisions. It migrates a deletion project key only
// when both the paired host/native session association and the old agent
// binding are verified; otherwise it retains the tombstone as
// recovery-blocked. It never discards a tombstone and never authorizes
// replaying a delete against a new endpoint or boot credential. Unknown source
// files are never touched by this plan.
func PlanDeletionMigration(tombstones []DeletionTombstone, moves []DeletionProjectMove, verifiedAssociations map[string]bool, verifiedBindings map[string]bool) ([]DeletionMigrationDecision, error) {
	targets := make(map[string]string, len(moves))
	for _, move := range moves {
		if move.SourceProjectID == "" || move.TargetProjectID == "" || move.SessionID == "" {
			return nil, fmt.Errorf("invalid deletion project move")
		}
		key := queueRecordKey(move.SourceProjectID, move.SessionID)
		if prior, exists := targets[key]; exists && prior != move.TargetProjectID {
			return nil, fmt.Errorf("session migration found conflicting deletion targets for %s", move.SessionID)
		}
		targets[key] = move.TargetProjectID
	}
	decisions := make([]DeletionMigrationDecision, 0, len(tombstones))
	for _, tombstone := range tombstones {
		if tombstone.ProjectID == "" || tombstone.SessionID == "" {
			return nil, fmt.Errorf("invalid session deletion target")
		}
		target, ok := targets[queueRecordKey(tombstone.ProjectID, tombstone.SessionID)]
		if !ok || target == tombstone.ProjectID {
			decisions = append(decisions, DeletionMigrationDecision{ProjectID: tombstone.ProjectID, SessionID: tombstone.SessionID, Phase: tombstone.Phase, Decision: DeletionDecisionPreserve, Reason: "no project move; tombstone preserved"})
			continue
		}
		associationKey := queueRecordKey(tombstone.ProjectID, tombstone.SessionID)
		if !verifiedAssociations[associationKey] || !verifiedBindings[tombstone.SessionID] {
			decisions = append(decisions, DeletionMigrationDecision{ProjectID: tombstone.ProjectID, SessionID: tombstone.SessionID, Phase: tombstone.Phase, Decision: DeletionDecisionBlocked, Reason: "unverifiable destructive recovery quarantined as recovery-blocked; tombstone preserved and delete never replayed against a new endpoint"})
			continue
		}
		decisions = append(decisions, DeletionMigrationDecision{ProjectID: target, SessionID: tombstone.SessionID, Phase: tombstone.Phase, Decision: DeletionDecisionMigrate, Reason: "paired association and old binding verified; phase and tombstone preserved"})
	}
	return decisions, nil
}

// ValidateDeletionPreservation confirms a staged conversion kept every
// requested/confirmed phase and tombstone. It never touches storage.
func ValidateDeletionPreservation(before, after DeletionLedgerSummary) error {
	if !before.SchemaSupported || !after.SchemaSupported {
		return fmt.Errorf("deletion preservation requires supported schemas on both sides")
	}
	beforeBySession := make(map[string]DeletionTombstone, len(before.Tombstones))
	for _, tombstone := range before.Tombstones {
		beforeBySession[tombstone.SessionID] = tombstone
	}
	afterBySession := make(map[string]DeletionTombstone, len(after.Tombstones))
	for _, tombstone := range after.Tombstones {
		afterBySession[tombstone.SessionID] = tombstone
	}
	for session, want := range beforeBySession {
		got, ok := afterBySession[session]
		if !ok || got.Phase != want.Phase {
			return fmt.Errorf("deletion migration must preserve requested/confirmed phases and tombstones")
		}
	}
	return nil
}
