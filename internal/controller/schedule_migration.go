package controller

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/miloszkolber/pixie/internal/persist"
)

const (
	ScheduleMigrationPhasePrepared  = "prepared"
	ScheduleMigrationPhaseMigrating = "migrating"
	scheduleLedgerVersion           = 2
)

// decodeScheduleLedger accepts the pre-ledger map and the v1 ledger only to
// migrate them forward. A v2 ledger is deliberately not readable by an older
// binary: its unknown version must fail before it can dispatch a newer state.
func decodeScheduleLedger(raw []byte) (map[string]Schedule, []scheduleOperation, bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, nil, false, err
	}
	if versionRaw, ok := fields["version"]; ok {
		var version int
		if err := json.Unmarshal(versionRaw, &version); err != nil {
			return nil, nil, false, fmt.Errorf("invalid schedule ledger version")
		}
		var disk scheduleDisk
		if err := json.Unmarshal(raw, &disk); err != nil {
			return nil, nil, false, err
		}
		switch version {
		case 1:
			if disk.Jobs == nil || len(disk.Operations) > 512 {
				return nil, nil, false, fmt.Errorf("invalid schedule ledger")
			}
			return disk.Jobs, disk.Operations, true, nil
		case scheduleLedgerVersion:
			if disk.Jobs == nil || len(disk.Operations) > 512 {
				return nil, nil, false, fmt.Errorf("invalid schedule ledger")
			}
			return disk.Jobs, disk.Operations, false, nil
		default:
			return nil, nil, false, fmt.Errorf("unsupported schedule ledger version %d", version)
		}
	}
	var jobs map[string]Schedule
	if err := json.Unmarshal(raw, &jobs); err != nil {
		return nil, nil, false, err
	}
	if jobs == nil {
		return nil, nil, false, fmt.Errorf("invalid schedule ledger")
	}
	// Pre-v1 maps and v1 jobs had no runtime budget. Nil explicitly preserves
	// their unlimited behavior after the v2 rewrite.
	return jobs, nil, true, nil
}

// ScheduleLedgerSummary is a read-only migration/rollback view. It carries
// only stable schedule identities and execution counts, never prompt content.
type ScheduleLedgerSummary struct {
	Present          bool
	Version          int
	Legacy           bool
	SchemaSupported  bool
	JobCount         int
	IDs              []string
	Timezones        []string
	RunCount         int
	SessionLinks     int
	Interrupted      int
	RunningUncertain int
	OperationCount   int
	PausedCount      int
}

// InspectScheduleLedger performs no conversion, write, or dispatch. Unknown
// versions remain inspectable so rollback logic can fail closed explicitly.
func InspectScheduleLedger(store persist.Store) (ScheduleLedgerSummary, error) {
	name := filepath.Join(store.Dir, "schedules.json")
	raw, _, err := persist.ReadFile(name)
	if os.IsNotExist(err) {
		if _, _, backupErr := persist.ReadFile(name + ".bak"); os.IsNotExist(backupErr) {
			return ScheduleLedgerSummary{SchemaSupported: true}, nil
		}
		return ScheduleLedgerSummary{}, fmt.Errorf("schedule state is missing while a backup remains")
	}
	if err != nil {
		return ScheduleLedgerSummary{}, fmt.Errorf("schedule state is unreadable")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return ScheduleLedgerSummary{}, fmt.Errorf("schedule state is unreadable")
	}
	version := 0
	if value, ok := fields["version"]; ok && json.Unmarshal(value, &version) != nil {
		return ScheduleLedgerSummary{}, fmt.Errorf("schedule state is unreadable")
	}
	summary := ScheduleLedgerSummary{Present: true, Version: version, Legacy: version == 0, SchemaSupported: version == 0 || version == 1 || version == scheduleLedgerVersion}
	if !summary.SchemaSupported {
		return summary, nil
	}
	jobs, operations, _, err := decodeScheduleLedger(raw)
	if err != nil {
		return ScheduleLedgerSummary{}, fmt.Errorf("schedule state is unreadable")
	}
	if err := validateSchedules(jobs); err != nil {
		return ScheduleLedgerSummary{}, fmt.Errorf("schedule state is unreadable")
	}
	for _, operation := range operations {
		if operation.Key == "" || operation.Fingerprint == "" || !json.Valid(operation.Result) {
			return ScheduleLedgerSummary{}, fmt.Errorf("schedule state is unreadable")
		}
	}
	return summarizeSchedules(jobs, version, version == 0, len(operations)), nil
}

