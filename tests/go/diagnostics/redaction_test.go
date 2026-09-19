package diagnostics_test

import (
	"fmt"
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

// A long opaque run glued directly to an absolute path has no delimiter before
// the leading slash, so the delimiter-based path pattern cannot see it. The
// adjoining run is itself sensitive, so the whole glued sequence is redacted
// in both value position and JSON-key position, while ordinary relative paths
// stay untouched.
func TestSanitizeDiagnosticTextRedactsGluedAbsolutePath(t *testing.T) {
	glued := strings.Repeat("A", 60) + "/home/operator/.pi/agent/secret.json"
	cases := []struct{ name, input, forbidden string }{
		{"value", "read " + glued, "/home/operator"},
		{"json key", fmt.Sprintf("{%q: %q}", glued, "benign"), "secret.json"},
	}
	for _, testCase := range cases {
		got := diagnostics.SanitizeDiagnosticText(testCase.input, 4096)
		if strings.Contains(got, testCase.forbidden) {
			t.Fatalf("%s retained %q in %q", testCase.name, testCase.forbidden, got)
		}
		if !strings.Contains(got, "[redacted path]") {
			t.Fatalf("%s did not emit the path placeholder: %q", testCase.name, got)
		}
	}

	const relative = "see src/main.go and ok.txt"
	if got := diagnostics.SanitizeDiagnosticText(relative, 4096); got != relative {
		t.Fatalf("relative path was over-redacted: %q", got)
	}
}
