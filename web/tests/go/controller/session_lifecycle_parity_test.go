package controller_test

import (
	"testing"
	"time"
)

// nextAgentEventOfType skips unrelated projections (for example the commands
// catalog replayed on attach) while waiting for one lifecycle assertion.
func nextAgentEventOfType(t *testing.T, events chan publishedEvent, kind string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		timeout := time.Until(deadline)
		if timeout <= 0 {
			t.Fatalf("agent event %q never arrived", kind)
		}
		select {
		case published := <-events:
			if published.channel != "agent.event" {
				continue
			}
			data, ok := published.data.(map[string]any)
			if !ok {
				continue
			}
			event, ok := data["event"].(map[string]any)
			if !ok || event["type"] != kind {
				continue
			}
			return event
		case <-time.After(timeout):
			t.Fatalf("agent event %q never arrived", kind)
		}
	}
}

func expectNoAgentEvent(t *testing.T, events chan publishedEvent) {
	t.Helper()
	select {
	case published := <-events:
		t.Fatalf("unexpected agent event reached browsers: %#v", published)
	case <-time.After(50 * time.Millisecond):
	}
}

func drainEvents(events chan publishedEvent) {
	for {
		select {
		case <-events:
		default:
			return
		}
	}
}

func TestPendingDialogSurvivesAttachAndReachesSnapshot(t *testing.T) {
	events := make(chan publishedEvent, 32)
	manager, _, project, _ := newSessionManagerWithPublisher(t, func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	})
	ctx := t.Context()
	if err := manager.SessionUpdate(ctx, dialogUpdate(selectRequest("dialog-reattach", nil))); err != nil {
		t.Fatal(err)
	}
	publishedUiEvents(t, events, "agent.event", 1)
	// Re-attachment must not discard the unresolved dialog: the host keeps
	// the awaiting call and re-publishes it, deduplicated by request ID.
	snapshot, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a")
	if err != nil {
		t.Fatal(err)
	}
	pending, ok := snapshot["pendingDialogs"].([]map[string]any)
	if !ok {
		t.Fatalf("snapshot has no pending dialogs: %#v", snapshot["pendingDialogs"])
	}
	if len(pending) != 1 || pending[0]["requestId"] != "dialog-reattach" {
		t.Fatalf("snapshot pending dialogs: %#v", pending)
	}
	if err := manager.ResolveDialog(ctx, "chat", "dialog-reattach", map[string]any{"cancelled": true}); err != nil {
		t.Fatalf("dialog did not survive re-attachment: %v", err)
	}
}

func TestArchiveDismissesPendingDialogs(t *testing.T) {
	events := make(chan publishedEvent, 32)
	manager, _, project, _ := newSessionManagerWithPublisher(t, func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	})
	ctx := t.Context()
	if err := manager.SessionUpdate(ctx, dialogUpdate(selectRequest("dialog-archive", nil))); err != nil {
		t.Fatal(err)
	}
	publishedUiEvents(t, events, "agent.event", 1)
	if err := manager.Archive(ctx, project.ID, "chat", project.Roots[0]); err != nil {
		t.Fatal(err)
	}
	cancelled := nextAgentEventOfType(t, events, "ui_cancel")
	if cancelled["requestId"] != "dialog-archive" {
		t.Fatalf("archive did not dismiss the dialog: %#v", cancelled)
	}
	if err := manager.ResolveDialog(ctx, "chat", "dialog-archive", map[string]any{"cancelled": true}); err == nil {
		t.Fatal("archived dialog still accepts answers")
	}
}

