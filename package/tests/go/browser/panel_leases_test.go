package browser_test

import (
	"bytes"
	"context"
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

const leasedPanel = "b-0123456789abcdef01"
const externalPanel = "b-0123456789abcdef02"

func createLeasedPanel(t *testing.T, runtime *testRuntime, session string) {
	t.Helper()
	request := browserRequest(t, context.Background(), testToken, "snapshot", session, nil)
	request.Header.Set("X-Pixie-Panel-Lease", "1")
	response := httptest.NewRecorder()
	runtime.service.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
}

func renewPanels(service http.Handler, token string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPost, "/v1/browser/leases", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	service.ServeHTTP(response, request)
	return response
}

func renewalBody(ids ...string) any { return map[string]any{"sessions": ids} }

func awaitSessionRemoval(t *testing.T, runtime *testRuntime, session string) {
	t.Helper()
	deadline := time.Now().Add(runtime.config.PanelLeaseTimeout + 3*time.Second)
	for {
		_, err := os.Stat(filepath.Join(runtime.config.StateRoot, session))
		if os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expired panel retained state: %s", session)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(runtime.config.ArtifactRoot, session)); !os.IsNotExist(err) {
		t.Fatalf("expired panel retained artifacts: %v", err)
	}
}

func TestControllerPanelLeaseReclaimsLostControllerWithoutTouchingMCPSessions(t *testing.T) {
	runtime := newTestRuntime(t, true, func(config *browser.Config) { config.PanelLeaseTimeout = 150 * time.Millisecond })
	server := httptest.NewServer(runtime.service)
	defer server.Close()
	panels := controller.NewBrowserPanels(controller.AuthConfig{BrowserURL: server.URL, BrowserEnabled: true, BrowserToken: testToken}, server.Client())
	defer panels.CloseAll(context.Background())
	id, err := panels.Open("client", "project")
	if err != nil {
		t.Fatal(err)
	}
	handler := controller.CoreHandler{BrowserPanels: panels}
	raw, _ := json.Marshal(map[string]any{"panelId": id, "action": map[string]any{"type": "snapshot"}})
	if _, err := handler.Handle(context.Background(), "browser.panelCommand", raw, "client"); err != nil {
		t.Fatal(err)
	}
	// Deliberately never start controller heartbeats: model a lost process.
	for _, session := range []string{"external-mcp", externalPanel} {
		if response := postBrowserRequest(t, runtime.service, testToken, "snapshot", session, nil); response.Code != 200 {
			t.Fatal(response.Body.String())
		}
	}
	awaitSessionRemoval(t, runtime, id)
	for _, session := range []string{"external-mcp", externalPanel} {
		if _, err := os.Stat(filepath.Join(runtime.config.StateRoot, session)); err != nil {
			t.Fatalf("unleased session removed: %s, %v", session, err)
		}
	}
}

func TestPanelLeaseRenewalIsAuthenticatedBoundedAndDoesNotAdoptSessions(t *testing.T) {
	runtime := newTestRuntime(t, true, func(config *browser.Config) { config.PanelLeaseTimeout = time.Second })
	createLeasedPanel(t, runtime, leasedPanel)
	if response := postBrowserRequest(t, runtime.service, testToken, "snapshot", externalPanel, nil); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	marker := filepath.Join(runtime.config.StateRoot, leasedPanel, ".controller-lease")
	before, _ := os.Stat(marker)
	for _, tc := range []struct {
		token  string
		body   any
		status int
	}{
		{"", renewalBody(leasedPanel), 401},
		{testToken, renewalBody("../escape"), 400},
		{testToken, renewalBody(leasedPanel, leasedPanel), 400},
		{testToken, map[string]any{"sessions": []string{}, "extra": true}, 400},
	} {
		if response := renewPanels(runtime.service, tc.token, tc.body); response.Code != tc.status {
			t.Fatalf("renewal status %d: %s", response.Code, response.Body.String())
		}
	}
	after, _ := os.Stat(marker)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("rejected renewal changed expiry")
	}
	for range 12 {
		time.Sleep(100 * time.Millisecond)
		response := renewPanels(runtime.service, testToken, renewalBody(leasedPanel, externalPanel, "b-000000000000000000"))
		var result struct{ Renewed []string }
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Renewed) != 1 || result.Renewed[0] != leasedPanel {
			t.Fatal(response.Body.String())
		}
	}
	if _, err := os.Stat(filepath.Join(runtime.config.StateRoot, externalPanel, ".controller-lease")); !os.IsNotExist(err) {
		t.Fatal("renewal adopted an unleased session")
	}
	if _, err := os.Stat(filepath.Join(runtime.config.StateRoot, "b-000000000000000000")); !os.IsNotExist(err) {
		t.Fatal("renewal created an absent session")
	}
	request := browserRequest(t, context.Background(), testToken, "snapshot", externalPanel, nil)
	request.Header.Set("X-Pixie-Panel-Lease", "1")
	response := httptest.NewRecorder()
	runtime.service.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatal("leased request adopted an existing external session")
	}
	awaitSessionRemoval(t, runtime, leasedPanel)
}

