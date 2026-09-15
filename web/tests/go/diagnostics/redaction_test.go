package diagnostics_test

import (
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
