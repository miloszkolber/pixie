package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/miloszkolber/pixie/internal/identifier"
	piwire "github.com/miloszkolber/pixie/internal/piprotocol"
)

type ImageContent struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

const (
	maxTextAttachmentBytes      = 1024 * 1024
	maxTextAttachmentTotalBytes = 2 * 1024 * 1024
	maxTextAttachmentCount      = 4
	maxTextAttachmentNameBytes  = 255
	maxTextAttachmentNameRunes  = 128
)

type TextResourceAttachment struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text"`
}

func (m *SessionManager) Prompt(ctx context.Context, sessionID, text string, images []ImageContent, resources []TextResourceAttachment) error {
	entry, err := m.queueEntry(sessionID)
	if err != nil {
		return err
	}
	defer m.releaseEntry(entry)
	if err := m.lockEntry(sessionID, entry); err != nil {
		return err
	}
	defer entry.op.Unlock()
	if err := m.attachLocked(ctx, sessionID, entry); err != nil {
		return err
	}
	if len(images) > 0 || len(resources) > 0 {
		_, profile, err := m.client.Profile(entry.context(ctx))
		if err != nil {
			return err
		}
		if len(images) > 0 && !profile.Operations.PromptImage {
			return unsupportedAgentCapability("image prompts")
		}
		if len(resources) > 0 && !profile.Operations.PromptEmbeddedContext {
			return unsupportedAgentCapability("text resource prompts")
		}
	}
	entry.state.Lock()
	busy := entry.streaming || entry.promptActive || queuedFollowUpCount(entry.queue) > 0
	entry.state.Unlock()
	if busy {
		return fmt.Errorf("wait for the running chat or resolve its queued follow-ups")
	}
	return m.startPromptLocked(sessionID, entry, text, images, resources, "")
}

func (m *SessionManager) Queue(ctx context.Context, sessionID, text string) error {
	entry, err := m.queueEntry(sessionID)
	if err != nil {
		return err
	}
	defer m.releaseEntry(entry)
	if err := m.lockEntry(sessionID, entry); err != nil {
		return err
	}
	defer entry.op.Unlock()
	text, err = queuedText(text)
	if err != nil {
		return err
	}
	entry.state.Lock()
	identity := queueIdentity(ctx)
	duplicate, err := entry.queue.operation(identity)
	if err != nil {
		entry.state.Unlock()
		return err
	}
	if duplicate {
		m.emitQueueProjection(sessionID, entry, !entry.promptActive)
		entry.state.Unlock()
		return nil
	}
	if queuedFollowUpCount(entry.queue) >= maxQueuedMessages {
		entry.state.Unlock()
		return fmt.Errorf("a chat can queue at most %d messages", maxQueuedMessages)
	}
	item := queuedFollowUp{ID: identifier.New(), Text: text}
	next := entry.queue.clone()
	next.remember(identity)
	next.FollowUp = append(next.FollowUp, item)
	next.Revision = identifier.New()
	if err := m.saveQueueLocked(sessionID, entry, next); err != nil {
		entry.state.Unlock()
		return err
	}
	m.emitQueue(sessionID, entry)
	entry.state.Unlock()
	m.scheduleFollowUp(sessionID, entry)
	return nil
}

func (m *SessionManager) EditQueue(ctx context.Context, sessionID, lane string, index int, text, revision string) error {
	entry, err := m.queueEntry(sessionID)
	if err != nil {
		return err
	}
	defer m.releaseEntry(entry)
	if err := m.lockEntry(sessionID, entry); err != nil {
		return err
	}
	defer entry.op.Unlock()
	entry.state.Lock()
	identity := queueIdentity(ctx)
	duplicate, err := entry.queue.operation(identity)
	if err != nil {
		entry.state.Unlock()
		return err
	}
	if duplicate {
		m.emitQueueProjection(sessionID, entry, !entry.promptActive)
		entry.state.Unlock()
		return nil
	}
	if revision == "" {
		entry.state.Unlock()
		return fmt.Errorf("reload Pixie before editing queued messages")
	}
	if revision != entry.queue.Revision {
		entry.state.Unlock()
		return fmt.Errorf("the queue changed; reopen the queued message before editing it")
	}
	text, err = queuedText(text)
	if err != nil {
		entry.state.Unlock()
		return err
	}
	next := entry.queue.clone()
	switch lane {
	case "steering":
		if index < 0 || index >= len(next.Steering) {
			entry.state.Unlock()
			return fmt.Errorf("queued message not found")
		}
		next.Steering[index] = text
	case "followUp":
		blocked := false
		if next.Dispatch != nil && !entry.promptActive {
			if index == 0 {
				next.Dispatch.Text = text
				blocked = true
			} else {
				index--
			}
		}
		if !blocked && next.Blocked != nil {
			if index == 0 {
				next.Blocked.Text = text
				blocked = true
			} else {
				index--
			}
		}
		if !blocked {
			if index < 0 || index >= len(next.FollowUp) {
				entry.state.Unlock()
				return fmt.Errorf("queued message not found")
			}
			next.FollowUp[index].Text = text
		}
	default:
		entry.state.Unlock()
		return fmt.Errorf("unknown queue lane")
	}
	next.remember(identity)
	next.Revision = identifier.New()
	if err := m.saveQueueLocked(sessionID, entry, next); err != nil {
		entry.state.Unlock()
		return err
	}
	m.emitQueue(sessionID, entry)
	entry.state.Unlock()
	return nil
}

