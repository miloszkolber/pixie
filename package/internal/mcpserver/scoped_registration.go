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

// NativeMCPRegistrationRequest identifies one explicitly scoped native MCP
// connection. The fields are claims, not authority: Register issues the
// opaque registration ID and credential that must be presented later.
type NativeMCPRegistrationRequest struct {
	ModuleID   string
	ServerID   string
	SessionID  string
	Generation uint64
	TTL        time.Duration
}

// NativeMCPAuthorization is the complete binding presented to an operation.
// Requiring all IDs on every authorization prevents a valid credential from
// being replayed with a forged server, session, module or generation.
type NativeMCPAuthorization struct {
	RegistrationID string
	Credential     string
	ModuleID       string
	ServerID       string
	SessionID      string
	Generation     uint64
}

// NativeMCPRegistration is returned only after a registration has been
// admitted. Credential is an opaque bearer secret for the private bridge
// channel; it must not be copied into prompts, URLs, layout state or logs.
type NativeMCPRegistration struct {
	RegistrationID string
	Credential     string
	ModuleID       string
	ServerID       string
	SessionID      string
	Generation     uint64
	ExpiresAt      time.Time
}

type nativeMCPRegistrationEntry struct {
	credentialHash [32]byte
	moduleID       string
	serverID       string
	sessionID      string
	generation     uint64
	expiresAt      time.Time
}

const (
	nativeMCPRegistrationIDBytes = 18
	nativeMCPCredentialBytes     = 32
	maxNativeMCPServerIDLength   = 128
	maxNativeMCPCredentialTTL    = 24 * time.Hour
)

// NativeMCPRegistry holds ephemeral native MCP registrations. It is separate
// from persisted module enablement: credentials authorize one live binding,
// not a durable module or global server permission.
type NativeMCPRegistry struct {
	mu            sync.Mutex
	registrations map[string]nativeMCPRegistrationEntry
	generations   map[string]uint64
}

// NewNativeMCPRegistry returns an empty registry.
func NewNativeMCPRegistry() *NativeMCPRegistry {
	return &NativeMCPRegistry{
		registrations: make(map[string]nativeMCPRegistrationEntry),
		generations:   make(map[string]uint64),
	}
}

