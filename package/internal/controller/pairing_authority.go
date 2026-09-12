package controller

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/miloszkolber/pixie/internal/persist"
)

// PairingCeremonyHelperVersion versions these additive API-03 pairing
// ceremony guards. They never change any other ledger schema.
const PairingCeremonyHelperVersion = 1

// DeletionAuthorityMode selects how requested deletion records are authorized
// for replay during recovery.
type DeletionAuthorityMode string

const (
	// DeletionAuthorityAuto requires pairing when a durable pairing record
	// exists and otherwise keeps the legacy agent-binding match.
	DeletionAuthorityAuto DeletionAuthorityMode = "auto"
	// DeletionAuthorityPaired requires a valid durable pairing for every
	// requested record. Unpaired or mismatched recovery is quarantined.
	DeletionAuthorityPaired DeletionAuthorityMode = "paired"
	// DeletionAuthorityLegacy keeps the pre-pairing agent-binding behavior
	// byte-for-byte, even when a pairing record exists.
	DeletionAuthorityLegacy DeletionAuthorityMode = "legacy"
)

// ParseDeletionAuthorityMode resolves PIXIE_DELETION_AUTHORITY
// case-insensitively. Empty selects auto, the compatibility default.
func ParseDeletionAuthorityMode(value string) (DeletionAuthorityMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(DeletionAuthorityAuto):
		return DeletionAuthorityAuto, nil
	case string(DeletionAuthorityPaired):
		return DeletionAuthorityPaired, nil
	case string(DeletionAuthorityLegacy):
		return DeletionAuthorityLegacy, nil
	default:
		return "", fmt.Errorf("PIXIE_DELETION_AUTHORITY must be one of auto, paired, legacy; got %q", value)
	}
}

// InspectPairingAuthority returns the durable pairing without writes.
func InspectPairingAuthority(store persist.Store) (persist.PairingAuthority, bool, error) {
	return persist.LoadPairingAuthority(store)
}

// legacyRecoveryBlocked reports the actionable quarantine for destructive
// recovery without a paired authority. Tombstones are preserved and a delete
// is never replayed against a new endpoint.
func legacyRecoveryBlocked(sessionID string) error {
	target := "destructive recovery"
	if sessionID != "" {
		target = fmt.Sprintf("destructive recovery of session %s", sessionID)
	}
	return fmt.Errorf("%s is recovery-blocked: no paired authority; pair explicitly with the verified host/native storage before retrying; tombstone preserved and delete never replayed against a new endpoint", target)
}

// savePairedAuthority publishes one pairing record with fail-closed
// reconciliation: an uncertain publish keeps the visible candidate and stays
// unresolved until the validated primary rereads successfully.
func savePairedAuthority(store persist.Store, pairing persist.PairingAuthority, faults persist.PublishFaults) (persist.PublishOutcome, error) {
	outcome, err := persist.SavePairingAuthority(store, pairing, faults)
	if err == nil {
		return outcome, nil
	}
	if outcome.Kind == persist.OutcomeDurabilityUncertain {
		if _, reconcileErr := persist.ReconcilePairingAuthority(store); reconcileErr != nil {
			return outcome, fmt.Errorf("uncertain pairing publish remains unresolved: %w", err)
		}
	}
	return outcome, err
}

// PairAuthorityWithFaults performs the explicit pairing ceremony: it binds a
// fresh durable authority ID to the verified host identity and native storage
// under an explicit secret verifier. It never records endpoint, port, boot ID
// or transport credentials. Pairing while paired is rejected so re-pairing
// after revoked status or changed storage stays explicit.
func PairAuthorityWithFaults(store persist.Store, hostIdentity, storageKey, secret string, faults persist.PublishFaults) (persist.PairingAuthority, persist.PublishOutcome, error) {
	if err := persist.ValidatePairingHostIdentity(hostIdentity); err != nil {
		return persist.PairingAuthority{}, persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StageValidate}, err
	}
	if err := persist.ValidatePairingStorageKey(storageKey); err != nil {
		return persist.PairingAuthority{}, persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StageValidate}, err
	}
	if err := persist.ValidatePairingSecret(secret); err != nil {
		return persist.PairingAuthority{}, persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StageValidate}, err
	}
	existing, found, err := persist.LoadPairingAuthority(store)
	if err != nil {
		return persist.PairingAuthority{}, persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StageValidate}, err
	}
	if found && existing.Status == persist.PairingStatusPaired {
		return persist.PairingAuthority{}, persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StageValidate}, fmt.Errorf("already paired; revoke the current pairing before re-pairing with different host or storage")
	}
	bindingID, err := persist.GenerateAuthorityBindingID()
	if err != nil {
		return persist.PairingAuthority{}, persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StageReserve}, err
	}
	verifier, err := persist.HashPairingVerifier(secret)
	if err != nil {
		return persist.PairingAuthority{}, persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StageValidate}, err
	}
	now := time.Now().Unix()
	pairing := persist.PairingAuthority{
		AuthorityBindingID: bindingID,
		HostIdentity:       hostIdentity,
		StorageKey:         storageKey,
		VerifierHash:       verifier,
		Status:             persist.PairingStatusPaired,
		Generation:         1,
		CreatedUnix:        now,
		UpdatedUnix:        now,
	}
	outcome, err := savePairedAuthority(store, pairing, faults)
	if err != nil {
		return persist.PairingAuthority{}, outcome, err
	}
	return pairing, outcome, nil
}

