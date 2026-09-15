package controller_test

import (
	"testing"
	"time"
)

// AUX-14: the optimistic user message is consumed by the live native echo. When
// the host supplies the native session-entry id on that echo, the controller
// must attach it to the existing message so "Edit from here" works on the
// newest turn without a replay or reattach, and must publish the enriched
// message so the browser learns the id live.
func TestOptimisticPromptEchoAttachesNativeEntryId(t *testing.T) {
	prompts := make(chan map[string]any, 4)
	events := make(chan publishedEvent, 64)
	manager, _, project, _ := newSessionManagerWithInitializeAndPublisher(t, nil, prompts, bunHostInitializeResponse(), func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	})
	ctx := t.Context()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Prompt(ctx, "chat", "hello", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-prompts:
	case <-time.After(2 * time.Second):
		t.Fatal("prompt was not dispatched")
	}
	if err := manager.SessionUpdate(ctx, dialogUpdate(map[string]any{
		"sessionUpdate": "user_message_chunk",
		"messageId":     "u1",
		"entryId":       "entry-42",
		"content":       map[string]any{"type": "text", "text": "hello"},
	})); err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client")
	if err != nil {
		t.Fatal(err)
	}
	messages, _ := snapshot["messages"].([]any)
	if len(messages) == 0 {
		t.Fatalf("no projected messages: %#v", snapshot)
	}
	newest, _ := messages[len(messages)-1].(map[string]any)
	if got, _ := newest["entryId"].(string); got != "entry-42" {
		t.Fatalf("newest user entryId = %q, want entry-42 (%#v)", got, newest)
	}
	// The live browser must learn the id without reattaching: the consumed echo
	// publishes the enriched user message.
	for {
		select {
		case published := <-events:
			if published.channel != "agent.event" {
				continue
			}
			data, _ := published.data.(map[string]any)
			event, _ := data["event"].(map[string]any)
			if event["type"] != "message_start" {
				continue
			}
			message, _ := event["message"].(map[string]any)
			if got, _ := message["entryId"].(string); got == "entry-42" {
				return
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatal("consumed optimistic echo did not publish the enriched user message")
		}
	}
}

// AUX-14: an echo without a native entry id must not invent one.
func TestOptimisticPromptEchoWithoutEntryIdStaysEntryLess(t *testing.T) {
	prompts := make(chan map[string]any, 4)
	manager, _, project, _ := newSessionManager(t, nil, prompts)
	ctx := t.Context()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Prompt(ctx, "chat", "hello", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-prompts:
	case <-time.After(2 * time.Second):
		t.Fatal("prompt was not dispatched")
	}
	if err := manager.SessionUpdate(ctx, dialogUpdate(map[string]any{
		"sessionUpdate": "user_message_chunk",
		"content":       map[string]any{"type": "text", "text": "hello"},
	})); err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client")
	if err != nil {
		t.Fatal(err)
	}
	messages, _ := snapshot["messages"].([]any)
	if len(messages) == 0 {
		t.Fatalf("no projected messages: %#v", snapshot)
	}
	newest, _ := messages[len(messages)-1].(map[string]any)
	if _, exists := newest["entryId"]; exists {
		t.Fatalf("controller fabricated an entry id: %#v", newest)
	}
}
