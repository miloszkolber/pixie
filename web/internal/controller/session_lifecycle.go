package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/miloszkolber/pixie/internal/identifier"
	"github.com/miloszkolber/pixie/internal/persist"
	piwire "github.com/miloszkolber/pixie/shared/piprotocol"
)

func (m *SessionManager) Fork(ctx context.Context, projectID, sessionID, cwd string) (SessionSummary, error) {
	return m.branchOrFork(ctx, projectID, sessionID, cwd, "")
}

// Branch implements "edit from here": an in-file sibling of entryID in the same
// native session file. The host serves this as session.fork plus an optional
// entryId (assistant/src/host.ts branchSession), so the request forwards the
// entry and never falls back to a new-file fork. The native session header keeps
// the true parent link; the controller deliberately adds no custom branch
// schema and records no fabricated parent link, so forks and in-file branches
// are roots in the controller's ancestry and only subagent sessions nest.
func (m *SessionManager) Branch(ctx context.Context, projectID, sessionID, cwd, entryID string) (SessionSummary, error) {
	entryID = strings.TrimSpace(entryID)
	if entryID == "" || containsNUL(entryID) {
		return SessionSummary{}, fmt.Errorf("session branch entry is invalid")
	}
	return m.branchOrFork(ctx, projectID, sessionID, cwd, entryID)
}

// branchOrFork performs the shared fork/branch lifecycle. An empty entryID is a
// new independent fork; a non-empty entryID is an in-file sibling branch and is
// dispatched with the entry in the host request. A host that does not implement
// session.fork fails closed through the negotiated operation set, and a host
// that ignores the entry cannot be silently mistaken for an edit because the
// same operation is explicit in the request.
func (m *SessionManager) branchOrFork(ctx context.Context, projectID, sessionID, cwd, entryID string) (SessionSummary, error) {
	admitted, err := m.projects.AssertCWD(projectID, cwd)
	if err != nil {
		return SessionSummary{}, err
	}
	entry, err := m.queueEntry(sessionID)
	if err != nil {
		return SessionSummary{}, err
	}
	defer m.releaseEntry(entry)
	if entry.projectID != projectID || entry.cwd != admitted {
		return SessionSummary{}, fmt.Errorf("unknown session: %s", sessionID)
	}
	if err := m.lockEntry(sessionID, entry); err != nil {
		return SessionSummary{}, err
	}
	defer entry.op.Unlock()
	finish, err := m.beginLifecycle(sessionID, entry)
	if err != nil {
		return SessionSummary{}, err
	}
	defer finish()
	if err := m.attachLocked(ctx, sessionID, entry); err != nil {
		return SessionSummary{}, err
	}
	ctx = entry.context(ctx)
	generation, profile, err := m.client.Profile(ctx)
	if err != nil {
		return SessionSummary{}, err
	}
	token := identifier.New()
	servers := make([]piwire.McpServer, 0)
	for _, server := range m.objectiveServers(profile, token) {
		http := piwire.McpServerHttpInline(*server.Http)
		servers = append(servers, piwire.McpServer{Http: &http})
	}
	var response piwire.NewSessionResponse
	if entryID == "" {
		response, err = m.client.ForkSession(ctx, piwire.LoadSessionRequest{SessionId: piwire.SessionId(sessionID), Cwd: admitted, McpServers: servers})
	} else {
		params := map[string]any{"sessionId": sessionID, "cwd": admitted, "entryId": entryID}
		if len(servers) > 0 {
			params["mcpServers"] = servers
		}
		var raw json.RawMessage
		raw, err = m.client.CallPi(ctx, "session.fork", params)
		if err == nil {
			err = json.Unmarshal(raw, &response)
		}
	}
	if err != nil {
		return SessionSummary{}, err
	}
	value := objectValue(response)
	childID := textValue(value["sessionId"])
	if childID == "" || childID == sessionID {
		return SessionSummary{}, fmt.Errorf("Pi agent returned an invalid session identifier for a fork")
	}
	m.mu.Lock()
	_, exists := m.sessions[childID]
	m.mu.Unlock()
	if exists {
		return SessionSummary{}, &sessionRegistrationError{sessionID: childID, detail: "Pi agent returned an existing session identifier"}
	}
	canvasAttached := false
	childCommitted := false
	defer func() {
		if childCommitted || !canvasAttached {
			return
		}
		m.revokeNativeMCPSession(childID)
	}()
	records, err := m.records.List()
	if err != nil {
		return SessionSummary{}, err
	}
	for _, record := range records {
		if record.SessionID == childID {
			return SessionSummary{}, &sessionRegistrationError{sessionID: childID, detail: "Pi agent returned an already recorded session identifier"}
		}
	}
	_, err = m.client.Ready(ctx)
	if err != nil {
		return SessionSummary{}, err
	}
	child := newSessionEntry(childID, projectID, admitted, "", token)
	child.agentIdentity = agentProfileIdentity(profile, generation)
	child.configOptions = arrayValue(value["configOptions"])
	child.thinkingLevel = thinkingFromOptions(child.configOptions)
	child.model = modelFromSetup(child.configOptions, response.Meta)
	child.capabilities = response.Capabilities
	canvasAttached = m.attachNativeCanvas(ctx, profile, childID, token, generation)
	if canvasAttached {
		child.canvasAttached = generation
	}
	// The agent creates the child, but this controller has not replayed its
	// transcript yet. The first read or prompt must load it from the agent. No
	// controller parent link is recorded: the native session header is the
	// branch/fork parent, and only subagent sessions nest in Pixie.
	if err := m.records.Record(ProjectSessionRecord{ProjectID: projectID, SessionID: childID, CWD: admitted}); err != nil {
		return SessionSummary{}, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return SessionSummary{}, fmt.Errorf("session manager has been shut down")
	}
	m.sessions[childID] = child
	m.mu.Unlock()
	childCommitted = true
	summary := m.summary(childID, child)
	operation := "forked"
	if entryID != "" {
		operation = "branched"
	}
	m.emit("session.lifecycleChanged", map[string]any{"projectId": projectID, "sessionId": childID, "operation": operation})
	return summary, nil
}

