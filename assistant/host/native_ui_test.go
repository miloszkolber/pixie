package host

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingWriteCloser struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *recordingWriteCloser) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *recordingWriteCloser) Close() error { return nil }

func (w *recordingWriteCloser) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestWriteRawWritesOneJSONLLineWithCallerID(t *testing.T) {
	writer := &recordingWriteCloser{}
	child := &nativeChild{
		stdin: writer, writes: make(chan nativeWrite, 2), done: make(chan struct{}),
		errors: make(chan error, 1), pending: map[uint64]nativePending{},
		pendingDialogs: map[string]string{}, writeTimeout: time.Second,
	}
	go child.writerLoop()
	if err := child.writeRaw(context.Background(), map[string]any{
		"type": "extension_ui_response", "id": "req-1", "value": "Allow",
	}); err != nil {
		t.Fatalf("writeRaw: %v", err)
	}
	record := writer.String()
	if strings.Count(record, "\n") != 1 || !strings.HasSuffix(record, "\n") {
		t.Fatalf("writeRaw record = %q, want one JSONL line", record)
	}
	var frame map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(record)), &frame) != nil {
		t.Fatalf("writeRaw frame is not JSON: %q", record)
	}
	// The caller's string id must survive; writeRaw must not invent a numeric
	// request id the way callPi does.
	if frame["id"] != "req-1" || frame["value"] != "Allow" || frame["type"] != "extension_ui_response" {
		t.Fatalf("writeRaw frame = %#v", frame)
	}
}

func TestFailNativeClearsPendingDialogs(t *testing.T) {
	child := &nativeChild{
		done: make(chan struct{}), errors: make(chan error, 1),
		pending: map[uint64]nativePending{}, pendingDialogs: map[string]string{"req-1": "select"},
	}
	child.failNative(errors.New("child exited"))
	if pending := child.testPendingDialogs(); len(pending) != 0 {
		t.Fatalf("pending dialogs after failure = %#v", pending)
	}
}

func startUiPiFixture(t *testing.T) (*nativeSupervisor, string, string) {
	t.Helper()
	cwd := t.TempDir()
	agentDir := t.TempDir()
	logPath := filepath.Join(agentDir, "ui.log")
	fixture := filepath.Join(t.TempDir(), "pi")
	script := `#!/bin/sh
state=ui-$$
path="$PI_CODING_AGENT_DIR/$state.jsonl"
: > "$path"
printf '{"type":"extension_ui_request","id":"dlg-confirm","method":"confirm","title":"Proceed?","message":"continue?","timeout":5000}\n'
printf '{"type":"extension_ui_request","id":"dlg-select","method":"select","title":"Pick","options":["a","b"]}\n'
printf '{"type":"extension_ui_request","id":"dlg-input","method":"input","title":"Value","placeholder":"type"}\n'
printf '{"type":"extension_ui_request","id":"note-1","method":"notify","message":"hello","notifyType":"warning"}\n'
printf '{"type":"agent_settled"}\n'
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"type":"get_state"'*) printf '{"id":%s,"type":"response","command":"get_state","success":true,"data":{"sessionId":"%s","sessionFile":"%s","isStreaming":false,"thinkingLevel":"medium","model":{"id":"fixture","provider":"fixture"}}}\n' "$id" "$state" "$path" ;;
    *'"type":"get_messages"'*) printf '{"id":%s,"type":"response","command":"get_messages","success":true,"data":{"messages":[]}}\n' "$id" ;;
    *'"type":"extension_ui_response"'*) printf 'ui:%s\n' "$line" >> 'LOG' ;;
    *'"type":"shutdown"'*) exit 0 ;;
  esac
done
`
	script = strings.ReplaceAll(script, "LOG", logPath)
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	supervisor := newNativeSupervisor(Config{PiExecutable: fixture, AgentDir: agentDir})
	if err := supervisor.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := supervisor.close(ctx); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	})
	return supervisor, logPath, cwd
}

