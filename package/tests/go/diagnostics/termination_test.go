package diagnostics_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDiagnosticsFinalContainerReapsChildren verifies the FIX-12/X03
// final-container reaping contract at the owned level: the published pixie
// image must run under tini so PID 1 reaps orphaned managed-group
// descendants (Git and Browser helpers) instead of leaving zombies.
func TestDiagnosticsFinalContainerReapsChildren(t *testing.T) {
	dockerfile := findDockerfile(t)
	contents, err := os.ReadFile(dockerfile)
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	text := string(contents)

	if !strings.Contains(text, "tini") {
		t.Fatalf("Dockerfile does not mention tini: %s", dockerfile)
	}

	lines := strings.Split(text, "\n")
	lastEntrypoint := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ENTRYPOINT") {
			lastEntrypoint = trimmed
		}
	}
	if lastEntrypoint == "" {
		t.Fatal("Dockerfile has no ENTRYPOINT")
	}
	// The final stage is the published image. It must keep tini as PID 1
	// with pixie as its child, not replace tini with a bare pixie entrypoint.
	if !strings.Contains(lastEntrypoint, "tini") || !strings.Contains(lastEntrypoint, "/app/pixie") {
		t.Fatalf("final ENTRYPOINT does not reap under tini: %q", lastEntrypoint)
	}
	if strings.TrimSpace(lastEntrypoint) == `ENTRYPOINT ["/app/pixie"]` {
		t.Fatalf("final ENTRYPOINT lost its init, PID 1 would not reap helpers: %q", lastEntrypoint)
	}
}

func findDockerfile(t *testing.T) string {
	t.Helper()
	_, caller, _, ok := runtime.Caller(0)
	candidates := []string{"../../../Dockerfile"}
	if ok {
		candidates = append([]string{filepath.Join(filepath.Dir(caller), "..", "..", "..", "Dockerfile")}, candidates...)
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			absolute, err := filepath.Abs(candidate)
			if err == nil {
				return absolute
			}
			return candidate
		}
	}
	t.Fatalf("Dockerfile not found (tried %q)", candidates)
	return ""
}