func (m *SessionManager) Rename(ctx context.Context, projectID, sessionID, cwd, title string) error {
	title = strings.TrimSpace(title)
	if title == "" || strings.ContainsRune(title, 0) || utf16Length(title) > 200 {
		return fmt.Errorf("session title is invalid")
	}
	entry, err := m.EnsureAttached(ctx, sessionID, projectID, cwd)
	if err != nil {
		return err
	}
	defer m.releaseEntry(entry)
	if err := m.lockEntry(sessionID, entry); err != nil {
		return err
	}
	defer entry.op.Unlock()
	if _, err := m.client.CallPi(entry.context(ctx), "session.rename", map[string]any{"sessionId": sessionID, "title": title}); err != nil {
		return err
	}
	if err := m.records.SetTitle(projectID, sessionID, title); err != nil {
		return fmt.Errorf("persist session title: %w", err)
	}
	entry.state.Lock()
	entry.title = title
	entry.state.Unlock()
	m.history.Forget(sessionID)
	m.emit("session.lifecycleChanged", map[string]any{"projectId": projectID, "sessionId": sessionID, "operation": "renamed", "title": title})
	return nil
}

// errSessionArchiveUnavailable fails archive/unarchive closed.
//
// Archive is controller-owned state: the catalog marks session.archive,
// session.unarchive, pi.session.archive and pi.session.unarchive "absent", and
// the Bun host negotiates none of them. This controller does not yet persist an
// archive marker, so delegating to a native route could only surface a host
// success a legacy fixture invented. Failing closed here keeps a caller from
// observing a false success; it never mutates the resident projection, revokes
// MCP credentials or emits an archived lifecycle event.
var errSessionArchiveUnavailable = errors.New("session archive is controller-owned state; this controller does not implement it yet")

func (m *SessionManager) Archive(ctx context.Context, projectID, sessionID, cwd string) error {
	admitted, err := m.projects.AssertCWD(projectID, cwd)
	if err != nil {
		return err
	}
	entry, err := m.queueEntry(sessionID)
	if err != nil {
		return err
	}
	defer m.releaseEntry(entry)
	if entry.projectID != projectID || entry.cwd != admitted {
		return fmt.Errorf("unknown session: %s", sessionID)
	}
	if err := m.lockEntry(sessionID, entry); err != nil {
		return err
	}
	defer entry.op.Unlock()
	entry.state.Lock()
	queued := queuedFollowUpCount(entry.queue) > 0
	entry.state.Unlock()
	if queued {
		return fmt.Errorf("remove or finish queued follow-ups before archiving the chat")
	}
	finish, err := m.beginLifecycle(sessionID, entry)
	if err != nil {
		return err
	}
	defer finish()
	// The operation is rejected before any controller mutation: no native MCP
	// revocation, no resident removal, no dialog dismissal and no archived
	// event. The host routes stay unused because the real Bun host exposes
	// none of them.
	return errSessionArchiveUnavailable
}

