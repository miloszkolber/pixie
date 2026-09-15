package controller_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

// fakeBrowserMCPHost is a minimal assistant that exposes exactly the four
// pi.mcp.servers.* methods plus the runtime.hello negotiation. It keeps the
// registered definition so read/probe/remove observe their effects.
type fakeBrowserMCPHost struct {
	mu                           sync.Mutex
	entries                      map[string]map[string]any
	collisions                   map[string][]fakeBrowserMCPCollision
	changeAgentTokenBeforeUpsert string
	changeAgentTokenBeforeRemove string
	probeURL                     string
	upserts                      int
	removes                      int
	probes                       int
	failNextUpsert               bool
	failNextRemove               bool
	probeError                   string
	probeInfo                    map[string]any
	probeProtocol                string
	probeTools                   []any
}

type fakeBrowserMCPCollision struct {
	Layer      string
	Path       string
	Disabled   bool
	Definition map[string]any
}

func newFakeBrowserMCPHost() *fakeBrowserMCPHost {
	return &fakeBrowserMCPHost{entries: make(map[string]map[string]any), collisions: make(map[string][]fakeBrowserMCPCollision)}
}

func browserMCPString(value any) string {
	text, _ := value.(string)
	return text
}

func browserMCPObject(value any) map[string]any {
	if object, ok := value.(map[string]any); ok {
		return object
	}
	return map[string]any{}
}

func fakeBrowserMCPCandidate(layer, path string, disabled bool, definition map[string]any) map[string]any {
	markers := map[string]any{}
	for _, key := range []string{"_pixieManagedBy", "_pixieOwnershipToken"} {
		if value, ok := definition[key].(string); ok {
			markers[key] = value
		}
	}
	return map[string]any{"layer": layer, "path": path, "disabled": disabled, "markers": markers}
}

func (f *fakeBrowserMCPHost) candidates(name string) []any {
	result := []any{}
	if entry, ok := f.entries[name]; ok {
		result = append(result, fakeBrowserMCPCandidate("agent-dir", "/tmp/mcp.json", false, entry))
	}
	for _, collision := range f.collisions[name] {
		result = append(result, fakeBrowserMCPCandidate(collision.Layer, collision.Path, collision.Disabled, collision.Definition))
	}
	return result
}

func fakeBrowserMCPOwned(entry map[string]any, token string) bool {
	return browserMCPString(entry["_pixieManagedBy"]) == "browser-mcp/v2" &&
		browserMCPString(entry["_pixieOwnershipToken"]) == token
}

func (f *fakeBrowserMCPHost) ownershipConflict(name string, params map[string]any, remove bool) bool {
	expected, conditional := params["expectedAgentOwnershipToken"].(string)
	requireNoCollisions, collisionRequired := params["requireNoOtherLayerCollisions"].(bool)
	if !conditional && !collisionRequired {
		return false
	}
	if !conditional || !requireNoCollisions {
		return true
	}
	entry, exists := f.entries[name]
	if len(f.collisions[name]) > 0 || remove && (!exists || !fakeBrowserMCPOwned(entry, expected)) {
		return true
	}
	return !remove && exists && !fakeBrowserMCPOwned(entry, expected)
}