// Register issues a fresh opaque ID and credential for one exact native MCP
// scope. Invalid or zero-value requests fail closed before any state is added.
func (r *NativeMCPRegistry) Register(request NativeMCPRegistrationRequest) (NativeMCPRegistration, error) {
	if r == nil {
		return NativeMCPRegistration{}, fmt.Errorf("native MCP registry is not configured")
	}
	if !isValidDescriptorModuleID(request.ModuleID) {
		return NativeMCPRegistration{}, fmt.Errorf("invalid native MCP module id %q", request.ModuleID)
	}
	if err := validateNativeMCPServerID(request.ServerID); err != nil {
		return NativeMCPRegistration{}, err
	}
	if err := validateNativeMCPSessionID(request.SessionID); err != nil {
		return NativeMCPRegistration{}, err
	}
	if request.Generation == 0 {
		return NativeMCPRegistration{}, fmt.Errorf("native MCP generation must be positive")
	}
	ttl := request.TTL
	if ttl <= 0 || ttl > maxNativeMCPCredentialTTL {
		return NativeMCPRegistration{}, fmt.Errorf("native MCP credential TTL must be between 1ns and %s", maxNativeMCPCredentialTTL)
	}

	registrationID, err := randomOpaqueToken(nativeMCPRegistrationIDBytes)
	if err != nil {
		return NativeMCPRegistration{}, fmt.Errorf("issue native MCP registration ID: %w", err)
	}
	credential, err := randomOpaqueToken(nativeMCPCredentialBytes)
	if err != nil {
		return NativeMCPRegistration{}, fmt.Errorf("issue native MCP credential: %w", err)
	}
	expiresAt := time.Now().Add(ttl)
	entry := nativeMCPRegistrationEntry{
		credentialHash: sha256.Sum256([]byte(credential)),
		moduleID:       request.ModuleID,
		serverID:       request.ServerID,
		sessionID:      request.SessionID,
		generation:     request.Generation,
		expiresAt:      expiresAt,
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.registrations == nil {
		r.registrations = make(map[string]nativeMCPRegistrationEntry)
	}
	if r.generations == nil {
		r.generations = make(map[string]uint64)
	}
	scopeKey := nativeMCPScopeKey(request.ModuleID, request.ServerID, request.SessionID)
	if current, ok := r.generations[scopeKey]; ok {
		if request.Generation < current {
			return NativeMCPRegistration{}, fmt.Errorf("native MCP generation %d is stale; current generation is %d", request.Generation, current)
		}
		if request.Generation > current {
			for id, previous := range r.registrations {
				if nativeMCPScopeKey(previous.moduleID, previous.serverID, previous.sessionID) == scopeKey && previous.generation < request.Generation {
					delete(r.registrations, id)
				}
			}
			r.generations[scopeKey] = request.Generation
		}
	} else {
		r.generations[scopeKey] = request.Generation
	}
	// A collision is extraordinarily unlikely, but never overwrite a live
	// registration: retry issuance with a new ID instead.
	for {
		if _, exists := r.registrations[registrationID]; !exists {
			break
		}
		registrationID, err = randomOpaqueToken(nativeMCPRegistrationIDBytes)
		if err != nil {
			return NativeMCPRegistration{}, fmt.Errorf("issue native MCP registration ID: %w", err)
		}
	}
	r.registrations[registrationID] = entry
	return NativeMCPRegistration{
		RegistrationID: registrationID,
		Credential:     credential,
		ModuleID:       request.ModuleID,
		ServerID:       request.ServerID,
		SessionID:      request.SessionID,
		Generation:     request.Generation,
		ExpiresAt:      expiresAt,
	}, nil
}

// Authorize accepts only a live server-issued credential and the exact
// registration binding. Forged IDs, swapped IDs, stale generations, expired
// registrations and revoked credentials all fail closed.
func (r *NativeMCPRegistry) Authorize(request NativeMCPAuthorization) error {
	if r == nil || request.RegistrationID == "" || request.Credential == "" {
		return fmt.Errorf("unknown native MCP registration")
	}
	r.mu.Lock()
	entry, ok := r.registrations[request.RegistrationID]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("unknown native MCP registration")
	}
	if time.Now().After(entry.expiresAt) {
		delete(r.registrations, request.RegistrationID)
		r.mu.Unlock()
		return fmt.Errorf("expired native MCP registration")
	}
	credentialHash := sha256.Sum256([]byte(request.Credential))
	credentialMatch := subtle.ConstantTimeCompare(credentialHash[:], entry.credentialHash[:]) == 1
	moduleMatch := nativeMCPConstantTimeEqual(entry.moduleID, request.ModuleID)
	serverMatch := nativeMCPConstantTimeEqual(entry.serverID, request.ServerID)
	sessionMatch := nativeMCPConstantTimeEqual(entry.sessionID, request.SessionID)
	generationMatch := entry.generation == request.Generation
	r.mu.Unlock()
	if !credentialMatch {
		return fmt.Errorf("native MCP credential rejected")
	}
	if !moduleMatch {
		return fmt.Errorf("native MCP scope mismatch: module")
	}
	if !serverMatch {
		return fmt.Errorf("native MCP scope mismatch: server")
	}
	if !sessionMatch {
		return fmt.Errorf("native MCP scope mismatch: session")
	}
	if !generationMatch {
		return fmt.Errorf("native MCP scope mismatch: generation changed")
	}
	return nil
}

// Revoke removes one registration. Unknown IDs are deliberately a no-op so a
// repeated lifecycle cleanup does not disclose registry state.
func (r *NativeMCPRegistry) Revoke(registrationID string) {
	if r == nil || registrationID == "" {
		return
	}
	r.mu.Lock()
	delete(r.registrations, registrationID)
	r.mu.Unlock()
}

