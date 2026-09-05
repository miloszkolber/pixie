package controller_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
)

func todoPlanUpdates(t *testing.T, events []map[string]any) []map[string]any {
	t.Helper()
	var filter []map[string]any
	for _, update := range events {
		if update["sessionUpdate"] == "plan" {
			filter = append(filter, update)
		}
	}
	return filter
}

func waitTodoPlans(t *testing.T, sink *nativeProjection, count int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		sink.mu.Lock()
		plans := todoPlanUpdates(t, sink.updates)
		sink.mu.Unlock()
		if len(plans) >= count {
			return plans
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d plan updates, have %d", count, len(plans))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUpstreamTodoResultsProjectOntoPlan(t *testing.T) {
	todoDetails := map[string]any{
		"action": "update",
		"params": map[string]any{"action": "update", "id": 1},
		"tasks": []any{
			map[string]any{"id": 1.0, "subject": "Write tests", "status": "in_progress", "metadata": map[string]any{"priority": "high"}},
			map[string]any{"id": 2.0, "subject": "Ship it", "status": "pending", "blockedBy": []any{1.0}},
			map[string]any{"id": 3.0, "subject": "Old step", "status": "deleted"},
			map[string]any{"id": 4.0, "subject": "Unprioritized", "status": "pending"},
		},
		"nextId": 5.0,
	}
	legacyDetails := map[string]any{
		"plan": map[string]any{"entries": []any{
			map[string]any{"content": "Legacy plan", "priority": "medium", "status": "pending"},
		}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		push := func(event map[string]any) {
			_ = writeRPC(ws, map[string]any{"method": "session.event", "params": map[string]any{"sessionId": "todo-session", "event": event}})
		}
		push(map[string]any{"type": "tool_execution_end", "toolCallId": "todo-call", "result": map[string]any{"content": []any{}, "details": todoDetails}})
		// No nextId: not a todo envelope, so no plan may follow.
		push(map[string]any{"type": "tool_execution_end", "toolCallId": "other-call", "result": map[string]any{"content": []any{}, "details": map[string]any{"tasks": []any{}}}})
		// Legacy update_plan envelopes keep projecting unchanged.
		push(map[string]any{"type": "tool_execution_end", "toolCallId": "legacy-call", "result": map[string]any{"content": []any{}, "details": legacyDetails}})
		for {
			_, raw, err := ws.Read(r.Context())
			if err != nil {
				return
			}
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(raw, &req) != nil {
				return
			}
			if writeRPC(ws, map[string]any{"id": req.ID, "result": piInitializeResponse()}) != nil {
				return
			}
		}
	}))
	defer server.Close()
	sink := &nativeProjection{}
	client := controller.NewPiClient("ws"+server.URL[len("http"):], "test-secret", "test", sink)
	defer client.Close()
	if _, err := client.CallPi(t.Context(), "pi.providers.list", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	plans := waitTodoPlans(t, sink, 2)
	// The tasks-without-nextId envelope must not project; settle briefly and
	// require the count to stay at legacy + upstream.
	time.Sleep(200 * time.Millisecond)
	sink.mu.Lock()
	if extra := todoPlanUpdates(t, sink.updates); len(extra) != 2 {
		sink.mu.Unlock()
		t.Fatalf("unexpected plan updates: %d", len(extra))
	}
	sink.mu.Unlock()
	first := plans[0]
	entries, ok := first["entries"].([]any)
	if !ok || len(entries) != 3 {
		t.Fatalf("todo plan entries lost: %#v", first["entries"])
	}
	pending := entries[1].(map[string]any)
	if pending["content"] != "Ship it" || pending["priority"] != "medium" || pending["status"] != "pending" || pending["id"] != 2.0 {
		t.Fatalf("todo dependency entry lost: %#v", pending)
	}
	if blocked, ok := pending["blockedBy"].([]any); !ok || len(blocked) != 1 || blocked[0] != 1.0 {
		t.Fatalf("todo dependencies lost: %#v", pending["blockedBy"])
	}
	if entries[0].(map[string]any)["priority"] != "high" {
		t.Fatalf("todo metadata priority lost: %#v", entries[0])
	}
	if entries[2].(map[string]any)["content"] != "Unprioritized" {
		t.Fatalf("todo default priority lost: %#v", entries[2])
	}
	second := plans[1]
	legacy, ok := second["entries"].([]any)
	if !ok || len(legacy) != 1 || legacy[0].(map[string]any)["content"] != "Legacy plan" {
		t.Fatalf("legacy plan envelope changed: %#v", second["entries"])
	}
}

func TestUpstreamTodoReplayProjectsOntoPlan(t *testing.T) {
	toolMessage := map[string]any{
		"role":       "toolResult",
		"toolCallId": "todo-replay",
		"toolName":   "todo",
		"content":    []any{map[string]any{"type": "text", "text": "Updated #1"}},
		"details":    map[string]any{"action": "update", "params": map[string]any{}, "tasks": []any{map[string]any{"id": 1.0, "subject": "Reloaded task", "status": "completed", "metadata": map[string]any{"priority": "low"}}}, "nextId": 2.0},
		"messageId":  "tool-todo-1",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		for {
			_, raw, err := ws.Read(r.Context())
			if err != nil {
				return
			}
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(raw, &req) != nil {
				return
			}
			result := any(piInitializeResponse())
			if req.Method == "session.load" {
				result = map[string]any{"sessionId": "todo-replay-session", "messages": []any{toolMessage}, "commands": []any{}}
			}
			if writeRPC(ws, map[string]any{"id": req.ID, "result": result}) != nil {
				return
			}
		}
	}))
	defer server.Close()
	sink := &nativeProjection{}
	client := controller.NewPiClient("ws"+server.URL[len("http"):], "test-secret", "test", sink)
	defer client.Close()
	if _, err := client.CallPi(t.Context(), "session.load", map[string]any{"sessionId": "todo-replay-session"}); err != nil {
		t.Fatal(err)
	}
	plans := waitTodoPlans(t, sink, 1)
	entries, ok := plans[0]["entries"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("replayed todo plan lost: %#v", plans[0]["entries"])
	}
	entry := entries[0].(map[string]any)
	if entry["content"] != "Reloaded task" || entry["status"] != "completed" || entry["priority"] != "low" {
		t.Fatalf("replayed todo entry wrong: %#v", entry)
	}
}
