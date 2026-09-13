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
	pairing, err := controller.PairAuthority(store, "pi:fixture-runtime", storageKey, pairingSecret('a'))
	if err != nil {
		t.Fatal(err)
	}
	manager.SetDeletionAuthority(controller.DeletionAuthorityPaired, storageKey)
	binding, err := controller.PairedDeletionBinding(pairing, "chat")
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.NewSessionDeletions(store).Request(project.ID, "chat", binding); err != nil {
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

func TestDeletionAuthorityPairedV2BindingForOtherSessionQuarantines(t *testing.T) {
	recorder := &deletionMethodRecorder{}
	manager, _, project, store := newSessionManagerWithInitializeAndPublisher(t, nil, nil, piInitializeResponse(), nil, recorder.observe)
	storageKey, err := persist.DerivePairingStorageKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := controller.PairAuthority(store, "pi:fixture-runtime", storageKey, pairingSecret('a'))
	if err != nil {
		t.Fatal(err)
	}
	manager.SetDeletionAuthority(controller.DeletionAuthorityPaired, storageKey)
	// The binding is valid for a different session under the same pairing; the
	// record is for "chat" and must not inherit another session's authority.
	binding, err := controller.PairedDeletionBinding(pairing, "another-session")
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.NewSessionDeletions(store).Request(project.ID, "chat", binding); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecoverDeletions(context.Background()); err != nil {
		t.Fatalf("paired recovery blocked startup: %v", err)
	}
	status := manager.DeletionRecoveryStatus()
	if len(status) != 1 || status[0].SessionID != "chat" || !strings.Contains(status[0].Reason, "recovery-blocked") {
		t.Fatalf("v2 binding for another session was not quarantined actionably: %#v", status)
	}
	if recorder.saw("session.delete") {
		t.Fatal("v2 binding for another session still dispatched a delete")
	}
	pending, err := controller.NewSessionDeletions(store).List()
	if err != nil || len(pending) != 1 || pending[0].Phase != "requested" {
		t.Fatalf("v2 binding for another session did not keep the tombstone: %#v %v", pending, err)
	}
}

func TestDeletionAuthorityPairedLegacyBindingQuarantinesAndNeverDispatches(t *testing.T) {
	recorder := &deletionMethodRecorder{}
	manager, _, project, store := newSessionManagerWithInitializeAndPublisher(t, nil, nil, piInitializeResponse(), nil, recorder.observe)
	storageKey, err := persist.DerivePairingStorageKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.PairAuthority(store, "pi:fixture-runtime", storageKey, pairingSecret('a')); err != nil {
		t.Fatal(err)
	}
	manager.SetDeletionAuthority(controller.DeletionAuthorityPaired, storageKey)
	// A legacy agent binding matches the live host identity but carries no
	// record-level pairing/session binding, so paired strict mode must never
	// replay it against the paired endpoint.
	if err := controller.NewSessionDeletions(store).Request(project.ID, "chat", fixtureDeletionAgentBinding(t)); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecoverDeletions(context.Background()); err != nil {
		t.Fatalf("paired legacy recovery blocked startup: %v", err)
	}
	status := manager.DeletionRecoveryStatus()
	if len(status) != 1 || status[0].SessionID != "chat" || !strings.Contains(status[0].Reason, "recovery-blocked") {
		t.Fatalf("legacy-only binding in paired mode was not quarantined actionably: %#v", status)
	}
	if recorder.saw("session.delete") {
		t.Fatal("legacy-only binding in paired mode dispatched a delete")
	}
	pending, err := controller.NewSessionDeletions(store).List()
	if err != nil || len(pending) != 1 || pending[0].Phase != "requested" {
		t.Fatalf("legacy-only binding in paired mode did not keep the tombstone: %#v %v", pending, err)
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

func TestDeletionAuthorityAutoWithPairingRequiresV2SessionBinding(t *testing.T) {
	manager, _, project, store := newSessionManager(t, nil, nil)
	storageKey, err := persist.DerivePairingStorageKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := controller.PairAuthority(store, "pi:fixture-runtime", storageKey, pairingSecret('a'))
	if err != nil {
		t.Fatal(err)
	}
	manager.SetDeletionAuthority(controller.DeletionAuthorityAuto, storageKey)
	binding, err := controller.PairedDeletionBinding(pairing, "chat")
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.NewSessionDeletions(store).Request(project.ID, "chat", binding); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecoverDeletions(context.Background()); err != nil {
		t.Fatalf("auto paired recovery blocked startup: %v", err)
	}
	if status := manager.DeletionRecoveryStatus(); len(status) != 0 {
		t.Fatalf("auto mode quarantined a matching v2 binding: %#v", status)
	}
	pending, err := controller.NewSessionDeletions(store).List()
	if err != nil || len(pending) != 0 {
		t.Fatalf("auto mode did not finish the matching v2 record: %#v %v", pending, err)
	}
}

func TestDeletionAuthorityAutoWithPairingQuarantinesLegacyBinding(t *testing.T) {
	manager, _, project, store := newSessionManager(t, nil, nil)
	storageKey, err := persist.DerivePairingStorageKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.PairAuthority(store, "pi:fixture-runtime", storageKey, pairingSecret('a')); err != nil {
		t.Fatal(err)
	}
	manager.SetDeletionAuthority(controller.DeletionAuthorityAuto, storageKey)
	// The legacy binding matches the live agent, so a pure legacy fallback
	// would replay it. Auto must honor the existing pairing and require the
	// record-level v2 binding instead.
	if err := controller.NewSessionDeletions(store).Request(project.ID, "chat", fixtureDeletionAgentBinding(t)); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecoverDeletions(context.Background()); err != nil {
		t.Fatalf("auto paired recovery blocked startup: %v", err)
	}
	status := manager.DeletionRecoveryStatus()
	if len(status) != 1 || !strings.Contains(status[0].Reason, "recovery-blocked") {
		t.Fatalf("auto mode with a pairing did not require the v2 binding: %#v", status)
	}
	pending, err := controller.NewSessionDeletions(store).List()
	if err != nil || len(pending) != 1 {
		t.Fatalf("auto mode did not keep the legacy tombstone: %#v %v", pending, err)
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

// TestDeletionBindingFormatValidation exercises validDeletionAgentBinding
// through the exported Request boundary because the validator is unexported.
// Both the legacy agent digest and the versioned paired v2 form must be
// accepted; anything else must be rejected before it enters the journal.
func TestDeletionBindingFormatValidation(t *testing.T) {
	pairing, err := controller.PairAuthority(persist.Store{Dir: t.TempDir()}, "pi:fixture-runtime", "agent-dir-format", pairingSecret('f'))
	if err != nil {
		t.Fatal(err)
	}
	v2, err := controller.PairedDeletionBinding(pairing, "session-v2")
	if err != nil {
		t.Fatal(err)
	}
	accepted := []struct {
		name    string
		binding string
	}{
		{"v2", v2},
		{"legacy", fixtureDeletionAgentBinding(t)},
	}
	for _, test := range accepted {
		deletions := controller.NewSessionDeletions(persist.Store{Dir: t.TempDir()})
		if err := deletions.Request("project-format", "session-"+test.name, test.binding); err != nil {
			t.Fatalf("%s binding must be accepted: %v", test.name, err)
		}
	}
	malformed := []struct {
		name    string
		binding string
	}{
		{"empty", ""},
		{"missing digest", "sha256:"},
		{"short digest", "sha256:" + strings.Repeat("a", 63)},
		{"long digest", "sha256:" + strings.Repeat("a", 65)},
		{"non-hex digest", "sha256:" + strings.Repeat("z", 64)},
		{"v2 missing digest", "deletion-binding-v2:"},
		{"v2 malformed digest", "deletion-binding-v2:sha256:xyz"},
		{"garbage", "not-a-binding"},
	}
	for _, test := range malformed {
		deletions := controller.NewSessionDeletions(persist.Store{Dir: t.TempDir()})
		if err := deletions.Request("project-format", "session-"+test.name, test.binding); err == nil {
			t.Fatalf("malformed %s binding must be rejected", test.name)
		}
	}
}

// deletionBindingCapture observes the durable record while Delete has written
// it but not yet dispatched the native delete, so the persisted binding can be
// asserted without racing the later confirmation/forget.
type deletionBindingCapture struct {
	mu      sync.Mutex
	store   persist.Store
	binding string
}

func (c *deletionBindingCapture) setStore(store persist.Store) {
	c.mu.Lock()
	c.store = store
	c.mu.Unlock()
}

func (c *deletionBindingCapture) observer(method string, _ map[string]any) {
	if method != "session.delete" {
		return
	}
	c.mu.Lock()
	store := c.store
	c.mu.Unlock()
	records, err := controller.NewSessionDeletions(store).List()
	if err != nil {
		return
	}
	c.mu.Lock()
	for _, record := range records {
		if record.SessionID == "chat" {
			c.binding = record.AgentBinding
		}
	}
	c.mu.Unlock()
}

func (c *deletionBindingCapture) value() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.binding
}

// TestDeletePersistsV2BindingWhenPaired proves required behavior 2: with an
// active paired authority Delete persists the record-level v2 binding derived
// from the pairing and session, not the legacy connected-agent digest.
func TestDeletePersistsV2BindingWhenPaired(t *testing.T) {
	storageKey, err := persist.DerivePairingStorageKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	capture := &deletionBindingCapture{}
	manager, _, project, store := newSessionManagerWithInitializeAndPublisher(t, nil, nil, piInitializeResponse(), nil, capture.observer)
	capture.setStore(store)
	manager.SetDeletionAuthority(controller.DeletionAuthorityPaired, storageKey)
	pairing, err := controller.PairAuthority(store, "pi:fixture-runtime", storageKey, pairingSecret('a'))
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(ctx, project.ID, "chat", project.Roots[0]); err != nil {
		t.Fatalf("paired delete failed: %v", err)
	}
	want, err := controller.PairedDeletionBinding(pairing, "chat")
	if err != nil {
		t.Fatal(err)
	}
	if got := capture.value(); got != want {
		t.Fatalf("paired Delete persisted %q, want v2 binding %q", got, want)
	}
}

// TestDeletePersistsLegacyBindingWithoutPairing proves the other half of
// required behavior 2: without a paired authority the legacy connected-agent
// binding is still stored so unpaired auto and legacy recovery keep working.
func TestDeletePersistsLegacyBindingWithoutPairing(t *testing.T) {
	capture := &deletionBindingCapture{}
	manager, _, project, store := newSessionManagerWithInitializeAndPublisher(t, nil, nil, piInitializeResponse(), nil, capture.observer)
	capture.setStore(store)
	manager.SetDeletionAuthority(controller.DeletionAuthorityAuto, "")
	ctx := t.Context()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(ctx, project.ID, "chat", project.Roots[0]); err != nil {
		t.Fatalf("unpaired delete failed: %v", err)
	}
	if got, want := capture.value(), fixtureDeletionAgentBinding(t); got != want {
		t.Fatalf("unpaired Delete persisted %q, want legacy binding %q", got, want)
	}
}
