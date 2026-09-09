package controller_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
)

type limitBlockAllHandler struct {
	started atomic.Int32
	release chan struct{}
}

func (h *limitBlockAllHandler) Handle(_ context.Context, _ string, _ json.RawMessage, _ string) (any, error) {
	h.started.Add(1)
	<-h.release
	return map[string]any{"ok": true}, nil
}

type limitControlAwareHandler struct {
	ordinaryStarted atomic.Int32
	controlCalls    atomic.Int32
	release         chan struct{}
}

func (h *limitControlAwareHandler) Handle(_ context.Context, method string, _ json.RawMessage, _ string) (any, error) {
	if controller.IsBrowserControlMethod(method) {
		h.controlCalls.Add(1)
		return map[string]any{"ok": true}, nil
	}
	h.ordinaryStarted.Add(1)
	<-h.release
	return map[string]any{"ok": true}, nil
}

func newLimitTestServer(t *testing.T, server *controller.WebSocketServer) string {
	t.Helper()
	host := httptest.NewServer(server)
	t.Cleanup(host.Close)
	setWebSocketListenerPort(t, server, host)
	return host.URL
}

func waitForAtomicLimit(t *testing.T, ctx context.Context, counter *atomic.Int32, want int32) {
	t.Helper()
	for {
		if counter.Load() >= want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %d admissions, got %d", want, counter.Load())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestBrowserFrameBoundRejectsOversize(t *testing.T) {
	oversize := make([]byte, controller.BrowserFrameMaxBytes+1)
	if err := controller.ValidateBrowserFrame(oversize); err == nil {
		t.Fatal("oversize frame was admitted")
	}
	valid := []byte(`{"id":"one","method":"mutate","params":{}}`)
	if err := controller.ValidateBrowserFrame(valid); err != nil {
		t.Fatalf("valid frame rejected: %v", err)
	}
	if controller.BrowserFrameMaxBytes != 32*1024*1024 {
		t.Fatalf("frame bound moved: %d", controller.BrowserFrameMaxBytes)
	}
	if controller.BrowserAggregateMaxBytes != 64*1024*1024 {
		t.Fatalf("aggregate bound moved: %d", controller.BrowserAggregateMaxBytes)
	}
}

func TestBrowserDecodedBoundRejectsInvalidEnvelopes(t *testing.T) {
	longID := strings.Repeat("a", controller.BrowserIDMaxBytes+1)
	longMethod := strings.Repeat("m", controller.BrowserMethodMaxBytes+1)
	manyKeys := `{"id":"one","method":"m","k1":1,"k2":1,"k3":1,"k4":1,"k5":1,"k6":1,"k7":1}`
	ackMany := `{"ack":[` + strings.Repeat(`"x",`, controller.BrowserAckMaxIDs+1) + `"x"]}`
	cases := map[string]string{
		"too many keys":   manyKeys,
		"id too long":     fmt.Sprintf(`{"id":%q,"method":"m"}`, longID),
		"method too long": fmt.Sprintf(`{"id":"one","method":%q}`, longMethod),
		"missing method":  `{"id":"one"}`,
		"ack with method": `{"ack":["one"],"method":"m"}`,
		"ack fan-out":     ackMany,
		"not an object":   `[1,2,3]`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if err := controller.ValidateBrowserFrame([]byte(payload)); err == nil {
				t.Fatalf("invalid envelope admitted: %s", payload[:min(64, len(payload))])
			}
		})
	}
	if err := controller.ValidateBrowserFrame([]byte{0xff, 0xfe}); err == nil {
		t.Fatal("invalid UTF-8 frame was admitted")
	}
	if !controller.IsBrowserControlMethod("session.abort") || !controller.IsBrowserControlMethod("session.uiCancel") {
		t.Fatal("control lane must admit Stop and UI cancellation")
	}
	if controller.IsBrowserControlMethod("session.getMessages") {
		t.Fatal("history work must stay ordinary")
	}
}

