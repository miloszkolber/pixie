package controller_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/diagnostics"
)

// AUX-32 residual: native Pi/SDK update text carried inside session events must
// be normalized before it becomes persisted session state or reaches a browser
// projection. A bound secret, URL, absolute path, API key or configured secret
// must never survive into the event stream, the projected transcript or the
// session summary, while the typed failure class stays visible.
func TestNativeSessionEventErrorTextIsRedacted(t *testing.T) {
	const configuredSecret = "configured-s3cret-value-1234"
	diagnostics.ConfigureSanitizerSecrets(configuredSecret)
	t.Cleanup(func() { diagnostics.ConfigureSanitizerSecrets() })

	forbidden := []string{
		"raw-bearer-secret-value",
		"https://operator:token-value@example.test/mcp",
		"/home/operator/.pi/auth.json",
		"sk-live-uncovered-credential",
		configuredSecret,
	}
	hostile := "Pi SDK failed: Bearer " + strings.Join(forbidden, " ")

	recorder := &eventRecorder{}
	manager, _, project, _ := newSessionManagerWithPublisher(t, recorder.publish)
	ctx := t.Context()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}

	// A native status error is persisted into the settlement and projected as
	// the browser error event.
	if err := extensionUpdate(ctx, manager, map[string]any{
		"sessionUpdate": "status_message",
		"status":        map[string]any{"type": "error", "message": hostile},
	}); err != nil {
		t.Fatal(err)
	}
	// A native notice is projected into the transcript as an activity row.
	if err := extensionUpdate(ctx, manager, map[string]any{
		"sessionUpdate": "status_message",
		"status":        map[string]any{"type": "notice", "message": hostile},
	}); err != nil {
		t.Fatal(err)
	}
	// A lifecycle retry carries a raw provider error message.
	if err := manager.SessionUpdate(ctx, dialogUpdate(map[string]any{
		"sessionUpdate": "auto_retry_start",
		"attempt":       1,
		"maxAttempts":   3,
		"delayMs":       2000,
		"errorMessage":  hostile,
	})); err != nil {
		t.Fatal(err)
	}
	// The pi-only native_lifecycle projection carries the same retry error and a
	// forward-compatible field the controller does not know about.
	if err := extensionUpdate(ctx, manager, map[string]any{
		"sessionUpdate": "native_lifecycle",
		"event": map[string]any{
			"type":         "summarization_retry_scheduled",
			"attempt":      1,
			"maxAttempts":  3,
			"delayMs":      2000,
			"errorMessage": hostile,
			"futureField":  "preserved",
		},
	}); err != nil {
		t.Fatal(err)
	}
	// A failed tool result carries a raw native error into the transcript.
	if err := manager.SessionUpdate(ctx, dialogUpdate(map[string]any{
		"sessionUpdate": "tool_call",
		"toolCallId":    "hostile-tool",
		"title":         "hostile",
		"rawInput":      map[string]any{},
	})); err != nil {
		t.Fatal(err)
	}
	if err := manager.SessionUpdate(ctx, dialogUpdate(map[string]any{
		"sessionUpdate": "tool_call_update",
		"toolCallId":    "hostile-tool",
		"status":        "failed",
		"error":         hostile,
	})); err != nil {
		t.Fatal(err)
	}

	// The event stream the controller retained and published must be clean.
	events := recorder.snapshot()
	if len(events) == 0 {
		t.Fatal("no session events were published")
	}
	eventJSON, err := json.Marshal(publishedEventPayloads(events))
	if err != nil {
		t.Fatal(err)
	}
	assertNoForbiddenNativeText(t, "published events", string(eventJSON), forbidden)

	// The projected transcript and the session summary must also be clean.
	snapshot, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a")
	if err != nil {
		t.Fatal(err)
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	assertNoForbiddenNativeText(t, "session snapshot", string(snapshotJSON), forbidden)

	// The failure class stays visible: the settlement reports an error and the
	// transcript still marks the failed tool.
	summary, ok := snapshot["summary"].(controller.SessionSummary)
	if !ok || summary.LastSettlement == nil || summary.LastSettlement.StopReason != "error" {
		t.Fatalf("native failure class was lost: %#v", snapshot["summary"])
	}
	if summary.LastSettlement.ErrorMessage == "" {
		t.Fatal("native failure lost its bounded summary")
	}
	if !toolResultMarkedFailed(snapshot["messages"], "hostile-tool") {
		t.Fatalf("failed tool result was not preserved: %#v", snapshot["messages"])
	}
	if !hasFailedRetryEvent(events) {
		t.Fatal("retry lifecycle event was dropped")
	}
	if !hasEventField(events, "summarization_retry_scheduled", "futureField", "preserved") {
		t.Fatal("unknown native lifecycle field was not preserved")
	}
}

