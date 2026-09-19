package shared_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/piprotocol"
)

// TestHostMethodSchemaFailsAtControllerBoundary proves the shared method-level
// runtime schemas fail a malformed request before the host is written to and an
// invalid result before it reaches the controller projection.
func TestHostMethodSchemaFailsAtControllerBoundary(t *testing.T) {
	var mu sync.Mutex
	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			_, raw, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var rpc struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if json.Unmarshal(raw, &rpc) != nil {
				return
			}
			mu.Lock()
			received = append(received, rpc.Method)
			mu.Unlock()
			var result any
			switch rpc.Method {
			case "runtime.hello":
				result = map[string]any{
					"protocolVersion": 1,
					"runtimeId":       "schema-fixture",
					"bootId":          "schema-boot",
					"version":         "0.85.1",
					"capabilities":    map[string]any{"sessions": 1},
					"operationSet":    map[string]bool{"session.list": true, "session.prompt": true, "session.create": true, "session.load": true, "session.cancel": true},
				}
			case "session.list":
				// Invalid on purpose: the result schema requires `sessions`.
				result = map[string]any{"unexpected": true}
			case "session.prompt":
				result = map[string]any{"ok": true}
			default:
				result = map[string]any{}
			}
			payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
			if conn.Write(r.Context(), websocket.MessageText, payload) != nil {
				return
			}
		}
	}))
	defer server.Close()

	client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", nil)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// A malformed request is rejected before dispatch. The host must not see the
	// method, so validation cannot be a post-hoc wrapper around a side effect.
	if _, err := client.CallPi(ctx, "session.prompt", map[string]any{}); err == nil {
		t.Fatal("malformed session.prompt params were accepted")
	}
	mu.Lock()
	for _, method := range received {
		if method == "session.prompt" {
			mu.Unlock()
			t.Fatal("malformed request reached the host")
		}
	}
	mu.Unlock()

	// An invalid result fails before it is projected as a successful list.
	if _, err := client.ListSessions(ctx, piprotocol.ListSessionsRequest{}); err == nil {
		t.Fatal("invalid session.list result was accepted")
	}
	mu.Lock()
	sawList := false
	for _, method := range received {
		if method == "session.list" {
			sawList = true
		}
	}
	mu.Unlock()
	if !sawList {
		t.Fatal("session.list was not dispatched")
	}
}
