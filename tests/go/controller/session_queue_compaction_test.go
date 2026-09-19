package controller_test

import (
	"testing"
	"time"
)

// AUX-04: a prompt submitted while Pi compacts its context must be held in the
// controller-owned follow-up queue and delivered after compaction_end, never
// dropped and never raced against the compaction.
func TestQueuedPromptDuringCompactionDeliversAfterCompactionEnds(t *testing.T) {
	prompts := make(chan map[string]any, 4)
	manager, _, project, _ := newSessionManager(t, nil, prompts)
	ctx := t.Context()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SessionUpdate(ctx, dialogUpdate(map[string]any{"sessionUpdate": "compaction_start"})); err != nil {
		t.Fatal(err)
	}
	if err := manager.Queue(ctx, "chat", "queued during compaction"); err != nil {
		t.Fatalf("queue during compaction: %v", err)
	}
	select {
	case got := <-prompts:
		t.Fatalf("prompt dispatched while compaction was active: %#v", got)
	case <-time.After(150 * time.Millisecond):
	}
	if err := manager.SessionUpdate(ctx, dialogUpdate(map[string]any{"sessionUpdate": "compaction_end"})); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-prompts:
		params, _ := got["params"].(map[string]any)
		if params == nil {
			t.Fatalf("delivered prompt has no params: %#v", got)
		}
		if text := promptText(params); text != "queued during compaction" {
			t.Fatalf("delivered prompt text = %q", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued prompt was not delivered after compaction ended")
	}
}

// promptText extracts the leading text block from a forwarded session.prompt.
func promptText(params map[string]any) string {
	blocks, ok := params["content"].([]any)
	if !ok || len(blocks) == 0 {
		return ""
	}
	block, _ := blocks[0].(map[string]any)
	text, _ := block["text"].(string)
	return text
}
