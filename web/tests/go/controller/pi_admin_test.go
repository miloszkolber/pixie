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
	"github.com/miloszkolber/pixie/internal/workspace"
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
					map[string]any{"key": "compactionReserveTokens", "value": nil, "writable": false, "source": "read-only"},
					map[string]any{"key": "piThinkingEffort", "value": "max", "writable": true, "source": "pi"},
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
	preferences, err := admin.ReadPreferences(ctx)
	if err != nil || preferences.PiThinkingEffort == nil || *preferences.PiThinkingEffort != "max" {
		t.Fatalf("maximum thinking effort was not preserved: %#v, %v", preferences, err)
	}
	if len(preferences.Keys) != 2 {
		t.Fatalf("preference projection omitted writability metadata: %#v", preferences.Keys)
	}
	for _, descriptor := range preferences.Keys {
		switch descriptor.Key {
		case "piThinkingEffort":
			if !descriptor.Writable || descriptor.Source != "pi" {
				t.Fatalf("thinking effort writability = %#v", descriptor)
			}
		case "compactionReserveTokens":
			if descriptor.Writable || descriptor.Source != "read-only" {
				t.Fatalf("compaction reserve writability = %#v", descriptor)
			}
		}
	}
	maxThinking := "max"
	if _, err := admin.SavePreferences(ctx, controller.PiPreferences{PiThinkingEffort: &maxThinking}); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ResetPreferences(ctx, []string{"compactionReserveTokens", "piThinkingEffort"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	// The read-only reserve has no stored value, so its reset is a tolerated
	// no-op and only the writable key reaches Pi.
	if strings.Join(removed, ",") != "piThinkingEffort" {
		t.Fatalf("removed keys = %v", removed)
	}
	for _, method := range methods {
		if method == "pi.extensions.available" || method == "pi.preferences.remove" {
			t.Fatalf("called removed Pi method %s", method)
		}
	}
}

func TestPiAdminPreferencesTolerateUnchangedReadOnlyKeyAndFailClosedOnChange(t *testing.T) {
	var mu sync.Mutex
	var savedValues [][]any
	var resetKeys [][]any
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
			var result any = map[string]any{}
			switch rpc.Method {
			case "runtime.hello":
				result = piInitializeResponse()
			case "pi.preferences.read":
				result = map[string]any{"values": []any{
					map[string]any{"key": "compactionReserveTokens", "value": 16384, "writable": false, "source": "read-only"},
					map[string]any{"key": "piThinkingEffort", "value": "high", "writable": true, "source": "pi"},
				}}
			case "pi.preferences.save":
				mu.Lock()
				if values, ok := rpc.Params["values"].([]any); ok {
					savedValues = append(savedValues, values)
				}
				mu.Unlock()
			case "pi.preferences.reset":
				mu.Lock()
				if keys, ok := rpc.Params["keys"].([]any); ok {
					resetKeys = append(resetKeys, keys)
				}
				mu.Unlock()
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

	current, err := admin.ReadPreferences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.CompactionReserveTokens == nil || *current.CompactionReserveTokens != 16384 {
		t.Fatalf("stored reserve was not projected: %#v", current)
	}
	if len(current.Keys) != 2 {
		t.Fatalf("projection omitted writability metadata: %#v", current.Keys)
	}

	reserve := 16384.0
	thinking := "high"
	// An unchanged read-only value must not fail and must not reach Pi.
	if _, err := admin.SavePreferences(ctx, controller.PiPreferences{CompactionReserveTokens: &reserve, PiThinkingEffort: &thinking}); err != nil {
		t.Fatalf("unchanged read-only preference was rejected: %v", err)
	}
	mu.Lock()
	if len(savedValues) != 1 {
		t.Fatalf("save calls = %#v", savedValues)
	}
	for _, entry := range savedValues[0] {
		if entry.(map[string]any)["key"] == "compactionReserveTokens" {
			t.Fatalf("read-only key reached Pi: %#v", entry)
		}
	}
	mu.Unlock()

	// A changed read-only value must fail closed before any save reaches Pi.
	changed := 2048.0
	if _, err := admin.SavePreferences(ctx, controller.PiPreferences{CompactionReserveTokens: &changed}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("changed read-only preference was not rejected: %v", err)
	}
	mu.Lock()
	if len(savedValues) != 1 {
		t.Fatalf("changed read-only preference reached Pi: %#v", savedValues)
	}
	mu.Unlock()

	// Resetting a read-only key that currently has a stored value must fail closed.
	if _, err := admin.ResetPreferences(ctx, []string{"compactionReserveTokens"}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("stored read-only reset was not rejected: %v", err)
	}
	// Resetting the writable key still reaches Pi.
	if _, err := admin.ResetPreferences(ctx, []string{"piThinkingEffort"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(resetKeys) != 1 || len(resetKeys[0]) != 1 || resetKeys[0][0] != "piThinkingEffort" {
		t.Fatalf("reset keys = %#v", resetKeys)
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

func TestPiAdminSaveDefaultsRejectsUnknownModel(t *testing.T) {
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
			switch rpc.Method {
			case "runtime.hello":
				result = piInitializeResponse()
			case "pi.providers.list":
				result = map[string]any{"entries": []any{map[string]any{
					"providerId": "available", "configured": true, "available": true,
					"models": []any{map[string]any{"id": "known"}},
				}}}
			case "pi.defaults.save":
				result = map[string]any{"providerId": "available", "modelId": "known"}
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
	unknown := "missing"
	if _, err := admin.SaveDefaults(ctx, "available", &unknown); err == nil || !strings.Contains(err.Error(), "unknown model") {
		t.Fatalf("unknown default model was accepted: %v", err)
	}
	if _, err := admin.SaveDefaults(ctx, "missing-provider", nil); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("unknown default provider did not report unavailable: %v", err)
	}
}

func TestPiAdminCreateReleasesOrphanOnConfigureFailure(t *testing.T) {
	var mu sync.Mutex
	created := make(map[string]string)
	var releases []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
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
			switch rpc.Method {
			case "runtime.hello":
				_ = writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": piInitializeResponse()})
			case "session.create":
				cwd, _ := rpc.Params["cwd"].(string)
				mu.Lock()
				created["orphan-1"] = cwd
				mu.Unlock()
				_ = writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": map[string]any{"sessionId": "orphan-1"}})
			case "session.configure":
				_ = writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "error": map[string]any{"code": -32000, "message": "thinking rejected"}})
			case "session.release":
				mu.Lock()
				delete(created, "orphan-1")
				releases = append(releases, map[string]any{"sessionId": rpc.Params["sessionId"], "cwd": rpc.Params["cwd"]})
				mu.Unlock()
				_ = writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": map[string]any{}})
			default:
				_ = writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": map[string]any{}})
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	root := t.TempDir()
	policy, err := workspace.NewPathPolicy([]string{root}, false)
	if err != nil {
		t.Fatal(err)
	}
	store := persist.Store{Dir: t.TempDir()}
	projects := workspace.NewProjects(store, policy)
	project, err := projects.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	records := controller.NewSessionRecords(store)
	manager := controller.NewSessionManager(projects, policy, records, controller.NewSessionQueues(store), controller.NewObjectives(store), nil)
	client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", manager)
	manager.SetClient(client)
	defer client.Close()

	cwd := project.Roots[0]
	if _, err := manager.Create(ctx, project.ID, cwd, nil, "high", "client-1"); err == nil || !strings.Contains(err.Error(), "thinking rejected") {
		t.Fatalf("configure failure did not surface: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(releases) != 1 || releases[0]["sessionId"] != "orphan-1" || releases[0]["cwd"] != cwd {
		t.Fatalf("orphan was not released with the same cwd: %#v", releases)
	}
	if len(created) != 0 {
		t.Fatalf("orphan native session remains: %#v", created)
	}
	stored, err := records.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range stored {
		if record.SessionID == "orphan-1" {
			t.Fatalf("orphan durable association remains: %#v", record)
		}
	}
}
