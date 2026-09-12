package controller

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/miloszkolber/pixie/internal/persist"
)

const (
	deletionRequested = "requested"
	deletionConfirmed = "confirmed"
)

type sessionDeletion struct {
	ProjectID    string `json:"projectId"`
	SessionID    string `json:"sessionId"`
	AgentBinding string `json:"agentBinding"`
	Phase        string `json:"phase"`
}

type storedSessionDeletions struct {
	Version int               `json:"version"`
	Engine  string            `json:"engine"`
	Records []sessionDeletion `json:"records"`
}

// DeletionRecovery is one retained unauthorized deletion record that could not
// be safely resumed. It is surfaced for operator reconciliation; the tombstone
// is never replayed or discarded implicitly.
type DeletionRecovery struct {
	ProjectID string `json:"projectId"`
	SessionID string `json:"sessionId"`
	Phase     string `json:"phase"`
	Reason    string `json:"reason"`
}

// SessionDeletions is a fail-closed journal. Its primary file is the only
// deletion authority: an older backup must never resurrect a completed delete.
type SessionDeletions struct {
	mu            sync.Mutex
	store         persist.Store
	publishFaults persist.PublishFaults
}

func NewSessionDeletions(store persist.Store) *SessionDeletions {
	return &SessionDeletions{store: store}
}

// SetPublishFaults injects deterministic post-rename faults into deletion
// publication for X04 coverage. Production code leaves it zero-valued.
func (deletions *SessionDeletions) SetPublishFaults(faults persist.PublishFaults) {
	deletions.mu.Lock()
	defer deletions.mu.Unlock()
	deletions.publishFaults = faults
}

func (deletions *SessionDeletions) List() ([]sessionDeletion, error) {
	deletions.mu.Lock()
	defer deletions.mu.Unlock()
	return deletions.load()
}

func (deletions *SessionDeletions) Request(projectID, sessionID, agentBinding string) error {
	record := sessionDeletion{ProjectID: projectID, SessionID: sessionID, AgentBinding: agentBinding, Phase: deletionRequested}
	if err := validateSessionDeletion(record); err != nil {
		return err
	}
	deletions.mu.Lock()
	defer deletions.mu.Unlock()
	records, err := deletions.load()
	if err != nil {
		return err
	}
	for _, existing := range records {
		if existing.SessionID != sessionID {
			continue
		}
		if existing.ProjectID != projectID || existing.AgentBinding != agentBinding {
			return fmt.Errorf("session deletion belongs to another project or agent")
		}
		return nil
	}
	return deletions.save(append(records, record))
}

func (deletions *SessionDeletions) Confirm(projectID, sessionID string) error {
	deletions.mu.Lock()
	defer deletions.mu.Unlock()
	records, err := deletions.load()
	if err != nil {
		return err
	}
	for index := range records {
		if records[index].ProjectID != projectID || records[index].SessionID != sessionID {
			continue
		}
		if records[index].Phase == deletionConfirmed {
			return nil
		}
		records[index].Phase = deletionConfirmed
		return deletions.save(records)
	}
	return fmt.Errorf("session deletion request is missing")
}

func (deletions *SessionDeletions) Forget(projectID, sessionID string) error {
	deletions.mu.Lock()
	defer deletions.mu.Unlock()
	records, err := deletions.load()
	if err != nil {
		return err
	}
	filtered := records[:0]
	for _, record := range records {
		if record.ProjectID != projectID || record.SessionID != sessionID {
			filtered = append(filtered, record)
		}
	}
	if len(filtered) == len(records) {
		return nil
	}
	return deletions.save(filtered)
}

func (deletions *SessionDeletions) load() ([]sessionDeletion, error) {
	name := filepath.Join(deletions.store.Dir, "pi-session-deletions.json")
	raw, _, err := persist.ReadFile(name)
	if os.IsNotExist(err) {
		if _, _, backupErr := persist.ReadFile(name + ".bak"); os.IsNotExist(backupErr) {
			return []sessionDeletion{}, nil
		}
		return nil, fmt.Errorf("session deletion journal is missing while a backup remains")
	}
	if err != nil {
		return nil, fmt.Errorf("session deletion journal is unreadable")
	}
	var value storedSessionDeletions
	if persist.Decode(raw, &value, validateStoredSessionDeletions) != nil {
		return nil, fmt.Errorf("session deletion journal is unreadable")
	}
	return value.Records, nil
}

