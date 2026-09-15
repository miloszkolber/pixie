package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/ownerlock"
)

// TestOperatorRecoveryContractAcrossCmdPackages checks the boundary a launcher
// actually uses: resolve the native agent directory, probe the real owner lock,
// then project a bounded recovery report that never exposes a path.
func TestOperatorRecoveryContractAcrossCmdPackages(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}

	lock, err := ownerlock.Acquire(agentDir, "pixie_full")
	if err != nil {
		t.Fatal(err)
	}
	if held := diagnostics.OwnerLockHeld(agentDir); held == nil || !*held {
		t.Fatalf("held lock was not detected: %#v", held)
	}
	report := diagnostics.AssessRecovery(diagnostics.RecoveryFacts{
		ConfigReadable:   boolAddr(true),
		HostConfigured:   boolAddr(true),
		HostReachable:    boolAddr(false),
		AgentDirWritable: boolAddr(true),
		OwnerLockHeld:    diagnostics.OwnerLockHeld(agentDir),
		RestartCount:     0,
	})
	if report.Summary != "attention" {
		t.Fatalf("summary = %q", report.Summary)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if held := diagnostics.OwnerLockHeld(agentDir); held == nil || *held {
		t.Fatalf("released lock still reported held: %#v", held)
	}

	for _, check := range report.Checks {
		line := diagnostics.FormatRecoveryCheck(check)
		for _, forbidden := range []string{agentDir, dir, "Bearer ", "sk_live", "https://"} {
			if strings.Contains(line, forbidden) {
				t.Fatalf("recovery line leaked %q: %s", forbidden, line)
			}
		}
	}
}

func boolAddr(value bool) *bool { return &value }
