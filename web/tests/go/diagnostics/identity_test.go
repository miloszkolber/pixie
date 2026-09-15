package diagnostics_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/diagnostics"
)

func TestRunIdentityGeneratesStableBootAndPerRunValues(t *testing.T) {
	entropy := bytes.NewReader(bytes.Repeat([]byte{0xab}, 64))
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	identity := diagnostics.NewRunIdentity(now, "d1b2c3d4-1111-2222-3333-444455556666", entropy)
	if !strings.HasPrefix(identity.BootID, "d1b2c3d4-") || !strings.HasPrefix(identity.RunID, "run-") {
		t.Fatalf("generated identity = %#v", identity)
	}
	if identity.StartedAt != "2026-09-15T12:00:00Z" {
		t.Fatalf("startedAt = %q", identity.StartedAt)
	}
	second := diagnostics.NewRunIdentity(now, "d1b2c3d4-1111-2222-3333-444455556666", bytes.NewReader(bytes.Repeat([]byte{0xcd}, 64)))
	if second.RunID == identity.RunID {
		t.Fatalf("per-run identity did not change: %q", identity.RunID)
	}
	if second.BootID != identity.BootID {
		t.Fatalf("boot identity changed within one boot: %q vs %q", identity.BootID, second.BootID)
	}
	if environment := identity.Environment(); environment[diagnostics.BootIdentityEnvironment] != identity.BootID || environment[diagnostics.RunIdentityEnvironment] != identity.RunID {
		t.Fatalf("environment = %#v", environment)
	}
}

func TestRunIdentityRejectsHostileValues(t *testing.T) {
	hostile := []string{
		"Bearer bearer-token-value",
		"sk_live_hostile-api-token-value",
		"https://url-user:url-password@example.invalid/run",
		"/home/alice/.pi/agent",
		`C:\Users\alice\.pi`,
		"AKIAIOSFODNN7EXAMPLE",
		"token=opaque-credential-value",
		"../../../etc/passwd",
	}
	for _, value := range hostile {
		if sanitized, ok := diagnostics.SanitizeRunIdentity(diagnostics.RunIdentity{BootID: value, RunID: value}); ok {
			t.Fatalf("hostile identity %q was accepted as %#v", value, sanitized)
		}
	}
	lookup := func(key string) (string, bool) {
		switch key {
		case diagnostics.BootIdentityEnvironment:
			return "Bearer bearer-token-value", true
		case diagnostics.RunIdentityEnvironment:
			return "/home/alice/.pi", true
		default:
			return "", false
		}
	}
	if identity := diagnostics.RunIdentityFromEnvironment(lookup); !identity.IsZero() {
		t.Fatalf("hostile environment identity = %#v", identity)
	}
	if attributes := diagnostics.RunIdentityLoggerAttributes(diagnostics.RunIdentity{BootID: "/home/alice", RunID: "Bearer x"}); len(attributes) != 0 {
		t.Fatalf("hostile logger attributes = %#v", attributes)
	}
}

func TestRunIdentityEnvironmentRoundTripsValidValues(t *testing.T) {
	lookup := func(key string) (string, bool) {
		switch key {
		case diagnostics.BootIdentityEnvironment:
			return "D1B2C3D4-1111-2222-3333-444455556666", true
		case diagnostics.RunIdentityEnvironment:
			return "run-0123456789abcdef0123456789abcdef", true
		default:
			return "", false
		}
	}
	identity := diagnostics.RunIdentityFromEnvironment(lookup)
	if identity.BootID != "d1b2c3d4-1111-2222-3333-444455556666" || identity.RunID != "run-0123456789abcdef0123456789abcdef" {
		t.Fatalf("environment identity = %#v", identity)
	}
	if len(diagnostics.RunIdentityLoggerAttributes(identity)) != 4 {
		t.Fatalf("logger attributes = %#v", diagnostics.RunIdentityLoggerAttributes(identity))
	}
}

func TestSetProcessRunIdentityRejectsInvalidValues(t *testing.T) {
	diagnostics.SetProcessRunIdentity(diagnostics.RunIdentity{BootID: "bearer-token", RunID: "https://example.invalid"})
	t.Cleanup(func() { diagnostics.SetProcessRunIdentity(diagnostics.RunIdentity{}) })
	if identity := diagnostics.ProcessRunIdentity(); !identity.IsZero() {
		t.Fatalf("invalid process identity was stored: %#v", identity)
	}
	diagnostics.SetProcessRunIdentity(diagnostics.RunIdentity{RunID: "run-0123456789abcdef0123456789abcdef"})
	if identity := diagnostics.ProcessRunIdentity(); identity.RunID != "run-0123456789abcdef0123456789abcdef" {
		t.Fatalf("valid process identity = %#v", identity)
	}
}
