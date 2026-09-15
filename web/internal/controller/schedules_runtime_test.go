package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

func runtimeTestSchedule(t *testing.T, runtime ScheduleRuntimePolicy, run ScheduleRunner) (*Schedules, Schedule) {
	t.Helper()
	schedules, err := NewSchedulesWithRuntime(persist.Store{Dir: t.TempDir()}, nil, run, runtime)
	if err != nil {
		t.Fatal(err)
	}
	created, err := schedules.Handle(t.Context(), "schedule.create", map[string]any{
		"projectId": "project", "root": "/project", "prompt": "Review", "cron": "0 9 * * *",
	})
	if err != nil {
		t.Fatal(err)
	}
	return schedules, created.(Schedule)
}

func runtimeTestRun(t *testing.T, schedules *Schedules, schedule Schedule) {
	t.Helper()
	if _, err := schedules.Handle(t.Context(), "schedule.runNow", map[string]any{"projectId": "project", "scheduleId": schedule.ID}); err != nil {
		t.Fatal(err)
	}
}

func runtimeTestJob(t *testing.T, schedules *Schedules) Schedule {
	t.Helper()
	value, err := schedules.Handle(t.Context(), "schedule.list", map[string]any{"projectId": "project"})
	if err != nil {
		t.Fatal(err)
	}
	return value.([]Schedule)[0]
}

func waitForScheduleRun(t *testing.T, schedules *Schedules, want string) ScheduleRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job := runtimeTestJob(t, schedules)
		if len(job.Runs) > 0 && job.Runs[0].Status == want {
			return job.Runs[0]
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("schedule run did not become %q: %+v", want, runtimeTestJob(t, schedules))
	return ScheduleRun{}
}

func TestScheduleDeadlinePersistsCancellingBeforeNativeCancel(t *testing.T) {
	started := make(chan struct{}, 1)
	observed := make(chan struct{}, 1)
	release := make(chan struct{})
	schedules, job := runtimeTestSchedule(t, ScheduleRuntimePolicy{DefaultMaxRuntime: time.Second, DeadlinesEnabled: true}, func(ctx context.Context, _ Schedule, admitted func(string) error) error {
		if err := admitted("native-deadline"); err != nil {
			return err
		}
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})
	defer schedules.Close(context.Background())
	schedules.SetCancellationHandler(func(context.Context, string) (ScheduleCancellationOutcome, error) {
		var disk scheduleDisk
		if _, err := persist.Read(persist.Store{Dir: schedules.store.Dir}, "schedules.json", &disk, nil); err != nil {
			t.Error(err)
		} else {
			run := disk.Jobs[job.ID].Runs[0]
			if disk.Version != scheduleLedgerVersion || !disk.Jobs[job.ID].Paused || run.Status != scheduleRunCancelling || run.CancellationReason != scheduleCancellationDeadline || run.CancellationRequestedAt == nil {
				t.Errorf("native cancellation was not durably admitted first: %+v", disk.Jobs[job.ID])
			}
		}
		observed <- struct{}{}
		<-release
		return ScheduleCancellationOutcome{Confirmed: true}, nil
	})
	runtimeTestRun(t, schedules, job)
	<-started
	schedules.Watchdog(time.Now().Add(2 * time.Second))
	select {
	case <-observed:
	case <-time.After(5 * time.Second):
		t.Fatal("deadline never reached the native cancellation boundary")
	}
	close(release)
	if run := waitForScheduleRun(t, schedules, scheduleRunTimedOut); run.FinishedAt == nil {
		t.Fatal("confirmed deadline cancellation has no finish time")
	}
}

