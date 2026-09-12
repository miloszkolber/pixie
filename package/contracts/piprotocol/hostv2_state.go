// Host v2 epochs, snapshot/state transfer, settlement and durability for
// API-02 (contracts execution/persistence).
//
//   - Epochs separate stable host identity, fresh boot identity and fresh
//     managed child generation. Event checkpoints carry the correct epoch;
//     a new port, bootId or rotated credential is not a new native session.
//   - Snapshot/checkpoint and subsequent buffered events share one owner.
//     Several native queries are not an atomic snapshot: callers reconcile
//     leaf/history against intervening events before ready.
//   - Settlement follows native agent_settled and tested command-specific
//     behavior, not the first agent_end or a quiet timer. Unknown settlement
//     blocks automatic follow-up dispatch.
//   - Delivery transitions prepared -> dispatching -> accepted -> settled, or
//     explicit rejected/uncertain/interrupted outcomes. A retry uses a new
//     transport ID plus the original mutationId and fingerprint; different
//     content under one mutationId conflicts. Lost transport responses never
//     authorize an unrecorded fresh operation.
//   - Persistence distinguishes known-uncommitted, installed/committed and
//     durability/outcome-uncertain without overwriting a visible primary with
//     an old backup.
package piprotocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// HostV2Epoch identifies the owner of one snapshot/event stream.
type HostV2Epoch struct {
	HostIdentity    string `json:"hostIdentity"`
	BootID          string `json:"bootId"`
	ChildGeneration uint64 `json:"childGeneration"`
}

// Validate checks epoch identity fields. ChildGeneration may be zero only for
// fixtures that have not started a managed child yet.
func (e HostV2Epoch) Validate() error {
	if e.HostIdentity == "" || len(e.HostIdentity) > 256 {
		return fmt.Errorf("epoch misses host identity")
	}
	if !utf8.ValidString(e.HostIdentity) || strings.ContainsRune(e.HostIdentity, 0) {
		return fmt.Errorf("epoch host identity is invalid")
	}
	if e.BootID == "" || len(e.BootID) > 256 {
		return fmt.Errorf("epoch misses boot identity")
	}
	if !utf8.ValidString(e.BootID) || strings.ContainsRune(e.BootID, 0) {
		return fmt.Errorf("epoch boot identity is invalid")
	}
	return nil
}

// Equal reports whether two epochs share one owner.
func (e HostV2Epoch) Equal(other HostV2Epoch) bool {
	return e.HostIdentity == other.HostIdentity && e.BootID == other.BootID && e.ChildGeneration == other.ChildGeneration
}

// HostV2SequencedEvent is one projected event with its checkpoint owner and
// monotonic sequence. Final native messages are authoritative; partial blocks
// use native content indexes/tool IDs and never prove execution alone.
type HostV2SequencedEvent struct {
	SessionKey string          `json:"sessionKey"`
	Epoch      HostV2Epoch     `json:"epoch"`
	Sequence   uint64          `json:"sequence"`
	Method     string          `json:"method"`
	Params     json.RawMessage `json:"params"`
}

// Validate checks owner, sequence presence and envelope shape.
func (e HostV2SequencedEvent) Validate() error {
	if e.SessionKey == "" || strings.ContainsRune(e.SessionKey, 0) || !utf8.ValidString(e.SessionKey) {
		return fmt.Errorf("event misses session key")
	}
	if err := e.Epoch.Validate(); err != nil {
		return err
	}
	if err := ValidateHostV2Method(e.Method); err != nil {
		return err
	}
	if len(e.Params) > 0 && !hostV2IsJSONObject(e.Params) {
		return fmt.Errorf("event params must be an object")
	}
	return nil
}

// CheckHostV2Sequence proves monotonic checkpoints: each buffered event after
// a snapshot must advance the sequence.
func CheckHostV2Sequence(previous, next uint64) error {
	if next <= previous {
		return fmt.Errorf("event sequence must advance")
	}
	return nil
}

