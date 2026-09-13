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

// fakeBrowserMCPHost is a minimal assistant that exposes exactly the four
// pi.mcp.servers.* methods plus the runtime.hello negotiation. It keeps the
// registered definition so read/probe/remove observe their effects.
type fakeBrowserMCPHost struct {
	mu         sync.Mutex
	definition map[string]any
	registered bool
	probeURL   string
	upserts    int
	removes    int
	probes     int
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
				servers := []any{}
				if f.registered {
					servers = append(servers, map[string]any{
						"name": "pixie-browser", "layer": "agent-dir", "path": "/tmp/mcp.json",
						"disabled": false, "definition": f.definition,
					})
				}
				f.mu.Unlock()
				result = map[string]any{"servers": servers, "warnings": []any{}}
			case "pi.mcp.servers.upsert":
				f.mu.Lock()
				f.registered = true
				f.definition = browserMCPObject(rpc.Params["definition"])
				f.upserts++
				f.mu.Unlock()
				result = map[string]any{"servers": []any{}}
			case "pi.mcp.servers.remove":
				f.mu.Lock()
				f.registered = false
				f.removes++
				f.mu.Unlock()
				result = map[string]any{"ok": true}
			case "pi.mcp.servers.probe":
				definition := browserMCPObject(rpc.Params["definition"])
				f.mu.Lock()
				f.probeURL = browserMCPString(definition["url"])
				f.probes++
				f.mu.Unlock()
				result = map[string]any{
					"reachable": true, "serverInfo": map[string]any{"name": "stub", "version": "1"},
					"protocolVersion": "2025-06-18", "tools": []any{"browse"},
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
	fake := &fakeBrowserMCPHost{definition: map[string]any{}}
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
	if !status.Enabled || !status.Registered || !status.Reachable || status.Layer != "agent-dir" {
		t.Fatalf("configured status = %#v", status)
	}
	if len(status.Tools) != 1 || status.Tools[0] != "browse" || status.ServerInfo["name"] != "stub" {
		t.Fatalf("configured probe = %#v", status)
	}
	if persisted, err := settings.BrowserMCP(); err != nil || !persisted.Enabled || persisted.URL != status.URL {
		t.Fatalf("persisted setting = %#v, %v", persisted, err)
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
	upserts, removes, probes, registered := fake.upserts, fake.removes, fake.probes, fake.registered
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
