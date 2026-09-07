package controller_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

func TestNativeInventoryProjectionOmitsSecretsAndRejectsWrongReader(t *testing.T) {
	for _, reader := range []string{"service", "session"} {
		t.Run(reader, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ws, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer ws.CloseNow()
				for {
					_, payload, err := ws.Read(r.Context())
					if err != nil {
						return
					}
					var rpc struct {
						ID     json.RawMessage `json:"id"`
						Method string          `json:"method"`
					}
					if json.Unmarshal(payload, &rpc) != nil {
						return
					}
					result := any(piInitializeResponse())
					if rpc.Method == "pi.extensions.list" {
						result = map[string]any{
							"version": 1, "context": map[string]any{"cwd": "/agent", "sessionId": nil, "reader": reader},
							"packages": []any{}, "paths": []any{}, "resources": []any{}, "extensions": []any{},
							"errors":   []any{map[string]any{"path": "https://user:private-token@example.test/pkg?token=secret-query", "error": "Bearer raw-secret", "code": "load-failed"}},
							"warnings": []string{"unknown-error-with-secret"}, "environment": map[string]any{"TOKEN": "env-secret"},
						}
					} else if rpc.Method != "runtime.hello" {
						t.Errorf("unexpected side-effecting request: %s", rpc.Method)
					}
					if writeRPC(ws, map[string]any{"id": rpc.ID, "result": result}) != nil {
						return
					}
				}
			}))
			defer server.Close()
			client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", nil)
			defer client.Close()
			admin := controller.NewPiAdmin(client, controller.NewSettings(persist.Store{Dir: t.TempDir()}, nil))
			result, err := admin.Handle(context.Background(), "pi.nativeExtensions", json.RawMessage(`{}`), "fixture")
			if reader != "service" {
				if err == nil {
					t.Fatal("wrong reader accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(result)
			for _, secret := range []string{"private-token", "secret-query", "raw-secret", "env-secret", "unknown-error-with-secret"} {
				if strings.Contains(string(encoded), secret) {
					t.Fatalf("secret leaked: %s", encoded)
				}
			}
			if !strings.Contains(string(encoded), "https://example.test/pkg") || !strings.Contains(string(encoded), "load-failed") {
				t.Fatalf("safe diagnostics lost: %s", encoded)
			}
		})
	}
}
