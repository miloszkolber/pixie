package persist

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// PairingAuthorityHelperVersion versions these additive API-03 durable
// pairing helpers. It never changes any other ledger schema.
const PairingAuthorityHelperVersion = 1

const (
	// PairingAuthorityFile is the durable pairing authority. It stores only
	// durable identity (binding, host, verified native storage, verifier)
	// and never ephemeral dialing state such as endpoint, port, boot ID or
	// transport credentials.
	PairingAuthorityFile    = "pi-pairing-authority.json"
	PairingAuthorityVersion = 1
	PairingAuthorityEngine  = "pi"

	// PairingStatusPaired marks the active destructive-recovery authority.
	PairingStatusPaired = "paired"
	// PairingStatusRevoked marks an explicitly revoked pairing. The record
	// is retained for audit while recovery stays blocked until re-pairing.
	PairingStatusRevoked = "revoked"

	// PairingBindingPrefix prefixes the random authority binding ID.
	PairingBindingPrefix = "pairing-"

	// PairingStorageKeyPrefix labels a storage key derived from one native
	// agent directory. The digest is stable and non-reversible; the raw path
	// never enters the pairing record.
	PairingStorageKeyPrefix = "agent-dir-sha256:"
)

// pairingStorageKeyDomain separates derived storage keys from every other
// digest in this package.
const pairingStorageKeyDomain = "pixie-pairing-storage-key-v1\x00"

// PairingAuthority is the durable controller pairing with one host/native
// storage. It is independent of ephemeral dialing: a new port, boot ID or
// rotated transport credential never changes it.
type PairingAuthority struct {
	AuthorityBindingID string `json:"authorityBindingId"`
	HostIdentity       string `json:"hostIdentity"`
	StorageKey         string `json:"storageKey"`
	VerifierHash       string `json:"verifierHash"`
	Status             string `json:"status"`
	Generation         uint64 `json:"generation"`
	CreatedUnix        int64  `json:"createdUnix"`
	UpdatedUnix        int64  `json:"updatedUnix"`
}

type storedPairingAuthority struct {
	Version int              `json:"version"`
	Engine  string           `json:"engine"`
	Pairing PairingAuthority `json:"pairing"`
}

// ValidatePairingHostIdentity checks the stable host identity. It mirrors the
// host v2 epoch bounds without importing transport state.
func ValidatePairingHostIdentity(value string) error {
	if value == "" || len(value) > 256 {
		return fmt.Errorf("pairing host identity is invalid")
	}
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("pairing host identity is invalid")
	}
	return nil
}

// ValidatePairingStorageKey checks the canonical native-storage key. The key
// names verified native storage (for example the canonical agent-directory
// key), never an endpoint URL or credential.
func ValidatePairingStorageKey(value string) error {
	if value == "" || len(value) > 512 {
		return fmt.Errorf("pairing storage key is invalid")
	}
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("pairing storage key is invalid")
	}
	return nil
}

// DerivePairingStorageKey derives a canonical, stable and non-reversible
// pairing storage key from one native agent directory. Path separators are
// normalized and the cleaned path is hashed with domain separation, so the same
// directory yields the same key across restarts and spelling while the raw path
// is never stored or exposed in the pairing record or its diagnostics.
func DerivePairingStorageKey(agentDir string) (string, error) {
	trimmed := strings.TrimSpace(agentDir)
	if trimmed == "" {
		return "", fmt.Errorf("pairing storage key requires an agent directory")
	}
	// Treat both separators as path separators so a Windows-spelled directory
	// hashes identically to its slash form. Clean removes trailing separators,
	// "." segments and duplicate separators.
	normalized := path.Clean(strings.ReplaceAll(trimmed, "\\", "/"))
	if normalized == "." || normalized == "/" {
		return "", fmt.Errorf("pairing storage key requires a specific agent directory")
	}
	digest := sha256.Sum256([]byte(pairingStorageKeyDomain + normalized))
	return PairingStorageKeyPrefix + hex.EncodeToString(digest[:]), nil
}

// ValidatePairingSecret checks an explicit pairing secret before it is hashed.
// It uses the same printable-token bounds as controller authentication so a
// weak or placeholder ceremony secret cannot pair.
func ValidatePairingSecret(secret string) error {
	if len(secret) < 32 || len(secret) > 256 {
		return fmt.Errorf("pairing secret must be 32-256 characters")
	}
	if strings.HasPrefix(secret, "INVALID_REPLACE_WITH_RANDOM_") || strings.HasPrefix(secret, "replace-with-a-random-") {
		return fmt.Errorf("pairing secret must be a strong random token")
	}
	for _, character := range []byte(secret) {
		if character < 0x21 || character > 0x7e {
			return fmt.Errorf("pairing secret must be printable without whitespace")
		}
	}
	return nil
}

