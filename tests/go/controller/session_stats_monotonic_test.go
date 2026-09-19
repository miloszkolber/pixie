package controller_test

import (
	"encoding/json"
	"testing"
)

// AUX-15: an accumulated usage snapshot that shrinks at compaction must not
// regress the totals, while the live context usage still tracks the SDK.
func TestUsageTotalsStayMonotonicAcrossCompaction(t *testing.T) {
	loadUpdates := []map[string]any{
		{"__piOnly": true, "sessionUpdate": "usage_update",
			"accumulatedInputTokens": 1000, "accumulatedOutputTokens": 500,
			"accumulatedCost": 1.5, "costCurrency": "USD",
			"contextLimit": 200000, "used": 1800},
	}
	manager, _, project, _ := newSessionManager(t, loadUpdates, nil)
	if _, err := manager.Messages(t.Context(), "chat", project.ID, project.Roots[0], "test"); err != nil {
		t.Fatal(err)
	}
	before, err := manager.Stats("chat")
	if err != nil {
		t.Fatal(err)
	}
	if before.Tokens.Input != 1000 || before.Tokens.Output != 500 || before.Cost != 1.5 {
		t.Fatalf("initial usage projection: %#v", before)
	}

	// Compaction re-emits a smaller accumulated snapshot.
	payload, err := json.Marshal(map[string]any{
		"sessionId": "chat",
		"update": map[string]any{
			"sessionUpdate":           "usage_update",
			"accumulatedInputTokens":  400,
			"accumulatedOutputTokens": 100,
			"accumulatedCost":         0.5,
			"contextLimit":            200000,
			"used":                    800,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Extension(t.Context(), "pi.session.update", payload); err != nil {
		t.Fatal(err)
	}
	after, err := manager.Stats("chat")
	if err != nil {
		t.Fatal(err)
	}
	if after.Tokens.Input != 1000 || after.Tokens.Output != 500 || after.Cost != 1.5 {
		t.Fatalf("compaction regressed the totals: %#v", after)
	}
	if tokens, _ := after.ContextUsage["tokens"].(int64); tokens != 800 {
		t.Fatalf("live context usage did not follow the SDK: %#v", after.ContextUsage)
	}
}