func (m *SessionManager) RemoveQueue(ctx context.Context, sessionID, lane string, index int, revision string) error {
	entry, err := m.queueEntry(sessionID)
	if err != nil {
		return err
	}
	defer m.releaseEntry(entry)
	if err := m.lockEntry(sessionID, entry); err != nil {
		return err
	}
	defer entry.op.Unlock()
	entry.state.Lock()
	identity := queueIdentity(ctx)
	duplicate, err := entry.queue.operation(identity)
	if err != nil {
		entry.state.Unlock()
		return err
	}
	if duplicate {
		m.emitQueueProjection(sessionID, entry, !entry.promptActive)
		entry.state.Unlock()
		return nil
	}
	if revision == "" {
		entry.state.Unlock()
		return fmt.Errorf("reload Pixie before removing queued messages")
	}
	if revision != entry.queue.Revision {
		entry.state.Unlock()
		return fmt.Errorf("the queue changed; refresh the queued message before removing it")
	}
	next := entry.queue.clone()
	unblocked := false
	switch lane {
	case "steering":
		if index < 0 || index >= len(next.Steering) {
			entry.state.Unlock()
			return fmt.Errorf("queued message not found")
		}
		next.Steering = append(next.Steering[:index], next.Steering[index+1:]...)
	case "followUp":
		if next.Dispatch != nil && !entry.promptActive {
			if index == 0 {
				next.Dispatch = nil
				unblocked = true
			} else {
				index--
			}
		}
		if !unblocked && next.Blocked != nil {
			if index == 0 {
				next.Blocked = nil
				unblocked = true
			} else {
				index--
			}
		}
		if !unblocked {
			if index < 0 || index >= len(next.FollowUp) {
				entry.state.Unlock()
				return fmt.Errorf("queued message not found")
			}
			next.FollowUp = append(next.FollowUp[:index], next.FollowUp[index+1:]...)
		}
	default:
		entry.state.Unlock()
		return fmt.Errorf("unknown queue lane")
	}
	next.remember(identity)
	next.Revision = identifier.New()
	if err := m.saveQueueLocked(sessionID, entry, next); err != nil {
		entry.state.Unlock()
		return err
	}
	m.emitQueue(sessionID, entry)
	entry.state.Unlock()
	if unblocked {
		m.scheduleFollowUp(sessionID, entry)
	}
	return nil
}

