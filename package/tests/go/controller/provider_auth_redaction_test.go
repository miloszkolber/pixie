package controller_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

func TestProviderStatusRedactsSecretsFromAuthFailureDetail(t *testing.T) {
	configured := true
	secret := "sk-live-fixture-secret-value"
	providers := []map[string]any{
		{"providerId": "benign", "configured": configured, "available": true, "lastRefreshError": "authentication failed", "models": []any{map[string]any{"id": "model"}}},
		{"providerId": "leaky", "configured": configured, "available": true, "lastRefreshError": "authentication failed for api_key=" + secret, "models": []any{map[string]any{"id": "model"}}},
		{"providerId": "bearer", "configured": configured, "available": true, "lastRefreshError": "OAuth refresh rejected: Bearer fixture-bearer-credential", "models": []any{map[string]any{"id": "model"}}},
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.CloseNow()
		for {
			_, payload, err := connection.Read(context.Background())
			if err != nil {
				return
			}
			var rpc struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if err := json.Unmarshal(payload, &rpc); err != nil {
				return
			}
			var result any = map[string]any{}
			if rpc.Method == "runtime.hello" {
				result = piInitializeResponse()
			} else if rpc.Method == "pi.providers.list" {
				result = map[string]any{"entries": providers}
			}
			if err := writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result}); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", nil)
	defer client.Close()
	admin := controller.NewPiAdmin(client, controller.NewSettings(persist.Store{Dir: t.TempDir()}, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, err := admin.ProviderStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(status)
	for _, leaked := range []string{secret, "fixture-bearer-credential"} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("provider secret reached diagnostics output: %s", encoded)
		}
	}
	details := map[string]string{}
	for _, provider := range status["providers"].([]map[string]any) {
		details[provider["id"].(string)] = provider["detail"].(string)
	}
	if details["benign"] != "authentication failed" {
		t.Fatalf("benign failure detail changed: %q", details["benign"])
	}
	for _, id := range []string{"leaky", "bearer"} {
		if !strings.Contains(details[id], "authentication failed") && !strings.Contains(details[id], "rejected") {
			t.Fatalf("%s no longer reports the failure: %q", id, details[id])
		}
		if !strings.Contains(details[id], "[redacted]") {
			t.Fatalf("%s failure does not mark redaction: %q", id, details[id])
		}
	}
}
