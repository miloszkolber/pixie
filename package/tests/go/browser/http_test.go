package browser_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/browser"
	"github.com/miloszkolber/pixie/internal/diagnostics"
)

func TestConfigurationRejectsUnsafeBindingsAndWeakCredentials(t *testing.T) {
	lookup := func(values map[string]string) func(string) (string, bool) {
		return func(key string) (string, bool) { value, found := values[key]; return value, found }
	}
	configuration, err := browser.ConfigFromEnvironment(lookup(map[string]string{}))
	if err != nil || configuration.Host != "127.0.0.1" || configuration.Port != 8787 || configuration.Authentication {
		t.Fatalf("default configuration = %#v, %v", configuration, err)
	}
	configuration, err = browser.ConfigFromEnvironment(lookup(map[string]string{
		"PIXIE_BROWSER_HOST":          "127.0.0.2",
		"PIXIE_BROWSER_PORT":          "9000",
		"PIXIE_BROWSER_AUTH":          "true",
		"PIXIE_BROWSER_TOKEN":         testToken,
		"PIXIE_BROWSER_PUBLIC_ORIGIN": "https://Browser.Example:443",
	}))
	if err != nil || configuration.Host != "127.0.0.2" || configuration.Port != 9000 || !configuration.Authentication || configuration.PublicOrigin != "https://browser.example" {
		t.Fatalf("configured environment = %#v, %v", configuration, err)
	}
	for _, values := range []map[string]string{
		{"PIXIE_BROWSER_AUTH": "1"},
		{"PIXIE_BROWSER_PORT": "0"},
		{"PIXIE_BROWSER_HOST": "0.0.0.0"},
		{"PIXIE_BROWSER_HOST": "browser.example"},
		{"PIXIE_BROWSER_PUBLIC_ORIGIN": "https://browser.example"},
		{"PIXIE_BROWSER_AUTH": "true", "PIXIE_BROWSER_PUBLIC_ORIGIN": "https://browser.example/path"},
	} {
		if _, err := browser.ConfigFromEnvironment(lookup(values)); err == nil {
			t.Fatalf("invalid environment accepted: %#v", values)
		}
	}
	runtime := newTestRuntime(t, false, nil)
	for _, token := range []string{"short", "INVALID_REPLACE_WITH_RANDOM_BROWSER_TOKEN", strings.Repeat("x", 257)} {
		weak := runtime.config
		weak.Authentication = true
		weak.Token = token
		if _, err := browser.NewService(weak, diagnostics.NormalizeBuild("", ""), nil); err == nil {
			t.Fatalf("weak browser token was accepted: %q", token)
		}
	}
}

func TestHTTPBoundaryAppliesAuthorizationPolicyAndOutputBounds(t *testing.T) {
	runtime := newTestRuntime(t, true, nil)
	health := httptest.NewRecorder()
	runtime.service.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK || health.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("health response = %d, %#v", health.Code, health.Header())
	}

	wrongMethod := httptest.NewRecorder()
	runtime.service.ServeHTTP(wrongMethod, httptest.NewRequest(http.MethodGet, "/v1/browser", nil))
	if wrongMethod.Code != http.StatusMethodNotAllowed || wrongMethod.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("wrong-method response = %d", wrongMethod.Code)
	}
	unauthorized := postBrowserRequest(t, runtime.service, "", "snapshot", "secure", nil)
	if unauthorized.Code != http.StatusUnauthorized || responseCode(t, unauthorized) != "unauthorized" {
		t.Fatalf("unauthorized response = %d %s", unauthorized.Code, unauthorized.Body.String())
	}

	unsupported := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/browser", strings.NewReader("{}"))
	request.Header.Set("Authorization", "Bearer "+testToken)
	request.Header.Set("Content-Type", "text/plain")
	runtime.service.ServeHTTP(unsupported, request)
	if unsupported.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("content-type response = %d", unsupported.Code)
	}

	success := postBrowserRequest(t, runtime.service, testToken, "snapshot", "bounded", nil)
	var result map[string]any
	if err := json.Unmarshal(success.Body.Bytes(), &result); err != nil || success.Code != http.StatusOK || result["stdout"] != "stdout" || result["stderr"] != "stderr" {
		t.Fatalf("captured output = %d %#v, %v", success.Code, result, err)
	}
	t.Setenv("PIXIE_PRIVATE_TEST", "must-not-reach-browser")
	environment := postBrowserRequest(t, runtime.service, testToken, "get", "environment", []string{"title"})
	if err := json.Unmarshal(environment.Body.Bytes(), &result); err != nil || environment.Code != http.StatusOK {
		t.Fatalf("environment command = %d %#v, %v", environment.Code, result, err)
	}
	expectedEnvironment := strings.Join([]string{
		"browser",
		filepath.Dir(runtime.config.AgentBrowser),
		filepath.Join(runtime.config.StateRoot, "environment", "home"),
		filepath.Join(runtime.config.StateRoot, "environment", "tmp"),
		filepath.Join(runtime.config.StateRoot, "environment", "home", ".config"),
		filepath.Join(runtime.config.StateRoot, "environment", "home", ".local", "share"),
		filepath.Join(runtime.config.StateRoot, "environment", "home", ".local", "state"),
		filepath.Join(runtime.config.StateRoot, "environment", "run"),
		"1", "20000", "unset", "",
	}, "\n")
	if result["stdout"] != expectedEnvironment {
		t.Fatalf("browser environment = %q", result["stdout"])
	}

	for _, rejected := range []struct {
		command string
		args    []string
	}{
		{command: "eval"},
		{command: "open", args: []string{"file:///etc/passwd"}},
		{command: "open", args: []string{"https://user:pass@example.com"}},
		{command: "snapshot", args: []string{"--headers", "secret"}},
		{command: "screenshot", args: []string{"../screen.png"}},
		{command: "wait", args: []string{"30001"}},
		{command: "set", args: []string{"viewport", "9999", "720"}},
	} {
		response := postBrowserRequest(t, runtime.service, testToken, rejected.command, "bounded", rejected.args)
		if response.Code != http.StatusBadRequest || responseCode(t, response) != "invalid_request" {
			t.Fatalf("invalid request accepted: %s %#v -> %d %s", rejected.command, rejected.args, response.Code, response.Body.String())
		}
	}

	large := httptest.NewRequest(http.MethodPost, "/v1/browser", io.NopCloser(strings.NewReader("{"+strings.Repeat("x", 64*1024+1))))
	large.Header.Set("Authorization", "Bearer "+testToken)
	large.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	runtime.service.ServeHTTP(response, large)
	if response.Code != http.StatusRequestEntityTooLarge || responseCode(t, response) != "request_too_large" {
		t.Fatalf("large request = %d %s", response.Code, response.Body.String())
	}
}

