package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	piwire "github.com/miloszkolber/pixie/piprotocol"
)

// nativeErrorHandler fails every request with the supplied error so the
// transport boundary is exercised without a session.
type nativeErrorHandler struct{ err error }

func (h nativeErrorHandler) Handle(context.Context, string, json.RawMessage, string) (any, error) {
	return nil, h.err
}

func dialErrorBoundary(t *testing.T, handler Handler) (*websocket.Conn, func()) {
	t.Helper()
	server, err := NewWebSocketServer(handler, nil, AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(server)
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
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(host.URL, "http"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {host.URL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		connection.CloseNow()
		server.Close(context.Background())
		host.Close()
		cancel()
	}
	return connection, cleanup
}

func readErrorReply(t *testing.T, connection *websocket.Conn, id string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, raw, err := connection.Read(ctx)
		if err != nil {
			t.Fatalf("read error reply: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode error reply: %v", err)
		}
		if payload["id"] != id {
			continue
		}
		return payload
	}
}

// AUX-32: a native host error with paths, URLs and credentials must reach the
// browser as the fixed typed message, never as raw native text.
func TestWebSocketNormalizesNativeHostErrorForBrowser(t *testing.T) {
	forbidden := []string{
		"sk-live-uncovered-credential",
		"/home/operator/.pi/auth.json",
		"https://operator:token-value@example.test/mcp",
		"raw-bearer-secret",
	}
	hostErr := &piwire.RequestError{
		Code:    int(piwire.HostV2CodeCapabilityUnavailable),
		Message: "Pi SDK rejected the request: " + strings.Join(forbidden, " "),
	}
	connection, cleanup := dialErrorBoundary(t, nativeErrorHandler{err: hostErr})
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"id":"native","method":"session.prompt","params":{}}`)); err != nil {
		t.Fatal(err)
	}
	payload := readErrorReply(t, connection, "native")
	if payload["ok"] != false {
		t.Fatalf("native error reply was not a failure: %#v", payload)
	}
	want := piwire.BrowserErrorMessageForHostCode(int(piwire.HostV2CodeCapabilityUnavailable))
	if payload["error"] != want {
		t.Fatalf("browser error = %#v, want %q", payload["error"], want)
	}
	serialized, _ := json.Marshal(payload)
	for _, value := range forbidden {
		if strings.Contains(string(serialized), value) {
			t.Fatalf("browser reply leaked native value %q: %s", value, serialized)
		}
	}
}

// AUX-32/AUX-21: a controller-owned typed error keeps its stable code and a
// bounded, redacted message; a raw path in the cause is not reflected.
func TestWebSocketRedactsControllerOwnedTypedError(t *testing.T) {
	connection, cleanup := dialErrorBoundary(t, nativeErrorHandler{err: &codedError{
		code:    "PATH_ESCAPES_PROJECT_ROOT",
		message: "path escapes the admitted workspace: /home/operator/.pi/auth.json",
	}})
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"id":"coded","method":"fs.readFile","params":{}}`)); err != nil {
		t.Fatal(err)
	}
	payload := readErrorReply(t, connection, "coded")
	if payload["errorCode"] != "PATH_ESCAPES_PROJECT_ROOT" {
		t.Fatalf("typed error code = %#v", payload["errorCode"])
	}
	message, _ := payload["error"].(string)
	if message == "" || len(message) > browserErrorMaxBytes {
		t.Fatalf("controller error message is not bounded: %q", message)
	}
	if strings.Contains(message, "/home/operator/.pi/auth.json") {
		t.Fatalf("controller error message leaked a path: %q", message)
	}
}
