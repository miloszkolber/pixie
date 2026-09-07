package mcphost_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/browser"
	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/mcphost"
)

const hostTestToken = "mcp-host-test-token-0123456789abcdef0123456789"

func TestConfigFromEnvironmentMapsMCPSettingsToBrowserModule(t *testing.T) {
	values := map[string]string{
		"PIXIE_MCP_HOST":             "127.0.0.1",
		"PIXIE_MCP_PORT":             "9876",
		"PIXIE_MCP_AUTH":             "true",
		"PIXIE_MCP_TOKEN":            hostTestToken,
		"PIXIE_MCP_MODULES":          "browser",
		"PIXIE_MCP_DISABLED_MODULES": "",
	}
	config, err := mcphost.ConfigFromEnvironment(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.Host != "127.0.0.1" || config.Port != 9876 || !config.Authentication || config.Token != hostTestToken {
		t.Fatalf("host config = %#v", config)
	}
	if config.BrowserConfig.Host != config.Host || config.BrowserConfig.Port != config.Port || !config.BrowserConfig.Authentication || config.BrowserConfig.Token != config.Token {
		t.Fatalf("browser config did not inherit host settings: %#v", config.BrowserConfig)
	}
}

func TestDisabledModuleIsNeverInitializedOrPublished(t *testing.T) {
	config := testConfig(t)
	config.BrowserConfig.AgentBrowser = filepath.Join(t.TempDir(), "missing-agent-browser")
	config.BrowserConfig.BrowserConfig = filepath.Join(t.TempDir(), "missing-config.json")
	config.DisabledModules = []string{"browser"}
	service, err := mcphost.NewService(config, diagnostics.NormalizeBuild("1", "rev"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Shutdown()
	catalog := service.Catalog()
	if len(catalog.Modules) != 0 || catalog.Gateway.State != "ready" {
		t.Fatalf("disabled catalog = %#v", catalog)
	}
	for _, path := range []string{"/browser", "/mcp", "/status", "/v1/browser", "/v1/browser/leases"} {
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte("{}")))
		request.Host = "127.0.0.1:8787"
		response := httptest.NewRecorder()
		service.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("disabled module route %s status = %d", path, response.Code)
		}
	}
}

func TestBrowserModuleHasCanonicalAndLegacyRoutes(t *testing.T) {
	service, err := mcphost.NewService(testConfig(t), diagnostics.NormalizeBuild("1", "rev"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Shutdown()
	for _, path := range []string{"/browser", "/mcp"} {
		t.Run(path, func(t *testing.T) {
			body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			request.Host = "127.0.0.1:8787"
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json, text/event-stream")
			response := httptest.NewRecorder()
			service.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
			}
			var result map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatalf("response JSON: %v", err)
			}
			if result["result"] == nil {
				t.Fatalf("initialize result = %#v", result)
			}
		})
	}
	request := httptest.NewRequest(http.MethodGet, mcphost.StatusPath, nil)
	request.Host = "127.0.0.1:8787"
	response := httptest.NewRecorder()
	service.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("legacy browser status route status = %d body = %s", response.Code, response.Body.String())
	}
	var status map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatalf("legacy browser status JSON: %v", err)
	}
	if status["build"] == nil || status["readiness"] == nil {
		t.Fatalf("legacy browser status = %#v", status)
	}
	request = httptest.NewRequest(http.MethodGet, mcphost.GatewayStatusPath, nil)
	request.Host = "127.0.0.1:8787"
	response = httptest.NewRecorder()
	service.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("MCP host status route status = %d body = %s", response.Code, response.Body.String())
	}
	var gatewayStatus map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &gatewayStatus); err != nil {
		t.Fatalf("MCP host status JSON: %v", err)
	}
	if gatewayStatus["catalog"] == nil {
		t.Fatalf("MCP host status = %#v", gatewayStatus)
	}
}

func TestCatalogRequiresHostAuthentication(t *testing.T) {
	config := testConfig(t)
	config.Authentication = true
	config.Token = hostTestToken
	service, err := mcphost.NewService(config, diagnostics.NormalizeBuild("1", "rev"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Shutdown()
	request := httptest.NewRequest(http.MethodGet, mcphost.CatalogPath, nil)
	request.Host = "127.0.0.1:8787"
	response := httptest.NewRecorder()
	service.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d", response.Code)
	}
	request.Header.Set("Authorization", "Bearer "+hostTestToken)
	response = httptest.NewRecorder()
	service.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("valid token status = %d body = %s", response.Code, response.Body.String())
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
	request = httptest.NewRequest(http.MethodPost, "/browser", strings.NewReader(body))
	request.Host = "127.0.0.1:8787"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response = httptest.NewRecorder()
	service.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing module token status = %d", response.Code)
	}
	request.Header.Set("Authorization", "Bearer "+hostTestToken)
	response = httptest.NewRecorder()
	service.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("valid module token status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestConfigRejectsUnknownAndDuplicateModuleNames(t *testing.T) {
	for _, value := range []string{"browser,browser", "future"} {
		t.Run(value, func(t *testing.T) {
			values := map[string]string{"PIXIE_MCP_MODULES": value}
			_, err := mcphost.ConfigFromEnvironment(func(key string) (string, bool) {
				value, ok := values[key]
				return value, ok
			})
			if err == nil {
				t.Fatal("accepted invalid module list")
			}
		})
	}
}

func testConfig(t *testing.T) mcphost.Config {
	t.Helper()
	root := t.TempDir()
	agentBrowser, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifactRoot := filepath.Join(root, "artifacts")
	stateRoot := filepath.Join(root, "state")
	if err := os.MkdirAll(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	return mcphost.Config{
		Host: "127.0.0.1", Port: 8787, Modules: []string{"browser"},
		BrowserConfig: browser.Config{
			Host: "127.0.0.1", Port: 8787, AgentBrowser: agentBrowser, BrowserConfig: configPath,
			ArtifactRoot: artifactRoot, StateRoot: stateRoot, MaxArtifactBytes: 64 * 1024,
			MaxTotalArtifactBytes: 256 * 1024, MaxStateBytes: 256 * 1024, MaxStateEntries: 100,
		},
	}
}

func TestBrowserLeaseRouteUsesModuleAuthentication(t *testing.T) {
	config := testConfig(t)
	config.Authentication, config.BrowserConfig.Authentication = true, true
	config.Token, config.BrowserConfig.Token = hostTestToken, hostTestToken
	service, err := mcphost.NewService(config, diagnostics.NormalizeBuild("test", "test"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Shutdown()
	for _, token := range []string{"", hostTestToken} {
		request := httptest.NewRequest(http.MethodPost, "/v1/browser/leases", strings.NewReader(`{"sessions":[]}`))
		request.Host = "127.0.0.1:8787"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		service.ServeHTTP(response, request)
		expected := http.StatusOK
		if token == "" {
			expected = http.StatusUnauthorized
		}
		if response.Code != expected {
			t.Fatalf("lease route status %d: %s", response.Code, response.Body.String())
		}
	}
}
