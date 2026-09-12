package persist_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/persist"
)

func authorityPairingFixture(t *testing.T, binding, host, storage, verifier string) persist.PairingAuthority {
	t.Helper()
	return persist.PairingAuthority{
		AuthorityBindingID: binding,
		HostIdentity:       host,
		StorageKey:         storage,
		VerifierHash:       verifier,
		Status:             persist.PairingStatusPaired,
		Generation:         1,
		CreatedUnix:        1700000000,
		UpdatedUnix:        1700000000,
	}
}

func authorityVerifierFor(t *testing.T, secret string) string {
	t.Helper()
	verifier, err := persist.HashPairingVerifier(secret)
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

func TestAuthorityPairingDurableAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	store := persist.Store{Dir: dir}
	secret := strings.Repeat("s", 32)
	verifier := authorityVerifierFor(t, secret)
	binding, err := persist.GenerateAuthorityBindingID()
	if err != nil {
		t.Fatal(err)
	}
	pairing := authorityPairingFixture(t, binding, "host-identity-one", "agent-dir-key-one", verifier)
	if _, err := persist.SavePairingAuthority(store, pairing, persist.PublishFaults{}); err != nil {
		t.Fatal(err)
	}
	// Simulate a restart with a fresh store handle over the same directory.
	// Ephemeral dialing state (endpoint, port, boot ID, transport secret) is
	// deliberately not part of the record, so it cannot change the outcome.
	restarted := persist.Store{Dir: dir}
	loaded, found, err := persist.LoadPairingAuthority(restarted)
	if err != nil || !found {
		t.Fatalf("paired authority must survive restart: found=%v err=%v", found, err)
	}
	if loaded.AuthorityBindingID != binding || loaded.HostIdentity != "host-identity-one" || loaded.StorageKey != "agent-dir-key-one" {
		t.Fatalf("restart changed durable pairing: %#v", loaded)
	}
	if !persist.VerifyPairingSecretHash(loaded.VerifierHash, secret) {
		t.Fatal("verifier must survive restart")
	}
	if persist.VerifyPairingSecretHash(loaded.VerifierHash, strings.Repeat("x", 32)) {
		t.Fatal("wrong secret must not verify after restart")
	}
}

func TestAuthorityPairingVerifierCeremony(t *testing.T) {
	secret, err := persist.GeneratePairingSecret()
	if err != nil {
		t.Fatal(err)
	}
	if err := persist.ValidatePairingSecret(secret); err != nil {
		t.Fatalf("generated secret must be strong: %v", err)
	}
	verifier, err := persist.HashPairingVerifier(secret)
	if err != nil {
		t.Fatal(err)
	}
	if !persist.VerifyPairingSecretHash(verifier, secret) {
		t.Fatal("correct ceremony secret must verify")
	}
	if persist.VerifyPairingSecretHash(verifier, strings.Repeat("w", 32)) {
		t.Fatal("wrong ceremony secret must not verify")
	}
	if _, err := persist.HashPairingVerifier("short"); err == nil {
		t.Fatal("weak ceremony secret must be rejected")
	}
	if persist.VerifyPairingSecretHash(verifier, "") {
		t.Fatal("empty secret must not verify")
	}
	first, err := persist.GenerateAuthorityBindingID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := persist.GenerateAuthorityBindingID()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("binding IDs must be unguessable and unique")
	}
}

func TestAuthorityPairingValidationRejectsEphemeralShapes(t *testing.T) {
	verifier := authorityVerifierFor(t, strings.Repeat("v", 32))
	binding, err := persist.GenerateAuthorityBindingID()
	if err != nil {
		t.Fatal(err)
	}
	valid := authorityPairingFixture(t, binding, "host-one", "storage-one", verifier)
	if err := persist.ValidatePairingAuthority(valid); err != nil {
		t.Fatalf("valid pairing rejected: %v", err)
	}
	for name, mutate := range map[string]func(*persist.PairingAuthority){
		"empty binding":   func(p *persist.PairingAuthority) { p.AuthorityBindingID = "" },
		"legacy binding":  func(p *persist.PairingAuthority) { p.AuthorityBindingID = "sha256:" + strings.Repeat("a", 64) },
		"empty host":      func(p *persist.PairingAuthority) { p.HostIdentity = "" },
		"empty storage":   func(p *persist.PairingAuthority) { p.StorageKey = "" },
		"empty verifier":  func(p *persist.PairingAuthority) { p.VerifierHash = "" },
		"bad status":      func(p *persist.PairingAuthority) { p.Status = "pending" },
		"zero generation": func(p *persist.PairingAuthority) { p.Generation = 0 },
		"bad timestamps":  func(p *persist.PairingAuthority) { p.UpdatedUnix = p.CreatedUnix - 1 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			if err := persist.ValidatePairingAuthority(candidate); err == nil {
				t.Fatalf("invalid pairing accepted: %#v", candidate)
			}
			if _, err := persist.SavePairingAuthority(persist.Store{Dir: t.TempDir()}, candidate, persist.PublishFaults{}); err == nil {
				t.Fatal("invalid pairing publish must fail before commit")
			}
		})
	}
}