func (m *SessionManager) Unarchive(ctx context.Context, projectID, sessionID string) error {
	finish, err := m.beginLifecycle(sessionID, nil)
	if err != nil {
		return err
	}
	defer finish()
	records, err := m.records.List()
	if err != nil {
		return err
	}
	found := false
	for _, record := range records {
		if record.ProjectID == projectID && record.SessionID == sessionID {
			if _, err := m.projects.AssertCWD(projectID, record.CWD); err != nil {
				return err
			}
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("unknown session: %s", sessionID)
	}
	// Restore is controller-owned state too; no host route exists and no
	// archived event is emitted without a durable archive marker to clear.
	return errSessionArchiveUnavailable
}

func (m *SessionManager) Delete(ctx context.Context, projectID, sessionID, cwd string) error {
	admitted, err := m.projects.AssertCWD(projectID, cwd)
	if err != nil {
		return err
	}
	entry, err := m.queueEntry(sessionID)
	if err != nil {
		return err
	}
	defer m.releaseEntry(entry)
	if entry.projectID != projectID || entry.cwd != admitted {
		return fmt.Errorf("unknown session: %s", sessionID)
	}
	if err := m.lockEntry(sessionID, entry); err != nil {
		return err
	}
	defer entry.op.Unlock()
	finish, err := m.beginLifecycle(sessionID, entry)
	if err != nil {
		return err
	}
	finishLifecycle := true
	defer func() {
		if finishLifecycle {
			finish()
		}
	}()
	// AUX-27: admission precedes authority revocation. Validate the host
	// profile and pin its generation, confirm the session can be attached, and
	// persist the durable deletion intent before any native MCP authority is
	// revoked. A profile, capability, attachment, binding or journal failure
	// therefore leaves the session's existing native credentials intact, and
	// only a recorded intent justifies revocation.
	generation, profile, err := m.client.Profile(ctx)
	if err != nil {
		return err
	}
	if !profile.Operations.DeleteSession {
		return unsupportedAgentCapability("session.delete")
	}
	// Pin attachment and deletion to the capability profile checked above. This
	// also avoids replaying an unsupported session merely to reject deletion.
	ctx = context.WithValue(ctx, connectionGenerationKey{}, generation)
	if err := m.attachLockedWithoutCanvas(ctx, sessionID, entry); err != nil {
		return err
	}
	if m.deletions == nil {
		return fmt.Errorf("session deletion journal is not configured")
	}
	agentBinding, err := m.deletionBindingForRequest(sessionID, agentProfileIdentity(profile, generation))
	if err != nil {
		return err
	}
	if err := m.deletions.Request(projectID, sessionID, agentBinding); err != nil {
		// A failed write may still have replaced the journal before directory
		// sync failed. Do not guess whether there is a durable deletion intent.
		finishLifecycle = false
		return fmt.Errorf("session deletion could not be recorded; restart Pixie to reconcile it: %w", err)
	}
	// Durable admission is established: revoke native authority, then dispatch.
	// The record (or a confirmed native delete) is the only justification for
	// losing credentials, so a stale revocation cannot outlive a rejected
	// request.
	m.revokeNativeMCPSession(sessionID)
	if err := m.client.DeleteSession(entry.context(ctx), sessionID); err != nil && !agentSessionMissing(err) {
		// Once dispatched, no Pi error can prove the agent did not commit before
		// replying. Keep the marker and reservation for restart reconciliation.
		finishLifecycle = false
		return fmt.Errorf("session deletion outcome is uncertain; restart Pixie to reconcile it: %w", err)
	}
	if canvasErr := m.cleanupNativeMCPSession(sessionID); canvasErr != nil {
		// Native deletion is confirmed, but owned Canvas cleanup is durable work
		// that must remain retryable through the deletion journal.
		finishLifecycle = false
		return fmt.Errorf("clean up Canvas for deleted session: %w", canvasErr)
	}
	confirmErr := m.deletions.Confirm(projectID, sessionID)
	if confirmErr != nil {
		confirmErr = fmt.Errorf("confirm session deletion: %w", confirmErr)
	}

	// Deletion dismisses blocked UI like a stop: the host settles its
	// awaiting call on close, and browsers drop the modal.
	m.cancelDialogs(sessionID)
	m.mu.Lock()
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	m.history.Forget(sessionID)
	cleanupErr := m.cleanupSessionDeletion(projectID, sessionID)
	var journalErr error
	if confirmErr == nil && cleanupErr == nil {
		journalErr = m.deletions.Forget(projectID, sessionID)
		if journalErr != nil {
			journalErr = fmt.Errorf("finish session deletion: %w", journalErr)
		}
	}
	m.emit("session.deleted", map[string]any{"projectId": projectID, "sessionId": sessionID})
	deletionErr := errors.Join(confirmErr, cleanupErr, journalErr)
	if deletionErr != nil {
		finishLifecycle = false
	}
	return deletionErr
}

func (m *SessionManager) cleanupSessionDeletion(projectID, sessionID string) error {
	var cleanup []error
	if m.records != nil {
		if err := m.records.Forget(projectID, sessionID); err != nil {
			cleanup = append(cleanup, fmt.Errorf("remove session association: %w", err))
		}
	}
	if m.objectives != nil {
		if err := m.objectives.Forget(projectID, sessionID); err != nil {
			cleanup = append(cleanup, fmt.Errorf("remove session objective: %w", err))
		}
	}
	if m.queues != nil {
		if err := m.queues.Forget(projectID, sessionID); err != nil {
			cleanup = append(cleanup, fmt.Errorf("remove session queue: %w", err))
		}
	}
	return errors.Join(cleanup...)
}

// deletionBindingForRequest returns the binding persisted with a newly requested
// deletion record. An active durable pairing binds the record to that pairing
// and this exact session; otherwise today's legacy connected-agent binding is
// retained. A v2 binding is never produced without an active paired authority.
func (m *SessionManager) deletionBindingForRequest(sessionID, agentIdentity string) (string, error) {
	pairing, found, err := InspectPairingAuthority(m.deletions.store)
	if err != nil {
		// Recovery already fails closed on an unreadable pairing. Keep the
		// request on the legacy path instead of synthesizing a v2 binding from
		// a record that could not be validated; the active recovery mode still
		// quarantines the resulting record rather than replaying it.
		return m.client.deletionAgentBinding(agentIdentity)
	}
	if found && pairing.Status == persist.PairingStatusPaired {
		return PairedDeletionBinding(pairing, sessionID)
	}
	return m.client.deletionAgentBinding(agentIdentity)
}

// RecoverDeletions resumes or quarantines retained deletion records. Only an
// unreadable journal fails startup; a record that cannot be matched to the
// connected authority, or whose host does not support deletion, is retained and
// surfaced as recovery-blocked. This is what keeps an ordinary restart (new
// runtime identity, ephemeral port, rotated secret) from aborting boot.
func (m *SessionManager) RecoverDeletions(ctx context.Context) error {
	if m.deletions == nil {
		return nil
	}
	records, err := m.deletions.List()
	if err != nil {
		return err
	}
	quarantine := make(map[string]DeletionRecovery)
	for _, record := range records {
		if reason := m.recoverDeletionRecord(ctx, record); reason != "" {
			quarantine[record.SessionID] = DeletionRecovery{ProjectID: record.ProjectID, SessionID: record.SessionID, Phase: record.Phase, Reason: reason}
		}
	}
	m.mu.Lock()
	m.deletionQuarantine = quarantine
	m.mu.Unlock()
	return nil
}

func (m *SessionManager) recoverDeletionRecord(ctx context.Context, record sessionDeletion) string {
	if record.Phase == deletionRequested {
		if reason := m.admitDeletionRecovery(record); reason != "" {
			return reason
		}
		if m.client == nil {
			return "Pi client is not configured"
		}
		generation, profile, profileErr := m.client.Profile(ctx)
		if profileErr != nil {
			return fmt.Sprintf("agent profile unavailable: %v", profileErr)
		}
		if !profile.Operations.DeleteSession {
			return unsupportedAgentCapability("session.delete").Error()
		}
		if reason := m.authorizeDeletionRecovery(profile, generation, record); reason != "" {
			return reason
		}
		deleteContext := context.WithValue(ctx, connectionGenerationKey{}, generation)
		// The requested record is already durably admitted, so revocation is
		// justified before the replayed dispatch. An uncertain outcome keeps the
		// tombstone and is retried on the next reconciliation.
		m.revokeNativeMCPSession(record.SessionID)
		if deleteErr := m.client.DeleteSession(deleteContext, record.SessionID); deleteErr != nil && !agentSessionMissing(deleteErr) {
			return fmt.Sprintf("delete dispatch outcome is uncertain: %v", deleteErr)
		}
		if confirmErr := m.deletions.Confirm(record.ProjectID, record.SessionID); confirmErr != nil {
			return fmt.Sprintf("confirm deletion: %v", confirmErr)
		}
	}
	if canvasErr := m.cleanupNativeMCPSession(record.SessionID); canvasErr != nil {
		return fmt.Sprintf("clean up Canvas: %v", canvasErr)
	}
	if cleanupErr := m.cleanupSessionDeletion(record.ProjectID, record.SessionID); cleanupErr != nil {
		return fmt.Sprintf("clean up session: %v", cleanupErr)
	}
	if forgetErr := m.deletions.Forget(record.ProjectID, record.SessionID); forgetErr != nil {
		return fmt.Sprintf("finish deletion: %v", forgetErr)
	}
	return ""
}

// admitDeletionRecovery rechecks the exact durable session association before
// a requested tombstone can consult a host authority or dispatch a delete. A
// journal record does not carry a cwd, so a matching persisted association is
// the only source of that filesystem identity. Stored paths must still exist,
// remain non-symlinks and re-admit unchanged under the current project policy.
func (m *SessionManager) admitDeletionRecovery(record sessionDeletion) string {
	if m.records == nil {
		return "persisted session association is unavailable; retain the tombstone"
	}
	records, err := m.records.List()
	if err != nil {
		return fmt.Sprintf("persisted session association is unreadable: %v", err)
	}
	var association *ProjectSessionRecord
	for index := range records {
		candidate := &records[index]
		if candidate.SessionID != record.SessionID {
			continue
		}
		if candidate.ProjectID != record.ProjectID {
			return "persisted session association does not match the deletion project; retain the tombstone"
		}
		if association != nil {
			return "persisted session association is ambiguous; retain the tombstone"
		}
		association = candidate
	}
	if association == nil {
		return "persisted session association is missing; retain the tombstone"
	}
	info, err := os.Lstat(association.CWD)
	if err != nil {
		return fmt.Sprintf("persisted session cwd is unavailable; retain the tombstone: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "persisted session cwd is not an existing non-symlink directory; retain the tombstone"
	}
	if m.projects == nil {
		return "project filesystem admission is unavailable; retain the tombstone"
	}
	admitted, err := m.projects.AssertCWD(record.ProjectID, association.CWD)
	if err != nil {
		return fmt.Sprintf("persisted session cwd cannot be re-admitted; retain the tombstone: %v", err)
	}
	if admitted != association.CWD {
		return "persisted session cwd does not exactly match its admitted directory; retain the tombstone"
	}
	return ""
}

// authorizeDeletionRecovery resolves the configured destructive-recovery
// authority for one requested record. It returns an empty reason only when a
// dispatch is authorized; every other result retains the tombstone and blocks
// replay. The live host identity comes from the authenticated runtime.hello
// handshake that produced this profile, so a successful Profile is itself the
// authenticated-hello signal.
func (m *SessionManager) authorizeDeletionRecovery(profile AgentProfile, generation uint64, record sessionDeletion) string {
	identity := agentProfileIdentity(profile, generation)
	authenticatedHello := profile.Pi
	switch m.deletionAuthority {
	case DeletionAuthorityLegacy:
		return m.legacyDeletionBindingReason(identity, record.AgentBinding)
	case DeletionAuthorityPaired:
		return m.pairedRecoveryReason(identity, record.SessionID, record.AgentBinding, authenticatedHello)
	default:
		// Auto requires pairing whenever a durable pairing record exists; an
		// unreadable pairing fails closed rather than falling back to legacy.
		_, found, err := InspectPairingAuthority(m.deletions.store)
		if err != nil {
			return fmt.Sprintf("paired authority is unreadable and destructive recovery fails closed: %v", err)
		}
		if found {
			return m.pairedRecoveryReason(identity, record.SessionID, record.AgentBinding, authenticatedHello)
		}
		return m.legacyDeletionBindingReason(identity, record.AgentBinding)
	}
}

// pairedRecoveryReason validates the live authenticated host and resolved
// native-storage key against the durable pairing, then binds the record itself
// to the pairing and its exact session. A legacy-only binding (or a binding for
// another authority, storage or session) is quarantined with an actionable
// reason and never dispatched. The returned reason keeps the recovery-blocked
// tombstone.
func (m *SessionManager) pairedRecoveryReason(hostIdentity, sessionID, binding string, authenticatedHello bool) string {
	pairing, err := RequirePairedRecovery(m.deletions.store, hostIdentity, m.pairingStorageKey, authenticatedHello)
	if err != nil {
		return err.Error()
	}
	if err := CheckDeletionBindingV2(pairing, sessionID, deletionBindingDigest(binding)); err != nil {
		return err.Error()
	}
	return ""
}

// legacyDeletionBindingReason preserves the pre-pairing match path, including
// the legacy endpoint-bound binding accepted for the same durable identity.
func (m *SessionManager) legacyDeletionBindingReason(hostIdentity, binding string) string {
	matches, err := m.client.matchesDeletionAgentBinding(hostIdentity, binding)
	if err != nil {
		return err.Error()
	}
	if !matches {
		return "connected Pi agent binding changed; retain for operator reconciliation and never replay against a new endpoint"
	}
	return ""
}

// DeletionRecoveryStatus returns the retained deletion records for operator
// reconciliation. It unions the recovery-blocked quarantine with any pending
// journal tombstones that have not been quarantined yet (for example an
// uncertain dispatch that has not been through restart recovery), so unmatched
// and uncertain records are never invisible. It never dispatches or forgets
// them; only ConfirmExternalDeletion finishes a record and
// RetainExternalDeletion explicitly leaves it in place.
func (m *SessionManager) DeletionRecoveryStatus() []DeletionRecovery {
	reconciliation := m.DeletionReconciliationStatus()
	result := make([]DeletionRecovery, 0, len(reconciliation))
	for _, record := range reconciliation {
		result = append(result, record.DeletionRecovery)
	}
	return result
}

// DeletionReconciliation is one retained tombstone plus operator guidance. It
// embeds the wire-compatible DeletionRecovery so existing
// session.deletionRecovery callers keep working; Remediation never changes the
// tombstone itself.
type DeletionReconciliation struct {
	DeletionRecovery
	// Remediation is an actionable hint for the confirm/retain decision. It is
	// derived from the stored reason and never clears the tombstone.
	Remediation string `json:"remediation"`
	// Uncertain reports whether the native outcome is unknown (requested phase
	// or an uncertain dispatch) rather than a confirmed local-cleanup backlog.
	Uncertain bool `json:"uncertain"`
}

// DeletionReconciliationStatus projects every retained tombstone: the
// recovery-blocked quarantine plus any pending journal records not yet
// quarantined. Quarantine entries win on session identity so their actionable
// reason is preserved. The projection never dispatches, confirms or forgets.
func (m *SessionManager) DeletionReconciliationStatus() []DeletionReconciliation {
	m.mu.Lock()
	quarantined := make(map[string]DeletionRecovery, len(m.deletionQuarantine))
	for sessionID, record := range m.deletionQuarantine {
		quarantined[sessionID] = record
	}
	m.mu.Unlock()
	bySession := make(map[string]DeletionReconciliation, len(quarantined))
	for sessionID, record := range quarantined {
		bySession[sessionID] = DeletionReconciliation{
			DeletionRecovery: record,
			Remediation:      deletionRemediationHint(record.Reason),
			Uncertain:        record.Phase == deletionRequested,
		}
	}
	if m.deletions != nil {
		if records, err := m.deletions.List(); err == nil {
			for _, pending := range records {
				if _, ok := bySession[pending.SessionID]; ok {
					continue
				}
				reason := "deletion outcome is uncertain; restart Pixie to reconcile it; confirm only after verifying the native session is gone, or retain to keep the tombstone"
				if pending.Phase == deletionConfirmed {
					reason = "deletion is confirmed but local cleanup is still pending; confirm again only after verifying the native session is gone, or retain to keep the tombstone"
				}
				bySession[pending.SessionID] = DeletionReconciliation{
					DeletionRecovery: DeletionRecovery{ProjectID: pending.ProjectID, SessionID: pending.SessionID, Phase: pending.Phase, Reason: reason},
					Remediation:      deletionRemediationHint(reason),
					Uncertain:        pending.Phase == deletionRequested,
				}
			}
		}
		// An unreadable journal fails closed: the quarantine above stays
		// visible instead of clearing tombstones the operator has not reconciled.
	}
	result := make([]DeletionReconciliation, 0, len(bySession))
	for _, record := range bySession {
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SessionID < result[j].SessionID })
	return result
}

// deletionRemediationHint derives the confirm/retain guidance for one retained
// reason. Confirm asserts the native session is already gone and finishes
// local cleanup without another dispatch; retain is an explicit no-op that
// leaves the tombstone in place. Nothing here auto-clears.
func deletionRemediationHint(reason string) string {
	lowered := strings.ToLower(reason)
	switch {
	case strings.Contains(lowered, "binding changed"),
		strings.Contains(lowered, "recovery-blocked"),
		strings.Contains(lowered, "host identity mismatch"):
		return "Native identity changed: confirm only after verifying the native session is gone outside Pixie, or retain to keep the tombstone for a later host."
	case strings.Contains(lowered, "uncertain"),
		strings.Contains(lowered, "restart pixie to reconcile"):
		return "Outcome is uncertain: confirm only after verifying the native session is gone, or retain to keep the tombstone and retry after restart."
	case strings.Contains(lowered, "cleanup") || strings.Contains(lowered, "finish deletion"):
		return "Native deletion is done but local cleanup is pending: confirm again to retry cleanup, or retain to keep the tombstone."
	case strings.Contains(lowered, "capability"), strings.Contains(lowered, "unsupported"):
		return "Connected agent cannot delete: retain the record and retry after restoring a host with session.delete support; confirm only if the native session is already gone."
	default:
		return "Confirm only after verifying the native session is gone, or retain to keep the tombstone in place."
	}
}

// RetainExternalDeletion is the explicit retain reconciliation action. It
// verifies a tombstone exists in the quarantine or the durable journal and
// then leaves it untouched: it never dispatches a delete, confirms, or
// forgets. A missing record is an error so the UI never reports a silent keep.
func (m *SessionManager) RetainExternalDeletion(projectID, sessionID string) error {
	m.mu.Lock()
	quarantined, ok := m.deletionQuarantine[sessionID]
	m.mu.Unlock()
	if ok && quarantined.ProjectID == projectID {
		return nil
	}
	if m.deletions == nil {
		return fmt.Errorf("session deletion journal is not configured")
	}
	records, err := m.deletions.List()
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.ProjectID == projectID && record.SessionID == sessionID {
			return nil
		}
	}
	return fmt.Errorf("session deletion request is missing")
}

