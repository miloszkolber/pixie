package controller_test

import (
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

func pairingSecret(seed byte) string {
	return strings.Repeat(string([]byte{seed}), 32)
}

func TestPairingCeremonyPairsAndVerifies(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	secret := pairingSecret('s')
	pairing, err := controller.PairAuthority(store, "host-ceremony", "agent-dir-ceremony", secret)
	if err != nil {
		t.Fatal(err)
	}
	if pairing.AuthorityBindingID == "" || pairing.Generation != 1 {
		t.Fatalf("ceremony must issue a durable binding at first generation: %#v", pairing)
	}
	if err := controller.VerifyPairingSecret(store, secret); err != nil {
		t.Fatalf("correct ceremony secret must verify: %v", err)
	}
	if err := controller.VerifyPairingSecret(store, pairingSecret('w')); err == nil {
		t.Fatal("wrong ceremony secret must not verify")
	}
	if _, err := controller.PairAuthority(store, "host-ceremony", "agent-dir-ceremony", pairingSecret('n')); err == nil {
		t.Fatal("second pairing while paired must require explicit revocation first")
	} else if !strings.Contains(err.Error(), "revoke") {
		t.Fatalf("re-pairing guard must name revocation: %v", err)
	}
	if _, _, err := controller.PairAuthorityWithFaults(store, "", "agent-dir-ceremony", secret, persist.PublishFaults{}); err == nil {
		t.Fatal("ceremony without host identity must fail")
	}
	if _, _, err := controller.PairAuthorityWithFaults(store, "host-ceremony", "", secret, persist.PublishFaults{}); err == nil {
		t.Fatal("ceremony without verified storage must fail")
	}
	if _, _, err := controller.PairAuthorityWithFaults(store, "host-ceremony", "agent-dir-ceremony", "short", persist.PublishFaults{}); err == nil {
		t.Fatal("ceremony with a weak secret must fail")
	}
}

func TestPairedAuthorityDurableAcrossRestartIndependentOfDialing(t *testing.T) {
	dir := t.TempDir()
	store := persist.Store{Dir: dir}
	secret := pairingSecret('d')
	pairing, err := controller.PairAuthority(store, "host-durable", "agent-dir-durable", secret)
	if err != nil {
		t.Fatal(err)
	}
	// Restart loads the same durable record from disk. A new boot ID, port or
	// endpoint is ephemeral dialing state and is not even an input to the
	// recovery guard, so it cannot change the decision.
	restarted := persist.Store{Dir: dir}
	recovered, err := controller.RequirePairedRecovery(restarted, "host-durable", "agent-dir-durable", true)
	if err != nil {
		t.Fatalf("paired recovery must survive restart: %v", err)
	}
	if recovered.AuthorityBindingID != pairing.AuthorityBindingID {
		t.Fatalf("restart changed durable binding: %#v vs %#v", recovered, pairing)
	}
	// A second restart generation still authorizes the same durable binding.
	again, err := controller.RequirePairedRecovery(persist.Store{Dir: dir}, "host-durable", "agent-dir-durable", true)
	if err != nil || again.AuthorityBindingID != pairing.AuthorityBindingID {
		t.Fatalf("new boot generation must not invalidate pairing: %#v %v", again, err)
	}
	if _, err := controller.RequirePairedRecovery(restarted, "host-durable", "agent-dir-durable", false); err == nil {
		t.Fatal("a claimed host identity alone must not prove pairing without an authenticated connection")
	} else if !controller.IsPairingRecoveryBlocked(err) || !strings.Contains(err.Error(), "authenticated connection") {
		t.Fatalf("unpaired transport must fail with an actionable pairing error: %v", err)
	}
}

func TestPairingLegacyRecoveryBlocked(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	if _, err := controller.RequirePairedRecovery(store, "host-legacy", "agent-dir-legacy", true); err == nil {
		t.Fatal("legacy unpaired recovery must stay blocked")
	} else if !controller.IsPairingRecoveryBlocked(err) {
		t.Fatalf("legacy block must be detectable: %v", err)
	} else if !strings.Contains(err.Error(), "pair explicitly") || !strings.Contains(err.Error(), "tombstone preserved") || !strings.Contains(err.Error(), "never replayed") {
		t.Fatalf("legacy block must carry an actionable pairing remedy: %v", err)
	}
	if err := controller.VerifyPairingSecret(store, pairingSecret('l')); err == nil {
		t.Fatal("verification without pairing must stay blocked")
	} else if !controller.IsPairingRecoveryBlocked(err) {
		t.Fatalf("missing pairing verification must report recovery-blocked: %v", err)
	}
}

func TestPairingRotationPreservesBinding(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	current := pairingSecret('c')
	next := pairingSecret('n')
	pairing, err := controller.PairAuthority(store, "host-rotation", "agent-dir-rotation", current)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := controller.RotatePairingSecret(store, current, next)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.AuthorityBindingID != pairing.AuthorityBindingID {
		t.Fatal("rotation must preserve the durable binding")
	}
	if rotated.HostIdentity != pairing.HostIdentity || rotated.StorageKey != pairing.StorageKey {
		t.Fatalf("rotation must preserve paired host/storage: %#v", rotated)
	}
	if rotated.Generation != pairing.Generation+1 {
		t.Fatalf("rotation must advance the generation: %#v", rotated)
	}
	if err := controller.VerifyPairingSecret(store, next); err != nil {
		t.Fatalf("rotated secret must verify: %v", err)
	}
	if err := controller.VerifyPairingSecret(store, current); err == nil {
		t.Fatal("superseded secret must not verify after rotation")
	}
	if _, err := controller.RotatePairingSecret(store, current, pairingSecret('z')); err == nil {
		t.Fatal("rotation without proof of the current secret must fail")
	}
	if _, err := controller.RotatePairingSecret(store, next, next); err == nil {
		t.Fatal("rotation to the same secret must fail")
	}
	// Rotated credentials keep the same durable authority: recovery with the
	// paired host/storage and a live authenticated connection still succeeds.
	if _, err := controller.RequirePairedRecovery(store, "host-rotation", "agent-dir-rotation", true); err != nil {
		t.Fatalf("recovery must survive verified rotation: %v", err)
	}
}

func TestPairingRevocationBlocksRecoveryUntilRepairing(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	secret := pairingSecret('r')
	pairing, err := controller.PairAuthority(store, "host-revoked", "agent-dir-revoked", secret)
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := controller.RevokePairingAuthority(store)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.AuthorityBindingID != pairing.AuthorityBindingID || revoked.Status != persist.PairingStatusRevoked {
		t.Fatalf("revocation must retain the record as revoked: %#v", revoked)
	}
	if _, err := controller.RequirePairedRecovery(store, "host-revoked", "agent-dir-revoked", true); err == nil {
		t.Fatal("revoked pairing must block destructive recovery")
	} else if !controller.IsPairingRecoveryBlocked(err) || !strings.Contains(err.Error(), "re-pair") {
		t.Fatalf("revocation must direct operators to re-pair: %v", err)
	}
	if err := controller.VerifyPairingSecret(store, secret); err == nil {
		t.Fatal("revoked pairing must not verify")
	}
	if _, err := controller.RotatePairingSecret(store, secret, pairingSecret('q')); err == nil {
		t.Fatal("revoked pairing must not rotate; re-pairing is required")
	}
	repaired, err := controller.PairAuthority(store, "host-revoked", "agent-dir-revoked", pairingSecret('p'))
	if err != nil {
		t.Fatalf("explicit re-pairing after revocation must succeed: %v", err)
	}
	if repaired.AuthorityBindingID == pairing.AuthorityBindingID {
		t.Fatal("re-pairing must issue a fresh binding")
	}
	if _, err := controller.RequirePairedRecovery(store, "host-revoked", "agent-dir-revoked", true); err != nil {
		t.Fatalf("recovery must succeed after explicit re-pairing: %v", err)
	}
}

func TestPairingAuthorityGuardsHostAndStorageChange(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	if _, err := controller.PairAuthority(store, "host-guarded", "agent-dir-guarded", pairingSecret('g')); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.RequirePairedRecovery(store, "host-other", "agent-dir-guarded", true); err == nil {
		t.Fatal("changed host identity must require re-pairing")
	} else if !controller.IsPairingRecoveryBlocked(err) || !strings.Contains(err.Error(), "requires re-pairing") {
		t.Fatalf("host change must direct operators to re-pair: %v", err)
	}
	if _, err := controller.RequirePairedRecovery(store, "host-guarded", "agent-dir-other", true); err == nil {
		t.Fatal("changed native storage must require re-pairing")
	} else if !controller.IsPairingRecoveryBlocked(err) || !strings.Contains(err.Error(), "re-pair") {
		t.Fatalf("storage change must direct operators to re-pair: %v", err)
	}
}

func TestPairingDeletionBindingV2(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	secret := pairingSecret('b')
	pairing, err := controller.PairAuthority(store, "host-binding", "agent-dir-binding", secret)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "agent/session/binding"
	first, err := controller.DeletionBindingV2(pairing, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := controller.DeletionBindingV2(pairing, sessionID)
	if err != nil || first != second {
		t.Fatalf("v2 binding must be deterministic: %q %q %v", first, second, err)
	}
	other, err := controller.DeletionBindingV2(pairing, "agent/session/other")
	if err != nil || other == first {
		t.Fatalf("v2 binding must cover the session association: %q %q", first, other)
	}
	if err := controller.CheckDeletionBindingV2(pairing, sessionID, first); err != nil {
		t.Fatalf("matching v2 binding must authorize: %v", err)
	}
	if err := controller.CheckDeletionBindingV2(pairing, sessionID, other); err == nil {
		t.Fatal("binding for another session must stay recovery-blocked")
	} else if !controller.IsPairingRecoveryBlocked(err) {
		t.Fatalf("mismatched binding must report recovery-blocked: %v", err)
	}
	legacy := "sha256:" + strings.Repeat("a", 64)
	if err := controller.CheckDeletionBindingV2(pairing, sessionID, legacy); err == nil {
		t.Fatal("legacy endpoint-derived binding must not authorize v2 recovery")
	}
}
