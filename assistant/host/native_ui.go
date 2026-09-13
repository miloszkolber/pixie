package host

// This file owns the native extension UI bridge. Pi emits extension_ui_request
// frames on stdout; blocking dialogs (select, confirm, input, editor) stay
// pending until the controller answers with session.uiResponse/session.uiCancel.
// The host validates the answer against the exact outstanding string id and
// writes the matching extension_ui_response frame back to the resident child.
// Passive methods (notify, setStatus, setWidget, setTitle, setWorkingMessage)
// never register pending state and need no answer.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// nativeUiPendingMax bounds the per-child outstanding dialog map. The
// controller admits at most 16 dialogs per session; this is a defensive
// backstop against a misbehaving child, not a normal operating limit.
const nativeUiPendingMax = 128

// isDialogUiMethod reports whether a Pi extension UI method blocks until the
// client answers. Passive methods are fire-and-forget.
func isDialogUiMethod(method string) bool {
	switch method {
	case "select", "confirm", "input", "editor":
		return true
	}
	return false
}

// trackUiRequest records one blocking extension_ui_request so a later answer
// can be validated against the exact outstanding dialog. Passive methods,
// missing ids and malformed frames are ignored.
func (c *nativeChild) trackUiRequest(event map[string]any) {
	id, _ := event["id"].(string)
	method, _ := event["method"].(string)
	if id == "" || !isDialogUiMethod(method) {
		return
	}
	c.mu.Lock()
	if c.pendingDialogs == nil {
		c.pendingDialogs = make(map[string]string)
	}
	if len(c.pendingDialogs) >= nativeUiPendingMax {
		c.mu.Unlock()
		return
	}
	c.pendingDialogs[id] = method
	c.mu.Unlock()
}

// takeUiRequest atomically consumes one pending dialog id, returning its
// method. Unknown, expired and already answered ids are rejected so a stale
// answer can never settle a different dialog.
func (c *nativeChild) takeUiRequest(id string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	method, ok := c.pendingDialogs[id]
	if !ok {
		return "", false
	}
	delete(c.pendingDialogs, id)
	return method, true
}

// restoreUiRequest puts back an id whose response frame was never queued, so
// the controller can retry without losing the dialog.
func (c *nativeChild) restoreUiRequest(id, method string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pendingDialogs == nil {
		c.pendingDialogs = make(map[string]string)
	}
	c.pendingDialogs[id] = method
}

// staleUiRequestError reports an answer for a dialog id the resident child is
// not currently waiting on: unknown, already settled, or expired.
type staleUiRequestError struct{ id string }

func (e *staleUiRequestError) Error() string {
	if e.id == "" {
		return "dialog request id is required"
	}
	return fmt.Sprintf("dialog request %q is unknown or already settled", e.id)
}

// missingUiChildError reports a UI answer for a session with no resident Pi
// child, so the raw response frame has nowhere to go.
type missingUiChildError struct {
	sessionID string
	cause     error
}

func (e *missingUiChildError) Error() string {
	if e.cause == nil {
		return fmt.Sprintf("session %q has no resident Pi child", e.sessionID)
	}
	return fmt.Sprintf("session %q has no resident Pi child: %v", e.sessionID, e.cause)
}

func (e *missingUiChildError) Unwrap() error { return e.cause }

// respondUi routes session.uiResponse and session.uiCancel to the resident
// child as a raw extension_ui_response frame. The controller sends confirm
// answers as a boolean value, so the tracked method decides between the value
// and confirmed response shapes; cancel always sends cancelled:true.
func (s *nativeSupervisor) respondUi(ctx context.Context, params map[string]any, cancel bool) (json.RawMessage, error) {
	sessionID, _ := params["sessionId"].(string)
	requestID, _ := params["requestId"].(string)
	if sessionID == "" {
		return nil, &missingUiChildError{cause: errors.New("sessionId is required")}
	}
	if requestID == "" {
		return nil, &staleUiRequestError{}
	}
	child, err := s.resident(sessionID)
	if err != nil {
		return nil, &missingUiChildError{sessionID: sessionID, cause: err}
	}
	method, ok := child.takeUiRequest(requestID)
	if !ok {
		return nil, &staleUiRequestError{id: requestID}
	}
	frame, err := uiResponseFrame(requestID, method, params, cancel)
	if err != nil {
		child.restoreUiRequest(requestID, method)
		return nil, err
	}
	if err := child.writeRaw(ctx, frame); err != nil {
		child.restoreUiRequest(requestID, method)
		return nil, err
	}
	return json.RawMessage(`{}`), nil
}

// uiResponseFrame builds the exact Pi extension_ui_response payload for one
// tracked dialog method.
func uiResponseFrame(requestID, method string, params map[string]any, cancel bool) (map[string]any, error) {
	frame := map[string]any{"type": "extension_ui_response", "id": requestID}
	if cancel {
		frame["cancelled"] = true
		return frame, nil
	}
	if cancelled, _ := params["cancelled"].(bool); cancelled {
		frame["cancelled"] = true
		return frame, nil
	}
	if method == "confirm" {
		confirmed, ok := params["confirmed"].(bool)
		if !ok {
			// The controller maps the browser's confirm answer onto value.
			confirmed, ok = params["value"].(bool)
		}
		if !ok {
			return nil, errors.New("session.uiResponse requires a boolean confirmed value")
		}
		frame["confirmed"] = confirmed
		return frame, nil
	}
	value, ok := params["value"]
	if !ok || value == nil {
		return nil, errors.New("session.uiResponse requires a value or cancellation")
	}
	frame["value"] = value
	return frame, nil
}