func (m *SessionManager) RetryQueue(ctx context.Context, sessionID, lane string, index int, revision string) error {
	entry, err := m.queueEntry(sessionID)
	if err != nil {
		return err
	}
	defer m.releaseEntry(entry)
	if err := m.lockEntry(sessionID, entry); err != nil {
		return err
	}
	defer entry.op.Unlock()
	entry.state.Lock()
	identity := queueIdentity(ctx)
	duplicate, err := entry.queue.operation(identity)
	if err != nil {
		entry.state.Unlock()
		return err
	}
	if duplicate {
		m.emitQueueProjection(sessionID, entry, !entry.promptActive)
		entry.state.Unlock()
		return nil
	}
	if revision == "" || revision != entry.queue.Revision {
		entry.state.Unlock()
		return fmt.Errorf("the queue changed; refresh the queued message before retrying it")
	}
	if lane != "followUp" || index != 0 {
		entry.state.Unlock()
		return fmt.Errorf("queued message is not blocked")
	}
	retryID := ""
	if entry.queue.Dispatch != nil && entry.queue.Dispatch.Attempted && !entry.promptActive {
		retryID = entry.queue.Dispatch.ID
		requireQueueReplayLocked(entry)
	} else if entry.queue.Blocked != nil {
		retryID = entry.queue.Blocked.ID
	}
	if retryID == "" {
		entry.state.Unlock()
		return fmt.Errorf("queued message is not blocked")
	}
	entry.state.Unlock()
	if err := m.attachLocked(ctx, sessionID, entry); err != nil {
		return err
	}
	entry.state.Lock()
	if revision != entry.queue.Revision {
		if entry.queue.Blocked == nil || entry.queue.Blocked.ID != retryID {
			// Replay proved that Pi already stored this turn. Record the retry
			// request as resolved so a lost ACK remains safe across another restart.
			next := entry.queue.clone()
			next.remember(identity)
			if err := m.saveQueueLocked(sessionID, entry, next); err != nil {
				entry.state.Unlock()
				return err
			}
			entry.state.Unlock()
			return nil
		}
	}
	next := entry.queue.clone()
	var item queuedFollowUp
	if next.Dispatch != nil && next.Dispatch.Attempted && !entry.promptActive {
		item = queuedFollowUp{ID: next.Dispatch.ID, Text: next.Dispatch.Text}
		next.Dispatch = nil
	} else if next.Blocked != nil {
		item = *next.Blocked
		next.Blocked = nil
	} else {
		entry.state.Unlock()
		return fmt.Errorf("queued message is not blocked")
	}
	next.FollowUp = append([]queuedFollowUp{item}, next.FollowUp...)
	next.remember(identity)
	next.Revision = identifier.New()
	if err := m.saveQueueLocked(sessionID, entry, next); err != nil {
		entry.state.Unlock()
		return err
	}
	m.emitQueue(sessionID, entry)
	entry.state.Unlock()
	m.scheduleFollowUp(sessionID, entry)
	return nil
}

// An attempted dispatch can only become retryable after an authoritative
// replay, even when the Pi connection itself did not change.
func requireQueueReplayLocked(entry *sessionEntry) {
	entry.attached = 0
}

func (m *SessionManager) Steer(ctx context.Context, sessionID, text string, images []ImageContent, resources []TextResourceAttachment) error {
	entry, err := m.entry(sessionID)
	if err != nil {
		return err
	}
	defer m.releaseEntry(entry)
	if err := m.lockEntry(sessionID, entry); err != nil {
		return err
	}
	defer entry.op.Unlock()
	if err := m.attachLocked(ctx, sessionID, entry); err != nil {
		return err
	}
	ctx = entry.context(ctx)
	_, profile, err := m.client.Profile(ctx)
	if err != nil {
		return err
	}
	if !profile.Operations.Steer {
		return unsupportedAgentCapability("session steering")
	}
	if len(images) > 0 && !profile.Operations.PromptImage {
		return unsupportedAgentCapability("image prompts")
	}
	if len(resources) > 0 && !profile.Operations.PromptEmbeddedContext {
		return unsupportedAgentCapability("text resource prompts")
	}
	entry.state.Lock()
	runID := entry.runID
	entry.state.Unlock()
	if runID == "" {
		return fmt.Errorf("Pi has not supplied a steerable run id")
	}
	prompt, err := promptBlocks(text, images, resources)
	if err != nil {
		return err
	}
	response, err := m.client.CallPi(ctx, "pi.session.steer", map[string]any{"sessionId": sessionID, "expectedRunId": runID, "prompt": prompt})
	if err != nil {
		return err
	}
	var value map[string]any
	if json.Unmarshal(response, &value) != nil || textValue(value["runId"]) == "" {
		return fmt.Errorf("Pi steer response is invalid")
	}
	entry.state.Lock()
	entry.runID = textValue(value["runId"])
	entry.state.Unlock()
	return nil
}

