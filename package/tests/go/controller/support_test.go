package controller_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/coder/websocket"
	piwire "github.com/miloszkolber/pixie/contracts/piprotocol"
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
	return map[string]any{"protocolVersion": 1, "runtimeId": "fixture-runtime", "bootId": "fixture-boot", "version": "0.85.1", "capabilities": map[string]any{"sessions": 1, "providers": 1, "mcp": 1, "agents": 1, "plans": 1}, "operationSet": map[string]bool{
		"session.list": true, "session.create": true, "session.load": true, "session.prompt": true, "session.cancel": true,
		"session.delete": true, "session.fork": true, "session.prompt.image": true, "session.prompt.resource": true,
		"session.steer": true, "session.rename": true, "session.archive": true, "session.configure": true,
		"session.release": true, "runtime.release": true, "runtime.releaseToTui": true,
		"session.uiResponse": true, "session.uiCancel": true, "mcp.attach": true,
		"pi.session.info": true, "pi.session.rename": true, "pi.session.archive": true, "pi.session.unarchive": true, "pi.session.steer": true,
		"pi.tools.list": true, "runtime.capabilities": true, "pi.slash-commands.list": true,
		"pi.providers.list": true, "pi.providers.canonical-model-info": true, "pi.providers.inventory.refresh": true, "pi.providers.readiness.check": true,
		"provider.loginStart": true, "provider.loginBegin": true, "provider.loginReply": true, "provider.loginCancel": true,
		"pi.providers.config.read": true, "pi.providers.config.delete": true, "pi.defaults.read": true, "pi.defaults.save": true, "pi.defaults.clear": true,
		"pi.preferences.read": true, "pi.preferences.save": true, "pi.preferences.reset": true, "pi.extensions.list": true, "pi.extensions.configure": true,
		"pi.sources.list": true, "pi.sources.create": true, "pi.sources.update": true, "pi.sources.delete": true, "pi.agent-mentions.list": true,
		"pi.todo.plan": true, "pi.config.extensions.list": true, "pi.config.extensions.add": true, "pi.config.extensions.set-enabled": true,
		"pi.config.extensions.remove": true, "pi.session.extensions.list": true, "pi.session.extensions.add": true, "pi.session.extensions.remove": true,
		"adapter.status": true, "adapter.registerBrowser": true, "adapter.session.forget": true,
	}}
}

// piInitializeV2Response is the negotiation-aware hello result. It keeps the
// same operation set and capabilities as the v1 fixture so profile projection
// is exercised identically.
func piInitializeV2Response() map[string]any {
	response := piInitializeResponse()
	delete(response, "runtimeId")
	delete(response, "version")
	response["protocolVersion"] = 2
	response["supportedProtocolVersions"] = []int{2, 1}
	response["hostIdentity"] = "v2-runtime"
	response["nativeVersion"] = "0.85.1"
	return response
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