// HostV2Snapshot is one reconciled session bootstrap. Messages,
// PendingDialogs and Commands are opaque native payloads preserved without
// renaming native IDs; Pixie validates only the envelope/epoch/sequence here.
type HostV2Snapshot struct {
	SessionID      string            `json:"sessionId"`
	SessionKey     string            `json:"sessionKey"`
	Epoch          HostV2Epoch       `json:"epoch"`
	EventSequence  uint64            `json:"eventSequence"`
	Messages       []json.RawMessage `json:"messages,omitempty"`
	PendingDialogs []json.RawMessage `json:"pendingDialogs,omitempty"`
	Commands       []json.RawMessage `json:"commands,omitempty"`
	RunID          string            `json:"runId,omitempty"`
}

// Validate checks snapshot ownership and sequence binding.
func (s HostV2Snapshot) Validate() error {
	if s.SessionID == "" || strings.ContainsRune(s.SessionID, 0) || !utf8.ValidString(s.SessionID) {
		return fmt.Errorf("snapshot misses session id")
	}
	if s.SessionKey == "" || strings.ContainsRune(s.SessionKey, 0) || !utf8.ValidString(s.SessionKey) {
		return fmt.Errorf("snapshot misses session key")
	}
	if err := s.Epoch.Validate(); err != nil {
		return err
	}
	for _, group := range [][]json.RawMessage{s.Messages, s.PendingDialogs, s.Commands} {
		for _, raw := range group {
			if len(raw) > FrameMaxBytes {
				return fmt.Errorf("snapshot entry exceeds the 32 MiB frame limit")
			}
		}
	}
	return nil
}

// IsStale reports whether intervening events belong to another owner.
// A changed boot/child generation invalidates transport checkpoints, never
// the durable mutation ledger.
func (s HostV2Snapshot) IsStale(current HostV2Epoch) bool {
	return !s.Epoch.Equal(current)
}

// HostV2DeliveryState is one durable delivery outcome.
type HostV2DeliveryState string

const (
	HostV2DeliveryPrepared    HostV2DeliveryState = "prepared"
	HostV2DeliveryDispatching HostV2DeliveryState = "dispatching"
	HostV2DeliveryAccepted    HostV2DeliveryState = "accepted"
	HostV2DeliverySettled     HostV2DeliveryState = "settled"
	HostV2DeliveryRejected    HostV2DeliveryState = "rejected"
	HostV2DeliveryUncertain   HostV2DeliveryState = "uncertain"
	HostV2DeliveryInterrupted HostV2DeliveryState = "interrupted"
)

// IsTerminal reports whether no further automatic transition applies.
// Accepted means queued/handled, not a complete turn.
func (s HostV2DeliveryState) IsTerminal() bool {
	switch s {
	case HostV2DeliverySettled, HostV2DeliveryRejected, HostV2DeliveryUncertain, HostV2DeliveryInterrupted:
		return true
	default:
		return false
	}
}

// Validate checks the state is a known delivery outcome.
func (s HostV2DeliveryState) Validate() error {
	switch s {
	case HostV2DeliveryPrepared, HostV2DeliveryDispatching, HostV2DeliveryAccepted,
		HostV2DeliverySettled, HostV2DeliveryRejected, HostV2DeliveryUncertain, HostV2DeliveryInterrupted:
		return nil
	default:
		return fmt.Errorf("unknown delivery state %q", string(s))
	}
}

// HostV2CanTransition reports the legal delivery edges. Unknown settlement
// never advances to settled without explicit native evidence.
func HostV2CanTransition(from, to HostV2DeliveryState) bool {
	switch from {
	case HostV2DeliveryPrepared:
		return to == HostV2DeliveryDispatching || to == HostV2DeliveryRejected
	case HostV2DeliveryDispatching:
		return to == HostV2DeliveryAccepted || to == HostV2DeliveryUncertain || to == HostV2DeliveryInterrupted
	case HostV2DeliveryAccepted:
		return to == HostV2DeliverySettled || to == HostV2DeliveryUncertain || to == HostV2DeliveryInterrupted || to == HostV2DeliveryRejected
	default:
		return false
	}
}

// HostV2Settlement binds durable mutation identity to one delivery outcome.
type HostV2Settlement struct {
	MutationID  string              `json:"mutationId"`
	DeliveryID  string              `json:"deliveryId"`
	Fingerprint string              `json:"fingerprint"`
	State       HostV2DeliveryState `json:"state"`
}