// PairAuthority performs the explicit pairing ceremony without fault injection.
func PairAuthority(store persist.Store, hostIdentity, storageKey, secret string) (persist.PairingAuthority, error) {
	pairing, _, err := PairAuthorityWithFaults(store, hostIdentity, storageKey, secret, persist.PublishFaults{})
	return pairing, err
}

// VerifyPairingSecret checks an explicit ceremony secret against the stored
// verifier. Revoked or missing pairings fail closed without verifying.
func VerifyPairingSecret(store persist.Store, secret string) error {
	pairing, found, err := persist.LoadPairingAuthority(store)
	if err != nil {
		return err
	}
	if !found {
		return legacyRecoveryBlocked("")
	}
	if pairing.Status != persist.PairingStatusPaired {
		return fmt.Errorf("paired authority is revoked; re-pair explicitly with the verified host/native storage before retrying")
	}
	if !persist.VerifyPairingSecretHash(pairing.VerifierHash, secret) {
		return fmt.Errorf("pairing secret did not verify")
	}
	return nil
}

// RotatePairingSecretWithFaults performs verified secret rotation: proof of
// the current secret authorizes a new verifier while the durable binding,
// host identity and storage stay unchanged. A new port, boot ID or rotated
// credential is therefore not a new native session.
func RotatePairingSecretWithFaults(store persist.Store, currentSecret, newSecret string, faults persist.PublishFaults) (persist.PairingAuthority, persist.PublishOutcome, error) {
	if err := persist.ValidatePairingSecret(newSecret); err != nil {
		return persist.PairingAuthority{}, persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StageValidate}, err
	}
	pairing, found, err := persist.LoadPairingAuthority(store)
	if err != nil {
		return persist.PairingAuthority{}, persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StageValidate}, err
	}
	if !found {
		return persist.PairingAuthority{}, persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StageValidate}, legacyRecoveryBlocked("")
	}
	if pairing.Status != persist.PairingStatusPaired {
		return persist.PairingAuthority{}, persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StageValidate}, fmt.Errorf("paired authority is revoked; re-pair explicitly instead of rotating")
	}
	if !persist.VerifyPairingSecretHash(pairing.VerifierHash, currentSecret) {
		return persist.PairingAuthority{}, persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StageValidate}, fmt.Errorf("current pairing secret did not verify; rotation requires proof of the current secret")
	}
	if subtle.ConstantTimeCompare([]byte(currentSecret), []byte(newSecret)) == 1 && len(currentSecret) == len(newSecret) {
		return persist.PairingAuthority{}, persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StageValidate}, fmt.Errorf("new pairing secret must differ from the current secret")
	}
	verifier, err := persist.HashPairingVerifier(newSecret)
	if err != nil {
		return persist.PairingAuthority{}, persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StageValidate}, err
	}
	pairing.VerifierHash = verifier
	pairing.Generation++
	pairing.UpdatedUnix = time.Now().Unix()
	if pairing.UpdatedUnix < pairing.CreatedUnix {
		pairing.UpdatedUnix = pairing.CreatedUnix
	}
	outcome, err := savePairedAuthority(store, pairing, faults)
	if err != nil {
		return persist.PairingAuthority{}, outcome, err
	}
	return pairing, outcome, nil
}

// RotatePairingSecret performs verified secret rotation without fault injection.
func RotatePairingSecret(store persist.Store, currentSecret, newSecret string) (persist.PairingAuthority, error) {
	rotated, _, err := RotatePairingSecretWithFaults(store, currentSecret, newSecret, persist.PublishFaults{})
	return rotated, err
}

// RevokePairingAuthorityWithFaults explicitly revokes the durable pairing.
// The record is retained while destructive recovery stays blocked until an
// explicit re-pairing ceremony.
func RevokePairingAuthorityWithFaults(store persist.Store, faults persist.PublishFaults) (persist.PairingAuthority, persist.PublishOutcome, error) {
	pairing, found, err := persist.LoadPairingAuthority(store)
	if err != nil {
		return persist.PairingAuthority{}, persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StageValidate}, err
	}
	if !found {
		return persist.PairingAuthority{}, persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StageValidate}, fmt.Errorf("nothing to revoke: no paired authority")
	}
	if pairing.Status == persist.PairingStatusRevoked {
		return pairing, persist.PublishOutcome{Kind: persist.OutcomeInstalled, Stage: persist.StageAcknowledge, PrimaryVisible: true}, nil
	}
	pairing.Status = persist.PairingStatusRevoked
	pairing.UpdatedUnix = time.Now().Unix()
	if pairing.UpdatedUnix < pairing.CreatedUnix {
		pairing.UpdatedUnix = pairing.CreatedUnix
	}
	outcome, err := savePairedAuthority(store, pairing, faults)
	if err != nil {
		return persist.PairingAuthority{}, outcome, err
	}
	return pairing, outcome, nil
}

