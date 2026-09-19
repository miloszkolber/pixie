package diagnostics_test

import (
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/diagnostics"
)

func boolAddr(value bool) *bool { return &value }

func TestAssessRecoveryReportsEachFailureModeWithRemediation(t *testing.T) {
	report := diagnostics.AssessRecovery(diagnostics.RecoveryFacts{
		ConfigReadable:   boolAddr(false),
		HostConfigured:   boolAddr(false),
		PortMismatch:     boolAddr(true),
		PiPackagePresent: boolAddr(true),
		PiPackageValid:   boolAddr(false),
		AgentDirWritable: boolAddr(false),
		OwnerLockHeld:    boolAddr(true),
		RestartCount:     diagnostics.RestartLoopThreshold,
	})
	if report.Summary != "attention" {
		t.Fatalf("summary = %q", report.Summary)
	}
	wantCodes := map[string]string{
		"config.readable":         "config.unreadable",
		"host.ready":              "host.not_configured",
		"host.port":               "host.port_mismatch",
		"pi_package":              "pi_package.invalid",
		"agent_dir.writable":      "agent_dir.not_writable",
		"owner_lock":              "owner_lock.held",
		"supervisor.restart_loop": "supervisor.restart_loop",
	}
	for _, check := range report.Checks {
		want, ok := wantCodes[check.ID]
		if !ok {
			t.Fatalf("unexpected check %q", check.ID)
		}
		if check.Code != want {
			t.Fatalf("check %s code = %q, want %q", check.ID, check.Code, want)
		}
		if check.Status != diagnostics.RecoveryFailed && check.Status != diagnostics.RecoveryDegraded {
			t.Fatalf("check %s status = %q", check.ID, check.Status)
		}
		if check.Remediation == "" {
			t.Fatalf("check %s has no remediation", check.ID)
		}
	}
}

func TestAssessRecoveryDistinguishesUnknownFromHealthy(t *testing.T) {
	unknown := diagnostics.AssessRecovery(diagnostics.RecoveryFacts{RestartCount: -1})
	if unknown.Summary != "incomplete" {
		t.Fatalf("unknown summary = %q", unknown.Summary)
	}
	for _, check := range unknown.Checks {
		if check.Status != diagnostics.RecoveryUnknown {
			t.Fatalf("check %s status = %q, want unknown", check.ID, check.Status)
		}
	}

	healthy := diagnostics.AssessRecovery(diagnostics.RecoveryFacts{
		ConfigReadable:   boolAddr(true),
		HostConfigured:   boolAddr(true),
		HostReachable:    boolAddr(true),
		HostReady:        boolAddr(true),
		PortMismatch:     boolAddr(false),
		PiPackagePresent: boolAddr(true),
		PiPackageValid:   boolAddr(true),
		AgentDirWritable: boolAddr(true),
		OwnerLockHeld:    boolAddr(false),
		RestartCount:     0,
	})
	if healthy.Summary != diagnostics.RecoveryOK {
		t.Fatalf("healthy summary = %q: %#v", healthy.Summary, healthy.Checks)
	}
	for _, check := range healthy.Checks {
		if check.Status != diagnostics.RecoveryOK {
			t.Fatalf("check %s status = %q, want ok", check.ID, check.Status)
		}
	}
}

func TestRecoveryFormatCarriesNoSecretsOrPaths(t *testing.T) {
	report := diagnostics.AssessRecovery(diagnostics.RecoveryFacts{HostConfigured: boolAddr(false), RestartCount: 0})
	for _, check := range report.Checks {
		line := diagnostics.FormatRecoveryCheck(check)
		for _, forbidden := range []string{"Bearer ", "sk_live", "https://", "/home/", "/var/", "C:\\", "token="} {
			if strings.Contains(line, forbidden) {
				t.Fatalf("recovery line leaked %q: %s", forbidden, line)
			}
		}
		if strings.Contains(line, "\n") {
			t.Fatalf("recovery line is not bounded to one line: %q", line)
		}
	}
}
