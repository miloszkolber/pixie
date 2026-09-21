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

	"github.com/miloszkolber/pixie/internal/canvas"
	"github.com/miloszkolber/pixie/internal/design"
	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/mcpserver"
	"github.com/miloszkolber/pixie/tests/internal/designfixture"
)

const registryTestToken = "mcp-registry-test-token-0123456789abcdef0123456789"

func testRegistry(t *testing.T, mutate func(*mcpserver.Config)) *mcpserver.Registry {
	t.Helper()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	canvasConfig := canvas.DefaultConfig(dataDir)
	canvasConfig.WorkerLauncher = canvas.NewDeterministicWorkerLauncher()
	config := mcpserver.Config{
		Host: "127.0.0.1", Port: 17871, DataDir: dataDir,
		CanvasConfig: &canvasConfig,
		DesignConfig: &design.Config{DataDir: dataDir, Parser: designfixture.NewParser()},
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

func enableDesign(t *testing.T, registry *mcpserver.Registry) {
	t.Helper()
	if err := registry.SetEnabled("design", true); err != nil {
		t.Fatalf("enable design: %v", err)
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

func TestRegistryDefaultCatalogAndGateway(t *testing.T) {
	registry := testRegistry(t, nil)
	catalog := registry.Catalog()
	if catalog.SchemaVersion != 1 || catalog.Engine != "in-process" {
		t.Fatalf("default catalog = %#v", catalog)
	}
	if catalog.Gateway.State != "ready" {
		t.Fatalf("disabled optional modules degraded the gateway: %#v", catalog.Gateway)
	}
	if len(catalog.Modules) != 2 {
		t.Fatalf("default modules = %#v", catalog.Modules)
	}
	canvasModule := moduleByID(t, catalog, "canvas")
	if canvasModule.ExtensionName != "pixie-canvas" || canvasModule.Path != "/mcp/canvas" ||
		canvasModule.Transport != "streamable_http" || canvasModule.Enabled || canvasModule.State != "unavailable" {
		t.Fatalf("default canvas module = %#v", canvasModule)
	}
	if canvasModule.Endpoint != "http://127.0.0.1:17871/mcp/canvas" {
		t.Fatalf("default canvas endpoint = %q", canvasModule.Endpoint)
	}
	if ready, _ := registry.Health("canvas"); ready {
		t.Fatal("canvas module is ready while disabled")
	}
	// Enabling Design reconstructs and reports a ready module; the gateway
	// stays ready because the other optional module is merely disabled.
	enableDesign(t, registry)
	if module := moduleByID(t, registry.Catalog(), "design"); !module.Enabled || module.State != "ready" {
		t.Fatalf("enabled design = %#v", module)
	}
	if ready, detail := registry.Health("design"); !ready {
		t.Fatalf("design module is not ready with a fixture parser: %s", detail)
	}
	if gateway := registry.Catalog().Gateway; gateway.State != "ready" {
		t.Fatalf("gateway after enable = %#v", gateway)
	}
	// A repeated disable still leaves the module disabled.
	if err := registry.SetEnabled("design", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if module := moduleByID(t, registry.Catalog(), "design"); module.Enabled {
		t.Fatal("disable left the module enabled")
	}
}

func TestRegistryUnchangedEnableKeepsDesignReady(t *testing.T) {
	registry := testRegistry(t, nil)
	enableDesign(t, registry)
	if response := serve(registry, http.MethodGet, "/api/design/status", "", "127.0.0.1:17871", nil); response.Code != http.StatusOK {
		t.Fatalf("design status before unchanged enable = %d body = %s", response.Code, response.Body.String())
	}
	if err := registry.SetEnabled("design", true); err != nil {
		t.Fatal(err)
	}
	if ready, detail := registry.Health("design"); !ready {
		t.Fatalf("unchanged enable made design unavailable: %s", detail)
	}
	if response := serve(registry, http.MethodGet, "/api/design/status", "", "127.0.0.1:17871", nil); response.Code != http.StatusOK {
		t.Fatalf("design status after unchanged enable = %d body = %s", response.Code, response.Body.String())
	}
}

func TestRegistryExplicitRestartRecoversDesign(t *testing.T) {
	registry := testRegistry(t, nil)
	enableDesign(t, registry)
	if err := registry.Restart("design"); err != nil {
		t.Fatal(err)
	}
	if ready, detail := registry.Health("design"); !ready {
		t.Fatalf("restarted design is not ready: %s", detail)
	}
	if response := serve(registry, http.MethodGet, "/api/design/status", "", "127.0.0.1:17871", nil); response.Code != http.StatusOK {
		t.Fatalf("restarted design status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestRegistryPrePublicationPersistenceFailurePreservesStateAndRuntime(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dataDir, "mcp-modules.json")
	original := []byte("{\"modules\":{\"design\":{\"enabled\":true}}}\n")
	if err := os.WriteFile(statePath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	registry := testRegistry(t, func(config *mcpserver.Config) { config.DataDir = dataDir })
	before := registry.Catalog()
	if err := os.Mkdir(statePath+".bak", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetEnabled("design", false); err == nil {
		t.Fatal("persistence failure was not reported")
	}
	after := registry.Catalog()
	if len(after.Modules) != 2 || moduleByID(t, after, "design").Enabled == false || after.Revision != before.Revision {
		t.Fatalf("pre-publication failure changed catalog: before=%#v after=%#v", before, after)
	}
	if ready, detail := registry.Health("design"); !ready {
		t.Fatalf("pre-publication failure changed runtime readiness: %s", detail)
	}
	if current, err := os.ReadFile(statePath); err != nil || string(current) != string(original) {
		t.Fatalf("pre-publication failure changed primary: %q, %v", current, err)
	}
	if response := serve(registry, http.MethodGet, "/mcp/design/guide", "", "127.0.0.1:17873", nil); response.Code != http.StatusOK {
		t.Fatalf("pre-publication failure changed design route status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestRegistryRoutesDesignModuleWithToken(t *testing.T) {
	registry := testRegistry(t, func(config *mcpserver.Config) { config.Token = registryTestToken })
	enableDesign(t, registry)
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
	if response := serve(registry, http.MethodPost, "/mcp/design", body, host, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("design without token status = %d", response.Code)
	}
	response = serve(registry, http.MethodPost, "/mcp/design", body, host, headers)
	if response.Code != http.StatusOK {
		t.Fatalf("design initialize status = %d body = %s", response.Code, response.Body.String())
	}
	var initialized map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &initialized); err != nil || initialized["result"] == nil {
		t.Fatalf("design initialize = %#v err = %v", initialized, err)
	}
}

func TestRegistryDisableStopsModuleAndPersists(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	newRegistry := func() *mcpserver.Registry {
		registry := testRegistry(t, func(config *mcpserver.Config) { config.DataDir = dataDir })
		return registry
	}
	registry := newRegistry()
	enableDesign(t, registry)
	if err := registry.SetEnabled("design", false); err != nil {
		t.Fatal(err)
	}
	catalog := registry.Catalog()
	designModule := moduleByID(t, catalog, "design")
	if designModule.Enabled || designModule.State != "unavailable" || catalog.Gateway.State != "ready" {
		t.Fatalf("disabled catalog = %#v", catalog)
	}
	if ready, _ := registry.Health("design"); ready {
		t.Fatal("disabled module reports ready")
	}
	if response := serve(registry, http.MethodPost, "/mcp/design", "{}", "127.0.0.1:17872", nil); response.Code != http.StatusNotFound {
		t.Fatalf("disabled design status = %d", response.Code)
	}
	registry.Shutdown()
	// Persisted disablement survives the next start.
	reloaded := newRegistry()
	defer reloaded.Shutdown()
	if moduleByID(t, reloaded.Catalog(), "design").Enabled {
		t.Fatal("disablement did not survive restart")
	}
	if err := reloaded.SetEnabled("design", true); err != nil {
		t.Fatal(err)
	}
	if !moduleByID(t, reloaded.Catalog(), "design").Enabled {
		t.Fatal("re-enable did not apply")
	}
	if err := reloaded.SetEnabled("signet", true); err == nil {
		t.Fatal("unknown module was accepted")
	}
}

func TestRegistryIgnoresRetiredEnvironmentSelection(t *testing.T) {
	// Persisted enablement is the owner: the retired env variable must not
	// override durable desired state.
	dataDir := t.TempDir()
	statePath := filepath.Join(dataDir, "mcp-modules.json")
	if err := os.WriteFile(statePath, []byte("{\"modules\":{\"design\":{\"enabled\":true}}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := testRegistry(t, func(config *mcpserver.Config) {
		config.DataDir = dataDir
		config.Getenv = func(key string) (string, bool) {
			if key == "PIXIE_MCP_DISABLED_MODULES" {
				return "design", true
			}
			return "", false
		}
	})
	if !registry.DesiredEnabled("design") {
		t.Fatal("retired PIXIE_MCP_DISABLED_MODULES fallback disabled the module")
	}
	if ready, detail := registry.Health("design"); !ready {
		t.Fatalf("persisted enablement did not start design: %s", detail)
	}
}

func TestRegistryUnknownMCPRoutesReturnNotFound(t *testing.T) {
	registry := testRegistry(t, nil)
	enableDesign(t, registry)
	host := "127.0.0.1:17871"
	// Control: the owned Design guide route delegates to the module service.
	if response := serve(registry, http.MethodGet, "/mcp/design/guide", "", host, nil); response.Code != http.StatusOK {
		t.Fatalf("owned design route status = %d body = %s", response.Code, response.Body.String())
	}
	// Unknown and overlapping /mcp/* paths stay registry-owned not_found JSON.
	// They must not delegate to a module service (which uses an outcome
	// envelope) and must never return the SPA document. Assembled-handler SPA
	// fallback coverage stays with the controller owner.
	unknownRoutes := []string{
		"/mcp/unknown",
		"/mcp/design-evil",
		"/mcp/designfoo",
		"/mcp/designfoo/bar",
		"/mcp/DESIGN",
		"/mcp/",
		"/mcp",
		"/api/mcp/unknown",
		"/api/mcp/modules/extra",
		"/api/unknown",
	}
	for _, path := range unknownRoutes {
		response := serve(registry, http.MethodGet, path, "", host, nil)
		if response.Code != http.StatusNotFound {
			t.Fatalf("unknown route %q status = %d body = %s", path, response.Code, response.Body.String())
		}
		if contentType := response.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
			t.Fatalf("unknown route %q content type = %q", path, contentType)
		}
		if cache := response.Header().Get("Cache-Control"); cache != "no-store" {
			t.Fatalf("unknown route %q cache policy = %q", path, cache)
		}
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("unknown route %q body is not JSON: %v (%q)", path, err, response.Body.String())
		}
		if body["code"] != "not_found" {
			t.Fatalf("unknown route %q body = %#v", path, body)
		}
		if _, delegated := body["outcome"]; delegated {
			t.Fatalf("unknown route %q delegated to a module service: %#v", path, body)
		}
		if raw := response.Body.String(); strings.Contains(raw, "<main>") || strings.Contains(strings.ToLower(raw), "<!doctype") {
			t.Fatalf("unknown route %q returned SPA document: %q", path, raw)
		}
	}
	if response := serve(registry, http.MethodPost, "/mcp/unknown", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`, host, map[string]string{"Content-Type": "application/json"}); response.Code != http.StatusNotFound {
		t.Fatalf("unknown POST route status = %d body = %s", response.Code, response.Body.String())
	}
	// Ownership errors are not auth errors at registry level: a token-guarded
	// registry still reports unknown /mcp/* as not_found without credentials,
	// while metadata routes keep their bearer boundary.
	tokenRegistry := testRegistry(t, func(config *mcpserver.Config) { config.Token = registryTestToken })
	enableDesign(t, tokenRegistry)
	for _, path := range []string{"/mcp/unknown", "/mcp/design-evil"} {
		response := serve(tokenRegistry, http.MethodGet, path, "", host, nil)
		if response.Code != http.StatusNotFound {
			t.Fatalf("token-guarded unknown route %q status = %d body = %s", path, response.Code, response.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["code"] != "not_found" {
			t.Fatalf("token-guarded unknown route %q body = %q err = %v", path, response.Body.String(), err)
		}
	}
	authorized := map[string]string{"Authorization": "Bearer " + registryTestToken}
	if response := serve(tokenRegistry, http.MethodGet, "/api/mcp/modules/extra", "", host, authorized); response.Code != http.StatusNotFound {
		t.Fatalf("metadata overlap status = %d body = %s", response.Code, response.Body.String())
	}
}
