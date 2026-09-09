package controller

import (
	"fmt"
	"sort"

	"github.com/miloszkolber/pixie/internal/persist"
)

// MigrationApplyHelperVersion versions these additive MIG-01 topology and
// schema-aware rollback helpers. It never changes any ledger schema.
const MigrationApplyHelperVersion = 1

// MigrationTopology names a supported deployment composition. Docker runs
// controller-only and never starts local Pi; the combined host embeds
// engine/controller/UI in one executable. Both share implementation,
// authority and state contracts, not duplicated supervisors.
type MigrationTopology string

const (
	// TopologyDockerPlusAssistant is the Docker controller plus host assistant
	// composition with a private external credential.
	TopologyDockerPlusAssistant MigrationTopology = "docker-plus-assistant"
	// TopologyCombinedHost is the single full-host pixie.service composition
	// with its private internal transport credential.
	TopologyCombinedHost MigrationTopology = "combined-host"
)

// ValidateMigrationTopology rejects unknown topologies without touching storage.
func ValidateMigrationTopology(topology MigrationTopology) error {
	if topology != TopologyDockerPlusAssistant && topology != TopologyCombinedHost {
		return fmt.Errorf("unknown migration topology")
	}
	return nil
}

// ValidateTopologySwitch allows only the two defined switching directions.
// It never enables both units, copies all HOME, stops a different
// installation, or moves native Pi state.
func ValidateTopologySwitch(from, to MigrationTopology) error {
	if err := ValidateMigrationTopology(from); err != nil {
		return err
	}
	if err := ValidateMigrationTopology(to); err != nil {
		return err
	}
	if from == to {
		return fmt.Errorf("topology switch requires a different target topology")
	}
	return nil
}

// TopologySwitchPlan is a dry-run switching description with no writes,
// package loading, extension execution, model calls or native configuration
// changes. Roots are always redacted; secrets never enter diagnostics.
type TopologySwitchPlan struct {
	From                     MigrationTopology
	To                       MigrationTopology
	RedactedSource           string
	RedactedTarget           string
	PathMappings             []string
	DispatchPausedRequired   bool
	OwnershipReleaseRequired bool
	DispatchBlocked          bool
	Warnings                 []string
}

// PlanTopologySwitch derives a repeatable dry-run plan for either switching
// direction. Re-running identical inputs yields identical output. It performs
// no writes and never starts local Pi in Docker controller-only mode.
func PlanTopologySwitch(from, to MigrationTopology, sourceDir, targetDir string, pathMappings []string) (TopologySwitchPlan, error) {
	if err := ValidateTopologySwitch(from, to); err != nil {
		return TopologySwitchPlan{}, err
	}
	if sourceDir == "" || targetDir == "" {
		return TopologySwitchPlan{}, fmt.Errorf("topology switch requires explicit source and target directories")
	}
	redactedSource, redactedTarget := persist.RedactedMigrationRoots(sourceDir, targetDir)
	mappings := append([]string(nil), pathMappings...)
	sort.Strings(mappings)
	plan := TopologySwitchPlan{
		From:                     from,
		To:                       to,
		RedactedSource:           redactedSource,
		RedactedTarget:           targetDirRedacted(redactedTarget),
		PathMappings:             mappings,
		DispatchPausedRequired:   true,
		OwnershipReleaseRequired: true,
		DispatchBlocked:          true,
	}
	if len(mappings) == 0 {
		plan.Warnings = append(plan.Warnings, "no explicit container-volume to host path mapping; refusing to equate /var/lib/pixie with an XDG directory by product name")
	}
	if from == TopologyDockerPlusAssistant {
		plan.Warnings = append(plan.Warnings, "pause dispatch, stop the Docker controller and the managed host assistant, verify ownership release, then start pixie.service with its private internal credential")
	} else {
		plan.Warnings = append(plan.Warnings, "pause dispatch and stop pixie.service, install/start the host assistant with its private external credential, then start the digest-pinned Docker controller in controller-only mode")
	}
	plan.Warnings = append(plan.Warnings, "resume schedules/outbox explicitly after migration, never as an accidental consequence of startup")
	return plan, nil
}

