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
	"github.com/miloszkolber/pixie/shared/piprotocol"
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