func (m *SessionManager) Abort(ctx context.Context, sessionID string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	entry, err := m.entry(sessionID)
	if err != nil {
		return err
	}
	defer m.releaseEntry(entry)
	if err := m.lockEntryContext(ctx, sessionID, entry); err != nil {
		return err
	}
	if err := m.attachLocked(ctx, sessionID, entry); err != nil {
		entry.op.Unlock()
		return err
	}
	entry.state.Lock()
	done := entry.promptDone
	entry.state.Unlock()

	m.cancelQuestions(sessionID)
	err = m.client.Cancel(entry.context(ctx), sessionID)
	entry.op.Unlock()
	if err != nil || done == nil {
		return err
	}
	// Cancellation is a notification. Its acknowledgement is prompt settlement,
	// which must acquire the operation lock we just released.
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *SessionManager) startPromptLocked(sessionID string, entry *sessionEntry, text string, images []ImageContent, resources []TextResourceAttachment, queueID string) error {
	prompt, err := promptBlocks(text, images, resources)
	if err != nil {
		return err
	}
	if err := m.retainWork(sessionID, entry); err != nil {
		return err
	}
	handedOff := false
	defer func() {
		if !handedOff {
			m.releaseWork(entry)
		}
	}()
	content := any(text)
	echoImages := make([]map[string]any, 0, len(images))
	echoResources := make([]map[string]any, 0, len(resources))
	resourceBytes := 0
	if len(images) > 0 || len(resources) > 0 {
		blocks := []any{map[string]any{"type": "text", "text": text}}
		for _, image := range images {
			block := map[string]any{"type": "image", "data": image.Data, "mimeType": image.MimeType}
			blocks = append(blocks, block)
			echoImages = append(echoImages, block)
		}
		for _, resource := range resources {
			marker := map[string]any{"type": "resource", "name": resource.Name, "mimeType": resource.MimeType}
			blocks = append(blocks, marker)
			echoResources = append(echoResources, marker)
			resourceBytes += len(resource.Text)
		}
		content = blocks
	}
	entry.state.Lock()
	if queueID != "" {
		if entry.queue.Dispatch == nil || entry.queue.Dispatch.ID != queueID {
			entry.state.Unlock()
			return fmt.Errorf("queued follow-up changed before delivery")
		}
		next := entry.queue.clone()
		next.Dispatch.Attempted = true
		if err := m.saveQueueLocked(sessionID, entry, next); err != nil {
			entry.state.Unlock()
			return err
		}
	}
	if !entry.streaming {
		// Retire previews left by an interrupted turn. Do this at the next
		// prompt, after the SDK has drained the prior turn's notifications.
		entry.pendingToolOutputs = nil
	}
	entry.messages = append(entry.messages, map[string]any{"role": "user", "content": content})
	entry.userResourceBytes = resourceBytes
	entry.pendingEcho = &userEcho{
		text:            text,
		images:          echoImages,
		resources:       echoResources,
		matched:         make([]bool, len(echoImages)),
		resourceMatched: make([]bool, len(echoResources)),
	}
	entry.promptAcknowledged = false
	entry.stats.TotalMessages = len(entry.messages)
	entry.streaming = true
	entry.promptActive = true
	entry.promptDone = make(chan struct{})
	done := entry.promptDone
	entry.promptGeneration++
	generation := entry.promptGeneration
	if queueID != "" {
		// A snapshot may have exposed the prepared dispatch immediately before
		// this turn became active. Hide it authoritatively before run-start.
		m.emitQueueProjection(sessionID, entry, false)
		m.emit("agent.event", map[string]any{"sessionId": sessionID, "event": map[string]any{"type": "message_start", "message": cloneJSON(entry.messages[len(entry.messages)-1])}})
	}
	m.emit("agent.event", map[string]any{"sessionId": sessionID, "event": map[string]any{"type": "run-start"}})
	entry.state.Unlock()
	promptContext := entry.context(context.Background())
	go func() {
		defer close(done)
		defer m.releaseWork(entry)
		response, promptErr := m.client.Prompt(promptContext, piwire.PromptRequest{SessionId: piwire.SessionId(sessionID), Prompt: prompt})
		if err := m.lockEntry(sessionID, entry); err != nil {
			return
		}
		defer entry.op.Unlock()
		entry.state.Lock()
		if entry.promptGeneration != generation {
			entry.state.Unlock()
			return
		}
		entry.promptActive = false
		entry.streaming = false
		acknowledged := entry.promptAcknowledged
		entry.promptAcknowledged = false
		queueErr := m.settleQueuedPromptLocked(sessionID, entry, queueID, promptErr == nil || acknowledged, promptErr != nil)
		if queueErr != nil {
			if promptErr == nil {
				promptErr = queueErr
			} else {
				promptErr = fmt.Errorf("%v; persist queued follow-up: %w", promptErr, queueErr)
			}
		}
		if promptErr != nil {
			entry.settlement = &SessionSettlement{StopReason: "error", ErrorMessage: promptErr.Error()}
			m.emit("agent.event", map[string]any{"sessionId": sessionID, "event": map[string]any{"type": "error", "error": promptErr.Error()}})
			entry.state.Unlock()
			m.scheduleFollowUp(sessionID, entry)
			return
		}
		stopReason := string(response.StopReason)
		if stopReason == "" {
			stopReason = "complete"
		}
		entry.settlement = &SessionSettlement{StopReason: stopReason}
		m.emit("agent.event", map[string]any{"sessionId": sessionID, "event": map[string]any{"type": "complete", "status": stopReason}})
		entry.state.Unlock()
		m.scheduleFollowUp(sessionID, entry)
	}()
	handedOff = true
	return nil
}

