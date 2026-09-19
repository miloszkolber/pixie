package diagnostics_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/diagnostics"
)

func TestSanitizeHealthTransitionsBoundsAndAllowlists(t *testing.T) {
	transitions := []diagnostics.HealthTransition{
		{At: "2026-09-15T12:00:00Z", Component: "agent", From: "unknown", To: "ready"},
		{At: "2026-09-15T12:00:01Z", Component: "application", To: "degraded"},
		{At: "2026-09-15T12:00:02Z", Component: "schedule", To: "healthy"},
		// Hostile or malformed records must be dropped, never rewritten.
		{At: "2026-09-15T12:00:03Z", Component: "agent", To: "/home/alice"},
		{At: "2026-09-15T12:00:04Z", Component: "Bearer token=opaque-credential-value", To: "ready"},
		{At: "not-a-time", Component: "agent", To: "ready"},
		{At: "2026-09-15T12:00:05Z", Component: "agent", From: "sk_live_hostile-api-token-value", To: "ready"},
	}
	for index := 0; index < diagnostics.MaxHealthTransitions+5; index++ {
		transitions = append(transitions, diagnostics.HealthTransition{
			At:        "2026-09-15T12:00:06Z",
			Component: "host",
			To:        "ready",
		})
	}
	sanitized := diagnostics.SanitizeHealthTransitions(transitions)
	if len(sanitized) == 0 || len(sanitized) > diagnostics.MaxHealthTransitions {
		t.Fatalf("sanitized transitions = %d", len(sanitized))
	}
	for _, transition := range sanitized {
		if transition.Component != "agent" && transition.Component != "host" && transition.Component != "application" && transition.Component != "schedule" {
			t.Fatalf("unknown component survived: %#v", transition)
		}
	}
	if sanitized[len(sanitized)-1].Component != "host" {
		t.Fatalf("newest transition was not retained: %#v", sanitized[len(sanitized)-1])
	}
	if diagnostics.SanitizeHealthTransitions(nil) != nil {
		t.Fatal("nil transitions should sanitize to nil")
	}
	if diagnostics.SanitizeHealthTransitions([]diagnostics.HealthTransition{{At: "2026-09-15T12:00:00Z", Component: "agent", To: "https://example.invalid"}}) != nil {
		t.Fatal("hostile state should sanitize to nil")
	}
}

func TestSupportSnapshotCarriesOnlySanitizedTransitionsAndIdentity(t *testing.T) {
	payload, err := diagnostics.MarshalSupportSnapshot(diagnostics.SupportSnapshotRuntime{
		Identity: &diagnostics.RunIdentity{
			BootID: "d1b2c3d4-1111-2222-3333-444455556666",
			RunID:  "run-0123456789abcdef0123456789abcdef",
		},
		HealthTransitions: []diagnostics.HealthTransition{
			{At: "2026-09-15T12:00:00Z", Component: "agent", From: "unknown", To: "ready"},
			{At: "2026-09-15T12:00:01Z", Component: "agent", To: "Authorization: Bearer bearer-token-value"},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	if !strings.Contains(text, "run-0123456789abcdef0123456789abcdef") || !strings.Contains(text, `"agent"`) {
		t.Fatalf("valid identity or transition missing: %s", text)
	}
	for _, forbidden := range []string{"bearer-token-value", "Authorization"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("support snapshot leaked %q: %s", forbidden, text)
		}
	}
	var snapshot diagnostics.SupportSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Identity == nil || snapshot.Identity.RunID != "run-0123456789abcdef0123456789abcdef" {
		t.Fatalf("snapshot identity = %#v", snapshot.Identity)
	}
	if len(snapshot.HealthTransitions) != 1 || snapshot.HealthTransitions[0].To != "ready" {
		t.Fatalf("snapshot transitions = %#v", snapshot.HealthTransitions)
	}
}
