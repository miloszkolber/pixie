package controller_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

func TestScheduleRunNowWithMissingProjectRootFailsWithoutDispatch(t *testing.T) {
	var dispatches atomic.Int32
	var rootGone atomic.Bool
	s, err := controller.NewSchedules(persist.Store{Dir: t.TempDir()}, func(project, root string) (string, error) {
		if rootGone.Load() {
			return "", fmt.Errorf("project root unavailable")
		}
		if project != "p" || root != "/p" {
			return "", fmt.Errorf("project root unavailable")
		}
		return root, nil
	}, func(context.Context, controller.Schedule, func(string) error) error {
		dispatches.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	created, err := s.Handle(t.Context(), "schedule.create", map[string]any{"projectId": "p", "root": "/p", "prompt": "Review", "cron": "0 9 * * *"})
	if err != nil {
		t.Fatal(err)
	}
	id := created.(controller.Schedule).ID
	// The admitted root disappears after creation. Dispatch must fail
	// without invoking the runner, and the schedule itself stays listed.
	rootGone.Store(true)
	if _, err := s.Handle(t.Context(), "schedule.runNow", map[string]any{"projectId": "p", "scheduleId": id}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		list, _ := s.Handle(t.Context(), "schedule.list", map[string]any{"projectId": "p"})
		jobs := list.([]controller.Schedule)
		if len(jobs) != 1 {
			t.Fatalf("schedule lost after failed dispatch: %#v", jobs)
		}
		if len(jobs[0].Runs) == 1 && jobs[0].Runs[0].Status != "running" {
			run := jobs[0].Runs[0]
			if run.Status != "failed" || !strings.Contains(run.Error, "project root unavailable") {
				t.Fatalf("missing root misreported: %+v", run)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("missing-root run did not settle: %#v", jobs)
		}
		time.Sleep(time.Millisecond)
	}
	if dispatches.Load() != 0 {
		t.Fatal("missing project root dispatched the runner")
	}
}
