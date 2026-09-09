// Bounded native JSONL admission, correlation, child-exit and stalled-pipe
// handling for GO-02.
//
// One reader and one serialized writer per managed child correlate native
// calls by NativeCorrelationID. Aggregate serialized-byte admission covers
// reading, queued requests, replay and outbound buffers; a small control
// lane stays reserved for Stop, UI cancellation and service draining so
// large history/data work cannot consume it. Child exit invalidates
// callbacks, fails pending calls and marks unsettled delivery
// interrupted/uncertain while keeping transcript state. Stalled pipes fail
// with explicit timeouts; a partially dispatched request keeps its uncertain
// outcome and tears down safely instead of being resent.
package piprotocol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// NativeTransportConfig carries the explicit bounds and timeouts for one
// native child transport. Zero values are replaced by
// DefaultNativeTransportConfig.
type NativeTransportConfig struct {
	MaxRecordBytes      int
	AggregateMaxBytes   int
	ControlReserveBytes int
	ControlMaxOps       int
	ControlMaxBytesEach int
	MaxPending          int
	WriteTimeout        time.Duration
	ReadStallTimeout    time.Duration
	HelloTimeout        time.Duration
	DrainTimeout        time.Duration
}

// DefaultNativeTransportConfig returns the contracts.md initial bounds with
// explicit timeouts. No buffer is unbounded.
func DefaultNativeTransportConfig() NativeTransportConfig {
	return NativeTransportConfig{
		MaxRecordBytes:      NativeJSONLMaxRecordBytes,
		AggregateMaxBytes:   NativeJSONLAggregateMaxBytes,
		ControlReserveBytes: NativeJSONLControlReserveBytes,
		ControlMaxOps:       NativeJSONLControlMaxOps,
		ControlMaxBytesEach: NativeJSONLControlMaxBytesEach,
		MaxPending:          NativeJSONLMaxPendingPerChild,
		WriteTimeout:        NativeJSONLWriteTimeout,
		ReadStallTimeout:    NativeJSONLReadStallTimeout,
		HelloTimeout:        NativeJSONLHelloTimeout,
		DrainTimeout:        NativeJSONLDrainTimeout,
	}
}

// withDefaults fills zero config fields from the contracts defaults.
func (c NativeTransportConfig) withDefaults() NativeTransportConfig {
	defaults := DefaultNativeTransportConfig()
	if c.MaxRecordBytes <= 0 {
		c.MaxRecordBytes = defaults.MaxRecordBytes
	}
	if c.AggregateMaxBytes <= 0 {
		c.AggregateMaxBytes = defaults.AggregateMaxBytes
	}
	if c.ControlReserveBytes <= 0 {
		c.ControlReserveBytes = defaults.ControlReserveBytes
	}
	if c.ControlMaxOps <= 0 {
		c.ControlMaxOps = defaults.ControlMaxOps
	}
	if c.ControlMaxBytesEach <= 0 {
		c.ControlMaxBytesEach = defaults.ControlMaxBytesEach
	}
	if c.MaxPending <= 0 {
		c.MaxPending = defaults.MaxPending
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = defaults.WriteTimeout
	}
	if c.ReadStallTimeout <= 0 {
		c.ReadStallTimeout = defaults.ReadStallTimeout
	}
	if c.HelloTimeout <= 0 {
		c.HelloTimeout = defaults.HelloTimeout
	}
	if c.DrainTimeout <= 0 {
		c.DrainTimeout = defaults.DrainTimeout
	}
	return c
}

// NativeAggregateBudget accounts serialized bytes across reading, queued
// requests, replay and outbound buffers. Ordinary traffic cannot consume the
// control reserve; control traffic has its own op and byte caps. Every
// Reserve pairs with exactly one Release on a terminal path.
type NativeAggregateBudget struct {
	mu             sync.Mutex
	aggregateMax   int
	reserve        int
	controlMaxOps  int
	controlMaxEach int
	used           int
	controlUsed    int
	controlOps     int
}

// NewNativeAggregateBudget returns an empty budget for one engine scope.
func NewNativeAggregateBudget(config NativeTransportConfig) *NativeAggregateBudget {
	config = config.withDefaults()
	return &NativeAggregateBudget{
		aggregateMax:   config.AggregateMaxBytes,
		reserve:        config.ControlReserveBytes,
		controlMaxOps:  config.ControlMaxOps,
		controlMaxEach: config.ControlMaxBytesEach,
	}
}

