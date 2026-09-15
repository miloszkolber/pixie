package controller

import (
	"strings"
	"testing"
)

func TestRuntimeDiagnosticsSanitizesHostDiagnosticReason(t *testing.T) {
	secret := "support-secret-value"
	path := "/home/alice/.pi/agent/config.json"
	report := runtimeDiagnosticsSnapshot(nil, nil, map[string]any{
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
	report := runtimeDiagnosticsSnapshot(nil, nil, map[string]any{
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
