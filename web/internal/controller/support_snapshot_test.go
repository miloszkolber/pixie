package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/diagnostics"
)

func TestSupportSnapshotRequiresExplicitControllerAuthentication(t *testing.T) {
	var called atomic.Bool
	ring := diagnostics.NewControllerEventRing()
	handler := CoreHandler{
		SupportSnapshot: func(context.Context) (json.RawMessage, error) {
			called.Store(true)
			return json.RawMessage(`{"schemaVersion":1}`), nil
		},
		ControllerEvents: ring,
	}
	_, err := handler.Handle(t.Context(), "runtime.supportSnapshot", nil, "client")
	var denied *SupportSnapshotAuthenticationRequiredError
	if !errors.As(err, &denied) {
		t.Fatalf("unauthenticated support snapshot error = %v", err)
	}
	if called.Load() {
		t.Fatal("unauthenticated export called its snapshot provider")
	}
	if denied.ErrorCode() != "SUPPORT_SNAPSHOT_AUTH_REQUIRED" {
		t.Fatalf("denial code = %q", denied.ErrorCode())
	}
	events := ring.Snapshot()
	if len(events) != 1 || events[0].Operation != "runtime.supportSnapshot" || events[0].Outcome != "failed" || events[0].Detail != "request.authentication_required" {
		t.Fatalf("denial event = %#v", events)
	}
}

func TestSupportSnapshotControllerEventDoesNotRetainUnknownMethodText(t *testing.T) {
	rawMethod := "project-hostile-id Authorization: Basic YWxpY2U6YmFzaWMtcGFzcw== https://url-user:url-password@example.invalid/?token=query-token-value /home/alice/.pi"
	ring := diagnostics.NewControllerEventRing()
	handler := CoreHandler{ControllerEvents: ring}
	if _, err := handler.Handle(t.Context(), rawMethod, nil, "client"); err == nil {
		t.Fatal("unknown method unexpectedly succeeded")
	}
	payload, err := diagnostics.MarshalSupportSnapshot(diagnostics.SupportSnapshotRuntime{}, ring.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"project-hostile-id", "YWxpY2U6YmFzaWMtcGFzcw==", "url-user", "url-password", "example.invalid", "query-token-value", "/home/alice/.pi"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("support snapshot leaked %q: %s", forbidden, payload)
		}
	}
	var snapshot diagnostics.SupportSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Events) != 1 || snapshot.Events[0].Operation != "unknown" || snapshot.Events[0].Outcome != "failed" || snapshot.Events[0].Detail != "request.failed" {
		t.Fatalf("support snapshot event = %#v", snapshot.Events)
	}
}

func TestSupportSnapshotReturnsPreboundedJSONWhenAuthenticationIsEnabled(t *testing.T) {
	payload := json.RawMessage(`{"schemaVersion":1,"events":[]}`)
	handler := CoreHandler{
		SupportSnapshotAuthEnabled: true,
		SupportSnapshot: func(context.Context) (json.RawMessage, error) {
			return payload, nil
		},
	}
	result, err := handler.Handle(t.Context(), "runtime.supportSnapshot", json.RawMessage(`{}`), "client")
	if err != nil {
		t.Fatal(err)
	}
	actual, ok := result.(json.RawMessage)
	if !ok || string(actual) != string(payload) {
		t.Fatalf("support snapshot result = %#v", result)
	}
}

func TestWebSocketRejectsSupportSnapshotWithoutControllerAuthentication(t *testing.T) {
	var called atomic.Bool
	handler := CoreHandler{
		SupportSnapshotAuthEnabled: true,
		SupportSnapshot: func(context.Context) (json.RawMessage, error) {
			called.Store(true)
			return json.RawMessage(`{"schemaVersion":1,"events":[]}`), nil
		},
	}
	server, err := NewWebSocketServer(handler, nil, AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	host := httptest.NewServer(server)
	defer host.Close()
	parsed, err := url.Parse(host.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatal(err)
	}
	server.Auth.ControllerPort = port
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(host.URL, "http"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {host.URL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"id":"snapshot","method":"runtime.supportSnapshot","params":{}}`)); err != nil {
		t.Fatal(err)
	}
	_, response, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		OK        bool   `json:"ok"`
		ErrorCode string `json:"errorCode"`
	}
	if err := json.Unmarshal(response, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.OK || payload.ErrorCode != "SUPPORT_SNAPSHOT_AUTH_REQUIRED" || called.Load() {
		t.Fatalf("unauthenticated websocket snapshot = %s; provider called=%t", response, called.Load())
	}
}
