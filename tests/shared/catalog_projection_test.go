package shared_test

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
	"github.com/miloszkolber/pixie/piprotocol"
)

// TestControllerDropsNegotiatedOperationsOutsideCatalog binds the controller's
// negotiation to the generated shared catalog. A host that advertises a route
// the catalog does not define must not have that route authorized.
func TestControllerDropsNegotiatedOperationsOutsideCatalog(t *testing.T) {
	const bogus = "not.a.real.host.operation"
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
				ID json.RawMessage `json:"id"`
			}
			if json.Unmarshal(raw, &rpc) != nil {
				return
			}
			result := map[string]any{
				"protocolVersion": 1,
				"runtimeId":       "catalog-fixture",
				"bootId":          "catalog-boot",
				"version":         "0.85.1",
				"capabilities":    map[string]any{"sessions": 1},
				"operationSet": map[string]bool{
					"session.list":   true,
					"session.create": true,
					"session.load":   true,
					"session.prompt": true,
					"session.cancel": true,
					"pi.tools.list":  true,
					bogus:            true,
				},
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
	_, profile, err := client.Profile(ctx)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if _, present := profile.OperationSet[bogus]; present {
		t.Fatalf("controller authorized a route outside the generated catalog: %#v", profile.OperationSet)
	}
	if !profile.OperationSet["session.list"] {
		t.Fatalf("controller dropped a catalogued negotiated route: %#v", profile.OperationSet)
	}
	if piprotocol.CatalogHostOperationSet[bogus] {
		t.Fatalf("test fixture %q unexpectedly exists in the generated catalog", bogus)
	}
}

// TestSessionListMetadataContract makes the session.list metadata requirement
// explicit: the route is the catalogued host route, the generated result schema
// requires the listing, and every resident and cwd-scoped entry preserves the
// identity and working-directory metadata the UI needs.
func TestSessionListMetadataContract(t *testing.T) {
	if got := piprotocol.CatalogControllerMethodRoutes["session.list"]; got != "session.list" {
		t.Fatalf("session.list route = %q, want the catalogued host route", got)
	}
	if !piprotocol.CatalogControllerMethodIsAvailable("session.list") {
		t.Fatal("session.list is not dispatchable")
	}
	schemaFound := false
	for _, schema := range piprotocol.CatalogHostMethodSchemas {
		if schema.Name != "session.list" {
			continue
		}
		schemaFound = true
		if len(schema.Result) != 1 || schema.Result[0].Name != "sessions" || schema.Result[0].Type != "array" {
			t.Fatalf("session.list result schema drifted: %#v", schema.Result)
		}
	}
	if !schemaFound {
		t.Fatal("generated host method schemas are missing session.list")
	}

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
				ID json.RawMessage `json:"id"`
			}
			if json.Unmarshal(raw, &rpc) != nil {
				return
			}
			result := map[string]any{
				"protocolVersion": 1,
				"runtimeId":       "session-list-fixture",
				"bootId":          "session-list-boot",
				"version":         "0.85.1",
				"capabilities":    map[string]any{"sessions": 1},
				"operationSet":    map[string]bool{"session.list": true, "session.create": true, "session.load": true, "session.prompt": true, "session.cancel": true},
			}
			var request struct {
				Method string `json:"method"`
			}
			if json.Unmarshal(raw, &request) == nil && request.Method == "session.list" {
				result = map[string]any{"sessions": []any{
					map[string]any{"sessionId": "resident", "cwd": "/workspace"},
					map[string]any{"sessionId": "scoped", "cwd": "/workspace/sub", "title": "Scoped", "updatedAt": "2026-01-01T00:00:00Z"},
				}}
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
	listing, err := client.ListSessions(ctx, piprotocol.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(listing.Sessions) != 2 {
		t.Fatalf("expected resident and cwd-scoped entries, got %#v", listing.Sessions)
	}
	for _, entry := range listing.Sessions {
		if entry.SessionId == "" || entry.Cwd == "" {
			t.Fatalf("session.list entry lost identity/cwd metadata: %#v", entry)
		}
	}
}
