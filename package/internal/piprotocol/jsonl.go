// Bounded native JSONL framing for GO-02.
//
// One reader and one serialized writer per managed native child exchange
// LF-delimited JSON records. An optional carriage return immediately before
// LF is accepted; no other Unicode separator delimits a record. Whole
// records and whole writes are bounded; framing never truncates JSON and
// never treats a log line as an event. Slow consumers must not grow buffers
// without bound: oversized input fails before accumulation completes and
// incomplete trailing bytes stay incomplete instead of becoming an event.
package piprotocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"
)

const (
	// NativeJSONLMaxRecordBytes bounds one native JSONL record in serialized
	// UTF-8 bytes, excluding the LF delimiter and its optional preceding CR.
	// It matches the 32 MiB host/native frame ceiling from contracts.md and
	// is not an allocation budget: decoded structures are bounded
	// separately by the caller.
	NativeJSONLMaxRecordBytes = 32 * 1024 * 1024
	// NativeJSONLMaxWriteBytes bounds one serialized native write, excluding
	// the delimiter.
	NativeJSONLMaxWriteBytes = 32 * 1024 * 1024
	// NativeJSONLAggregateMaxBytes bounds all buffered native transport data
	// per assistant engine across reading, queued requests, replay and
	// outbound buffers.
	NativeJSONLAggregateMaxBytes = 64 * 1024 * 1024
	// NativeJSONLControlReserveBytes is the serialized-storage reserve that
	// ordinary traffic cannot consume. The control lane (Stop, UI
	// cancellation, service draining) keeps bounded-latency admission under
	// ordinary-load saturation.
	NativeJSONLControlReserveBytes = 1024 * 1024
	// NativeJSONLControlMaxOps bounds independently admitted control
	// operations per engine.
	NativeJSONLControlMaxOps = 8
	// NativeJSONLControlMaxBytesEach bounds one control record in serialized
	// UTF-8 bytes.
	NativeJSONLControlMaxBytesEach = 64 * 1024
	// NativeJSONLMaxPendingPerChild bounds correlated pending native calls
	// for one managed child.
	NativeJSONLMaxPendingPerChild = 128
)

const (
	// NativeJSONLHelloTimeout bounds the native hello/handshake.
	NativeJSONLHelloTimeout = 10 * time.Second
	// NativeJSONLWriteTimeout bounds one serialized native write before the
	// pipe counts as stalled.
	NativeJSONLWriteTimeout = 10 * time.Second
	// NativeJSONLReadStallTimeout bounds waiting for one complete native
	// record before the pipe counts as stalled.
	NativeJSONLReadStallTimeout = 30 * time.Second
	// NativeJSONLAbortGrace bounds the native abort grace before escalation.
	NativeJSONLAbortGrace = 10 * time.Second
	// NativeJSONLDrainTimeout bounds the complete native drain, including
	// pipe cleanup and pending-operation settlement.
	NativeJSONLDrainTimeout = 25 * time.Second
)

// NativeTransportErrorKind names one bounded-transport failure.
type NativeTransportErrorKind string

const (
	NativeTransportTooBig       NativeTransportErrorKind = "too_big"
	NativeTransportIncomplete   NativeTransportErrorKind = "incomplete"
	NativeTransportInvalidUTF8  NativeTransportErrorKind = "invalid_utf8"
	NativeTransportInvalidJSON  NativeTransportErrorKind = "invalid_json"
	NativeTransportLogLine      NativeTransportErrorKind = "log_line"
	NativeTransportDuplicate    NativeTransportErrorKind = "duplicate"
	NativeTransportBackpressure NativeTransportErrorKind = "backpressure"
	NativeTransportStalled      NativeTransportErrorKind = "stalled"
	NativeTransportChildExit    NativeTransportErrorKind = "child_exit"
)

// NativeTransportError is one bounded native-transport failure. Partially
// dispatched requests that fail this way keep an uncertain outcome and tear
// down safely instead of being resent as fresh work.
type NativeTransportError struct {
	Kind    NativeTransportErrorKind
	Message string
	Timeout time.Duration
}

func (e *NativeTransportError) Error() string { return e.Message }

// NewNativeTransportError builds one typed transport failure.
func NewNativeTransportError(kind NativeTransportErrorKind, message string) *NativeTransportError {
	return &NativeTransportError{Kind: kind, Message: message}
}

// NewNativeStalledError reports a stalled pipe with its explicit timeout.
func NewNativeStalledError(operation string, timeout time.Duration) *NativeTransportError {
	return &NativeTransportError{
		Kind:    NativeTransportStalled,
		Message: fmt.Sprintf("%s stalled after %s", operation, timeout),
		Timeout: timeout,
	}
}