// ConfirmExternalDeletion is the operator reconciliation action that asserts
// the native session is already gone. It records the confirmation and finishes
// local cleanup without dispatching another delete.
func (m *SessionManager) ConfirmExternalDeletion(projectID, sessionID string) error {
	if m.deletions == nil {
		return fmt.Errorf("session deletion journal is not configured")
	}
	if err := m.deletions.Confirm(projectID, sessionID); err != nil {
		return err
	}
	m.revokeNativeMCPSession(sessionID)
	if err := m.cleanupNativeMCPSession(sessionID); err != nil {
		return fmt.Errorf("clean up Canvas: %w", err)
	}
	if err := m.cleanupSessionDeletion(projectID, sessionID); err != nil {
		return fmt.Errorf("clean up session: %w", err)
	}
	if err := m.deletions.Forget(projectID, sessionID); err != nil {
		return fmt.Errorf("finish deletion: %w", err)
	}
	m.mu.Lock()
	delete(m.deletionQuarantine, sessionID)
	m.mu.Unlock()
	return nil
}

func agentSessionMissing(err error) bool {
	var requestError *piwire.RequestError
	return errors.As(err, &requestError) && requestError.Code == -32002
}

func (m *SessionManager) ObjectiveOwner(token string) (string, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for sessionID, entry := range m.sessions {
		if entry.objectiveToken == token && !m.lifecycle[sessionID] {
			return entry.projectID, sessionID, true
		}
	}
	return "", "", false
}

