package runtimeexec

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidateAcceptsHostArchitectureELF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bun")
	writeELF(t, path, hostMachine(t), 0o755)
	if err := Validate(path); err != nil {
		t.Fatalf("valid runtime was rejected: %v", err)
	}
}

func TestValidateRejectsWrongArchitectureAndNonELF(t *testing.T) {
	directory := t.TempDir()
	wrong := uint16(62)
	if hostMachine(t) == 62 {
		wrong = 183
	}
	wrongPath := filepath.Join(directory, "wrong")
	writeELF(t, wrongPath, wrong, 0o755)
	if err := Validate(wrongPath); err == nil {
		t.Fatal("wrong-architecture runtime was accepted")
	}

	textPath := filepath.Join(directory, "text")
	if err := os.WriteFile(textPath, []byte("not an elf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Validate(textPath); err == nil {
		t.Fatal("non-ELF runtime was accepted")
	}

	shortPath := filepath.Join(directory, "short")
	if err := os.WriteFile(shortPath, []byte{0x7f, 'E', 'L', 'F'}, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Validate(shortPath); err == nil {
		t.Fatal("truncated runtime was accepted")
	}
}

func TestValidateRejectsMissingNonExecutableAndSymlinkedRuntime(t *testing.T) {
	directory := t.TempDir()
	if err := Validate(filepath.Join(directory, "absent")); err == nil {
		t.Fatal("missing runtime was accepted")
	}

	path := filepath.Join(directory, "bun")
	writeELF(t, path, hostMachine(t), 0o644)
	if err := Validate(path); err == nil {
		t.Fatal("non-executable runtime was accepted")
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "bun-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := Validate(link); err == nil {
		t.Fatal("symlinked runtime was accepted")
	}
}

func hostMachine(t *testing.T) uint16 {
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

func writeELF(t *testing.T, path string, machine uint16, mode os.FileMode) {
	t.Helper()
	header := make([]byte, 64)
	copy(header, []byte{0x7f, 'E', 'L', 'F', elfClass64, elfDataLSB})
	header[18] = byte(machine)
	header[19] = byte(machine >> 8)
	if err := os.WriteFile(path, header, mode); err != nil {
		t.Fatal(err)
	}
}