func (m *SessionManager) scheduleFollowUp(sessionID string, entry *sessionEntry) {
	if m.admitFollowUp(sessionID, entry) {
		go m.runFollowUp(sessionID, entry)
	}
}

func (m *SessionManager) admitFollowUp(sessionID string, entry *sessionEntry) bool {
	m.mu.Lock()
	entry.state.Lock()
	if m.closed || m.sessions[sessionID] != entry || m.lifecycle[sessionID] || entry.drainScheduled || !runnableFollowUpLocked(entry) {
		entry.state.Unlock()
		m.mu.Unlock()
		return false
	}
	if entry.drainRetry != nil {
		entry.drainRetry.Stop()
		entry.drainRetry = nil
	}
	entry.drainScheduled = true
	entry.refs++
	m.work.Add(1)
	entry.state.Unlock()
	m.mu.Unlock()
	return true
}

func (m *SessionManager) runFollowUp(sessionID string, entry *sessionEntry) {
	defer m.releaseWork(entry)
	retry := false
	if err := m.lockEntry(sessionID, entry); err == nil {
		drainErr := m.drainFollowUp(sessionID, entry)
		retry = drainErr != nil && !errors.Is(drainErr, errAgentIdentityChanged)
		// Clear the admission flag while still owning op. A queue mutation
		// cannot observe the old flag and lose its wakeup in this interval.
		entry.state.Lock()
		entry.drainScheduled = false
		if !retry {
			entry.drainFailures = 0
		}
		entry.state.Unlock()
		entry.op.Unlock()
	} else {
		entry.state.Lock()
		entry.drainScheduled = false
		entry.state.Unlock()
	}
	if retry {
		m.retryFollowUp(sessionID, entry)
	}
}

func (m *SessionManager) retryFollowUp(sessionID string, entry *sessionEntry) {
	m.mu.Lock()
	entry.state.Lock()
	if m.closed || m.sessions[sessionID] != entry || m.lifecycle[sessionID] || !runnableFollowUpLocked(entry) || entry.drainRetry != nil {
		entry.state.Unlock()
		m.mu.Unlock()
		return
	}
	if entry.drainFailures < 7 {
		entry.drainFailures++
	}
	shift := int(entry.drainFailures) - 1
	delay := 500*time.Millisecond*time.Duration(1<<shift) + followUpRetryJitter(sessionID)
	var timer *time.Timer
	timer = time.AfterFunc(delay, func() {
		entry.state.Lock()
		if entry.drainRetry != timer {
			entry.state.Unlock()
			return
		}
		entry.state.Unlock()
		m.scheduleFollowUp(sessionID, entry)
	})
	entry.drainRetry = timer
	entry.state.Unlock()
	m.mu.Unlock()
}

func followUpRetryJitter(sessionID string) time.Duration {
	value := 0
	for index := 0; index < len(sessionID); index++ {
		value = (value*33 + int(sessionID[index])) % 250
	}
	return time.Duration(value) * time.Millisecond
}