func TestBrowserAdmissionAggregateCapAndControlReserve(t *testing.T) {
	limits := controller.BrowserAdmissionLimits{
		OrdinaryPerEngine:     4,
		OrdinaryPerConnection: 2,
		AggregateMaxBytes:     100,
		ControlMax:            2,
		ControlFrameMaxBytes:  10,
		ControlReserveBytes:   20,
	}
	admission := controller.NewBrowserAdmission(limits)
	if !admission.TryAcquireOrdinary("client", 60) {
		t.Fatal("first ordinary reservation rejected")
	}
	if !admission.TryAcquireOrdinary("client", 40) {
		t.Fatal("aggregate boundary reservation rejected")
	}
	if admission.TryAcquireOrdinary("client", 1) {
		t.Fatal("aggregate cap overflow admitted")
	}
	// Control reserve stays independent of ordinary saturation.
	if !admission.TryAcquireControl(10) {
		t.Fatal("control reserve was consumed by ordinary load")
	}
	admission.ReleaseOrdinary("client", 60)
	if !admission.TryAcquireOrdinary("other", 50) {
		t.Fatal("aggregate release did not free admission")
	}
	// Per-connection bound: other already holds 50, one more of 10 is its
	// second; a third must fail even though engine/aggregate allow it.
	if !admission.TryAcquireOrdinary("other", 10) {
		t.Fatal("second per-connection slot rejected")
	}
	if admission.TryAcquireOrdinary("other", 1) {
		t.Fatal("per-connection cap overflow admitted")
	}
	// Control frame bound: larger than the lane maximum never admits.
	if admission.TryAcquireControl(11) {
		t.Fatal("oversize control frame admitted")
	}
	admission.ReleaseControl(10)
	if !admission.TryAcquireControl(10) {
		t.Fatal("control release did not free the reserve")
	}
}

func TestBrowserInflightCapRejectsOrdinaryOverflow(t *testing.T) {
	handler := &limitBlockAllHandler{release: make(chan struct{})}
	server, err := controller.NewWebSocketServer(handler, nil, controller.AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	defer close(handler.release)
	host := newLimitTestServer(t, server)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connection := dialBrowserSocket(t, ctx, host, "limit-cap")
	for i := 0; i < controller.BrowserOrdinaryInflightPerConnection; i++ {
		payload := fmt.Sprintf(`{"id":"limit-%d","method":"mutate","params":{}}`, i)
		if err := connection.Write(ctx, websocket.MessageText, []byte(payload)); err != nil {
			t.Fatalf("ordinary write %d: %v", i, err)
		}
	}
	waitForAtomicLimit(t, ctx, &handler.started, int32(controller.BrowserOrdinaryInflightPerConnection))
	overflow := []byte(`{"id":"limit-overflow","method":"mutate","params":{}}`)
	_ = connection.Write(ctx, websocket.MessageText, overflow)
	readCtx, readCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer readCancel()
	_, _, readErr := connection.Read(readCtx)
	if readErr == nil {
		t.Fatal("ordinary in-flight overflow was admitted")
	}
	if closeErr := websocket.CloseStatus(readErr); closeErr != websocket.StatusTryAgainLater {
		t.Fatalf("overflow close status = %v, want TryAgainLater", closeErr)
	}
}

func TestBrowserControlReserveAdmittedUnderLoad(t *testing.T) {
	handler := &limitControlAwareHandler{release: make(chan struct{})}
	server, err := controller.NewWebSocketServer(handler, nil, controller.AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	defer close(handler.release)
	host := newLimitTestServer(t, server)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connection := dialBrowserSocket(t, ctx, host, "limit-control")
	for i := 0; i < controller.BrowserOrdinaryInflightPerConnection; i++ {
		payload := fmt.Sprintf(`{"id":"ordinary-%d","method":"mutate","params":{}}`, i)
		if err := connection.Write(ctx, websocket.MessageText, []byte(payload)); err != nil {
			t.Fatalf("ordinary write %d: %v", i, err)
		}
	}
	waitForAtomicLimit(t, ctx, &handler.ordinaryStarted, int32(controller.BrowserOrdinaryInflightPerConnection))
	control := []byte(`{"id":"stop-1","method":"session.abort","params":{"sessionId":"s"}}`)
	if err := connection.Write(ctx, websocket.MessageText, control); err != nil {
		t.Fatalf("control write under load: %v", err)
	}
	readCtx, readCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer readCancel()
	_, raw, readErr := connection.Read(readCtx)
	if readErr != nil {
		t.Fatalf("control reserve was not admitted under ordinary load: %v", readErr)
	}
	var response map[string]any
	if err := json.Unmarshal(raw, &response); err != nil || response["id"] != "stop-1" || response["ok"] != true {
		t.Fatalf("unexpected control response %s: %v", raw, err)
	}
	if handler.controlCalls.Load() != 1 {
		t.Fatalf("control handler calls = %d, want 1", handler.controlCalls.Load())
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