func (f *fakeBrowserMCPHost) handler() http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
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
			var result any
			switch rpc.Method {
			case "runtime.hello":
				hello := piInitializeResponse()
				operations, _ := hello["operationSet"].(map[string]bool)
				for _, method := range []string{"pi.mcp.servers.read", "pi.mcp.servers.upsert", "pi.mcp.servers.remove", "pi.mcp.servers.probe"} {
					operations[method] = true
				}
				result = hello
			case "pi.mcp.servers.read":
				f.mu.Lock()
				servers := f.candidates(browserMCPString(rpc.Params["name"]))
				f.mu.Unlock()
				result = map[string]any{"servers": servers, "warnings": []any{}}
			case "pi.mcp.servers.upsert":
				f.mu.Lock()
				f.upserts++
				if f.changeAgentTokenBeforeUpsert != "" {
					if entry := f.entries[browserMCPString(rpc.Params["name"])]; entry != nil {
						entry["_pixieOwnershipToken"] = f.changeAgentTokenBeforeUpsert
					}
					f.changeAgentTokenBeforeUpsert = ""
				}
				failed := f.failNextUpsert
				f.failNextUpsert = false
				conflict := f.ownershipConflict(browserMCPString(rpc.Params["name"]), rpc.Params, false)
				if !failed && !conflict {
					f.entries[browserMCPString(rpc.Params["name"])] = browserMCPObject(rpc.Params["definition"])
				}
				f.mu.Unlock()
				if conflict {
					_ = writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "error": map[string]any{"code": -32000, "message": "MCP ownership conflict"}})
					continue
				}
				if failed {
					_ = writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "error": map[string]any{"code": -32000, "message": "upsert rejected"}})
					continue
				}
				result = map[string]any{"servers": []any{}}
			case "pi.mcp.servers.remove":
				f.mu.Lock()
				f.removes++
				if f.changeAgentTokenBeforeRemove != "" {
					if entry := f.entries[browserMCPString(rpc.Params["name"])]; entry != nil {
						entry["_pixieOwnershipToken"] = f.changeAgentTokenBeforeRemove
					}
					f.changeAgentTokenBeforeRemove = ""
				}
				failed := f.failNextRemove
				f.failNextRemove = false
				conflict := f.ownershipConflict(browserMCPString(rpc.Params["name"]), rpc.Params, true)
				if !failed && !conflict {
					delete(f.entries, browserMCPString(rpc.Params["name"]))
				}
				f.mu.Unlock()
				if conflict {
					_ = writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "error": map[string]any{"code": -32000, "message": "MCP ownership conflict"}})
					continue
				}
				if failed {
					_ = writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "error": map[string]any{"code": -32000, "message": "remove rejected"}})
					continue
				}
				result = map[string]any{"ok": true}
			case "pi.mcp.servers.probe":
				definition := browserMCPObject(rpc.Params["definition"])
				f.mu.Lock()
				f.probeURL = browserMCPString(definition["url"])
				f.probes++
				probeError, probeInfo := f.probeError, f.probeInfo
				probeProtocol, probeTools := f.probeProtocol, f.probeTools
				f.mu.Unlock()
				if probeInfo == nil {
					probeInfo = map[string]any{"name": "stub", "version": "1"}
				}
				if probeProtocol == "" {
					probeProtocol = "2025-06-18"
				}
				if probeTools == nil {
					probeTools = []any{"browse"}
				}
				result = map[string]any{
					"reachable": true, "serverInfo": probeInfo,
					"protocolVersion": probeProtocol, "tools": probeTools,
				}
				if probeError != "" {
					result.(map[string]any)["error"] = probeError
				}
			}
			if len(rpc.ID) > 0 {
				_ = writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
			}
		}
	}
}

func TestBrowserMCPDefaultsAreLeanAndOptIn(t *testing.T) {
	settings := controller.NewSettings(persist.Store{Dir: t.TempDir()}, nil)
	config, err := settings.BrowserMCP()
	if err != nil {
		t.Fatal(err)
	}
	if config.Name != "pixie-browser" || config.URL != "http://127.0.0.1:3000/mcp" || config.Enabled {
		t.Fatalf("default browser MCP setting = %#v", config)
	}
}

