package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/miloszkolber/pixie/internal/persist"
)

// ScheduleMigrationHelperVersion versions these additive MIG-01 schedule
// inspect/plan helpers. It never changes the schedules.json schema itself.
const ScheduleMigrationHelperVersion = 1

// Repeatable schedule migration phases mirror the project-root journal:
// prepared inspects and stages, migrating publishes through checkpoints.
const (
	ScheduleMigrationPhasePrepared  = "prepared"
	ScheduleMigrationPhaseMigrating = "migrating"
)

// ScheduleLedgerSummary is a read-only dry-run view of schedules.json. It
// preserves IDs, timezone, occurrence/run identities, native session links,
// replay records and interrupted/uncertain claims without dispatching work.
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

// InspectScheduleLedger reads schedules.json with no writes, no admission and
// no dispatch. Unknown files stay untouched; only schedules.json is read. A
// missing primary with a remaining backup fails closed and never replays the
// older execution ledger. A newer unsupported schema stays diagnosable here
// while mutations stay blocked in PlanScheduleMigration.
func InspectScheduleLedger(store persist.Store) (ScheduleLedgerSummary, error) {
	path := filepath.Join(store.Dir, "schedules.json")
	raw, _, err := persist.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if _, backupErr := os.Stat(path + ".bak"); !errors.Is(backupErr, os.ErrNotExist) {
				return ScheduleLedgerSummary{}, fmt.Errorf("schedule primary is missing; refusing an older execution ledger")
			}
			return ScheduleLedgerSummary{SchemaSupported: true}, nil
		}
		return ScheduleLedgerSummary{}, fmt.Errorf("schedule ledger is unreadable")
	}
	var peek struct {
		Version *int `json:"version"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return ScheduleLedgerSummary{}, fmt.Errorf("schedule ledger is unreadable")
	}
	if peek.Version == nil {
		var jobs map[string]Schedule
		if err := persist.Decode(raw, &jobs, nil); err != nil {
			return ScheduleLedgerSummary{}, fmt.Errorf("schedule ledger is unreadable")
		}
		if err := validateSchedules(jobs); err != nil {
			return ScheduleLedgerSummary{}, err
		}
		return summarizeSchedules(jobs, 0, true, 0), nil
	}
	if *peek.Version != 1 {
		return ScheduleLedgerSummary{Present: true, Version: *peek.Version, SchemaSupported: false}, nil
	}
	var disk scheduleDisk
	if err := json.Unmarshal(raw, &disk); err != nil {
		return ScheduleLedgerSummary{}, fmt.Errorf("schedule ledger is unreadable")
	}
	if disk.Version != 1 || disk.Jobs == nil || len(disk.Operations) > 512 {
		return ScheduleLedgerSummary{}, fmt.Errorf("invalid schedule ledger")
	}
	for _, op := range disk.Operations {
		if op.Key == "" || op.Fingerprint == "" || !json.Valid(op.Result) {
			return ScheduleLedgerSummary{}, fmt.Errorf("invalid schedule operation")
		}
	}
	if err := validateSchedules(disk.Jobs); err != nil {
		return ScheduleLedgerSummary{}, err
	}
	return summarizeSchedules(disk.Jobs, disk.Version, false, len(disk.Operations)), nil
}

func summarizeSchedules(jobs map[string]Schedule, version int, legacy bool, operations int) ScheduleLedgerSummary {
	summary := ScheduleLedgerSummary{Present: true, Version: version, Legacy: legacy, SchemaSupported: true, JobCount: len(jobs), OperationCount: operations}
	zones := make(map[string]bool)
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
			case "interrupted":
				summary.Interrupted++
			case "running":
				summary.RunningUncertain++
			}
		}
	}
	sort.Strings(summary.IDs)
	for zone := range zones {
		summary.Timezones = append(summary.Timezones, zone)
	}
	sort.Strings(summary.Timezones)
	return summary
}

// ScheduleMigrationPlan is a dry-run conversion description with no writes.
// Schedules stay project-scoped; migration never dispatches missed jobs.
type ScheduleMigrationPlan struct {
	Phase            string
	JobCount         int
	Preserved        []string
	Conflicts        []string
	DispatchBlocked  bool
	MutationsBlocked bool
	Note             string
}

// PlanScheduleMigration derives a repeatable dry-run plan from one inspect
// pass. Re-running identical inputs is idempotent. Changed inputs conflict at
// the caller, which must compare input hashes before staging. It performs no
// writes and never restores an older ledger over dispatched effects.
func PlanScheduleMigration(summary ScheduleLedgerSummary) (ScheduleMigrationPlan, error) {
	if !summary.Present {
		return ScheduleMigrationPlan{
			Phase: ScheduleMigrationPhasePrepared, Preserved: []string{},
			DispatchBlocked: true, Note: "empty schedule ledger; nothing to convert, resume schedules explicitly after migration",
		}, nil
	}
	if !summary.SchemaSupported {
		return ScheduleMigrationPlan{
			Phase: ScheduleMigrationPhasePrepared, JobCount: summary.JobCount,
			Preserved:        []string{"ids", "timezone", "occurrence/run identities", "native session links", "replay records", "interrupted/uncertain claims"},
			DispatchBlocked:  true,
			MutationsBlocked: true,
			Note:             fmt.Sprintf("schedule schema v%d is newer than supported v1; diagnostics remain available while mutations stay blocked", summary.Version),
		}, nil
	}
	return ScheduleMigrationPlan{
		Phase:    ScheduleMigrationPhasePrepared,
		JobCount: summary.JobCount,
		Preserved: []string{
			"ids",
			"timezone",
			"occurrence/run identities",
			"native session links",
			"replay records",
			"interrupted/uncertain claims",
		},
		DispatchBlocked: true,
		Note:            "schedules stay project-scoped; migration claims due occurrences without dispatching missed jobs and pauses ambiguous restarts",
	}, nil
}

// ValidateSchedulePreservation confirms a staged conversion kept every
// schedule identity, timezone occurrence, run link, replay record and
// interrupted claim. It never touches storage.
func ValidateSchedulePreservation(before, after ScheduleLedgerSummary) error {
	if !before.SchemaSupported || !after.SchemaSupported {
		return fmt.Errorf("schedule preservation requires supported schemas on both sides")
	}
	if len(before.IDs) != len(after.IDs) {
		return fmt.Errorf("schedule migration lost or invented schedule identities")
	}
	for i := range before.IDs {
		if before.IDs[i] != after.IDs[i] {
			return fmt.Errorf("schedule migration lost or invented schedule identities")
		}
	}
	if after.RunCount < before.RunCount || after.OperationCount < before.OperationCount {
		return fmt.Errorf("schedule migration must preserve run and replay records")
	}
	return nil
}