func targetDirRedacted(redacted string) string { return redacted }

// ValidateTopologySwitchPreconditions confirms stopped admission and verified
// release of the old managed owner before any copy. It never terminates an
// independent TUI and never assumes a lockfile alone proves release.
func ValidateTopologySwitchPreconditions(dispatchPaused, ownershipReleased bool) error {
	if !dispatchPaused {
		return fmt.Errorf("topology switch requires paused schedule/outbox admission with settled or explicitly interrupted work")
	}
	if !ownershipReleased {
		return fmt.Errorf("topology switch requires verified release of the old managed owner")
	}
	return nil
}

// ArchiveAssociation is the minimal durable grouping view used for migration
// validation. Full transcripts stay native; planning only preserves
// archive/parent/catalog links without synthesizing missing transcripts.
type ArchiveAssociation struct {
	ProjectID       string
	SessionID       string
	ParentSessionID string
	CWD             string
	Title           string
	Archived        bool
}

// ValidateArchivePreservation confirms a staged conversion kept every archive
// flag, parent link and catalog association without inventing transcripts or
// branches. Duplicate native IDs block ambiguous actions. It never touches
// storage.
func ValidateArchivePreservation(before, after []ArchiveAssociation) error {
	beforeBySession := make(map[string]ArchiveAssociation, len(before))
	for _, record := range before {
		if record.SessionID == "" {
			return fmt.Errorf("archive preservation requires session identity")
		}
		if prior, exists := beforeBySession[record.SessionID]; exists && prior != record {
			return fmt.Errorf("duplicate native session identity blocks ambiguous archive actions")
		}
		beforeBySession[record.SessionID] = record
	}
	afterBySession := make(map[string]ArchiveAssociation, len(after))
	for _, record := range after {
		if record.SessionID == "" {
			return fmt.Errorf("archive preservation requires session identity")
		}
		if prior, exists := afterBySession[record.SessionID]; exists && prior != record {
			return fmt.Errorf("duplicate native session identity blocks ambiguous archive actions")
		}
		afterBySession[record.SessionID] = record
	}
	if len(beforeBySession) != len(afterBySession) {
		return fmt.Errorf("archive migration must preserve every catalog association without synthesizing transcripts")
	}
	for session, want := range beforeBySession {
		got, ok := afterBySession[session]
		if !ok {
			return fmt.Errorf("archive migration must preserve every catalog association without synthesizing transcripts")
		}
		if got.Archived != want.Archived || got.ParentSessionID != want.ParentSessionID || got.ProjectID != want.ProjectID || got.CWD != want.CWD {
			return fmt.Errorf("archive migration must preserve archive, parent, project and cwd links without inventing a new branch")
		}
	}
	return nil
}

// ConvertLegacyArchiveMetadata preserves old assistant archive/parent/catalog
// associations as explicit grouping. Empty project sections stay ungrouped;
// no hidden all-files project is fabricated. Parent links whose source is
// missing are preserved dangling without inventing a new branch. Unknown
// entries are carried verbatim; only exact duplicates fold.
func ConvertLegacyArchiveMetadata(legacy []ArchiveAssociation) ([]ArchiveAssociation, []string, error) {
	converted := make([]ArchiveAssociation, 0, len(legacy))
	seen := make(map[string]ArchiveAssociation, len(legacy))
	var warnings []string
	for _, record := range legacy {
		if record.SessionID == "" {
			return nil, nil, fmt.Errorf("invalid legacy archive association")
		}
		if prior, exists := seen[record.SessionID]; exists {
			if prior != record {
				return nil, nil, fmt.Errorf("archive migration found conflicting entries for one session")
			}
			continue
		}
		seen[record.SessionID] = record
		converted = append(converted, record)
		if record.ProjectID == "" {
			warnings = append(warnings, fmt.Sprintf("session %s stays ungrouped; no hidden all-files project fabricated", record.SessionID))
		}
		if record.ParentSessionID != "" {
			if _, ok := seen[record.ParentSessionID]; !ok {
				found := false
				for _, candidate := range legacy {
					if candidate.SessionID == record.ParentSessionID {
						found = true
						break
					}
				}
				if !found {
					warnings = append(warnings, fmt.Sprintf("session %s keeps dangling parent link without inventing a new branch", record.SessionID))
				}
			}
		}
	}
	sort.Slice(converted, func(i, j int) bool { return converted[i].SessionID < converted[j].SessionID })
	sort.Strings(warnings)
	return converted, warnings, nil
}