// ReserveOrdinary admits n serialized bytes for ordinary traffic. Large
// history/data work cannot consume the control reserve. Per-record framing
// caps live in jsonl.go; the budget admits aggregate units incrementally up
// to the engine aggregate ceiling.
func (b *NativeAggregateBudget) ReserveOrdinary(n int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n < 0 || n > b.aggregateMax {
		return NewNativeTransportError(NativeTransportTooBig, "native admission unit out of bounds")
	}
	if b.used+n > b.aggregateMax {
		return NewNativeTransportError(NativeTransportBackpressure, "native aggregate budget exceeded")
	}
	if b.used+n > b.aggregateMax-b.reserve {
		return NewNativeTransportError(NativeTransportBackpressure, "native control reserve is not available to ordinary traffic")
	}
	b.used += n
	return nil
}

// ReserveControl admits n serialized bytes on the reserved control lane.
func (b *NativeAggregateBudget) ReserveControl(n int) error {
	if n < 0 {
		return NewNativeTransportError(NativeTransportTooBig, "native admission unit out of bounds")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > b.controlMaxEach {
		return NewNativeTransportError(NativeTransportTooBig, "native control record exceeds the 64 KiB limit")
	}
	if b.controlOps >= b.controlMaxOps {
		return NewNativeTransportError(NativeTransportBackpressure, "native control lane is saturated")
	}
	if b.controlUsed+n > b.reserve {
		return NewNativeTransportError(NativeTransportBackpressure, "native control reserve exceeded")
	}
	if b.used+n > b.aggregateMax {
		return NewNativeTransportError(NativeTransportBackpressure, "native aggregate budget exceeded")
	}
	b.used += n
	b.controlUsed += n
	b.controlOps++
	return nil
}

// ReleaseOrdinary frees n ordinary bytes. Call once per successful ordinary
// Reserve on every terminal path.
func (b *NativeAggregateBudget) ReleaseOrdinary(n int) {
	if n <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.used -= n
	if b.used < 0 {
		b.used = 0
	}
}

// ReleaseControl frees n control bytes and one control slot.
func (b *NativeAggregateBudget) ReleaseControl(n int) {
	if n <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.used -= n
	if b.used < 0 {
		b.used = 0
	}
	b.controlUsed -= n
	if b.controlUsed < 0 {
		b.controlUsed = 0
	}
	if b.controlOps > 0 {
		b.controlOps--
	}
}

// Used returns admitted aggregate bytes.
func (b *NativeAggregateBudget) Used() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

// ControlUsed returns admitted control bytes.
func (b *NativeAggregateBudget) ControlUsed() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.controlUsed
}

// ControlOps returns admitted control operations.
func (b *NativeAggregateBudget) ControlOps() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.controlOps
}

// NativePendingTable correlates pending native calls for one child. It is
// bounded: duplicates fail and overflow applies backpressure instead of
// growing without bound.
type NativePendingTable struct {
	mu  sync.Mutex
	ids map[NativeCorrelationID]bool
	max int
}

// NewNativePendingTable returns an empty correlation table.
func NewNativePendingTable(config NativeTransportConfig) *NativePendingTable {
	config = config.withDefaults()
	return &NativePendingTable{ids: make(map[NativeCorrelationID]bool), max: config.MaxPending}
}

// Add admits one native correlation. Zero (absent) never correlates;
// duplicates fail so a retry must use a fresh correlation.
func (t *NativePendingTable) Add(id NativeCorrelationID) error {
	if !id.IsSet() {
		return NewNativeTransportError(NativeTransportInvalidJSON, "native correlation is absent")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ids == nil {
		t.ids = make(map[NativeCorrelationID]bool)
	}
	if _, exists := t.ids[id]; exists {
		return NewNativeTransportError(NativeTransportDuplicate, "native correlation is already in flight")
	}
	if len(t.ids) >= t.max {
		return NewNativeTransportError(NativeTransportBackpressure, "too many pending native calls")
	}
	t.ids[id] = false
	return nil
}

// MarkAccepted records the native acknowledgement for one pending call. A
// later child exit then reports interrupted rather than uncertain delivery.
func (t *NativePendingTable) MarkAccepted(id NativeCorrelationID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.ids[id]; exists {
		t.ids[id] = true
	}
}

// Remove releases one correlation. Unknown IDs are ignored.
func (t *NativePendingTable) Remove(id NativeCorrelationID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.ids, id)
}

