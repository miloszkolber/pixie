package controller_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
)

// AUX-19 drain truth: a browser session.prompt admitted before BeginDrain must
// keep WaitForDrain blocked until the asynchronous agent run settles, not
// merely until the browser dispatch returns. The dispatch responds immediately
// with its ack, while the native prompt stays in flight until the test answers
// it; the drain must still be waiting at that point.
func TestWebSocketDrainWaitsForAdmittedPromptRun(t *testing.T) {
	prompts := make(chan map[string]any, 4)
	manager, _, _, _ := newSessionManager(t, nil, prompts)
	server, err := controller.NewWebSocketServer(controller.CoreHandler{Sessions: manager}, nil, controller.AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	host := httptest.NewServer(server)
	defer host.Close()
	setWebSocketListenerPort(t, server, host)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection := dialBrowserSocket(t, ctx, host.URL, "drain-prompt-run")

	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"id":"prompt","method":"session.prompt","params":{"sessionId":"chat","text":"go"}}`)); err != nil {
		t.Fatal(err)
	}
	// The dispatch acknowledges immediately; the run it started is still in
	// flight and holding the drain admission.
	_, raw, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ack map[string]any
	if err := json.Unmarshal(raw, &ack); err != nil || ack["ok"] != true {
		t.Fatalf("prompt ack %s: %v", raw, err)
	}
	var request map[string]any
	select {
	case request = <-prompts:
	case <-time.After(2 * time.Second):
		t.Fatal("admitted prompt run did not reach the native prompt")
	}

	server.BeginDrain()
	drained := make(chan struct{})
	go func() {
		server.WaitForDrain(context.Background())
		close(drained)
	}()
	select {
	case <-drained:
		t.Fatal("WaitForDrain settled while the admitted prompt run was still in flight")
	case <-time.After(50 * time.Millisecond):
	}
	if err := writeRPC(request["connection"].(*websocket.Conn), map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"stopReason": "end_turn"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForDrain did not settle after the admitted prompt run finished")
	}
	params, _ := request["params"].(map[string]any)
	if text := promptText(params); text != "go" {
		t.Fatalf("dispatched prompt text = %q, want go", text)
	}
}