// RevokePairingAuthority explicitly revokes the durable pairing without fault injection.
func RevokePairingAuthority(store persist.Store) (persist.PairingAuthority, error) {
	revoked, _, err := RevokePairingAuthorityWithFaults(store, persist.PublishFaults{})
	return revoked, err
}

// RequirePairedRecovery guards destructive recovery against the durable
// pairing. It requires the stored binding plus an authenticated connection to
// the paired host/storage. Boot ID, port and endpoint are intentionally not
// parameters: they are ephemeral dialing state and never prove or break
// pairing. A claimed host identity alone is not proof without authentication.
func RequirePairedRecovery(store persist.Store, liveHostIdentity, liveStorageKey string, authenticated bool) (persist.PairingAuthority, error) {
	pairing, found, err := persist.LoadPairingAuthority(store)
	if err != nil {
		return persist.PairingAuthority{}, fmt.Errorf("paired authority is unreadable and destructive recovery fails closed: %w", err)
	}
	if !found {
		return persist.PairingAuthority{}, legacyRecoveryBlocked("")
	}
	if pairing.Status != persist.PairingStatusPaired {
		return persist.PairingAuthority{}, fmt.Errorf("destructive recovery is recovery-blocked: paired authority is revoked; re-pair explicitly with the verified host/native storage before retrying; tombstone preserved and delete never replayed against a new endpoint")
	}
	if liveHostIdentity == "" || liveStorageKey == "" {
		return persist.PairingAuthority{}, fmt.Errorf("destructive recovery is recovery-blocked: live host identity and storage are required; tombstone preserved and delete never replayed against a new endpoint")
	}
	if !authenticated {
		return persist.PairingAuthority{}, fmt.Errorf("destructive recovery is recovery-blocked: live recovery needs an authenticated connection to the paired authority; a claimed host identity alone is not proof of pairing")
	}
	if liveHostIdentity != pairing.HostIdentity {
		return persist.PairingAuthority{}, fmt.Errorf("destructive recovery is recovery-blocked: paired host identity mismatch; a changed host/native storage requires re-pairing; tombstone preserved and delete never replayed against a new endpoint")
	}
	if liveStorageKey != pairing.StorageKey {
		return persist.PairingAuthority{}, fmt.Errorf("destructive recovery is recovery-blocked: native storage changed; re-pair explicitly with the verified storage before retrying; tombstone preserved and delete never replayed against a new endpoint")
	}
	return pairing, nil
}

// DeletionBindingV2 binds a destructive recovery to the durable pairing plus
// the exact session association. It replaces endpoint/secret-derived bindings:
// only authorityBindingId, hostIdentity, verified storage and session ID feed
// the digest.
func DeletionBindingV2(pairing persist.PairingAuthority, sessionID string) (string, error) {
	if pairing.Status != persist.PairingStatusPaired {
		return "", fmt.Errorf("destructive recovery is recovery-blocked: no paired authority for the v2 deletion binding")
	}
	if err := persist.ValidatePairingAuthority(pairing); err != nil {
		return "", err
	}
	if err := validatePiSessionID(sessionID); err != nil {
		return "", fmt.Errorf("invalid session deletion target")
	}
	encoded, _ := json.Marshal([]any{
		"pixie/deletion-binding/v2",
		pairing.AuthorityBindingID,
		pairing.HostIdentity,
		pairing.StorageKey,
		sessionID,
	})
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest), nil
}

// CheckDeletionBindingV2 verifies a v2 deletion binding with a constant-time
// comparison. Legacy endpoint-derived bindings and bindings for another
// authority, storage or session stay recovery-blocked with an actionable
// error instead of replaying against a new endpoint.
func CheckDeletionBindingV2(pairing persist.PairingAuthority, sessionID, binding string) error {
	expected, err := DeletionBindingV2(pairing, sessionID)
	if err != nil {
		return err
	}
	if len(expected) != len(binding) || subtle.ConstantTimeCompare([]byte(expected), []byte(binding)) != 1 {
		return fmt.Errorf("destructive recovery is recovery-blocked: deletion binding does not match the paired authority and session association; retain the tombstone as recovery-blocked and never replay a delete against a new endpoint")
	}
	return nil
}

// IsPairingRecoveryBlocked reports whether an error is the actionable
// recovery-blocked quarantine rather than another failure.
func IsPairingRecoveryBlocked(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "recovery-blocked")
}