func summarizeSchedules(jobs map[string]Schedule, version int, legacy bool, operations int) ScheduleLedgerSummary {
	summary := ScheduleLedgerSummary{Present: true, Version: version, Legacy: legacy, SchemaSupported: true, JobCount: len(jobs), OperationCount: operations}
	zones := map[string]bool{}
	for id, job := range jobs {
		summary.IDs = append(summary.IDs, id)
		zone := job.Timezone
		if zone == "" {
			zone = "UTC"
		}
		zones[zone] = true
		summary.RunCount += len(job.Runs)
		if job.Paused {
			summary.PausedCount++
		}
		for _, run := range job.Runs {
			if run.SessionID != "" {
				summary.SessionLinks++
			}
			switch run.Status {
			case scheduleRunInterrupted:
				summary.Interrupted++
			case scheduleRunRunning, scheduleRunCancelling, scheduleRunCancellationUnconfirmed:
				summary.RunningUncertain++
			}
		}
	}
	for zone := range zones {
		summary.Timezones = append(summary.Timezones, zone)
	}
	sort.Strings(summary.IDs)
	sort.Strings(summary.Timezones)
	return summary
}

type ScheduleMigrationPlan struct {
	Phase            string
	JobCount         int
	DispatchBlocked  bool
	MutationsBlocked bool
	Preserved        []string
	Conflicts        []string
	Note             string
	Unresolved       []string
}

// PlanScheduleMigration is a dry-run guard. Runtime migration is only the
// v1-to-v2 durable conversion in NewSchedules; this helper never writes it.
func PlanScheduleMigration(summary ScheduleLedgerSummary) (ScheduleMigrationPlan, error) {
	preserved := []string{"ids", "timezone", "occurrence/run identities", "native session links", "replay records", "interrupted/uncertain claims", "runtime budgets"}
	if !summary.Present {
		return ScheduleMigrationPlan{
			Phase: ScheduleMigrationPhasePrepared, Preserved: []string{}, DispatchBlocked: true,
			Note: "empty schedule ledger; nothing to convert, resume schedules explicitly after migration",
		}, nil
	}
	plan := ScheduleMigrationPlan{Phase: ScheduleMigrationPhasePrepared, JobCount: summary.JobCount, Preserved: preserved, DispatchBlocked: true, Unresolved: []string{}}
	if !summary.SchemaSupported {
		plan.MutationsBlocked = true
		plan.Unresolved = append(plan.Unresolved, "unsupported schedule ledger version")
		plan.Note = fmt.Sprintf("schedule schema v%d is newer than supported v%d; diagnostics remain available while mutations stay blocked", summary.Version, scheduleLedgerVersion)
		return plan, nil
	}
	plan.Note = "schedules stay project-scoped; migration claims due occurrences without dispatching missed jobs and pauses ambiguous restarts"
	return plan, nil
}

func ValidateSchedulePreservation(before, after ScheduleLedgerSummary) error {
	if !before.SchemaSupported || !after.SchemaSupported {
		return fmt.Errorf("schedule preservation requires supported schemas on both sides")
	}
	if !sameStrings(before.IDs, after.IDs) {
		return fmt.Errorf("schedule migration lost or invented schedule identities")
	}
	if after.RunCount < before.RunCount || after.OperationCount < before.OperationCount {
		return fmt.Errorf("schedule migration must preserve run and replay records")
	}
	return nil
}

func sameStrings(first, second []string) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}
