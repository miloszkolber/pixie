package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseModeDefaultsToFullHostAndAcceptsController(t *testing.T) {
	mode, err := parseMode(nil)
	if err != nil || mode != modeFullHost {
		t.Fatalf("parseMode(nil) = %q, %v", mode, err)
	}
	mode, err = parseMode([]string{"serve", "--mode=controller", "--config", "/tmp/pixie.json"})
	if err != nil || mode != modeController {
		t.Fatalf("parseMode(controller) = %q, %v", mode, err)
	}
	if _, err := parseMode([]string{"serve", "--mode=full-host", "--mode=controller"}); err == nil {
		t.Fatal("duplicate mode was accepted")
	}
}

func TestRuntimeConfigRejectsModeTopologyMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pixie.json")
	if err := os.WriteFile(path, []byte(`{"mode":"controller","host":"127.0.0.1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runtimeConfigFor(path, modeFullHost); err == nil {
		t.Fatal("full-host accepted controller configuration")
	}
	if _, err := runtimeConfigFor("relative.json", modeController); err == nil {
		t.Fatal("relative configuration path was accepted")
	}
}

func TestRuntimeConfigExpandsHomePaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pixie.json")
	if err := os.WriteFile(path, []byte(`{"mode":"full-host","dataDir":"~/.local/share/pixie"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := runtimeConfigFor(path, modeFullHost)
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".local", "share", "pixie"); config.DataDir != want {
		t.Fatalf("dataDir = %q, want %q", config.DataDir, want)
	}
}

func TestControllerRejectsLocalAssistantSettings(t *testing.T) {
	lookup := func(key string) (string, bool) {
		if key == "PI_CODING_AGENT_DIR" {
			return "/tmp/pi", true
		}
		return "", false
	}
	if err := rejectControllerAssistantSettings(lookup); err == nil {
		t.Fatal("controller accepted a local assistant setting")
	}
	if err := rejectControllerConfigAssistantSettings(runtimeConfigFile{AgentDir: "/tmp/pi"}); err == nil {
		t.Fatal("controller accepted an agentDir from configuration")
	}
	if err := rejectControllerConfigAssistantSettings(runtimeConfigFile{PiExecutable: "/usr/local/bin/pi"}); err == nil {
		t.Fatal("controller accepted a piExecutable from configuration")
	}
}

func TestFullHostRequiresResolvedPiSelection(t *testing.T) {
	if _, err := validateFullHostPiSelection("", "/usr/local/bin/pi"); err == nil {
		t.Fatal("missing agent directory was accepted")
	}
	if _, err := validateFullHostPiSelection("relative/agent", "/usr/local/bin/pi"); err == nil {
		t.Fatal("relative agent directory was accepted")
	}
	if _, err := validateFullHostPiSelection(t.TempDir(), "definitely-not-a-pi-binary"); err == nil {
		t.Fatal("missing Pi executable was accepted")
	}
	if _, err := validateFullHostPiSelection(t.TempDir(), "sh"); err != nil {
		t.Fatalf("resolvable Pi executable was rejected: %v", err)
	}
}

func TestFullHostWaitsOnAssistantLifecycle(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitFullHost(cancelled, make(chan error), make(chan error), make(chan struct{})); err != nil {
		t.Fatal(err)
	}
	assistantErr := make(chan error, 1)
	assistantErr <- errors.New("native engine lost")
	err := waitFullHost(context.Background(), make(chan error), assistantErr, make(chan struct{}))
	if err == nil || !strings.Contains(err.Error(), "native engine lost") {
		t.Fatalf("assistant failure was not joined: %v", err)
	}
	restart := make(chan struct{})
	close(restart)
	if err := waitFullHost(context.Background(), make(chan error), make(chan error), restart); !errors.Is(err, errRestartRequested) {
		t.Fatalf("restart signal = %v, want errRestartRequested", err)
	}
}

func TestRunUtilityCommandIsReadOnlyAndConfigBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pixie.json")
	if err := os.WriteFile(path, []byte(`{"mode":"full-host"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runUtilityCommand("doctor", path); err != nil {
		t.Fatal(err)
	}
	if err := runUtilityCommand("uninstall", path); err != nil {
		t.Fatal(err)
	}
}