func TestBrowserMCPConfigureStatusRemoveAgainstAssistant(t *testing.T) {
	fake := newFakeBrowserMCPHost()
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", nil)
	defer client.Close()
	settings := controller.NewSettings(persist.Store{Dir: t.TempDir()}, nil)
	admin := controller.NewPiAdmin(client, settings)
	handler := controller.CoreHandler{Admin: admin, Settings: settings}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	configured, err := handler.Handle(ctx, "browserMcp.configure", json.RawMessage(`{"enabled":true,"name":"pixie-browser","url":"http://127.0.0.1:3000/mcp"}`), "test-client")
	if err != nil {
		t.Fatal(err)
	}
	status, ok := configured.(controller.BrowserMCPStatus)
	if !ok {
		t.Fatalf("configure returned %#v", configured)
	}
	if !status.Enabled || !status.Registered || !status.Reachable || status.Layer != "" || status.Path != "" || status.Disabled {
		t.Fatalf("configured status = %#v", status)
	}
	if len(status.Tools) != 1 || status.Tools[0] != "Tool 1" || status.ServerInfo != nil || status.ProtocolVersion != "" {
		t.Fatalf("configured probe = %#v", status)
	}
	fake.mu.Lock()
	marker := browserMCPString(fake.entries["pixie-browser"]["_pixieManagedBy"])
	nativeToken := browserMCPString(fake.entries["pixie-browser"]["_pixieOwnershipToken"])
	fake.mu.Unlock()
	if marker != "browser-mcp/v2" {
		t.Fatalf("configured native definition did not retain Pixie ownership marker: %q", marker)
	}
	if persisted, err := settings.BrowserMCP(); err != nil || !persisted.Enabled || persisted.URL != "http://127.0.0.1:3000/mcp" || persisted.OwnershipToken == "" || persisted.OwnershipToken != nativeToken {
		t.Fatalf("persisted setting = %#v, %v", persisted, err)
	}
	serializedStatus, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serializedStatus), nativeToken) {
		t.Fatalf("browser status leaked ownership token: %s", serializedStatus)
	}

	reported, err := handler.Handle(ctx, "browserMcp.status", json.RawMessage(`{}`), "test-client")
	if err != nil {
		t.Fatal(err)
	}
	reportedStatus, _ := reported.(controller.BrowserMCPStatus)
	if !reportedStatus.Registered || !reportedStatus.Reachable {
		t.Fatalf("reported status = %#v", reportedStatus)
	}

	removed, err := handler.Handle(ctx, "browserMcp.remove", json.RawMessage(`{}`), "test-client")
	if err != nil {
		t.Fatal(err)
	}
	removedStatus, _ := removed.(controller.BrowserMCPStatus)
	if removedStatus.Enabled || removedStatus.Registered || !removedStatus.Reachable {
		t.Fatalf("removed status = %#v", removedStatus)
	}

	fake.mu.Lock()
	upserts, removes, probes, registered := fake.upserts, fake.removes, fake.probes, len(fake.entries) > 0
	fake.mu.Unlock()
	if upserts != 1 || removes != 1 || probes != 3 || registered {
		t.Fatalf("fake host calls = upserts %d removes %d probes %d registered %v", upserts, removes, probes, registered)
	}

	// The same surface fails closed when the assistant is unavailable.
	if _, err := (controller.CoreHandler{}).Handle(ctx, "browserMcp.status", json.RawMessage(`{}`), "test-client"); err == nil {
		t.Fatal("status without Pi administration succeeded")
	}
	if _, err := (controller.CoreHandler{}).Handle(ctx, "browserMcp.configure", json.RawMessage(`{"enabled":true}`), "test-client"); err == nil {
		t.Fatal("configure without Pi administration succeeded")
	}
	if _, err := (controller.CoreHandler{}).Handle(ctx, "browserMcp.remove", json.RawMessage(`{}`), "test-client"); err == nil {
		t.Fatal("remove without Pi administration succeeded")
	}
}

