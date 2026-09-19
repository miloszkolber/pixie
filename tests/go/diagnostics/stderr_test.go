package diagnostics_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/diagnostics"
)

func TestStderrRingRedactsHostileValuesAndKeepsSafeLines(t *testing.T) {
	ring := diagnostics.NewStderrRing()
	hostile := []string{
		"Authorization: Bearer bearer-token-value",
		"sk_live_hostile-api-token-value",
		"https://url-user:url-password@example.invalid/log?access_token=query-token-value",
		"/home/alice/.pi/agent/config.json",
		`C:\Users\alice\.pi\config.json`,
		"token=opaque-credential-value",
	}
	for _, value := range hostile {
		if _, err := fmt.Fprintln(ring, "child stderr: "+value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fmt.Fprintln(ring, "assistant ready on loopback"); err != nil {
		t.Fatal(err)
	}
	summary := ring.Snapshot()
	if summary.Retained != len(hostile)+1 {
		t.Fatalf("retained = %d, want %d: %#v", summary.Retained, len(hostile)+1, summary)
	}
	joined := ""
	for _, entry := range summary.Entries {
		joined += entry.Text + "\n"
	}
	for _, forbidden := range []string{
		"bearer-token-value", "hostile-api-token-value", "url-user", "url-password", "example.invalid",
		"query-token-value", "alice", "opaque-credential-value",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("retained stderr leaked %q: %s", forbidden, joined)
		}
	}
	if !strings.Contains(joined, "assistant ready on loopback") {
		t.Fatalf("safe line was dropped: %s", joined)
	}
}

func TestStderrRingBoundsLinesBytesAndAge(t *testing.T) {
	ring := diagnostics.NewStderrRing()
	for index := 0; index < diagnostics.DefaultStderrMaxLines+10; index++ {
		fmt.Fprintf(ring, "line-%d\n", index)
	}
	summary := ring.Snapshot()
	if summary.Retained > diagnostics.DefaultStderrMaxLines {
		t.Fatalf("retained %d lines, limit %d", summary.Retained, diagnostics.DefaultStderrMaxLines)
	}
	if summary.Bytes > diagnostics.DefaultStderrMaxBytes {
		t.Fatalf("retained %d bytes, limit %d", summary.Bytes, diagnostics.DefaultStderrMaxBytes)
	}
	if summary.Dropped == 0 {
		t.Fatal("dropped counter did not advance after eviction")
	}
	if !strings.Contains(summary.Entries[len(summary.Entries)-1].Text, "line-") {
		t.Fatalf("newest line missing: %#v", summary.Entries)
	}

	// One line longer than the per-line bound is discarded rather than retained
	// in fragments that could contain a partial secret.
	fmt.Fprintf(ring, "%s\n", strings.Repeat("A", diagnostics.MaxStderrLineBytes*4))
	if got := ring.Len(); got > diagnostics.DefaultStderrMaxLines {
		t.Fatalf("oversized line grew the ring to %d", got)
	}

	// Every line carries a bounded length and a parseable timestamp.
	for _, entry := range ring.Snapshot().Entries {
		if len(entry.Text) > diagnostics.MaxStderrLineBytes {
			t.Fatalf("entry exceeds the line bound: %d", len(entry.Text))
		}
		if _, err := time.Parse(time.RFC3339, entry.At); err != nil {
			t.Fatalf("entry timestamp = %q", entry.At)
		}
	}
}

func TestStderrRingNilIsSafeSink(t *testing.T) {
	var ring *diagnostics.StderrRing
	if written, err := ring.Write([]byte("anything")); err != nil || written != len("anything") {
		t.Fatalf("nil ring Write = %d, %v", written, err)
	}
	ring.AppendLine("anything")
	if summary := ring.Snapshot(); summary.Retained != 0 {
		t.Fatalf("nil ring snapshot = %#v", summary)
	}
}