func (m *SessionManager) drainFollowUp(sessionID string, entry *sessionEntry) error {
	entry.state.Lock()
	if !runnableFollowUpLocked(entry) {
		entry.state.Unlock()
		return nil
	}
	needsReplay := entry.queue.Dispatch != nil && entry.queue.Dispatch.Attempted
	entry.state.Unlock()
	if needsReplay {
		if err := m.attachLocked(context.Background(), sessionID, entry); err != nil {
			return err
		}
	}
	entry.state.Lock()
	if entry.queue.Dispatch != nil && !entry.queue.Dispatch.Attempted {
		next := entry.queue.clone()
		item := queuedFollowUp{ID: next.Dispatch.ID, Text: next.Dispatch.Text}
		next.Dispatch = nil
		next.FollowUp = append([]queuedFollowUp{item}, next.FollowUp...)
		next.Revision = identifier.New()
		if err := m.saveQueueLocked(sessionID, entry, next); err != nil {
			entry.state.Unlock()
			return err
		}
		m.emitQueue(sessionID, entry)
	}
	ready := !entry.streaming && !entry.promptActive && entry.queue.Dispatch == nil && entry.queue.Blocked == nil && len(entry.queue.FollowUp) > 0
	entry.state.Unlock()
	if !ready {
		return nil
	}
	// A reconnect must load the session successfully before consuming its queue.
	if err := m.attachLocked(context.Background(), sessionID, entry); err != nil {
		return err
	}
	entry.state.Lock()
	if entry.streaming || entry.promptActive || entry.queue.Dispatch != nil || entry.queue.Blocked != nil || len(entry.queue.FollowUp) == 0 {
		entry.state.Unlock()
		return nil
	}
	item := entry.queue.FollowUp[0]
	next := entry.queue.clone()
	next.FollowUp = append([]queuedFollowUp{}, next.FollowUp[1:]...)
	next.Dispatch = &queuedDispatch{ID: item.ID, Text: item.Text, PreviousUserMessages: userMessageCount(entry.messages)}
	next.Revision = identifier.New()
	if err := m.saveQueueLocked(sessionID, entry, next); err != nil {
		entry.state.Unlock()
		return err
	}
	m.emitQueue(sessionID, entry)
	entry.state.Unlock()
	if err := m.startPromptLocked(sessionID, entry, item.Text, nil, nil, item.ID); err != nil {
		if restoreErr := m.restoreQueuedDispatch(sessionID, entry, item.ID); restoreErr != nil {
			return fmt.Errorf("prepare queued follow-up: %v; restore queue: %w", err, restoreErr)
		}
		return err
	}
	return nil
}

// Called with entry.state held. Prepared dispatches are safe to restore; an
// attempted dispatch is only recoverable after replay on a new connection.
func runnableFollowUpLocked(entry *sessionEntry) bool {
	if entry.streaming || entry.promptActive || entry.queue.Blocked != nil {
		return false
	}
	if entry.queue.Dispatch != nil {
		return !entry.queue.Dispatch.Attempted || entry.attached == 0
	}
	return len(entry.queue.FollowUp) > 0
}

// Called with entry.state held immediately after a queue mutation. An opaque
// revision also distinguishes identical messages and newly loaded projections.
func (m *SessionManager) emitQueue(sessionID string, entry *sessionEntry) {
	m.emitQueueProjection(sessionID, entry, false)
}

func (m *SessionManager) emitQueueProjection(sessionID string, entry *sessionEntry, includeDispatch bool) {
	queue := entry.queue.wire(includeDispatch)
	event := map[string]any{"type": "queue_update", "revision": queue.Revision, "steering": queue.Steering, "followUp": queue.FollowUp}
	if queue.Blocked != nil {
		event["blocked"] = queue.Blocked
	}
	m.emit("agent.event", map[string]any{"sessionId": sessionID, "event": event})
}

func (m *SessionManager) saveQueueLocked(sessionID string, entry *sessionEntry, next sessionQueueState) error {
	if m.queues != nil {
		if err := m.queues.Save(entry.projectID, sessionID, next); err != nil {
			return err
		}
	}
	entry.queue = next
	if len(next.FollowUp) == 0 || next.Dispatch != nil || next.Blocked != nil {
		if entry.drainRetry != nil {
			entry.drainRetry.Stop()
			entry.drainRetry = nil
		}
		if len(next.FollowUp) == 0 {
			entry.drainFailures = 0
		}
	}
	return nil
}

