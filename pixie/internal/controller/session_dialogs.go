package controller

import (
	"context"
	"fmt"
	"time"

	piwire "github.com/miloszkolber/pixie/internal/piprotocol"
)

// Pending extension dialogs are manager-level, like pending questions:
// session-bound, single-use, and bounded by a timeout. The Pi host holds the
// matching promise; the controller only validates the browser's answer and
// relays it to the host, so neither side can satisfy another session's dialog.
const dialogTimeout = 30 * time.Minute

type dialogKey struct{ sessionID, requestID string }

type pendingDialog struct {
	sessionID string
	primitive string
	timer     *time.Timer
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
		timeout = time.Duration(ms) * time.Millisecond
		if timeout > dialogTimeout {
			timeout = dialogTimeout
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
		return nil
	}
	return fmt.Errorf("malformed dialog response")
}

// registerDialog records a host dialog request and publishes it to browsers.
// Invalid or duplicate requests are dropped; a dialog fires once and settles
// cancelled on timeout so neither side waits forever.
func (m *SessionManager) registerDialog(update map[string]any) {
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
	if _, exists := m.dialogs[key]; exists {
		m.mu.Unlock()
		return
	}
	pending := &pendingDialog{sessionID: sessionID, primitive: primitive}
	pending.timer = time.AfterFunc(timeout, func() {
		m.mu.Lock()
		if m.dialogs[key] == pending {
			delete(m.dialogs, key)
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
	if pending == nil || pending.sessionID != sessionID {
		m.mu.Unlock()
		return fmt.Errorf("dialog is no longer awaiting input")
	}
	if err := validateDialogResult(result, map[string]any{"primitive": pending.primitive}); err != nil {
		m.mu.Unlock()
		return err
	}
	delete(m.dialogs, key)
	pending.timer.Stop()
	m.mu.Unlock()
	if m.client == nil {
		return nil
	}
	params := map[string]any{"sessionId": sessionID, "requestId": requestID}
	if value, exists := result["value"]; exists {
		params["value"] = value
	}
	if cancelled, ok := result["cancelled"].(bool); ok && cancelled {
		params["cancelled"] = true
	}
	_, err := m.client.CallPiUntilDone(ctx, piwire.UiResponseMethod, params)
	return err
}

// CancelDialog dismisses one pending dialog as cancelled and tells the host
// its extension call can stop waiting. Idempotent: settling twice is a no-op.
func (m *SessionManager) CancelDialog(ctx context.Context, sessionID, requestID string) error {
	m.mu.Lock()
	key := dialogKey{sessionID, requestID}
	pending := m.dialogs[key]
	if pending == nil {
		m.mu.Unlock()
		return nil
	}
	delete(m.dialogs, key)
	pending.timer.Stop()
	m.mu.Unlock()
	m.emit("agent.event", map[string]any{
		"sessionId": sessionID,
		"event":     map[string]any{"type": "ui_cancel", "requestId": requestID},
	})
	if m.client == nil {
		return nil
	}
	_, err := m.client.CallPiUntilDone(ctx, piwire.UiCancelMethod, map[string]any{
		"sessionId": sessionID, "requestId": requestID, "reason": "cancelled",
	})
	return err
}

// applyUiUpdate routes projected UI bridge updates. Dialog requests register
// pending state and reach browsers through registerDialog; notifications and
// host-side cancellations only fan out to browsers.
func (m *SessionManager) applyUiUpdate(sessionID, kind string, update map[string]any) error {
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
		m.registerDialog(request)
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
		m.CancelDialog(context.Background(), sessionID, textValue(update["requestId"]))
		return nil
	}
	return nil
}

func (m *SessionManager) cancelDialogs(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, pending := range m.dialogs {
		if sessionID == "" || key.sessionID == sessionID {
			pending.timer.Stop()
			delete(m.dialogs, key)
		}
	}
}