// Len returns the number of pending correlations.
func (t *NativePendingTable) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.ids)
}

// DrainOnExit fails every pending correlation when the managed child exits.
// Callbacks are invalidated; the caller marks each returned ID
// interrupted/uncertain via NativeUnsettledOutcome and never resends the
// partially dispatched work as fresh operations.
func (t *NativePendingTable) DrainOnExit() []NativeCorrelationID {
	t.mu.Lock()
	defer t.mu.Unlock()
	drained := make([]NativeCorrelationID, 0, len(t.ids))
	for id := range t.ids {
		drained = append(drained, id)
	}
	t.ids = make(map[NativeCorrelationID]bool)
	return drained
}

// NativePendingOutcome is the explicit terminal result of one callback when a
// child exits. No outcome authorizes an automatic resend.
type NativePendingOutcome struct {
	ID      NativeCorrelationID
	Outcome HostV2DeliveryState
}

// DrainOnExitOutcomes drains pending calls while preserving acceptance state.
func (t *NativePendingTable) DrainOnExitOutcomes() []NativePendingOutcome {
	t.mu.Lock()
	defer t.mu.Unlock()
	outcomes := make([]NativePendingOutcome, 0, len(t.ids))
	for id, accepted := range t.ids {
		outcomes = append(outcomes, NativePendingOutcome{ID: id, Outcome: NativeUnsettledOutcome(accepted)})
	}
	t.ids = make(map[NativeCorrelationID]bool)
	return outcomes
}

// NativeUnsettledOutcome maps child-exit timing to durable delivery state.
// Accepted work was queued/handled, so exit interrupts it; work lost before
// acceptance is uncertain. Both stay terminal and block automatic follow-up
// until explicit resolution or native settlement evidence.
func NativeUnsettledOutcome(wasAccepted bool) HostV2DeliveryState {
	if wasAccepted {
		return HostV2DeliveryInterrupted
	}
	return HostV2DeliveryUncertain
}

// NativeWriter serializes native writes for one child. Records never
// interleave: one mutex owns the pipe until the bounded write completes or
// its explicit timeout fires.
type NativeWriter struct {
	mu       sync.Mutex
	writer   io.Writer
	timeout  time.Duration
	maxBytes int
	failed   error
}

// NewNativeWriter returns a serialized writer over one child stdin pipe.
func NewNativeWriter(writer io.Writer, config NativeTransportConfig) *NativeWriter {
	config = config.withDefaults()
	return &NativeWriter{writer: writer, timeout: config.WriteTimeout, maxBytes: config.MaxRecordBytes + 1}
}

// WriteRecord writes one LF-terminated record within the explicit write
// timeout. Oversized writes fail without partial bytes; a stalled pipe fails
// with a Stalled error so the caller preserves the uncertain outcome and
// tears down instead of resending.
func (w *NativeWriter) WriteRecord(record []byte) error {
	if len(record) > w.maxBytes {
		return NewNativeTransportError(
			NativeTransportTooBig,
			fmt.Sprintf("native write exceeds the %d-byte limit", w.maxBytes-1),
		)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failed != nil {
		return w.failed
	}
	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		written, err := w.writer.Write(record)
		if err == nil && written != len(record) {
			err = NewNativeTransportError(NativeTransportStalled, "native write completed only partially")
		}
		done <- result{err: err}
	}()
	timer := time.NewTimer(w.timeout)
	defer timer.Stop()
	select {
	case outcome := <-done:
		if outcome.err != nil {
			w.failed = outcome.err
		}
		return outcome.err
	case <-timer.C:
		w.failed = NewNativeStalledError("native write", w.timeout)
		return w.failed
	}
}

// ReadNativeRecord reads one complete LF-delimited native record within the
// explicit stall timeout, or fails when the child exits first. The caller
// supplies done, closed when the managed child exits; pending reads then
// fail with ChildExit instead of blocking behind teardown. Empty keep-alive
// lines are skipped without counting as events. The returned release frees
// the aggregate reservation and must run once on every terminal path.
func ReadNativeRecord(
	reader *bufio.Reader,
	budget *NativeAggregateBudget,
	ordinary bool,
	timeout time.Duration,
	done <-chan struct{},
) (json.RawMessage, func(), error) {
	return readNativeRecord(reader, budget, ordinary, timeout, done, DefaultNativeTransportConfig())
}