// ClassifyUngroupedQueues separates ungrouped queues (empty project) from
// grouped ones. Ungrouped state is retained as-is; conversion never invents a
// project for it.
func ClassifyUngroupedQueues(associations []QueueAssociation) (grouped, ungrouped []QueueAssociation) {
	for _, association := range associations {
		if association.ProjectID == "" {
			ungrouped = append(ungrouped, association)
		} else {
			grouped = append(grouped, association)
		}
	}
	return grouped, ungrouped
}

// ValidateUngroupedQueuePreservation confirms ungrouped queues survived without
// a fabricated hidden project. It never touches storage.
func ValidateUngroupedQueuePreservation(before, after []QueueAssociation) error {
	beforeUngrouped := make(map[string]bool)
	for _, record := range before {
		if record.ProjectID == "" {
			beforeUngrouped[record.SessionID] = true
		}
	}
	afterUngrouped := make(map[string]bool)
	for _, record := range after {
		if record.ProjectID == "" {
			afterUngrouped[record.SessionID] = true
		}
	}
	for session := range beforeUngrouped {
		if !afterUngrouped[session] {
			return fmt.Errorf("ungrouped queue for %s must survive without a fabricated hidden project", session)
		}
	}
	for session := range afterUngrouped {
		if !beforeUngrouped[session] {
			return fmt.Errorf("queue migration must not invent ungrouped state")
		}
	}
	return nil
}

// ClassifyUngroupedDeletions separates ungrouped deletion tombstones from
// grouped ones with the same no-hidden-project rule.
func ClassifyUngroupedDeletions(tombstones []DeletionTombstone) (grouped, ungrouped []DeletionTombstone) {
	for _, tombstone := range tombstones {
		if tombstone.ProjectID == "" {
			ungrouped = append(ungrouped, tombstone)
		} else {
			grouped = append(grouped, tombstone)
		}
	}
	return grouped, ungrouped
}

// ValidateUngroupedDeletionPreservation confirms ungrouped tombstones survived
// with phases intact. It never touches storage and never replays a delete.
func ValidateUngroupedDeletionPreservation(before, after []DeletionTombstone) error {
	beforeBySession := make(map[string]DeletionTombstone)
	for _, tombstone := range before {
		if tombstone.ProjectID == "" {
			beforeBySession[tombstone.SessionID] = tombstone
		}
	}
	afterBySession := make(map[string]DeletionTombstone)
	for _, tombstone := range after {
		if tombstone.ProjectID == "" {
			afterBySession[tombstone.SessionID] = tombstone
		}
	}
	for session, want := range beforeBySession {
		got, ok := afterBySession[session]
		if !ok || got.Phase != want.Phase {
			return fmt.Errorf("ungrouped deletion for %s must survive with its phase intact", session)
		}
	}
	return nil
}

// LedgerRollbackAssessment tells callers whether an older snapshot may resume
// as runnable authority. Restoring JSON never undoes native tool effects, so
// any post-backup dispatch, run, replay or tombstone growth keeps dispatch
// disabled and requires explicit reconciliation. Do not replay missed
// occurrences during rollback.
type LedgerRollbackAssessment struct {
	Ledger                 LedgerKind
	AllowedAsRunnable      bool
	DispatchDisabled       bool
	RequiresReconciliation bool
	RetainedEffects        []string
	Unresolved             []string
}

