package controller

import (
	"context"
	"fmt"
	"time"

	piwire "github.com/miloszkolber/pixie/contracts/piprotocol"
)

// Pending extension dialogs are manager-level:
// session-bound, single-use, and bounded by a timeout. The Pi host holds the
// matching promise; the controller only validates the browser's answer and
// relays it to the host, so neither side can satisfy another session's dialog.
const dialogTimeout = 30 * time.Minute

type dialogKey struct{ sessionID, requestID string }

// UI bypasses transcript replay, but must not bypass connection ownership.
func (m *SessionManager) acceptUiUpdate(ctx context.Context, sessionID string) bool {
	m.mu.Lock()
	closed, entry, creating, client := m.closed, m.sessions[sessionID], m.creating, m.client
	m.mu.Unlock()
	if closed {
		return false
	}
	generation, tagged := ctx.Value(connectionGenerationKey{}).(uint64)
	if !tagged {
		return true
	}
	if client != nil {
		client.mu.Lock()
		current := !client.closed && client.generation == generation
		client.mu.Unlock()
		if !current {
			return false
		}
	}
	// Initial session_start requests may precede the create response.
	if entry == nil {
		return creating > 0
	}
	entry.state.Lock()
	defer entry.state.Unlock()
	target := entry
	if entry.replay != nil {
		target = entry.replay
	}
	return target.attached == generation
}

type pendingDialog struct {
	sessionID   string
	primitive   string
	title       string
	message     string
	options     []any
	placeholder string
	prefill     string
	timer       *time.Timer
	resolving   bool
	generation  uint64
}

func validDialogPrimitive(primitive string) bool {
	switch primitive {
	case piwire.UiPrimitiveSelect, piwire.UiPrimitiveConfirm, piwire.UiPrimitiveInput, piwire.UiPrimitiveEditor:
		return true
	}
	return false
}

func validateDialogRequest(value map[string]any) (sessionID, requestID, primitive string, timeout time.Duration, err error) {
	sessionID, requestID, primitive = textValue(value["sessionId"]), textValue(value["requestId"]), textValue(value["primitive"])
	if sessionID == "" || len(sessionID) > 256 || requestID == "" || len(requestID) > 256 {
		return "", "", "", 0, fmt.Errorf("dialog request needs a session and a request ID")
	}
	if !validDialogPrimitive(primitive) {
		return "", "", "", 0, fmt.Errorf("unknown dialog primitive")
	}
	if title := textValue(value["title"]); title == "" || utf16Length(title) > 2000 {
		return "", "", "", 0, fmt.Errorf("dialog title is invalid")
	}
	for _, key := range []string{"message", "placeholder", "prefill"} {
		if utf16Length(textValue(value[key])) > 8000 {
			return "", "", "", 0, fmt.Errorf("dialog text is too long")
		}
	}
	if primitive == piwire.UiPrimitiveSelect {
		options := arrayValue(value["options"])
		if len(options) < 1 || len(options) > 24 {
			return "", "", "", 0, fmt.Errorf("dialog options are invalid")
		}
		for _, option := range options {
			label := textValue(option)
			if label == "" || utf16Length(label) > 500 {
				return "", "", "", 0, fmt.Errorf("dialog options are invalid")
			}
		}
	}
	timeout = dialogTimeout
	if ms, ok := numeric(value["timeout"]); ok && ms > 0 {
		if ms < dialogTimeout.Milliseconds() {
			timeout = time.Duration(ms) * time.Millisecond
		}
	}
	return sessionID, requestID, primitive, timeout, nil
}

func validateDialogResult(result, request map[string]any) error {
	cancelled, ok := result["cancelled"].(bool)
	if !ok {
		return fmt.Errorf("malformed dialog response")
	}
	if cancelled {
		return nil
	}
	switch textValue(request["primitive"]) {
	case piwire.UiPrimitiveConfirm:
		if _, ok := result["value"].(bool); !ok {
			return fmt.Errorf("malformed dialog response")
		}
		return nil
	case piwire.UiPrimitiveSelect, piwire.UiPrimitiveInput, piwire.UiPrimitiveEditor:
		if value, ok := result["value"].(string); !ok || utf16Length(value) > 8000 {
			return fmt.Errorf("malformed dialog response")
		}
		if textValue(request["primitive"]) == piwire.UiPrimitiveSelect {
			for _, option := range arrayValue(request["options"]) {
				if option == result["value"] {
					return nil
				}
			}
			return fmt.Errorf("dialog value was not offered")
		}
		return nil
	}
	return fmt.Errorf("malformed dialog response")
}

