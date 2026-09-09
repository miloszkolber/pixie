package controller

import (
	"fmt"
	"os"
	"sort"

	"path/filepath"

	"github.com/miloszkolber/pixie/internal/persist"
)

// QueueMigrationHelperVersion versions these additive MIG-01 queue
// inspect/plan helpers. It never changes the pi-session-queues.json schema.
const QueueMigrationHelperVersion = 1

// Repeatable queue migration phases mirror the project-root journal:
// prepared inspects and stages, migrating publishes through checkpoints.
const (
	QueueMigrationPhasePrepared  = "prepared"
	QueueMigrationPhaseMigrating = "migrating"
)

// QueueLedgerSummary is a read-only dry-run view of pi-session-queues.json.
// It preserves mutation/delivery IDs, attempted/accepted/uncertain claims and
// paused work without restoring an older runnable queue over dispatched
// effects.
type QueueLedgerSummary struct {
	Present         bool
	Version         int
	SchemaSupported bool
	RecordCount     int
	FollowUpCount   int
	DispatchCount   int
	BlockedCount    int
	UncertainCount  int
	HandledCount    int
	Sessions        []string
}

// InspectQueueLedger reads the queue ledger with no writes and no dispatch.
// A missing or corrupt primary fails closed; the backup generation is never
// execution input and is never replayed automatically.
func InspectQueueLedger(store persist.Store) (QueueLedgerSummary, error) {
	name := filepath.Join(store.Dir, "pi-session-queues.json")
	raw, _, err := persist.ReadFile(name)
	if os.IsNotExist(err) {
		if _, _, backupErr := persist.ReadFile(name + ".bak"); os.IsNotExist(backupErr) {
			return QueueLedgerSummary{SchemaSupported: true}, nil
		}
		return QueueLedgerSummary{}, fmt.Errorf("session queue state is missing while a backup remains")
	}
	if err != nil {
		return QueueLedgerSummary{}, fmt.Errorf("session queue state is unreadable")
	}
	var value storedSessionQueues
	if persist.Decode(raw, &value, validateStoredQueues) != nil {
		return QueueLedgerSummary{}, fmt.Errorf("session queue state is unreadable")
	}
	summary := QueueLedgerSummary{Present: true, Version: value.Version, SchemaSupported: value.Version == 1, RecordCount: len(value.Records)}
	sessions := make(map[string]bool)
	for _, record := range value.Records {
		sessions[record.SessionID] = true
		summary.FollowUpCount += len(record.FollowUp)
		if record.Dispatch != nil {
			summary.DispatchCount++
			if record.Dispatch.Attempted {
				summary.UncertainCount++
			}
		}
		if record.Blocked != nil {
			summary.BlockedCount++
			summary.UncertainCount++
		}
		summary.HandledCount += len(record.Handled)
	}
	for session := range sessions {
		summary.Sessions = append(summary.Sessions, session)
	}
	sort.Strings(summary.Sessions)
	return summary, nil
}

// QueueAssociation is the minimal project/session key used for migration
// planning. Full queue payloads stay in the ledger; planning only moves the
// project key after paired host/native associations are verified.
type QueueAssociation struct {
	ProjectID string
	SessionID string
}

// QueueProjectMove proposes one project reassociation for a session queue.
type QueueProjectMove struct {
	SourceProjectID string
	TargetProjectID string
	SessionID       string
}

// PlanQueueProjectMigration is a pure, repeatable dry-run: re-running
// identical inputs yields identical outputs. It converts project-required
// queue keys only where verified[sourceProject\x00session] is true and
// otherwise retains the association instead of fabricating a hidden all-files
// project. Conversion must preserve revision, follow-ups, dispatch/blocked
// uncertainty and handled mutation/delivery IDs; callers verify payload
// equality except for the project key before applying. Unknown source files
// are never touched by this plan.
func PlanQueueProjectMigration(associations []QueueAssociation, moves []QueueProjectMove, verified map[string]bool) (planned []QueueAssociation, retained []QueueAssociation, conflicts []string, err error) {
	targets := make(map[string]string, len(moves))
	for _, move := range moves {
		if move.SourceProjectID == "" || move.TargetProjectID == "" || move.SessionID == "" {
			return nil, nil, nil, fmt.Errorf("invalid queue project move")
		}
		key := queueRecordKey(move.SourceProjectID, move.SessionID)
		if prior, exists := targets[key]; exists && prior != move.TargetProjectID {
			conflicts = append(conflicts, fmt.Sprintf("conflicting queue targets for session %s", move.SessionID))
			continue
		}
		targets[key] = move.TargetProjectID
	}
	if len(conflicts) > 0 {
		return nil, nil, conflicts, fmt.Errorf("session migration found conflicting queue targets")
	}
	plannedKeys := make(map[string]bool)
	for _, association := range associations {
		target, ok := targets[queueRecordKey(association.ProjectID, association.SessionID)]
		if !ok || target == association.ProjectID {
			planned = append(planned, association)
			plannedKeys[queueRecordKey(association.ProjectID, association.SessionID)] = true
			continue
		}
		if !verified[queueRecordKey(association.ProjectID, association.SessionID)] {
			retained = append(retained, association)
			planned = append(planned, association)
			plannedKeys[queueRecordKey(association.ProjectID, association.SessionID)] = true
			continue
		}
		converted := QueueAssociation{ProjectID: target, SessionID: association.SessionID}
		planned = append(planned, converted)
		key := queueRecordKey(converted.ProjectID, converted.SessionID)
		if plannedKeys[key] {
			return nil, nil, []string{fmt.Sprintf("session migration would create conflicting queues for %s", association.SessionID)}, fmt.Errorf("session migration found conflicting queues for %s", association.SessionID)
		}
		plannedKeys[key] = true
	}
	return planned, retained, nil, nil
}

// ValidateQueuePreservation confirms a staged conversion kept every follow-up,
// uncertain dispatch/block and handled mutation identity. It never touches
// storage.
func ValidateQueuePreservation(before, after QueueLedgerSummary) error {
	if !before.SchemaSupported || !after.SchemaSupported {
		return fmt.Errorf("queue preservation requires supported schemas on both sides")
	}
	if before.RecordCount != after.RecordCount || before.FollowUpCount != after.FollowUpCount || before.HandledCount != after.HandledCount || before.UncertainCount != after.UncertainCount {
		return fmt.Errorf("queue migration must preserve follow-ups, uncertainty and mutation identities")
	}
	return nil
}
