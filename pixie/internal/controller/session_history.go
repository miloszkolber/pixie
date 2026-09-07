package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
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
	result := map[string]any{
		"kind":         "snapshot",
		"summary":      m.summaryLocked(sessionID, entry),
		"messages":     resultMessages,
		"pendingTools": pendingToolPreviewsLocked(entry),
		"commands":     cloneSlashCommands(entry.commands),
		"modes":        cloneSessionModes(entry.modes),
		"planState":    cloneSessionPlan(entry.planState),
		"page":         page,
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
// passed through unchanged, including images and MCP App metadata.
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
