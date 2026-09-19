//go:build linux

package ownerlock

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireSerializesCanonicalAgentDirectoryAndWritesSafeDiagnostic(t *testing.T) {
	agentDir := filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(filepath.Dir(agentDir), "agent-alias")
	if err := os.Symlink(agentDir, alias); err != nil {
		t.Fatal(err)
	}
	canonical, err := ResolveAgentDir(alias, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	first, err := Acquire(canonical, "pixie")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if want := filepath.Join(agentDir, "pixie", "owner.lock"); first.Path() != want {
		t.Fatalf("lock path = %q, want %q", first.Path(), want)
	}
	info, err := os.Stat(first.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("owner lock mode = %o, want 600", got)
	}
	record, err := os.ReadFile(first.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(record), `"product":"pixie"`) || strings.Contains(string(record), agentDir) {
		t.Fatalf("owner diagnostic must contain only safe ownership facts: %q", record)
	}
	if _, err := Acquire(canonical, "pixie"); err == nil {
		t.Fatal("second owner acquired an active lock")
	} else if !IsContended(err) || ExitCode(err) != ContentionExitCode {
		t.Fatalf("second owner error = %v, want contention status %d", err, ContentionExitCode)
	}
}

func TestResolveAgentDirUsesNativeOverrideThenServiceConfigThenHome(t *testing.T) {
	base := t.TempDir()
	envAgent := filepath.Join(base, "native")
	configAgent := filepath.Join(base, "config")
	home := filepath.Join(base, "home")
	lookup := func(key string) (string, bool) {
		switch key {
		case "PI_CODING_AGENT_DIR":
			return envAgent, true
		case "HOME":
			return home, true
		default:
			return "", false
		}
	}
	resolved, err := ResolveAgentDir(configAgent, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != envAgent {
		t.Fatalf("native agent directory = %q, want %q", resolved, envAgent)
	}
	resolved, err = ResolveAgentDir(configAgent, func(key string) (string, bool) {
		if key == "HOME" {
			return home, true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved != configAgent {
		t.Fatalf("service agent directory = %q, want %q", resolved, configAgent)
	}
	resolved, err = ResolveAgentDir("", func(key string) (string, bool) {
		if key == "HOME" {
			return home, true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".pi", "agent"); resolved != want {
		t.Fatalf("home agent directory = %q, want %q", resolved, want)
	}
	if _, err := ResolveAgentDir("relative", func(string) (string, bool) { return "", false }); err == nil {
		t.Fatal("relative service agent directory was accepted")
	}
}

func TestPixieServeHoldsOneHandoffLock(t *testing.T) {
	agentDir := filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	lock, err := Acquire(agentDir, "pixie")
	if err != nil {
		t.Fatalf("pixie serve did not acquire its Pi ownership lock: %v", err)
	}
	_, err = Acquire(agentDir, "pixie")
	if !IsContended(err) || ExitCode(err) != 73 {
		t.Fatalf("second pixie serve did not hand off with status 73: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}