func (deletions *SessionDeletions) save(records []sessionDeletion) error {
	if records == nil {
		records = []sessionDeletion{}
	}
	outcome, err := persist.WriteWithOutcome(deletions.store, "pi-session-deletions.json", storedSessionDeletions{Version: 1, Engine: "pi", Records: records}, validateStoredSessionDeletions, deletions.publishFaults)
	decision := DecideDeletionPublish(outcome)
	if decision.MayDispatch {
		return nil
	}
	if outcome.Kind == persist.OutcomeDurabilityUncertain {
		// The candidate tombstone stays visible. Reconcile the validated
		// primary without resurrecting an older backup, and keep the delete
		// blocked from replay against a new endpoint until resolved.
		if _, reconcileErr := ReconcileDeletionAfterPublish(deletions.store, outcome); reconcileErr != nil {
			return fmt.Errorf("uncertain deletion publish remains unresolved: %w", errors.Join(err, reconcileErr))
		}
		return err
	}
	return err
}

func validateStoredSessionDeletions(value storedSessionDeletions) error {
	if value.Version != 1 || value.Engine != "pi" || value.Records == nil {
		return fmt.Errorf("invalid session deletion journal")
	}
	seen := make(map[string]bool, len(value.Records))
	for _, record := range value.Records {
		if err := validateSessionDeletion(record); err != nil || seen[record.SessionID] {
			return fmt.Errorf("invalid session deletion journal")
		}
		seen[record.SessionID] = true
	}
	return nil
}

func validateSessionDeletion(record sessionDeletion) error {
	// Match the existing project-session record contract. Pi session IDs are
	// opaque and may legitimately contain slashes or exceed path-key limits.
	if record.ProjectID == "" || validatePiSessionID(record.SessionID) != nil || containsNUL(record.ProjectID) {
		return fmt.Errorf("invalid session deletion target")
	}
	if !validDeletionAgentBinding(record.AgentBinding) {
		return fmt.Errorf("invalid deletion agent binding")
	}
	if record.Phase != deletionRequested && record.Phase != deletionConfirmed {
		return fmt.Errorf("invalid deletion phase")
	}
	return nil
}

func stableDeletionAgentIdentity(identity string) bool {
	return strings.HasPrefix(identity, "pi:") && len(identity) > 3
}

// deletionBindingV2Prefix labels the versioned deletion binding that binds one
// requested deletion record to the durable pairing plus the exact session. A
// legacy agent binding is a bare "sha256:<hex>" digest, so carrying the
// DeletionBindingV2 digest under this prefix keeps the paired form
// distinguishable in the journal without changing the journal schema/version.
const deletionBindingV2Prefix = "deletion-binding-v2:"

// PairedDeletionBinding returns the versioned binding persisted with a
// requested deletion record while a durable pairing is active. It is the
// existing DeletionBindingV2 digest shape carried under deletionBindingV2Prefix
// so paired recovery can tell a record-level v2 binding from a legacy agent
// digest and verify the exact paired session.
func PairedDeletionBinding(pairing persist.PairingAuthority, sessionID string) (string, error) {
	digest, err := DeletionBindingV2(pairing, sessionID)
	if err != nil {
		return "", err
	}
	return deletionBindingV2Prefix + digest, nil
}

// deletionBindingDigest returns the sha256 digest a stored binding carries,
// accepting both the versioned v2 form and the bare legacy shape.
func deletionBindingDigest(binding string) string {
	return strings.TrimPrefix(binding, deletionBindingV2Prefix)
}

func validDeletionAgentBinding(binding string) bool {
	const prefix = "sha256:"
	digest := deletionBindingDigest(binding)
	if len(digest) != len(prefix)+64 || !strings.HasPrefix(digest, prefix) {
		return false
	}
	for _, character := range digest[len(prefix):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
