package main

import (
	"os"
	"path/filepath"
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
