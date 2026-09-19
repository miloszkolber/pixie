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

// AUX-19: session.queueAdd and session.queueRetry can schedule a follow-up
// prompt, so they are run-creating admission. During a drain the browser
// receives the typed quiescing refusal before the handler runs, and no queued
// work is added inside the quiesce window.
func TestWebSocketDrainRefusesQueuedFollowUpAdmission(t *testing.T) {
	handler := &countingHandler{}
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
	connection := dialBrowserSocket(t, ctx, host.URL, "drain-queue")

	server.BeginDrain()

	send := func(frame string) map[string]any {
		t.Helper()
		if err := connection.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
			t.Fatal(err)
		}
		_, raw, err := connection.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var response map[string]any
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatalf("response %s: %v", raw, err)
		}
		return response
	}
	for _, method := range []string{"session.queueAdd", "session.queueRetry"} {
		response := send(`{"id":"` + method + `","method":"` + method + `","params":{"sessionId":"s1","lane":"followUp","index":0,"revision":"r","text":"later"}}`)
		if response["ok"] != false || response["errorCode"] != "controller_quiescing" {
			t.Fatalf("draining %s response = %#v", method, response)
		}
	}
	if handler.calls.Load() != 0 {
		t.Fatalf("draining queued admission reached the handler: calls=%d", handler.calls.Load())
	}
}

// AUX-19: a queued follow-up admitted before the drain is not revoked. Its
// admission stays in-flight until it settles, so WaitForDrain does not report
// completion early and new queueAdd/queueRetry work stays refused.
func TestWebSocketDrainWaitsForAdmittedQueuedFollowUp(t *testing.T) {
	handler := &inflightHandler{started: make(chan struct{}, 1), release: make(chan struct{})}
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
	connection := dialBrowserSocket(t, ctx, host.URL, "drain-queue-work")
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"id":"queue","method":"session.queueAdd","params":{"sessionId":"s1","text":"later"}}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handler.started:
	case <-ctx.Done():
		t.Fatal("admitted queued follow-up did not start")
	}
	server.BeginDrain()
	drained := make(chan struct{})
	go func() {
		server.WaitForDrain(context.Background())
		close(drained)
	}()
	settledEarly := false
	select {
	case <-drained:
		settledEarly = true
	case <-time.After(50 * time.Millisecond):
	}
	// Release the admitted work before reporting an early settle so a failing
	// regression still tears the server down instead of deadlocking cleanup.
	close(handler.release)
	select {
	case <-drained:
	case <-ctx.Done():
		t.Fatal("drain did not settle after the admitted queued follow-up released")
	}
	if settledEarly {
		t.Fatal("drain settled before the admitted queued follow-up released")
	}
}