func TestArtifactsShareQuotasAndRejectPathEscapes(t *testing.T) {
	runtime := newTestRuntime(t, true, nil)
	first := postBrowserRequest(t, runtime.service, testToken, "screenshot", "screens", []string{"screen.png"})
	if first.Code != http.StatusOK {
		t.Fatalf("screenshot response = %d %s", first.Code, first.Body.String())
	}
	duplicate := postBrowserRequest(t, runtime.service, testToken, "screenshot", "screens", []string{"screen.png"})
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate response = %d", duplicate.Code)
	}

	artifact := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/artifacts/screens/screen.png", nil)
	request.Header.Set("Authorization", "Bearer "+testToken)
	runtime.service.ServeHTTP(artifact, request)
	if artifact.Code != http.StatusOK || artifact.Body.String() != "fake-image" || artifact.Header().Get("X-Content-Type-Options") != "nosniff" || artifact.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("artifact response = %d %q %#v", artifact.Code, artifact.Body.String(), artifact.Header())
	}
	if err := os.Symlink(filepath.Join(runtime.root, "outside.png"), filepath.Join(runtime.config.ArtifactRoot, "screens", "linked.png")); err != nil {
		t.Fatal(err)
	}
	linked := httptest.NewRecorder()
	linkedRequest := httptest.NewRequest(http.MethodGet, "/v1/artifacts/screens/linked.png", nil)
	linkedRequest.Header.Set("Authorization", "Bearer "+testToken)
	runtime.service.ServeHTTP(linked, linkedRequest)
	if linked.Code != http.StatusNotFound {
		t.Fatalf("symlink response = %d", linked.Code)
	}
	second := postBrowserRequest(t, runtime.service, testToken, "screenshot", "quota", []string{"two.png"})
	if second.Code != http.StatusRequestEntityTooLarge || responseCode(t, second) != "quota_exceeded" {
		t.Fatalf("global quota response = %d %s", second.Code, second.Body.String())
	}
}

func TestCommandFailuresCancellationAndQuotasRemoveSessionState(t *testing.T) {
	t.Run("request cancellation", func(t *testing.T) {
		runtime := newTestRuntime(t, false, nil)
		ctx, cancel := context.WithCancel(context.Background())
		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			runtime.service.ServeHTTP(response, browserRequest(t, ctx, "", "open", "cancelled", []string{"https://example.com"}))
			close(done)
		}()
		waitForPath(t, filepath.Join(runtime.config.StateRoot, "cancelled", ".lock"))
		busy := postBrowserRequest(t, runtime.service, "", "snapshot", "cancelled", nil)
		if busy.Code != http.StatusConflict || responseCode(t, busy) != "session_busy" {
			t.Fatalf("concurrent session response = %d %s", busy.Code, busy.Body.String())
		}
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("cancelled browser command did not terminate")
		}
		if response.Code != 499 || responseCode(t, response) != "request_cancelled" {
			t.Fatalf("cancelled response = %d %s", response.Code, response.Body.String())
		}
		if _, err := os.Stat(filepath.Join(runtime.config.StateRoot, "cancelled")); !os.IsNotExist(err) {
			t.Fatalf("cancelled state remains: %v", err)
		}
	})

	tests := []struct {
		name, command, code string
		args                []string
		configure           func(*browser.Config)
		status              int
	}{
		{name: "signal", command: "back", code: "child_process", status: http.StatusBadGateway},
		{name: "inherited pipe", command: "press", args: []string{"Enter"}, code: "child_process", status: http.StatusBadGateway, configure: func(config *browser.Config) {
			config.CommandTimeout = 4 * time.Second
			config.RequestTimeout = 6 * time.Second
		}},
		{name: "state bytes", command: "forward", code: "quota_exceeded", status: http.StatusRequestEntityTooLarge},
		{name: "state entries", command: "reload", code: "quota_exceeded", status: http.StatusRequestEntityTooLarge, configure: func(config *browser.Config) {
			config.MaxStateBytes = 1 << 20
			config.MaxStateEntries = 4
		}},
		{name: "combined output", command: "vitals", code: "output_limit", status: http.StatusRequestEntityTooLarge, configure: func(config *browser.Config) {
			config.MaxStateBytes = 1 << 20
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := newTestRuntime(t, false, test.configure)
			response := postBrowserRequest(t, runtime.service, "", test.command, strings.ReplaceAll(test.name, " ", "-"), test.args)
			if response.Code != test.status || responseCode(t, response) != test.code {
				t.Fatalf("failure response = %d %s", response.Code, response.Body.String())
			}
			if _, err := os.Stat(filepath.Join(runtime.config.StateRoot, strings.ReplaceAll(test.name, " ", "-"))); !os.IsNotExist(err) {
				t.Fatalf("failed command retained state: %v", err)
			}
		})
	}
}