func TestAuthorityPairingFailClosed(t *testing.T) {
	dir := t.TempDir()
	store := persist.Store{Dir: dir}
	if _, found, err := persist.LoadPairingAuthority(store); err != nil || found {
		t.Fatalf("empty state must report unpaired without error: found=%v err=%v", found, err)
	}
	verifier := authorityVerifierFor(t, strings.Repeat("k", 32))
	binding, err := persist.GenerateAuthorityBindingID()
	if err != nil {
		t.Fatal(err)
	}
	pairing := authorityPairingFixture(t, binding, "host-fail-closed", "storage-fail-closed", verifier)
	if _, err := persist.SavePairingAuthority(store, pairing, persist.PublishFaults{}); err != nil {
		t.Fatal(err)
	}
	// A second publish creates the backup generation so the missing-primary
	// case below exercises the fail-closed backup path.
	second := pairing
	second.Generation = 2
	second.UpdatedUnix = pairing.CreatedUnix + 1
	if _, err := persist.SavePairingAuthority(store, second, persist.PublishFaults{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, persist.PairingAuthorityFile), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := persist.LoadPairingAuthority(store); err == nil {
		t.Fatal("corrupt pairing primary must fail closed instead of replaying backup")
	}
	if _, err := persist.ReconcilePairingAuthority(store); err == nil {
		t.Fatal("corrupt pairing primary must keep uncertainty unresolved")
	}
	if err := os.Remove(filepath.Join(dir, persist.PairingAuthorityFile)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := persist.LoadPairingAuthority(store); err == nil {
		t.Fatal("missing pairing primary with backup must fail closed")
	} else if !strings.Contains(err.Error(), "backup remains") {
		t.Fatalf("missing primary must name the retained backup: %v", err)
	}
}

func TestDerivePairingStorageKeyIsCanonicalStableAndPathFree(t *testing.T) {
	dir := t.TempDir()
	first, err := persist.DerivePairingStorageKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := persist.ValidatePairingStorageKey(first); err != nil {
		t.Fatalf("derived storage key must be a valid pairing storage key: %v", err)
	}
	// A trailing separator and an equivalent spelling must derive the same
	// key, and a different directory must not collide.
	again, err := persist.DerivePairingStorageKey(dir + string(filepath.Separator))
	if err != nil || again != first {
		t.Fatalf("same directory must derive a stable key: %q vs %q err=%v", first, again, err)
	}
	other, err := persist.DerivePairingStorageKey(t.TempDir())
	if err != nil || other == first {
		t.Fatalf("different directories must derive different keys: %q vs %q err=%v", first, other, err)
	}
	nested := filepath.Join(dir, "agent", "native")
	slashed, err := persist.DerivePairingStorageKey(nested)
	if err != nil {
		t.Fatal(err)
	}
	backslashed, err := persist.DerivePairingStorageKey(strings.ReplaceAll(nested, string(filepath.Separator), "\\"))
	if err != nil {
		t.Fatal(err)
	}
	if slashed != backslashed {
		t.Fatalf("path separators must normalize: %q vs %q", slashed, backslashed)
	}
	// The key must not leak the raw agent directory.
	if strings.Contains(first, dir) || strings.Contains(first, "\\") || strings.Contains(first, "/") {
		t.Fatalf("derived key leaked path material: %q", first)
	}
	if _, err := persist.DerivePairingStorageKey("   "); err == nil {
		t.Fatal("empty agent directory must not derive a storage key")
	}
}

func TestAuthorityPairingUncertainReconcilesPrimary(t *testing.T) {
	dir := t.TempDir()
	store := persist.Store{Dir: dir}
	verifier := authorityVerifierFor(t, strings.Repeat("u", 32))
	binding, err := persist.GenerateAuthorityBindingID()
	if err != nil {
		t.Fatal(err)
	}
	prior := authorityPairingFixture(t, binding, "host-uncertain", "storage-uncertain", verifier)
	if _, err := persist.SavePairingAuthority(store, prior, persist.PublishFaults{}); err != nil {
		t.Fatal(err)
	}
	candidate := prior
	candidate.Generation = 2
	candidate.UpdatedUnix = prior.CreatedUnix + 10
	outcome, err := persist.SavePairingAuthority(store, candidate, persist.PublishFaults{FailDirSync: errors.New("injected dir-sync failure")})
	if err == nil || outcome.Kind != persist.OutcomeDurabilityUncertain || !outcome.PrimaryVisible {
		t.Fatalf("post-rename dir-sync must be durability-uncertain: %#v %v", outcome, err)
	}
	if outcome.MayDispatch() || !outcome.MustReconcileLedger() {
		t.Fatalf("uncertain pairing publish must block dispatch until reconciled: %#v", outcome)
	}
	reconciled, err := persist.ReconcilePairingAuthority(store)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Generation != 2 || reconciled.AuthorityBindingID != binding {
		t.Fatalf("reconcile must read the visible candidate: %#v", reconciled)
	}
	primary, err := os.ReadFile(filepath.Join(dir, persist.PairingAuthorityFile))
	if err != nil || !strings.Contains(string(primary), binding) {
		t.Fatal("reconcile must not overwrite the visible candidate with the older backup")
	}
}
