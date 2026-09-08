package controller_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/mcpserver"
	"github.com/miloszkolber/pixie/internal/persist"
)

func testInProcessRegistry(t *testing.T) *mcpserver.Registry {
	t.Helper()
	root := t.TempDir()
	agentBrowser, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	registry, err := mcpserver.NewRegistry(mcpserver.Config{
		Host: "127.0.0.1", Port: 17873, DataDir: dataDir,
		Binaries: &mcpserver.BinaryConfig{
			AgentBrowser: agentBrowser, BrowserConfig: configPath,
			ArtifactRoot: filepath.Join(root, "artifacts"), StateRoot: filepath.Join(root, "state"),
		},
	}, diagnostics.NormalizeBuild("test", "test"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Shutdown)
	return registry
}

func handleJSON(t *testing.T, handler controller.CoreHandler, method, params string) any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := handler.Handle(ctx, method, json.RawMessage(params), "test-client")
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return result
}

func TestMCPRegistryHandlerTogglesPersistedEnablement(t *testing.T) {
	handler := controller.CoreHandler{MCPRegistry: testInProcessRegistry(t)}
	catalog := handleJSON(t, handler, "mcpRegistry.catalog", "{}").(mcpserver.Catalog)
	if len(catalog.Modules) != 1 || !catalog.Modules[0].Enabled || catalog.Modules[0].Endpoint == "" {
		t.Fatalf("registry catalog = %#v", catalog)
	}
	disabled := handleJSON(t, handler, "mcpRegistry.moduleSetEnabled", `{"moduleId":"browser","enabled":false}`).(mcpserver.Catalog)
	if disabled.Modules[0].Enabled || disabled.Modules[0].State != "unavailable" {
		t.Fatalf("disabled catalog = %#v", disabled)
	}
	enabled := handleJSON(t, handler, "mcpRegistry.moduleSetEnabled", `{"moduleId":"browser","enabled":true}`).(mcpserver.Catalog)
	if !enabled.Modules[0].Enabled {
		t.Fatalf("re-enabled catalog = %#v", enabled)
	}
	restarted := handleJSON(t, handler, "mcpRegistry.moduleRestart", `{"moduleId":"browser"}`).(mcpserver.Catalog)
	if !restarted.Modules[0].Enabled || restarted.Modules[0].State != "ready" {
		t.Fatalf("restarted catalog = %#v", restarted)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := handler.Handle(ctx, "mcpRegistry.moduleSetEnabled", json.RawMessage(`{"moduleId":"signet","enabled":true}`), "test-client"); err == nil {
		t.Fatal("unknown module was accepted")
	}
	if _, err := handler.Handle(ctx, "mcpRegistry.moduleSetEnabled", json.RawMessage(`{"moduleId":"browser"}`), "test-client"); err == nil {
		t.Fatal("malformed module request was accepted")
	}
	empty := controller.CoreHandler{}
	optional := handleJSON(t, empty, "mcpRegistry.catalog", "{}").(map[string]any)
	if optional["gateway"].(map[string]any)["state"] != "not-configured" {
		t.Fatalf("unconfigured registry catalog = %#v", optional)
	}
}

func TestMCPAdapterStatusProjectsBridgeAndStaysFailOpen(t *testing.T) {
	// Without Pi administration the adapter status stays fail-open.
	unavailable := handleJSON(t, controller.CoreHandler{}, "mcpAdapter.status", "{}").(map[string]any)
	if unavailable["available"] != false {
		t.Fatalf("adapter status without admin = %#v", unavailable)
	}

	piServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
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
			}
			if json.Unmarshal(payload, &rpc) != nil {
				return
			}
			result := any(map[string]any{})
			switch rpc.Method {
			case "runtime.hello":
				result = piInitializeResponse()
			case "adapter.status":
				result = map[string]any{
					"engine": "pi-mcp-adapter", "version": "2.32.1", "bunCompat": "unknown",
					"proxyTool": "mcp", "runtimeName": "pixie-browser",
					"snapshot": map[string]any{
						"version": 1, "totalTools": 1, "connectedCount": 1, "disabledCount": 0,
						"servers": []any{map[string]any{"name": "pixie-browser", "status": "connected", "toolCount": 2}},
					},
				}
			}
			if len(rpc.ID) > 0 {
				_ = writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
			}
		}
	}))
	defer piServer.Close()
	client := controller.NewPiClient("ws"+strings.TrimPrefix(piServer.URL, "http"), "", "test", nil)
	defer client.Close()
	admin := controller.NewPiAdmin(client, controller.NewSettings(persist.Store{Dir: t.TempDir()}, nil))
	projected := handleJSON(t, controller.CoreHandler{Admin: admin}, "mcpAdapter.status", "{}").(map[string]any)
	if projected["available"] != true || projected["engine"] != "pi-mcp-adapter" {
		t.Fatalf("adapter status = %#v", projected)
	}
	snapshot := projected["snapshot"].(map[string]any)
	servers := snapshot["servers"].([]any)
	if len(servers) != 1 || servers[0].(map[string]any)["status"] != "connected" {
		t.Fatalf("adapter snapshot servers = %#v", servers)
	}
}
