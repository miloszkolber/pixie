package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/ownerlock"
)

func TestParseArgumentsAcceptsOnlyCombinedCommandSurface(t *testing.T) {
	arguments, err := parseArguments([]string{
		"serve", "--assistant-config", "/tmp/assistant.json", "--web-config", "/tmp/web.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if arguments.command != commandServe || arguments.assistantConfig != "/tmp/assistant.json" || arguments.webConfig != "/tmp/web.json" {
		t.Fatalf("parseArguments returned %#v", arguments)
	}
	for _, values := range [][]string{
		{"version"},
		{"-h"},
		{"serve", "--assistant-config=/tmp/assistant.json", "--web-config", "/tmp/web.json", "extra"},
		{"serve", "--assistant-config", "assistant.json", "--web-config", "/tmp/web.json"},
		{"serve", "--assistant-config", "/tmp/assistant.json", "--version", "/tmp/web.json"},
	} {
		if _, err := parseArguments(values); err == nil {
			t.Fatalf("parseArguments(%q) accepted an unsupported form", values)
		}
	}
	for _, values := range [][]string{{"--version"}, {"--help"}} {
		if _, err := parseArguments(values); err != nil {
			t.Fatalf("parseArguments(%q): %v", values, err)
		}
	}
}

func TestReadAssistantEndpointChecksConfigAndHostOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assistant.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":2,"host":"localhost","port":3289,"agentDir":"/var/lib/pi","allowSelfRestart":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	lookup := func(key string) (string, bool) {
		switch key {
		case "PIXIE_ASSISTANT_HOST":
			return "127.0.0.1", true
		case "PIXIE_ASSISTANT_PORT":
			return "3290", true
		default:
			return "", false
		}
	}
	endpoint, err := readAssistantEndpoint(path, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != (assistantEndpoint{host: "127.0.0.1", port: 3289, agentDir: "/var/lib/pi"}) {
		t.Fatalf("endpoint = %#v, want config port with loopback host override", endpoint)
	}

	for _, config := range []string{
		`{"schemaVersion":2,"host":"0.0.0.0","port":3284,"agentDir":"/var/lib/pi","allowSelfRestart":false}`,
		`{"schemaVersion":2,"host":" localhost ","port":3284,"agentDir":"/var/lib/pi","allowSelfRestart":false}`,
		`{"schemaVersion":2,"host":"127.0.0.1","port":0,"agentDir":"/var/lib/pi","allowSelfRestart":false}`,
		`{"schemaVersion":2,"host":"127.0.0.1","port":3284,"agentDir":"relative","allowSelfRestart":false}`,
		`{"schemaVersion":2,"host":"127.0.0.1","agentDir":"/var/lib/pi","allowSelfRestart":false}`,
		`{"schemaVersion":2,"host":"127.0.0.1","port":3284,"agentDir":"/var/lib/pi","allowSelfRestart":false,"piPackage":"/outside/pi"}`,
	} {
		if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readAssistantEndpoint(path, func(string) (string, bool) { return "", false }); err == nil {
			t.Fatalf("invalid assistant configuration was accepted: %s", config)
		}
	}
}

func TestRequireSecretRequiresInheritedMinimum(t *testing.T) {
	if _, err := requireSecret(func(string) (string, bool) { return "", false }); err == nil {
		t.Fatal("missing secret was accepted")
	}
	if _, err := requireSecret(func(string) (string, bool) { return strings.Repeat("x", 31), true }); err == nil {
		t.Fatal("short secret was accepted")
	}
	secret := strings.Repeat("x", 32)
	resolved, err := requireSecret(func(string) (string, bool) { return secret, true })
	if err != nil || resolved != secret {
		t.Fatalf("requireSecret() = %q, %v", resolved, err)
	}
}