// registerDialog records a host dialog request and publishes it to browsers.
// Invalid or duplicate requests are dropped; a dialog fires once and settles
// cancelled on timeout so neither side waits forever. Pending dialogs survive
// Pi re-attachment: the host re-publishes unresolved requests on
// session.load (deduplicated here by request ID), and late subscribers catch
// up through the snapshot's pendingDialogs. A request vanishes only when it
// is answered, cancelled, times out, or its session stops.
func (m *SessionManager) registerDialog(ctx context.Context, update map[string]any) {
	sessionID, requestID, primitive, timeout, err := validateDialogRequest(update)
	if err != nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	if m.dialogs == nil {
		m.dialogs = make(map[dialogKey]*pendingDialog)
	}
	key := dialogKey{sessionID, requestID}
	generation, _ := ctx.Value(connectionGenerationKey{}).(uint64)
	if existing, exists := m.dialogs[key]; exists {
		existing.generation = generation
		m.mu.Unlock()
		return
	}
	count := 0
	for existing, dialog := range m.dialogs {
		if existing.sessionID == sessionID && dialog.generation == generation {
			count++
		}
	}
	if count >= 16 {
		m.mu.Unlock()
		return
	}
	pending := &pendingDialog{
		generation:  generation,
		sessionID:   sessionID,
		primitive:   primitive,
		title:       textValue(update["title"]),
		message:     textValue(update["message"]),
		options:     arrayValue(update["options"]),
		placeholder: textValue(update["placeholder"]),
		prefill:     textValue(update["prefill"]),
	}
	pending.timer = time.AfterFunc(timeout, func() {
		m.mu.Lock()
		if m.dialogs[key] == pending {
			delete(m.dialogs, key)
		} else {
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
		m.emit("agent.event", map[string]any{
			"sessionId": sessionID,
			"event":     map[string]any{"type": "ui_cancel", "requestId": requestID},
		})
		if m.client != nil {
			_, _ = m.client.CallPiUntilDone(context.Background(), piwire.UiCancelMethod, map[string]any{
				"sessionId": sessionID, "requestId": requestID, "reason": "timeout",
			})
		}
	})
	m.dialogs[key] = pending
	m.mu.Unlock()
	request := map[string]any{
		"type":      "ui_request",
		"requestId": requestID,
		"sessionId": sessionID,
		"primitive": primitive,
		"title":     update["title"],
	}
	for _, key := range []string{"message", "options", "placeholder", "prefill"} {
		if value, exists := update[key]; exists {
			request[key] = value
		}
	}
	m.emit("agent.event", map[string]any{"sessionId": sessionID, "event": request})
}

// ResolveDialog validates the browser's answer for one pending dialog and
// relays it to the Pi host, which settles the extension's awaiting call.
// Session-bound and single-use: foreign or replayed answers are rejected.
func (m *SessionManager) ResolveDialog(ctx context.Context, sessionID, requestID string, result map[string]any) error {
	m.mu.Lock()
	key := dialogKey{sessionID, requestID}
	pending := m.dialogs[key]
	if pending == nil || pending.sessionID != sessionID || pending.resolving {
		m.mu.Unlock()
		return fmt.Errorf("dialog is no longer awaiting input")
	}
	if err := validateDialogResult(result, map[string]any{"primitive": pending.primitive, "options": pending.options}); err != nil {
		m.mu.Unlock()
		return err
	}
	pending.resolving = true
	m.mu.Unlock()
	params := map[string]any{"sessionId": sessionID, "requestId": requestID}
	if value, exists := result["value"]; exists {
		params["value"] = value
	}
	if cancelled, ok := result["cancelled"].(bool); ok && cancelled {
		params["cancelled"] = true
	}
	var err error
	if m.client != nil {
		_, err = m.client.CallPiUntilDone(ctx, piwire.UiResponseMethod, params)
	}
	m.mu.Lock()
	if m.dialogs[key] == pending {
		if err == nil {
			delete(m.dialogs, key)
			pending.timer.Stop()
		} else {
			// Keep the original deadline and permit a retry only while the host
			// has not cancelled/settled this request. Never resurrect removed state.
			pending.resolving = false
		}
	}
	m.mu.Unlock()
	if err == nil {
		m.emit("agent.event", map[string]any{"sessionId": sessionID, "event": map[string]any{"type": "ui_cancel", "requestId": requestID}})
	}
	return err
}

// CancelDialog dismisses one pending dialog as cancelled and tells the host
// its extension call can stop waiting. Idempotent: settling twice is a no-op.
func (m *SessionManager) CancelDialog(ctx context.Context, sessionID, requestID string) error {
	if !m.dismissDialog(sessionID, requestID) || m.client == nil {
		return nil
	}
	_, err := m.client.CallPiUntilDone(ctx, piwire.UiCancelMethod, map[string]any{
		"sessionId": sessionID, "requestId": requestID, "reason": "cancelled",
	})
	return err
}

func (m *SessionManager) dismissDialog(sessionID, requestID string) bool {
	m.mu.Lock()
	key := dialogKey{sessionID, requestID}
	pending := m.dialogs[key]
	if pending == nil {
		m.mu.Unlock()
		return false
	}
	delete(m.dialogs, key)
	pending.timer.Stop()
	m.mu.Unlock()
	m.emit("agent.event", map[string]any{
		"sessionId": sessionID,
		"event":     map[string]any{"type": "ui_cancel", "requestId": requestID},
	})
	return true
}

// applyUiUpdate routes projected UI bridge updates. Dialog requests register
// pending state and reach browsers through registerDialog; notifications,
// status/widget/title/working projections, and host-side cancellations only
// fan out to browsers.
func (m *SessionManager) applyUiUpdate(ctx context.Context, sessionID, kind string, update map[string]any) error {
	switch kind {
	case "ui_request":
		request := map[string]any{
			"sessionId": sessionID,
			"requestId": update["requestId"],
			"primitive": update["primitive"],
			"title":     update["title"],
		}
		for _, key := range []string{"message", "options", "placeholder", "prefill", "timeout"} {
			if value, exists := update[key]; exists {
				request[key] = value
			}
		}
		m.registerDialog(ctx, request)
		return nil
	case "ui_notify":
		m.emit("agent.event", map[string]any{
			"sessionId": sessionID,
			"event": map[string]any{
				"type":    "ui_notify",
				"message": textValue(update["message"]),
				"level":   textValue(update["level"]),
			},
		})
		return nil
	case "ui_cancel":
		// The host already settled this request. Do not echo an RPC from the
		// synchronous receive loop, which must keep reading its response.
		m.dismissDialog(sessionID, textValue(update["requestId"]))
		return nil
	case "ui_status":
		m.emit("agent.event", map[string]any{
			"sessionId": sessionID,
			"event": map[string]any{
				"type": "ui_status",
				"key":  textValue(update["key"]),
				"text": textValue(update["text"]),
			},
		})
		return nil
	case "ui_widget":
		event := map[string]any{"type": "ui_widget", "key": textValue(update["key"])}
		if lines, ok := update["lines"].([]any); ok {
			strings := make([]any, 0, len(lines))
			for _, line := range lines {
				if text, ok := line.(string); ok {
					strings = append(strings, text)
				}
			}
			event["lines"] = strings
		}
		if placement := textValue(update["placement"]); placement != "" {
			event["placement"] = placement
		}
		m.emit("agent.event", map[string]any{"sessionId": sessionID, "event": event})
		return nil
	case "ui_title":
		m.emit("agent.event", map[string]any{
			"sessionId": sessionID,
			"event":     map[string]any{"type": "ui_title", "title": textValue(update["title"])},
		})
		return nil
	case "ui_working":
		m.emit("agent.event", map[string]any{
			"sessionId": sessionID,
			"event":     map[string]any{"type": "ui_working", "message": textValue(update["message"])},
		})
		return nil
	}
	return nil
}

// pendingDialogRequests returns the unresolved blocking dialogs for one
// session so snapshots can reconcile late subscribers after a reconnect.
func (m *SessionManager) pendingDialogRequests(sessionID string) []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	var requests []map[string]any
	for key, pending := range m.dialogs {
		if key.sessionID != sessionID {
			continue
		}
		request := map[string]any{
			"requestId": key.requestID,
			"sessionId": pending.sessionID,
			"primitive": pending.primitive,
			"title":     pending.title,
		}
		if pending.message != "" {
			request["message"] = pending.message
		}
		if len(pending.options) > 0 {
			request["options"] = append([]any(nil), pending.options...)
		}
		if pending.placeholder != "" {
			request["placeholder"] = pending.placeholder
		}
		if pending.prefill != "" {
			request["prefill"] = pending.prefill
		}
		requests = append(requests, request)
	}
	return requests
}

