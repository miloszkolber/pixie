package controller_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

// Settings updates return browser projections, so an endpoint credential saved
// for Pi's direct MCP client cannot cross the handler or WebSocket boundary.
func TestSettingsUpdateRedactsBrowserMCPEndpoint(t *testing.T) {
	const endpoint = "https://browser.example/mcp?access_token=browser-url-secret"
	settings := controller.NewSettings(persist.Store{Dir: t.TempDir()}, nil)
	if _, err := settings.SetBrowserMCP(controller.BrowserMCPConfig{Name: "browser", URL: endpoint, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	handler := controller.CoreHandler{Settings: settings}
	request := json.RawMessage(`{"config":{"hiddenModels":[{"provider":"test","id":"model"}]}}`)

	assertRedacted := func(t *testing.T, value []byte) {
		t.Helper()
		for _, forbidden := range []string{endpoint, "access_token", "browser-url-secret"} {
			if strings.Contains(string(value), forbidden) {
				t.Fatalf("settings.update leaked %q: %s", forbidden, value)
			}
		}
		var response struct {
			Result struct {
				BrowserMCP controller.BrowserMCPConfig `json:"browserMCP"`
			} `json:"result"`
		}
		if err := json.Unmarshal(value, &response); err != nil {
			t.Fatal(err)
		}
		if response.Result.BrowserMCP.URL != "" {
			t.Fatalf("settings.update returned browser MCP URL %q", response.Result.BrowserMCP.URL)
		}
	}

	rawResult, err := handler.Handle(t.Context(), "settings.update", request, "test-client")
	if err != nil {
		t.Fatalf("raw settings.update: %v", err)
	}
	if _, ok := rawResult.(controller.AppConfig); !ok {
		t.Fatalf("raw settings.update result = %#v", rawResult)
	}
	rawResponse, err := json.Marshal(map[string]any{"result": rawResult})
	if err != nil {
		t.Fatal(err)
	}
	assertRedacted(t, rawResponse)

	server, err := controller.NewWebSocketServer(handler, nil, controller.AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	host := httptest.NewServer(server)
	defer host.Close()
	setWebSocketListenerPort(t, server, host)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection := dialBrowserSocket(t, ctx, host.URL, "settings-update")
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"id":"settings-update","method":"settings.update","params":{"config":{"hiddenModels":[]}}}`)); err != nil {
		t.Fatal(err)
	}
	_, response, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertRedacted(t, response)
	var reply struct {
		ID string `json:"id"`
		OK bool   `json:"ok"`
	}
	if err := json.Unmarshal(response, &reply); err != nil {
		t.Fatal(err)
	}
	if reply.ID != "settings-update" || !reply.OK {
		t.Fatalf("settings.update WebSocket response = %s", response)
	}
}
