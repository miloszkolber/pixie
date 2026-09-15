package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/diagnostics"
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
	doctor, err := parseArguments([]string{
		"doctor", "--assistant-config", "/tmp/assistant.json", "--web-config", "/tmp/web.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if doctor.command != commandDoctor {
		t.Fatalf("doctor command = %#v", doctor)
	}
	for _, values := range [][]string{
		{"version"},
		{"-h"},
		{"serve", "--assistant-config=/tmp/assistant.json", "--web-config", "/tmp/web.json", "extra"},
		{"serve", "--assistant-config", "assistant.json", "--web-config", "/tmp/web.json"},
		{"serve", "--assistant-config", "/tmp/assistant.json", "--version", "/tmp/web.json"},
		{"doctor"},
		{"doctor", "--assistant-config", "relative.json", "--web-config", "/tmp/web.json"},
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

func TestApplyRunIdentityExportsOnlyOpaqueIdentifiers(t *testing.T) {
	identity := diagnostics.RunIdentity{
		BootID: "d1b2c3d4-1111-2222-3333-444455556666",
		RunID:  "run-0123456789abcdef0123456789abcdef",
	}
	invocation := childInvocation{env: map[string]string{"PIXIE_PI_SECRET_KEY": strings.Repeat("s", 32)}}
	applyRunIdentity(&invocation, identity)
	if invocation.env[diagnostics.BootIdentityEnvironment] != identity.BootID || invocation.env[diagnostics.RunIdentityEnvironment] != identity.RunID {
		t.Fatalf("identity environment = %#v", invocation.env)
	}
	if invocation.env["PIXIE_PI_SECRET_KEY"] != strings.Repeat("s", 32) {
		t.Fatalf("existing environment was clobbered: %#v", invocation.env)
	}
	hostile := childInvocation{env: map[string]string{}}
	applyRunIdentity(&hostile, diagnostics.RunIdentity{BootID: "Bearer bearer-token-value", RunID: "/home/alice/.pi"})
	if len(hostile.env) != 0 {
		t.Fatalf("hostile identity was exported: %#v", hostile.env)
	}
}

func TestDiagnoseFactsReportsArchivePackageLockAndAgentDirectory(t *testing.T) {
	root := t.TempDir()
	writeArchiveFile(t, filepath.Join(root, "libexec", "pixie_full"))
	writeArchiveModule(t, filepath.Join(root, "libexec", "pixie_assistant.js"))
	writeArchiveFile(t, filepath.Join(root, "libexec", "pixie_web"))
	writeRuntimeExecutable(t, filepath.Join(root, "runtime", "bin", "bun"), hostELFMachine(t))
	if err := os.MkdirAll(filepath.Join(root, "runtime", "node_modules", "@earendil-works", "pi-coding-agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "assistant.json")
	config := fmt.Sprintf(`{"schemaVersion":2,"host":"127.0.0.1","port":3284,"agentDir":%q,"allowSelfRestart":false}`, agentDir)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed := arguments{command: commandDoctor, assistantConfig: configPath, webConfig: "/tmp/web.json"}
	facts := diagnoseFacts(parsed, []string{"PIXIE_PI_SECRET_KEY=" + strings.Repeat("s", 32)}, filepath.Join(root, "libexec", "pixie_full"))
	if facts.ConfigReadable == nil || !*facts.ConfigReadable {
		t.Fatalf("config readable = %#v", facts.ConfigReadable)
	}
	if facts.PiPackagePresent == nil || !*facts.PiPackagePresent || facts.PiPackageValid == nil || !*facts.PiPackageValid {
		t.Fatalf("pi package facts = %#v", facts)
	}
	if facts.AgentDirWritable == nil || !*facts.AgentDirWritable {
		t.Fatalf("agent dir writable = %#v", facts.AgentDirWritable)
	}
	if facts.OwnerLockHeld == nil || *facts.OwnerLockHeld {
		t.Fatalf("owner lock held = %#v", facts.OwnerLockHeld)
	}
	if facts.HostConfigured == nil || !*facts.HostConfigured {
		t.Fatalf("host configured = %#v", facts.HostConfigured)
	}
	if facts.RestartCount != 0 {
		t.Fatalf("restart count = %d", facts.RestartCount)
	}
}

func TestPixieFullDoctorOutputOmitsPathsAndEndpoints(t *testing.T) {
	root := t.TempDir()
	writeArchiveFile(t, filepath.Join(root, "libexec", "pixie_full"))
	writeArchiveModule(t, filepath.Join(root, "libexec", "pixie_assistant.js"))
	writeArchiveFile(t, filepath.Join(root, "libexec", "pixie_web"))
	writeRuntimeExecutable(t, filepath.Join(root, "runtime", "bin", "bun"), hostELFMachine(t))
	if err := os.MkdirAll(filepath.Join(root, "runtime", "node_modules", "@earendil-works", "pi-coding-agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "assistant.json")
	config := fmt.Sprintf(`{"schemaVersion":2,"host":"127.0.0.1","port":3284,"agentDir":%q,"allowSelfRestart":false}`, agentDir)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed := arguments{command: commandDoctor, assistantConfig: configPath, webConfig: "/tmp/web.json"}
	report := diagnostics.AssessRecovery(diagnoseFacts(parsed, []string{}, filepath.Join(root, "libexec", "pixie_full")))
	var output strings.Builder
	for _, check := range report.Checks {
		output.WriteString(diagnostics.FormatRecoveryCheck(check))
		output.WriteByte('\n')
	}
	for _, forbidden := range []string{root, agentDir, configPath, "Bearer "} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("doctor output leaked %q: %s", forbidden, output.String())
		}
	}
}

func TestPixieFullPortMismatchIsDetectedFromDialKnobs(t *testing.T) {
	mismatch := dialPortMismatch(func(key string) (string, bool) {
		switch key {
		case "PIXIE_PI_PORT":
			return "3284", true
		case "PIXIE_PI_URL":
			return "ws://127.0.0.1:9999/pi", true
		default:
			return "", false
		}
	})
	if mismatch == nil || !*mismatch {
		t.Fatalf("mismatch = %#v", mismatch)
	}
	match := dialPortMismatch(func(key string) (string, bool) {
		switch key {
		case "PIXIE_PI_PORT":
			return "3284", true
		case "PIXIE_PI_URL":
			return "ws://127.0.0.1:3284/pi", true
		default:
			return "", false
		}
	})
	if match == nil || *match {
		t.Fatalf("matching ports reported a mismatch: %#v", match)
	}
}

func TestStartChildRetainsBoundedStderr(t *testing.T) {
	ring := diagnostics.NewStderrRing()
	child, err := startChild(childInvocation{
		path: "/bin/sh",
		args: []string{"-c", "printf 'child-boot-line\\n' >&2"},
		env:  map[string]string{},
	}, ring)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-child.done:
	case <-time.After(5 * time.Second):
		t.Fatal("managed child did not exit")
	}
	summary := ring.Snapshot()
	if summary.Retained != 1 || !strings.Contains(summary.Entries[0].Text, "child-boot-line") {
		t.Fatalf("retained stderr = %#v", summary)
	}
}

func TestStartChildConsoleMirrorRedactsHostileStderr(t *testing.T) {
	secret := strings.Repeat("k", 20)
	t.Cleanup(func() { diagnostics.ConfigureSanitizerSecrets() })
	diagnostics.ConfigureSanitizerSecrets(secret)

	var console bytes.Buffer
	ring := diagnostics.NewStderrRingWithConsole(&console)
	script := "printf '%s\\n' 'Authorization: Bearer hostile-bearer-value' >&2\n" +
		"printf '%s\\n' 'token=opaque-credential-value' >&2\n" +
		"printf '%s\\n' 'https://url-user:url-password@example.invalid/path' >&2\n" +
		"printf '%s\\n' '/home/alice/.pi/agent/config.json' >&2\n" +
		"printf 'secret=' >&2\n" +
		"printf '%s' '" + secret + "' >&2\n" +
		"printf '\\n' >&2\n" +
		"printf '%s' 'child-status: ready' >&2\n"
	child, err := startChild(childInvocation{
		path: "/bin/sh",
		args: []string{"-c", script},
		env:  map[string]string{},
	}, ring)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-child.done:
	case <-time.After(5 * time.Second):
		t.Fatal("managed child did not exit")
	}
	stderr := console.String()
	for _, forbidden := range []string{
		secret, "hostile-bearer-value", "opaque-credential-value",
		"url-user", "url-password", "example.invalid", "alice",
	} {
		if strings.Contains(stderr, forbidden) {
			t.Fatalf("supervisor console leaked %q: %s", forbidden, stderr)
		}
	}
	if !strings.Contains(stderr, "child-status: ready") {
		t.Fatalf("safe lifecycle line was not mirrored: %s", stderr)
	}
	summary := ring.Snapshot()
	joined := ""
	for _, entry := range summary.Entries {
		joined += entry.Text + "\n"
	}
	for _, forbidden := range []string{secret, "hostile-bearer-value", "opaque-credential-value", "alice"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("retained ring leaked %q: %s", forbidden, joined)
		}
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