func TestScheduleDeadlineDoesNotSignalPiWhenCancellingCannotPersist(t *testing.T) {
	started := make(chan struct{}, 1)
	var nativeCancels atomic.Int32
	schedules, job := runtimeTestSchedule(t, ScheduleRuntimePolicy{DefaultMaxRuntime: time.Second, DeadlinesEnabled: true}, func(ctx context.Context, _ Schedule, admitted func(string) error) error {
		if err := admitted("native-persist-failure"); err != nil {
			return err
		}
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})
	defer schedules.Close(context.Background())
	schedules.SetCancellationHandler(func(context.Context, string) (ScheduleCancellationOutcome, error) {
		nativeCancels.Add(1)
		return ScheduleCancellationOutcome{Confirmed: true}, nil
	})
	runtimeTestRun(t, schedules, job)
	<-started
	path := filepath.Join(schedules.store.Dir, "schedules.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	schedules.Watchdog(time.Now().Add(2 * time.Second))
	time.Sleep(20 * time.Millisecond)
	if nativeCancels.Load() != 0 {
		t.Fatal("deadline sent native cancellation after durable cancelling state failed")
	}
}

func TestScheduleStopRetriesReconciledIntentWithoutSignalingPiOnPersistFailure(t *testing.T) {
	started := make(chan struct{}, 1)
	var nativeCancels atomic.Int32
	schedules, job := runtimeTestSchedule(t, DefaultScheduleRuntimePolicy(), func(ctx context.Context, _ Schedule, admitted func(string) error) error {
		if err := admitted("native-retry"); err != nil {
			return err
		}
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})
	defer schedules.Close(context.Background())
	schedules.SetCancellationHandler(func(context.Context, string) (ScheduleCancellationOutcome, error) {
		nativeCancels.Add(1)
		return ScheduleCancellationOutcome{Confirmed: true}, nil
	})
	runtimeTestRun(t, schedules, job)
	<-started
	schedules.SetPublishFaults(persist.PublishFaults{FailDirSync: errors.New("injected dir-sync failure")})
	params := map[string]any{"projectId": "project", "scheduleId": job.ID, "mutationId": "stop-once"}
	if _, err := schedules.Handle(t.Context(), "schedule.stop", params); err == nil {
		t.Fatal("post-rename stop persistence failure must reach the caller")
	}
	if nativeCancels.Load() != 0 {
		t.Fatal("schedule.stop signaled Pi after its persistence failure")
	}
	if run := waitForScheduleRun(t, schedules, scheduleRunCancelling); run.CancellationRequestedAt == nil {
		t.Fatalf("reconciled cancellation intent is missing: %+v", run)
	}
	schedules.SetPublishFaults(persist.PublishFaults{})
	result, err := schedules.Handle(t.Context(), "schedule.stop", params)
	receipt, ok := result.(map[string]any)
	if err != nil || !ok || receipt["accepted"] != true {
		t.Fatalf("same mutation did not resume the durable cancellation intent: %#v %v", result, err)
	}
	waitForScheduleRun(t, schedules, scheduleRunInterrupted)
	if nativeCancels.Load() != 1 {
		t.Fatalf("expected one native cancellation after retry, got %d", nativeCancels.Load())
	}
	if _, err := schedules.Handle(t.Context(), "schedule.stop", params); err != nil {
		t.Fatal(err)
	}
	if nativeCancels.Load() != 1 {
		t.Fatal("a settled retry signaled Pi again")
	}
}

func TestScheduleCancellationConfirmationAndUncertaintyBlockDispatch(t *testing.T) {
	started := make(chan struct{}, 2)
	confirmed := true
	schedules, job := runtimeTestSchedule(t, DefaultScheduleRuntimePolicy(), func(ctx context.Context, _ Schedule, admitted func(string) error) error {
		if err := admitted("native-cancel"); err != nil {
			return err
		}
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})
	defer schedules.Close(context.Background())
	schedules.SetCancellationHandler(func(context.Context, string) (ScheduleCancellationOutcome, error) {
		if confirmed {
			return ScheduleCancellationOutcome{Confirmed: true}, nil
		}
		return ScheduleCancellationOutcome{Confirmed: false}, errors.New("native cancellation lost")
	})
	runtimeTestRun(t, schedules, job)
	<-started
	result, err := schedules.Handle(t.Context(), "schedule.stop", map[string]any{"projectId": "project", "scheduleId": job.ID})
	if err != nil || !result.(scheduleStopResult).Accepted || result.(scheduleStopResult).Settled {
		t.Fatalf("stop must report accepted but not settled: %#v %v", result, err)
	}
	if run := waitForScheduleRun(t, schedules, scheduleRunInterrupted); run.FinishedAt == nil {
		t.Fatal("confirmed manual cancellation has no finish time")
	}

	confirmed = false
	runtimeTestRun(t, schedules, job)
	<-started
	if _, err := schedules.Handle(t.Context(), "schedule.stop", map[string]any{"projectId": "project", "scheduleId": job.ID}); err != nil {
		t.Fatal(err)
	}
	run := waitForScheduleRun(t, schedules, scheduleRunCancellationUnconfirmed)
	if run.FinishedAt != nil || run.CancellationRequestedAt == nil {
		t.Fatalf("uncertain cancellation became terminal: %+v", run)
	}
	for _, method := range []string{"schedule.runNow", "schedule.update", "schedule.delete"} {
		params := map[string]any{"projectId": "project", "scheduleId": job.ID}
		if method == "schedule.update" {
			params["prompt"] = "Changed"
		}
		if _, err := schedules.Handle(t.Context(), method, params); err == nil {
			t.Fatalf("%s dispatched while cancellation was unconfirmed", method)
		}
	}
	confirmed = true
	if _, err := schedules.Handle(t.Context(), "schedule.stop", map[string]any{"projectId": "project", "scheduleId": job.ID}); err != nil {
		t.Fatal(err)
	}
	waitForScheduleRun(t, schedules, scheduleRunInterrupted)
}

func TestScheduleDeadlineKeepsLateNativeCreateUntilLinkAndReleaseReconcile(t *testing.T) {
	createReceived := make(chan struct{}, 1)
	releaseCreate := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCreate) }) }
	var prompts atomic.Int32
	var releases atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		for {
			_, payload, err := connection.Read(context.Background())
			if err != nil {
				return
			}
			var rpc struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if json.Unmarshal(payload, &rpc) != nil {
				return
			}
			result := any(map[string]any{})
			switch rpc.Method {
			case "runtime.hello":
				result = map[string]any{
					"protocolVersion": 1,
					"runtimeId":       "late-create-host",
					"bootId":          "boot",
					"version":         "test",
					"capabilities":    map[string]any{"sessions": 1},
					"operationSet": map[string]bool{
						"session.list": true, "session.create": true, "session.load": true,
						"session.prompt": true, "session.cancel": true, "session.release": true,
					},
				}
			case "session.create":
				createReceived <- struct{}{}
				<-releaseCreate
				result = map[string]any{"sessionId": "late-native", "configOptions": []any{}, "capabilities": map[string]any{"sessions": 1}}
			case "session.prompt":
				prompts.Add(1)
				result = map[string]any{"stopReason": "complete"}
			case "session.release":
				releases.Add(1)
			}
			encoded, err := json.Marshal(map[string]any{"id": rpc.ID, "result": result})
			if err != nil || connection.Write(context.Background(), websocket.MessageText, encoded) != nil {
				return
			}
		}
	}))
	defer server.Close()

	root := t.TempDir()
	store := persist.Store{Dir: t.TempDir()}
	policy, err := workspace.NewPathPolicy([]string{root}, false)
	if err != nil {
		t.Fatal(err)
	}
	projects := workspace.NewProjects(store, policy)
	project, err := projects.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewSessionManager(projects, policy, NewSessionRecords(store), NewSessionQueues(store), NewObjectives(store), nil)
	client := NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", manager)
	manager.SetClient(client)
	defer client.Close()
	schedules, err := NewSchedulesWithRuntime(store, projects.AssertRoot, manager.runSchedule, ScheduleRuntimePolicy{DefaultMaxRuntime: time.Second, DeadlinesEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	schedules.SetCancellationHandler(manager.cancelScheduleRun)
	defer schedules.Close(context.Background())
	defer release()
	created, err := schedules.Handle(t.Context(), "schedule.create", map[string]any{"projectId": project.ID, "root": root, "prompt": "Review", "cron": "0 9 * * *"})
	if err != nil {
		t.Fatal(err)
	}
	job := created.(Schedule)
	currentRun := func() ScheduleRun {
		t.Helper()
		schedules.mu.Lock()
		defer schedules.mu.Unlock()
		return schedules.jobs[job.ID].Runs[0]
	}
	waitRun := func(want string) ScheduleRun {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			run := currentRun()
			if run.Status == want {
				return run
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("schedule run did not become %q: %+v", want, currentRun())
		return ScheduleRun{}
	}
	if _, err := schedules.Handle(t.Context(), "schedule.runNow", map[string]any{"projectId": project.ID, "scheduleId": job.ID}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-createReceived:
	case <-time.After(5 * time.Second):
		t.Fatal("host did not receive session.create")
	}

	// The deadline cancels the scheduling context after session.create is on the
	// wire. The Pi client must keep draining that reply instead of terminalizing
	// the no-ID run.
	schedules.Watchdog(time.Now().Add(2 * time.Second))
	if run := waitRun(scheduleRunCancelling); run.SessionID != "" || !run.CreationPending {
		t.Fatalf("deadline did not retain the pending native creation: %+v", run)
	}
	schedules.SetPublishFaults(persist.PublishFaults{FailPrimary: errors.New("link persistence failed")})
	release()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		schedules.mu.Lock()
		running := schedules.running[job.ID] != nil
		schedules.mu.Unlock()
		if !running {
			break
		}
		time.Sleep(time.Millisecond)
	}
	schedules.mu.Lock()
	running := schedules.running[job.ID] != nil
	schedules.mu.Unlock()
	if running {
		t.Fatal("schedule runner did not return after the delayed create reply")
	}
	uncertain := currentRun()
	if uncertain.Status != scheduleRunCancelling || uncertain.SessionID != "" || !uncertain.CreationPending || uncertain.FinishedAt != nil {
		t.Fatalf("failed schedule link became terminal or lost its creation claim: %+v", uncertain)
	}
	if _, err := schedules.Handle(t.Context(), "schedule.runNow", map[string]any{"projectId": project.ID, "scheduleId": job.ID}); err == nil {
		t.Fatal("duplicate schedule run was admitted while native creation was unresolved")
	}

	// Repairing storage only reconciles the returned ID; the deadline remains
	// nonterminal until the exact native session has been released or stopped.
	schedules.SetPublishFaults(persist.PublishFaults{})
	schedules.Watchdog(time.Now().Add(31 * time.Second))
	run := waitRun(scheduleRunTimedOut)
	if run.SessionID != "late-native" || run.CreationPending || run.FinishedAt == nil {
		t.Fatalf("deadline terminalized without a durable native release: %+v", run)
	}
	if releases.Load() < 2 {
		t.Fatalf("expected partial-create cleanup and reconciled release, got %d releases", releases.Load())
	}
	if prompts.Load() != 0 {
		t.Fatalf("schedule prompted native work after its link persistence failed: %d", prompts.Load())
	}
}

func TestScheduleV1MigrationKeepsUnlimitedBudgetsAndFailsUnknownVersions(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	legacy := Schedule{ID: "legacy", ProjectID: "project", Root: "/project", Prompt: "Review", Cron: "0 9 * * *", Timezone: "UTC", NextRun: time.Now().Add(time.Hour), Runs: []ScheduleRun{}}
	if err := persist.Write(store, "schedules.json", scheduleDisk{Version: 1, Jobs: map[string]Schedule{legacy.ID: legacy}, Operations: []scheduleOperation{}}, nil); err != nil {
		t.Fatal(err)
	}
	schedules, err := NewSchedules(store, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer schedules.Close(context.Background())
	job := runtimeTestJob(t, schedules)
	if job.MaxRuntimeSeconds != nil {
		t.Fatalf("v1 schedule gained a budget: %+v", job)
	}
	raw, _, err := persist.ReadFile(schedules.store.Dir + "/schedules.json")
	if err != nil || !json.Valid(raw) || !jsonContainsVersion(raw, scheduleLedgerVersion) {
		t.Fatalf("v1 ledger was not durably migrated to v2: %s %v", raw, err)
	}
	if err := persist.Write(store, "schedules.json", scheduleDisk{Version: 99, Jobs: map[string]Schedule{}, Operations: []scheduleOperation{}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSchedules(store, nil, nil); err == nil {
		t.Fatal("unknown v2+ ledger version was accepted")
	}
}

func jsonContainsVersion(raw []byte, want int) bool {
	var disk scheduleDisk
	return json.Unmarshal(raw, &disk) == nil && disk.Version == want
}

func TestScheduleRuntimeDefaultsAndDeadlineSwitch(t *testing.T) {
	policy, err := ParseScheduleRuntimePolicy(func(string) string { return "" })
	if err != nil || policy.DefaultMaxRuntime != defaultScheduleMaxRuntime || !policy.DeadlinesEnabled {
		t.Fatalf("default policy: %+v %v", policy, err)
	}
	off, err := ParseScheduleRuntimePolicy(func(name string) string {
		if name == "PIXIE_SCHEDULE_DEFAULT_MAX_RUN" || name == "PIXIE_SCHEDULE_DEADLINES" {
			return "off"
		}
		return ""
	})
	if err != nil || off.DefaultMaxRuntime != 0 || off.DeadlinesEnabled {
		t.Fatalf("off policy: %+v %v", off, err)
	}
	configured, err := ParseScheduleRuntimePolicy(func(name string) string {
		if name == "PIXIE_SCHEDULE_DEFAULT_MAX_RUN" {
			return "90m"
		}
		return ""
	})
	if err != nil || configured.DefaultMaxRuntime != 90*time.Minute || !configured.DeadlinesEnabled {
		t.Fatalf("configured policy: %+v %v", configured, err)
	}
	schedules, job := runtimeTestSchedule(t, policy, nil)
	defer schedules.Close(context.Background())
	if job.MaxRuntimeSeconds == nil || *job.MaxRuntimeSeconds != int64(defaultScheduleMaxRuntime/time.Second) {
		t.Fatalf("new schedule missed default budget: %+v", job)
	}
	if _, err := schedules.Handle(t.Context(), "schedule.update", map[string]any{"projectId": "project", "scheduleId": job.ID, "maxRuntimeSeconds": "off"}); err != nil {
		t.Fatal(err)
	}
	if runtimeTestJob(t, schedules).MaxRuntimeSeconds != nil {
		t.Fatal("explicit off did not persist unlimited runtime")
	}
	configuredSchedules, configuredJob := runtimeTestSchedule(t, configured, nil)
	defer configuredSchedules.Close(context.Background())
	if configuredJob.MaxRuntimeSeconds == nil || *configuredJob.MaxRuntimeSeconds != 90*60 {
		t.Fatalf("configured default was not persisted on a new schedule: %+v", configuredJob)
	}
	deadlineCancel := make(chan struct{}, 1)
	offStarted := make(chan struct{}, 1)
	offSchedules, offJob := runtimeTestSchedule(t, ScheduleRuntimePolicy{DefaultMaxRuntime: time.Second, DeadlinesEnabled: false}, func(ctx context.Context, _ Schedule, admitted func(string) error) error {
		if err := admitted("native-deadlines-off"); err != nil {
			return err
		}
		offStarted <- struct{}{}
		<-ctx.Done()
		deadlineCancel <- struct{}{}
		return ctx.Err()
	})
	defer offSchedules.Close(context.Background())
	runtimeTestRun(t, offSchedules, offJob)
	<-offStarted
	before, _, err := persist.ReadFile(filepath.Join(offSchedules.store.Dir, "schedules.json"))
	if err != nil {
		t.Fatal(err)
	}
	offSchedules.Watchdog(time.Now().Add(2 * time.Second))
	select {
	case <-deadlineCancel:
		t.Fatal("deadlines-off watchdog canceled a run")
	case <-time.After(20 * time.Millisecond):
	}
	after, _, err := persist.ReadFile(filepath.Join(offSchedules.store.Dir, "schedules.json"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("deadlines-off watchdog rewrote the ledger: %v", err)
	}
}