func (m *SessionManager) cancelDialogs(sessionID string) {
	m.mu.Lock()
	pending := make([]dialogKey, 0)
	for key := range m.dialogs {
		if sessionID == "" || key.sessionID == sessionID {
			if entry := m.dialogs[key]; entry != nil {
				entry.timer.Stop()
			}
			delete(m.dialogs, key)
			pending = append(pending, key)
		}
	}
	m.mu.Unlock()
	// Dismiss the browser modal for every discarded dialog. The host settles
	// its own awaiting call through session.cancel/close, so this side only
	// needs the fan-out (idempotent with a racing host ui_cancel).
	for _, key := range pending {
		m.emit("agent.event", map[string]any{
			"sessionId": key.sessionID,
			"event":     map[string]any{"type": "ui_cancel", "requestId": key.requestID},
		})
	}
}

// After a successful native load, only requests observed in that connection's
// replay remain authoritative. Untagged local updates have no generation proof.
func (m *SessionManager) reconcileDialogGeneration(sessionID string, generation uint64) {
	m.mu.Lock()
	var removed []string
	for key, pending := range m.dialogs {
		if key.sessionID == sessionID && pending.generation != 0 && pending.generation != generation {
			pending.timer.Stop()
			delete(m.dialogs, key)
			removed = append(removed, key.requestID)
		}
	}
	m.mu.Unlock()
	for _, requestID := range removed {
		m.emit("agent.event", map[string]any{"sessionId": sessionID, "event": map[string]any{"type": "ui_cancel", "requestId": requestID}})
	}
}
