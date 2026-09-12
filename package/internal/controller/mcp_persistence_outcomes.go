package controller

import (
	"fmt"

	"github.com/miloszkolber/pixie/internal/persist"
)

// MCPersistenceOutcomeHelperVersion versions these additive FIX-13/X04
// schedule/queue/deletion outcome guards. It never changes any ledger schema.
const MCPersistenceOutcomeHelperVersion = 1

// LedgerKind names the durable ledger a publication decision guards.
type LedgerKind string

const (
	LedgerSchedules LedgerKind = "schedules"
	LedgerQueue     LedgerKind = "queue"
	LedgerDeletions LedgerKind = "deletions"
)

// LedgerPublishDecision tells callers whether an uncertain or failed
// publication may dispatch, must reconcile or must retain mutation identity.
// Only an installed outcome allows dependent mutations or unsafe effects.
type LedgerPublishDecision struct {
	Ledger             LedgerKind
	MayDispatch        bool
	MustReconcile      bool
	MustRetainMutation bool
	Reason             string
}

// DecideLedgerPublish maps a typed persist outcome onto ledger policy:
// known-uncommitted preserves the prior commit, uncertain blocks dispatch
// until the validated primary is reconciled, and installed allows progress.
func DecideLedgerPublish(ledger LedgerKind, outcome persist.PublishOutcome) LedgerPublishDecision {
	switch outcome.Kind {
	case persist.OutcomeInstalled:
		return LedgerPublishDecision{
			Ledger:        ledger,
			MayDispatch:   true,
			MustReconcile: false,
			Reason:        "installed and durable; dependent work remains gated by ledger rules",
		}
	case persist.OutcomeDurabilityUncertain:
		return LedgerPublishDecision{
			Ledger:             ledger,
			MayDispatch:        false,
			MustReconcile:      true,
			MustRetainMutation: true,
			Reason:             "primary may be visible with unconfirmed durability; retain mutation identity and reconcile the validated primary before dependent mutations or unsafe effects",
		}
	default:
		return LedgerPublishDecision{
			Ledger:        ledger,
			MayDispatch:   false,
			MustReconcile: false,
			Reason:        "known pre-publication failure; prior committed state preserved",
		}
	}
}

// DecideSchedulePublish guards schedule publication. An uncertain schedule
// claim must never dispatch a missed job; ambiguous restarts stay paused
// until the validated primary is reconciled.
func DecideSchedulePublish(outcome persist.PublishOutcome) LedgerPublishDecision {
	decision := DecideLedgerPublish(LedgerSchedules, outcome)
	if decision.MayDispatch {
		decision.Reason = "installed schedule ledger; dispatch stays gated by pause and interrupted-claim rules"
	} else {
		decision.Reason += "; schedules must not dispatch an uncertain claim"
	}
	return decision
}

// DecideQueuePublish guards queue publication. An uncertain queue claim keeps
// its mutation/delivery identity and must never restore an older runnable
// queue over dispatched effects.
func DecideQueuePublish(outcome persist.PublishOutcome) LedgerPublishDecision {
	decision := DecideLedgerPublish(LedgerQueue, outcome)
	if decision.MayDispatch {
		decision.Reason = "installed queue ledger; delivery follows mutation identity and pause rules"
	} else {
		decision.Reason += "; queues must not dispatch an uncertain claim or rewind over dispatched effects"
	}
	return decision
}

// DecideDeletionPublish guards deletion publication. Tombstones never
// resurrect through fallback and a delete is never replayed against a new
// endpoint, including after an uncertain publish.
func DecideDeletionPublish(outcome persist.PublishOutcome) LedgerPublishDecision {
	decision := DecideLedgerPublish(LedgerDeletions, outcome)
	if decision.MayDispatch {
		decision.Reason = "installed deletion journal; tombstone phases remain authoritative"
	} else {
		decision.Reason += "; tombstones never resurrect through fallback and deletes never replay against a new endpoint"
	}
	return decision
}

// ReconcileScheduleAfterPublish rereads the validated schedule primary after
// publication. An uncertain outcome stays unresolved until this read
// succeeds; it never replays the older backup generation.
func ReconcileScheduleAfterPublish(store persist.Store, outcome persist.PublishOutcome) (ScheduleLedgerSummary, error) {
	summary, err := InspectScheduleLedger(store)
	if err != nil {
		if outcome.Kind == persist.OutcomeDurabilityUncertain {
			return ScheduleLedgerSummary{}, fmt.Errorf("uncertain schedule publish remains unresolved: %w", err)
		}
		return ScheduleLedgerSummary{}, err
	}
	return summary, nil
}

// ReconcileQueueAfterPublish rereads the validated queue primary after
// publication with the same fail-closed, no-backup-replay rule.
func ReconcileQueueAfterPublish(store persist.Store, outcome persist.PublishOutcome) (QueueLedgerSummary, error) {
	summary, err := InspectQueueLedger(store)
	if err != nil {
		if outcome.Kind == persist.OutcomeDurabilityUncertain {
			return QueueLedgerSummary{}, fmt.Errorf("uncertain queue publish remains unresolved: %w", err)
		}
		return QueueLedgerSummary{}, err
	}
	return summary, nil
}

// ReconcileDeletionAfterPublish rereads the validated deletion primary after
// publication. A missing or corrupt primary keeps an uncertain outcome
// unresolved instead of resurrecting a tombstone from backup.
func ReconcileDeletionAfterPublish(store persist.Store, outcome persist.PublishOutcome) (DeletionLedgerSummary, error) {
	summary, err := InspectDeletionLedger(store)
	if err != nil {
		if outcome.Kind == persist.OutcomeDurabilityUncertain {
			return DeletionLedgerSummary{}, fmt.Errorf("uncertain deletion publish remains unresolved: %w", err)
		}
		return DeletionLedgerSummary{}, err
	}
	return summary, nil
}
