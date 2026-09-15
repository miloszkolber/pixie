package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/cmd/internal/assistantconfig"
	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/ownerlock"
)

func TestPixieCLIRequiresOnlyExplicitServeConfig(t *testing.T) {
	parsed, err := parseArguments([]string{"serve", "--config", "/private/assistant.json"})
	if err != nil || parsed.configPath != "/private/assistant.json" {
		t.Fatalf("parseArguments = %#v, %v", parsed, err)
	}
	for _, args := range [][]string{
		nil,
		{"serve"},
		{"--pi-package", "/outside/pi"},
		{"serve", "--pi-package", "/outside/pi"},
		{"serve", "--config", "relative.json"},
	} {
		if _, err := parseArguments(args); err == nil {
			t.Fatalf("parseArguments(%q) accepted an unsupported public command", args)
		}
	}
}

func TestPixieCLIReportsInjectedReleaseIdentity(t *testing.T) {
	originalVersion, originalRevision := version, revision
	t.Cleanup(func() { version, revision = originalVersion, originalRevision })
	version = "sha-0123456789ab"
	revision = "0123456789abcdef0123456789abcdef01234567"
	if got, want := versionLine(), "pixie_cli sha-0123456789ab (revision 0123456789abcdef0123456789abcdef01234567)"; got != want {
		t.Fatalf("versionLine() = %q, want %q", got, want)
	}
}

func TestPixieCLIUnknownCommandIsNotADoctorFailure(t *testing.T) {
	for _, values := range [][]string{{"doctor"}, {"uninstall"}, {"--doctor"}} {
		_, err := parseArguments(values)
		if err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("parseArguments(%q) = %v, want an unknown-command error", values, err)
		}
	}
	// A malformed serve invocation is a usage error, not an unknown command.
	if _, err := parseArguments([]string{"serve", "--config"}); err == nil || strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("malformed serve usage = %v, want a plain usage error", err)
	}
}

func TestPixieCLIUsesBunAssistantBundleAndPrivateBundledPackage(t *testing.T) {
	paths := archivePaths{
		bun:       "/archive/runtime/bin/bun",
		assistant: "/archive/libexec/pixie_assistant.js",
		piPackage: "/archive/runtime/node_modules/@earendil-works/pi-coding-agent",
	}
	parent := map[string]string{
		"HOME":                        "/home/operator",
		"PI_CODING_AGENT_DIR":         "/home/operator/.pi/agent",
		"PIXIE_BUNDLED_PI_PACKAGE":    "/outside/ignored",
		"PIXIE_PI_SECRET_KEY":         strings.Repeat("s", 32),
		"NATIVE_PI_EXTENSION_SETTING": "kept",
	}
	invocation := assistantInvocation(paths, arguments{configPath: "/private/assistant.json"}, parent)
	if invocation.Path != paths.bun {
		t.Fatalf("assistant runtime = %q, want bundled Bun", invocation.Path)
	}
	if want := []string{paths.assistant, "serve", "--config", "/private/assistant.json"}; !reflect.DeepEqual(invocation.Args, want) {
		t.Fatalf("assistant arguments = %#v, want %#v", invocation.Args, want)
	}
	environment := environmentMap(invocation.Environment)
	if environment[bundledPiPackageEnvironment] != paths.piPackage {
		t.Fatalf("private bundled package = %q", environment[bundledPiPackageEnvironment])
	}
	if environment["NATIVE_PI_EXTENSION_SETTING"] != "kept" || environment["HOME"] != "/home/operator" {
		t.Fatalf("native environment was not retained: %#v", environment)
	}
	if err := rejectPublicPiSelection(map[string]string{"PIXIE_PI_PACKAGE": ""}); err == nil {
		t.Fatal("empty public PIXIE_PI_PACKAGE was accepted")
	}
}

