package controller_test

import (
	"context"
	"testing"
	"time"

	piwire "github.com/miloszkolber/pixie/internal/piprotocol"
)

func dialogUpdate(update map[string]any) piwire.SessionNotification {
	return piwire.SessionNotification{SessionId: "chat", Update: update}
}

func selectRequest(requestID string, extra map[string]any) map[string]any {
	update := map[string]any{
		"sessionUpdate": "ui_request",
		"requestId":     requestID,
		"primitive":     "select",
		"title":         "Choose",
		"options":       []any{"Red", "Blue"},
	}
	for key, value := range extra {
		update[key] = value
	}
	return update
}

func publishedUiEvents(t *testing.T, events chan publishedEvent, channel string, count int) []map[string]any {
	t.Helper()
	var matched []map[string]any
	deadline := time.Now().Add(2 * time.Second)
	for len(matched) < count {
		timeout := time.Until(deadline)
		if timeout <= 0 {
			t.Fatalf("only %d of %d %s events arrived", len(matched), count, channel)
		}
		select {
		case published := <-events:
			if published.channel != channel {
				continue
			}
			data, ok := published.data.(map[string]any)
			if !ok {
				t.Fatalf("published data is not an object: %#v", published.data)
			}
			matched = append(matched, data)
		case <-time.After(timeout):
			t.Fatalf("only %d of %d %s events arrived", len(matched), count, channel)
		}
	}
	return matched
}

func TestDialogRequestPublishesUiRequestToBrowsers(t *testing.T) {
	events := make(chan publishedEvent, 16)
	manager, _, _, _ := newSessionManagerWithPublisher(t, func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	})
	if err := manager.SessionUpdate(t.Context(), dialogUpdate(selectRequest("dialog-1", nil))); err != nil {
		t.Fatal(err)
	}
	got := publishedUiEvents(t, events, "agent.event", 1)
	event, ok := got[0]["event"].(map[string]any)
	if !ok || event["type"] != "ui_request" || event["requestId"] != "dialog-1" || event["primitive"] != "select" {
		t.Fatalf("dialog request projection: %#v", got[0])
	}
}

func TestDialogResolveStaysSessionBoundAndSingleUse(t *testing.T) {
	events := make(chan publishedEvent, 16)
	manager, _, _, _ := newSessionManagerWithPublisher(t, func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	})
	ctx := t.Context()
	if err := manager.SessionUpdate(ctx, dialogUpdate(selectRequest("dialog-1", nil))); err != nil {
		t.Fatal(err)
	}
	publishedUiEvents(t, events, "agent.event", 1)
	if err := manager.ResolveDialog(ctx, "chat", "dialog-1", map[string]any{"cancelled": true}); err != nil {
		t.Fatal(err)
	}
	// Replaying the same answer settles nothing twice.
	if err := manager.ResolveDialog(ctx, "chat", "dialog-1", map[string]any{"cancelled": true}); err == nil {
		t.Fatal("dialog response was accepted twice")
	}
	// Another session cannot satisfy this session's dialog.
	if err := manager.SessionUpdate(ctx, dialogUpdate(selectRequest("dialog-2", nil))); err != nil {
		t.Fatal(err)
	}
	publishedUiEvents(t, events, "agent.event", 1)
	if err := manager.ResolveDialog(ctx, "other", "dialog-2", map[string]any{"cancelled": true}); err == nil {
		t.Fatal("dialog response crossed session ownership")
	}
}

