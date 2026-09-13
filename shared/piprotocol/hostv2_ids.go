// Host v2 separated request-ID domains for API-02/X06 (F34).
//
// Three wire domains stay distinct and are never coerced:
//
//   - BrowserRequestID is the browser protocol's opaque string correlation /
//     replay ID, scoped by client/connection policy.
//   - HostRequestID is the host v2 positive safe integer, scoped to one host
//     transport connection.
//   - NativeCorrelationID is the native uint64 correlation, scoped to one
//     managed native child.
//
// Adapters keep explicit maps at each boundary. A retry uses a new host
// transport ID plus the original mutation identity; reconnect invalidates
// transport mappings, never the mutation ledger. The helpers below reject
// cross-domain JSON shapes (host numbers vs. browser strings vs. native
// numbers) so browser string "001" never becomes host integer 1 and host IDs
// never flow to native callbacks as durable identity.
package piprotocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

// HostV2MaxSafeInteger is the largest host v2 request ID: 2^53-1.
// Host IDs are JSON numbers; browser IDs are JSON strings.
const HostV2MaxSafeInteger = 9007199254740991

// HostV2BrowserIDMaxBytes bounds one browser string ID in serialized UTF-8
// bytes. Unicode/escaping cannot bypass it because len counts UTF-8 bytes.
const HostV2BrowserIDMaxBytes = 256

// BrowserRequestID is an opaque browser string correlation ID.
type BrowserRequestID string

// HostRequestID is a host v2 positive safe integer transport ID.
type HostRequestID int64

// NativeCorrelationID is a native uint64 correlation scoped to one child.
// Zero means absent; nonzero values are live correlations.
type NativeCorrelationID uint64

// Validate reports whether a browser ID is well-formed.
func (id BrowserRequestID) Validate() error {
	raw := string(id)
	if len(raw) == 0 || len(raw) > HostV2BrowserIDMaxBytes {
		return fmt.Errorf("browser request id out of bounds")
	}
	if !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) {
		return fmt.Errorf("browser request id is invalid")
	}
	return nil
}

// Validate reports whether a host ID is a positive safe integer.
func (id HostRequestID) Validate() error {
	if id <= 0 || int64(id) > HostV2MaxSafeInteger {
		return fmt.Errorf("host request id out of bounds")
	}
	return nil
}

// IsSet reports whether a native correlation is present.
func (id NativeCorrelationID) IsSet() bool { return id != 0 }

// Validate reports whether a native correlation is well-formed.
// Zero (absent) is valid; any uint64 value is otherwise valid.
func (id NativeCorrelationID) Validate() error { return nil }

// MarshalJSON preserves the browser string domain.
func (id BrowserRequestID) MarshalJSON() ([]byte, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(string(id))
}

// UnmarshalJSON rejects non-string JSON (notably host numbers).
func (id *BrowserRequestID) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("browser request id must be a string")
	}
	candidate := BrowserRequestID(value)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*id = candidate
	return nil
}

// MarshalJSON preserves the host numeric domain.
func (id HostRequestID) MarshalJSON() ([]byte, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(int64(id))
}

// UnmarshalJSON rejects quoted strings, fractions, and out-of-range values.
func (id *HostRequestID) UnmarshalJSON(raw []byte) error {
	parsed, err := parseHostV2NumberID(raw, HostV2MaxSafeInteger)
	if err != nil {
		return err
	}
	*id = HostRequestID(parsed)
	return nil
}

// MarshalJSON preserves the native numeric domain.
func (id NativeCorrelationID) MarshalJSON() ([]byte, error) {
	return json.Marshal(uint64(id))
}

// UnmarshalJSON rejects quoted strings and negative/fractional JSON.
func (id *NativeCorrelationID) UnmarshalJSON(raw []byte) error {
	parsed, err := parseNativeV2NumberID(raw)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

func parseHostV2NumberID(raw []byte, max int64) (int64, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return 0, fmt.Errorf("host request id must be a number")
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		return 0, fmt.Errorf("host request id must be a number, not a string")
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, fmt.Errorf("host request id must be a number")
	}
	// Strict integer check: reject fractions/exponents that are not integers
	// by round-tripping through int64 and float comparison.
	asInt, err := number.Int64()
	if err != nil {
		return 0, fmt.Errorf("host request id must be an integer")
	}
	// Reject "1.0"/"1e3" spellings that hide coercion: the JSON text must be
	// a plain decimal integer.
	text := string(number.String())
	if strings.ContainsAny(text, ".eE") {
		// Allow only when the numeric value is still an integer, but require
		// canonical integer spelling for host IDs to avoid "1.0" == 1 drift.
		return 0, fmt.Errorf("host request id must use integer spelling")
	}
	if strings.HasPrefix(text, "+") {
		return 0, fmt.Errorf("host request id must use integer spelling")
	}
	if asInt <= 0 || asInt > max {
		return 0, fmt.Errorf("host request id out of bounds")
	}
	return asInt, nil
}

