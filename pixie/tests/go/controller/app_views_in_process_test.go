package controller_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/browser"
	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/diagnostics"
)

const appViewInProcessToken = "in-process-app-token-0123456789abcdef"

func testBrowserService(t *testing.T) *browser.Service {
	t.Helper()
	root := t.TempDir()
	configFile := filepath.Join(root, "browser.json")
	if err := os.WriteFile(configFile, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	service, err := browser.NewService(browser.Config{
		Host: "127.0.0.1", Port: 7312, Authentication: true, Token: appViewInProcessToken,
		ArtifactRoot: filepath.Join(root, "artifacts"), StateRoot: filepath.Join(root, "state"),
		AgentBrowser: "/bin/true", BrowserConfig: configFile,
	}, diagnostics.BuildInfo{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Shutdown)
	return service
}

func appViewFixture(t *testing.T) (*controller.SessionManager, string) {
	t.Helper()
	app := controller.AppAttachment{ToolName: "show", ExtensionName: "appserver", ResourceURI: "ui://fixture/unlisted"}
	updates := []map[string]any{{"__native": map[string]any{
		"type": "tool_execution_end", "toolCallId": "app-call", "toolName": "mcp",
		"result": map[string]any{"content": []any{}, "details": map[string]any{"mcp": map[string]any{"app": map[string]any{"toolName": app.ToolName, "extensionName": app.ExtensionName, "resourceUri": app.ResourceURI, "toolNameIsActual": true}}}},
	}}}
	manager, _, project, _ := newSessionManagerWithInitializeAndPublisher(t, updates, nil, piInitializeResponse(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "owner"); err != nil {
		t.Fatal(err)
	}
	return manager, project.ID
}

func openInProcessView(t *testing.T, apps *controller.AppViews, projectID, parentOrigin string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := apps.Open(ctx, projectID, "chat", "app-call", parentOrigin, "client-a")
	if err != nil {
		t.Fatal(err)
	}
	opened, ok := result.(controller.AppViewOpenResult)
	if !ok {
		t.Fatalf("unexpected open result: %#v", result)
	}
	if opened.ViewID == "" || opened.URL == "" {
		t.Fatalf("incomplete open result: %#v", result)
	}
	return map[string]any{"viewId": opened.ViewID, "url": opened.URL}
}

func TestAppViewsOpenCloseThroughInProcessSandbox(t *testing.T) {
	manager, projectID := appViewFixture(t)
	service := testBrowserService(t)
	auth := controller.AuthConfig{MCPToken: appViewInProcessToken, ControllerToken: "distinct-web-ui-token-0123456789abcd"}
	apps := controller.NewAppViews(manager, auth, 7312)
	apps.SetBrowserHandler(func() http.Handler { return service })

	opened := openInProcessView(t, apps, projectID, "http://127.0.0.1:7312")
	viewID, _ := opened["viewId"].(string)
	viewURL, _ := opened["url"].(string)
	if !strings.HasPrefix(viewURL, "http://localhost:7312/v1/app-views/") {
		t.Fatalf("sandbox origin not isolated from parent: %s", viewURL)
	}

	// The mirror direction swaps back, so neither loopback spelling can nest.
	mirrored := openInProcessView(t, apps, projectID, "http://localhost:7312")
	mirrorURL, _ := mirrored["url"].(string)
	if !strings.HasPrefix(mirrorURL, "http://127.0.0.1:7312/v1/app-views/") {
		t.Fatalf("mirror sandbox origin not isolated: %s", mirrorURL)
	}
	mirrorID, _ := mirrored["viewId"].(string)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := apps.Close(ctx, viewID, "client-b"); err == nil {
		t.Fatal("another client closed the view")
	}
	if err := apps.Close(ctx, viewID, "client-a"); err != nil {
		t.Fatalf("owner close: %v", err)
	}
	if err := apps.Close(ctx, mirrorID, "client-a"); err != nil {
		t.Fatalf("mirror close: %v", err)
	}
}

func TestAppViewsRejectUnavailableSandbox(t *testing.T) {
	manager, projectID := appViewFixture(t)
	auth := controller.AuthConfig{MCPToken: appViewInProcessToken}
	apps := controller.NewAppViews(manager, auth, 7312)
	apps.SetBrowserHandler(func() http.Handler { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := apps.Open(ctx, projectID, "chat", "app-call", "http://127.0.0.1:7312", "client-a"); err == nil {
		t.Fatal("open succeeded without a sandbox")
	}
}

func TestAppViewsExternalServicePathUnchanged(t *testing.T) {
	manager, projectID := appViewFixture(t)
	ticket := make([]byte, 32)
	if _, err := rand.Read(ticket); err != nil {
		t.Fatal(err)
	}
	ticketHex := hex.EncodeToString(ticket)
	var stub *httptest.Server
	stub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/app-views" {
			t.Errorf("unexpected external request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer external-browser-token-0123456789" {
			t.Error("external request lost the browser credential")
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ticket": ticketHex, "path": "/v1/app-views/" + ticketHex,
			"url":       stub.URL + "/v1/app-views/" + ticketHex,
			"expiresAt": time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano),
		})
	}))
	defer stub.Close()
	auth := controller.AuthConfig{
		BrowserURL: stub.URL, BrowserEnabled: true, BrowserToken: "external-browser-token-0123456789",
	}
	apps := controller.NewAppViews(manager, auth, 7312)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := apps.Open(ctx, projectID, "chat", "app-call", "http://127.0.0.1:7312", "client-a")
	if err != nil {
		t.Fatalf("external open: %v", err)
	}
	opened := result.(controller.AppViewOpenResult)
	if !strings.HasPrefix(opened.URL, stub.URL+"/v1/app-views/") {
		t.Fatalf("external view URL wrong: %s", opened.URL)
	}
}
