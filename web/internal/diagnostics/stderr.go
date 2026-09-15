package diagnostics

import (
	"bytes"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultStderrMaxBytes bounds the retained redacted text per ring.
	DefaultStderrMaxBytes = 16 * 1024
	// DefaultStderrMaxAge bounds how long a retained line stays visible.
	DefaultStderrMaxAge = 10 * time.Minute
	// DefaultStderrMaxLines bounds the retained line count per ring.
	DefaultStderrMaxLines = 64
	// MaxStderrLineBytes bounds one redacted line before it is stored.
	MaxStderrLineBytes = 512
)

// StderrEntry is one retained, already-redacted child stderr line.
type StderrEntry struct {
	At   string `json:"at"`
	Text string `json:"text"`
}

// StderrSummary is the bounded, redacted view of a child stderr ring. It never
// contains raw child text: every entry passed through SanitizeDiagnosticText.
type StderrSummary struct {
	Retained int           `json:"retained"`
	Dropped  int           `json:"dropped"`
	Bytes    int           `json:"bytes"`
	Entries  []StderrEntry `json:"entries,omitempty"`
}

// StderrRing retains a bounded, aged, redacted tail of one child process's
// stderr. It is an io.Writer so it can be used directly with exec.Cmd and is
// safe for concurrent writes.
type StderrRing struct {
	mu         sync.Mutex
	now        func() time.Time
	maxBytes   int
	maxAge     time.Duration
	maxLines   int
	entries    []StderrEntry
	totalBytes int
	dropped    int
	partial    []byte
	discarding bool
}

// NewStderrRing returns a ring with the package defaults. A nil ring is a safe
// no-op sink.
func NewStderrRing() *StderrRing {
	return &StderrRing{
		now:      time.Now,
		maxBytes: DefaultStderrMaxBytes,
		maxAge:   DefaultStderrMaxAge,
		maxLines: DefaultStderrMaxLines,
	}
}

// Write splits p into lines and retains each redacted line. It always reports
// the full length so a write error can never stall the child.
func (r *StderrRing) Write(p []byte) (int, error) {
	if r == nil {
		return len(p), nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	consumed := 0
	for consumed < len(p) {
		index := bytes.IndexByte(p[consumed:], '\n')
		if index < 0 {
			r.appendPartialLocked(p[consumed:])
			break
		}
		r.appendPartialLocked(p[consumed : consumed+index])
		r.flushPartialLocked()
		consumed += index + 1
	}
	return len(p), nil
}

// AppendLine retains one already-delimited line.
func (r *StderrRing) AppendLine(line string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entryLocked(line)
}

func (r *StderrRing) appendPartialLocked(chunk []byte) {
	if r.discarding {
		return
	}
	if len(r.partial)+len(chunk) > MaxStderrLineBytes {
		remaining := MaxStderrLineBytes - len(r.partial)
		if remaining > 0 {
			r.partial = append(r.partial, chunk[:remaining]...)
		}
		// A line longer than the per-line bound is discarded rather than
		// retained in fragments, so a runaway writer cannot force a partial
		// secret into the tail.
		r.partial = nil
		r.discarding = true
		return
	}
	r.partial = append(r.partial, chunk...)
}

func (r *StderrRing) flushPartialLocked() {
	if r.discarding {
		r.discarding = false
		r.partial = nil
		r.dropped++
		return
	}
	line := string(r.partial)
	r.partial = nil
	r.entryLocked(line)
}

func (r *StderrRing) entryLocked(line string) {
	text := SanitizeDiagnosticText(line, MaxStderrLineBytes)
	if strings.TrimSpace(text) == "" {
		return
	}
	r.entries = append(r.entries, StderrEntry{
		At:   r.now().UTC().Format(time.RFC3339),
		Text: text,
	})
	r.totalBytes += len(text)
	r.pruneLocked()
}

func (r *StderrRing) pruneLocked() {
	cutoff := r.now().Add(-r.maxAge)
	for len(r.entries) > 0 {
		oldest := r.entries[0]
		expired := false
		if parsed, err := time.Parse(time.RFC3339, oldest.At); err == nil {
			expired = parsed.Before(cutoff)
		}
		if !expired && len(r.entries) <= r.maxLines && r.totalBytes <= r.maxBytes {
			break
		}
		r.totalBytes -= len(oldest.Text)
		r.entries = r.entries[1:]
		r.dropped++
	}
}

// Snapshot prunes stale entries and returns a bounded copy. The entries slice
// is freshly allocated, so callers cannot mutate the ring.
func (r *StderrRing) Snapshot() StderrSummary {
	if r == nil {
		return StderrSummary{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()
	entries := append([]StderrEntry(nil), r.entries...)
	return StderrSummary{
		Retained: len(entries),
		Dropped:  r.dropped,
		Bytes:    r.totalBytes,
		Entries:  entries,
	}
}

// Len reports the number of retained lines. It is a convenience for tests.
func (r *StderrRing) Len() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}