func parseNativeV2NumberID(raw []byte) (NativeCorrelationID, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return 0, fmt.Errorf("native correlation must be a number")
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		return 0, fmt.Errorf("native correlation must be a number, not a string")
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, fmt.Errorf("native correlation must be a number")
	}
	text := string(number.String())
	if strings.ContainsAny(text, ".eE") || strings.HasPrefix(text, "-") || strings.HasPrefix(text, "+") {
		return 0, fmt.Errorf("native correlation must use unsigned integer spelling")
	}
	// Use unsigned parsing so values above the host safe-integer ceiling
	// remain valid in the native domain.
	var value uint64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("native correlation out of bounds")
	}
	// Re-encode check: reject leading zeros like "001" as non-canonical JSON
	// numbers (encoding/json already rejects them as invalid JSON).
	return NativeCorrelationID(value), nil
}

// HostV2ParseBrowserID is the compatibility shim for the browser domain: it
// accepts only JSON strings and never coerces host/native numbers.
func HostV2ParseBrowserID(raw json.RawMessage) (BrowserRequestID, error) {
	var id BrowserRequestID
	if err := json.Unmarshal(raw, &id); err != nil {
		return "", err
	}
	return id, nil
}

// HostV2ParseHostID is the compatibility shim for the host domain: it accepts
// only JSON numbers and never coerces browser strings such as "001".
func HostV2ParseHostID(raw json.RawMessage) (HostRequestID, error) {
	var id HostRequestID
	if err := json.Unmarshal(raw, &id); err != nil {
		return 0, err
	}
	return id, nil
}

// HostV2ParseNativeID is the compatibility shim for the native domain: it
// accepts only JSON numbers in the full uint64 range, including values above
// the host safe-integer ceiling that must never become host IDs.
func HostV2ParseNativeID(raw json.RawMessage) (NativeCorrelationID, error) {
	var id NativeCorrelationID
	if err := json.Unmarshal(raw, &id); err != nil {
		return 0, err
	}
	return id, nil
}

// HostV2IDMapper holds explicit per-connection transport bindings. There is
// no implicit conversion: callers must Bind the three IDs together and then
// resolve through the map. Reconnect calls InvalidateTransport, which drops
// transport bindings while the caller-retained mutation ledger survives.
type HostV2IDMapper struct {
	mu            sync.Mutex
	browserToHost map[BrowserRequestID]HostRequestID
	hostToBrowser map[HostRequestID]BrowserRequestID
	hostToNative  map[HostRequestID]NativeCorrelationID
	nativeToHost  map[NativeCorrelationID]HostRequestID
}

// NewHostV2IDMapper returns an empty transport map.
func NewHostV2IDMapper() *HostV2IDMapper {
	return &HostV2IDMapper{
		browserToHost: make(map[BrowserRequestID]HostRequestID),
		hostToBrowser: make(map[HostRequestID]BrowserRequestID),
		hostToNative:  make(map[HostRequestID]NativeCorrelationID),
		nativeToHost:  make(map[NativeCorrelationID]HostRequestID),
	}
}

// Bind records one explicit browser/host/native triple.
func (m *HostV2IDMapper) Bind(browser BrowserRequestID, host HostRequestID, native NativeCorrelationID) error {
	if err := browser.Validate(); err != nil {
		return err
	}
	if err := host.Validate(); err != nil {
		return err
	}
	if err := native.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.hostToBrowser[host]; exists {
		return fmt.Errorf("host request id is already bound")
	}
	if _, exists := m.browserToHost[browser]; exists {
		return fmt.Errorf("browser request id is already bound")
	}
	if native.IsSet() {
		if _, exists := m.nativeToHost[native]; exists {
			return fmt.Errorf("native correlation is already bound")
		}
	}
	m.browserToHost[browser] = host
	m.hostToBrowser[host] = browser
	if native.IsSet() {
		m.hostToNative[host] = native
		m.nativeToHost[native] = host
	}
	return nil
}

// ResolveHost returns the host ID bound to one browser ID.
func (m *HostV2IDMapper) ResolveHost(browser BrowserRequestID) (HostRequestID, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.browserToHost[browser]
	return id, ok
}

// ResolveBrowser returns the browser ID bound to one host ID.
func (m *HostV2IDMapper) ResolveBrowser(host HostRequestID) (BrowserRequestID, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.hostToBrowser[host]
	return id, ok
}

// ResolveNative returns the native correlation bound to one host ID.
func (m *HostV2IDMapper) ResolveNative(host HostRequestID) (NativeCorrelationID, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.hostToNative[host]
	return id, ok
}

// ResolveHostByNative returns the host ID bound to one native correlation.
func (m *HostV2IDMapper) ResolveHostByNative(native NativeCorrelationID) (HostRequestID, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.nativeToHost[native]
	return id, ok
}

// InvalidateTransport drops all transport bindings after reconnect. Durable
// mutation identity lives outside this map and is intentionally retained.
func (m *HostV2IDMapper) InvalidateTransport() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.browserToHost = make(map[BrowserRequestID]HostRequestID)
	m.hostToBrowser = make(map[HostRequestID]BrowserRequestID)
	m.hostToNative = make(map[HostRequestID]NativeCorrelationID)
	m.nativeToHost = make(map[NativeCorrelationID]HostRequestID)
}

// Len returns the number of bound host IDs.
func (m *HostV2IDMapper) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.hostToBrowser)
}
