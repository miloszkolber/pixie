package diagnostics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// MaxStartRecords bounds the persisted start history.
	MaxStartRecords = 16
	// StartLedgerWindow is the window that counts toward a restart loop.
	StartLedgerWindow = 5 * time.Minute
)

// StartLedger is a small, bounded start-history file used to detect a service
// restart loop before the service manager gives up. It stores only RFC3339
// timestamps: no path, credential, product or error text.
type StartLedger struct {
	path string
	now  func() time.Time
}

// NewStartLedger returns a ledger at path. An empty path disables recording.
func NewStartLedger(path string) StartLedger {
	return StartLedger{path: path, now: time.Now}
}

// NewStartLedgerWithClock is NewStartLedger with an explicit clock, so callers
// can evaluate the restart window deterministically.
func NewStartLedgerWithClock(path string, now func() time.Time) StartLedger {
	if now == nil {
		now = time.Now
	}
	return StartLedger{path: path, now: now}
}

// Recent reports how many recorded starts fall inside the restart window. A
// missing, unreadable or malformed ledger reports 0 rather than claiming a
// problem it cannot establish.
func (ledger StartLedger) Recent() int {
	return len(ledger.window(ledger.read()))
}

// Record prunes old starts, appends the current start and returns the number of
// starts now inside the window. Recording is best-effort: a read-only state
// directory returns the count it could read plus one without failing startup.
func (ledger StartLedger) Record() int {
	if ledger.path == "" {
		return 0
	}
	starts := ledger.window(ledger.read())
	starts = append(starts, ledger.now().UTC())
	sort.Slice(starts, func(left, right int) bool { return starts[left].Before(starts[right]) })
	if len(starts) > MaxStartRecords {
		starts = starts[len(starts)-MaxStartRecords:]
	}
	ledger.write(starts)
	return len(starts)
}

func (ledger StartLedger) window(starts []time.Time) []time.Time {
	if len(starts) == 0 {
		return nil
	}
	cutoff := ledger.now().Add(-StartLedgerWindow)
	result := make([]time.Time, 0, len(starts))
	for _, start := range starts {
		if !start.Before(cutoff) {
			result = append(result, start)
		}
	}
	return result
}

func (ledger StartLedger) read() []time.Time {
	if ledger.path == "" {
		return nil
	}
	content, err := os.ReadFile(ledger.path)
	if err != nil {
		return nil
	}
	var values []string
	if err := json.Unmarshal(content, &values); err != nil {
		return nil
	}
	if len(values) > MaxStartRecords {
		values = values[len(values)-MaxStartRecords:]
	}
	result := make([]time.Time, 0, len(values))
	for _, value := range values {
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			result = append(result, parsed.UTC())
		}
	}
	return result
}

func (ledger StartLedger) write(starts []time.Time) {
	if ledger.path == "" {
		return
	}
	values := make([]string, 0, len(starts))
	for _, start := range starts {
		values = append(values, start.UTC().Format(time.RFC3339))
	}
	content, err := json.Marshal(values)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(ledger.path), 0o700); err != nil {
		return
	}
	temporary, err := os.CreateTemp(filepath.Dir(ledger.path), ".starts-*.tmp")
	if err != nil {
		return
	}
	name := temporary.Name()
	cleanup := func() {
		temporary.Close()
		os.Remove(name)
	}
	if err := temporary.Chmod(0o600); err != nil {
		cleanup()
		return
	}
	if _, err := temporary.Write(append(content, '\n')); err != nil {
		cleanup()
		return
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return
	}
	if err := temporary.Close(); err != nil {
		os.Remove(name)
		return
	}
	if err := os.Rename(name, ledger.path); err != nil {
		os.Remove(name)
	}
}
