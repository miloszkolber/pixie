package controller_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/coder/websocket"
	piwire "github.com/miloszkolber/pixie/internal/piprotocol"
)

type recordingEvents struct {
	mu      sync.Mutex
	methods []string
}

func (e *recordingEvents) SessionUpdate(context.Context, piwire.SessionNotification) error {
	return nil
}

func (e *recordingEvents) Extension(_ context.Context, method string, _ json.RawMessage) error {
	e.mu.Lock()
	e.methods = append(e.methods, method)
	e.mu.Unlock()
	return nil
}

func (e *recordingEvents) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.methods...)
}

func piInitializeResponse() map[string]any {
	return map[string]any{"protocolVersion": 1, "runtimeId": "fixture-runtime", "version": "0.85.1", "capabilities": map[string]any{"sessions": 1, "providers": 1, "mcp": 1, "agents": 1, "plans": 1}}
}

func writeRPC(connection *websocket.Conn, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return connection.Write(context.Background(), websocket.MessageText, payload)
}

func fixtureNotification(method, sessionID string, update map[string]any) map[string]any {
	if method != "session.event" {
		return map[string]any{"sessionId": sessionID, "update": update}
	}
	if event, ok := update["__native"].(map[string]any); ok {
		return map[string]any{"sessionId": sessionID, "event": event}
	}
	var event map[string]any
	switch update["sessionUpdate"] {
	case "session_info_update":
		event = map[string]any{"type": "session_info_changed", "name": update["title"]}
	case "user_message_chunk":
		event = map[string]any{"type": "message_start", "message": map[string]any{"role": "user", "messageId": update["messageId"], "content": []any{update["content"]}}}
	case "agent_message_chunk", "agent_thought_chunk":
		kind := "text_delta"
		if update["sessionUpdate"] == "agent_thought_chunk" {
			kind = "thinking_delta"
		}
		event = map[string]any{"type": "message_update", "message": map[string]any{"role": "assistant", "messageId": update["messageId"]}, "assistantMessageEvent": map[string]any{"type": kind, "delta": update["content"].(map[string]any)["text"]}}
	case "tool_call":
		event = map[string]any{"type": "tool_execution_start", "toolCallId": update["toolCallId"], "toolName": update["title"], "args": update["rawInput"]}
	case "plan":
		event = map[string]any{"type": "plan", "entries": update["entries"]}
	default:
		panic(fmt.Sprintf("fixture needs native event for %v", update["sessionUpdate"]))
	}
	return map[string]any{"sessionId": sessionID, "event": event}
}