func (m *SessionManager) Stats(sessionID string) (SessionStats, error) {
	entry, err := m.entry(sessionID)
	if err != nil {
		return SessionStats{}, err
	}
	defer m.releaseEntry(entry)
	entry.state.Lock()
	defer entry.state.Unlock()
	stats := entry.stats
	stats.Reported = maps.Clone(stats.Reported)
	stats.ContextUsage = maps.Clone(stats.ContextUsage)
	return stats, nil
}

func (m *SessionManager) ClampThinking(sessionID, requested string) (string, error) {
	entry, err := m.entry(sessionID)
	if err != nil {
		return "", err
	}
	defer m.releaseEntry(entry)
	entry.state.Lock()
	defer entry.state.Unlock()
	values := thinkingLevels(entry.configOptions)
	current := "off"
	for _, candidate := range entry.configOptions {
		option := mapValue(candidate)
		if option["id"] != "thinking" {
			continue
		}
		if value := textValue(option["currentValue"]); value != "" {
			current = value
		}
	}
	if len(values) == 0 {
		return current, nil
	}
	for _, value := range values {
		if value == requested {
			return requested, nil
		}
	}
	scale := []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
	requestedIndex := stringIndex(scale, requested)
	if requestedIndex < 0 {
		return current, nil
	}
	if stringIndex(scale, current) < 0 {
		return current, nil
	}
	closest := ""
	closestDistance := 0
	for _, value := range values {
		valueIndex := stringIndex(scale, value)
		if valueIndex < 0 {
			continue
		}
		distance := absolute(valueIndex - requestedIndex)
		if closest == "" || distance < closestDistance {
			closest = value
			closestDistance = distance
		}
	}
	if closest == "" {
		return current, nil
	}
	return closest, nil
}

