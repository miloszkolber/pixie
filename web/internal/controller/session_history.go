package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/miloszkolber/pixie/internal/diagnostics"
)

const (
	transcriptPageMaxMessages = 100
	transcriptPageSoftBytes   = 2 * 1024 * 1024
)

type transcriptBefore struct {
	ProjectionID string `json:"projectionId"`
	Index        int    `json:"index"`
}

type transcriptPageRequest struct {
	Before *transcriptBefore `json:"before"`
}

type transcriptPage struct {
	ProjectionID string `json:"projectionId"`
	Start        int    `json:"start"`
	Total        int    `json:"total"`
}

func (m *SessionManager) Messages(ctx context.Context, sessionID, projectID, cwd, clientKey string) (map[string]any, error) {
	result, release, err := m.messageSnapshot(ctx, sessionID, projectID, cwd, clientKey, true)
	if release != nil {
		release()
	}
	return result, err
}

// RefreshFromDisk re-reads a resident, non-streaming session from its native
// file after a foreign writer advanced it. The host takes an advisory lease
// only for its own mutations, so an external Pi CLI append is invisible to the
// resident projection until the native runtime is released and reattached.
// A streaming or prompt-active session is left untouched: replacing its runtime
// mid-run would interleave two owners. Returns false when no refresh was needed
// or possible.
func (m *SessionManager) RefreshFromDisk(ctx context.Context, sessionID, projectID, cwd string) (bool, error) {
	entry, err := m.entry(sessionID)
	if err != nil {
		return false, err
	}
	defer m.releaseEntry(entry)
	if err := m.lockEntryContext(ctx, sessionID, entry); err != nil {
		return false, err
	}
	defer entry.op.Unlock()
	if entry.projectID != projectID || entry.cwd != cwd {
		return false, fmt.Errorf("unknown session: %s", sessionID)
	}
	m.mu.Lock()
	if m.closed || m.sessions[sessionID] != entry || m.lifecycle[sessionID] {
		m.mu.Unlock()
		return false, fmt.Errorf("wait for the chat lifecycle operation to finish")
	}
	entry.state.Lock()
	streaming := entry.streaming || entry.promptActive
	attached := entry.attached != 0
	entry.state.Unlock()
	m.mu.Unlock()
	if streaming || !attached {
		return false, nil
	}
	// The host release repeats exact native identity and quiescence checks. Only
	// after it succeeds is the stale generation dropped and reattached.
	if err := m.client.ReleaseSession(ctx, sessionID, entry.cwd); err != nil {
		return false, err
	}
	m.revokeNativeMCPSession(sessionID)
	m.mu.Lock()
	entry.state.Lock()
	if m.sessions[sessionID] == entry {
		entry.attached = 0
		entry.promptGeneration++
	}
	entry.state.Unlock()
	m.mu.Unlock()
	if err := m.attachLocked(ctx, sessionID, entry); err != nil {
		return false, err
	}
	return true, nil
}

// SessionSchemaSummary returns the bounded AUX-12 native session-schema
// degradation summary for the most degraded resident session. It reads only the
// header version and record counters; no native text, path or session identity
// is exported. Nil means no resident session carried a schema summary. The
// support-snapshot owner maps this into SupportSnapshotRuntime.SessionSchema.
func (m *SessionManager) SessionSchemaSummary() *diagnostics.SessionSchemaSummary {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result *diagnostics.SessionSchemaSummary
	for _, entry := range m.sessions {
		entry.state.Lock()
		schema := entry.schema
		entry.state.Unlock()
		if schema == nil {
			continue
		}
		candidate := diagnostics.SessionSchemaSummary{
			WrittenByNewerRuntime: booleanValue(schema["writtenByNewerRuntime"]),
			Version:               int(integerValue(schema["version"])),
			UnknownRecords:        int(integerValue(schema["unknownRecords"])),
			InvalidRecords:        int(integerValue(schema["invalidRecords"])),
			RepairedToolCalls:     int(integerValue(schema["repairedToolCalls"])),
		}
		if result == nil {
			result = &candidate
			continue
		}
		// Multiple residents may degrade differently; export the worst case so
		// the snapshot never understates the observed damage.
		result.WrittenByNewerRuntime = result.WrittenByNewerRuntime || candidate.WrittenByNewerRuntime
		result.Version = max(result.Version, candidate.Version)
		result.UnknownRecords = max(result.UnknownRecords, candidate.UnknownRecords)
		result.InvalidRecords = max(result.InvalidRecords, candidate.InvalidRecords)
		result.RepairedToolCalls = max(result.RepairedToolCalls, candidate.RepairedToolCalls)
	}
	return result
}

func booleanValue(value any) bool {
	result, _ := value.(bool)
	return result
}

