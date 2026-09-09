package piprotocol_test

import (
	"encoding/json"
	"testing"

	piwire "github.com/miloszkolber/pixie/internal/piprotocol"
)

func hostV2TestEpoch() piwire.HostV2Epoch {
	return piwire.HostV2Epoch{HostIdentity: "host-abc", BootID: "boot-1", ChildGeneration: 3}
}

func TestHostV2EpochAndSnapshotOwnership(t *testing.T) {
	epoch := hostV2TestEpoch()
	if err := epoch.Validate(); err != nil {
		t.Fatalf("valid epoch rejected: %v", err)
	}
	snapshot := piwire.HostV2Snapshot{
		SessionID:     "sess-1",
		SessionKey:    "key-1",
		Epoch:         epoch,
		EventSequence: 41,
		Messages:      []json.RawMessage{json.RawMessage(`{"type":"text"}`)},
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
	if snapshot.IsStale(epoch) {
		t.Fatal("snapshot reported stale against its own epoch")
	}
	next := piwire.HostV2Epoch{HostIdentity: "host-abc", BootID: "boot-2", ChildGeneration: 3}
	if !snapshot.IsStale(next) {
		t.Fatal("boot change did not invalidate snapshot checkpoint")
	}
	forked := piwire.HostV2Epoch{HostIdentity: "host-abc", BootID: "boot-1", ChildGeneration: 4}
	if !snapshot.IsStale(forked) {
		t.Fatal("child-generation change did not invalidate snapshot checkpoint")
	}
	if err := piwire.CheckHostV2Sequence(41, 42); err != nil {
		t.Fatalf("advancing sequence rejected: %v", err)
	}
	if err := piwire.CheckHostV2Sequence(42, 42); err == nil {
		t.Fatal("replayed sequence was admitted")
	}
}

func TestHostV2SettlementTransitionsAndFollowUp(t *testing.T) {
	// Legal path: prepared -> dispatching -> accepted -> settled.
	if !piwire.HostV2CanTransition(piwire.HostV2DeliveryPrepared, piwire.HostV2DeliveryDispatching) {
		t.Fatal("prepared->dispatching rejected")
	}
	if !piwire.HostV2CanTransition(piwire.HostV2DeliveryDispatching, piwire.HostV2DeliveryAccepted) {
		t.Fatal("dispatching->accepted rejected")
	}
	if !piwire.HostV2CanTransition(piwire.HostV2DeliveryAccepted, piwire.HostV2DeliverySettled) {
		t.Fatal("accepted->settled rejected")
	}
	// Terminal states never transition.
	for _, terminal := range []piwire.HostV2DeliveryState{
		piwire.HostV2DeliverySettled, piwire.HostV2DeliveryRejected,
		piwire.HostV2DeliveryUncertain, piwire.HostV2DeliveryInterrupted,
	} {
		if !terminal.IsTerminal() {
			t.Fatalf("terminal state %q not terminal", string(terminal))
		}
		if piwire.HostV2CanTransition(terminal, piwire.HostV2DeliverySettled) {
			t.Fatalf("terminal state %q transitioned", string(terminal))
		}
	}
	// Accepted is not settlement: it must not allow follow-up dispatch.
	accepted := piwire.HostV2Settlement{MutationID: "m-1", DeliveryID: "d-1", Fingerprint: "fp", State: piwire.HostV2DeliveryAccepted}
	if accepted.AllowsFollowUp() {
		t.Fatal("accepted delivery allowed follow-up before settlement")
	}
	uncertain := piwire.HostV2Settlement{MutationID: "m-1", DeliveryID: "d-1", Fingerprint: "fp", State: piwire.HostV2DeliveryUncertain}
	if uncertain.AllowsFollowUp() {
		t.Fatal("uncertain delivery allowed follow-up")
	}
	settled := piwire.HostV2Settlement{MutationID: "m-1", DeliveryID: "d-1", Fingerprint: "fp", State: piwire.HostV2DeliverySettled}
	if !settled.AllowsFollowUp() {
		t.Fatal("settled delivery blocked follow-up")
	}
	if err := settled.Validate(); err != nil {
		t.Fatalf("valid settlement rejected: %v", err)
	}
}

func TestHostV2DurabilityOutcomes(t *testing.T) {
	if !piwire.HostV2Installed.MayDispatch() || piwire.HostV2KnownUncommitted.MayDispatch() || piwire.HostV2DurabilityUncertain.MayDispatch() {
		t.Fatal("durability dispatch gate mismatch")
	}
	if !piwire.HostV2DurabilityUncertain.MustReconcile() || piwire.HostV2Installed.MustReconcile() {
		t.Fatal("durability reconcile gate mismatch")
	}
	if detail := piwire.HostV2DurabilityError(piwire.HostV2Installed, ""); detail != nil {
		t.Fatalf("installed outcome produced an error: %#v", detail)
	}
	uncertain := piwire.HostV2DurabilityError(piwire.HostV2DurabilityUncertain, "")
	if uncertain == nil || uncertain.Code != piwire.HostV2CodePersistenceUncertain {
		t.Fatalf("uncertain outcome mistyped: %#v", uncertain)
	}
	if err := piwire.HostV2Durability("bogus").Validate(); err == nil {
		t.Fatal("unknown durability admitted")
	}
}