func validPairingBindingID(value string) bool {
	if !strings.HasPrefix(value, PairingBindingPrefix) {
		return false
	}
	hexPart := strings.TrimPrefix(value, PairingBindingPrefix)
	if len(hexPart) != 32 {
		return false
	}
	for _, character := range hexPart {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validPairingVerifierHash(value string) bool {
	const prefix = "sha256:"
	if len(value) != len(prefix)+64 || !strings.HasPrefix(value, prefix) {
		return false
	}
	for _, character := range value[len(prefix):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// ValidatePairingAuthority checks one durable pairing record.
func ValidatePairingAuthority(pairing PairingAuthority) error {
	if !validPairingBindingID(pairing.AuthorityBindingID) {
		return fmt.Errorf("pairing authority binding is invalid")
	}
	if err := ValidatePairingHostIdentity(pairing.HostIdentity); err != nil {
		return err
	}
	if err := ValidatePairingStorageKey(pairing.StorageKey); err != nil {
		return err
	}
	if !validPairingVerifierHash(pairing.VerifierHash) {
		return fmt.Errorf("pairing verifier is invalid")
	}
	if pairing.Status != PairingStatusPaired && pairing.Status != PairingStatusRevoked {
		return fmt.Errorf("pairing status is invalid")
	}
	if pairing.Generation == 0 {
		return fmt.Errorf("pairing generation is invalid")
	}
	if pairing.CreatedUnix <= 0 || pairing.UpdatedUnix < pairing.CreatedUnix {
		return fmt.Errorf("pairing timestamps are invalid")
	}
	return nil
}

func validateStoredPairingAuthority(value storedPairingAuthority) error {
	if value.Version != PairingAuthorityVersion || value.Engine != PairingAuthorityEngine {
		return fmt.Errorf("invalid pairing authority")
	}
	return ValidatePairingAuthority(value.Pairing)
}

// GenerateAuthorityBindingID returns a fresh unguessable durable binding ID.
// Ephemeral dialing values never influence it.
func GenerateAuthorityBindingID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate pairing binding: %w", err)
	}
	return PairingBindingPrefix + hex.EncodeToString(raw[:]), nil
}

// GeneratePairingSecret returns a fresh unguessable ceremony secret. The
// secret itself is never persisted; only its verifier hash is stored.
func GeneratePairingSecret() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate pairing secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// HashPairingVerifier hashes an explicit ceremony secret with domain
// separation. Only the returned hash is persisted.
func HashPairingVerifier(secret string) (string, error) {
	if err := ValidatePairingSecret(secret); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte("pixie-pairing-verifier-v1\x00" + secret))
	return fmt.Sprintf("sha256:%x", digest), nil
}

// VerifyPairingSecretHash reports whether a candidate secret matches the
// stored verifier hash using a constant-time comparison.
func VerifyPairingSecretHash(storedHash, candidate string) bool {
	if !validPairingVerifierHash(storedHash) || candidate == "" {
		return false
	}
	digest := sha256.Sum256([]byte("pixie-pairing-verifier-v1\x00" + candidate))
	computed := fmt.Sprintf("sha256:%x", digest)
	if len(computed) != len(storedHash) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(computed), []byte(storedHash)) == 1
}

// LoadPairingAuthority reads the durable pairing primary with no writes. The
// primary is the only pairing authority: a missing or corrupt primary fails
// closed and never falls back to the older backup generation. A missing file
// with no backup reports found=false for explicit legacy handling.
func LoadPairingAuthority(store Store) (PairingAuthority, bool, error) {
	name := filepath.Join(store.Dir, PairingAuthorityFile)
	raw, _, err := ReadFile(name)
	if os.IsNotExist(err) {
		if _, _, backupErr := ReadFile(name + ".bak"); os.IsNotExist(backupErr) {
			return PairingAuthority{}, false, nil
		}
		return PairingAuthority{}, false, fmt.Errorf("pairing authority is missing while a backup remains")
	}
	if err != nil {
		return PairingAuthority{}, false, fmt.Errorf("pairing authority is unreadable")
	}
	var value storedPairingAuthority
	if Decode(raw, &value, validateStoredPairingAuthority) != nil {
		return PairingAuthority{}, false, fmt.Errorf("pairing authority is unreadable")
	}
	return value.Pairing, true, nil
}

// SavePairingAuthority validates and publishes one pairing record,
// classifying the result with the shared typed outcome contract. Uncertain
// outcomes keep the visible candidate for reconciliation and never restore an
// older backup over it.
func SavePairingAuthority(store Store, pairing PairingAuthority, faults PublishFaults) (PublishOutcome, error) {
	return WriteWithOutcome(store, PairingAuthorityFile, storedPairingAuthority{Version: PairingAuthorityVersion, Engine: PairingAuthorityEngine, Pairing: pairing}, validateStoredPairingAuthority, faults)
}

// ReconcilePairingAuthority rereads the validated pairing primary after a
// durability-uncertain publish. It never falls back to the backup generation.
func ReconcilePairingAuthority(store Store) (PairingAuthority, error) {
	value, err := ReconcilePrimary[storedPairingAuthority](store, PairingAuthorityFile, validateStoredPairingAuthority)
	if err != nil {
		return PairingAuthority{}, err
	}
	return value.Pairing, nil
}