func TestDeleteDismissesPendingDialogs(t *testing.T) {
	events := make(chan publishedEvent, 32)
	manager, _, project, _ := newSessionManagerWithPublisher(t, func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	})
	ctx := t.Context()
	if err := manager.SessionUpdate(ctx, dialogUpdate(selectRequest("dialog-delete", nil))); err != nil {
		t.Fatal(err)
	}
	publishedUiEvents(t, events, "agent.event", 1)
	// Deletion dismisses blocked UI like a stop: the host settles its
	// awaiting call on close, and browsers drop the modal.
	if err := manager.Delete(ctx, project.ID, "chat", project.Roots[0]); err != nil {
		t.Fatal(err)
	}
	cancelled := nextAgentEventOfType(t, events, "ui_cancel")
	if cancelled["requestId"] != "dialog-delete" {
		t.Fatalf("delete did not dismiss the dialog: %#v", cancelled)
	}
	if err := manager.ResolveDialog(ctx, "chat", "dialog-delete", map[string]any{"cancelled": true}); err == nil {
		t.Fatal("deleted dialog still accepts answers")
	}
}

func TestAgentEndForwardsWhileStaleStartIsDropped(t *testing.T) {
	events := make(chan publishedEvent, 32)
	manager, _, project, _ := newSessionManagerWithPublisher(t, func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	})
	ctx := t.Context()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	drainEvents(events)
	// A stale agent_start with no open run must not resurrect streaming state.
	if err := manager.SessionUpdate(ctx, dialogUpdate(map[string]any{"sessionUpdate": "agent_start"})); err != nil {
		t.Fatal(err)
	}
	expectNoAgentEvent(t, events)
	// Completion annotations travel verbatim; the first agent_end is never
	// treated as final by itself.
	if err := manager.SessionUpdate(ctx, dialogUpdate(map[string]any{"sessionUpdate": "agent_end", "willRetry": true})); err != nil {
		t.Fatal(err)
	}
	if event := nextAgentEventOfType(t, events, "agent_end"); event["willRetry"] != true {
		t.Fatalf("agent_end projection: %#v", event)
	}
}

func TestLateMessageChunksAreDroppedWhenIdle(t *testing.T) {
	events := make(chan publishedEvent, 32)
	manager, _, project, _ := newSessionManagerWithPublisher(t, func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	})
	ctx := t.Context()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	drainEvents(events)
	if err := manager.SessionUpdate(ctx, dialogUpdate(map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"messageId":     "late",
		"content":       map[string]any{"type": "text", "text": "stale tail"},
	})); err != nil {
		t.Fatal(err)
	}
	expectNoAgentEvent(t, events)
}

func TestStatusWidgetTitleAndWorkingProjectToBrowsers(t *testing.T) {
	events := make(chan publishedEvent, 32)
	manager, _, _, _ := newSessionManagerWithPublisher(t, func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	})
	ctx := t.Context()
	for _, update := range []map[string]any{
		{"sessionUpdate": "ui_status", "key": "signet", "text": "recall: 3 notes"},
		{"sessionUpdate": "ui_widget", "key": "plan", "lines": []any{"step one"}, "placement": "aboveEditor"},
		{"sessionUpdate": "ui_title", "title": "Deep work"},
		{"sessionUpdate": "ui_working", "message": "Thinking…"},
	} {
		if err := manager.SessionUpdate(ctx, dialogUpdate(update)); err != nil {
			t.Fatal(err)
		}
	}
	got := publishedUiEvents(t, events, "agent.event", 4)
	want := []string{"ui_status", "ui_widget", "ui_title", "ui_working"}
	for index, kind := range want {
		if event, ok := got[index]["event"].(map[string]any); !ok || event["type"] != kind {
			t.Fatalf("projection %d: %#v", index, got[index])
		}
	}
}

func TestSessionLivenessRegistrationIsScopedAndIdempotent(t *testing.T) {
	manager, _, _, _ := newSessionManagerWithPublisher(t, nil)
	active := true
	unregister := manager.RegisterSessionLiveness("chat", "extension-work", func() bool { return active })
	if unregister == nil {
		t.Fatal("liveness registration returned no unregister function")
	}
	active = false
	unregister()
	// Unregistering twice and registering nothing must stay safe.
	unregister()
	manager.RegisterSessionLiveness("", "nameless", func() bool { return true })()
	manager.RegisterSessionLiveness("chat", "", func() bool { return true })()
}
