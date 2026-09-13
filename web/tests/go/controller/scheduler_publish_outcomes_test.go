package controller_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

func mustCreateSchedule(t *testing.T, schedules *controller.Schedules, mutationID, prompt string) controller.Schedule {
	t.Helper()
	result, err := schedules.Handle(context.Background(), "schedule.create", map[string]any{
		"projectId":  "project",
		"root":       "/project",
		"prompt":     prompt,
		"cron":       "0 9 * * *",
		"timezone":   "UTC",
		"mutationId": mutationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result.(controller.Schedule)
}

func schedulePrompt(t *testing.T, schedules *controller.Schedules, jobID string) string {
	t.Helper()
	result, err := schedules.Handle(context.Background(), "schedule.list", map[string]any{"projectId": "project"})
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range result.([]controller.Schedule) {
		if job.ID == jobID {
			return job.Prompt
		}
	}
	t.Fatalf("schedule %s missing", jobID)
	return ""
}

func TestSchedulePublishPostRenameUncertainRetainsMutation(t *testing.T) {
	for name, faults := range map[string]persist.PublishFaults{
		"DurabilityDirSync": {FailDirSync: errors.New("injected dir-sync failure")},
		"DurabilityReply":   {FailReply: errors.New("injected reply loss")},
	} {
		t.Run(name, func(t *testing.T) {
			store := persist.Store{Dir: t.TempDir()}
			schedules, err := controller.NewSchedules(store, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer schedules.Close(context.Background())
			job := mustCreateSchedule(t, schedules, "create-once", "Review")
			schedules.SetPublishFaults(faults)
			_, err = schedules.Handle(context.Background(), "schedule.update", map[string]any{
				"projectId":  "project",
				"scheduleId": job.ID,
				"prompt":     "Candidate",
				"mutationId": "update-once",
			})
			if err == nil {
				t.Fatal("post-rename fault must block the publish")
			}
			decision := controller.DecideSchedulePublish(persist.PublishOutcome{Kind: persist.OutcomeDurabilityUncertain, Stage: persist.StageDirSync, PrimaryVisible: true, MustReconcile: true})
			if decision.MayDispatch || !decision.MustReconcile || !decision.MustRetainMutation {
				t.Fatalf("uncertain schedule publish must block dispatch and retain mutation: %#v", decision)
			}
			primary, err := os.ReadFile(filepath.Join(store.Dir, "schedules.json"))
			if err != nil || !strings.Contains(string(primary), "Candidate") {
				t.Fatalf("uncertain primary must stay visible for reconciliation: %v", err)
			}
			if got := schedulePrompt(t, schedules, job.ID); got != "Candidate" {
				t.Fatalf("uncertain candidate must be retained in memory, got %q", got)
			}
			summary, err := controller.ReconcileScheduleAfterPublish(store, persist.PublishOutcome{Kind: persist.OutcomeDurabilityUncertain, Stage: persist.StageDirSync, PrimaryVisible: true, MustReconcile: true})
			if err != nil {
				t.Fatal(err)
			}
			if summary.JobCount != 1 || len(summary.IDs) != 1 || summary.IDs[0] != job.ID {
				t.Fatalf("reconcile must read the validated candidate: %#v", summary)
			}
			// An identical retry must reconcile the original operation instead
			// of duplicating work, even while the fault is still armed.
			retry, err := schedules.Handle(context.Background(), "schedule.update", map[string]any{
				"projectId":  "project",
				"scheduleId": job.ID,
				"prompt":     "Candidate",
				"mutationId": "update-once",
			})
			if err != nil {
				t.Fatalf("identical mutation retry must reconcile: %v", err)
			}
			if retry.(controller.Schedule).ID != job.ID || retry.(controller.Schedule).Prompt != "Candidate" {
				t.Fatalf("retry reconciled the wrong operation: %#v", retry)
			}
			if _, err := schedules.Handle(context.Background(), "schedule.update", map[string]any{
				"projectId":  "project",
				"scheduleId": job.ID,
				"prompt":     "Different",
				"mutationId": "update-once",
			}); err == nil || !strings.Contains(err.Error(), "mutation identity") {
				t.Fatalf("reused mutation identity with different input must conflict: %v", err)
			}
			schedules.SetPublishFaults(persist.PublishFaults{})
			if _, err := schedules.Handle(context.Background(), "schedule.update", map[string]any{
				"projectId":  "project",
				"scheduleId": job.ID,
				"prompt":     "Installed",
				"mutationId": "update-installed",
			}); err != nil {
				t.Fatalf("installed publish must succeed after faults clear: %v", err)
			}
			if got := schedulePrompt(t, schedules, job.ID); got != "Installed" {
				t.Fatalf("installed publish lost: %q", got)
			}
		})
	}
}

func TestSchedulePublishPreRenameKnownUncommittedPreservesPrior(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	schedules, err := controller.NewSchedules(store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer schedules.Close(context.Background())
	job := mustCreateSchedule(t, schedules, "create-once", "Review")
	schedules.SetPublishFaults(persist.PublishFaults{FailPrimary: errors.New("injected primary failure")})
	if _, err := schedules.Handle(context.Background(), "schedule.update", map[string]any{
		"projectId":  "project",
		"scheduleId": job.ID,
		"prompt":     "Candidate",
		"mutationId": "update-once",
	}); err == nil {
		t.Fatal("pre-rename fault must fail")
	}
	primary, err := os.ReadFile(filepath.Join(store.Dir, "schedules.json"))
	if err != nil || strings.Contains(string(primary), "Candidate") || !strings.Contains(string(primary), "Review") {
		t.Fatal("known-uncommitted failure must preserve the prior commit")
	}
	if got := schedulePrompt(t, schedules, job.ID); got != "Review" {
		t.Fatalf("known-uncommitted failure must restore prior memory, got %q", got)
	}
	schedules.SetPublishFaults(persist.PublishFaults{})
	if _, err := schedules.Handle(context.Background(), "schedule.update", map[string]any{
		"projectId":  "project",
		"scheduleId": job.ID,
		"prompt":     "Candidate",
		"mutationId": "update-once",
	}); err != nil {
		t.Fatalf("retry after known-uncommitted must re-attempt: %v", err)
	}
}

func TestSchedulePublishUncertainDeleteStaysApplied(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	schedules, err := controller.NewSchedules(store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer schedules.Close(context.Background())
	job := mustCreateSchedule(t, schedules, "create-once", "Review")
	schedules.SetPublishFaults(persist.PublishFaults{FailDirSync: errors.New("injected dir-sync failure")})
	if _, err := schedules.Handle(context.Background(), "schedule.delete", map[string]any{
		"projectId":  "project",
		"scheduleId": job.ID,
		"mutationId": "delete-once",
	}); err == nil {
		t.Fatal("uncertain delete must stay blocked")
	}
	summary, err := controller.ReconcileScheduleAfterPublish(store, persist.PublishOutcome{Kind: persist.OutcomeDurabilityUncertain, Stage: persist.StageDirSync, PrimaryVisible: true, MustReconcile: true})
	if err != nil {
		t.Fatal(err)
	}
	if summary.JobCount != 0 {
		t.Fatalf("uncertain delete must stay applied on the visible primary: %#v", summary)
	}
	result, err := schedules.Handle(context.Background(), "schedule.list", map[string]any{"projectId": "project"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.([]controller.Schedule)) != 0 {
		t.Fatalf("uncertain delete must not resurrect in memory: %#v", result)
	}
}

func TestSchedulePublishUncertainRunBlocksDispatch(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	dispatched := 0
	schedules, err := controller.NewSchedules(store, nil, func(context.Context, controller.Schedule, func(string) error) error {
		dispatched++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer schedules.Close(context.Background())
	job := mustCreateSchedule(t, schedules, "create-once", "Review")
	schedules.SetPublishFaults(persist.PublishFaults{FailReply: errors.New("injected reply loss")})
	if _, err := schedules.Handle(context.Background(), "schedule.runNow", map[string]any{
		"projectId":  "project",
		"scheduleId": job.ID,
		"mutationId": "run-once",
	}); err == nil {
		t.Fatal("uncertain run claim must not dispatch")
	}
	if dispatched != 0 {
		t.Fatal("only an installed claim may dispatch a run")
	}
}
