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

// promptBlockingHandler blocks only a run-creating method so a read-only
// request can complete while an admitted prompt is still in flight.
type promptBlockingHandler struct {
	started chan struct{}
	release chan struct{}
}

func (h *promptBlockingHandler) Handle(_ context.Context, method string, _ json.RawMessage, _ string) (any, error) {
	if method == "session.prompt" {
		select {
		case h.started <- struct{}{}:
		default:
		}
		<-h.release
	}
	return map[string]any{"method": method}, nil
}

// AUX-19 admission accounting: a non-gated request completing during a drain
// must not settle the admitted runnable slot. WaitForDrain keeps blocking until
// the admitted prompt itself finishes.
func TestWebSocketDrainNonGatedRequestDoesNotReleaseAdmittedWork(t *testing.T) {
	handler := &promptBlockingHandler{started: make(chan struct{}, 1), release: make(chan struct{})}
	server, err := controller.NewWebSocketServer(handler, nil, controller.AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	host := httptest.NewServer(server)
	defer host.Close()
	setWebSocketListenerPort(t, server, host)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection := dialBrowserSocket(t, ctx, host.URL, "drain-non-gated")

	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"id":"prompt","method":"session.prompt","params":{"sessionId":"s1","text":"go"}}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handler.started:
	case <-ctx.Done():
		t.Fatal("admitted prompt did not start")
	}
	server.BeginDrain()
	drained := make(chan struct{})
	go func() {
		server.WaitForDrain(context.Background())
		close(drained)
	}()

	// A read-only request admitted and completed during the drain must not
	// release the admitted prompt's accounting slot.
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"id":"read","method":"session.getMessages","params":{"sessionId":"s1"}}`)); err != nil {
		t.Fatal(err)
	}
	_, raw, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(raw, &response); err != nil || response["ok"] != true {
		t.Fatalf("read-only response %s: %v", raw, err)
	}
	settledEarly := false
	select {
	case <-drained:
		settledEarly = true
	case <-time.After(50 * time.Millisecond):
	}
	// Release the admitted prompt before reporting an early settle so a failing
	// regression still tears the server down instead of deadlocking cleanup.
	close(handler.release)
	select {
	case <-drained:
	case <-ctx.Done():
		t.Fatal("drain did not settle after the admitted prompt released")
	}
	if settledEarly {
		t.Fatal("a non-gated request settled WaitForDrain while an admitted prompt was in flight")
	}
}
