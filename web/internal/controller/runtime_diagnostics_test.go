package controller

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/diagnostics"
)

func TestRuntimeDiagnosticsSanitizesHostDiagnosticReason(t *testing.T) {
	secret := "support-secret-value"
	path := "/home/alice/.pi/agent/config.json"
	report := runtimeDiagnosticsSnapshot(nil, nil, nil, map[string]any{
		"configured": true,
		"reachable":  false,
		"error": "dial https://alice:" + secret + "@assistant.example/pi?token=" + secret +
			" with Bearer " + secret + " at " + path + " PIXIE_PI_SECRET_KEY=" + secret,
	})
	if report.Host.Reason == "" {
		t.Fatal("host reason was removed instead of safely projected")
	}
	for _, forbidden := range []string{secret, "assistant.example", path, "alice"} {
		if strings.Contains(report.Host.Reason, forbidden) {
			t.Fatalf("host diagnostic leaked %q: %q", forbidden, report.Host.Reason)
		}
	}
}

func TestRuntimeDiagnosticsUnreachableRecoveryUsesPortSettingsWithoutConfig(t *testing.T) {
	rawConfig := `{"schemaVersion":2,"host":"127.0.0.1","port":3285,"agentDir":"/home/alice/.pi/agent"}`
	report := runtimeDiagnosticsSnapshot(nil, nil, nil, map[string]any{
		"configured": true,
		"reachable":  false,
		"error":      rawConfig,
	})
	recovery := strings.Join(report.Remediation, "\n")
	if !strings.Contains(recovery, "config port") || !strings.Contains(recovery, "PIXIE_PI_PORT") {
		t.Fatalf("unreachable recovery = %q, want config port and PIXIE_PI_PORT", recovery)
	}
	for _, forbidden := range []string{"PIXIE_ASSISTANT_PORT", rawConfig, "3285", "/home/alice"} {
		if strings.Contains(recovery, forbidden) {
			t.Fatalf("unreachable recovery leaked or recommended %q: %q", forbidden, recovery)
		}
	}
}

func TestHealthTransitionRingIsBoundedAndRejectsUnknownText(t *testing.T) {
	ring := newHealthTransitionRing()
	secret := "token=super-secret /home/alice/.pi/agent/config.json"
	// Unknown states and components never enter the ring, so raw error text,
	// endpoints and paths cannot leak through this surface.
	ring.Observe("agent", secret)
	ring.Observe("not-a-component", "ready")
	if got := ring.Snapshot(); len(got) != 0 {
		t.Fatalf("ring retained non-vocabulary text: %#v", got)
	}
	ring.Observe("agent", "unreachable")
	ring.Observe("agent", "unreachable")
	if got := ring.Snapshot(); len(got) != 1 || got[0].To != "unreachable" || got[0].From != "" {
		t.Fatalf("deduplicated transition = %#v", got)
	}
	// Cycle well past the cap; the ring must retain only the most recent bound.
	for i := 0; i < healthTransitionMaxEntries*3; i++ {
		state := "ready"
		if i%2 == 0 {
			state = "degraded"
		}
		ring.Observe("application", state)
	}
	snapshot := ring.Snapshot()
	if len(snapshot) != healthTransitionMaxEntries {
		t.Fatalf("health ring length = %d, want %d", len(snapshot), healthTransitionMaxEntries)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{secret, "super-secret", "/home/alice", "not-a-component"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("health transitions leaked %q: %s", forbidden, encoded)
		}
	}
	if _, err := time.Parse(time.RFC3339, snapshot[len(snapshot)-1].At); err != nil {
		t.Fatalf("transition timestamp %q is not RFC3339: %v", snapshot[len(snapshot)-1].At, err)
	}
}