func TestPanelLeaseSurvivesServiceRestartAndRetriesFailedClose(t *testing.T) {
	runtime := newTestRuntime(t, true, func(config *browser.Config) { config.PanelLeaseTimeout = 150 * time.Millisecond })
	createLeasedPanel(t, runtime, leasedPanel)
	runtime.service.Shutdown()
	original, err := os.ReadFile(runtime.config.AgentBrowser)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtime.config.AgentBrowser, []byte(strings.Replace(string(original), "close) exit 0", "close) exit 9", 1)), 0700); err != nil {
		t.Fatal(err)
	}
	restarted, err := browser.NewService(runtime.config, diagnostics.NormalizeBuild("test", "test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Shutdown()
	time.Sleep(350 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(runtime.config.StateRoot, leasedPanel, "run")); err != nil {
		t.Fatalf("failed close discarded runtime addressing: %v", err)
	}
	if err := os.WriteFile(runtime.config.AgentBrowser, original, 0700); err != nil {
		t.Fatal(err)
	}
	awaitSessionRemoval(t, runtime, leasedPanel)
}

func TestActiveCommandPreventsLeaseExpiryAndRenewsOnCompletion(t *testing.T) {
	runtime := newTestRuntime(t, true, func(config *browser.Config) { config.PanelLeaseTimeout = 150 * time.Millisecond })
	original, _ := os.ReadFile(runtime.config.AgentBrowser)
	script := strings.Replace(string(original), "snapshot) printf", "snapshot) /bin/sleep 0.4; printf", 1)
	if err := os.WriteFile(runtime.config.AgentBrowser, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	createLeasedPanel(t, runtime, leasedPanel)
	if _, err := os.Stat(filepath.Join(runtime.config.StateRoot, leasedPanel, "run")); err != nil {
		t.Fatalf("active command lost its panel: %v", err)
	}
	response := renewPanels(runtime.service, testToken, renewalBody(leasedPanel))
	if response.Code != 200 || !strings.Contains(response.Body.String(), leasedPanel) {
		t.Fatal(response.Body.String())
	}
	awaitSessionRemoval(t, runtime, leasedPanel)
}

// Opt-in service acceptance against the actual agent-browser and Chromium.
// Compile this package's test binary in Linux, then run it in browser-automation
// with isolated state and these executable/configuration paths in the environment.
func TestRealBrowserPanelLeaseLifecycle(t *testing.T) {
	executable := os.Getenv("PIXIE_TEST_AGENT_BROWSER")
	if executable == "" {
		t.Skip("real browser runtime is not configured")
	}
	root, err := os.MkdirTemp("/tmp", "gbl-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	config, err := browser.ConfigFromEnvironment(func(key string) (string, bool) {
		values := map[string]string{"PIXIE_BROWSER_AUTH": "true", "PIXIE_BROWSER_TOKEN": testToken}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	config.AgentBrowser = executable
	config.BrowserConfig = os.Getenv("PIXIE_TEST_BROWSER_CONFIG")
	config.ArtifactRoot, config.StateRoot = filepath.Join(root, "a"), filepath.Join(root, "s")
	config.PanelLeaseTimeout = 3 * time.Second
	service, err := browser.NewService(config, diagnostics.NormalizeBuild("test", "test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Shutdown()
	runtime := &testRuntime{service: service, config: config, root: root}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><title>Lease fixture</title><h1>Lease fixture</h1>"))
	}))
	defer site.Close()
	request := browserRequest(t, context.Background(), testToken, "open", leasedPanel, []string{site.URL})
	request.Header.Set("X-Pixie-Panel-Lease", "1")
	response := httptest.NewRecorder()
	service.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	response = postBrowserRequest(t, service, testToken, "screenshot", leasedPanel, []string{"lease.png"})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	artifact := httptest.NewRecorder()
	get := httptest.NewRequest(http.MethodGet, "/v1/artifacts/"+leasedPanel+"/lease.png", nil)
	get.Header.Set("Authorization", "Bearer "+testToken)
	service.ServeHTTP(artifact, get)
	if artifact.Code != 200 || !bytes.HasPrefix(artifact.Body.Bytes(), []byte{137, 80, 78, 71}) {
		t.Fatal("real screenshot was not served")
	}
	if response = postBrowserRequest(t, service, testToken, "open", "external", []string{site.URL}); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	awaitSessionRemoval(t, runtime, leasedPanel)
	response = postBrowserRequest(t, service, testToken, "snapshot", "external", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Lease fixture") {
		t.Fatal("external browser was closed with the leased panel: " + response.Body.String())
	}
	if response = postBrowserRequest(t, service, testToken, "close", "external", nil); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
}
