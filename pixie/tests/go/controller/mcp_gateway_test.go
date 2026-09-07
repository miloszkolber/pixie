package controller_test

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
	"github.com/miloszkolber/pixie/internal/persist"
)

const gatewayTestToken = "gateway-test-token-0123456789abcdef0123456789"

func TestMCPGatewayCatalogIsOptionalAndValidatesModulePaths(t *testing.T) {
	optional := controller.NewMCPGateway(controller.AuthConfig{})
	catalog, err := optional.Catalog(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if catalog["schemaVersion"] != 1 || catalog["gateway"].(map[string]any)["state"] != "not-configured" {
		t.Fatalf("optional catalog = %#v", catalog)
	}

	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"schemaVersion":1,"revision":"rev-1","gateway":{"state":"ready"},"modules":[{"id":"browser","extensionName":"pixie-browser","displayName":"Pixie Browser","description":"Browser","path":"/browser","transport":"streamable_http","state":"ready"}]}`))
	}))
	defer server.Close()
	gateway := controller.NewMCPGateway(controller.AuthConfig{MCPURL: server.URL, MCPToken: gatewayTestToken})
	catalog, err = gateway.Catalog(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer "+gatewayTestToken {
		t.Fatalf("catalog authorization = %q", authorization)
	}
	modules := catalog["modules"].([]map[string]any)
	if len(modules) != 1 || modules[0]["id"] != "browser" || modules[0]["binding"] != "unavailable" {
		t.Fatalf("catalog modules = %#v", modules)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte(`{"schemaVersion":1,"revision":"rev-1","gateway":{"state":"ready"},"modules":[{"id":"browser","extensionName":"pixie-browser","displayName":"Browser","description":"","path":"http://evil.example","transport":"streamable_http","state":"ready"}]}`))
	}))
	defer bad.Close()
	badGateway := controller.NewMCPGateway(controller.AuthConfig{MCPURL: bad.URL})
	badCatalog, err := badGateway.Catalog(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if badCatalog["gateway"].(map[string]any)["state"] != "incompatible" {
		t.Fatalf("invalid catalog was accepted: %#v", badCatalog)
	}
}

func TestMCPGatewayInstallsAndTogglesOnlyTheDiscoveredEndpoint(t *testing.T) {
	moduleServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte(`{"schemaVersion":1,"revision":"rev-1","gateway":{"state":"ready"},"modules":[{"id":"browser","extensionName":"pixie-browser","displayName":"Pixie Browser","description":"Browser","path":"/browser","transport":"streamable_http","state":"ready"}]}`))
	}))
	defer moduleServer.Close()
	piServer, state := gatewayPi(t)
	defer piServer.Close()
	client := controller.NewPiClient("ws"+strings.TrimPrefix(piServer.URL, "http"), "", "test", nil)
	defer client.Close()
	admin := controller.NewPiAdmin(client, controller.NewSettings(persist.Store{Dir: t.TempDir()}, nil))
	gateway := controller.NewMCPGateway(controller.AuthConfig{MCPURL: moduleServer.URL, MCPToken: gatewayTestToken})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := gateway.SetPiEnabled(ctx, admin, "browser", true, "rev-1")
	if err != nil {
		t.Fatal(err)
	}
	modules := result["modules"].([]map[string]any)
	if len(modules) != 1 || modules[0]["binding"] != "enabled" {
		t.Fatalf("installed module binding = %#v", modules)
	}
	state.mu.Lock()
	if len(state.extensions) != 1 {
		state.mu.Unlock()
		t.Fatalf("installed extensions = %#v", state.extensions)
	}
	extension := state.extensions[0].(map[string]any)
	serverConfig := extension["server"].(map[string]any)
	if serverConfig["url"] != moduleServer.URL+"/browser" || serverConfig["name"] != "pixie-browser" || serverConfig["type"] != "http" {
		state.mu.Unlock()
		t.Fatalf("installed MCP server = %#v", serverConfig)
	}
	if serverConfig["headers"].([]any)[0].(map[string]any)["value"] != "Bearer ${PIXIE_MCP_TOKEN}" {
		state.mu.Unlock()
		t.Fatalf("installed MCP header = %#v", serverConfig["headers"])
	}
	state.mu.Unlock()

	result, err = gateway.SetPiEnabled(ctx, admin, "browser", false, "rev-1")
	if err != nil {
		t.Fatal(err)
	}
	modules = result["modules"].([]map[string]any)
	if modules[0]["binding"] != "disabled" {
		t.Fatalf("disabled module binding = %#v", modules[0])
	}
}

func TestMCPGatewayRejectsAnExistingExtensionWithTheWrongCredential(t *testing.T) {
	moduleServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte(`{"schemaVersion":1,"revision":"rev-1","gateway":{"state":"ready"},"modules":[{"id":"browser","extensionName":"pixie-browser","displayName":"Browser","description":"Browser","path":"/browser","transport":"streamable_http","state":"ready"}]}`))
	}))
	defer moduleServer.Close()
	piServer, state := gatewayPi(t)
	defer piServer.Close()
	state.mu.Lock()
	state.extensions = []any{map[string]any{
		"type": "mcp",
		"server": map[string]any{
			"type": "http", "name": "pixie-browser", "url": moduleServer.URL + "/browser",
			"headers": []any{map[string]any{"name": "Authorization", "value": "Bearer ${PIXIE_BROWSER_TOKEN}"}},
		},
	}}
	state.enabled = true
	state.mu.Unlock()
	client := controller.NewPiClient("ws"+strings.TrimPrefix(piServer.URL, "http"), "", "test", nil)
	defer client.Close()
	admin := controller.NewPiAdmin(client, controller.NewSettings(persist.Store{Dir: t.TempDir()}, nil))
	gateway := controller.NewMCPGateway(controller.AuthConfig{MCPURL: moduleServer.URL, MCPToken: gatewayTestToken})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := gateway.SetPiEnabled(ctx, admin, "browser", true, "rev-1"); err == nil || !strings.Contains(err.Error(), "different credential") {
		t.Fatalf("wrong credential was accepted: %v", err)
	}
}

type gatewayPiState struct {
	mu         sync.Mutex
	extensions []any
	enabled    bool
}

func gatewayPi(t *testing.T) (*httptest.Server, *gatewayPiState) {
	t.Helper()
	state := &gatewayPiState{}
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
			result := any(map[string]any{})
			switch rpc.Method {
			case "runtime.hello":
				result = piInitializeResponse()
			case "pi.config.extensions.list":
				state.mu.Lock()
				entries := make([]any, 0, len(state.extensions))
				for _, extension := range state.extensions {
					entries = append(entries, map[string]any{"extension": extension, "enabled": state.enabled, "configKey": "gateway-browser"})
				}
				state.mu.Unlock()
				result = map[string]any{"extensions": entries, "warnings": []any{}}
			case "pi.config.extensions.add":
				state.mu.Lock()
				state.extensions = append(state.extensions, rpc.Params["extension"])
				state.enabled = true
				state.mu.Unlock()
			case "pi.config.extensions.set-enabled":
				state.mu.Lock()
				state.enabled, _ = rpc.Params["enabled"].(bool)
				state.mu.Unlock()
			}
			if len(rpc.ID) > 0 {
				_ = writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
			}
		}
	}))
	return server, state
}

func TestMCPGatewayOutageRetainsGlobalDisableAndUnknownReadiness(t *testing.T) {
	moduleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"schemaVersion":1,"revision":"rev-1","gateway":{"state":"ready"},"modules":[{"id":"browser","extensionName":"pixie-browser","displayName":"Browser","path":"/browser","transport":"streamable_http","state":"ready"}]}`))
	}))
	defer moduleServer.Close()
	piServer, _ := gatewayPi(t)
	defer piServer.Close()
	client := controller.NewPiClient("ws"+strings.TrimPrefix(piServer.URL, "http"), "", "test", nil)
	defer client.Close()
	admin := controller.NewPiAdmin(client, controller.NewSettings(persist.Store{Dir: t.TempDir()}, nil))
	gateway := controller.NewMCPGateway(controller.AuthConfig{MCPURL: moduleServer.URL, MCPToken: gatewayTestToken})
	defer gateway.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := gateway.SetPiEnabled(ctx, admin, "browser", true, "rev-1"); err != nil {
		t.Fatal(err)
	}
	moduleServer.Close()
	catalog, err := gateway.Catalog(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	modules := catalog["modules"].([]map[string]any)
	if len(modules) != 1 || modules[0]["binding"] != "enabled" || modules[0]["state"] != "unavailable" {
		t.Fatalf("outage lost binding or fabricated readiness: %#v", catalog)
	}
	if _, err := gateway.SetPiEnabled(ctx, admin, "browser", true, "rev-1"); err == nil {
		t.Fatal("outage allowed enable")
	}
	catalog, err = gateway.SetPiEnabled(ctx, admin, "browser", false, "rev-1")
	if err != nil {
		t.Fatal(err)
	}
	if catalog["modules"].([]map[string]any)[0]["binding"] != "disabled" {
		t.Fatalf("outage blocked disable: %#v", catalog)
	}
}