// waitForUiFrames polls the fixture log until it holds want parsed response
// frames.
func waitForUiFrames(t *testing.T, logPath string, want int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var raw []byte
	for {
		content, err := os.ReadFile(logPath)
		if err == nil {
			raw = content
			frames := make([]map[string]any, 0)
			for _, line := range strings.Split(string(content), "\n") {
				if !strings.HasPrefix(line, "ui:") {
					continue
				}
				var frame map[string]any
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "ui:")), &frame) == nil {
					frames = append(frames, frame)
				}
			}
			if len(frames) >= want {
				return frames
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d UI response frames; log=%q", want, raw)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestExtensionUiDialogRoutingAndPassiveFrames(t *testing.T) {
	supervisor, logPath, cwd := startUiPiFixture(t)
	var mu sync.Mutex
	seen := make([]nativeEvent, 0)
	unsubscribe := supervisor.subscribe(func(event nativeEvent) error {
		mu.Lock()
		seen = append(seen, event)
		mu.Unlock()
		return nil
	})
	defer unsubscribe()
	id := createFixtureSession(t, supervisor, cwd)
	child := supervisor.testChild(id)
	if child == nil {
		t.Fatal("fixture child is not resident")
	}
	pending := child.testPendingDialogs()
	if _, ok := pending["dlg-confirm"]; !ok || pending["dlg-confirm"] != "confirm" {
		t.Fatalf("confirm dialog was not tracked: %#v", pending)
	}
	if _, ok := pending["dlg-select"]; !ok {
		t.Fatalf("select dialog was not tracked: %#v", pending)
	}
	if _, ok := pending["dlg-input"]; !ok {
		t.Fatalf("input dialog was not tracked: %#v", pending)
	}
	if _, ok := pending["note-1"]; ok {
		t.Fatalf("passive notify frame was tracked as a pending dialog: %#v", pending)
	}

	// Passive frames still reach subscribers so the controller can project
	// them without any pending dialog state.
	mu.Lock()
	passive := false
	for _, event := range seen {
		if event.event["type"] == "extension_ui_request" && event.event["method"] == "notify" {
			passive = true
		}
	}
	mu.Unlock()
	if !passive {
		t.Fatal("passive notify frame did not reach subscribers")
	}

	ctx := context.Background()
	// Confirm: the controller sends the browser's boolean as value.
	if _, err := supervisor.callHost(ctx, "session.uiResponse", map[string]any{"sessionId": id, "requestId": "dlg-confirm", "value": true}); err != nil {
		t.Fatalf("confirm response: %v", err)
	}
	// Select: an explicit cancel writes cancelled.
	if _, err := supervisor.callHost(ctx, "session.uiCancel", map[string]any{"sessionId": id, "requestId": "dlg-select"}); err != nil {
		t.Fatalf("select cancel: %v", err)
	}
	// Input: the browser's cancelled answer arrives through uiResponse.
	if _, err := supervisor.callHost(ctx, "session.uiResponse", map[string]any{"sessionId": id, "requestId": "dlg-input", "cancelled": true}); err != nil {
		t.Fatalf("input cancel: %v", err)
	}

	// A settled id cannot be answered again.
	_, err := supervisor.callHost(ctx, "session.uiResponse", map[string]any{"sessionId": id, "requestId": "dlg-confirm", "value": true})
	var stale *staleUiRequestError
	if err == nil || !errors.As(err, &stale) {
		t.Fatalf("replayed confirm error = %v, want staleUiRequestError", err)
	}
	// A passive id is never answerable.
	_, err = supervisor.callHost(ctx, "session.uiCancel", map[string]any{"sessionId": id, "requestId": "note-1"})
	if err == nil || !errors.As(err, &stale) {
		t.Fatalf("passive cancel error = %v, want staleUiRequestError", err)
	}
	// A session without a resident child is a typed error.
	_, err = supervisor.callHost(ctx, "session.uiCancel", map[string]any{"sessionId": "missing", "requestId": "dlg-select"})
	var missingChild *missingUiChildError
	if err == nil || !errors.As(err, &missingChild) {
		t.Fatalf("absent child error = %v, want missingUiChildError", err)
	}

	frames := waitForUiFrames(t, logPath, 3)
	byID := map[string]map[string]any{}
	for _, frame := range frames {
		byID[frame["id"].(string)] = frame
	}
	if confirmed, ok := byID["dlg-confirm"]; !ok || confirmed["confirmed"] != true {
		t.Fatalf("confirm frame = %#v", byID["dlg-confirm"])
	}
	if selected, ok := byID["dlg-select"]; !ok || selected["cancelled"] != true {
		t.Fatalf("select frame = %#v", byID["dlg-select"])
	}
	if input, ok := byID["dlg-input"]; !ok || input["cancelled"] != true {
		t.Fatalf("input frame = %#v", byID["dlg-input"])
	}
	for _, frame := range frames {
		if frame["type"] != "extension_ui_response" {
			t.Fatalf("unexpected response type: %#v", frame)
		}
	}
}