// AssessQueueRollback blocks runnable restore when the current queue advanced
// past its backup (new follow-ups, handled mutation/delivery IDs, uncertain
// dispatch/block claims) or when either side uses an unsupported schema.
func AssessQueueRollback(backup, current QueueLedgerSummary) (LedgerRollbackAssessment, error) {
	assessment := LedgerRollbackAssessment{Ledger: LedgerQueue, DispatchDisabled: true, RequiresReconciliation: true}
	if !backup.SchemaSupported || !current.SchemaSupported {
		assessment.RetainedEffects = append(assessment.RetainedEffects, "unknown newer queue schema; keep dispatch disabled and require explicit reconciliation")
		assessment.Unresolved = append(assessment.Unresolved, "queue schema is newer than supported; rollback cannot resume as runnable authority")
		return assessment, nil
	}
	if !backup.Present && !current.Present {
		assessment.AllowedAsRunnable = true
		assessment.DispatchDisabled = false
		assessment.RequiresReconciliation = false
		return assessment, nil
	}
	if current.RecordCount != backup.RecordCount || current.FollowUpCount != backup.FollowUpCount || current.HandledCount != backup.HandledCount || current.UncertainCount != backup.UncertainCount {
		assessment.RetainedEffects = append(assessment.RetainedEffects, "queue advanced after backup; accepted prompts and uncertain delivery claims are retained and never rewound")
		assessment.Unresolved = append(assessment.Unresolved, "queue has post-backup effects; rollback cannot restore the older snapshot as runnable authority")
		return assessment, nil
	}
	if !equalStrings(current.Sessions, backup.Sessions) {
		assessment.RetainedEffects = append(assessment.RetainedEffects, "queue session set changed after backup; retained without rewind")
		assessment.Unresolved = append(assessment.Unresolved, "queue sessions diverged; reconcile explicitly before any runnable restore")
		return assessment, nil
	}
	assessment.AllowedAsRunnable = true
	assessment.DispatchDisabled = false
	assessment.RequiresReconciliation = false
	return assessment, nil
}

// AssessScheduleRollback blocks runnable restore when the current schedule
// ledger advanced past its backup (new runs, replay records, occurrence
// claims) or uses an unsupported schema. Rollback never dispatches missed jobs.
func AssessScheduleRollback(backup, current ScheduleLedgerSummary) (LedgerRollbackAssessment, error) {
	assessment := LedgerRollbackAssessment{Ledger: LedgerSchedules, DispatchDisabled: true, RequiresReconciliation: true}
	if !backup.SchemaSupported || !current.SchemaSupported {
		assessment.RetainedEffects = append(assessment.RetainedEffects, "unknown newer schedule schema; keep dispatch disabled and require explicit reconciliation")
		assessment.Unresolved = append(assessment.Unresolved, "schedule schema is newer than supported; rollback cannot resume as runnable authority")
		return assessment, nil
	}
	if !backup.Present && !current.Present {
		assessment.AllowedAsRunnable = true
		assessment.DispatchDisabled = false
		assessment.RequiresReconciliation = false
		return assessment, nil
	}
	if !equalStrings(current.IDs, backup.IDs) {
		assessment.RetainedEffects = append(assessment.RetainedEffects, "schedule identities changed after backup; retained without dispatching missed jobs")
		assessment.Unresolved = append(assessment.Unresolved, "schedule identities diverged; reconcile explicitly before any runnable restore")
		return assessment, nil
	}
	if current.RunCount > backup.RunCount || current.OperationCount > backup.OperationCount {
		assessment.RetainedEffects = append(assessment.RetainedEffects, "schedule runs/replay records advanced after backup; monotonic claims are preserved and missed occurrences are never replayed")
		assessment.Unresolved = append(assessment.Unresolved, "schedule has post-backup runs; rollback cannot rewind the execution ledger")
		return assessment, nil
	}
	assessment.AllowedAsRunnable = true
	assessment.DispatchDisabled = false
	assessment.RequiresReconciliation = false
	return assessment, nil
}

