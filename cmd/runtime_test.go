package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
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
	if _, err := readRuntimeConfig(path); err != nil {
		t.Fatalf("controller configuration was rejected: %v", err)
	}
	fullHostPath := filepath.Join(t.TempDir(), "pixie.json")
	if err := os.WriteFile(fullHostPath, []byte(`{"mode":"full-host","host":"127.0.0.1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRuntimeConfig(fullHostPath); err == nil {
		t.Fatal("full-host configuration was accepted in controller-only mode")
	} else if !strings.Contains(err.Error(), "unsupported config mode") {
		t.Fatalf("full-host config error = %q, want unsupported config mode", err)
	}
	if _, err := readRuntimeConfig("relative.json"); err == nil {
		t.Fatal("relative configuration path was accepted")
	}
}

func TestRuntimeConfigExpandsHomePaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pixie.json")
	if err := os.WriteFile(path, []byte(`{"mode":"controller","dataDir":"~/.local/share/pixie"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := readRuntimeConfig(path)
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

func TestControllerConfigAcceptsDefaultsAndPortBoundaries(t *testing.T) {
	for _, content := range []string{`{}`, `{"port":1}`, `{"port":65535,"mode":"controller"}`} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readRuntimeConfig(path); err != nil {
			t.Fatalf("rejected valid config %s: %v", content, err)
		}
	}
}

func TestControllerConfigRejectsInvalidValuesAcrossCommands(t *testing.T) {
	cases := map[string]string{
		"top-level null":       `null`,
		"top-level array":      `[]`,
		"unknown field":        `{"mode":"controller","unexpected":true}`,
		"capitalized field":    `{"Host":"127.0.0.1"}`,
		"duplicate field":      `{"host":"first","host":"second"}`,
		"duplicate null field": `{"host":null,"host":"127.0.0.1"}`,
		"null mode":            `{"mode":null}`,
		"null data directory":  `{"dataDir":null}`,
		"trailing JSON value":  `{"mode":"controller"}{}`,
		"empty mode":           `{"mode":""}`,
		"host mode":            `{"mode":"full-host"}`,
		"zero port":            `{"port":0}`,
		"negative port":        `{"port":-1}`,
		"oversized port":       `{"port":65536}`,
		"empty agentDir":       `{"agentDir":""}`,
		"empty piExecutable":   `{"piExecutable":""}`,
		"agentDir":             `{"agentDir":"/tmp/pi"}`,
		"piExecutable":         `{"piExecutable":"/usr/local/bin/pi"}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pixie.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readRuntimeConfig(path); err == nil {
				t.Fatalf("runtime configuration accepted %s", name)
			}
			var doctor bytes.Buffer
			if err := runUtilityCommand("doctor", path, &doctor); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(doctor.String(), "config.readable=failed") {
				t.Fatalf("doctor accepted %s: %s", name, doctor.String())
			}
			if err := runUtilityCommand("uninstall", path, &bytes.Buffer{}); err == nil {
				t.Fatalf("uninstall accepted %s", name)
			}
		})
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
	output := doctor.String()
	if !strings.Contains(output, "pixie_web doctor: summary=") || !strings.Contains(output, "config.readable=ok") || !strings.Contains(output, "host.ready=") {
		t.Fatalf("doctor output = %q", output)
	}
	if strings.Contains(output, path) || strings.Contains(output, t.TempDir()) {
		t.Fatalf("doctor output leaked a path: %q", output)
	}
	var uninstall bytes.Buffer
	if err := runUtilityCommand("uninstall", path, &uninstall); err != nil {
		t.Fatal(err)
	}
	if got, want := uninstall.String(), "pixie_web uninstall: stop and remove the selected user unit and binary\n"; got != want {
		t.Fatalf("uninstall output = %q, want %q", got, want)
	}
	var rejected bytes.Buffer
	if err := runUtilityCommand("uninstall", "relative.json", &rejected); err == nil {
		t.Fatal("uninstall accepted a relative config path")
	}
	if err := runUtilityCommand("uninstall", filepath.Join(t.TempDir(), "missing.json"), &rejected); err == nil {
		t.Fatal("uninstall accepted a missing config file")
	}
	assistantConfig := filepath.Join(t.TempDir(), "assistant.json")
	if err := os.WriteFile(assistantConfig, []byte(`{"agentDir":"/tmp/pi"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var assistantDoctor bytes.Buffer
	if err := runUtilityCommand("doctor", assistantConfig, &assistantDoctor); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(assistantDoctor.String(), "config.readable=failed code=config.unreadable") {
		t.Fatalf("doctor accepted an assistant configuration: %q", assistantDoctor.String())
	}
	if err := runUtilityCommand("uninstall", assistantConfig, &rejected); err == nil {
		t.Fatal("uninstall accepted an assistant configuration")
	}
}

func TestControllerDoctorReportsUnreadableConfigWithoutExposingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	var doctor bytes.Buffer
	if err := runUtilityCommand("doctor", path, &doctor); err != nil {
		t.Fatal(err)
	}
	output := doctor.String()
	if !strings.Contains(output, "config.readable=failed code=config.unreadable") {
		t.Fatalf("doctor output = %q", output)
	}
	if strings.Contains(output, path) {
		t.Fatalf("doctor output leaked the config path: %q", output)
	}
}

func TestControllerDoctorFlagsDialPortMismatch(t *testing.T) {
	t.Setenv("PIXIE_PI_PORT", "3284")
	t.Setenv("PIXIE_PI_URL", "ws://127.0.0.1:9999/pi")
	facts, err := controllerRecoveryFacts("", os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	if facts.PortMismatch == nil || !*facts.PortMismatch {
		t.Fatalf("port mismatch = %#v", facts.PortMismatch)
	}
	t.Setenv("PIXIE_PI_URL", "ws://127.0.0.1:3284/pi")
	facts, err = controllerRecoveryFacts("", os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	if facts.PortMismatch == nil || *facts.PortMismatch {
		t.Fatalf("matching ports reported a mismatch: %#v", facts.PortMismatch)
	}
}

// fakeControllerRuntime records the entrypoint's shutdown ordering so the test
// can prove drain precedes shutdown and is bounded.
type fakeControllerRuntime struct {
	mu           sync.Mutex
	events       []string
	errors       chan error
	started      chan struct{}
	drainEntered chan struct{}
	releaseDrain chan struct{}
	startOnce    sync.Once
	drainOnce    sync.Once
	bounded      bool
}

func (fake *fakeControllerRuntime) record(event string) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.events = append(fake.events, event)
}

func (fake *fakeControllerRuntime) snapshot() []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]string(nil), fake.events...)
}

func (fake *fakeControllerRuntime) hasEvent(event string) bool {
	for _, recorded := range fake.snapshot() {
		if recorded == event {
			return true
		}
	}
	return false
}

func (fake *fakeControllerRuntime) Start() (string, error) {
	fake.record("start")
	fake.startOnce.Do(func() {
		if fake.started != nil {
			close(fake.started)
		}
	})
	return "http://127.0.0.1:0", nil
}

func (fake *fakeControllerRuntime) Errors() <-chan error { return fake.errors }

func (fake *fakeControllerRuntime) BeginDrain() { fake.record("beginDrain") }

func (fake *fakeControllerRuntime) WaitForDrain(ctx context.Context) {
	fake.record("waitForDrain")
	if _, ok := ctx.Deadline(); ok {
		fake.mu.Lock()
		fake.bounded = true
		fake.mu.Unlock()
	}
	fake.drainOnce.Do(func() {
		if fake.drainEntered != nil {
			close(fake.drainEntered)
		}
	})
	if fake.releaseDrain != nil {
		select {
		case <-fake.releaseDrain:
		case <-ctx.Done():
			fake.record("waitForDrainExpired")
			return
		}
	}
	fake.record("drained")
}

func (fake *fakeControllerRuntime) Shutdown(context.Context) error {
	fake.record("shutdown")
	return nil
}

// A service-manager stop (the update/rollback path) must request quiesce, wait
// for in-flight work, and only then shut down. Shutdown must not race the drain.
func TestServeControllerDrainsOnSignalBeforeShutdown(t *testing.T) {
	fake := &fakeControllerRuntime{
		errors:       make(chan error),
		started:      make(chan struct{}),
		drainEntered: make(chan struct{}),
		releaseDrain: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveController(ctx, fake) }()
	select {
	case <-fake.started:
	case <-time.After(2 * time.Second):
		t.Fatal("controller runtime was never started")
	}
	cancel()
	select {
	case <-fake.drainEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("drain was not requested after the stop signal")
	}
	if fake.hasEvent("shutdown") {
		t.Fatal("shutdown ran before the drain settled")
	}
	close(fake.releaseDrain)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveController() = %v, want a clean quiesce", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveController did not return after the drain settled")
	}
	want := []string{"start", "beginDrain", "waitForDrain", "drained", "shutdown"}
	if got := fake.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("shutdown sequence = %v, want %v", got, want)
	}
}

// A drain that does not settle must remain bounded and must not turn a normal
// update/rollback stop into an error.
func TestDrainForUpdateIsBounded(t *testing.T) {
	blocked := make(chan struct{})
	fake := &fakeControllerRuntime{errors: make(chan error), releaseDrain: blocked}
	started := time.Now()
	drainForUpdate(fake, 100*time.Millisecond)
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("bounded drain blocked for %s", elapsed)
	}
	if !fake.bounded {
		t.Fatal("drain wait did not receive a deadline")
	}
	if !fake.hasEvent("beginDrain") || !fake.hasEvent("waitForDrain") {
		t.Fatalf("drain sequence = %v", fake.snapshot())
	}
	if !fake.hasEvent("waitForDrainExpired") {
		t.Fatalf("drain did not report bounded expiry: %v", fake.snapshot())
	}
}