func TestArchivePayloadValidationRejectsTraversalAndSymlinks(t *testing.T) {
	root := t.TempDir()
	writeArchiveFile(t, filepath.Join(root, "libexec", "pixie_full"))
	writeArchiveModule(t, filepath.Join(root, "libexec", "pixie_assistant.js"))
	writeArchiveFile(t, filepath.Join(root, "libexec", "pixie_web"))
	writeRuntimeExecutable(t, filepath.Join(root, "runtime", "bin", "bun"), hostELFMachine(t))
	if err := os.MkdirAll(filepath.Join(root, "runtime", "node_modules", "@earendil-works", "pi-coding-agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths, err := resolveArchivePaths(filepath.Join(root, "libexec", "pixie_full"))
	if err != nil {
		t.Fatal(err)
	}
	if paths.bun != filepath.Join(root, "runtime", "bin", "bun") || paths.assistant != filepath.Join(root, "libexec", "pixie_assistant.js") {
		t.Fatalf("archive paths = %#v", paths)
	}
	if paths.piPackage != filepath.Join(root, "runtime", "node_modules", "@earendil-works", "pi-coding-agent") {
		t.Fatalf("package path = %q", paths.piPackage)
	}
	if _, err := archiveRelativePath(root, "../outside", false); err == nil {
		t.Fatal("archive traversal was accepted")
	}
	if err := os.Remove(filepath.Join(root, "libexec", "pixie_web")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("pixie_assistant.js", filepath.Join(root, "libexec", "pixie_web")); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveArchivePaths(filepath.Join(root, "libexec", "pixie_full")); err == nil {
		t.Fatal("symlinked executable payload was accepted")
	}
}

func TestFullSupervisorResolvesExactArchiveStartupLayout(t *testing.T) {
	root := t.TempDir()
	writeArchiveFile(t, filepath.Join(root, "libexec", "pixie_full"))
	writeArchiveModule(t, filepath.Join(root, "libexec", "pixie_assistant.js"))
	writeArchiveFile(t, filepath.Join(root, "libexec", "pixie_web"))
	bun := filepath.Join(root, "runtime", "bin", "bun")
	writeRuntimeExecutable(t, bun, hostELFMachine(t))
	piPackage := filepath.Join(root, "runtime", "node_modules", "@earendil-works", "pi-coding-agent")
	if err := os.MkdirAll(piPackage, 0o755); err != nil {
		t.Fatal(err)
	}

	paths, err := resolveArchivePaths(filepath.Join(root, "libexec", "pixie_full"))
	if err != nil {
		t.Fatal(err)
	}
	want := archivePaths{
		bun:        bun,
		assistant:  filepath.Join(root, "libexec", "pixie_assistant.js"),
		controller: filepath.Join(root, "libexec", "pixie_web"),
		piPackage:  piPackage,
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("archive paths = %#v, want %#v", paths, want)
	}
}

func TestFullSupervisorRejectsSymlinkedLibexecDirectory(t *testing.T) {
	root := t.TempDir()
	payload := filepath.Join(root, "payload")
	writeArchiveFile(t, filepath.Join(payload, "pixie_full"))
	writeArchiveModule(t, filepath.Join(payload, "pixie_assistant.js"))
	writeArchiveFile(t, filepath.Join(payload, "pixie_web"))
	writeRuntimeExecutable(t, filepath.Join(root, "runtime", "bin", "bun"), hostELFMachine(t))
	if err := os.MkdirAll(filepath.Join(root, "runtime", "node_modules", "@earendil-works", "pi-coding-agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("payload", filepath.Join(root, "libexec")); err != nil {
		t.Fatal(err)
	}

	if _, err := resolveArchivePaths(filepath.Join(root, "libexec", "pixie_full")); err == nil {
		t.Fatal("symlinked libexec directory was accepted")
	}
}

func TestChildInvocationsPairPortAndFilterControllerEnvironment(t *testing.T) {
	paths := archivePaths{
		bun:        "/archive/runtime/bin/bun",
		assistant:  "/archive/libexec/pixie_assistant.js",
		controller: "/archive/libexec/pixie_web",
		piPackage:  "/archive/runtime/node_modules/@earendil-works/pi-coding-agent",
	}
	parsed := arguments{command: commandServe, assistantConfig: "/private/assistant.json", webConfig: "/private/web.json"}
	parent := map[string]string{
		"PIXIE_PI_SECRET_KEY":    strings.Repeat("s", 32),
		"PI_CODING_AGENT_DIR":    "/home/user/.pi/agent",
		"PIXIE_ASSISTANT_PORT":   "3284",
		"PIXIE_ASSISTANT_CUSTOM": "value",
		"PIXIE_PI_URL":           "ws://127.0.0.1:9999/pi",
		"PIXIE_PI_PROTOCOL":      "v1",
	}
	assistant, controller := childInvocations(paths, parsed, assistantEndpoint{host: "127.0.0.1", port: 3291}, parent)
	if assistant.path != paths.bun {
		t.Fatalf("assistant runtime = %q, want bundled Bun", assistant.path)
	}
	if !reflect.DeepEqual(assistant.args, []string{paths.assistant, "serve", "--config", "/private/assistant.json"}) {
		t.Fatalf("assistant arguments = %#v", assistant.args)
	}
	if assistant.env["PIXIE_BUNDLED_PI_PACKAGE"] != paths.piPackage {
		t.Fatalf("assistant package = %q, want bundled package", assistant.env["PIXIE_BUNDLED_PI_PACKAGE"])
	}
	if assistant.env["PI_CODING_AGENT_DIR"] == "" {
		t.Fatal("assistant lost its agent directory selection")
	}
	if controller.path != paths.controller {
		t.Fatalf("controller runtime = %q", controller.path)
	}
	if !reflect.DeepEqual(controller.args, []string{"serve", "--config", "/private/web.json"}) {
		t.Fatalf("controller arguments = %#v", controller.args)
	}
	if controller.env["PIXIE_PI_PORT"] != "3291" {
		t.Fatalf("controller port = %q", controller.env["PIXIE_PI_PORT"])
	}
	for _, key := range []string{"PIXIE_PI_PACKAGE", "PI_CODING_AGENT_DIR", "PIXIE_ASSISTANT_PORT", "PIXIE_ASSISTANT_CUSTOM", "PIXIE_PI_URL"} {
		if _, present := controller.env[key]; present {
			t.Fatalf("controller retained %s", key)
		}
	}
	if controller.env["PIXIE_PI_SECRET_KEY"] != strings.Repeat("s", 32) || controller.env["PIXIE_PI_PROTOCOL"] != "v1" {
		t.Fatalf("controller lost required inherited settings: %#v", controller.env)
	}
}

func TestFullServiceRejectsPublicPiPackageBeforeLaunchingChildren(t *testing.T) {
	if err := rejectPublicPiSelection(map[string]string{"PIXIE_PI_PACKAGE": ""}); err == nil {
		t.Fatal("empty PIXIE_PI_PACKAGE was accepted")
	} else if !strings.Contains(err.Error(), "bundled Pi archive") {
		t.Fatalf("public package error = %q", err)
	}
}

func TestReadinessRequestUsesBearerAuthentication(t *testing.T) {
	secret := strings.Repeat("x", 32)
	request, err := readinessRequest(context.Background(), assistantEndpoint{host: "127.0.0.1", port: 3284}, secret)
	if err != nil {
		t.Fatal(err)
	}
	if request.Method != "GET" || request.URL.String() != "http://127.0.0.1:3284/readyz" {
		t.Fatalf("request = %s %s", request.Method, request.URL)
	}
	if request.Header.Get("Authorization") != "Bearer "+secret {
		t.Fatal("readiness request did not carry the inherited bearer secret")
	}
}

func TestFullSupervisorReportsOwnerLockContentionAsExitCode73(t *testing.T) {
	agentDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "assistant.json")
	config := fmt.Sprintf(`{"schemaVersion":2,"host":"127.0.0.1","port":3284,"agentDir":%q,"allowSelfRestart":false}`, agentDir)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := ownerlock.Acquire(agentDir, "pixie_full")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	parsed := arguments{command: commandServe, assistantConfig: configPath, webConfig: "/tmp/web.json"}
	environment := []string{"PIXIE_PI_SECRET_KEY=" + strings.Repeat("s", 32)}
	if clean, err := supervise(parsed, environment, make(chan os.Signal)); err == nil {
		t.Fatalf("second owner started (clean=%t) despite an active lock", clean)
	} else if !ownerlock.IsContended(err) || ownerlock.ExitCode(err) != ownerlock.ContentionExitCode {
		t.Fatalf("owner contention error = %v, want exit code %d", err, ownerlock.ContentionExitCode)
	}
}

func writeArchiveFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// writeArchiveModule writes a non-executable regular file, matching the bundled
// JS assistant that the supervisor hands to Bun as a script.
func writeArchiveModule(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("export const serve = true;"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeRuntimeExecutable(t *testing.T, path string, machine uint16) {
	t.Helper()
	header := make([]byte, 64)
	copy(header, []byte{0x7f, 'E', 'L', 'F', 2, 1})
	header[18] = byte(machine)
	header[19] = byte(machine >> 8)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, header, 0o755); err != nil {
		t.Fatal(err)
	}
}

func hostELFMachine(t *testing.T) uint16 {
	t.Helper()
	switch runtime.GOARCH {
	case "amd64":
		return 62
	case "arm64":
		return 183
	default:
		t.Skipf("no pinned ELF machine for test host architecture %s", runtime.GOARCH)
		return 0
	}
}
