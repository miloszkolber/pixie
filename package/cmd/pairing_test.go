package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	controller "github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

// pairingTestIdentity is a fixed 32-character lowercase hex host identity,
// matching the value the Go host writes to <agentDir>/pixie/host-identity.json.
const pairingTestIdentity = "0123456789abcdef0123456789abcdef"

func writePairingHostIdentity(t *testing.T, agentDir, identity string) {
	t.Helper()
	dir := filepath.Join(agentDir, "pixie")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(fmt.Sprintf(`{"version":1,"identity":%q}`, identity))
	if err := os.WriteFile(filepath.Join(dir, "host-identity.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runPairingCLI(t *testing.T, command string, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runPairingCommand(command, args, &out); err != nil {
		t.Fatalf("runPairingCommand(%s) failed: %v", command, err)
	}
	return out.String()
}

func decodePairingOutput(t *testing.T, out string) pairingOutput {
	t.Helper()
	var result pairingOutput
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("ceremony output is not valid JSON: %q: %v", out, err)
	}
	return result
}

func TestPairingCLIPairsResolvedAgentDirAndBlocksWrongStorage(t *testing.T) {
	dataDir := t.TempDir()
	agentDir := t.TempDir()
	writePairingHostIdentity(t, agentDir, pairingTestIdentity)

	out := runPairingCLI(t, "pair", "--data-dir", dataDir, "--agent-dir", agentDir, "--json")
	result := decodePairingOutput(t, out)
	if result.Status != persist.PairingStatusPaired {
		t.Fatalf("status = %q, want paired", result.Status)
	}
	if result.Secret == "" {
		t.Fatal("pair must print the generated ceremony secret")
	}
	if strings.Count(out, result.Secret) != 1 {
		t.Fatalf("secret must be printed exactly once: %q", out)
	}

	store := persist.Store{Dir: dataDir}
	pairing, found, err := persist.LoadPairingAuthority(store)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("pair did not write a durable pairing record")
	}
	wantHost := "pi:" + pairingTestIdentity
	wantStorage, err := persist.DerivePairingStorageKey(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if pairing.HostIdentity != wantHost {
		t.Fatalf("host identity = %q, want %q", pairing.HostIdentity, wantHost)
	}
	if pairing.StorageKey != wantStorage {
		t.Fatalf("storage key = %q, want %q", pairing.StorageKey, wantStorage)
	}
	if strings.Contains(out, pairing.VerifierHash) {
		t.Fatalf("verifier hash leaked into command output: %q", out)
	}
	if _, err := controller.RequirePairedRecovery(store, wantHost, wantStorage, true); err != nil {
		t.Fatalf("paired recovery with the resolved identity and storage must succeed: %v", err)
	}

	otherStorage, err := persist.DerivePairingStorageKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.RequirePairedRecovery(store, wantHost, otherStorage, true); err == nil {
		t.Fatal("recovery with a different storage key must be blocked")
	} else if !controller.IsPairingRecoveryBlocked(err) {
		t.Fatalf("wrong storage must report recovery-blocked: %v", err)
	}

	var second bytes.Buffer
	err = runPairingCommand("pair", []string{"--data-dir", dataDir, "--agent-dir", agentDir}, &second)
	if err == nil {
		t.Fatal("pairing twice without revocation must fail")
	}
	if !strings.Contains(err.Error(), "revoke") {
		t.Fatalf("pair-twice failure must direct operators to revoke: %v", err)
	}
	if second.Len() != 0 {
		t.Fatalf("failed pairing must not print a secret: %q", second.String())
	}
}