func (m *SessionManager) ThinkingLevels(sessionID string) ([]string, error) {
	entry, err := m.entry(sessionID)
	if err != nil {
		return nil, err
	}
	defer m.releaseEntry(entry)
	entry.state.Lock()
	defer entry.state.Unlock()
	return thinkingLevels(entry.configOptions), nil
}

func thinkingLevels(options []any) []string {
	values := []string{}
	seen := make(map[string]bool)
	for _, candidate := range options {
		option := mapValue(candidate)
		if option["id"] != "thinking" {
			continue
		}
		for _, raw := range arrayValue(option["options"]) {
			item := mapValue(raw)
			items := []any{item}
			if nested := arrayValue(item["options"]); len(nested) > 0 {
				items = nested
			}
			for _, rawItem := range items {
				value := textValue(mapValue(rawItem)["value"])
				if value != "" && !seen[value] {
					seen[value] = true
					values = append(values, value)
				}
			}
		}
	}
	return values
}

func (m *SessionManager) ListWithFallback(ctx context.Context, projectID string, archived any) ([]SessionSummary, error) {
	return m.List(ctx, projectID, archived)
}

func stringIndex(values []string, wanted string) int {
	for index, value := range values {
		if value == wanted {
			return index
		}
	}
	return -1
}
func absolute(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
func parseTimestamp(value string) (int64, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return parsed.UnixMilli(), err
}

// LIFE-01/X10-X11: explicit idle-runtime release for eligible settled
// residents. Close/Archive/Delete never imply Stop: Archive and Delete require
// an already stopped session through beginLifecycle and never send
// session.cancel themselves. ReleaseIdleRuntime likewise never aborts; it only
// frees residence for a verified idle session while retaining history, draft,
// queue, selection and metadata for later reattachment. Release to TUI stays
// separate.

// IdleReleaseEligible reports whether a resident session may be explicitly
// released. Active, queued, uncertain, scheduled, pinned or lifecycle-busy
// sessions are never eligible.
func (m *SessionManager) IdleReleaseEligible(sessionID string) (bool, string) {
	entry, err := m.entry(sessionID)
	if err != nil {
		return false, err.Error()
	}
	defer m.releaseEntry(entry)
	if err := m.lockEntry(sessionID, entry); err != nil {
		return false, err.Error()
	}
	defer entry.op.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.sessions[sessionID] != entry || m.lifecycle[sessionID] {
		return false, "wait for the chat lifecycle operation to finish"
	}
	if entry.refs > 1 {
		return false, "session is busy"
	}
	if m.hasActiveLivenessLocked(sessionID) {
		return false, "extension work still pins the session"
	}
	for key := range m.dialogs {
		if key.sessionID == sessionID {
			return false, "dialog still awaiting input"
		}
	}
	entry.state.Lock()
	defer entry.state.Unlock()
	return idleStateEligibleLocked(entry)
}

// ReleaseIdleRuntime frees residence for a verified idle session while
// retaining its durable history, queue, selection and metadata. The next
// read or prompt reloads the authoritative transcript from Pi.
func (m *SessionManager) ReleaseIdleRuntime(ctx context.Context, sessionID string) error {
	entry, err := m.entry(sessionID)
	if err != nil {
		return err
	}
	defer m.releaseEntry(entry)
	if err := m.lockEntryContext(ctx, sessionID, entry); err != nil {
		return err
	}
	defer entry.op.Unlock()
	m.mu.Lock()
	if m.closed || m.sessions[sessionID] != entry || m.lifecycle[sessionID] {
		m.mu.Unlock()
		return fmt.Errorf("wait for the chat lifecycle operation to finish")
	}
	if entry.refs > 1 {
		m.mu.Unlock()
		return fmt.Errorf("session is busy")
	}
	if m.hasActiveLivenessLocked(sessionID) {
		m.mu.Unlock()
		return fmt.Errorf("extension work still pins the session")
	}
	for key := range m.dialogs {
		if key.sessionID == sessionID {
			m.mu.Unlock()
			return fmt.Errorf("dialog still awaiting input")
		}
	}
	entry.state.Lock()
	eligible, reason := idleStateEligibleLocked(entry)
	if !eligible {
		entry.state.Unlock()
		m.mu.Unlock()
		return fmt.Errorf("session is not idle: %s", reason)
	}
	if m.lifecycle == nil {
		m.lifecycle = make(map[string]bool)
	}
	m.lifecycle[sessionID] = true
	entry.state.Unlock()
	m.mu.Unlock()
	// Host release repeats exact native identity and quiescence checks. The
	// controller entry remains authoritative until that operation succeeds, so
	// a timeout/rejection cannot silently claim resident capacity.
	if err := m.client.ReleaseSession(ctx, sessionID, entry.cwd); err != nil {
		m.mu.Lock()
		delete(m.lifecycle, sessionID)
		m.mu.Unlock()
		return err
	}
	m.mu.Lock()
	entry.state.Lock()
	if m.closed || m.sessions[sessionID] != entry {
		entry.state.Unlock()
		delete(m.lifecycle, sessionID)
		m.mu.Unlock()
		return fmt.Errorf("session changed while releasing its native runtime")
	}
	// Invalidate the resident generation before dropping the projection. Late
	// native callbacks must not revive a runtime that the user explicitly
	// released; the durable association and transcript remain available for a
	// later load.
	entry.attached = 0
	entry.promptGeneration++
	delete(m.sessions, sessionID)
	delete(m.lifecycle, sessionID)
	entry.state.Unlock()
	m.mu.Unlock()
	m.revokeNativeMCPSession(sessionID)
	m.emit("session.lifecycleChanged", map[string]any{"sessionId": sessionID, "operation": "idle-released"})
	return nil
}