func TestBrowserMCPStatusNeverSerializesEndpointCredentials(t *testing.T) {
	fake := newFakeBrowserMCPHost()
	fake.probeError = "request failed: Authorization: Bearer browser-probe-secret"
	fake.probeInfo = map[string]any{
		"name":    "http://127.0.0.1/mcp?access_token=browser-url-secret",
		"version": "Authorization: Bearer browser-header-secret",
	}
	fake.probeProtocol = "browser-url-secret"
	fake.probeTools = []any{"browser-url-secret", "browser-header-secret"}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", nil)
	defer client.Close()
	settings := controller.NewSettings(persist.Store{Dir: t.TempDir()}, nil)
	handler := controller.CoreHandler{Admin: controller.NewPiAdmin(client, settings), Settings: settings}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const endpointSecret = "browser-url-secret"
	const bearerSecret = "browser-header-secret"
	configured, err := handler.Handle(ctx, "browserMcp.configure", json.RawMessage(`{
		"enabled":true,
		"name":"pixie-browser",
		"url":"http://127.0.0.1:3000/mcp?access_token=browser-url-secret",
		"headers":{"Authorization":"Bearer browser-header-secret"}
	}`), "test-client")
	if err != nil {
		t.Fatal(err)
	}
	rawStatus, err := json.Marshal(configured)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{endpointSecret, bearerSecret, "Authorization"} {
		if strings.Contains(string(rawStatus), secret) {
			t.Fatalf("browser status leaked %q: %s", secret, rawStatus)
		}
	}
	status, ok := configured.(controller.BrowserMCPStatus)
	if !ok || len(status.Tools) != 2 || status.Tools[0] != "Tool 1" || status.ServerInfo != nil || status.ProtocolVersion != "" {
		t.Fatalf("status did not retain only safe probe projection: %#v", configured)
	}

	webSocket, err := controller.NewWebSocketServer(handler, nil, controller.AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer webSocket.Close(context.Background())
	browser := httptest.NewServer(webSocket)
	defer browser.Close()
	setWebSocketListenerPort(t, webSocket, browser)
	connection := dialBrowserSocket(t, ctx, browser.URL, "browser-mcp-status")
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"id":"browser-status","method":"browserMcp.status","params":{}}`)); err != nil {
		t.Fatal(err)
	}
	_, rawResponse, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{endpointSecret, bearerSecret, "Authorization"} {
		if strings.Contains(string(rawResponse), secret) {
			t.Fatalf("browser WebSocket response leaked %q: %s", secret, rawResponse)
		}
	}
	if !strings.Contains(string(rawResponse), "The endpoint probe failed.") {
		t.Fatalf("browser WebSocket response did not retain safe recovery detail: %s", rawResponse)
	}
}

func TestBrowserMCPOwnershipTokenNeverReachesBrowserJSON(t *testing.T) {
	const token = "browser-mcp-ownership-token-that-must-stay-private"
	fake := newFakeBrowserMCPHost()
	fake.entries["pixie-browser"] = map[string]any{
		"url":                  "http://127.0.0.1:3000/mcp",
		"_pixieManagedBy":      "browser-mcp/v2",
		"_pixieOwnershipToken": token,
	}
	piServer := httptest.NewServer(fake.handler())
	defer piServer.Close()
	dataDir := t.TempDir()
	stored, err := json.Marshal(controller.AppConfig{HiddenModels: []controller.ModelReference{}, BrowserMCP: controller.BrowserMCPConfig{
		Name: "pixie-browser", URL: "http://127.0.0.1:3000/mcp", Enabled: true, OwnershipToken: token,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "config.json"), stored, 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	policy, err := workspace.NewPathPolicy([]string{root}, false)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	runtime, err := controller.NewRuntime(controller.RuntimeConfig{
		Host: "127.0.0.1", Port: port, DataDir: dataDir, StaticDir: t.TempDir(),
		PiURL: "ws" + strings.TrimPrefix(piServer.URL, "http"), Policy: policy, Getenv: func(string) string { return "" },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	host, err := runtime.Start()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection := dialRuntimeSocket(t, ctx, host, "browser-token-redaction")
	_, welcome, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(welcome), token) {
		t.Fatalf("welcome leaked Browser MCP ownership token: %s", welcome)
	}

	assertReplyDoesNotLeak := func(id, method, payload string) {
		t.Helper()
		if err := connection.Write(ctx, websocket.MessageText, []byte(payload)); err != nil {
			t.Fatal(err)
		}
		for {
			_, raw, err := connection.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), token) {
				t.Fatalf("%s leaked Browser MCP ownership token: %s", method, raw)
			}
			var frame struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(raw, &frame) == nil && frame.ID == id {
				return
			}
		}
	}
	assertReplyDoesNotLeak("settings-update", "settings.update", `{"id":"settings-update","method":"settings.update","params":{"config":{"hiddenModels":[]}}}`)
	assertReplyDoesNotLeak("browser-status", "browserMcp.status", `{"id":"browser-status","method":"browserMcp.status","params":{}}`)
}

func TestBrowserMCPRefusesToMutateAnUnownedSameNameEntry(t *testing.T) {
	fake := newFakeBrowserMCPHost()
	fake.entries["pixie-browser"] = map[string]any{
		"url":             "https://third-party.example/mcp?access_token=third-party-secret",
		"headers":         map[string]any{"Authorization": "Bearer third-party-header"},
		"custom":          map[string]any{"preserve": []any{"exact", true}},
		"_pixieManagedBy": "browser-mcp/v1",
	}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", nil)
	defer client.Close()
	settings := controller.NewSettings(persist.Store{Dir: t.TempDir()}, nil)
	handler := controller.CoreHandler{Admin: controller.NewPiAdmin(client, settings), Settings: settings}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fake.mu.Lock()
	before, err := json.Marshal(fake.entries["pixie-browser"])
	fake.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.Handle(ctx, "browserMcp.configure", json.RawMessage(`{"enabled":true,"name":"pixie-browser","url":"http://127.0.0.1:3000/mcp"}`), "test-client"); err == nil || !strings.Contains(err.Error(), "needs recovery") {
		t.Fatalf("configure conflict = %v", err)
	}
	if _, err := handler.Handle(ctx, "browserMcp.remove", json.RawMessage(`{}`), "test-client"); err == nil || !strings.Contains(err.Error(), "needs recovery") {
		t.Fatalf("remove conflict = %v", err)
	}
	fake.mu.Lock()
	after, err := json.Marshal(fake.entries["pixie-browser"])
	upserts, removes := fake.upserts, fake.removes
	fake.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("unowned entry changed:\n before %s\n after  %s", before, after)
	}
	if upserts != 1 || removes != 0 {
		t.Fatalf("unowned entry mutation calls = upserts %d removes %d", upserts, removes)
	}
	if persisted, err := settings.BrowserMCP(); err != nil || persisted.Enabled || persisted.Name != "pixie-browser" {
		t.Fatalf("conflict changed persisted browser MCP setting = %#v, %v", persisted, err)
	}
	if _, err := handler.Handle(ctx, "browserMcp.configure", json.RawMessage(`{"enabled":false,"name":"replacement-browser","url":"http://127.0.0.1:3000/mcp"}`), "test-client"); err != nil {
		t.Fatalf("disabled replacement name should not remove the unowned entry: %v", err)
	}
	fake.mu.Lock()
	replaced, err := json.Marshal(fake.entries["pixie-browser"])
	upserts, removes = fake.upserts, fake.removes
	fake.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, replaced) || upserts != 1 || removes != 0 {
		t.Fatalf("disabled replacement touched the unowned entry: %s", replaced)
	}
}

func TestBrowserMCPConditionalOwnershipRejectsRandomTokensCollisionsAndRaces(t *testing.T) {
	fake := newFakeBrowserMCPHost()
	const name = "pixie-browser"
	const persistedToken = "persisted-browser-ownership-token"
	fake.entries[name] = map[string]any{
		"url":                  "https://browser.example/original",
		"headers":              map[string]any{"Authorization": "Bearer third-party-header"},
		"_pixieManagedBy":      "browser-mcp/v2",
		"_pixieOwnershipToken": "forged-random-token",
	}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", nil)
	defer client.Close()
	settings := controller.NewSettings(persist.Store{Dir: t.TempDir()}, nil)
	if _, err := settings.SetBrowserMCP(controller.BrowserMCPConfig{Name: name, URL: "http://127.0.0.1:3000/mcp", Enabled: true, OwnershipToken: persistedToken}); err != nil {
		t.Fatal(err)
	}
	handler := controller.CoreHandler{Admin: controller.NewPiAdmin(client, settings), Settings: settings}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	assertUnchanged := func(before []byte) {
		t.Helper()
		fake.mu.Lock()
		after, err := json.Marshal(fake.entries[name])
		fake.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatalf("conditional conflict changed native entry:\n before %s\n after  %s", before, after)
		}
	}
	assertRecovery := func(err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "needs recovery") {
			t.Fatalf("ownership conflict = %v", err)
		}
	}

	fake.mu.Lock()
	randomBefore, err := json.Marshal(fake.entries[name])
	fake.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	reported, err := handler.Handle(ctx, "browserMcp.status", json.RawMessage(`{}`), "test-client")
	if err != nil {
		t.Fatal(err)
	}
	if status, ok := reported.(controller.BrowserMCPStatus); !ok || status.Registered {
		t.Fatalf("forged-token Browser MCP status = %#v", reported)
	}
	_, err = handler.Handle(ctx, "browserMcp.configure", json.RawMessage(`{"enabled":true,"name":"pixie-browser","url":"http://127.0.0.1:3001/mcp"}`), "test-client")
	assertRecovery(err)
	assertUnchanged(randomBefore)
	_, err = handler.Handle(ctx, "browserMcp.remove", json.RawMessage(`{}`), "test-client")
	assertRecovery(err)
	assertUnchanged(randomBefore)

	fake.mu.Lock()
	fake.entries[name]["_pixieOwnershipToken"] = persistedToken
	fake.collisions[name] = []fakeBrowserMCPCollision{{
		Layer: "project", Path: "/work/.mcp.json", Definition: map[string]any{"url": "https://third-party.example/collision"},
	}}
	collisionBefore, err := json.Marshal(fake.entries[name])
	fake.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	reported, err = handler.Handle(ctx, "browserMcp.status", json.RawMessage(`{}`), "test-client")
	if err != nil {
		t.Fatal(err)
	}
	if status, ok := reported.(controller.BrowserMCPStatus); !ok || status.Registered {
		t.Fatalf("colliding Browser MCP status = %#v", reported)
	}
	_, err = handler.Handle(ctx, "browserMcp.configure", json.RawMessage(`{"enabled":true,"name":"pixie-browser","url":"http://127.0.0.1:3001/mcp"}`), "test-client")
	assertRecovery(err)
	assertUnchanged(collisionBefore)
	_, err = handler.Handle(ctx, "browserMcp.remove", json.RawMessage(`{}`), "test-client")
	assertRecovery(err)
	assertUnchanged(collisionBefore)

	fake.mu.Lock()
	fake.collisions[name] = nil
	fake.changeAgentTokenBeforeUpsert = "changed-during-upsert-race"
	fake.mu.Unlock()
	_, err = handler.Handle(ctx, "browserMcp.configure", json.RawMessage(`{"enabled":true,"name":"pixie-browser","url":"http://127.0.0.1:3001/mcp"}`), "test-client")
	assertRecovery(err)
	fake.mu.Lock()
	entryAfterUpsertRace := fake.entries[name]
	fake.mu.Unlock()
	if browserMCPString(entryAfterUpsertRace["url"]) != "https://browser.example/original" || browserMCPString(entryAfterUpsertRace["_pixieOwnershipToken"]) != "changed-during-upsert-race" {
		t.Fatalf("raced upsert overwrote native entry: %#v", entryAfterUpsertRace)
	}

	fake.mu.Lock()
	fake.entries[name]["_pixieOwnershipToken"] = persistedToken
	fake.changeAgentTokenBeforeRemove = "changed-during-remove-race"
	fake.mu.Unlock()
	_, err = handler.Handle(ctx, "browserMcp.remove", json.RawMessage(`{}`), "test-client")
	assertRecovery(err)
	fake.mu.Lock()
	entryAfterRemoveRace := fake.entries[name]
	fake.mu.Unlock()
	if browserMCPString(entryAfterRemoveRace["url"]) != "https://browser.example/original" || browserMCPString(entryAfterRemoveRace["_pixieOwnershipToken"]) != "changed-during-remove-race" {
		t.Fatalf("raced remove changed native entry: %#v", entryAfterRemoveRace)
	}
}

func TestBrowserMCPNameUsesTheHostASCIIBoundary(t *testing.T) {
	fake := newFakeBrowserMCPHost()
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", nil)
	defer client.Close()
	settings := controller.NewSettings(persist.Store{Dir: t.TempDir()}, nil)
	handler := controller.CoreHandler{Admin: controller.NewPiAdmin(client, settings), Settings: settings}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := handler.Handle(ctx, "browserMcp.configure", json.RawMessage(`{"enabled":true,"name":"bröwser","url":"http://127.0.0.1:3000/mcp"}`), "test-client"); err == nil || !strings.Contains(err.Error(), "ASCII") {
		t.Fatalf("unicode name error = %v", err)
	}
	fake.mu.Lock()
	upserts := fake.upserts
	fake.mu.Unlock()
	if upserts != 0 {
		t.Fatalf("unicode name reached the host: %d upserts", upserts)
	}

	name := strings.Repeat("a", 128)
	if _, err := handler.Handle(ctx, "browserMcp.configure", json.RawMessage(`{"enabled":false,"name":"`+name+`","url":"http://127.0.0.1:3000/mcp"}`), "test-client"); err != nil {
		t.Fatalf("128-character ASCII name was not accepted while disabled: %v", err)
	}
	if _, err := handler.Handle(ctx, "browserMcp.configure", json.RawMessage(`{"enabled":true,"name":"`+name+`","url":"http://127.0.0.1:3000/mcp"}`), "test-client"); err != nil {
		t.Fatalf("128-character ASCII name could not be registered: %v", err)
	}
	fake.mu.Lock()
	_, registered := fake.entries[name]
	fake.mu.Unlock()
	if !registered {
		t.Fatal("valid boundary name was not registered")
	}
}

func TestBrowserMCPRenameRequiresAConfirmedDisabledTransition(t *testing.T) {
	fake := newFakeBrowserMCPHost()
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", nil)
	defer client.Close()
	settings := controller.NewSettings(persist.Store{Dir: t.TempDir()}, nil)
	handler := controller.CoreHandler{Admin: controller.NewPiAdmin(client, settings), Settings: settings}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := handler.Handle(ctx, "browserMcp.configure", json.RawMessage(`{"enabled":true,"name":"pixie-browser","url":"http://127.0.0.1:3000/mcp"}`), "test-client"); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.Handle(ctx, "browserMcp.configure", json.RawMessage(`{"enabled":true,"name":"renamed-browser","url":"http://127.0.0.1:3000/mcp"}`), "test-client"); err == nil || !strings.Contains(err.Error(), "remove the enabled entry first") {
		t.Fatalf("enabled rename error = %v", err)
	}
	if persisted, err := settings.BrowserMCP(); err != nil || persisted.Name != "pixie-browser" || !persisted.Enabled {
		t.Fatalf("enabled rename lost old setting = %#v, %v", persisted, err)
	}
	fake.mu.Lock()
	hasOnlyOldEntry := len(fake.entries) == 1 && fake.entries["pixie-browser"] != nil
	fake.mu.Unlock()
	if !hasOnlyOldEntry {
		t.Fatal("enabled rename changed managed entries")
	}

	if _, err := handler.Handle(ctx, "browserMcp.remove", json.RawMessage(`{}`), "test-client"); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.Handle(ctx, "browserMcp.configure", json.RawMessage(`{"enabled":false,"name":"renamed-browser","url":"http://127.0.0.1:3000/mcp"}`), "test-client"); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.Handle(ctx, "browserMcp.configure", json.RawMessage(`{"enabled":true,"name":"renamed-browser","url":"http://127.0.0.1:3000/mcp"}`), "test-client"); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	hasOnlyRenamedEntry := len(fake.entries) == 1 && fake.entries["renamed-browser"] != nil && fake.entries["pixie-browser"] == nil
	fake.mu.Unlock()
	if !hasOnlyRenamedEntry {
		t.Fatal("completed rename did not leave exactly one managed entry")
	}
}

func TestBrowserMCPMutationFailureRetainsPersistedEntryIdentity(t *testing.T) {
	fake := newFakeBrowserMCPHost()
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", nil)
	defer client.Close()
	settings := controller.NewSettings(persist.Store{Dir: t.TempDir()}, nil)
	handler := controller.CoreHandler{Admin: controller.NewPiAdmin(client, settings), Settings: settings}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := handler.Handle(ctx, "browserMcp.configure", json.RawMessage(`{"enabled":true,"name":"pixie-browser","url":"http://127.0.0.1:3000/mcp"}`), "test-client"); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.failNextUpsert = true
	fake.mu.Unlock()
	if _, err := handler.Handle(ctx, "browserMcp.configure", json.RawMessage(`{"enabled":true,"name":"pixie-browser","url":"http://127.0.0.1:3001/mcp"}`), "test-client"); err == nil {
		t.Fatal("upsert failure was accepted")
	}
	if persisted, err := settings.BrowserMCP(); err != nil || persisted.URL != "http://127.0.0.1:3000/mcp" || !persisted.Enabled {
		t.Fatalf("upsert failure lost persisted state = %#v, %v", persisted, err)
	}

	fake.mu.Lock()
	fake.failNextRemove = true
	fake.mu.Unlock()
	if _, err := handler.Handle(ctx, "browserMcp.remove", json.RawMessage(`{}`), "test-client"); err == nil {
		t.Fatal("remove failure was accepted")
	}
	if persisted, err := settings.BrowserMCP(); err != nil || persisted.Name != "pixie-browser" || !persisted.Enabled {
		t.Fatalf("remove failure lost persisted state = %#v, %v", persisted, err)
	}
	fake.mu.Lock()
	entry := fake.entries["pixie-browser"]
	fake.mu.Unlock()
	if browserMCPString(entry["url"]) != "http://127.0.0.1:3000/mcp" {
		t.Fatalf("failed mutations changed managed entry: %#v", entry)
	}
}
