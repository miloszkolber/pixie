package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

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
