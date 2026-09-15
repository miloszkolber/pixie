package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseModeDefaultsToController(t *testing.T) {
	mode, err := parseMode(nil)
	if err != nil || mode != modeController {
		t.Fatalf("parseMode(nil) = %q, %v", mode, err)
	}
	mode, err = parseMode([]string{"serve"})
	if err != nil || mode != modeController {
		t.Fatalf("parseMode(serve) = %q, %v", mode, err)
	}
}

func TestParseModeAcceptsExplicitController(t *testing.T) {
	mode, err := parseMode([]string{"serve", "--mode=controller", "--config", "/tmp/pixie.json"})
	if err != nil || mode != modeController {
		t.Fatalf("parseMode(controller) = %q, %v", mode, err)
	}
	mode, err = parseMode([]string{"serve", "--mode", "controller"})
	if err != nil || mode != modeController {
		t.Fatalf("parseMode(--mode controller) = %q, %v", mode, err)
	}
	if _, err := parseMode([]string{"serve", "--mode=controller", "--mode=controller"}); err == nil {
		t.Fatal("duplicate mode was accepted")
	}
}

func TestParseModeRejectsFullHost(t *testing.T) {
	cases := [][]string{
		{"serve", "--mode=full-host"},
		{"serve", "--mode", "full-host"},
		{"serve", "--mode=full-host", "--config", "/tmp/pixie.json"},
	}
	for _, args := range cases {
		mode, err := parseMode(args)
		if err == nil {
			t.Fatalf("parseMode(%q) = %q, want unsupported mode error", args, mode)
		}
		if !strings.Contains(err.Error(), "unsupported serve mode") {
			t.Fatalf("parseMode(%q) error = %q, want unsupported serve mode", args, err)
		}
	}
}

func TestRuntimeConfigRejectsUnsupportedMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pixie.json")
	if err := os.WriteFile(path, []byte(`{"mode":"controller","host":"127.0.0.1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runtimeConfigFor(path, modeController); err != nil {
		t.Fatalf("controller configuration was rejected: %v", err)
	}
	fullHostPath := filepath.Join(t.TempDir(), "pixie.json")
	if err := os.WriteFile(fullHostPath, []byte(`{"mode":"full-host","host":"127.0.0.1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runtimeConfigFor(fullHostPath, modeController); err == nil {
		t.Fatal("full-host configuration was accepted in controller-only mode")
	} else if !strings.Contains(err.Error(), "unsupported config mode") {
		t.Fatalf("full-host config error = %q, want unsupported config mode", err)
	}
	if _, err := runtimeConfigFor("relative.json", modeController); err == nil {
		t.Fatal("relative configuration path was accepted")
	}
}

func TestRuntimeConfigExpandsHomePaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pixie.json")
	if err := os.WriteFile(path, []byte(`{"mode":"controller","dataDir":"~/.local/share/pixie"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := runtimeConfigFor(path, modeController)
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
	executableLookup := func(key string) (string, bool) {
		if key == "PIXIE_PI_EXECUTABLE" {
			return "/usr/local/bin/pi", true
		}
		return "", false
	}
	if err := rejectControllerAssistantSettings(executableLookup); err == nil {
		t.Fatal("controller accepted a piExecutable from the environment")
	}
	if err := rejectControllerAssistantSettings(func(string) (string, bool) { return "", false }); err != nil {
		t.Fatalf("controller rejected an empty environment: %v", err)
	}
	if err := rejectControllerConfigAssistantSettings(runtimeConfigFile{AgentDir: "/tmp/pi"}); err == nil {
		t.Fatal("controller accepted an agentDir from configuration")
	}
	if err := rejectControllerConfigAssistantSettings(runtimeConfigFile{PiExecutable: "/usr/local/bin/pi"}); err == nil {
		t.Fatal("controller accepted a piExecutable from configuration")
	}
	if err := rejectControllerConfigAssistantSettings(runtimeConfigFile{}); err != nil {
		t.Fatalf("controller rejected a clean configuration: %v", err)
	}
}

func TestControllerRejectsSharedAssistantEnv(t *testing.T) {
	for _, key := range []string{"PIXIE_PI_PACKAGE", "PIXIE_ASSISTANT_PORT", "PIXIE_ASSISTANT_HOST"} {
		lookup := func(candidate string) (string, bool) {
			if candidate == key {
				return "/tmp/shared-value", true
			}
			return "", false
		}
		if err := rejectControllerAssistantSettings(lookup); err == nil {
			t.Fatalf("controller accepted shared setting %s", key)
		} else if !strings.Contains(err.Error(), key) {
			t.Fatalf("shared setting error %q does not name %s", err, key)
		}
	}
	validLookup := func(key string) (string, bool) {
		if key == "PIXIE_PI_PORT" {
			return "3284", true
		}
		if key == "PIXIE_PI_URL" {
			return "ws://127.0.0.1:3284/pi", true
		}
		return "", false
	}
	if err := rejectControllerAssistantSettings(validLookup); err != nil {
		t.Fatalf("controller rejected dial knobs PIXIE_PI_PORT/PIXIE_PI_URL: %v", err)
	}
}

func TestRunUtilityCommandIsReadOnlyAndConfigBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pixie.json")
	if err := os.WriteFile(path, []byte(`{"mode":"controller"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var doctor bytes.Buffer
	if err := runUtilityCommand("doctor", path, &doctor); err != nil {
		t.Fatal(err)
	}
	if got, want := doctor.String(), "pixie_web doctor: configuration is readable ("+path+")\n"; got != want {
		t.Fatalf("doctor output = %q, want %q", got, want)
	}
	var uninstall bytes.Buffer
	if err := runUtilityCommand("uninstall", path, &uninstall); err != nil {
		t.Fatal(err)
	}
	if got, want := uninstall.String(), "pixie_web uninstall: stop and remove the selected user unit and binary\n"; got != want {
		t.Fatalf("uninstall output = %q, want %q", got, want)
	}
}
