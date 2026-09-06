package controller_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/miloszkolber/pixie/internal/browser"
	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/persist"
)

func TestPersistentBrowserPanelsInProcessOpenClose(t *testing.T) {
	root := t.TempDir()
	configFile := filepath.Join(root, "browser.json")
	if err := os.WriteFile(configFile, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	// Closing a panel with no commands is idempotent and needs no Chromium.
	service, err := browser.NewService(browser.Config{
		Host: "127.0.0.1", Port: 7312, Authentication: true, Token: browserPanelToken,
		ArtifactRoot: filepath.Join(root, "artifacts"), StateRoot: filepath.Join(root, "state"),
		AgentBrowser: "/bin/true", BrowserConfig: configFile,
	}, diagnostics.BuildInfo{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Shutdown)
	var calls atomic.Int32
	var panelID string
	panels, err := controller.NewPersistentBrowserPanels(controller.AuthConfig{
		MCPToken: browserPanelToken, Enabled: true, ControllerToken: "distinct-web-ui-token-0123456789abcd",
	}, nil, persist.Store{Dir: t.TempDir()}, func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if r.Method != http.MethodPost || r.URL.Path != "/v1/browser" || r.RequestURI != "/v1/browser" || r.Host != "127.0.0.1" {
				t.Errorf("invalid in-process request: %s URL=%v URI=%q Host=%q", r.Method, r.URL, r.RequestURI, r.Host)
			}
			if r.Header.Get("Authorization") != "Bearer "+browserPanelToken || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Pixie-Panel-Lease") != "1" {
				t.Error("missing Browser credential or command headers")
			}
			var body struct {
				Session string `json:"session"`
				Command string `json:"command"`
			}
			// Inspect without replacing the request passed to the real service.
			content, readErr := io.ReadAll(r.Body)
			if readErr != nil || json.Unmarshal(content, &body) != nil || body.Session != panelID || body.Command != "close" {
				t.Error("invalid owned-session close command")
			}
			r.Body = io.NopCloser(bytes.NewReader(content))
			service.ServeHTTP(w, r)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { panels.CloseAll(context.Background()) })
	panelID, err = panels.Open("client-a", "project-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := panels.Close(context.Background(), "client-b", panelID); err == nil || calls.Load() != 0 {
		t.Fatal("another client reached the Browser handler")
	}
	if err := panels.Close(context.Background(), "client-a", panelID); err != nil {
		t.Fatalf("real Browser close: %v", err)
	}
	if err := panels.Close(context.Background(), "client-a", panelID); err != nil || calls.Load() != 1 {
		t.Fatalf("close was not idempotent: calls=%d err=%v", calls.Load(), err)
	}
}

func TestPersistentBrowserPanelsMissingHandlerFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		browser func() http.Handler
	}{
		{name: "missing callback"},
		{name: "unavailable module", browser: func() http.Handler { return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			panels, err := controller.NewPersistentBrowserPanels(controller.AuthConfig{MCPToken: browserPanelToken}, nil, persist.Store{Dir: t.TempDir()}, tc.browser)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { panels.CloseAll(context.Background()) })
			id, err := panels.Open("client", "project")
			if err != nil {
				t.Fatal(err)
			}
			if err := panels.Close(context.Background(), "client", id); err == nil || !strings.Contains(err.Error(), "unavailable") {
				t.Fatalf("missing handler did not return unavailable: %v", err)
			}
		})
	}
}

func TestPersistentBrowserPanelsExternalURLBypassesHandler(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/browser" || r.Header.Get("Authorization") != "Bearer "+browserPanelToken {
			t.Error("external Browser path or credential changed")
		}
		_, _ = w.Write([]byte(`{"outcome":"completed","command":"close","code":0}`))
	}))
	defer server.Close()
	panels, err := controller.NewPersistentBrowserPanels(controller.AuthConfig{
		BrowserURL: server.URL, BrowserEnabled: true, BrowserToken: browserPanelToken,
		MCPToken: "distinct-publisher-token-0123456789",
	}, server.Client(), persist.Store{Dir: t.TempDir()}, func() http.Handler {
		t.Error("external Browser request consulted in-process handler")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer panels.CloseAll(context.Background())
	id, err := panels.Open("client", "project")
	if err != nil {
		t.Fatal(err)
	}
	if err := panels.Close(context.Background(), "client", id); err != nil || calls.Load() != 1 {
		t.Fatalf("external close: calls=%d err=%v", calls.Load(), err)
	}
}
