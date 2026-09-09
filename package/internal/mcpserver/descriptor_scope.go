package mcpserver

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Trusted scope authority for contribution resources (MODULE-01).
//
// Caller-supplied IDs alone never authorize access: neither an MCP
// transport-session ID, a native session argument, a project label, nor a
// document/resource ID grants anything. Access requires a server-issued
// credential bound to the exact module, resource, session, and generation.
// The credential is an unguessable random token; mismatched, revoked, or
// expired bindings fail closed.
//
// Lifetime model: credentials carry an expiry, are scoped to one
// module/resource/session/generation tuple, and are revoked on session
// deletion (RevokeSession) or generation change (generation mismatch fails).
// Secrets never enter prompts, URLs, or layout state; only the opaque token
// leaves the server, and only to the authorized holder.

const (
	scopeTokenBytes   = 32
	maxScopeSessionID = 256
)

// IssuedScope is a server-issued credential binding one token to one
// module/resource/session/generation tuple until ExpiresAt.
type IssuedScope struct {
	Token      string
	ModuleID   string
	ResourceID string
	SessionID  string
	Generation uint64
	ExpiresAt  time.Time
}

type scopeEntry struct {
	moduleID   string
	resourceID string
	sessionID  string
	generation uint64
	expiresAt  time.Time
}

// ScopeAuthority is an in-memory store of live scope credentials. It is
// intentionally separate from persisted module enablement: credentials are
// short-lived admission bindings, not durable intent.
type ScopeAuthority struct {
	mu      sync.Mutex
	entries map[string]scopeEntry
}

// NewScopeAuthority returns an empty scope authority.
func NewScopeAuthority() *ScopeAuthority {
	return &ScopeAuthority{entries: make(map[string]scopeEntry)}
}

// Issue binds a fresh random token to the exact module/resource/session/
// generation tuple. The resource ID must already be namespaced to the module
// (see ValidateResourceID); otherwise issuance fails closed.
func (a *ScopeAuthority) Issue(moduleID, resourceID, sessionID string, generation uint64, ttl time.Duration) (IssuedScope, error) {
	if !isValidDescriptorModuleID(moduleID) {
		return IssuedScope{}, fmt.Errorf("invalid scope module id %q", moduleID)
	}
	if err := ValidateResourceID(resourceID, moduleID); err != nil {
		return IssuedScope{}, err
	}
	if err := validateScopeSessionID(sessionID); err != nil {
		return IssuedScope{}, err
	}
	tokenBytes := make([]byte, scopeTokenBytes)
	if _, err := rand.Read(tokenBytes); err != nil {
		return IssuedScope{}, fmt.Errorf("issue scope credential: %w", err)
	}
	issued := IssuedScope{
		Token: hex.EncodeToString(tokenBytes), ModuleID: moduleID,
		ResourceID: resourceID, SessionID: sessionID,
		Generation: generation, ExpiresAt: time.Now().Add(ttl),
	}
	a.mu.Lock()
	a.entries[issued.Token] = scopeEntry{
		moduleID: moduleID, resourceID: resourceID,
		sessionID: sessionID, generation: generation, expiresAt: issued.ExpiresAt,
	}
	a.mu.Unlock()
	return issued, nil
}

// Authorize grants access only when the presented token is live and every
// presented ID matches the binding issued by the server. Forged or swapped
// IDs, unknown tokens, expired bindings, and generation changes all fail
// closed with an error and grant nothing.
func (a *ScopeAuthority) Authorize(token, moduleID, resourceID, sessionID string, generation uint64) error {
	if token == "" {
		return fmt.Errorf("unknown scope credential")
	}
	a.mu.Lock()
	entry, ok := a.entries[token]
	if !ok {
		a.mu.Unlock()
		return fmt.Errorf("unknown scope credential")
	}
	if time.Now().After(entry.expiresAt) {
		delete(a.entries, token)
		a.mu.Unlock()
		return fmt.Errorf("expired scope credential")
	}
	moduleMatch := scopeConstantTimeEqual(entry.moduleID, moduleID)
	resourceMatch := scopeConstantTimeEqual(entry.resourceID, resourceID)
	sessionMatch := scopeConstantTimeEqual(entry.sessionID, sessionID)
	generationMatch := entry.generation == generation
	a.mu.Unlock()
	if !moduleMatch {
		return fmt.Errorf("scope mismatch: module")
	}
	if !resourceMatch {
		return fmt.Errorf("scope mismatch: resource")
	}
	if !sessionMatch {
		return fmt.Errorf("scope mismatch: session")
	}
	if !generationMatch {
		return fmt.Errorf("scope mismatch: generation changed")
	}
	return nil
}

// Revoke drops one credential. Unknown tokens are a no-op.
func (a *ScopeAuthority) Revoke(token string) {
	if token == "" {
		return
	}
	a.mu.Lock()
	delete(a.entries, token)
	a.mu.Unlock()
}

// RevokeSession drops every credential bound to the session. It models
// session deletion: stale tokens from a deleted session never authorize new
// work. An empty session ID revokes nothing, so instance-scoped bindings are
// never wiped by accident. Expired entries are pruned as well.
func (a *ScopeAuthority) RevokeSession(sessionID string) int {
	if sessionID == "" {
		a.pruneExpiredLockedCheck()
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	revoked := 0
	for token, entry := range a.entries {
		if now.After(entry.expiresAt) || scopeConstantTimeEqual(entry.sessionID, sessionID) {
			delete(a.entries, token)
			if scopeConstantTimeEqual(entry.sessionID, sessionID) {
				revoked++
			}
		}
	}
	return revoked
}

// Len reports the number of unexpired credentials.
func (a *ScopeAuthority) Len() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	count := 0
	for token, entry := range a.entries {
		if now.After(entry.expiresAt) {
			delete(a.entries, token)
			continue
		}
		count++
	}
	return count
}

func (a *ScopeAuthority) pruneExpiredLockedCheck() {
	a.mu.Lock()
	now := time.Now()
	for token, entry := range a.entries {
		if now.After(entry.expiresAt) {
			delete(a.entries, token)
		}
	}
	a.mu.Unlock()
}

func validateScopeSessionID(sessionID string) error {
	if sessionID == "" {
		return nil
	}
	if len(sessionID) > maxScopeSessionID {
		return fmt.Errorf("invalid scope session id length %d", len(sessionID))
	}
	if strings.TrimSpace(sessionID) != sessionID {
		return fmt.Errorf("invalid scope session id %q", sessionID)
	}
	for _, r := range sessionID {
		if r < 0x20 || r == 0x7f || r == '<' || r == '>' {
			return fmt.Errorf("invalid scope session id %q", sessionID)
		}
	}
	return nil
}

func scopeConstantTimeEqual(left, right string) bool {
	leftDigest := sha256.Sum256([]byte(left))
	rightDigest := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftDigest[:], rightDigest[:]) == 1
}