func TestReadinessStatusAndCountersExposeBoundedOperationalState(t *testing.T) {
	runtime := newTestRuntime(t, true, nil)
	readyResponse := httptest.NewRecorder()
	runtime.service.ServeHTTP(readyResponse, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var ready struct {
		Ready  bool `json:"ready"`
		Checks struct {
			Executable, Config, ArtifactStorage, StateStorage bool
		} `json:"checks"`
	}
	if err := json.Unmarshal(readyResponse.Body.Bytes(), &ready); err != nil || readyResponse.Code != http.StatusOK || !ready.Ready || !ready.Checks.Executable || !ready.Checks.Config || !ready.Checks.ArtifactStorage || !ready.Checks.StateStorage {
		t.Fatalf("readiness = %d %#v, %v", readyResponse.Code, ready, err)
	}
	for _, root := range []string{runtime.config.StateRoot, runtime.config.ArtifactRoot} {
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			t.Fatalf("readiness launched a browser session in %s: %#v, %v", root, entries, err)
		}
	}

	unauthorized := httptest.NewRecorder()
	runtime.service.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/status", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	if response := postBrowserRequest(t, runtime.service, testToken, "snapshot", "metrics", nil); response.Code != http.StatusOK {
		t.Fatalf("HTTP command = %d %s", response.Code, response.Body.String())
	}
	var failed browserToolResult
	mcpRoundTrip(t, runtime.service, "tools/call", map[string]any{"name": "browser_command", "arguments": map[string]any{"command": "click", "session": "metrics", "args": []string{"#missing"}}}, &failed)
	if !failed.IsError {
		t.Fatalf("failed MCP command = %#v", failed)
	}
	var rejected browserToolResult
	mcpRoundTrip(t, runtime.service, "tools/call", map[string]any{"name": "browser_command", "arguments": map[string]any{"command": "eval", "session": "metrics"}}, &rejected)
	if !rejected.IsError || rejected.StructuredContent["outcome"] != "rejected" {
		t.Fatalf("invalid MCP command = %#v", rejected)
	}

	request := httptest.NewRequest(http.MethodGet, "/status", nil)
	request.Header.Set("Authorization", "Bearer "+testToken)
	statusResponse := httptest.NewRecorder()
	runtime.service.ServeHTTP(statusResponse, request)
	var status struct {
		Build   struct{ Version, Revision string } `json:"build"`
		Process struct {
			Goroutines int `json:"goroutines"`
		} `json:"process"`
		Requests struct {
			Total, Failures uint64
			Active          int64
		} `json:"requests"`
	}
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &status); err != nil || statusResponse.Code != http.StatusOK || status.Build.Version != "2.3.4" || status.Build.Revision != "abcdef123456" || status.Process.Goroutines < 1 || status.Requests.Total != 2 || status.Requests.Failures != 1 || status.Requests.Active != 0 {
		t.Fatalf("status = %d %#v, %v", statusResponse.Code, status, err)
	}
	for _, secret := range []string{testToken, runtime.root, runtime.config.AgentBrowser, runtime.config.BrowserConfig, runtime.config.ArtifactRoot, runtime.config.StateRoot} {
		if strings.Contains(statusResponse.Body.String(), secret) {
			t.Fatalf("status exposed private configuration %q", secret)
		}
	}

	if err := os.Chmod(runtime.config.AgentBrowser, 0o600); err != nil {
		t.Fatal(err)
	}
	degraded := httptest.NewRecorder()
	runtime.service.ServeHTTP(degraded, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if degraded.Code != http.StatusServiceUnavailable || json.Unmarshal(degraded.Body.Bytes(), &ready) != nil || ready.Ready || ready.Checks.Executable {
		t.Fatalf("degraded readiness = %d %#v", degraded.Code, ready)
	}
}