func (m *SessionManager) settleQueuedPromptLocked(sessionID string, entry *sessionEntry, queueID string, delivered, failed bool) error {
	if queueID == "" || entry.queue.Dispatch == nil || entry.queue.Dispatch.ID != queueID {
		return nil
	}
	next := entry.queue.clone()
	dispatch := queuedFollowUp{ID: next.Dispatch.ID, Text: next.Dispatch.Text}
	next.Dispatch = nil
	if failed && !delivered {
		next.Blocked = &dispatch
		next.Revision = identifier.New()
	}
	if err := m.saveQueueLocked(sessionID, entry, next); err != nil {
		m.emitQueueProjection(sessionID, entry, true)
		return err
	}
	// Publish every clear. A terminal Pi status may make a concurrent
	// snapshot expose the attempt before the prompt response settles it.
	m.emitQueue(sessionID, entry)
	return nil
}

func (m *SessionManager) restoreQueuedDispatch(sessionID string, entry *sessionEntry, queueID string) error {
	entry.state.Lock()
	defer entry.state.Unlock()
	if entry.queue.Dispatch == nil || entry.queue.Dispatch.ID != queueID {
		return nil
	}
	next := entry.queue.clone()
	item := queuedFollowUp{ID: next.Dispatch.ID, Text: next.Dispatch.Text}
	next.Dispatch = nil
	next.FollowUp = append([]queuedFollowUp{item}, next.FollowUp...)
	next.Revision = identifier.New()
	if err := m.saveQueueLocked(sessionID, entry, next); err != nil {
		m.emitQueueProjection(sessionID, entry, true)
		return err
	}
	m.emitQueue(sessionID, entry)
	return nil
}

func queuedFollowUpCount(queue sessionQueueState) int {
	count := len(queue.FollowUp)
	if queue.Dispatch != nil {
		count++
	}
	if queue.Blocked != nil {
		count++
	}
	return count
}

func userMessageCount(messages []any) int {
	count := 0
	for _, raw := range messages {
		if textValue(mapValue(raw)["role"]) == "user" {
			count++
		}
	}
	return count
}

func userMessageAt(messages []any, wanted int) (string, bool) {
	seen := 0
	for _, raw := range messages {
		message := mapValue(raw)
		if textValue(message["role"]) != "user" {
			continue
		}
		if seen == wanted {
			return historyText(message), true
		}
		seen++
	}
	return "", false
}

func (m *SessionManager) recoverQueuedDispatchLocked(sessionID string, entry *sessionEntry) error {
	if entry.queue.Dispatch == nil {
		return nil
	}
	dispatch := entry.queue.Dispatch
	next := entry.queue.clone()
	next.Dispatch = nil
	users := userMessageCount(entry.messages)
	if text, found := userMessageAt(entry.messages, dispatch.PreviousUserMessages); found && text == dispatch.Text {
		// Pi replayed the exact next user turn, so the durable attempt was admitted.
	} else if !dispatch.Attempted && users == dispatch.PreviousUserMessages {
		next.FollowUp = append([]queuedFollowUp{{ID: dispatch.ID, Text: dispatch.Text}}, next.FollowUp...)
	} else {
		next.Blocked = &queuedFollowUp{ID: dispatch.ID, Text: dispatch.Text}
		entry.settlement = &SessionSettlement{StopReason: "error", ErrorMessage: "A queued follow-up may already have reached the Pi agent before the controller restarted. Check the transcript, then retry or remove it."}
	}
	next.Revision = identifier.New()
	return m.saveQueueLocked(sessionID, entry, next)
}

func promptBlocks(text string, images []ImageContent, resources []TextResourceAttachment) ([]piwire.ContentBlock, error) {
	blocks := []piwire.ContentBlock{piwire.TextBlock(text)}
	total := 0
	for _, image := range images {
		if image.Type != "image" || (image.MimeType != "image/png" && image.MimeType != "image/jpeg" && image.MimeType != "image/gif" && image.MimeType != "image/webp") || !canonicalBase64(image.Data) {
			return nil, fmt.Errorf("malformed session image")
		}
		if len(image.Data) > int(4.5*1024*1024) {
			return nil, fmt.Errorf("session image exceeds the 4.5 MiB encoded size limit")
		}
		total += len(image.Data)
		if total > 24*1024*1024 {
			return nil, fmt.Errorf("session images exceed the 24 MiB aggregate encoded size limit")
		}
		blocks = append(blocks, piwire.ImageBlock(image.Data, image.MimeType))
	}
	if len(resources) > maxTextAttachmentCount {
		return nil, fmt.Errorf("session text attachments are limited to %d files", maxTextAttachmentCount)
	}
	resourceBytes := 0
	for _, resource := range resources {
		if !validTextResourceAttachment(resource) {
			return nil, fmt.Errorf("malformed session text attachment")
		}
		if len(resource.Text) > maxTextAttachmentBytes {
			return nil, fmt.Errorf("session text attachment exceeds the 1 MiB size limit")
		}
		resourceBytes += len(resource.Text)
		if resourceBytes > maxTextAttachmentTotalBytes {
			return nil, fmt.Errorf("session text attachments exceed the 2 MiB aggregate size limit")
		}
		mimeType := resource.MimeType
		blocks = append(blocks, piwire.ResourceBlock(piwire.EmbeddedResourceResource{
			TextResourceContents: &piwire.TextResourceContents{
				Uri:      "pixie://attachment/" + url.PathEscape(resource.Name),
				MimeType: &mimeType,
				Text:     resource.Text,
			},
		}))
	}
	return blocks, nil
}

