package controller_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

func TestPiAdminModelsRequireUsableProviderInventory(t *testing.T) {
	configured := true
	providers := []map[string]any{
		{"providerId": "missing", "models": []any{map[string]any{"id": "model"}}},
		{"providerId": "false", "configured": false, "models": []any{map[string]any{"id": "model"}}},
		{"providerId": "unavailable", "configured": configured, "available": false, "models": []any{map[string]any{"id": "model"}}},
		{"providerId": "refresh-error", "configured": configured, "available": true, "lastRefreshError": "authentication failed", "models": []any{map[string]any{"id": "model"}}},
		{"providerId": "available", "configured": configured, "available": true, "models": []any{map[string]any{"id": "model"}}},
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
			} else if rpc.Method == "pi.defaults.save" {
				result = map[string]any{"providerId": "refresh-error", "modelId": nil}
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
	models, err := admin.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"missing": false, "false": false, "unavailable": false, "refresh-error": true, "available": true}
	for _, model := range models {
		if model.ID != "model" {
			t.Fatalf("unexpected model: %#v", model)
		}
		if model.Available != want[model.Provider] {
			t.Errorf("%s availability = %v, want %v", model.Provider, model.Available, want[model.Provider])
		}
		delete(want, model.Provider)
	}
	if len(want) != 0 {
		t.Fatalf("missing providers: %#v", want)
	}
	status, err := admin.ProviderStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range status["providers"].([]map[string]any) {
		if provider["id"] != "refresh-error" {
			continue
		}
		if provider["available"] != true || provider["availableModelCount"] != 1 || provider["detail"] != "authentication failed" {
			t.Fatalf("inventory failure changed runtime availability: %#v", provider)
		}
		if _, err := admin.SaveDefaults(ctx, "refresh-error", nil); err != nil {
			t.Fatalf("discovery failure incorrectly blocked the reported available runtime: %v", err)
		}
		return
	}
	t.Fatal("missing refresh-error provider status")
}

func TestPiAdminUsesReleaseMatchedExtensionsAndGenericPreferenceRemoval(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	var removed []string
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
				Params map[string]any  `json:"params"`
			}
			if json.Unmarshal(payload, &rpc) != nil {
				return
			}
			mu.Lock()
			methods = append(methods, rpc.Method)
			mu.Unlock()
			var result any = map[string]any{}
			switch rpc.Method {
			case "runtime.hello":
				result = piInitializeResponse()
			case "pi.config.extensions.list":
				result = map[string]any{"extensions": []any{}, "warnings": []any{}}
			case "pi.preferences.reset":
				mu.Lock()
				for _, key := range rpc.Params["keys"].([]any) {
					removed = append(removed, key.(string))
				}
				mu.Unlock()
			case "pi.preferences.read":
				result = map[string]any{"values": []any{
					map[string]any{"key": "compactionReserveTokens", "value": nil},
					map[string]any{"key": "piThinkingEffort", "value": nil},
				}}
			}
			if writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result}) != nil {
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
	catalogValue, err := admin.Handle(ctx, "pi.extensionList", []byte(`{}`), "client")
	if err != nil {
		t.Fatal(err)
	}
	catalog := catalogValue.(map[string]any)
	available := catalog["available"].([]map[string]any)
	if len(available) != 0 {
		t.Fatalf("unexpected bundled extension catalog: %#v", available)
	}
	if _, err := admin.ResetPreferences(ctx, []string{"compactionReserveTokens", "piThinkingEffort"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(removed, ",") != "compactionReserveTokens,piThinkingEffort" {
		t.Fatalf("removed keys = %v", removed)
	}
	for _, method := range methods {
		if method == "pi.extensions.available" || method == "pi.preferences.remove" {
			t.Fatalf("called removed Pi method %s", method)
		}
	}
}

func TestPiAdminRefreshModelsWaitsForInventoryCompletion(t *testing.T) {
	var providerReads atomic.Int32
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
			if json.Unmarshal(payload, &rpc) != nil {
				return
			}
			var result any = map[string]any{}
			switch rpc.Method {
			case "runtime.hello":
				result = piInitializeResponse()
			case "pi.providers.inventory.refresh":
				result = map[string]any{"started": []string{"dynamic"}}
			case "pi.providers.list":
				read := providerReads.Add(1)
				result = map[string]any{"entries": []any{map[string]any{"providerId": "dynamic", "configured": true, "available": true, "refreshing": read == 1}}}
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
	result, err := admin.RefreshModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result["complete"] != true || providerReads.Load() < 3 {
		t.Fatalf("refresh returned before the completed inventory was reloaded: %#v, reads=%d", result, providerReads.Load())
	}
}

func TestPiAdminRefreshModelsRejectsMissingRefreshingProvider(t *testing.T) {
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
			if json.Unmarshal(payload, &rpc) != nil {
				return
			}
			var result any = map[string]any{}
			if rpc.Method == "runtime.hello" {
				result = piInitializeResponse()
			} else if rpc.Method == "pi.providers.inventory.refresh" {
				result = map[string]any{"started": []string{"removed"}}
			} else if rpc.Method == "pi.providers.list" {
				result = map[string]any{"entries": []any{}}
			}
			if writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result}) != nil {
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
	if _, err := admin.RefreshModels(ctx); err == nil || !strings.Contains(err.Error(), "lost provider removed") {
		t.Fatalf("missing refresh provider was not rejected: %v", err)
	}
}

func TestPiAdminRefreshModelsReportsIncompleteCanonicalMetadata(t *testing.T) {
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
			if json.Unmarshal(payload, &rpc) != nil {
				return
			}
			if rpc.Method == "pi.providers.canonical-model-info" {
				continue
			}
			var result any = map[string]any{}
			if rpc.Method == "runtime.hello" {
				result = piInitializeResponse()
			} else if rpc.Method == "pi.providers.inventory.refresh" {
				result = map[string]any{"started": []string{}}
			} else if rpc.Method == "pi.providers.list" {
				result = map[string]any{"entries": []any{map[string]any{"providerId": "available", "configured": true, "available": true, "models": []any{map[string]any{"id": "slow-model"}}}}}
			}
			if writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result}) != nil {
				return
			}
		}
	}))
	defer server.Close()

	client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", nil)
	defer client.Close()
	admin := controller.NewPiAdmin(client, controller.NewSettings(persist.Store{Dir: t.TempDir()}, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	result, err := admin.RefreshModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result["complete"] != false || len(result["models"].([]controller.WireModel)) != 1 {
		t.Fatalf("canonical timeout was reported as complete: %#v", result)
	}
}
