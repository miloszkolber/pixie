package controller_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPiInitialToolPayload(t *testing.T) {
	m, _, p, _ := newSessionManager(t, []map[string]any{
		{"sessionUpdate": "tool_call", "toolCallId": "review-tool", "title": "Read file", "kind": "read", "status": "completed", "rawInput": map[string]any{"path": "a"}, "content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "review-initial-output"}}}},
	}, nil)
	result, err := m.Messages(t.Context(), "chat", p.ID, p.Roots[0], "review")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if !strings.Contains(string(raw), "review-initial-output") {
		t.Fatalf("initial completed tool output is lost: %s", raw)
	}
}

func TestPiToolInputPatch(t *testing.T) {
	m, _, p, _ := newSessionManager(t, []map[string]any{
		{"sessionUpdate": "tool_call", "toolCallId": "review-tool", "title": "Preparing", "status": "pending"},
		{"sessionUpdate": "tool_call_update", "toolCallId": "review-tool", "title": "Read actual file", "rawInput": map[string]any{"path": "review-late-input"}, "status": "in_progress"},
	}, nil)
	result, err := m.Messages(t.Context(), "chat", p.ID, p.Roots[0], "review")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if !strings.Contains(string(raw), "review-late-input") {
		t.Fatalf("updated tool input is lost: %s", raw)
	}
}

func TestPiDistinctMessageIDs(t *testing.T) {
	m, _, p, _ := newSessionManager(t, []map[string]any{
		{"sessionUpdate": "agent_message_chunk", "messageId": "first", "content": map[string]any{"type": "text", "text": "first message"}},
		{"sessionUpdate": "agent_message_chunk", "messageId": "second", "content": map[string]any{"type": "text", "text": "second message"}},
	}, nil)
	result, err := m.Messages(t.Context(), "chat", p.ID, p.Roots[0], "review")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result["messages"])
	var messages []any
	_ = json.Unmarshal(raw, &messages)
	if len(messages) != 2 {
		t.Fatalf("distinct message IDs collapsed: %s", raw)
	}
}

func TestPiMessageUsageIsReplacedByIdentity(t *testing.T) {
	updates := []map[string]any{}
	for _, tokens := range []int{100, 100, 120} {
		updates = append(updates, map[string]any{"__piOnly": true, "sessionUpdate": "message_usage", "messageId": "usage-one", "usage": map[string]any{"inputTokens": tokens, "outputTokens": 20, "totalTokens": tokens + 20, "cost": 0.25}})
	}
	manager, _, project, _ := newSessionManager(t, updates, nil)
	if _, err := manager.Messages(t.Context(), "chat", project.ID, project.Roots[0], "test"); err != nil {
		t.Fatal(err)
	}
	stats, err := manager.Stats("chat")
	if err != nil || stats.Tokens.Input != 120 || stats.Tokens.Output != 20 || stats.Tokens.Total != 140 || stats.Cost != 0.25 {
		t.Fatalf("usage counted twice: %#v %v", stats, err)
	}
}

func TestPiConfigSelectorUsesAdvertisedIdentityAndValues(t *testing.T) {
	option := func(current string) []any {
		return []any{map[string]any{"id": "custom-reasoning-42", "category": "thought_level", "name": "Reasoning", "type": "select", "currentValue": current, "options": []any{map[string]any{"value": "low", "name": "Low"}, map[string]any{"value": "high", "name": "High"}}}}
	}
	manager, project, agent := newModelSwitchManager(t, [][]any{option("low")}, []modelSwitchConfigResult{{options: option("high")}})
	if _, err := manager.Messages(t.Context(), "chat", project.ID, project.Roots[0], "test"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetConfigOption(t.Context(), "chat", "custom-reasoning-42", "invalid"); err == nil {
		t.Fatal("unadvertised value accepted")
	}
	if err := manager.SetConfigOption(t.Context(), "chat", "custom-reasoning-42", "high"); err != nil {
		t.Fatal(err)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.calls) != 1 || agent.calls[0].configID != "custom-reasoning-42" || agent.calls[0].value != "high" {
		t.Fatalf("wrong config routing: %#v", agent.calls)
	}
}
