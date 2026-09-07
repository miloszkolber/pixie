package controller_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

func TestSchedulesOwnPersistenceProjectIsolationAndCancellation(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	var calls atomic.Int32
	started := make(chan struct{}, 1)
	runner := func(ctx context.Context, j controller.Schedule, admitted func(string) error) error {
		calls.Add(1)
		if err := admitted("native-session"); err != nil {
			return err
		}
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	roots := func(project, root string) (string, error) {
		if project != "project" || root != "/project" {
			return "", fmt.Errorf("invalid root")
		}
		return root, nil
	}
	schedules, err := controller.NewSchedules(store, roots, runner)
	if err != nil {
		t.Fatal(err)
	}
	defer schedules.Close(context.Background())
	call := func(method string, p map[string]any) (any, error) {
		p["projectId"] = "project"
		return schedules.Handle(context.Background(), "schedule."+method, p)
	}
	_, err = call("create", map[string]any{"root": "/escape", "prompt": "Review", "cron": "0 9 * * *"})
	if err == nil {
		t.Fatal("accepted unadmitted root")
	}
	_, err = call("create", map[string]any{"root": "/project", "prompt": "Review", "cron": "TZ=0"})
	if err == nil {
		t.Fatal("accepted invalid cron")
	}
	result, err := call("create", map[string]any{"root": "/project", "prompt": "Review", "cron": "0 9 * * *", "timezone": "Europe/Warsaw"})
	if err != nil {
		t.Fatal(err)
	}
	job := result.(controller.Schedule)
	zone, _ := time.LoadLocation("Europe/Warsaw")
	if job.NextRun.In(zone).Hour() != 9 {
		t.Fatal("timezone ignored")
	}
	if _, err = schedules.Handle(context.Background(), "schedule.runNow", map[string]any{"projectId": "other", "scheduleId": job.ID}); err == nil {
		t.Fatal("cross-project dispatch allowed")
	}
	if _, err = call("runNow", map[string]any{"scheduleId": job.ID}); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err = call("update", map[string]any{"scheduleId": job.ID, "paused": true}); err != nil {
		t.Fatalf("pause active schedule: %v", err)
	}
	if _, err = call("runNow", map[string]any{"scheduleId": job.ID}); err == nil {
		t.Fatal("overlapping run admitted")
	}
	if _, err = call("delete", map[string]any{"scheduleId": job.ID}); err == nil {
		t.Fatal("deleted running schedule")
	}
	_, _ = call("stop", map[string]any{"scheduleId": job.ID})
	schedules.Close(context.Background())
	restored, err := controller.NewSchedules(store, roots, runner)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close(context.Background())
	result, err = restored.Handle(context.Background(), "schedule.list", map[string]any{"projectId": "project"})
	if err != nil {
		t.Fatal(err)
	}
	jobs := result.([]controller.Schedule)
	if calls.Load() != 1 || len(jobs) != 1 || len(jobs[0].Runs) != 1 || jobs[0].Runs[0].SessionID != "native-session" || jobs[0].Runs[0].Status != "interrupted" {
		t.Fatalf("lost run state: %+v", jobs)
	}
}
func TestSchedulesClaimDueOccurrenceAndPauseAmbiguousRestart(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	past := time.Now().Add(-time.Hour)
	job := controller.Schedule{ID: "job", ProjectID: "project", Root: "/project", Prompt: "Review", Cron: "0 9 * * *", Timezone: "UTC", NextRun: past, Runs: []controller.ScheduleRun{}}
	if err := persist.Write(store, "schedules.json", map[string]controller.Schedule{"job": job}, nil); err != nil {
		t.Fatal(err)
	}
	ran := make(chan struct{}, 1)
	s, err := controller.NewSchedules(store, nil, func(ctx context.Context, j controller.Schedule, admitted func(string) error) error {
		var disk map[string]controller.Schedule
		if _, err := persist.Read(store, "schedules.json", &disk, nil); err != nil {
			return err
		}
		if !disk[j.ID].NextRun.After(past) || disk[j.ID].Runs[0].Status != "running" {
			return fmt.Errorf("occurrence not claimed")
		}
		if err := admitted("session"); err != nil {
			return err
		}
		ran <- struct{}{}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("due schedule not dispatched")
	}
	s.Close(context.Background())
	var disk map[string]controller.Schedule
	_, err = persist.Read(store, "schedules.json", &disk, nil)
	if err != nil {
		t.Fatal(err)
	}
	job = disk["job"]
	if job.Runs[0].Status != "completed" {
		t.Fatalf("completion not saved: %+v", job)
	}
	job.Runs[0].Status = "running"
	if err := persist.Write(store, "schedules.json", map[string]controller.Schedule{"job": job}, nil); err != nil {
		t.Fatal(err)
	}
	restored, err := controller.NewSchedules(store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close(context.Background())
	result, _ := restored.Handle(context.Background(), "schedule.list", map[string]any{"projectId": "project"})
	raw, _ := json.Marshal(result)
	got := result.([]controller.Schedule)[0]
	if !got.Paused || got.Runs[0].Status != "interrupted" {
		t.Fatalf("ambiguous run not paused: %s", raw)
	}
}

func TestScheduleRunsThroughTheApplicationSessionLifecycle(t *testing.T) {
	server, calls := sessionExtensionPi(t, nil)
	runtime, host, root := sessionExtensionRuntime(t, server.URL)
	defer runtime.Shutdown(context.Background())
	connection := dialRuntimeSocket(t, context.Background(), host, "schedule-owner")
	project := callBrowser(t, connection, "project", "project.open", map[string]any{"path": root})["result"].(map[string]any)
	created := callBrowser(t, connection, "create-schedule", "schedule.create", map[string]any{"projectId": project["id"], "root": root, "prompt": "Review this project", "cron": "0 9 * * *"})
	if created["ok"] != true {
		t.Fatalf("create: %#v", created)
	}
	job := created["result"].(map[string]any)
	started := callBrowser(t, connection, "run-schedule", "schedule.runNow", map[string]any{"projectId": project["id"], "scheduleId": job["id"]})
	if started["ok"] != true {
		t.Fatalf("run: %#v", started)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		response := callBrowser(t, connection, fmt.Sprintf("list-schedules-%d", time.Now().UnixNano()), "schedule.list", map[string]any{"projectId": project["id"]})
		jobs := response["result"].([]any)
		runs := jobs[0].(map[string]any)["runs"].([]any)
		if len(runs) > 0 && runs[0].(map[string]any)["status"] != "running" {
			run := runs[0].(map[string]any)
			if run["status"] != "completed" || run["sessionId"] != "chat" {
				t.Fatalf("run did not complete: %#v", run)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("schedule did not settle; jobs=%#v calls=%#v", jobs, calls.snapshot())
		}
		time.Sleep(10 * time.Millisecond)
	}
	foundCreate, foundPrompt := false, false
	for _, call := range calls.snapshot() {
		switch call.method {
		case "session.create":
			foundCreate = true
			if call.params["cwd"] != root {
				t.Fatalf("wrong scheduled root: %#v", call.params)
			}
		case "session.prompt":
			foundPrompt = true
		}
	}
	if !foundCreate || !foundPrompt {
		t.Fatalf("native session lifecycle not invoked: %#v", calls.snapshot())
	}
}

func TestScheduleExecutionLedgerRejectsStaleBackupRecovery(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	job := controller.Schedule{ID: "job", ProjectID: "p", Root: "/p", Prompt: "Task", Cron: "0 9 * * *", Timezone: "UTC", NextRun: time.Now(), Runs: []controller.ScheduleRun{}}
	if err := persist.Write(store, "schedules.json", map[string]controller.Schedule{"job": job}, nil); err != nil {
		t.Fatal(err)
	}
	if err := persist.Write(store, "schedules.json", map[string]controller.Schedule{"job": job}, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Dir, "schedules.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.NewSchedules(store, nil, nil); err == nil {
		t.Fatal("restored an older execution claim")
	}
	if err := os.Remove(filepath.Join(store.Dir, "schedules.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.NewSchedules(store, nil, nil); err == nil {
		t.Fatal("missing primary restored an older execution claim")
	}
}