// Validate checks durable identity and state.
func (s HostV2Settlement) Validate() error {
	if s.MutationID == "" || strings.ContainsRune(s.MutationID, 0) || !utf8.ValidString(s.MutationID) {
		return fmt.Errorf("settlement misses mutation id")
	}
	if s.DeliveryID == "" || strings.ContainsRune(s.DeliveryID, 0) || !utf8.ValidString(s.DeliveryID) {
		return fmt.Errorf("settlement misses delivery id")
	}
	if s.Fingerprint == "" || strings.ContainsRune(s.Fingerprint, 0) || !utf8.ValidString(s.Fingerprint) {
		return fmt.Errorf("settlement misses payload fingerprint")
	}
	return s.State.Validate()
}

// AllowsFollowUp reports whether automatic follow-up dispatch may proceed.
// Only settled work allows it; uncertain/interrupted/accepted work blocks
// until explicit user resolution or native settlement evidence.
func (s HostV2Settlement) AllowsFollowUp() bool {
	return s.State == HostV2DeliverySettled
}

// HostV2Mutation is the stable retry identity: one mutationId plus the
// payload fingerprint. Retries reuse both with a fresh transport ID.
type HostV2Mutation struct {
	MutationID  string `json:"mutationId"`
	Fingerprint string `json:"fingerprint"`
}

// Validate checks retry identity fields.
func (m HostV2Mutation) Validate() error {
	if m.MutationID == "" || strings.ContainsRune(m.MutationID, 0) || !utf8.ValidString(m.MutationID) {
		return fmt.Errorf("mutation misses id")
	}
	if m.Fingerprint == "" || strings.ContainsRune(m.Fingerprint, 0) || !utf8.ValidString(m.Fingerprint) {
		return fmt.Errorf("mutation misses payload fingerprint")
	}
	return nil
}

// CheckHostV2MutationConflict enforces idempotent retry: identical identity
// reconciles the original operation, while different content under one
// mutationId is a typed resource conflict.
func CheckHostV2MutationConflict(existing, incoming HostV2Mutation) error {
	if err := existing.Validate(); err != nil {
		return err
	}
	if err := incoming.Validate(); err != nil {
		return err
	}
	if existing.MutationID != incoming.MutationID {
		return fmt.Errorf("mutation identity mismatch")
	}
	if existing.Fingerprint != incoming.Fingerprint {
		return NewHostV2ResourceConflict("different content under the same mutation id")
	}
	return nil
}

// HostV2Durability distinguishes known pre-publication failure from an
// installed commit and a durability/outcome-uncertain result. It mirrors the
// persist publication contract for host-scoped reconciliation without
// importing controller state.
type HostV2Durability string

const (
	HostV2KnownUncommitted    HostV2Durability = "known-uncommitted"
	HostV2Installed           HostV2Durability = "installed"
	HostV2DurabilityUncertain HostV2Durability = "durability-uncertain"
)

// Validate checks the durability outcome is known.
func (d HostV2Durability) Validate() error {
	switch d {
	case HostV2KnownUncommitted, HostV2Installed, HostV2DurabilityUncertain:
		return nil
	default:
		return fmt.Errorf("unknown durability outcome %q", string(d))
	}
}

// MayDispatch reports whether dependent mutations or unsafe effects may
// proceed. Only installed outcomes allow them.
func (d HostV2Durability) MayDispatch() bool { return d == HostV2Installed }

// MustReconcile reports whether the validated primary must be reconciled
// before accepting dependent mutations.
func (d HostV2Durability) MustReconcile() bool { return d == HostV2DurabilityUncertain }

// HostV2DurabilityError maps a durability outcome to its typed wire error, or
// nil when no error frame applies (installed success).
func HostV2DurabilityError(outcome HostV2Durability, message string) *HostV2ErrorDetail {
	switch outcome {
	case HostV2Installed:
		return nil
	case HostV2DurabilityUncertain:
		if message == "" {
			message = "publication is installed but unconfirmed"
		}
		return NewHostV2PersistenceUncertain(message)
	case HostV2KnownUncommitted:
		if message == "" {
			message = "publication did not commit"
		}
		return NewHostV2Error(HostV2CodeInternal, HostV2ReasonInternal, message)
	default:
		return NewHostV2Error(HostV2CodeInternal, HostV2ReasonInternal, "unknown durability outcome")
	}
}
