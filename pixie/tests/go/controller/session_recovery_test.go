package controller_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
	piwire "github.com/miloszkolber/pixie/internal/piprotocol"
)

func TestAbortWaitsForPromptSettlement(t *testing.T) {
	requests := make(chan map[string]any, 2)
	manager, _, project, _ := newSessionManager(t, nil, requests)
	if _, err := manager.Messages(t.Context(), "chat", project.ID, project.Roots[0], "test"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Prompt(t.Context(), "chat", "first", nil, nil); err != nil {
		t.Fatal(err)
	}
	request := <-requests
	done := make(chan error, 1)
	go func() { done <- manager.Abort(t.Context(), "chat") }()
	select {
	case err := <-done:
		t.Fatalf("abort acknowledged before prompt settlement: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := writeRPC(request["connection"].(*websocket.Conn), map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"stopReason": "cancelled"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("abort did not settle")
	}
	if err := manager.Prompt(t.Context(), "chat", "replacement", nil, nil); err != nil {
		t.Fatalf("replacement rejected after acknowledged abort: %v", err)
	}
	second := <-requests
	_ = writeRPC(second["connection"].(*websocket.Conn), map[string]any{"jsonrpc": "2.0", "id": second["id"], "result": map[string]any{"stopReason": "end_turn"}})
}

func TestAbortWaitHonorsCallerDeadline(t *testing.T) {
	requests := make(chan map[string]any, 1)
	manager, _, project, _ := newSessionManager(t, nil, requests)
	if _, err := manager.Messages(t.Context(), "chat", project.ID, project.Roots[0], "test"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Prompt(t.Context(), "chat", "first", nil, nil); err != nil {
		t.Fatal(err)
	}
	request := <-requests
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err := manager.Abort(ctx, "chat"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("abort deadline: %v", err)
	}
	_ = writeRPC(request["connection"].(*websocket.Conn), map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"stopReason": "cancelled"}})
}

func TestQuestionRequestCancellationReleasesPendingReply(t *testing.T) {
	args := map[string]any{"questions": []any{map[string]any{"question": "Choose", "header": "Choice", "options": []any{map[string]any{"label": "One", "description": "First"}}}}}
	manager, _, project, _ := newSessionManager(t, []map[string]any{{"sessionUpdate": "tool_call", "toolCallId": "question", "title": "ask_user_question", "status": "pending", "rawInput": args}}, nil)
	if _, err := manager.Messages(t.Context(), "chat", project.ID, project.Roots[0], "test"); err != nil {
		t.Fatal(err)
	}
	emitLiveQuestion(t, manager, "question", args)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	result, err := manager.AskQuestion(ctx, "chat", args)
	if err != nil || result["cancelled"] != true {
		t.Fatalf("request cancellation: %#v %v", result, err)
	}
	if err := manager.ResolveQuestion("chat", "question", map[string]any{"answers": []any{}, "cancelled": true}); err == nil {
		t.Fatal("canceled question still accepts answers")
	}
}

func TestSignetSettingsAttachMemoryThroughStandardSessionMCP(t *testing.T) {
	loaded := make(chan map[string]any, 1)
	manager, _, project, store := newSessionManagerWithInitializeAndPublisher(t, nil, nil, piInitializeResponse(), nil, func(method string, params map[string]any) {
		if method == "session.load" {
			loaded <- params
		}
	})
	settings := controller.NewSettings(store, nil)
	enabled := true
	if _, err := settings.Update(controller.AppConfigPatch{Signet: &controller.SignetPatch{Enabled: &enabled}}); err != nil {
		t.Fatal(err)
	}
	manager.SetSettings(settings)
	if _, err := manager.Messages(t.Context(), "chat", project.ID, project.Roots[0], "test"); err != nil {
		t.Fatal(err)
	}
	params := <-loaded
	for _, raw := range params["mcpServers"].([]any) {
		server := raw.(map[string]any)
		if server["name"] == "signet" && server["url"] == "http://127.0.0.1:3850/mcp" && server["type"] == "http" {
			return
		}
	}
	t.Fatalf("Signet MCP missing: %#v", params)
}

func emitLiveQuestion(t *testing.T, manager *controller.SessionManager, id string, args map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"sessionId": "chat", "update": map[string]any{"sessionUpdate": "tool_call", "toolCallId": id, "title": "ask_user_question", "status": "pending", "rawInput": args}})
	var notification piwire.SessionNotification
	if err := json.Unmarshal(raw, &notification); err != nil {
		t.Fatal(err)
	}
	if err := manager.SessionUpdate(t.Context(), notification); err != nil {
		t.Fatal(err)
	}
}

func TestQuestionCannotBindHistoricalUnfinishedTool(t *testing.T) {
	args := map[string]any{"questions": []any{map[string]any{"question": "Choose", "header": "Choice", "options": []any{map[string]any{"label": "One", "description": "First"}}}}}
	manager, _, project, _ := newSessionManager(t, []map[string]any{{"sessionUpdate": "tool_call", "toolCallId": "historical", "title": "ask_user_question", "status": "pending", "rawInput": args}}, nil)
	if _, err := manager.Messages(t.Context(), "chat", project.ID, project.Roots[0], "test"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := manager.AskQuestion(ctx, "chat", args); err == nil {
		t.Fatal("historical tool accepted a live question")
	}
}
