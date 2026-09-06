package controller_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/miloszkolber/pixie/internal/controller"
)

func TestObjectiveQuestionProxyCallsKeepQuestionPresentation(t *testing.T) {
	for _, args := range []map[string]any{
		{"server": "pixie_objectives", "tool": "ask_user_question", "args": map[string]any{"question": "Which?"}},
		{"tool": "pixie_objectives_ask_user_question", "args": `{"question":"Which?"}`},
	} {
		manager, _, project, _ := newSessionManager(t, []map[string]any{{"__native": map[string]any{"type": "tool_execution_start", "toolCallId": "question", "toolName": "mcp", "args": args}}}, nil)
		snapshot, err := manager.Messages(t.Context(), "chat", project.ID, project.Roots[0], "owner")
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(snapshot)
		if !strings.Contains(string(encoded), `"toolName":"ask_user_question"`) || !strings.Contains(string(encoded), `"question":"Which?"`) {
			t.Fatalf("question proxy was not projected: %s", encoded)
		}
	}
}

func TestAppCallsSelectHostOriginOnlyAfterAttachmentAndServerAuthorization(t *testing.T) {
	for _, nativeAppAPI := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-compatible-host", true: "app-origin-host"}[nativeAppAPI], func(t *testing.T) {
			initialize := piInitializeResponse()
			if nativeAppAPI {
				initialize["capabilities"].(map[string]any)["mcp-app-tools"] = 1
			}
			app := controller.AppAttachment{ToolName: "show", ExtensionName: "appserver", ResourceURI: "ui://fixture/unlisted"}
			updates := []map[string]any{{"__native": map[string]any{
				"type": "tool_execution_end", "toolCallId": "app-call", "toolName": "mcp",
				"result": map[string]any{"content": []any{}, "details": map[string]any{"mcp": map[string]any{"app": map[string]any{"toolName": app.ToolName, "extensionName": app.ExtensionName, "resourceUri": app.ResourceURI, "toolNameIsActual": true}}}},
			}}}
			var mu sync.Mutex
			var calls []sessionExtensionCall
			manager, _, project, _ := newSessionManagerWithInitializeAndPublisher(t, updates, nil, initialize, nil, func(method string, params map[string]any) {
				if method == "pi.apps.tools.call" || method == "pi.tools.call" {
					mu.Lock()
					calls = append(calls, sessionExtensionCall{method, params})
					mu.Unlock()
				}
			})
			ctx := t.Context()
			if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "owner"); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.ReadAppResource(ctx, project.ID, "chat", "app-call", app, "ui://fixture/unlisted"); err != nil {
				t.Fatal(err)
			}
			result, err := manager.CallAppTool(ctx, project.ID, "chat", "app-call", app, "app_only", map[string]any{"origin": "model"})
			if err != nil || result.(map[string]any)["isError"] != false {
				t.Fatalf("App call: %#v, %v", result, err)
			}
			for _, attempt := range []struct {
				project, session, call, tool string
				attachment                   controller.AppAttachment
			}{
				{"other-project", "chat", "app-call", "app_only", app},
				{project.ID, "other-session", "app-call", "app_only", app},
				{project.ID, "chat", "unknown-view", "app_only", app},
				{project.ID, "chat", "app-call", "other__app_only", app},
				{project.ID, "chat", "app-call", "app_only", controller.AppAttachment{ToolName: "show", ExtensionName: "other", ResourceURI: app.ResourceURI}},
			} {
				if _, err := manager.CallAppTool(ctx, attempt.project, attempt.session, attempt.call, attempt.attachment, attempt.tool, nil); err == nil {
					t.Fatalf("Unauthorized App call accepted: %#v", attempt)
				}
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := manager.CallAppTool(cancelled, project.ID, "chat", "app-call", app, "app_only", nil); err == nil {
				t.Fatal("cancelled call accepted")
			}
			mu.Lock()
			defer mu.Unlock()
			if len(calls) != 1 {
				t.Fatalf("unauthorized requests reached Pi: %#v", calls)
			}
			if nativeAppAPI {
				if calls[0].method != "pi.apps.tools.call" || calls[0].params["extensionName"] != "appserver" || calls[0].params["toolName"] != "app_only" {
					t.Fatalf("wrong App origin: %#v", calls)
				}
			} else if calls[0].method != "pi.tools.call" || calls[0].params["name"] != "appserver__app_only" {
				t.Fatalf("legacy compatibility lost: %#v", calls)
			}
		})
	}
}
