package controller

import (
	"encoding/json"
	"sync"
)

// AUX-16 revision-chained frames.
//
// Channel frames carry a monotonic sequence and, for appendable list
// snapshots, a revision chain. A client that misses a frame detects the gap
// from the sequence (or a baseRev mismatch) and resyncs instead of silently
// diverging. The tracker owns only framing metadata; payloads stay owned by the
// caller's immutable byte slice.

// streamAllowedChannels is the fixed set of channels a browser may receive.
// Publish drops anything else, so a new internal channel cannot become an
// accidental browser surface.
var streamAllowedChannels = map[string]bool{
	"project.updated":          true,
	"agent.profileChanged":     true,
	"agent.event":              true,
	"session.deleted":          true,
	"session.lifecycleChanged": true,
	"session.objectiveChanged": true,
	"project.fsChanged":        true,
	"pi.commandCatalogChanged": true,
	"settings.changed":         true,
	"provider.login":           true,
}

// streamTracker is one browser identity's framing state. It is safe for
// concurrent use.
type streamTracker struct {
	mu       sync.Mutex
	seq      uint64
	revision map[string]uint64
	entries  map[string][]json.RawMessage
}

func newStreamTracker() *streamTracker {
	return &streamTracker{revision: make(map[string]uint64), entries: make(map[string][]json.RawMessage)}
}

// reset restarts the sequence and every channel revision chain. A client that
// asked to resync keeps its own state cleared, so the server must send full
// snapshots again rather than a delta based on a revision the client dropped.
func (t *streamTracker) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq = 0
	t.revision = make(map[string]uint64)
	t.entries = make(map[string][]json.RawMessage)
}

// stamp builds one channel frame. It returns the encoded frame bytes. An
// appendable list payload produces a delta carrying rev/baseRev/appended; the
// first frame and any broken chain carry the full data and baseRev 0.
func (t *streamTracker) stamp(channel string, payload []byte) []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq++
	seq := t.seq
	previous, had := t.entries[channel]
	current := decodeJSONArray(payload)
	frame := streamFrame{Channel: channel, Seq: seq}
	if had && current != nil {
		if appended, ok := appendSuffix(previous, current); ok {
			t.revision[channel]++
			t.entries[channel] = current
			frame.Rev = t.revision[channel]
			frame.BaseRev = t.revision[channel] - 1
			frame.Appended = encodeJSONArray(appended)
			if encoded, err := json.Marshal(frame); err == nil {
				return encoded
			}
			return payload
		}
	}
	t.revision[channel]++
	t.entries[channel] = current
	frame.Rev = t.revision[channel]
	frame.Data = json.RawMessage(payload)
	if encoded, err := json.Marshal(frame); err == nil {
		return encoded
	}
	return payload
}

// decodeJSONArray returns the elements when payload is a JSON array, else nil.
func decodeJSONArray(payload []byte) []json.RawMessage {
	if len(payload) == 0 || payload[0] != '[' {
		return nil
	}
	var elements []json.RawMessage
	if err := json.Unmarshal(payload, &elements); err != nil {
		return nil
	}
	if elements == nil {
		elements = []json.RawMessage{}
	}
	return elements
}

func encodeJSONArray(elements []json.RawMessage) []byte {
	encoded, err := json.Marshal(elements)
	if err != nil {
		return []byte("[]")
	}
	return encoded
}

// appendSuffix reports whether current is strictly previous plus a suffix with
// identical leading entries. A reorder, replacement or truncation is not an
// append and forces a full resync.
func appendSuffix(previous, current []json.RawMessage) ([]json.RawMessage, bool) {
	if len(current) <= len(previous) {
		return nil, false
	}
	for index := range previous {
		if !jsonEqual(previous[index], current[index]) {
			return nil, false
		}
	}
	return current[len(previous):], true
}

func jsonEqual(left, right json.RawMessage) bool {
	if string(left) == string(right) {
		return true
	}
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	leftEncoded, leftErr := json.Marshal(leftValue)
	rightEncoded, rightErr := json.Marshal(rightValue)
	return leftErr == nil && rightErr == nil && string(leftEncoded) == string(rightEncoded)
}

// streamFrame is the wire shape for one channel frame. JSON field names stay
// additive so an older client ignores the framing metadata.
type streamFrame struct {
	Channel  string          `json:"channel"`
	Data     json.RawMessage `json:"data,omitempty"`
	Seq      uint64          `json:"seq,omitempty"`
	Rev      uint64          `json:"rev,omitempty"`
	BaseRev  uint64          `json:"baseRev,omitempty"`
	Appended json.RawMessage `json:"appended,omitempty"`
}
