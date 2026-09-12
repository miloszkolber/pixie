// Host v2 envelope schemas for API-02 (implementation-summary transport).
//
// ProtocolVersion 2 requires an explicit v2 hello before other calls and
// keeps one host contract per connection. Browser protocol 88 is a separate
// reviewed baseline, never an alias for either host version.
//
// Wire rules encoded here:
//   - requests are objects with a positive safe-integer id, nonempty method
//     and object params; the host never coerces IDs or params.
//   - replies are {id,result} or {id,error:{code,message,reason?}}.
//   - events carry method/params without an id.
//   - malformed JSON closes 1007; invalid envelope/handshake or a duplicate
//     in-flight host ID closes 1008 (v1 answered duplicates with an error
//     frame and kept the connection); oversized input closes 1009; exceeded
//     output budget closes 1013.
//   - well-formed unknown methods receive method-not-found, never a close.
//   - resource/revision conflicts, unavailable capabilities, delivery
//     uncertainty and persistence uncertainty have explicit typed errors.
//
// Serialized admission reuses FrameMaxBytes (32 MiB serialized UTF-8) from
// limits.go; decoded structures are bounded separately below.
package piprotocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// HostV2ProtocolVersion is the rewritten host contract version.
const HostV2ProtocolVersion = 2

// Host v2 transport close codes from roadmap/README.md.
type HostV2CloseCode int

const (
	HostV2CloseInvalidJSON     HostV2CloseCode = 1007
	HostV2CloseInvalidEnvelope HostV2CloseCode = 1008
	HostV2CloseMessageTooBig   HostV2CloseCode = 1009
	HostV2CloseOutputBudget    HostV2CloseCode = 1013
)

// HostV2CloseError reports a transport close. Invalid envelope, handshake or
// duplicate host IDs close with 1008; malformed JSON closes with 1007.
type HostV2CloseError struct {
	Code   HostV2CloseCode
	Reason string
}

func (e *HostV2CloseError) Error() string {
	return fmt.Sprintf("host v2 close %d: %s", int(e.Code), e.Reason)
}

// HostV2CloseCodeFor extracts the close code from a parse failure.
func HostV2CloseCodeFor(err error) (HostV2CloseCode, bool) {
	if closeErr, ok := err.(*HostV2CloseError); ok {
		return closeErr.Code, true
	}
	return 0, false
}

// Ordinary in-flight host bounds from roadmap/README.md. Exceeding them answers
// with a typed error frame; only duplicate in-flight IDs close the
// connection in v2.
const (
	HostV2OrdinaryInflightPerConnection = 128
	HostV2OrdinaryInflightPerEngine     = 256
	HostV2AggregateMaxBytes             = 64 * 1024 * 1024
)

// HostV2ErrorCode is the numeric wire code for a typed host v2 error frame.
type HostV2ErrorCode int

const (
	// HostV2CodeInternal is the generic fallback (v1 -32000).
	HostV2CodeInternal HostV2ErrorCode = -32000
	// HostV2CodeUnknownSession preserves the v1 unknown/ambiguous session
	// code for compatibility shims.
	HostV2CodeUnknownSession HostV2ErrorCode = -32002
	// HostV2CodeMethodNotFound answers well-formed unknown methods.
	HostV2CodeMethodNotFound HostV2ErrorCode = -32601
	// HostV2CodeResourceConflict reports revision/generation conflicts and
	// mutation fingerprint mismatches.
	HostV2CodeResourceConflict HostV2ErrorCode = -32003
	// HostV2CodeCapabilityUnavailable reports an unavailable capability or
	// ungated operation.
	HostV2CodeCapabilityUnavailable HostV2ErrorCode = -32004
	// HostV2CodeDeliveryUncertain reports lost transport acknowledgement:
	// the operation may have been accepted and must be reconciled by
	// mutation identity, never resent as fresh work.
	HostV2CodeDeliveryUncertain HostV2ErrorCode = -32005
	// HostV2CodePersistenceUncertain reports an installed-but-unconfirmed
	// publication that must be reconciled before dependent mutations.
	HostV2CodePersistenceUncertain HostV2ErrorCode = -32006
)

// Stable reason strings for typed host v2 errors.
const (
	HostV2ReasonInternal              = "internal"
	HostV2ReasonUnknownSession        = "unknown_session"
	HostV2ReasonMethodNotFound        = "method_not_found"
	HostV2ReasonResourceConflict      = "resource_conflict"
	HostV2ReasonCapabilityUnavailable = "capability_unavailable"
	HostV2ReasonDeliveryUncertain     = "delivery_uncertain"
	HostV2ReasonPersistenceUncertain  = "persistence_uncertain"
)

