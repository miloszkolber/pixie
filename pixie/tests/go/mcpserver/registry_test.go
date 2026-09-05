package mcpserver_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/mcpserver"
)

const registryTestToken = "mcp-registry-test-token-0123456789abcdef0123456789"

func testRegistry(t *testing.T, mutate func(*mcpserver.Config)) *mcpserver.Registry {
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
	config := mcpserver.Config{
		Host: "127.0.0.1", Port: 17871, DataDir: filepath.Join(root, "data"),
		Binaries: testBinaries(t, root, agentBrowser, configPath),
	}
	if mutate != nil {
		mutate(&config)
	}
	if err := os.MkdirAll(config.DataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	registry, err := mcpserver.NewRegistry(config, diagnostics.NormalizeBuild("test", "test"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Shutdown)
	return registry
}

func testBinaries(t *testing.T, root, agentBrowser, configPath string) *mcpserver.BinaryConfig {
	t.Helper()
	return &mcpserver.BinaryConfig{
		AgentBrowser: agentBrowser, BrowserConfig: configPath,
		ArtifactRoot: filepath.Join(root, "artifacts"), StateRoot: filepath.Join(root, "state"),
	}
}

func serve(registry *mcpserver.Registry, method, path, body, host string, headers map[string]string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Host = host
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	registry.ServeHTTP(response, request)
	return response
}

func TestRegistryPublishesBrowserByDefault(t *testing.T) {
	registry := testRegistry(t, nil)
	catalog := registry.Catalog()
	if catalog.SchemaVersion != 1 || catalog.Engine != "in-process" || catalog.Gateway.State != "ready" {
		t.Fatalf("default catalog = %#v", catalog)
	}
	if len(catalog.Modules) != 1 {
		t.Fatalf("default modules = %#v", catalog.Modules)
	}
	module := catalog.Modules[0]
	if module.ID != "browser" || module.ExtensionName != "pixie-browser" || module.Path != "/mcp/browser" ||
		module.Transport != "streamable_http" || !module.Enabled || module.State != "ready" {
		t.Fatalf("default module = %#v", module)
	}
	if module.Endpoint != "http://127.0.0.1:17871/mcp/browser" {
		t.Fatalf("default endpoint = %q", module.Endpoint)
	}
	if ready, _ := registry.Health("browser"); !ready {
		t.Fatal("browser module is not ready with fixture binaries")
	}
}

func TestRegistryRoutesBrowserModuleWithToken(t *testing.T) {
	registry := testRegistry(t, func(config *mcpserver.Config) { config.Token = registryTestToken })
	host := "127.0.0.1:17871"
	auth := map[string]string{"Authorization": "Bearer " + registryTestToken}
	if response := serve(registry, http.MethodGet, mcpserver.CatalogPath, "", host, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("catalog without token status = %d", response.Code)
	}
	response := serve(registry, http.MethodGet, mcpserver.CatalogPath, "", host, auth)
	if response.Code != http.StatusOK {
		t.Fatalf("catalog with token status = %d body = %s", response.Code, response.Body.String())
	}
	var catalog map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog["schemaVersion"] != float64(1) || catalog["engine"] != "in-process" {
		t.Fatalf("catalog body = %#v", catalog)
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
	headers := map[string]string{
		"Authorization": "Bearer " + registryTestToken,
		"Content-Type":  "application/json", "Accept": "application/json, text/event-stream",
	}
	if response := serve(registry, http.MethodPost, "/mcp/browser", body, host, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("browser without token status = %d", response.Code)
	}
	response = serve(registry, http.MethodPost, "/mcp/browser", body, host, headers)
	if response.Code != http.StatusOK {
		t.Fatalf("browser initialize status = %d body = %s", response.Code, response.Body.String())
	}
	var initialized map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &initialized); err != nil || initialized["result"] == nil {
		t.Fatalf("browser initialize = %#v err = %v", initialized, err)
	}
}

func TestRegistryDisableStopsModuleAndPersists(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	agentBrowser, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	newRegistry := func() *mcpserver.Registry {
		registry, err := mcpserver.NewRegistry(mcpserver.Config{
			Host: "127.0.0.1", Port: 17872, DataDir: dataDir,
			Binaries: testBinaries(t, root, agentBrowser, configPath),
		}, diagnostics.NormalizeBuild("test", "test"), slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		return registry
	}
	registry := newRegistry()
	if err := registry.SetEnabled("browser", false); err != nil {
		t.Fatal(err)
	}
	catalog := registry.Catalog()
	if catalog.Modules[0].Enabled || catalog.Modules[0].State != "unavailable" || catalog.Gateway.State != "degraded" {
		t.Fatalf("disabled catalog = %#v", catalog)
	}
	if ready, _ := registry.Health("browser"); ready {
		t.Fatal("disabled module reports ready")
	}
	if response := serve(registry, http.MethodPost, "/mcp/browser", "{}", "127.0.0.1:17872", nil); response.Code != http.StatusNotFound {
		t.Fatalf("disabled browser status = %d", response.Code)
	}
	registry.Shutdown()
	// Persisted disablement wins over the enabled-by-default environment on
	// the next start.
	reloaded := newRegistry()
	defer reloaded.Shutdown()
	if reloaded.Catalog().Modules[0].Enabled {
		t.Fatal("disablement did not survive restart")
	}
	if err := reloaded.SetEnabled("browser", true); err != nil {
		t.Fatal(err)
	}
	if !reloaded.Catalog().Modules[0].Enabled {
		t.Fatal("re-enable did not apply")
	}
	if err := reloaded.SetEnabled("signet", true); err == nil {
		t.Fatal("unknown module was accepted")
	}
}

func TestRegistryHonorsDeprecatedEnvironmentFallback(t *testing.T) {
	registry := testRegistry(t, func(config *mcpserver.Config) {
		config.Getenv = func(key string) (string, bool) {
			if key == "PIXIE_MCP_DISABLED_MODULES" {
				return "browser", true
			}
			return "", false
		}
	})
	if registry.Catalog().Modules[0].Enabled {
		t.Fatal("deprecated PIXIE_MCP_DISABLED_MODULES fallback was ignored")
	}
}
