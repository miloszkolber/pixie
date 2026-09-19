package persist

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Auth-secret persistence. The controller signing secret is generated once and
// stored with owner-only permissions like an SSH host key so issued sessions
// survive a restart. The password hash is stored in the same directory and
// keeps its encoded scrypt parameters so a later parameter bump still verifies
// older credentials.

const (
	// AuthSecretFileName is the persisted controller signing secret.
	AuthSecretFileName = "auth-secret"
	// PasswordHashFileName is the persisted scrypt login credential.
	PasswordHashFileName = "password-hash"

	authSecretBytes     = 48
	minAuthSecretLength = 32
	maxAuthSecretLength = 256
	maxPasswordHashSize = 512
)

// LoadOrCreateAuthSecret returns the persisted signing secret, generating and
// atomically storing a new one on first use. An existing but malformed secret
// fails closed instead of being silently rotated and invalidating sessions.
func LoadOrCreateAuthSecret(dir string) (string, error) {
	path := filepath.Join(dir, AuthSecretFileName)
	raw, _, err := ReadBoundedFile(path, maxAuthSecretLength+1)
	switch {
	case err == nil:
		secret := strings.TrimSpace(string(raw))
		if !validAuthSecret(secret) {
			return "", fmt.Errorf("persisted %s is not a valid controller signing secret", AuthSecretFileName)
		}
		return secret, nil
	case !errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("read %s: %w", AuthSecretFileName, err)
	}
	secret, err := newAuthSecret()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := AtomicReplace(path, []byte(secret+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("persist %s: %w", AuthSecretFileName, err)
	}
	return secret, nil
}

// ReadPasswordHash returns the stored scrypt credential. A missing file is not
// an error; an unreadable or oversized file is reported so the caller can fail
// closed rather than fall back to a weaker credential.
func ReadPasswordHash(dir string) (string, bool, error) {
	path := filepath.Join(dir, PasswordHashFileName)
	raw, _, err := ReadBoundedFile(path, maxPasswordHashSize+1)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("read %s: %w", PasswordHashFileName, err)
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "", false, nil
	}
	if len(value) > maxPasswordHashSize || strings.ContainsAny(value, "\r\n") {
		return "", false, fmt.Errorf("persisted %s is not a bounded single-line credential", PasswordHashFileName)
	}
	return value, true, nil
}

// WritePasswordHash atomically stores an encoded scrypt credential at 0600.
func WritePasswordHash(dir, encoded string) error {
	if encoded == "" || len(encoded) > maxPasswordHashSize || strings.ContainsAny(encoded, "\r\n") {
		return fmt.Errorf("password hash must be a bounded single-line value")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := AtomicReplace(filepath.Join(dir, PasswordHashFileName), []byte(encoded+"\n"), 0o600); err != nil {
		return fmt.Errorf("persist %s: %w", PasswordHashFileName, err)
	}
	return nil
}

func newAuthSecret() (string, error) {
	buffer := make([]byte, authSecretBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate controller signing secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func validAuthSecret(secret string) bool {
	if len(secret) < minAuthSecretLength || len(secret) > maxAuthSecretLength {
		return false
	}
	for _, character := range []byte(secret) {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}