// NewNativeChildExitError fails pending work when the managed child exits.
// Callbacks are invalidated; unsettled delivery is interrupted/uncertain.
func NewNativeChildExitError(reason string) *NativeTransportError {
	if reason == "" {
		reason = "native child exited"
	}
	return &NativeTransportError{Kind: NativeTransportChildExit, Message: reason}
}

// NativeTransportErrorKindOf extracts the typed kind from a transport failure.
func NativeTransportErrorKindOf(err error) (NativeTransportErrorKind, bool) {
	if transportErr, ok := err.(*NativeTransportError); ok {
		return transportErr.Kind, true
	}
	return "", false
}

// SplitNativeJSONLRecord is a bufio.Scanner split function for native JSONL.
// LF delimits a record; one optional CR immediately before LF is stripped.
// Only the 0x0A byte delimits: Unicode separators (for example U+2028) inside
// a JSON string never split a record. Oversized or invalid-UTF-8 lines fail
// instead of growing without bound; an incomplete trailing line at EOF stays
// incomplete and is never returned as an event.
func SplitNativeJSONLRecord(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if index := bytes.IndexByte(data, '\n'); index >= 0 {
		line := data[:index]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if len(line) > NativeJSONLMaxRecordBytes {
			return 0, nil, NewNativeTransportError(
				NativeTransportTooBig,
				fmt.Sprintf("native record exceeds the %d-byte limit", NativeJSONLMaxRecordBytes),
			)
		}
		if !utf8.Valid(line) {
			return 0, nil, NewNativeTransportError(NativeTransportInvalidUTF8, "native record is not valid UTF-8")
		}
		return index + 1, line, nil
	}
	if len(data) > NativeJSONLMaxRecordBytes {
		return 0, nil, NewNativeTransportError(
			NativeTransportTooBig,
			fmt.Sprintf("native record exceeds the %d-byte limit", NativeJSONLMaxRecordBytes),
		)
	}
	if atEOF {
		if len(data) == 0 {
			return 0, nil, nil
		}
		return 0, nil, NewNativeTransportError(NativeTransportIncomplete, "incomplete trailing native record")
	}
	return 0, nil, nil
}

// EncodeNativeJSONLRecord serializes one native record plus its LF
// delimiter. Oversized payloads fail without truncation, and records are
// never interleaved by this function: callers write the returned bytes
// through the serialized writer.
func EncodeNativeJSONLRecord(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, NewNativeTransportError(NativeTransportInvalidJSON, "native record is not serializable")
	}
	if len(raw) > NativeJSONLMaxWriteBytes {
		return nil, NewNativeTransportError(
			NativeTransportTooBig,
			fmt.Sprintf("native write exceeds the %d-byte limit", NativeJSONLMaxWriteBytes),
		)
	}
	if !utf8.Valid(raw) {
		return nil, NewNativeTransportError(NativeTransportInvalidUTF8, "native record is not valid UTF-8")
	}
	return append(raw, '\n'), nil
}

// ParseNativeJSONLRecord validates one framed native record. The input is the
// split line without the delimiter; a single trailing CR is still accepted
// here for callers that do not use the split function. Non-object JSON and
// malformed JSON are log lines, never events: they fail with LogLine/Invalid
// instead of being dispatched.
func ParseNativeJSONLRecord(line []byte) (json.RawMessage, error) {
	trimmed := bytes.TrimSuffix(line, []byte{'\n'})
	trimmed = bytes.TrimSuffix(trimmed, []byte{'\r'})
	if len(trimmed) > NativeJSONLMaxRecordBytes {
		return nil, NewNativeTransportError(
			NativeTransportTooBig,
			fmt.Sprintf("native record exceeds the %d-byte limit", NativeJSONLMaxRecordBytes),
		)
	}
	if !utf8.Valid(trimmed) {
		return nil, NewNativeTransportError(NativeTransportInvalidUTF8, "native record is not valid UTF-8")
	}
	stripped := bytes.TrimSpace(trimmed)
	if len(stripped) == 0 {
		return nil, NewNativeTransportError(NativeTransportLogLine, "native log line is not an event")
	}
	if stripped[0] != '{' {
		return nil, NewNativeTransportError(NativeTransportLogLine, "native log line is not an event")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return nil, NewNativeTransportError(NativeTransportInvalidJSON, "invalid native JSON")
	}
	if string(bytes.TrimSpace(trimmed)) == "null" {
		return nil, NewNativeTransportError(NativeTransportLogLine, "native log line is not an event")
	}
	duplicated := make([]byte, len(trimmed))
	copy(duplicated, trimmed)
	return json.RawMessage(duplicated), nil
}