func TestPairingCLIRevokeBlocksRecovery(t *testing.T) {
	dataDir := t.TempDir()
	agentDir := t.TempDir()
	writePairingHostIdentity(t, agentDir, pairingTestIdentity)
	store := persist.Store{Dir: dataDir}
	wantHost := "pi:" + pairingTestIdentity
	wantStorage, err := persist.DerivePairingStorageKey(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	runPairingCLI(t, "pair", "--data-dir", dataDir, "--agent-dir", agentDir, "--json")

	out := runPairingCLI(t, "revoke-pairing", "--data-dir", dataDir, "--agent-dir", agentDir, "--json")
	result := decodePairingOutput(t, out)
	if result.Status != persist.PairingStatusRevoked {
		t.Fatalf("status = %q, want revoked", result.Status)
	}
	if result.Secret != "" {
		t.Fatalf("revocation must not issue a secret: %q", result.Secret)
	}
	if _, err := controller.RequirePairedRecovery(store, wantHost, wantStorage, true); err == nil {
		t.Fatal("revoked pairing must block destructive recovery")
	} else if !controller.IsPairingRecoveryBlocked(err) {
		t.Fatalf("revocation block must be actionable: %v", err)
	}
}

func TestPairingCLIRotateChangesVerifier(t *testing.T) {
	dataDir := t.TempDir()
	agentDir := t.TempDir()
	writePairingHostIdentity(t, agentDir, pairingTestIdentity)
	store := persist.Store{Dir: dataDir}

	pairOut := runPairingCLI(t, "pair", "--data-dir", dataDir, "--agent-dir", agentDir, "--json")
	current := decodePairingOutput(t, pairOut).Secret
	before, _, err := persist.LoadPairingAuthority(store)
	if err != nil {
		t.Fatal(err)
	}

	var missing bytes.Buffer
	if err := runPairingCommand("rotate-pairing", []string{"--data-dir", dataDir, "--agent-dir", agentDir}, &missing); err == nil {
		t.Fatal("rotation without --secret must fail")
	} else if !strings.Contains(err.Error(), "--secret") {
		t.Fatalf("missing rotation secret must be named: %v", err)
	}

	rotateOut := runPairingCLI(t, "rotate-pairing", "--data-dir", dataDir, "--agent-dir", agentDir, "--secret", current, "--json")
	rotated := decodePairingOutput(t, rotateOut)
	if rotated.Secret == "" || rotated.Secret == current {
		t.Fatalf("rotation must issue a fresh secret: %q", rotated.Secret)
	}
	if rotated.Generation != before.Generation+1 {
		t.Fatalf("rotation generation = %d, want %d", rotated.Generation, before.Generation+1)
	}
	after, _, err := persist.LoadPairingAuthority(store)
	if err != nil {
		t.Fatal(err)
	}
	if after.VerifierHash == before.VerifierHash {
		t.Fatal("rotation must change the stored verifier")
	}
	if err := controller.VerifyPairingSecret(store, rotated.Secret); err != nil {
		t.Fatalf("rotated secret must verify: %v", err)
	}
	if err := controller.VerifyPairingSecret(store, current); err == nil {
		t.Fatal("superseded secret must not verify after rotation")
	}
}

func TestPairingCLIExplicitOverridesAndMissingIdentity(t *testing.T) {
	dataDir := t.TempDir()
	var missing bytes.Buffer
	if err := runPairingCommand("pair", []string{"--data-dir", dataDir}, &missing); err == nil {
		t.Fatal("pair without an agent directory or explicit overrides must fail")
	} else if !strings.Contains(err.Error(), "--agent-dir") {
		t.Fatalf("missing identity source must be actionable: %v", err)
	}

	storageKey := persist.PairingStorageKeyPrefix + strings.Repeat("0", 64)
	out := runPairingCLI(t, "pair", "--data-dir", dataDir, "--host-identity", pairingTestIdentity, "--storage-key", storageKey, "--json")
	result := decodePairingOutput(t, out)
	if result.Status != persist.PairingStatusPaired {
		t.Fatalf("status = %q, want paired", result.Status)
	}
	pairing, found, err := persist.LoadPairingAuthority(persist.Store{Dir: dataDir})
	if err != nil || !found {
		t.Fatalf("explicit override pairing was not stored: %v", err)
	}
	if pairing.HostIdentity != "pi:"+pairingTestIdentity {
		t.Fatalf("explicit host identity = %q, want AgentProfile form", pairing.HostIdentity)
	}
	if pairing.StorageKey != storageKey {
		t.Fatalf("explicit storage key = %q, want %q", pairing.StorageKey, storageKey)
	}
}

func TestPairingCLIUsesConfigDataDirAndAgentDir(t *testing.T) {
	dataDir := t.TempDir()
	agentDir := t.TempDir()
	writePairingHostIdentity(t, agentDir, pairingTestIdentity)
	configPath := filepath.Join(t.TempDir(), "pixie.json")
	config := fmt.Sprintf(`{"dataDir":%q,"agentDir":%q}`, dataDir, agentDir)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	out := runPairingCLI(t, "pair", "--config", configPath, "--json")
	if result := decodePairingOutput(t, out); result.Status != persist.PairingStatusPaired {
		t.Fatalf("status = %q, want paired", result.Status)
	}
	pairing, found, err := persist.LoadPairingAuthority(persist.Store{Dir: dataDir})
	if err != nil || !found {
		t.Fatalf("config-resolved pairing was not stored: %v", err)
	}
	if pairing.HostIdentity != "pi:"+pairingTestIdentity {
		t.Fatalf("config agentDir host identity = %q", pairing.HostIdentity)
	}
}