func TestPixieCLIArchivePathsAreExactAndRejectSymlinkPayloads(t *testing.T) {
	root := t.TempDir()
	writeArchiveFile(t, filepath.Join(root, "pixie_cli"))
	writeRuntimeExecutable(t, filepath.Join(root, "runtime", "bin", "bun"), hostELFMachine(t))
	writeArchiveModule(t, filepath.Join(root, "libexec", "pixie_assistant.js"))
	if err := os.MkdirAll(filepath.Join(root, "runtime", "node_modules", "@earendil-works", "pi-coding-agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths, err := resolveArchivePaths(filepath.Join(root, "pixie_cli"))
	if err != nil {
		t.Fatal(err)
	}
	if paths.bun != filepath.Join(root, "runtime", "bin", "bun") || paths.assistant != filepath.Join(root, "libexec", "pixie_assistant.js") || !strings.HasSuffix(paths.piPackage, "/runtime/node_modules/@earendil-works/pi-coding-agent") {
		t.Fatalf("archive paths = %#v", paths)
	}
	if err := os.Remove(paths.assistant); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("pixie_assistant.js", paths.assistant); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveArchivePaths(filepath.Join(root, "pixie_cli")); err == nil {
		t.Fatal("symlinked assistant bundle was accepted")
	}
}

func TestPixieCLIRejectsMissingAndWrongArchitectureBun(t *testing.T) {
	root := t.TempDir()
	writeArchiveFile(t, filepath.Join(root, "pixie_cli"))
	bun := filepath.Join(root, "runtime", "bin", "bun")
	writeRuntimeExecutable(t, bun, hostELFMachine(t))
	writeArchiveModule(t, filepath.Join(root, "libexec", "pixie_assistant.js"))
	if err := os.MkdirAll(filepath.Join(root, "runtime", "node_modules", "@earendil-works", "pi-coding-agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(bun); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveArchivePaths(filepath.Join(root, "pixie_cli")); err == nil {
		t.Fatal("missing bundled Bun runtime was accepted")
	}
	wrong := uint16(62)
	if hostELFMachine(t) == 62 {
		wrong = 183
	}
	writeRuntimeExecutable(t, bun, wrong)
	if _, err := resolveArchivePaths(filepath.Join(root, "pixie_cli")); err == nil {
		t.Fatal("wrong-architecture bundled Bun runtime was accepted")
	}
}

func TestPixieCLIReportsOwnerLockContentionAsExitCode73(t *testing.T) {
	agentDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "assistant.json")
	config := fmt.Sprintf(`{"schemaVersion":2,"host":"127.0.0.1","port":3284,"agentDir":%q,"allowSelfRestart":false}`, agentDir)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := ownerlock.Acquire(agentDir, "pixie_cli")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	code, err := run(arguments{configPath: configPath}, []string{"HOME=/home/operator"})
	if err == nil {
		t.Fatalf("second owner started with code %d despite an active lock", code)
	}
	if !ownerlock.IsContended(err) || ownerlock.ExitCode(err) != ownerlock.ContentionExitCode {
		t.Fatalf("owner contention error = %v, want exit code %d", err, ownerlock.ContentionExitCode)
	}
}

func TestPixieCLIInjectsOpaqueRunIdentityAndRejectsHostileValues(t *testing.T) {
	identity := diagnostics.RunIdentity{
		BootID: "d1b2c3d4-1111-2222-3333-444455556666",
		RunID:  "run-0123456789abcdef0123456789abcdef",
	}
	environment := withRunIdentity([]string{"HOME=/home/operator", "PIXIE_PI_SECRET_KEY=" + strings.Repeat("s", 32)}, identity)
	values := environmentMap(environment)
	if values[diagnostics.BootIdentityEnvironment] != identity.BootID || values[diagnostics.RunIdentityEnvironment] != identity.RunID {
		t.Fatalf("identity environment = %#v", values)
	}
	if values["HOME"] != "/home/operator" || values["PIXIE_PI_SECRET_KEY"] != strings.Repeat("s", 32) {
		t.Fatalf("identity injection clobbered native values: %#v", values)
	}
	hostile := withRunIdentity([]string{"HOME=/home/operator"}, diagnostics.RunIdentity{BootID: "Bearer bearer-token-value"})
	if _, present := environmentMap(hostile)[diagnostics.BootIdentityEnvironment]; present {
		t.Fatalf("hostile identity was exported: %#v", hostile)
	}
}

func TestAssistantReadinessEndpointUsesConfiguredHostAndPort(t *testing.T) {
	endpoint := assistantReadinessEndpoint(assistantconfig.Config{Host: "localhost", Port: 3284})
	if got, want := endpoint.readyURL(), "http://localhost:3284/readyz"; got != want {
		t.Fatalf("readyURL() = %q, want %q", got, want)
	}
}

func TestWaitForAssistantReadyAcceptsReadyAndRequiresBearer(t *testing.T) {
	secret := strings.Repeat("s", 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	endpoint := assistantEndpointForTest(t, server.URL)
	if err := waitForAssistantReady(context.Background(), endpoint, secret, time.Second); err != nil {
		t.Fatalf("waitForAssistantReady() = %v", err)
	}
}

func TestWaitForAssistantReadyFailsClosedWhenNeverReady(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	endpoint := assistantEndpointForTest(t, server.URL)
	err := waitForAssistantReady(context.Background(), endpoint, strings.Repeat("s", 32), 150*time.Millisecond)
	if err == nil {
		t.Fatal("a never-ready assistant was accepted")
	}
	if !strings.Contains(err.Error(), "ready") {
		t.Fatalf("readiness error = %q, want actionable readiness text", err)
	}
	if strings.Contains(err.Error(), strings.Repeat("s", 32)) {
		t.Fatalf("readiness error leaked the bearer secret: %q", err)
	}
}

func TestWaitForAssistantReadyHonorsAbort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	endpoint := assistantEndpointForTest(t, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if err := waitForAssistantReady(ctx, endpoint, strings.Repeat("s", 32), 10*time.Second); err == nil {
		t.Fatal("a cancelled readiness wait returned nil")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("a cancelled readiness wait blocked for %s", elapsed)
	}
}

func TestRequireAssistantSecretRejectsMissingOrShort(t *testing.T) {
	if _, err := requireAssistantSecret(func(string) (string, bool) { return "", false }); err == nil {
		t.Fatal("missing secret was accepted")
	}
	if _, err := requireAssistantSecret(func(string) (string, bool) { return strings.Repeat("x", 31), true }); err == nil {
		t.Fatal("short secret was accepted")
	}
	secret := strings.Repeat("x", 32)
	if got, err := requireAssistantSecret(func(string) (string, bool) { return secret, true }); err != nil || got != secret {
		t.Fatalf("requireAssistantSecret() = %q, %v", got, err)
	}
}

// A partial line from the hosted assistant is buffered until a newline or
// Flush, so a fragment cannot bypass the one stderr boundary or be lost on exit.
func TestAssistantStderrSinkBuffersPartialLinesUntilFlush(t *testing.T) {
	var console bytes.Buffer
	sink := assistantStderrSink(&console)
	if _, err := io.WriteString(sink, "assistant booting"); err != nil {
		t.Fatal(err)
	}
	if console.Len() != 0 {
		t.Fatalf("partial line reached the console before a newline or flush: %q", console.String())
	}
	sink.Flush()
	if !strings.Contains(console.String(), "assistant booting") {
		t.Fatalf("flushed line = %q", console.String())
	}
}

// The CLI records its start in the same bounded ledger pixie_full uses, and the
// ledger expires starts outside the restart window so a long-lived host is not
// permanently over budget.
func TestRecordRestartBudgetResetsOutsideWindow(t *testing.T) {
	agentDir := t.TempDir()
	ledgerPath := filepath.Join(agentDir, "pixie", "restarts.json")
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o700); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if err := os.WriteFile(ledgerPath, []byte(`["`+stale+`"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if starts := recordRestartBudget(agentDir); starts != 1 {
		t.Fatalf("stale start counted toward the budget: %d", starts)
	}
	if starts := recordRestartBudget(agentDir); starts != 2 {
		t.Fatalf("recent start was not counted: %d", starts)
	}
}

func assistantEndpointForTest(t *testing.T, raw string) assistantEndpoint {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatal(err)
	}
	return assistantEndpoint{host: parsed.Hostname(), port: port}
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
// JS assistant that the launcher hands to Bun as a script.
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