// AUX-32: the lifecycle pass-through must also drop the raw array so a
// non-host sender or a future Pi shape cannot relay native transcript text.
func TestAgentEndStripsNativeMessagesAtControllerBoundary(t *testing.T) {
	const hostile = "raw native tool text"
	recorder := &eventRecorder{}
	manager, _, project, _ := newSessionManagerWithPublisher(t, recorder.publish)
	if _, err := manager.Messages(t.Context(), "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SessionUpdate(t.Context(), dialogUpdate(map[string]any{
		"sessionUpdate": "agent_end",
		"willRetry":     false,
		"messages": []any{
			map[string]any{"role": "toolResult", "toolCallId": "t1", "content": hostile},
		},
	})); err != nil {
		t.Fatal(err)
	}
	end, ok := agentEventOfType(recorder.snapshot(), "agent_end")
	if !ok {
		t.Fatal("agent_end never reached the browser stream")
	}
	if _, exists := end["messages"]; exists {
		t.Fatalf("agent_end relayed the raw messages array: %#v", end)
	}
	if encoded := encodeEvents(t, recorder.snapshot()); strings.Contains(encoded, hostile) {
		t.Fatalf("raw native message text reached the published stream: %s", encoded)
	}
}

func agentEventOfType(events []publishedEvent, kind string) (map[string]any, bool) {
	for _, event := range events {
		if event.channel != "agent.event" {
			continue
		}
		payload, _ := event.data.(map[string]any)
		inner, _ := payload["event"].(map[string]any)
		if inner["type"] == kind {
			return inner, true
		}
	}
	return nil, false
}

func encodeEvents(t *testing.T, events []publishedEvent) string {
	t.Helper()
	encoded, err := json.Marshal(publishedEventPayloads(events))
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

type eventRecorder struct {
	mu     sync.Mutex
	events []publishedEvent
}

func (r *eventRecorder) publish(channel string, data any) {
	r.mu.Lock()
	r.events = append(r.events, publishedEvent{channel: channel, data: data})
	r.mu.Unlock()
}

func (r *eventRecorder) snapshot() []publishedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]publishedEvent(nil), r.events...)
}

// extensionUpdate delivers a pi-only native update, the path the host adapter
// uses for status and extension-error projections.
func extensionUpdate(ctx context.Context, manager *controller.SessionManager, update map[string]any) error {
	payload, err := json.Marshal(map[string]any{"sessionId": "chat", "update": update})
	if err != nil {
		return err
	}
	return manager.Extension(ctx, "pi.session.update", payload)
}

// publishedEventPayloads exposes the unexported data field for JSON assertions.
func publishedEventPayloads(events []publishedEvent) []map[string]any {
	payloads := make([]map[string]any, 0, len(events))
	for _, event := range events {
		payloads = append(payloads, map[string]any{"channel": event.channel, "data": event.data})
	}
	return payloads
}

func assertNoForbiddenNativeText(t *testing.T, surface, encoded string, forbidden []string) {
	t.Helper()
	for _, value := range forbidden {
		if strings.Contains(encoded, value) {
			t.Fatalf("%s leaked native text %q: %s", surface, value, encoded)
		}
	}
}

func toolResultMarkedFailed(messages any, toolCallID string) bool {
	for _, raw := range messages.([]any) {
		message, _ := raw.(map[string]any)
		if message["role"] != "toolResult" || message["toolCallId"] != toolCallID {
			continue
		}
		failed, _ := message["isError"].(bool)
		return failed
	}
	return false
}

func hasFailedRetryEvent(events []publishedEvent) bool {
	for _, event := range events {
		payload, _ := event.data.(map[string]any)
		inner, _ := payload["event"].(map[string]any)
		if inner["type"] == "auto_retry_start" {
			return true
		}
	}
	return false
}

func hasEventField(events []publishedEvent, eventType, field string, want any) bool {
	for _, event := range events {
		payload, _ := event.data.(map[string]any)
		inner, _ := payload["event"].(map[string]any)
		if inner["type"] == eventType && inner[field] == want {
			return true
		}
	}
	return false
}