func validTextResourceAttachment(resource TextResourceAttachment) bool {
	if resource.Type != "text" || !validTextAttachmentName(resource.Name) || !validTextAttachmentMimeType(resource.MimeType) || !utf8.ValidString(resource.Text) || containsNUL(resource.Text) {
		return false
	}
	for _, character := range resource.Text {
		if character < 0x20 && character != '\t' && character != '\n' && character != '\r' {
			return false
		}
	}
	return true
}

func validTextAttachmentName(name string) bool {
	if name == "" || strings.TrimSpace(name) != name || strings.ContainsAny(name, "/\\") || containsNUL(name) || !utf8.ValidString(name) || len(name) > maxTextAttachmentNameBytes || utf8.RuneCountInString(name) > maxTextAttachmentNameRunes {
		return false
	}
	for _, character := range name {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validTextAttachmentMimeType(mimeType string) bool {
	switch mimeType {
	case "text/plain", "text/markdown", "text/css", "text/html", "text/javascript", "text/x-c", "text/x-c++src", "text/x-csharp", "text/x-go", "text/x-java-source", "text/x-python", "text/x-rust", "text/x-shellscript", "text/x-typescript", "text/x-yaml", "application/json", "application/toml", "application/xml":
		return true
	default:
		return false
	}
}

func replayTextResourceMarker(content map[string]any) (map[string]any, int, bool) {
	resource := mapValue(content["resource"])
	uri, mimeType := textValue(resource["uri"]), textValue(resource["mimeType"])
	text, textOK := resource["text"].(string)
	if uri == "" || !textOK {
		return nil, 0, false
	}
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "pixie" || parsed.Host != "attachment" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || parsed.Opaque != "" {
		return nil, 0, false
	}
	escapedName, found := strings.CutPrefix(parsed.EscapedPath(), "/")
	if !found || strings.Contains(escapedName, "/") {
		return nil, 0, false
	}
	name, err := url.PathUnescape(escapedName)
	if err != nil || name == "" || !validTextResourceAttachment(TextResourceAttachment{Type: "text", Name: name, MimeType: mimeType, Text: text}) || len(text) > maxTextAttachmentBytes || uri != "pixie://attachment/"+url.PathEscape(name) {
		return nil, 0, false
	}
	return map[string]any{"type": "resource", "name": name, "mimeType": mimeType}, len(text), true
}

func canonicalBase64(value string) bool {
	if value == "" || len(value)%4 != 0 {
		return false
	}
	padding := 0
	if value[len(value)-1] == '=' {
		padding++
		if value[len(value)-2] == '=' {
			padding++
		}
	}
	for index := 0; index < len(value)-padding; index++ {
		if base64Index(value[index]) < 0 {
			return false
		}
	}
	for index := len(value) - padding; index < len(value); index++ {
		if value[index] != '=' {
			return false
		}
	}
	if padding == 2 {
		return base64Index(value[len(value)-3])&15 == 0
	}
	if padding == 1 {
		return base64Index(value[len(value)-2])&3 == 0
	}
	return true
}

func base64Index(value byte) int {
	switch {
	case value >= 'A' && value <= 'Z':
		return int(value - 'A')
	case value >= 'a' && value <= 'z':
		return int(value-'a') + 26
	case value >= '0' && value <= '9':
		return int(value-'0') + 52
	case value == '+':
		return 62
	case value == '/':
		return 63
	default:
		return -1
	}
}

func queuedText(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || containsNUL(value) || !utf8.ValidString(value) {
		return "", fmt.Errorf("queued message is invalid")
	}
	return value, nil
}
