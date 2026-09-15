package controller_test

import (
	"testing"
)

func fixtureMessageRole(message any) string {
	value, _ := message.(map[string]any)
	role, _ := value["role"].(string)
	return role
}

// AUX-08: a newer Pi within the pinned version may emit event kinds and
// transcript roles this controller does not know. They must be dropped or
// projected without breaking the session or throwing.
func TestUnknownEventsAndRolesDegradeSafely(t *testing.T) {
	loadUpdates := []map[string]any{
		{"__native": map[string]any{"type": "future_event_kind", "payload": "ignored"}},
		{"__piOnly": true, "sessionUpdate": "future_update", "payload": "ignored"},
		{"__native": map[string]any{"type": "replay_message", "message": map[string]any{"role": "futureRole", "content": "projected", "messageId": "m1"}}},
		{"__native": map[string]any{"type": "replay_message", "message": map[string]any{"role": "user", "content": "known", "messageId": "u1"}}},
	}
	manager, _, project, _ := newSessionManager(t, loadUpdates, nil)
	snapshot, err := manager.Messages(t.Context(), "chat", project.ID, project.Roots[0], "test")
	if err != nil {
		t.Fatalf("unknown input broke the session projection: %v", err)
	}
	messages, ok := snapshot["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("unknown input changed the transcript shape: %#v", snapshot["messages"])
	}
	if role := fixtureMessageRole(messages[0]); role != "assistant" {
		t.Fatalf("unknown transcript role was not projected as assistant: %#v", messages[0])
	}
	if role := fixtureMessageRole(messages[1]); role != "user" {
		t.Fatalf("known user message was not preserved: %#v", messages[1])
	}
	// The session still answers a later read and its stats endpoint.
	if _, err := manager.Stats("chat"); err != nil {
		t.Fatalf("stats failed after unknown input: %v", err)
	}
}

// AUX-12: the host's session-header/record degradation summary is recorded in
// the session projection so an operator can see why unknown input was dropped.
func TestSessionSchemaDegradationIsProjected(t *testing.T) {
	snapshot := map[string]any{
		"sessionId":     "chat",
		"configOptions": []any{},
		"metadata": map[string]any{
			"sessionSchema": map[string]any{
				"version":               3,
				"writtenByNewerRuntime": false,
				"unknownRecords":        2,
				"invalidRecords":        1,
				"repairedToolCalls":     1,
			},
		},
		"messages": []any{},
	}
	manager, _, project, _ := newSessionManager(t, []map[string]any{{"__snapshot": snapshot}}, nil)
	result, err := manager.Messages(t.Context(), "chat", project.ID, project.Roots[0], "test")
	if err != nil {
		t.Fatal(err)
	}
	schema, ok := result["schema"].(map[string]any)
	if !ok {
		t.Fatalf("session schema degradation was not recorded: %#v", result)
	}
	if schema["unknownRecords"] != float64(2) || schema["invalidRecords"] != float64(1) {
		t.Fatalf("session schema degradation shape: %#v", schema)
	}
}

// AUX-13: a resident, non-streaming session is released and reattached from
// its native file when a foreign writer advanced it.
func TestRefreshFromDiskReloadsNonStreamingSession(t *testing.T) {
	loads, releases := 0, 0
	manager, _, project, _ := newSessionManagerWithInitializeAndPublisher(
		t,
		nil,
		nil,
		piInitializeResponse(),
		nil,
		func(method string, _ map[string]any) {
			switch method {
			case "session.load":
				loads++
			case "session.release":
				releases++
			}
		},
	)
	if _, err := manager.Messages(t.Context(), "chat", project.ID, project.Roots[0], "test"); err != nil {
		t.Fatal(err)
	}
	if loads != 1 {
		t.Fatalf("initial attach loaded the session %d times", loads)
	}
	refreshed, err := manager.RefreshFromDisk(t.Context(), "chat", project.ID, project.Roots[0])
	if err != nil {
		t.Fatalf("refresh from disk: %v", err)
	}
	if !refreshed {
		t.Fatal("idle resident session was not re-read from disk")
	}
	if loads != 2 || releases != 1 {
		t.Fatalf("refresh loads=%d releases=%d", loads, releases)
	}
}

// A refresh must not silently succeed for a session that is not resident, so a
// caller cannot report a re-read that never happened.
func TestRefreshFromDiskRequiresResidentSession(t *testing.T) {
	manager, _, project, _ := newSessionManager(t, nil, nil)
	if _, err := manager.RefreshFromDisk(t.Context(), "chat", project.ID, project.Roots[0]); err == nil {
		t.Fatal("refresh accepted a session that was never attached")
	}
}
