package diagnostics_test

import (
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/diagnostics"
)

// A hostile host error can reach the exported sanitizer at up to the 32 MiB Pi
// client read limit. Every regex in the chain must therefore run on a value
// that the sanitizer bounds itself; otherwise one oversized single-token value
// stalls the controller goroutine. These fixtures are the smallest sizes that
// reproduced the defect (roughly 15 s each before the input bound existed).
func TestSanitizeDiagnosticTextBoundsAdversarialInput(t *testing.T) {
	const limit = 4096
	cases := []struct {
		name  string
		input string
	}{
		// A single token with no delimiter: the glued-path pattern probes up to
		// its repetition bound at every offset.
		{"all-A", strings.Repeat("A", 1<<20)},
		// Same shape, but every byte is also a candidate token character.
		{"hex-blob", strings.Repeat("deadbeef", 1<<17)},
	}
	for _, testCase := range cases {
		if len(testCase.input) < 1<<20 {
			t.Fatalf("%s fixture is %d bytes, want at least 1 MiB", testCase.name, len(testCase.input))
		}
		start := time.Now()
		got := diagnostics.SanitizeDiagnosticText(testCase.input, limit)
		elapsed := time.Since(start)
		t.Logf("%s: len=%d in %s", testCase.name, len(got), elapsed)
		// The input bound is the guarantee; this generous ceiling only catches a
		// return of the unbounded stall (roughly 15s per fixture before the fix)
		// without depending on the exact CI runner speed.
		if elapsed > 10*time.Second {
			t.Fatalf("%s sanitized in %s, want under 10s", testCase.name, elapsed)
		}
		if len(got) > limit {
			t.Fatalf("%s output has %d bytes, limit is %d", testCase.name, len(got), limit)
		}
		if got == "" {
			t.Fatalf("%s produced an empty result", testCase.name)
		}
	}
}

// Bounding the input must not change redaction near the head of the value, and
// a distinctive tail beyond the input bound must not reach the output.
func TestSanitizeDiagnosticTextBoundsInputAndKeepsRedaction(t *testing.T) {
	const limit = 4096
	glued := strings.Repeat("Z", 60) + "/home/operator/.pi/agent/secret.json"
	const tail = "TAIL_MARKER_BEYOND_INPUT_BOUND"
	input := "Authorization: token supersecrettokenvalue " + glued + " " + strings.Repeat("A", 1<<20) + tail
	got := diagnostics.SanitizeDiagnosticText(input, limit)
	if len(got) > limit {
		t.Fatalf("output has %d bytes, limit is %d", len(got), limit)
	}
	for _, forbidden := range []string{"supersecrettokenvalue", "/home/operator", "secret.json", tail} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("sanitizer retained %q in %q", forbidden, got)
		}
	}
	if !strings.Contains(got, "[redacted") {
		t.Fatalf("sanitizer dropped all redaction placeholders: %q", got)
	}
}