// HostV2ErrorDetail is a typed host v2 error frame payload. Reason extends
// the v1 {code,message} shape without changing its required fields.
type HostV2ErrorDetail struct {
	Code    HostV2ErrorCode `json:"code"`
	Message string          `json:"message"`
	Reason  string          `json:"reason,omitempty"`
}

func (e *HostV2ErrorDetail) Error() string { return e.Message }

// NewHostV2Error builds a typed error with its stable reason.
func NewHostV2Error(code HostV2ErrorCode, reason, message string) *HostV2ErrorDetail {
	return &HostV2ErrorDetail{Code: code, Reason: reason, Message: message}
}

// NewHostV2MethodNotFound answers well-formed unknown methods.
func NewHostV2MethodNotFound(method string) *HostV2ErrorDetail {
	return NewHostV2Error(HostV2CodeMethodNotFound, HostV2ReasonMethodNotFound, fmt.Sprintf("unknown host method %q", method))
}

// NewHostV2ResourceConflict reports revision/generation or fingerprint
// conflicts, including different content under one mutationId.
func NewHostV2ResourceConflict(message string) *HostV2ErrorDetail {
	return NewHostV2Error(HostV2CodeResourceConflict, HostV2ReasonResourceConflict, message)
}

// NewHostV2CapabilityUnavailable reports an unavailable capability.
func NewHostV2CapabilityUnavailable(message string) *HostV2ErrorDetail {
	return NewHostV2Error(HostV2CodeCapabilityUnavailable, HostV2ReasonCapabilityUnavailable, message)
}

// NewHostV2DeliveryUncertain reports uncertain delivery after lost
// acknowledgement. Callers reconcile by mutation identity.
func NewHostV2DeliveryUncertain(message string) *HostV2ErrorDetail {
	return NewHostV2Error(HostV2CodeDeliveryUncertain, HostV2ReasonDeliveryUncertain, message)
}

// NewHostV2PersistenceUncertain reports an installed-but-unconfirmed outcome.
func NewHostV2PersistenceUncertain(message string) *HostV2ErrorDetail {
	return NewHostV2Error(HostV2CodePersistenceUncertain, HostV2ReasonPersistenceUncertain, message)
}

// HostV2V1DuplicateErrorFrame is the v1 compatibility shim: v1 answered a
// reused in-flight ID with an error frame and kept the connection open. Host
// v2 instead closes 1008; use HostV2InflightSet to enforce the v2 rule.
func HostV2V1DuplicateErrorFrame(id HostRequestID) HostV2ErrorFrame {
	return HostV2ErrorFrame{
		ID:    id,
		Error: *NewHostV2Error(HostV2CodeInternal, HostV2ReasonInternal, "request id is already in flight"),
	}
}

