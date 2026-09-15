package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestResolveArchivePathsUsesBundledBunPiRuntimeLayout(t *testing.T) {
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, "pixie"))
	writeRuntimeExecutable(t, filepath.Join(root, "runtime", "bin", "bun"), hostELFMachine(t))
	writeFile(t, filepath.Join(root, "runtime", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "bun", "cli.js"), 0o644, "cli")
	writeFile(t, filepath.Join(root, "runtime", "node_modules", "@earendil-works", "pi-coding-agent", "package.json"), 0o644, `{"name":"@earendil-works/pi-coding-agent","version":"0.85.1"}`)
	paths, err := resolveArchivePaths(filepath.Join(root, "pixie"))
	if err != nil {
		t.Fatal(err)
	}
	if paths.bun != filepath.Join(root, "runtime", "bin", "bun") {
		t.Fatalf("bun path = %q", paths.bun)
	}
	if paths.cli != filepath.Join(root, "runtime", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "bun", "cli.js") {
		t.Fatalf("cli path = %q", paths.cli)
	}
	if version, err := bundledPiVersion(paths.piPackage); err != nil || version != "0.85.1" {
		t.Fatalf("bundledPiVersion = %q, %v", version, err)
	}
	if err := os.Remove(paths.cli); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../cli.js", paths.cli); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveArchivePaths(filepath.Join(root, "pixie")); err == nil {
		t.Fatal("symlinked Pi CLI payload was accepted")
	}
}

func TestResolveArchivePathsRejectsMissingAndWrongArchitectureBun(t *testing.T) {
	root := t.TempDir()
	writeExecutable(t, filepath.Join(root, "pixie"))
	bun := filepath.Join(root, "runtime", "bin", "bun")
	writeRuntimeExecutable(t, bun, hostELFMachine(t))
	writeFile(t, filepath.Join(root, "runtime", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "bun", "cli.js"), 0o644, "cli")
	writeFile(t, filepath.Join(root, "runtime", "node_modules", "@earendil-works", "pi-coding-agent", "package.json"), 0o644, `{"name":"@earendil-works/pi-coding-agent","version":"0.85.1"}`)

	if err := os.Remove(bun); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveArchivePaths(filepath.Join(root, "pixie")); err == nil {
		t.Fatal("missing bundled Bun runtime was accepted")
	}

	wrong := uint16(62)
	if hostELFMachine(t) == 62 {
		wrong = 183
	}
	writeRuntimeExecutable(t, bun, wrong)
	if _, err := resolveArchivePaths(filepath.Join(root, "pixie")); err == nil {
		t.Fatal("wrong-architecture bundled Bun runtime was accepted")
	}
}

func TestPiInvocationForwardsNativeArgumentsWithoutChangingEnvironmentOrCWD(t *testing.T) {
	paths := archivePaths{
		bun: "/archive/runtime/bin/bun",
		cli: "/archive/runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/cli.js",
	}
	invocation := piInvocation(paths, []string{"--model", "native model", "prompt"})
	if invocation.Path != paths.bun {
		t.Fatalf("bun executable = %q", invocation.Path)
	}
	if want := []string{paths.cli, "--model", "native model", "prompt"}; !reflect.DeepEqual(invocation.Args, want) {
		t.Fatalf("arguments = %#v, want %#v", invocation.Args, want)
	}
	if invocation.Environment != nil || invocation.Directory != "" {
		t.Fatalf("Pi invocation changed inherited environment or cwd: %#v", invocation)
	}
}

func TestSelfUpdateGuardLeavesNativeExtensionAndConfigurationCommandsAlone(t *testing.T) {
	for _, args := range [][]string{
		{"update"},
		{"update", "--self"},
		{"update", "pi"},
		{"update", "self", "--extensions"},
		{"update", "--all"},
	} {
		if !isPiSelfUpdate(args) {
			t.Fatalf("self update %q was not blocked", args)
		}
	}
	for _, args := range [][]string{
		{"update", "--extensions"},
		{"update", "--models"},
		{"update", "my-extension"},
		{"update", "--extension", "my-extension"},
		{"install", "my-extension"},
		{"config", "set", "model"},
		{"update", "--help"},
	} {
		if isPiSelfUpdate(args) {
			t.Fatalf("native non-self-update %q was blocked", args)
		}
	}
}

func TestEnvironmentLookupPreservesExactNativeValues(t *testing.T) {
	lookup := environmentLookup([]string{"HOME=/home/pixie", "PI_NATIVE_OPTION= spaced value ", "EMPTY="})
	if home, ok := lookup("HOME"); !ok || home != "/home/pixie" {
		t.Fatalf("HOME = %q, %t", home, ok)
	}
	if value, ok := lookup("PI_NATIVE_OPTION"); !ok || value != " spaced value " {
		t.Fatalf("native environment value = %q, %t", value, ok)
	}
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	writeFile(t, path, 0o755, "fixture")
}

// writeRuntimeExecutable writes a minimal 64-bit little-endian ELF header with
// the supplied e_machine so resolveArchivePaths can validate the architecture
// without a real 80 MB runtime.
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

func writeFile(t *testing.T, path string, mode os.FileMode, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(contents) == "" {
		t.Fatal("archive fixture content must not be empty")
	}
}
