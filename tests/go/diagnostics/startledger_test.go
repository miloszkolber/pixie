package diagnostics_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/diagnostics"
)

func TestStartLedgerCountsAndBoundsRapidStarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pixie", "restarts.json")
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	ledger := diagnostics.NewStartLedgerWithClock(path, func() time.Time { return now })

	if count := ledger.Recent(); count != 0 {
		t.Fatalf("empty ledger recent = %d", count)
	}
	for index := 0; index < diagnostics.RestartLoopThreshold; index++ {
		count := ledger.Record()
		if count != index+1 {
			t.Fatalf("record %d count = %d", index, count)
		}
	}
	if count := ledger.Recent(); count != diagnostics.RestartLoopThreshold {
		t.Fatalf("recent = %d, want %d", count, diagnostics.RestartLoopThreshold)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("ledger mode = %o, want 600", info.Mode().Perm())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Bearer ", "sk_live", "https://", "/home/", "token="} {
		if containsBytes(content, forbidden) {
			t.Fatalf("ledger leaked %q: %s", forbidden, content)
		}
	}
}

func TestStartLedgerWindowAndMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restarts.json")
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	ledger := diagnostics.NewStartLedgerWithClock(path, func() time.Time { return now })
	ledger.Record()
	ledger = diagnostics.NewStartLedgerWithClock(path, func() time.Time { return now.Add(diagnostics.StartLedgerWindow + time.Minute) })
	if count := ledger.Record(); count != 1 {
		t.Fatalf("start outside the window was counted: %d", count)
	}

	malformed := filepath.Join(t.TempDir(), "malformed.json")
	if err := os.WriteFile(malformed, []byte("Bearer bearer-token-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if count := diagnostics.NewStartLedger(malformed).Recent(); count != 0 {
		t.Fatalf("malformed ledger recent = %d", count)
	}
}

func containsBytes(content []byte, value string) bool {
	for index := 0; index+len(value) <= len(content); index++ {
		if string(content[index:index+len(value)]) == value {
			return true
		}
	}
	return false
}
