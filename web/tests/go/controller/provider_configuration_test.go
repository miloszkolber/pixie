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

func TestDefaultConnectionValuesDoNotConfigureProviders(t *testing.T) {
	for _, id := range []string{"claude-code", "atomic_chat", "cursor-agent", "gemini-cli", "future-local-provider"} {
		t.Run(id, func(t *testing.T) {
			for _, explicit := range []bool{false, true} {
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
						var result any = map[string]any{}
						switch rpc.Method {
						case "runtime.hello":
							result = piInitializeResponse()
						case "pi.providers.list":
							result = map[string]any{"entries": []any{map[string]any{"providerId": id, "name": id, "configured": true, "available": true, "visibleInSetup": true, "configKeys": []any{map[string]any{"name": "COMMAND", "required": true, "default": "cli"}}, "models": []any{map[string]any{"id": "model"}}}}}
						case "pi.providers.config.read":
							result = map[string]any{"fields": []any{map[string]any{"name": "COMMAND", "isSet": explicit, "value": "must-not-reach-ui"}}}
						}
						if writeRPC(conn, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result}) != nil {
							return
						}
					}
				}))
				client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", nil)
				admin := controller.NewPiAdmin(client, controller.NewSettings(persist.Store{Dir: t.TempDir()}, nil))
				report, err := admin.ProviderStatus(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				provider := report["providers"].([]map[string]any)[0]
				if provider["configured"] != explicit {
					t.Fatalf("explicit=%v: %#v", explicit, provider)
				}
				encoded, _ := json.Marshal(report)
				if strings.Contains(string(encoded), "must-not-reach-ui") {
					t.Fatal("configuration value leaked")
				}
				models, err := admin.Models(context.Background())
				if err != nil || len(models) != 1 || models[0].Available != explicit {
					t.Fatalf("selector inventory: %#v %v", models, err)
				}
				client.Close()
				server.Close()
			}
		})
	}
}
