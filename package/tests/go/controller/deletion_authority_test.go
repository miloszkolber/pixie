package controller_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

// fixtureDeletionAgentBinding reproduces the stable v2 agent binding written by
// the production client for the fixture identity. The harness dials an explicit
// endpoint, so requirePi/requireSecret are false.
func fixtureDeletionAgentBinding(t *testing.T) string {
	t.Helper()
	encoded, err := json.Marshal([]any{
		"pixie/session-deletion-agent-binding/v2",
		"pi:fixture-runtime",
		false,
		false,
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest)
}

type deletionMethodRecorder struct {
	mu      sync.Mutex
	methods []string
}

func (r *deletionMethodRecorder) observe(method string, _ map[string]any) {
	r.mu.Lock()
	r.methods = append(r.methods, method)
	r.mu.Unlock()
}

func (r *deletionMethodRecorder) saw(method string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, candidate := range r.methods {
		if candidate == method {
			return true
		}
	}
	return false
}

func TestDeletionAuthorityPairedAuthorizesRequestedRecord(t *testing.T) {
	manager, _, project, store := newSessionManager(t, nil, nil)
	storageKey, err := persist.DerivePairingStorageKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.PairAuthority(store, "pi:fixture-runtime", storageKey, pairingSecret('a')); err != nil {
		t.Fatal(err)
	}
	manager.SetDeletionAuthority(controller.DeletionAuthorityPaired, storageKey)
	if err := controller.NewSessionDeletions(store).Request(project.ID, "chat", fixtureDeletionAgentBinding(t)); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecoverDeletions(context.Background()); err != nil {
		t.Fatalf("paired recovery blocked startup: %v", err)
	}
	if status := manager.DeletionRecoveryStatus(); len(status) != 0 {
		t.Fatalf("paired recovery quarantined a valid pairing: %#v", status)
	}
	pending, err := controller.NewSessionDeletions(store).List()
	if err != nil || len(pending) != 0 {
		t.Fatalf("paired recovery did not finish the authorized record: %#v %v", pending, err)
	}
}

func TestDeletionAuthorityPairedWithoutPairingQuarantinesAndNeverDispatches(t *testing.T) {
	recorder := &deletionMethodRecorder{}
	manager, _, project, store := newSessionManagerWithInitializeAndPublisher(t, nil, nil, piInitializeResponse(), nil, recorder.observe)
	manager.SetDeletionAuthority(controller.DeletionAuthorityPaired, "agent-dir-unpaired")
	if err := controller.NewSessionDeletions(store).Request(project.ID, "chat", fixtureDeletionAgentBinding(t)); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecoverDeletions(context.Background()); err != nil {
		t.Fatalf("unpaired paired-mode recovery blocked startup: %v", err)
	}
	status := manager.DeletionRecoveryStatus()
	if len(status) != 1 || status[0].SessionID != "chat" || !strings.Contains(status[0].Reason, "recovery-blocked") {
		t.Fatalf("unpaired paired-mode recovery was not quarantined actionably: %#v", status)
	}
	if recorder.saw("session.delete") {
		t.Fatal("unpaired paired-mode recovery dispatched a delete")
	}
	pending, err := controller.NewSessionDeletions(store).List()
	if err != nil || len(pending) != 1 || pending[0].Phase != "requested" {
		t.Fatalf("unpaired paired-mode recovery did not keep the tombstone: %#v %v", pending, err)
	}
}

func TestDeletionAuthorityAutoWithoutPairingKeepsLegacyMatch(t *testing.T) {
	manager, _, project, store := newSessionManager(t, nil, nil)
	manager.SetDeletionAuthority(controller.DeletionAuthorityAuto, "")
	if err := controller.NewSessionDeletions(store).Request(project.ID, "chat", fixtureDeletionAgentBinding(t)); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecoverDeletions(context.Background()); err != nil {
		t.Fatalf("auto legacy recovery blocked startup: %v", err)
	}
	if status := manager.DeletionRecoveryStatus(); len(status) != 0 {
		t.Fatalf("auto legacy recovery quarantined a matching binding: %#v", status)
	}
	pending, err := controller.NewSessionDeletions(store).List()
	if err != nil || len(pending) != 0 {
		t.Fatalf("auto legacy recovery did not finish the matching record: %#v %v", pending, err)
	}
}

func TestDeletionAuthorityAutoRequiresPairingWhenRecordExists(t *testing.T) {
	manager, _, project, store := newSessionManager(t, nil, nil)
	if _, err := controller.PairAuthority(store, "pi:some-other-host", "agent-dir-other", pairingSecret('o')); err != nil {
		t.Fatal(err)
	}
	manager.SetDeletionAuthority(controller.DeletionAuthorityAuto, "agent-dir-other")
	// The record's binding matches the live fixture identity, so a legacy
	// fallback would replay it. Auto must instead honor the existing pairing
	// record and quarantine the host mismatch.
	if err := controller.NewSessionDeletions(store).Request(project.ID, "chat", fixtureDeletionAgentBinding(t)); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecoverDeletions(context.Background()); err != nil {
		t.Fatalf("auto paired recovery blocked startup: %v", err)
	}
	status := manager.DeletionRecoveryStatus()
	if len(status) != 1 || !strings.Contains(status[0].Reason, "host identity mismatch") {
		t.Fatalf("auto mode did not require the existing pairing: %#v", status)
	}
	pending, err := controller.NewSessionDeletions(store).List()
	if err != nil || len(pending) != 1 {
		t.Fatalf("auto paired recovery did not keep the tombstone: %#v %v", pending, err)
	}
}

func TestParseDeletionAuthorityModeCaseInsensitive(t *testing.T) {
	for value, want := range map[string]controller.DeletionAuthorityMode{
		"":         controller.DeletionAuthorityAuto,
		"auto":     controller.DeletionAuthorityAuto,
		"AUTO":     controller.DeletionAuthorityAuto,
		"Paired":   controller.DeletionAuthorityPaired,
		" paired ": controller.DeletionAuthorityPaired,
		"LEGACY":   controller.DeletionAuthorityLegacy,
	} {
		got, err := controller.ParseDeletionAuthorityMode(value)
		if err != nil || got != want {
			t.Fatalf("ParseDeletionAuthorityMode(%q) = %q, %v; want %q", value, got, err, want)
		}
	}
	if _, err := controller.ParseDeletionAuthorityMode("bogus"); err == nil || !strings.Contains(err.Error(), "PIXIE_DELETION_AUTHORITY") {
		t.Fatalf("invalid authority mode was accepted: %v", err)
	}
}

func TestInvalidDeletionAuthorityFailsStartup(t *testing.T) {
	_, err := controller.NewRuntime(controller.RuntimeConfig{
		Getenv: func(key string) string {
			if key == "PIXIE_DELETION_AUTHORITY" {
				return "bogus"
			}
			return ""
		},
	})
	if err == nil || !strings.Contains(err.Error(), "PIXIE_DELETION_AUTHORITY") {
		t.Fatalf("runtime accepted an invalid deletion authority: %v", err)
	}
}

func TestPairedDeletionAuthorityRequiresStorageKeyAtStartup(t *testing.T) {
	_, err := controller.NewRuntime(controller.RuntimeConfig{
		Getenv: func(key string) string {
			if key == "PIXIE_DELETION_AUTHORITY" {
				return "paired"
			}
			return ""
		},
	})
	if err == nil || !strings.Contains(err.Error(), "pairing storage key") {
		t.Fatalf("paired authority without a storage key was accepted: %v", err)
	}
}