// HostV2Request is one host v2 request envelope.
type HostV2Request struct {
	ID     HostRequestID   `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// HostV2ErrorFrame is one host v2 error reply.
type HostV2ErrorFrame struct {
	ID    HostRequestID     `json:"id"`
	Error HostV2ErrorDetail `json:"error"`
}

// HostV2ResultFrame is one host v2 success reply.
type HostV2ResultFrame struct {
	ID     HostRequestID   `json:"id"`
	Result json.RawMessage `json:"result"`
}

// HostV2EventFrame is one host-to-controller event without a transport ID.
type HostV2EventFrame struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// HostV2HelloParams requires protocolVersion 2.
type HostV2HelloParams struct {
	ProtocolVersion int `json:"protocolVersion"`
}

// HostV2HelloResult reports release/source, host identity, boot identity,
// native version/executable and capability versions without credentials.
// SupportedProtocolVersions carries the negotiation advertisement and
// OperationSet preserves the exhaustive fail-closed operation map the v1 hello
// already exposed.
type HostV2HelloResult struct {
	ProtocolVersion           int             `json:"protocolVersion"`
	SupportedProtocolVersions []int           `json:"supportedProtocolVersions,omitempty"`
	HostIdentity              string          `json:"hostIdentity"`
	BootID                    string          `json:"bootId"`
	SourceCommit              string          `json:"sourceCommit,omitempty"`
	ReleaseID                 string          `json:"releaseId,omitempty"`
	NativeVersion             string          `json:"nativeVersion,omitempty"`
	NativeExecutable          string          `json:"nativeExecutable,omitempty"`
	Capabilities              map[string]int  `json:"capabilities,omitempty"`
	OperationSet              map[string]bool `json:"operationSet,omitempty"`
}

// ValidateHostV2Method checks the nonempty method rule. Length is bounded by
// the 32 MiB serialized frame, counted in UTF-8 bytes.
func ValidateHostV2Method(method string) error {
	if len(method) == 0 || len(method) > FrameMaxBytes {
		return fmt.Errorf("host method out of bounds")
	}
	if !utf8.ValidString(method) || strings.ContainsRune(method, 0) {
		return fmt.Errorf("host method is invalid")
	}
	return nil
}

func hostV2IsJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return false
	}
	// json.Unmarshal("null") into a map succeeds with nil; reject it via the
	// leading-brace check above plus an explicit null rejection.
	if string(trimmed) == "null" {
		return false
	}
	return true
}

// ParseHostV2RequestFrame validates one serialized host v2 request frame.
// Size, UTF-8, JSON shape, ID, method and params violations return a close
// error (1007/1008/1009); well-formed unknown methods are not rejected here
// and instead receive a method-not-found error frame from dispatch.
func ParseHostV2RequestFrame(payload []byte) (HostV2Request, error) {
	var zero HostV2Request
	if len(payload) > FrameMaxBytes {
		return zero, &HostV2CloseError{Code: HostV2CloseMessageTooBig, Reason: "frame exceeds the 32 MiB limit"}
	}
	if !utf8.Valid(payload) {
		return zero, &HostV2CloseError{Code: HostV2CloseInvalidJSON, Reason: "frame is not valid UTF-8"}
	}
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		// Valid JSON arrays/scalars are still invalid host envelopes.
		var probe any
		if json.Unmarshal(payload, &probe) != nil {
			return zero, &HostV2CloseError{Code: HostV2CloseInvalidJSON, Reason: "invalid JSON"}
		}
		return zero, &HostV2CloseError{Code: HostV2CloseInvalidEnvelope, Reason: "request envelope must be an object"}
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return zero, &HostV2CloseError{Code: HostV2CloseInvalidJSON, Reason: "invalid JSON"}
	}
	rawID, hasID := envelope["id"]
	rawMethod, hasMethod := envelope["method"]
	rawParams, hasParams := envelope["params"]
	if !hasID || !hasMethod || !hasParams {
		return zero, &HostV2CloseError{Code: HostV2CloseInvalidEnvelope, Reason: "request envelope must carry id, method and params"}
	}
	var id HostRequestID
	if err := json.Unmarshal(rawID, &id); err != nil {
		return zero, &HostV2CloseError{Code: HostV2CloseInvalidEnvelope, Reason: "invalid request ID"}
	}
	var method string
	if err := json.Unmarshal(rawMethod, &method); err != nil || ValidateHostV2Method(method) != nil {
		return zero, &HostV2CloseError{Code: HostV2CloseInvalidEnvelope, Reason: "invalid request method"}
	}
	if !hostV2IsJSONObject(rawParams) {
		return zero, &HostV2CloseError{Code: HostV2CloseInvalidEnvelope, Reason: "request params must be an object"}
	}
	return HostV2Request{ID: id, Method: method, Params: rawParams}, nil
}

// ValidateHostV2HelloRequest enforces the v2 handshake: the first call must
// be runtime.hello with protocolVersion 2.
func ValidateHostV2HelloRequest(req HostV2Request) error {
	if req.Method != "runtime.hello" {
		return &HostV2CloseError{Code: HostV2CloseInvalidEnvelope, Reason: "missing runtime hello"}
	}
	var params HostV2HelloParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &HostV2CloseError{Code: HostV2CloseInvalidEnvelope, Reason: "invalid hello params"}
	}
	if params.ProtocolVersion != HostV2ProtocolVersion {
		return &HostV2CloseError{Code: HostV2CloseInvalidEnvelope, Reason: "unsupported host protocol version"}
	}
	return nil
}

// ValidateHostV2HelloResult checks the hello reply shape and proves it
// carries no credentials.
func ValidateHostV2HelloResult(result HostV2HelloResult) error {
	if result.ProtocolVersion != HostV2ProtocolVersion {
		return fmt.Errorf("hello result must negotiate protocolVersion 2")
	}
	if result.HostIdentity == "" || strings.ContainsRune(result.HostIdentity, 0) || !utf8.ValidString(result.HostIdentity) {
		return fmt.Errorf("hello result misses host identity")
	}
	if result.BootID == "" || strings.ContainsRune(result.BootID, 0) || !utf8.ValidString(result.BootID) {
		return fmt.Errorf("hello result misses boot identity")
	}
	return nil
}

// HostV2HelloResultHasNoCredentials proves the hello reply carries no secret
// material on the wire.
func HostV2HelloResultHasNoCredentials(raw json.RawMessage) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return err
	}
	lowered := strings.ToLower(string(raw))
	for _, forbidden := range []string{`"secret"`, `"authorization"`, `"bearer"`, `"token"`} {
		// Capability names never use these keys; their presence means
		// credential leakage into the hello reply.
		if strings.Contains(lowered, forbidden) {
			return fmt.Errorf("hello result must not carry credentials")
		}
	}
	return nil
}

// ParseHostV2EventFrame validates one host-to-controller event frame: object
// envelope with method/params and no transport ID.
func ParseHostV2EventFrame(payload []byte) (HostV2EventFrame, error) {
	var zero HostV2EventFrame
	if len(payload) > FrameMaxBytes {
		return zero, &HostV2CloseError{Code: HostV2CloseMessageTooBig, Reason: "frame exceeds the 32 MiB limit"}
	}
	if !utf8.Valid(payload) {
		return zero, &HostV2CloseError{Code: HostV2CloseInvalidJSON, Reason: "frame is not valid UTF-8"}
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return zero, &HostV2CloseError{Code: HostV2CloseInvalidJSON, Reason: "invalid JSON"}
	}
	if _, hasID := envelope["id"]; hasID {
		return zero, &HostV2CloseError{Code: HostV2CloseInvalidEnvelope, Reason: "event must not carry an id"}
	}
	rawMethod, hasMethod := envelope["method"]
	rawParams, hasParams := envelope["params"]
	if !hasMethod || !hasParams {
		return zero, &HostV2CloseError{Code: HostV2CloseInvalidEnvelope, Reason: "event must carry method and params"}
	}
	var method string
	if err := json.Unmarshal(rawMethod, &method); err != nil || ValidateHostV2Method(method) != nil {
		return zero, &HostV2CloseError{Code: HostV2CloseInvalidEnvelope, Reason: "invalid event method"}
	}
	if !hostV2IsJSONObject(rawParams) {
		return zero, &HostV2CloseError{Code: HostV2CloseInvalidEnvelope, Reason: "event params must be an object"}
	}
	return HostV2EventFrame{Method: method, Params: rawParams}, nil
}

// HostV2InflightSet tracks in-flight host IDs for one connection. In host v2
// a duplicate in-flight ID closes 1008; v1 answered an error frame instead
// (see HostV2V1DuplicateErrorFrame). Exceeding the ordinary cap answers with
// a typed error frame and keeps the connection open.
type HostV2InflightSet struct {
	ids map[HostRequestID]struct{}
}

// NewHostV2InflightSet returns an empty v2 duplicate tracker.
func NewHostV2InflightSet() *HostV2InflightSet {
	return &HostV2InflightSet{ids: make(map[HostRequestID]struct{})}
}

// Add admits one host ID or reports the v2 duplicate close / overflow frame.
func (s *HostV2InflightSet) Add(id HostRequestID) (frameErr *HostV2ErrorDetail, closeErr *HostV2CloseError) {
	if err := id.Validate(); err != nil {
		return nil, &HostV2CloseError{Code: HostV2CloseInvalidEnvelope, Reason: "invalid request ID"}
	}
	if s.ids == nil {
		s.ids = make(map[HostRequestID]struct{})
	}
	if _, exists := s.ids[id]; exists {
		return nil, &HostV2CloseError{Code: HostV2CloseInvalidEnvelope, Reason: "duplicate in-flight host id"}
	}
	if len(s.ids) >= HostV2OrdinaryInflightPerConnection {
		return NewHostV2Error(HostV2CodeInternal, HostV2ReasonInternal, "too many pending requests"), nil
	}
	s.ids[id] = struct{}{}
	return nil, nil
}

// Remove releases one host ID. Unknown IDs are ignored.
func (s *HostV2InflightSet) Remove(id HostRequestID) {
	delete(s.ids, id)
}

// Len returns the number of in-flight host IDs.
func (s *HostV2InflightSet) Len() int { return len(s.ids) }