func (m *SessionManager) messageResponse(ctx context.Context, sessionID, projectID, cwd, clientKey string, request transcriptPageRequest) (any, error) {
	if request.Before != nil {
		return m.olderMessagePage(ctx, sessionID, projectID, cwd, clientKey, request.Before)
	}
	result, release, err := m.messageSnapshot(ctx, sessionID, projectID, cwd, clientKey, false)
	if err != nil {
		return nil, err
	}
	return deferredResponse{result: result, after: release}, nil
}

// messageSnapshot keeps state locked until its WebSocket response is queued.
// A live event therefore falls wholly before or after the authoritative newest
// snapshot. Direct callers receive an owned copy and release immediately.
func (m *SessionManager) messageSnapshot(ctx context.Context, sessionID, projectID, cwd, clientKey string, detach bool) (map[string]any, func(), error) {
	entry, err := m.EnsureAttached(ctx, sessionID, projectID, cwd)
	if err != nil {
		return nil, nil, err
	}
	m.mu.Lock()
	m.retainSessionLocked(clientKey, sessionID, projectID)
	m.mu.Unlock()
	entry.state.Lock()
	messages, page, err := transcriptPageLocked(entry, nil)
	if err != nil {
		entry.state.Unlock()
		m.releaseEntry(entry)
		return nil, nil, err
	}
	var resultMessages any = messages
	if detach {
		resultMessages = cloneJSON(messages)
	}
	pendingDialogs := m.pendingDialogRequests(sessionID)
	result := map[string]any{
		"kind":           "snapshot",
		"summary":        m.summaryLocked(sessionID, entry),
		"messages":       resultMessages,
		"pendingTools":   pendingToolPreviewsLocked(entry),
		"pendingDialogs": pendingDialogs,
		"commands":       cloneSlashCommands(entry.commands),
		"planState":      cloneSessionPlan(entry.planState),
		"page":           page,
	}
	// AUX-12: record native header/record degradation in the session projection
	// so an operator can see why unknown input was dropped. It is read-only and
	// never fed back into a native rewrite.
	if entry.schema != nil {
		result["schema"] = cloneJSON(entry.schema)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			entry.state.Unlock()
			m.releaseEntry(entry)
		})
	}
	return result, release, nil
}

// Older pages end at a prior user-round boundary and are immutable within a
// projection. Copy them under state, then release before WebSocket encoding so
// loading history cannot hold up live text or tool updates.
func (m *SessionManager) olderMessagePage(ctx context.Context, sessionID, projectID, cwd, clientKey string, before *transcriptBefore) (map[string]any, error) {
	entry, err := m.EnsureAttached(ctx, sessionID, projectID, cwd)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.retainSessionLocked(clientKey, sessionID, projectID)
	m.mu.Unlock()
	entry.state.Lock()
	messages, page, err := transcriptPageLocked(entry, before)
	if err == nil {
		messages = cloneJSON(messages).([]any)
	}
	entry.state.Unlock()
	m.releaseEntry(entry)
	if err != nil {
		return nil, err
	}
	return map[string]any{"kind": "page", "messages": messages, "page": page}, nil
}

// The caller holds entry.state. A page starts on a user message whenever one
// exists, so a prepend does not split a tool/activity round. Message values are
// passed through unchanged, including images.
func transcriptPageLocked(entry *sessionEntry, before *transcriptBefore) ([]any, transcriptPage, error) {
	end := len(entry.messages)
	if before != nil {
		if before.ProjectionID == "" || before.Index <= 0 {
			return nil, transcriptPage{}, fmt.Errorf("invalid transcript page")
		}
		if before.ProjectionID != entry.projectionID {
			return nil, transcriptPage{}, &codedError{code: "STALE_TRANSCRIPT_PROJECTION", message: "Chat history changed; reload it before loading earlier messages"}
		}
		if before.Index > len(entry.messages) {
			return nil, transcriptPage{}, fmt.Errorf("invalid transcript page")
		}
		end = before.Index
	}
	start, err := transcriptPageStart(entry.messages, end)
	if err != nil {
		return nil, transcriptPage{}, err
	}
	return entry.messages[start:end], transcriptPage{ProjectionID: entry.projectionID, Start: start, Total: len(entry.messages)}, nil
}

func transcriptPageStart(messages []any, end int) (int, error) {
	start, size := end, 0
	for start > 0 {
		encoded, err := json.Marshal(messages[start-1])
		if err != nil {
			return 0, err
		}
		start--
		size += len(encoded)
		if (end-start >= transcriptPageMaxMessages || size >= transcriptPageSoftBytes) && messageRole(messages[start]) == "user" {
			break
		}
	}
	return start, nil
}

func messageRole(message any) string {
	return textValue(mapValue(message)["role"])
}
