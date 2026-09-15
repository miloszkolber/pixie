package diagnostics_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/diagnostics"
)

// Delimited absolute paths, UNC shares and whitespace-separated credential
// labels must be redacted before any diagnostic text leaves the process.
func TestSanitizeDiagnosticTextRedactsDelimitedPathsAndLabelledCredentials(t *testing.T) {
	cases := []struct{ input, forbidden string }{
		{"dial failed at [/home/operator/.pi]", "/home/operator"},
		{"config {/var/lib/pixie/data} unavailable", "/var/lib/pixie"},
		{`share \\server\share\secret unreachable`, `server\share`},
		{"Authorization: token abcdef0123456789", "abcdef0123456789"},
	}
	for _, testCase := range cases {
		got := diagnostics.SanitizeDiagnosticText(testCase.input, 4096)
		if strings.Contains(got, testCase.forbidden) {
			t.Fatalf("sanitizer retained %q in %q", testCase.forbidden, got)
		}
	}
}

// A caller-supplied child-stderr summary can exceed the byte budget even when
// its line count is within bounds; the export must prune to both limits.
func TestSupportSnapshotPrunesChildStderrToByteBudget(t *testing.T) {
	line := strings.Repeat("x", diagnostics.MaxStderrLineBytes-1)
	entries := make([]diagnostics.StderrEntry, 0, diagnostics.DefaultStderrMaxLines)
	for index := 0; index < diagnostics.DefaultStderrMaxLines; index++ {
		entries = append(entries, diagnostics.StderrEntry{At: "2026-09-15T12:00:00Z", Text: line})
	}
	payload, err := diagnostics.MarshalSupportSnapshot(diagnostics.SupportSnapshotRuntime{
		ChildStderr: &diagnostics.StderrSummary{Entries: entries},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot diagnostics.SupportSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.ChildStderr == nil || snapshot.ChildStderr.Bytes > diagnostics.DefaultStderrMaxBytes {
		t.Fatalf("child stderr byte budget exceeded: %#v", snapshot.ChildStderr)
	}
	if snapshot.ChildStderr.Retained > diagnostics.DefaultStderrMaxLines {
		t.Fatalf("child stderr line budget exceeded: %#v", snapshot.ChildStderr)
	}
}