// ReadNativeRecordWithConfig is the configurable reader used by transports
// that lower limits for a fixture or a managed child. It retains the same
// terminal release contract as ReadNativeRecord.
func ReadNativeRecordWithConfig(
	reader *bufio.Reader,
	budget *NativeAggregateBudget,
	ordinary bool,
	timeout time.Duration,
	done <-chan struct{},
	config NativeTransportConfig,
) (json.RawMessage, func(), error) {
	return readNativeRecord(reader, budget, ordinary, timeout, done, config.withDefaults())
}

// readNativeRecord owns the bounded accumulation loop: each chunk is admitted
// before it is retained, and partial reservations release on failure.
func readNativeRecord(
	reader *bufio.Reader,
	budget *NativeAggregateBudget,
	ordinary bool,
	timeout time.Duration,
	done <-chan struct{},
	config NativeTransportConfig,
) (json.RawMessage, func(), error) {
	type outcome struct {
		record  []byte
		reserve int
		control bool
		err     error
	}
	doneCh := make(chan outcome, 1)
	go func() {
		var accumulated []byte
		reserved := 0
		isControl := !ordinary
		release := func() {
			if reserved <= 0 {
				return
			}
			if isControl {
				budget.ReleaseControl(reserved)
			} else {
				budget.ReleaseOrdinary(reserved)
			}
			reserved = 0
		}
		defer func() {
			if reserved > 0 {
				// Partial accumulation on internal failure releases here;
				// successful reads transfer ownership to the caller release.
				release()
			}
		}()
		for {
			chunk, err := reader.ReadSlice('\n')
			if len(chunk) > 0 {
				// Reserve incrementally before accumulating so a large body
				// cannot bypass the concurrent-request cap.
				if isControl {
					if reserveErr := budget.ReserveControl(len(chunk)); reserveErr != nil {
						doneCh <- outcome{err: reserveErr}
						return
					}
				} else if reserveErr := budget.ReserveOrdinary(len(chunk)); reserveErr != nil {
					doneCh <- outcome{err: reserveErr}
					return
				}
				reserved += len(chunk)
				if reserved > config.MaxRecordBytes+1 {
					doneCh <- outcome{err: NewNativeTransportError(
						NativeTransportTooBig,
						fmt.Sprintf("native record exceeds the %d-byte limit", config.MaxRecordBytes),
					)}
					return
				}
				accumulated = append(accumulated, chunk...)
				if chunk[len(chunk)-1] == '\n' {
					line := accumulated[:len(accumulated)-1]
					if len(line) > 0 && line[len(line)-1] == '\r' {
						line = line[:len(line)-1]
					}
					if len(line) > config.MaxRecordBytes {
						doneCh <- outcome{err: NewNativeTransportError(
							NativeTransportTooBig,
							fmt.Sprintf("native record exceeds the %d-byte limit", config.MaxRecordBytes),
						)}
						return
					}
					if len(bytes.TrimSpace(line)) == 0 {
						// Skip keep-alive blanks without events.
						if reserved > 0 {
							release()
							accumulated = nil
						}
						continue
					}
					record, parseErr := ParseNativeJSONLRecord(line)
					if parseErr != nil {
						doneCh <- outcome{err: parseErr}
						return
					}
					// Transfer reservation ownership to the caller.
					owned := reserved
					reserved = 0
					doneCh <- outcome{record: []byte(record), reserve: owned, control: isControl}
					return
				}
			}
			if err != nil {
				if errors.Is(err, bufio.ErrBufferFull) {
					continue
				}
				if err == io.EOF {
					doneCh <- outcome{err: NewNativeTransportError(NativeTransportIncomplete, "incomplete trailing native record")}
					return
				}
				doneCh <- outcome{err: err}
				return
			}
		}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-doneCh:
		if result.err != nil {
			noop := func() {}
			return nil, noop, result.err
		}
		release := func() {
			if result.control {
				budget.ReleaseControl(result.reserve)
			} else {
				budget.ReleaseOrdinary(result.reserve)
			}
		}
		return json.RawMessage(result.record), release, nil
	case <-timer.C:
		return nil, func() {}, NewNativeStalledError("native read", timeout)
	case <-done:
		return nil, func() {}, NewNativeChildExitError("native child exited while reading")
	}
}