func TestRuntimeDiagnosticsHealthTransitionsFollowProjectedState(t *testing.T) {
	ring := newHealthTransitionRing()
	secret := "unreachable because token=super-secret at /home/alice/.pi/agent"
	countAgent := func(transitions []HealthTransition, to string) int {
		count := 0
		for _, transition := range transitions {
			if transition.Component == "agent" && transition.To == to {
				count++
			}
		}
		return count
	}
	first := runtimeDiagnosticsSnapshot(ring, nil, nil, map[string]any{
		"configured": true,
		"reachable":  false,
		"error":      secret,
	})
	if countAgent(first.Health.Transitions, "unreachable") != 1 {
		t.Fatalf("first agent transition = %#v", first.Health.Transitions)
	}
	// A repeated identical state is deduplicated, then a recovery transition is
	// recorded without the raw host reason.
	second := runtimeDiagnosticsSnapshot(ring, nil, nil, map[string]any{
		"configured": true,
		"reachable":  false,
		"error":      secret,
	})
	if len(second.Health.Transitions) != len(first.Health.Transitions) {
		t.Fatalf("repeated state appended a transition: %#v", second.Health.Transitions)
	}
	compatible := true
	third := runtimeDiagnosticsSnapshot(ring, nil, nil, map[string]any{
		"configured": true,
		"reachable":  true,
		"agentProfile": AgentProfile{
			Compatible: compatible,
		},
	})
	if countAgent(third.Health.Transitions, "ready") != 1 {
		t.Fatalf("recovery transition missing: %#v", third.Health.Transitions)
	}
	if last := third.Health.Transitions[len(third.Health.Transitions)-1]; last.Component != "agent" || last.From != "unreachable" || last.To != "ready" {
		t.Fatalf("recovery transition = %#v", last)
	}
	encoded, err := json.Marshal(third.Health)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"super-secret", "/home/alice", secret} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("health diagnostics leaked %q: %s", forbidden, encoded)
		}
	}
	if _, err := time.Parse(time.RFC3339, third.Health.Transitions[0].At); err != nil {
		t.Fatalf("transition timestamp %q is not RFC3339: %v", third.Health.Transitions[0].At, err)
	}
}

// The support export must carry only the controller's stable transition
// tokens; diagnostics sanitizes them again on the way out.
func TestSupportSnapshotCarriesBoundedHealthTransitions(t *testing.T) {
	report := RuntimeDiagnosticsReport{Health: RuntimeDiagnosticsHealth{Transitions: []HealthTransition{
		{At: time.Now().UTC().Format(time.RFC3339), Component: "agent", From: "unreachable", To: "ready"},
		{At: time.Now().UTC().Format(time.RFC3339), Component: "schedule", To: "degraded"},
	}}}
	raw, err := diagnostics.MarshalSupportSnapshot(supportSnapshotRuntime(diagnostics.BuildInfo{}, report, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"healthTransitions"`) || !strings.Contains(string(raw), `"agent"`) || !strings.Contains(string(raw), `"schedule"`) {
		t.Fatalf("support snapshot dropped health transitions: %s", raw)
	}
}

// AUX-12 wiring: the sampling closure must forward the session manager's
// bounded degradation summary. A summary reaches the support export as
// sessionSchema counters, and no session text or path crosses the boundary.
func TestSupportSnapshotCarriesSessionSchemaSummary(t *testing.T) {
	report := RuntimeDiagnosticsReport{}
	schema := &diagnostics.SessionSchemaSummary{
		WrittenByNewerRuntime: true,
		Version:               9,
		UnknownRecords:        3,
		InvalidRecords:        1,
		RepairedToolCalls:     2,
	}
	raw, err := diagnostics.MarshalSupportSnapshot(supportSnapshotRuntime(diagnostics.BuildInfo{}, report, schema), nil)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot diagnostics.SupportSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	got := snapshot.Runtime.SessionSchema
	if got == nil || !got.WrittenByNewerRuntime || got.Version != 9 || got.UnknownRecords != 3 || got.InvalidRecords != 1 || got.RepairedToolCalls != 2 {
		t.Fatalf("session schema summary not projected: %#v", got)
	}
	if raw, err := diagnostics.MarshalSupportSnapshot(supportSnapshotRuntime(diagnostics.BuildInfo{}, report, nil), nil); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(raw), "sessionSchema") {
		t.Fatalf("nil session schema was exported as a healthy zero: %s", raw)
	}
}
