package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/miloszkolber/pixie/internal/identifier"
	piwire "github.com/miloszkolber/pixie/internal/piprotocol"
)

func (m *SessionManager) Fork(ctx context.Context, projectID, sessionID, cwd string) (SessionSummary, error) {
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
	response, err := m.client.ForkSession(ctx, piwire.LoadSessionRequest{SessionId: piwire.SessionId(sessionID), Cwd: admitted, McpServers: servers})
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
		return SessionSummary{}, fmt.Errorf("Pi agent returned an existing session identifier for a fork")
	}
	records, err := m.records.List()
	if err != nil {
		return SessionSummary{}, err
	}
	for _, record := range records {
		if record.SessionID == childID {
			return SessionSummary{}, fmt.Errorf("Pi agent returned an existing session identifier for a fork")
		}
	}
	_, err = m.client.Ready(ctx)
	if err != nil {
		return SessionSummary{}, err
	}
	child := newSessionEntry(childID, projectID, admitted, sessionID, token)
	child.agentIdentity = agentProfileIdentity(profile, generation)
	child.configOptions = arrayValue(value["configOptions"])
	child.thinkingLevel = thinkingFromOptions(child.configOptions)
	child.model = modelFromSetup(child.configOptions, response.Meta)
	child.capabilities = response.Capabilities
	// The agent creates the child, but this controller has not replayed its
	// inherited transcript yet. The first read or prompt must load it from the agent.
	if err := m.records.Record(ProjectSessionRecord{ProjectID: projectID, SessionID: childID, CWD: admitted, ParentSessionID: sessionID}); err != nil {
		return SessionSummary{}, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return SessionSummary{}, fmt.Errorf("session manager has been shut down")
	}
	m.sessions[childID] = child
	m.mu.Unlock()
	summary := m.summary(childID, child)
	m.emit("session.lifecycleChanged", map[string]any{"projectId": projectID, "sessionId": childID, "operation": "forked"})
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
	if _, err := m.client.CallPi(entry.context(ctx), "pi.session.rename", map[string]any{"sessionId": sessionID, "title": title}); err != nil {
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
	if err := m.attachLocked(ctx, sessionID, entry); err != nil {
		return err
	}
	if _, err := m.client.CallPi(entry.context(ctx), "pi.session.archive", map[string]any{"sessionId": sessionID}); err != nil {
		return err
	}

	m.cancelQuestions(sessionID)
	m.mu.Lock()
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	m.history.Forget(sessionID)
	m.emit("session.lifecycleChanged", map[string]any{"projectId": projectID, "sessionId": sessionID, "operation": "archived"})
	return nil
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
	if _, err := m.client.CallPi(ctx, "pi.session.unarchive", map[string]any{"sessionId": sessionID}); err != nil {
		return err
	}
	m.emit("session.lifecycleChanged", map[string]any{"projectId": projectID, "sessionId": sessionID, "operation": "unarchived"})
	return nil
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
	if err := m.attachLocked(ctx, sessionID, entry); err != nil {
		return err
	}
	agentBinding, err := m.client.deletionAgentBinding(agentProfileIdentity(profile, generation))
	if err != nil {
		return err
	}
	if m.deletions == nil {
		return fmt.Errorf("session deletion journal is not configured")
	}
	if err := m.deletions.Request(projectID, sessionID, agentBinding); err != nil {
		// A failed write may still have replaced the journal before directory
		// sync failed. Do not guess whether there is a durable deletion intent.
		finishLifecycle = false
		return fmt.Errorf("session deletion could not be recorded; restart Pixie to reconcile it: %w", err)
	}
	if err := m.client.DeleteSession(entry.context(ctx), sessionID); err != nil && !agentSessionMissing(err) {
		// Once dispatched, no Pi error can prove the agent did not commit before
		// replying. Keep the marker and reservation for restart reconciliation.
		finishLifecycle = false
		return fmt.Errorf("session deletion outcome is uncertain; restart Pixie to reconcile it: %w", err)
	}
	confirmErr := m.deletions.Confirm(projectID, sessionID)
	if confirmErr != nil {
		confirmErr = fmt.Errorf("confirm session deletion: %w", confirmErr)
	}

	m.cancelQuestions(sessionID)
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

func (m *SessionManager) recoverDeletions(ctx context.Context) error {
	if m.deletions == nil {
		return nil
	}
	records, err := m.deletions.List()
	if err != nil {
		return err
	}
	var recovery []error
	for _, record := range records {
		if record.Phase == deletionRequested {
			if m.client == nil {
				recovery = append(recovery, fmt.Errorf("resume deletion of session %s: Pi client is not configured", record.SessionID))
				continue
			}
			generation, profile, profileErr := m.client.Profile(ctx)
			if profileErr != nil {
				recovery = append(recovery, fmt.Errorf("resume deletion of session %s: %w", record.SessionID, profileErr))
				continue
			}
			binding, bindingErr := m.client.deletionAgentBinding(agentProfileIdentity(profile, generation))
			if bindingErr != nil {
				recovery = append(recovery, fmt.Errorf("resume deletion of session %s: %w", record.SessionID, bindingErr))
				continue
			}
			if binding != record.AgentBinding {
				recovery = append(recovery, fmt.Errorf("resume deletion of session %s: connected Pi agent binding changed (identity, endpoint, or configuration)", record.SessionID))
				continue
			}
			if !profile.Operations.DeleteSession {
				recovery = append(recovery, fmt.Errorf("resume deletion of session %s: %w", record.SessionID, unsupportedAgentCapability("session.delete")))
				continue
			}
			deleteContext := context.WithValue(ctx, connectionGenerationKey{}, generation)
			if deleteErr := m.client.DeleteSession(deleteContext, record.SessionID); deleteErr != nil && !agentSessionMissing(deleteErr) {
				recovery = append(recovery, fmt.Errorf("resume deletion of session %s: %w", record.SessionID, deleteErr))
				continue
			}
			if confirmErr := m.deletions.Confirm(record.ProjectID, record.SessionID); confirmErr != nil {
				recovery = append(recovery, fmt.Errorf("confirm deletion of session %s: %w", record.SessionID, confirmErr))
				continue
			}
		}
		if cleanupErr := m.cleanupSessionDeletion(record.ProjectID, record.SessionID); cleanupErr != nil {
			recovery = append(recovery, fmt.Errorf("resume deletion of session %s: %w", record.SessionID, cleanupErr))
			continue
		}
		if forgetErr := m.deletions.Forget(record.ProjectID, record.SessionID); forgetErr != nil {
			recovery = append(recovery, fmt.Errorf("finish deletion of session %s: %w", record.SessionID, forgetErr))
		}
	}
	return errors.Join(recovery...)
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
	scale := []string{"off", "minimal", "low", "medium", "high", "xhigh"}
	requestedIndex := stringIndex(scale, requested)
	if requestedIndex < 0 {
		requestedIndex = 0
	}
	closest := values[0]
	for _, value := range values[1:] {
		if absolute(stringIndex(scale, value)-requestedIndex) < absolute(stringIndex(scale, closest)-requestedIndex) {
			closest = value
		}
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

func (m *SessionManager) info(ctx context.Context, sessionID string) (remoteSession, error) {
	response, err := m.client.CallPi(ctx, "pi.session.info", map[string]any{"sessionId": sessionID})
	if err != nil {
		return remoteSession{}, err
	}
	var value map[string]any
	if json.Unmarshal(response, &value) != nil {
		return remoteSession{}, fmt.Errorf("Pi session info is invalid")
	}
	session := mapValue(value["session"])
	if session["sessionId"] == nil {
		session["sessionId"] = sessionID
	}
	meta := mapValue(session["_meta"])
	archived := session["archived"] == true || meta["archivedAt"] != nil || session["archivedAt"] != nil
	title := textValue(session["title"])
	if title == "" {
		title = "Chat"
	}
	updated := timeNowMillis()
	if parsed, err := parseTimestamp(textValue(session["updatedAt"])); err == nil {
		updated = parsed
	}
	return remoteSession{title: title, updatedAt: updated, messageCount: int(integerValue(firstNonNil(session["messageCount"], meta["messageCount"]))), archived: archived}, nil
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
func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
func timeNowMillis() int64 { return time.Now().UnixMilli() }
func parseTimestamp(value string) (int64, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return parsed.UnixMilli(), err
}
