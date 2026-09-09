package controller_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
)

func waitPromptRequest(t *testing.T, requests chan map[string]any) map[string]any {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(5 * time.Second):
		t.Fatal("prompt was not dispatched")
		return nil
	}
}

func TestStopFreezesDispatchAndRetainsPausedOutbox(t *testing.T) {
	events := make(chan publishedEvent, 64)
	requests := make(chan map[string]any, 4)
	manager, _, project, store := newSessionManagerWithInitializeAndPublisher(t, nil, requests, piInitializeResponse(), func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	drainEvents(events)
	if err := manager.Prompt(ctx, "chat", "first", nil, nil); err != nil {
		t.Fatal(err)
	}
	first := waitPromptRequest(t, requests)
	if err := manager.Queue(ctx, "chat", "next"); err != nil {
		t.Fatal(err)
	}
	stopResult := make(chan struct{})
	var outcome controller.StopOutcome
	var stopErr error
	go func() {
		defer close(stopResult)
		outcome, stopErr = manager.Stop(ctx, "chat")
	}()
	stopping := nextAgentEventOfType(t, events, "stopping")
	if stopping == nil {
		t.Fatal("missing stopping event")
	}
	if err := writeRPC(first["connection"].(*websocket.Conn), map[string]any{
		"jsonrpc": "2.0", "id": first["id"], "result": map[string]any{"stopReason": "end_turn"},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopResult:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not verify quiescence")
	}
	if stopErr != nil {
		t.Fatalf("Stop returned error for a clean abort: %v", stopErr)
	}
	if outcome.Status != controller.StopStatusStopped {
		t.Fatalf("Stop status = %q, want stopped (reason %q)", outcome.Status, outcome.Reason)
	}
	if outcome.RetainedPaused != 1 {
		t.Fatalf("retained paused = %d, want 1", outcome.RetainedPaused)
	}
	stopped := nextAgentEventOfType(t, events, "stopped")
	if stopped == nil {
		t.Fatal("missing stopped event")
	}
	select {
	case second := <-requests:
		t.Fatalf("Stop auto-resumed a paused prompt: %#v", second)
	case <-time.After(150 * time.Millisecond):
	}
	queue, found, err := controller.NewSessionQueues(store).Get(project.ID, "chat")
	if err != nil || !found {
		t.Fatalf("paused outbox was not retained: found=%v err=%v", found, err)
	}
	if len(queue.FollowUp) != 1 || queue.FollowUp[0].Text != "next" {
		t.Fatalf("paused follow-up: %#v", queue.FollowUp)
	}
	if queue.Dispatch != nil || queue.Blocked != nil {
		t.Fatalf("Stop left a dispatch behind: %#v", queue)
	}
}

func TestStopQuiescenceUncertainOnTimeout(t *testing.T) {
	events := make(chan publishedEvent, 64)
	requests := make(chan map[string]any, 4)
	manager, _, project, _ := newSessionManagerWithInitializeAndPublisher(t, nil, requests, piInitializeResponse(), func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	drainEvents(events)
	if err := manager.Prompt(ctx, "chat", "hangs", nil, nil); err != nil {
		t.Fatal(err)
	}
	_ = waitPromptRequest(t, requests)
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer shortCancel()
	outcome, err := manager.Stop(shortCtx, "chat")
	if err == nil {
		t.Fatal("bounded Stop did not report an error for an unverifiable abort")
	}
	if outcome.Status != controller.StopStatusUncertain {
		t.Fatalf("Stop status = %q, want uncertain", outcome.Status)
	}
	if outcome.Reason == "" {
		t.Fatal("uncertain Stop reported no reason")
	}
	uncertain := nextAgentEventOfType(t, events, "stop_uncertain")
	if uncertain == nil {
		t.Fatal("missing stop_uncertain event")
	}
}

func TestOutboxDrainStaysPausedAfterStop(t *testing.T) {
	events := make(chan publishedEvent, 64)
	requests := make(chan map[string]any, 4)
	manager, _, project, _ := newSessionManagerWithInitializeAndPublisher(t, nil, requests, piInitializeResponse(), func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	drainEvents(events)
	if err := manager.Prompt(ctx, "chat", "first", nil, nil); err != nil {
		t.Fatal(err)
	}
	first := waitPromptRequest(t, requests)
	if err := manager.Queue(ctx, "chat", "paused-one"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Queue(ctx, "chat", "paused-two"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = manager.Stop(ctx, "chat")
	}()
	nextAgentEventOfType(t, events, "stopping")
	if err := writeRPC(first["connection"].(*websocket.Conn), map[string]any{
		"jsonrpc": "2.0", "id": first["id"], "result": map[string]any{"stopReason": "end_turn"},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not finish")
	}
	nextAgentEventOfType(t, events, "stopped")
	select {
	case extra := <-requests:
		t.Fatalf("drain resumed paused outbox automatically: %#v", extra)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestIdleReleaseOnlyForEligibleSettledResidents(t *testing.T) {
	events := make(chan publishedEvent, 64)
	requests := make(chan map[string]any, 4)
	manager, _, project, _ := newSessionManagerWithInitializeAndPublisher(t, nil, requests, piInitializeResponse(), func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	drainEvents(events)
	if eligible, reason := manager.IdleReleaseEligible("chat"); !eligible {
		t.Fatalf("idle session not eligible: %s", reason)
	}
	if err := manager.Prompt(ctx, "chat", "running", nil, nil); err != nil {
		t.Fatal(err)
	}
	running := waitPromptRequest(t, requests)
	if eligible, _ := manager.IdleReleaseEligible("chat"); eligible {
		t.Fatal("running session reported idle-release eligible")
	}
	if err := manager.ReleaseIdleRuntime(ctx, "chat"); err == nil || !strings.Contains(err.Error(), "idle") && !strings.Contains(err.Error(), "running") && !strings.Contains(err.Error(), "busy") {
		t.Fatalf("idle release freed a running resident: %v", err)
	}
	if err := writeRPC(running["connection"].(*websocket.Conn), map[string]any{
		"jsonrpc": "2.0", "id": running["id"], "result": map[string]any{"stopReason": "end_turn"},
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if eligible, _ := manager.IdleReleaseEligible("chat"); eligible {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("settled session never became idle-release eligible")
		}
		time.Sleep(time.Millisecond)
	}
	unregister := manager.RegisterSessionLiveness("chat", "test-pin", func() bool { return true })
	if eligible, _ := manager.IdleReleaseEligible("chat"); eligible {
		t.Fatal("liveness-pinned session reported idle-release eligible")
	}
	if err := manager.ReleaseIdleRuntime(ctx, "chat"); err == nil {
		t.Fatal("idle release evicted a pinned resident")
	}
	unregister()
	if err := manager.Queue(ctx, "chat", "pending"); err != nil {
		t.Fatal(err)
	}
	if eligible, _ := manager.IdleReleaseEligible("chat"); eligible {
		t.Fatal("queued session reported idle-release eligible")
	}
	if err := manager.ReleaseIdleRuntime(ctx, "chat"); err == nil {
		t.Fatal("idle release evicted a session with pending outbox")
	}
}

func TestIdleReleaseRetainsHistoryAndReloads(t *testing.T) {
	manager, _, project, store := newSessionManagerWithPublisher(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snapshot, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot["messages"] == nil {
		t.Fatal("snapshot has no messages")
	}
	if eligible, reason := manager.IdleReleaseEligible("chat"); !eligible {
		t.Fatalf("idle session not eligible: %s", reason)
	}
	if err := manager.ReleaseIdleRuntime(ctx, "chat"); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.NewSessionRecords(store).List(); err != nil {
		t.Fatal(err)
	}
	cwd, err := manager.RecordedCWD(project.ID, "chat")
	if err != nil || cwd == "" {
		t.Fatalf("durable association was not retained: %q %v", cwd, err)
	}
	reloaded, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a")
	if err != nil {
		t.Fatalf("history did not reload after idle release: %v", err)
	}
	if reloaded["messages"] == nil {
		t.Fatal("reloaded snapshot has no messages")
	}
}

func TestArchiveDeleteNeverImplyStop(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	events := make(chan publishedEvent, 64)
	requests := make(chan map[string]any, 4)
	manager, _, project, _ := newSessionManagerWithInitializeAndPublisher(t, nil, requests, piInitializeResponse(), func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	}, func(method string, _ map[string]any) {
		mu.Lock()
		methods = append(methods, method)
		mu.Unlock()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	drainEvents(events)
	if err := manager.Prompt(ctx, "chat", "running", nil, nil); err != nil {
		t.Fatal(err)
	}
	first := waitPromptRequest(t, requests)
	if err := manager.Archive(ctx, project.ID, "chat", project.Roots[0]); err == nil || (!strings.Contains(err.Error(), "stop the running chat") && !strings.Contains(err.Error(), "wait for the chat")) {
		t.Fatalf("archive did not require an explicit stop first: %v", err)
	}
	mu.Lock()
	for _, method := range methods {
		if method == "session.cancel" {
			mu.Unlock()
			t.Fatal("archive implied Stop via session.cancel")
		}
	}
	mu.Unlock()
	if err := writeRPC(first["connection"].(*websocket.Conn), map[string]any{
		"jsonrpc": "2.0", "id": first["id"], "result": map[string]any{"stopReason": "end_turn"},
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if eligible, _ := manager.IdleReleaseEligible("chat"); eligible {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("session never settled after prompt completion")
		}
		time.Sleep(time.Millisecond)
	}
	drainEvents(events)
	mu.Lock()
	methods = nil
	mu.Unlock()
	if err := manager.Archive(ctx, project.ID, "chat", project.Roots[0]); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, method := range methods {
		if method == "session.cancel" {
			t.Fatal("archive implied Stop via session.cancel")
		}
	}
}