// AssessDeletionRollback blocks runnable restore when the current deletion
// journal advanced past its backup (requested/confirmed phases, tombstones) or
// uses an unsupported schema. Confirmed tombstones are never discarded and a
// delete is never replayed against a new endpoint.
func AssessDeletionRollback(backup, current DeletionLedgerSummary) (LedgerRollbackAssessment, error) {
	assessment := LedgerRollbackAssessment{Ledger: LedgerDeletions, DispatchDisabled: true, RequiresReconciliation: true}
	if !backup.SchemaSupported || !current.SchemaSupported {
		assessment.RetainedEffects = append(assessment.RetainedEffects, "unknown newer deletion schema; keep dispatch disabled and require explicit reconciliation")
		assessment.Unresolved = append(assessment.Unresolved, "deletion schema is newer than supported; rollback cannot resume as runnable authority")
		return assessment, nil
	}
	if !backup.Present && !current.Present {
		assessment.AllowedAsRunnable = true
		assessment.DispatchDisabled = false
		assessment.RequiresReconciliation = false
		return assessment, nil
	}
	if current.Requested != backup.Requested || current.Confirmed != backup.Confirmed || len(current.Tombstones) != len(backup.Tombstones) {
		assessment.RetainedEffects = append(assessment.RetainedEffects, "deletion tombstones advanced after backup; confirmed deletions are retained and never resurrected through fallback")
		assessment.Unresolved = append(assessment.Unresolved, "deletion journal has post-backup tombstones; rollback cannot rewind confirmed deletions")
		return assessment, nil
	}
	for i := range current.Tombstones {
		if i >= len(backup.Tombstones) || current.Tombstones[i] != backup.Tombstones[i] {
			assessment.RetainedEffects = append(assessment.RetainedEffects, "deletion tombstone set changed after backup; preserved without replay against a new endpoint")
			assessment.Unresolved = append(assessment.Unresolved, "deletion tombstones diverged; reconcile explicitly before any runnable restore")
			return assessment, nil
		}
	}
	assessment.AllowedAsRunnable = true
	assessment.DispatchDisabled = false
	assessment.RequiresReconciliation = false
	return assessment, nil
}

// ControllerRollbackReceipt names the schemas, restored files, retained
// post-backup effects and unresolved work for a topology-aware rollback.
// Cache loss is never durable-user-data loss; only declared regenerable cache
// may be discarded automatically.
type ControllerRollbackReceipt struct {
	FromTopology           MigrationTopology
	ToTopology             MigrationTopology
	RestoredFiles          []string
	RetainedEffects        []string
	Unresolved             []string
	DispatchDisabled       bool
	RequiresReconciliation bool
}

// PlanControllerRollback aggregates ledger assessments into one
// topology-aware rollback decision without touching storage.
func PlanControllerRollback(from, to MigrationTopology, queue, schedule, deletion LedgerRollbackAssessment) (ControllerRollbackReceipt, error) {
	if err := ValidateTopologySwitch(from, to); err != nil {
		// A same-topology rollback is still a valid no-switch restore; only
		// unknown topologies are rejected here.
		if ValidateMigrationTopology(from) != nil || ValidateMigrationTopology(to) != nil {
			return ControllerRollbackReceipt{}, err
		}
	}
	receipt := ControllerRollbackReceipt{FromTopology: from, ToTopology: to, DispatchDisabled: false}
	for _, assessment := range []LedgerRollbackAssessment{queue, schedule, deletion} {
		receipt.RetainedEffects = append(receipt.RetainedEffects, assessment.RetainedEffects...)
		receipt.Unresolved = append(receipt.Unresolved, assessment.Unresolved...)
		if assessment.DispatchDisabled || assessment.RequiresReconciliation || !assessment.AllowedAsRunnable {
			receipt.DispatchDisabled = true
			receipt.RequiresReconciliation = true
		}
	}
	if len(receipt.Unresolved) == 0 {
		receipt.RestoredFiles = []string{"non-authority state may be restored from compatible backups; authority ledgers resume only after explicit resume"}
	} else {
		receipt.RestoredFiles = []string{}
	}
	sort.Strings(receipt.RetainedEffects)
	sort.Strings(receipt.Unresolved)
	return receipt, nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