// RevokeSession invalidates every registration for one native session. It also
// prunes expired entries, and an empty session never revokes instance state.
func (r *NativeMCPRegistry) RevokeSession(sessionID string) int {
	if r == nil || sessionID == "" {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	revoked := 0
	for id, entry := range r.registrations {
		if now.After(entry.expiresAt) {
			delete(r.registrations, id)
			continue
		}
		if nativeMCPConstantTimeEqual(entry.sessionID, sessionID) {
			delete(r.registrations, id)
			revoked++
		}
	}
	for key := range r.generations {
		if strings.HasSuffix(key, "\x00"+sessionID) {
			delete(r.generations, key)
		}
	}
	return revoked
}

// RevokeModule invalidates registrations belonging to one module, including
// registrations for different native sessions and MCP servers.
func (r *NativeMCPRegistry) RevokeModule(moduleID string) int {
	if r == nil || moduleID == "" {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	revoked := 0
	for id, entry := range r.registrations {
		if now.After(entry.expiresAt) {
			delete(r.registrations, id)
			continue
		}
		if nativeMCPConstantTimeEqual(entry.moduleID, moduleID) {
			delete(r.registrations, id)
			revoked++
		}
	}
	for key := range r.generations {
		if strings.HasPrefix(key, moduleID+"\x00") {
			delete(r.generations, key)
		}
	}
	return revoked
}

// RevokeAll removes every live registration and generation watermark. The
// registry is ephemeral, so shutdown must not leave credentials usable if the
// publisher instance is retained by a caller during teardown.
func (r *NativeMCPRegistry) RevokeAll() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	revoked := len(r.registrations)
	r.registrations = make(map[string]nativeMCPRegistrationEntry)
	r.generations = make(map[string]uint64)
	return revoked
}

// AdvanceGeneration revokes registrations from older generations without
// issuing a new credential. A reconnecting native session calls this before
// registering its replacement binding.
func (r *NativeMCPRegistry) AdvanceGeneration(moduleID, serverID, sessionID string, generation uint64) (int, error) {
	if r == nil {
		return 0, fmt.Errorf("native MCP registry is not configured")
	}
	if !isValidDescriptorModuleID(moduleID) {
		return 0, fmt.Errorf("invalid native MCP module id %q", moduleID)
	}
	if err := validateNativeMCPServerID(serverID); err != nil {
		return 0, err
	}
	if err := validateNativeMCPSessionID(sessionID); err != nil {
		return 0, err
	}
	if generation == 0 {
		return 0, fmt.Errorf("native MCP generation must be positive")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.generations == nil {
		r.generations = make(map[string]uint64)
	}
	key := nativeMCPScopeKey(moduleID, serverID, sessionID)
	if current, ok := r.generations[key]; ok && generation < current {
		return 0, fmt.Errorf("native MCP generation %d is stale; current generation is %d", generation, current)
	}
	revoked := 0
	for id, entry := range r.registrations {
		if nativeMCPScopeKey(entry.moduleID, entry.serverID, entry.sessionID) == key && entry.generation <= generation {
			delete(r.registrations, id)
			revoked++
		}
	}
	r.generations[key] = generation
	return revoked, nil
}

// Len reports unexpired registrations only.
func (r *NativeMCPRegistry) Len() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	count := 0
	for id, entry := range r.registrations {
		if now.After(entry.expiresAt) {
			delete(r.registrations, id)
			continue
		}
		count++
	}
	return count
}

// ScopedRegistration and ScopedRegistrationStore are concise compatibility
// aliases for callers that do not need to mention the native transport.
type ScopedRegistration = NativeMCPRegistration
type ScopedRegistrationRequest = NativeMCPRegistrationRequest
type ScopedAuthorization = NativeMCPAuthorization
type ScopedRegistrationStore = NativeMCPRegistry

func NewScopedRegistrationStore() *ScopedRegistrationStore { return NewNativeMCPRegistry() }

func validateNativeMCPServerID(serverID string) error {
	if len(serverID) == 0 || len(serverID) > maxNativeMCPServerIDLength || strings.TrimSpace(serverID) != serverID {
		return fmt.Errorf("invalid native MCP server id %q", serverID)
	}
	for _, character := range serverID {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' || character == '.' {
			continue
		}
		return fmt.Errorf("invalid native MCP server id %q", serverID)
	}
	return nil
}

func validateNativeMCPSessionID(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("native MCP session id is required")
	}
	return validateScopeSessionID(sessionID)
}

func randomOpaqueToken(size int) (string, error) {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func nativeMCPConstantTimeEqual(left, right string) bool {
	leftDigest := sha256.Sum256([]byte(left))
	rightDigest := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftDigest[:], rightDigest[:]) == 1
}

func nativeMCPScopeKey(moduleID, serverID, sessionID string) string {
	return moduleID + "\x00" + serverID + "\x00" + sessionID
}
