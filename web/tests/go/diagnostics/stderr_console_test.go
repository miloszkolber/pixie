package diagnostics_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/diagnostics"
)

// failingWriter models a console that cannot accept output. The ring must still
// retain the redacted line and report a complete write so a child is never
// stalled by a broken console.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("console unavailable") }

func TestStderrRingConsoleMirrorsOnlyRedactedCompleteLines(t *testing.T) {
	secret := strings.Repeat("c", 20)
	t.Cleanup(func() { diagnostics.ConfigureSanitizerSecrets() })
	diagnostics.ConfigureSanitizerSecrets(secret)

	var console bytes.Buffer
	ring := diagnostics.NewStderrRingWithConsole(&console)
	// A partial line must not reach the console before it is delimited, so a
	// split secret or path cannot leak through the live mirror.
	if _, err := ring.Write([]byte("Authorization: Bearer bearer-token-value")); err != nil {
		t.Fatal(err)
	}
	if console.Len() != 0 {
		t.Fatalf("partial line was mirrored before a newline: %q", console.String())
	}
	if _, err := ring.Write([]byte("\nassistant ready on loopback\n")); err != nil {
		t.Fatal(err)
	}
	// A secret split across two writes is joined before redaction.
	if _, err := ring.Write([]byte(secret[:10])); err != nil {
		t.Fatal(err)
	}
	if _, err := ring.Write([]byte(secret[10:] + "\n")); err != nil {
		t.Fatal(err)
	}
	// An unfinished hostile path is buffered, not mirrored.
	if _, err := ring.Write([]byte("/home/alice/.pi/agent/config.json")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(console.String(), "alice") {
		t.Fatalf("partial path reached the console: %q", console.String())
	}
	stderr := console.String()
	for _, forbidden := range []string{"bearer-token-value", secret, "alice"} {
		if strings.Contains(stderr, forbidden) {
			t.Fatalf("console mirror leaked %q: %q", forbidden, stderr)
		}
	}
	if !strings.Contains(stderr, "assistant ready on loopback") {
		t.Fatalf("safe line was not mirrored: %q", stderr)
	}
	if strings.Contains(stderr, "\n\n\n") {
		t.Fatalf("console mirror emitted an empty line: %q", stderr)
	}

	// Flushing the final partial resolves it through the same boundary.
	ring.Flush()
	if strings.Contains(console.String(), "alice") {
		t.Fatalf("flushed partial path leaked: %q", console.String())
	}
	if summary := ring.Snapshot(); summary.Retained != 4 {
		t.Fatalf("retained summary = %#v", summary)
	}
}

func TestStderrRingConsoleWriteFailureDoesNotAffectRetention(t *testing.T) {
	ring := diagnostics.NewStderrRingWithConsole(failingWriter{})
	line := "managed child started\n"
	written, err := ring.Write([]byte(line))
	if err != nil || written != len(line) {
		t.Fatalf("Write = %d, %v; want %d, nil", written, err, len(line))
	}
	if summary := ring.Snapshot(); summary.Retained != 1 || !strings.Contains(summary.Entries[0].Text, "managed child started") {
		t.Fatalf("console failure changed retention: %#v", summary)
	}
}

// An oversized line is discarded before redaction, and the console must not see
// a raw fragment of it either.
func TestStderrRingConsoleDropsOversizedPartial(t *testing.T) {
	var console bytes.Buffer
	ring := diagnostics.NewStderrRingWithConsole(&console)
	hostile := "secret-prefix " + strings.Repeat("A", diagnostics.MaxStderrLineBytes*2)
	if _, err := ring.Write([]byte(hostile)); err != nil {
		t.Fatal(err)
	}
	ring.Flush()
	if strings.Contains(console.String(), "secret-prefix") {
		t.Fatalf("oversized partial leaked to console: %q", console.String())
	}
	if summary := ring.Snapshot(); summary.Retained != 0 || summary.Dropped == 0 {
		t.Fatalf("oversized line was retained: %#v", summary)
	}
}

func TestStderrRingNilConsoleIsRetentionOnly(t *testing.T) {
	ring := diagnostics.NewStderrRingWithConsole(nil)
	if _, err := fmt.Fprintln(ring, "retained only"); err != nil {
		t.Fatal(err)
	}
	if summary := ring.Snapshot(); summary.Retained != 1 {
		t.Fatalf("nil console changed retention: %#v", summary)
	}
}
