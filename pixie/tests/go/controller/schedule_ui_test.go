package controller_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

func TestSchedulePreviewUsesSchedulerValidationAndDoesNotPersist(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	s, err := controller.NewSchedules(store, func(project, root string) (string, error) {
		if project != "p" || root != "/p" {
			return "", fmt.Errorf("project root unavailable")
		}
		return root, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	for _, tc := range []struct {
		name, project, root, cron, zone string
		valid                           bool
	}{
		{"default UTC", "p", "/p", "0 9 * * *", "", true},
		{"IANA", "p", "/p", "0 9 * * *", "Europe/Warsaw", true},
		{"bad cron", "p", "/p", "* * *", "UTC", false},
		{"impossible date", "p", "/p", "0 9 31 2 *", "UTC", false},
		{"bad zone", "p", "/p", "0 9 * * *", "Not/AZone", false},
		{"injected zone", "p", "/p", "0 9 * * *", "UTC CRON_TZ=UTC", false},
		{"missing root", "p", "/gone", "0 9 * * *", "UTC", false},
		{"other project", "other", "/p", "0 9 * * *", "UTC", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := s.Handle(t.Context(), "schedule.preview", map[string]any{"projectId": tc.project, "root": tc.root, "cron": tc.cron, "timezone": tc.zone})
			if (err == nil) != tc.valid {
				t.Fatalf("preview: %v, %v", result, err)
			}
			if !tc.valid {
				return
			}
			preview := result.(map[string]any)
			zone, err := time.LoadLocation(preview["timezone"].(string))
			if err != nil {
				t.Fatal(err)
			}
			next := preview["nextRun"].(time.Time)
			if !next.After(time.Now()) || next.In(zone).Hour() != 9 {
				t.Fatalf("wrong next occurrence: %v", next)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(store.Dir, "schedules.json")); !os.IsNotExist(err) {
		t.Fatalf("preview wrote schedule state: %v", err)
	}
}

func TestSchedulePreviewPreservesWarsawDSTSemantics(t *testing.T) {
	s, err := controller.NewSchedules(persist.Store{Dir: t.TempDir()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	zone, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(zone)
	for _, month := range []time.Month{time.March, time.October} {
		// Choose the next last Sunday in the transition month. The five-field cron
		// remains annual, so a missing spring occurrence moves to a later year.
		year := now.Year()
		lastSunday := func(year int) time.Time {
			last := time.Date(year, month+1, 0, 12, 0, 0, 0, zone)
			return last.AddDate(0, 0, -int(last.Weekday()))
		}
		transition := lastSunday(year)
		if !transition.After(now.Add(24 * time.Hour)) {
			year++
			transition = lastSunday(year)
		}
		result, err := s.Handle(t.Context(), "schedule.preview", map[string]any{"projectId": "p", "root": "/p", "cron": fmt.Sprintf("30 2 %d %d *", transition.Day(), month), "timezone": zone.String()})
		if err != nil {
			t.Fatal(err)
		}
		next := result.(map[string]any)["nextRun"].(time.Time).In(zone)
		if next.Year() < year {
			// In the final days of a transition month, next year's calendar day
			// can still have a valid occurrence this year. Preview uses the real
			// server clock, so that intervening annual occurrence is authoritative.
			t.Logf("%s transition is beyond the next annual occurrence %v", month, next)
			continue
		}
		if month == time.March && next.Year() <= year {
			t.Fatalf("dispatched nonexistent spring time: %v", next)
		}
		if month == time.October {
			_, offset := next.Zone()
			if next.Year() != year || next.Day() != transition.Day() || next.Hour() != 2 || next.Minute() != 30 || offset != 2*60*60 {
				t.Fatalf("first repeated autumn time changed: %v", next)
			}
		}
	}
}

func TestPausedRunNowEditAndDeleteRemainSeparateFromStop(t *testing.T) {
	started := make(chan struct{}, 1)
	s, err := controller.NewSchedules(persist.Store{Dir: t.TempDir()}, nil, func(ctx context.Context, _ controller.Schedule, admitted func(string) error) error {
		if err := admitted("native-paused-run"); err != nil {
			return err
		}
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
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
	call := func(method string, p map[string]any) (any, error) {
		p["projectId"], p["scheduleId"] = "p", id
		return s.Handle(t.Context(), "schedule."+method, p)
	}
	if _, err := call("update", map[string]any{"paused": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := call("runNow", map[string]any{"mutationId": "paused-run"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("paused manual run not dispatched")
	}
	for _, method := range []string{"delete", "update"} {
		_, err := call(method, map[string]any{"prompt": "Changed"})
		if err == nil || !strings.Contains(err.Error(), "stop the running schedule") {
			t.Fatalf("%s changed a running schedule: %v", method, err)
		}
	}
	if _, err := call("runNow", map[string]any{"mutationId": "paused-run"}); err != nil {
		t.Fatalf("duplicate retry: %v", err)
	}
	if _, err := call("runNow", map[string]any{"mutationId": "another-run"}); err == nil {
		t.Fatal("overlap admitted")
	}
	list, _ := s.Handle(t.Context(), "schedule.list", map[string]any{"projectId": "p"})
	job := list.([]controller.Schedule)[0]
	if !job.Paused || len(job.Runs) != 1 || job.Runs[0].SessionID != "native-paused-run" {
		t.Fatalf("paused run-now changed state: %+v", job)
	}
	if _, err := call("stop", map[string]any{"mutationId": "stop"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		list, _ = s.Handle(t.Context(), "schedule.list", map[string]any{"projectId": "p"})
		job = list.([]controller.Schedule)[0]
		if job.Runs[0].Status == "interrupted" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stop did not settle")
		}
		time.Sleep(time.Millisecond)
	}
	if !job.Paused {
		t.Fatal("stop resumed future dispatch")
	}
	if _, err := call("update", map[string]any{"prompt": "Changed", "paused": false}); err != nil {
		t.Fatal(err)
	}
	if _, err := call("delete", map[string]any{"mutationId": "delete"}); err != nil {
		t.Fatal(err)
	}
	if _, err := call("delete", map[string]any{"mutationId": "delete"}); err != nil {
		t.Fatalf("delete retry: %v", err)
	}
}

func TestScheduleMutationPersistenceFailureRetainsLastSavedState(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	s, err := controller.NewSchedules(store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	created, err := s.Handle(t.Context(), "schedule.create", map[string]any{"projectId": "p", "root": "/p", "prompt": "Review", "cron": "0 9 * * *"})
	if err != nil {
		t.Fatal(err)
	}
	id := created.(controller.Schedule).ID
	// A directory at the primary filename forces atomic replacement to fail even as root.
	path := filepath.Join(store.Dir, "schedules.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	p := map[string]any{"projectId": "p", "scheduleId": id, "paused": true, "mutationId": "retry-write"}
	if _, err := s.Handle(t.Context(), "schedule.update", p); err == nil {
		t.Fatal("failed write acknowledged")
	}
	list, _ := s.Handle(t.Context(), "schedule.list", map[string]any{"projectId": "p"})
	if list.([]controller.Schedule)[0].Paused {
		t.Fatal("failed write replaced saved state")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Handle(t.Context(), "schedule.update", p); err != nil {
		t.Fatalf("same mutation could not retry failed persistence: %v", err)
	}
}
