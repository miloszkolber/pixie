package controller_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
)

// AUX-19 internal drain: a follow-up held by compaction is durable but not
// dispatched. If the controller begins a drain before compaction ends, the
// settlement wakeup must be refused so no prompt enters the quiesce window, and
// the queued message must survive for a later recovery.
func TestQueuedFollowUpDoesNotDispatchAfterBeginDrain(t *testing.T) {
	prompts := make(chan map[string]any, 4)
	manager, _, project, _ := newSessionManager(t, nil, prompts)
	gate := controller.NewAdmissionGate(0)
	manager.SetAdmissionGate(gate)
	ctx := t.Context()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SessionUpdate(ctx, dialogUpdate(map[string]any{"sessionUpdate": "compaction_start"})); err != nil {
		t.Fatal(err)
	}
	if err := manager.Queue(ctx, "chat", "queued during drain"); err != nil {
		t.Fatalf("queue before drain: %v", err)
	}
	gate.BeginDrain()
	if err := manager.SessionUpdate(ctx, dialogUpdate(map[string]any{"sessionUpdate": "compaction_end"})); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-prompts:
		t.Fatalf("follow-up dispatched after the drain began: %#v", got)
	case <-time.After(150 * time.Millisecond):
	}
	snapshot, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client")
	if err != nil {
		t.Fatal(err)
	}
	summary, _ := snapshot["summary"].(controller.SessionSummary)
	if summary.Queue == nil || len(summary.Queue.FollowUp) != 1 {
		t.Fatalf("refused follow-up was not retained durably: %#v", summary.Queue)
	}
}

// AUX-19 internal drain: accepting a follow-up dispatch before BeginDrain must
// keep WaitForDrain blocked until the admitted run settles, not just until the
// browser handler returns. The admission is handed to the asynchronous native
// prompt, so the drain stays blocked while that prompt is still in flight and
// settles only after the run ends.
func TestWaitForDrainWaitsForAdmittedFollowUpRun(t *testing.T) {
	prompts := make(chan map[string]any, 4)
	loadEntered := make(chan struct{})
	loadRelease := make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	releaseLoad := func() { releaseOnce.Do(func() { close(loadRelease) }) }
	defer releaseLoad()
	observer := func(method string, _ map[string]any) {
		if method != "session.load" {
			return
		}
		enteredOnce.Do(func() { close(loadEntered) })
		<-loadRelease
	}
	manager, _, _, _ := newSessionManagerWithFixtureBehavior(t, nil, prompts, bunHostInitializeResponse(), nil, sessionFixtureBehavior{}, observer)
	gate := controller.NewAdmissionGate(0)
	manager.SetAdmissionGate(gate)
	ctx := t.Context()
	if err := manager.Queue(ctx, "chat", "admitted follow-up"); err != nil {
		t.Fatalf("queue: %v", err)
	}
	select {
	case <-loadEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("admitted follow-up did not reach its native load")
	}
	gate.BeginDrain()
	drained := make(chan struct{})
	go func() {
		gate.WaitForDrain(context.Background())
		close(drained)
	}()
	select {
	case <-drained:
		t.Fatal("WaitForDrain settled while an admitted follow-up run was still in flight")
	case <-time.After(50 * time.Millisecond):
	}
	releaseLoad()
	// The run has started but its native prompt is still in flight. The
	// handoff must keep the admission until that prompt settles.
	var request map[string]any
	select {
	case request = <-prompts:
	case <-time.After(2 * time.Second):
		t.Fatal("admitted follow-up did not dispatch its prompt")
	}
	select {
	case <-drained:
		t.Fatal("WaitForDrain settled while the asynchronous follow-up prompt was still in flight")
	case <-time.After(50 * time.Millisecond):
	}
	if err := writeRPC(request["connection"].(*websocket.Conn), map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"stopReason": "end_turn"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForDrain did not settle after the admitted follow-up run finished")
	}
	params, _ := request["params"].(map[string]any)
	if text := promptText(params); text != "admitted follow-up" {
		t.Fatalf("dispatched prompt text = %q, want the admitted follow-up", text)
	}
}
