package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseArgsAndReadConfig(t *testing.T) {
	command, config, err := parseArgs([]string{"serve", "--config", "/tmp/pixie-assistant.json"})
	if err != nil || command != "serve" || config != "/tmp/pixie-assistant.json" {
		t.Fatalf("parseArgs = %q, %q, %v", command, config, err)
	}
	command, config, err = parseArgs([]string{"--config", "/tmp/pixie-assistant.json"})
	if err != nil || command != "serve" || config != "/tmp/pixie-assistant.json" {
		t.Fatalf("option-first parseArgs = %q, %q, %v", command, config, err)
	}
	path := filepath.Join(t.TempDir(), "assistant.json")
	if err := os.WriteFile(path, []byte(`{"host":"127.0.0.1","port":3284,"agentDir":"/tmp/pi"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := readConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Host != "127.0.0.1" || loaded.Port != 3284 || loaded.AgentDir != "/tmp/pi" {
		t.Fatalf("loaded config = %#v", loaded)
	}
}

func TestReadConfigRequiresAbsolutePath(t *testing.T) {
	if _, err := readConfig("assistant.json"); err == nil {
		t.Fatal("relative config path was accepted")
	}
}

func TestExpandHomePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got := expandHomePath("~/agent"); got != filepath.Join(home, "agent") {
		t.Fatalf("expandHomePath = %q", got)
	}
	if got := expandHomePath("~other/agent"); got != "~other/agent" {
		t.Fatalf("expandHomePath expanded another user's path: %q", got)
	}
}
