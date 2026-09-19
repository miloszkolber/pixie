package controller

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/persist"
)

func TestScheduleV2LedgerRejectsOversizedRuntimeBeforeRewriteOrDispatch(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	maxRuntime := maxScheduleRuntimeSeconds + 1
	job := Schedule{
		ID:                "oversized-runtime",
		ProjectID:         "project",
		Root:              "/project",
		Prompt:            "Review",
		Cron:              "0 9 * * *",
		Timezone:          "UTC",
		MaxRuntimeSeconds: &maxRuntime,
		NextRun:           time.Now().Add(-time.Hour),
		Runs: []ScheduleRun{{
			ID:        "running-run",
			StartedAt: time.Now().Add(-time.Hour),
			Status:    scheduleRunRunning,
		}},
	}
	if err := persist.Write(store, "schedules.json", scheduleDisk{Version: scheduleLedgerVersion, Jobs: map[string]Schedule{job.ID: job}, Operations: []scheduleOperation{}}, nil); err != nil {
		t.Fatal(err)
	}
	before, _, err := persist.ReadFile(filepath.Join(store.Dir, "schedules.json"))
	if err != nil {
		t.Fatal(err)
	}
	var dispatched atomic.Int32
	if schedules, err := NewSchedules(store, nil, func(context.Context, Schedule, func(string) error) error {
		dispatched.Add(1)
		return nil
	}); err == nil || schedules != nil {
		t.Fatalf("oversized v2 runtime was accepted: schedules=%v err=%v", schedules, err)
	}
	if dispatched.Load() != 0 {
		t.Fatal("invalid ledger dispatched a schedule run")
	}
	after, _, err := persist.ReadFile(filepath.Join(store.Dir, "schedules.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("invalid ledger was rewritten before it was rejected")
	}
}