func TestDialogResolveValidatesAnswerShapes(t *testing.T) {
	events := make(chan publishedEvent, 16)
	manager, _, _, _ := newSessionManagerWithPublisher(t, func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	})
	ctx := t.Context()
	if err := manager.SessionUpdate(ctx, dialogUpdate(selectRequest("dialog-shape", nil))); err != nil {
		t.Fatal(err)
	}
	publishedUiEvents(t, events, "agent.event", 1)
	for _, result := range []map[string]any{
		{},
		{"value": "Red"},
		{"cancelled": false, "value": 42},
	} {
		if err := manager.ResolveDialog(ctx, "chat", "dialog-shape", result); err == nil {
			t.Fatalf("malformed dialog response accepted: %#v", result)
		}
	}
	if err := manager.ResolveDialog(ctx, "chat", "dialog-shape", map[string]any{"cancelled": false, "value": "Red"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.SessionUpdate(ctx, dialogUpdate(map[string]any{
		"sessionUpdate": "ui_request", "requestId": "dialog-confirm", "primitive": "confirm", "title": "Sure?",
	})); err != nil {
		t.Fatal(err)
	}
	publishedUiEvents(t, events, "agent.event", 1)
	if err := manager.ResolveDialog(ctx, "chat", "dialog-confirm", map[string]any{"cancelled": false, "value": "yes"}); err == nil {
		t.Fatal("string answer accepted for a confirm dialog")
	}
	if err := manager.ResolveDialog(ctx, "chat", "dialog-confirm", map[string]any{"cancelled": false, "value": true}); err != nil {
		t.Fatal(err)
	}
}

func TestDialogTimeoutDismissesBrowsersAndHost(t *testing.T) {
	forwarded := make(chan map[string]any, 4)
	events := make(chan publishedEvent, 16)
	manager, _, _, _ := newSessionManagerWithInitializeAndPublisher(t, nil, nil, piInitializeResponse(), func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	}, func(method string, params map[string]any) {
		if method == "session.uiCancel" {
			forwarded <- params
		}
	})
	ctx := t.Context()
	if err := manager.SessionUpdate(ctx, dialogUpdate(selectRequest("dialog-timeout", map[string]any{"timeout": 30}))); err != nil {
		t.Fatal(err)
	}
	publishedUiEvents(t, events, "agent.event", 1)
	cancelled := publishedUiEvents(t, events, "agent.event", 1)
	event, ok := cancelled[0]["event"].(map[string]any)
	if !ok || event["type"] != "ui_cancel" || event["requestId"] != "dialog-timeout" {
		t.Fatalf("dialog timeout projection: %#v", cancelled[0])
	}
	select {
	case params := <-forwarded:
		if params["requestId"] != "dialog-timeout" {
			t.Fatalf("timeout forwarded for the wrong dialog: %#v", params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout was not forwarded to the Pi host")
	}
	if err := manager.ResolveDialog(ctx, "chat", "dialog-timeout", map[string]any{"cancelled": true}); err == nil {
		t.Fatal("timed-out dialog still accepts answers")
	}
}

func TestDialogCancelIsIdempotentAndDismissesBrowsers(t *testing.T) {
	events := make(chan publishedEvent, 16)
	manager, _, _, _ := newSessionManagerWithPublisher(t, func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	})
	ctx := t.Context()
	if err := manager.CancelDialog(ctx, "chat", "missing"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SessionUpdate(ctx, dialogUpdate(selectRequest("dialog-cancel", nil))); err != nil {
		t.Fatal(err)
	}
	publishedUiEvents(t, events, "agent.event", 1)
	if err := manager.CancelDialog(ctx, "chat", "dialog-cancel"); err != nil {
		t.Fatal(err)
	}
	cancelled := publishedUiEvents(t, events, "agent.event", 1)
	if event, ok := cancelled[0]["event"].(map[string]any); !ok || event["type"] != "ui_cancel" {
		t.Fatalf("dialog cancel projection: %#v", cancelled[0])
	}
	if err := manager.CancelDialog(ctx, "chat", "dialog-cancel"); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidDialogRequestsAreDropped(t *testing.T) {
	events := make(chan publishedEvent, 16)
	manager, _, _, _ := newSessionManagerWithPublisher(t, func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	})
	ctx := t.Context()
	for _, update := range []map[string]any{
		{"sessionUpdate": "ui_request", "requestId": "bad-primitive", "primitive": "widget", "title": "Pick"},
		{"sessionUpdate": "ui_request", "requestId": "empty-title", "primitive": "select", "title": "", "options": []any{"A"}},
		{"sessionUpdate": "ui_request", "requestId": "no-options", "primitive": "select", "title": "Pick"},
		{"sessionUpdate": "ui_request", "requestId": "", "primitive": "select", "title": "Pick", "options": []any{"A"}},
	} {
		if err := manager.SessionUpdate(ctx, dialogUpdate(update)); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case published := <-events:
		t.Fatalf("invalid dialog request reached browsers: %#v", published)
	case <-time.After(50 * time.Millisecond):
	}
	if err := manager.ResolveDialog(ctx, "chat", "bad-primitive", map[string]any{"cancelled": true}); err == nil {
		t.Fatal("dropped dialog still accepts answers")
	}
}

func TestDialogResolveForwardsAnswersToThePiHost(t *testing.T) {
	forwarded := make(chan map[string]any, 4)
	manager, _, _, _ := newSessionManagerWithInitializeAndPublisher(t, nil, nil, piInitializeResponse(), nil, func(method string, params map[string]any) {
		if method == piwire.UiResponseMethod {
			forwarded <- params
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.SessionUpdate(ctx, dialogUpdate(selectRequest("dialog-relay", nil))); err != nil {
		t.Fatal(err)
	}
	if err := manager.ResolveDialog(ctx, "chat", "dialog-relay", map[string]any{"cancelled": false, "value": "Red"}); err != nil {
		t.Fatal(err)
	}
	select {
	case params := <-forwarded:
		if params["sessionId"] != "chat" || params["requestId"] != "dialog-relay" || params["value"] != "Red" {
			t.Fatalf("relayed dialog answer: %#v", params)
		}
	case <-ctx.Done():
		t.Fatal("dialog answer was not forwarded to the Pi host")
	}
}

func TestUiNotifyFansOutAsToastEvent(t *testing.T) {
	events := make(chan publishedEvent, 16)
	manager, _, _, _ := newSessionManagerWithPublisher(t, func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	})
	if err := manager.SessionUpdate(t.Context(), dialogUpdate(map[string]any{
		"sessionUpdate": "ui_notify", "message": "Saved", "level": "info",
	})); err != nil {
		t.Fatal(err)
	}
	got := publishedUiEvents(t, events, "agent.event", 1)
	event, ok := got[0]["event"].(map[string]any)
	if !ok || event["type"] != "ui_notify" || event["message"] != "Saved" {
		t.Fatalf("notify projection: %#v", got[0])
	}
	if err := manager.ResolveDialog(t.Context(), "chat", "notify-is-not-a-dialog", map[string]any{"cancelled": true}); err == nil {
		t.Fatal("notification was registered as a dialog")
	}
}

func TestHostUiEventsProjectThroughSessionUpdate(t *testing.T) {
	events := make(chan publishedEvent, 16)
	manager, _, _, _ := newSessionManagerWithPublisher(t, func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	})
	ctx := t.Context()
	// Host-side cancellation dismisses the browser modal without an answer.
	if err := manager.SessionUpdate(ctx, dialogUpdate(selectRequest("dialog-host-cancel", nil))); err != nil {
		t.Fatal(err)
	}
	publishedUiEvents(t, events, "agent.event", 1)
	if err := manager.SessionUpdate(ctx, dialogUpdate(map[string]any{
		"sessionUpdate": "ui_cancel", "requestId": "dialog-host-cancel",
	})); err != nil {
		t.Fatal(err)
	}
	cancelled := publishedUiEvents(t, events, "agent.event", 1)
	if event, ok := cancelled[0]["event"].(map[string]any); !ok || event["type"] != "ui_cancel" {
		t.Fatalf("host cancel projection: %#v", cancelled[0])
	}
	if err := manager.ResolveDialog(ctx, "chat", "dialog-host-cancel", map[string]any{"cancelled": true}); err == nil {
		t.Fatal("host-cancelled dialog still accepts answers")
	}
}
