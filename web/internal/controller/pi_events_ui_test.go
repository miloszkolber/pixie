package controller

import (
	"context"
	"encoding/json"
	"testing"

	piwire "github.com/miloszkolber/pixie/shared/piprotocol"
)

type recordingPiEvents struct {
	updates    []piwire.SessionNotification
	extensions []recordedExtension
}

type recordedExtension struct {
	method string
	params json.RawMessage
}

func (r *recordingPiEvents) SessionUpdate(_ context.Context, notification piwire.SessionNotification) error {
	r.updates = append(r.updates, notification)
	return nil
}

func (r *recordingPiEvents) Extension(_ context.Context, method string, params json.RawMessage) error {
	r.extensions = append(r.extensions, recordedExtension{method: method, params: params})
	return nil
}

func TestProjectNativeUiRequestBecomesControllerDialog(t *testing.T) {
	sink := &recordingPiEvents{}
	raw := json.RawMessage(`{"sessionId":"s1","event":{"type":"extension_ui_request","id":"req-1","method":"select","title":"Pick","options":["a","b"],"timeout":5000}}`)
	if err := projectPiEvent(context.Background(), sink, raw); err != nil {
		t.Fatal(err)
	}
	if len(sink.updates) != 1 {
		t.Fatalf("updates = %#v", sink.updates)
	}
	update := sink.updates[0].Update
	if sink.updates[0].SessionId != "s1" || update["sessionUpdate"] != "ui_request" {
		t.Fatalf("projected notification = %#v", sink.updates[0])
	}
	if update["requestId"] != "req-1" || update["primitive"] != "select" || update["title"] != "Pick" {
		t.Fatalf("projected request = %#v", update)
	}
	if options, ok := update["options"].([]any); !ok || len(options) != 2 || options[0] != "a" {
		t.Fatalf("projected options = %#v", update["options"])
	}
	if update["timeout"] != float64(5000) {
		t.Fatalf("projected timeout = %#v", update["timeout"])
	}
}

func TestProjectNativeUiPassiveMethodsFanOut(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		kind      string
		extension bool
	}{
		{"notify", `{"type":"extension_ui_request","id":"n","method":"notify","message":"hi","notifyType":"warning"}`, "ui_notify", true},
		{"setStatus", `{"type":"extension_ui_request","id":"s","method":"setStatus","statusKey":"k","statusText":"v"}`, "ui_status", false},
		{"setWidget", `{"type":"extension_ui_request","id":"w","method":"setWidget","widgetKey":"k","widgetLines":["a","b"],"widgetPlacement":"belowEditor"}`, "ui_widget", false},
		{"setTitle", `{"type":"extension_ui_request","id":"t","method":"setTitle","title":"T"}`, "ui_title", false},
		{"setWorkingMessage", `{"type":"extension_ui_request","id":"m","method":"setWorkingMessage","message":"working"}`, "ui_working", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			sink := &recordingPiEvents{}
			raw := json.RawMessage(`{"sessionId":"s1","event":` + test.raw + `}`)
			if err := projectPiEvent(context.Background(), sink, raw); err != nil {
				t.Fatal(err)
			}
			// notify travels through the extension channel; the remaining
			// passive projections are session updates.
			if test.extension {
				if len(sink.extensions) != 1 || len(sink.updates) != 0 {
					t.Fatalf("notify channels: updates=%#v extensions=%#v", sink.updates, sink.extensions)
				}
				extension := sink.extensions[0]
				if extension.method != "pi.session.update" {
					t.Fatalf("extension method = %q", extension.method)
				}
				var envelope map[string]any
				if json.Unmarshal(extension.params, &envelope) != nil {
					t.Fatalf("extension params = %s", extension.params)
				}
				update := mapValue(envelope["update"])
				if update["sessionUpdate"] != test.kind || update["message"] != "hi" || update["level"] != "warning" {
					t.Fatalf("notify projection = %#v", update)
				}
				return
			}
			if len(sink.updates) != 1 || len(sink.extensions) != 0 {
				t.Fatalf("passive channels: updates=%#v extensions=%#v", sink.updates, sink.extensions)
			}
			if sink.updates[0].Update["sessionUpdate"] != test.kind {
				t.Fatalf("passive projection = %#v", sink.updates[0].Update)
			}
		})
	}
}

func TestProjectNativeUiUnknownMethodIsDropped(t *testing.T) {
	sink := &recordingPiEvents{}
	raw := json.RawMessage(`{"sessionId":"s1","event":{"type":"extension_ui_request","id":"x","method":"set_editor_text","text":"draft"}}`)
	if err := projectPiEvent(context.Background(), sink, raw); err != nil {
		t.Fatal(err)
	}
	if len(sink.updates) != 0 || len(sink.extensions) != 0 {
		t.Fatalf("unknown UI method projected: updates=%#v extensions=%#v", sink.updates, sink.extensions)
	}
}
