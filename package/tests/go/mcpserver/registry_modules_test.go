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
)

func configuredModuleRegistry(t *testing.T) *mcpserver.Registry {
	t.Helper()
	root := t.TempDir()
	configPath := filepath.Join(root, "browser.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	agentBrowser, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := mcpserver.NewRegistry(mcpserver.Config{
		Host: "127.0.0.1", Port: 17875, DataDir: dataDir,
		Binaries: &mcpserver.BinaryConfig{AgentBrowser: agentBrowser, BrowserConfig: configPath},
		CanvasConfig: func() *canvas.Config {
			config := canvas.DefaultConfig(dataDir)
			config.WorkerLauncher = canvas.NewDeterministicWorkerLauncher()
			return &config
		}(),
		DesignConfig: &design.Config{DataDir: dataDir, Parser: design.NewDeterministicParser()},
	}, diagnostics.NormalizeBuild("test", "test"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Shutdown)
	return registry
}

func moduleByID(t *testing.T, catalog mcpserver.Catalog, id string) mcpserver.Module {
	t.Helper()
	for _, module := range catalog.Modules {
		if module.ID == id {
			return module
		}
	}
	t.Fatalf("catalog is missing module %q: %#v", id, catalog.Modules)
	return mcpserver.Module{}
}

func TestRegistryComposesOptionalModulesWithIndependentDefaultsAndRoutes(t *testing.T) {
	registry := configuredModuleRegistry(t)
	catalog := registry.Catalog()
	if len(catalog.Modules) != 3 {
		t.Fatalf("registered module count = %d, want 3: %#v", len(catalog.Modules), catalog.Modules)
	}
	if catalog.Gateway.State != "ready" {
		t.Fatalf("disabled optional modules degraded gateway: %#v", catalog.Gateway)
	}
	browser := moduleByID(t, catalog, "browser")
	canvasModule := moduleByID(t, catalog, "canvas")
	designModule := moduleByID(t, catalog, "design")
	if !browser.Enabled || browser.State != "ready" {
		t.Fatalf("browser default = %#v", browser)
	}
	if canvasModule.Enabled || canvasModule.State != "unavailable" || designModule.Enabled || designModule.State != "unavailable" {
		t.Fatalf("optional defaults = %#v %#v", canvasModule, designModule)
	}
	if response := serveRegistry(t, registry, http.MethodGet, "/mcp/canvas/guide"); response.Code != http.StatusNotFound {
		t.Fatalf("disabled Canvas route status = %d body=%s", response.Code, response.Body.String())
	}
	if err := registry.SetEnabled("canvas", true); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetEnabled("design", true); err != nil {
		t.Fatal(err)
	}
	if ready, detail := registry.Health("canvas"); !ready {
		t.Fatalf("enabled Canvas health = %v (%s)", ready, detail)
	}
	if ready, detail := registry.Health("design"); !ready {
		t.Fatalf("enabled Design health = %v (%s)", ready, detail)
	}
	if response := serveRegistry(t, registry, http.MethodGet, "/mcp/canvas/guide"); response.Code != http.StatusOK {
		t.Fatalf("enabled Canvas guide status = %d body=%s", response.Code, response.Body.String())
	}
	if response := serveRegistry(t, registry, http.MethodGet, "/api/design/status"); response.Code != http.StatusOK {
		t.Fatalf("enabled Design status = %d body=%s", response.Code, response.Body.String())
	}
	for _, route := range []string{"/mcp/canvas-evil", "/mcp/design-evil", "/api/canvas-evil"} {
		if response := serveRegistry(t, registry, http.MethodGet, route); response.Code != http.StatusNotFound {
			t.Fatalf("overlapping route %q status = %d body=%s", route, response.Code, response.Body.String())
		}
	}
}

func TestRegistryCanvasAuthorityRevokesWithSessionLifecycle(t *testing.T) {
	registry := configuredModuleRegistry(t)
	if err := registry.SetEnabled("canvas", true); err != nil {
		t.Fatal(err)
	}
	authority, err := registry.AttachCanvas("native-session", 4)
	if err != nil || authority.Token == "" {
		t.Fatalf("canvas attach = %#v, err=%v", authority, err)
	}
	if err := registry.RevokeSession("native-session"); err == 0 {
		t.Fatal("session revocation did not revoke Canvas authority")
	}
	if ready, detail := registry.Health("canvas"); !ready {
		t.Fatalf("session cleanup changed module readiness: %s", detail)
	}
}

func TestRegistryKeepsUnavailableCanvasMCPClosedWhileHealthRemainsReachable(t *testing.T) {
	root := t.TempDir()
	registry, err := mcpserver.NewRegistry(mcpserver.Config{
		Host: "127.0.0.1", Port: 17876, DataDir: root,
	}, diagnostics.NormalizeBuild("test", "test"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Shutdown)
	if err := registry.SetEnabled("canvas", true); err != nil {
		t.Fatal(err)
	}
	if response := serveRegistry(t, registry, http.MethodGet, "/mcp/canvas"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable Canvas MCP status = %d body=%s", response.Code, response.Body.String())
	}
	if response := serveRegistry(t, registry, http.MethodGet, "/mcp/canvas/guide"); response.Code != http.StatusOK {
		t.Fatalf("unavailable Canvas guide status = %d body=%s", response.Code, response.Body.String())
	}
	if response := serveRegistry(t, registry, http.MethodGet, "/api/canvas/status"); response.Code != http.StatusOK {
		t.Fatalf("unavailable Canvas management status = %d body=%s", response.Code, response.Body.String())
	}
	if _, err := registry.AttachCanvas("native-session", 1); err == nil {
		t.Fatal("Canvas authority was issued without a configured worker")
	}
}

func serveRegistry(t *testing.T, registry *mcpserver.Registry, method, route string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "http://127.0.0.1:17875"+route, nil)
	response := httptest.NewRecorder()
	registry.ServeHTTP(response, request)
	return response
}

func assertRegistryJSONNotSPA(t *testing.T, response *httptest.ResponseRecorder, target string) {
	t.Helper()
	if response.Code < http.StatusBadRequest && response.Code != http.StatusOK {
		t.Fatalf("route %q returned unexpected success-transport status %d", target, response.Code)
	}
	body := response.Body.String()
	if strings.Contains(body, "<main>application</main>") || strings.Contains(strings.ToLower(body), "<!doctype html") {
		t.Fatalf("route %q returned the SPA document: %q", target, body)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") && !strings.HasPrefix(contentType, "text/markdown") && !strings.HasPrefix(contentType, "image/png") {
		t.Fatalf("route %q content type = %q body=%q", target, contentType, body)
	}
	if cache := response.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("route %q cache policy = %q", target, cache)
	}
}

func TestRegistryKeepsUnavailableDesignMCPClosedWhileGuideAndStatusStayReachable(t *testing.T) {
	root := t.TempDir()
	registry, err := mcpserver.NewRegistry(mcpserver.Config{
		Host: "127.0.0.1", Port: 17877, DataDir: root,
	}, diagnostics.NormalizeBuild("test", "test"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Shutdown)
	if err := registry.SetEnabled("design", true); err != nil {
		t.Fatal(err)
	}
	// Parser-missing readiness is the Design counterpart to the Canvas
	// worker-missing case: model-facing MCP fails closed while guide and
	// retained status stay reachable without a parser.
	if response := serveRegistry(t, registry, http.MethodPost, "/mcp/design"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable Design MCP status = %d body=%s", response.Code, response.Body.String())
	}
	if response := serveRegistry(t, registry, http.MethodGet, "/mcp/design/guide"); response.Code != http.StatusOK {
		t.Fatalf("unavailable Design guide status = %d body=%s", response.Code, response.Body.String())
	}
	if response := serveRegistry(t, registry, http.MethodGet, "/api/design/status"); response.Code != http.StatusOK {
		t.Fatalf("unavailable Design management status = %d body=%s", response.Code, response.Body.String())
	}
	snapshot := registry.ModuleLifecycle("design")
	if !snapshot.Desired || snapshot.Ready {
		t.Fatalf("parser-missing lifecycle must keep desired with readiness failed: %+v", snapshot)
	}
	if ready, _ := registry.Health("design"); ready {
		t.Fatal("parser-missing Design reports ready")
	}
}

func TestValidateModuleDefinitionsRejectsDuplicateOverlapAndCoreTakeover(t *testing.T) {
	if err := mcpserver.ValidateModuleDefinitions(mcpserver.DefaultModuleDefinitions()); err != nil {
		t.Fatalf("compiled defaults rejected: %v", err)
	}
	fixture := mcpserver.FixtureModuleDefinition()
	combined := append(append([]mcpserver.ModuleDefinition(nil), mcpserver.DefaultModuleDefinitions()...), fixture)
	if err := mcpserver.ValidateModuleDefinitions(combined); err != nil {
		t.Fatalf("defaults plus generic fixture rejected: %v", err)
	}
	// Similarly named routes are distinct ownership, not overlap.
	evilCandidate := append(append([]mcpserver.ModuleDefinition(nil), mcpserver.DefaultModuleDefinitions()...), mcpserver.ModuleDefinition{
		ID: "evil", ExtensionName: "pixie-evil", DisplayName: "Evil", Description: "Evil Joins.",
		Path: "/mcp/canvas-evil", Transport: "streamable_http", ManagementPaths: []string{"/api/evil"},
		Frontend: mcpserver.FrontendDescriptor{ModuleID: "evil", Version: "1.0.0", Label: "Evil", Icon: "evil", Sidebar: true, Viewer: true, ContextScope: "instance", ResourceNamespace: "evil"},
	})
	if err := mcpserver.ValidateModuleDefinitions(evilCandidate); err != nil {
		t.Fatalf("evil-suffix distinct route rejected as overlap: %v", err)
	}
	takeovers := []struct {
		name       string
		path       string
		management []string
	}{
		{name: "ws", path: "/ws", management: []string{"/api/takeover-ws"}},
		{name: "auth", path: "/auth", management: []string{"/api/takeover-auth"}},
		{name: "auth-login", path: "/mcp/ok-takeover-auth-login", management: []string{"/api/takeover2"}},
		{name: "objective", path: "/mcp/objective", management: []string{"/api/takeover-objective"}},
		{name: "catalog", path: "/mcp/ok-takeover-catalog", management: []string{"/api/mcp/modules"}},
		{name: "duplicate-mcp", path: "/mcp/canvas", management: []string{"/api/takeover-dup"}},
		{name: "overlap-child", path: "/mcp/canvas/sub", management: []string{"/api/takeover-overlap"}},
		{name: "overlap-parent", path: "/mcp", management: []string{"/api/takeover-parent"}},
		{name: "duplicate-management", path: "/mcp/takeover-dup-mgmt", management: []string{"/api/design"}},
		{name: "overlap-management", path: "/mcp/takeover-overlap-mgmt", management: []string{"/api/design/sub"}},
	}
	for _, item := range takeovers {
		definition := mcpserver.ModuleDefinition{
			ID: "takeover-" + item.name, ExtensionName: "pixie-takeover", DisplayName: "Takeover",
			Description: "Core takeover probe.", Path: item.path, Transport: "streamable_http",
			ManagementPaths: item.management,
			Frontend:        mcpserver.FrontendDescriptor{ModuleID: "takeover-" + item.name, Version: "1.0.0", Label: "Takeover", Icon: "takeover", Sidebar: true, Viewer: true, ContextScope: "instance", ResourceNamespace: "takeover-" + item.name},
		}
		// The auth-login case claims /auth/login via management; rewrite the
		// management prefix for that row so the MCP path stays valid while the
		// management prefix performs the takeover.
		if item.name == "auth-login" {
			definition.ManagementPaths = []string{"/auth/login"}
			definition.Path = "/mcp/takeover-auth-login"
		}
		candidate := append(append([]mcpserver.ModuleDefinition(nil), mcpserver.DefaultModuleDefinitions()...), definition)
		if err := mcpserver.ValidateModuleDefinitions(candidate); err == nil {
			t.Fatalf("takeover %q (%q/%q) was accepted", item.name, item.path, item.management)
		}
	}
	// Duplicate IDs never validate, even with distinct routes.
	duplicated := append(append([]mcpserver.ModuleDefinition(nil), mcpserver.DefaultModuleDefinitions()...), mcpserver.ModuleDefinition{
		ID: "canvas", ExtensionName: "pixie-dupe", DisplayName: "Dupe", Description: "Dupe.",
		Path: "/mcp/dupe", Transport: "streamable_http", ManagementPaths: []string{"/api/dupe"},
		Frontend: mcpserver.FrontendDescriptor{ModuleID: "canvas", Version: "1.0.0", Label: "Dupe", Icon: "dupe", Sidebar: true, Viewer: true, ContextScope: "instance", ResourceNamespace: "canvas"},
	})
	if err := mcpserver.ValidateModuleDefinitions(duplicated); err == nil {
		t.Fatal("duplicate module id was accepted")
	}
	// Core routes are never reported as registered.
	for _, route := range []string{"/ws", "/auth", "/auth/login", "/auth/status", "/mcp/objective", "/mcp/objective/sub"} {
		if mcpserver.IsRegisteredRoute(route) {
			t.Fatalf("core route %q reported as registered", route)
		}
	}
}

func fixtureStubHandler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		switch {
		case request.URL.Path == "/mcp/fixture" && request.Method == http.MethodPost:
			response.Header().Set("Content-Type", "application/json; charset=utf-8")
			response.WriteHeader(http.StatusOK)
			_, _ = response.Write([]byte(`{"code":"ok","surface":"mcp"}`))
		case request.URL.Path == "/mcp/fixture/guide" && request.Method == http.MethodGet:
			response.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			response.WriteHeader(http.StatusOK)
			_, _ = response.Write([]byte("# Fixture guide\n"))
		case request.URL.Path == "/api/fixture/status" && request.Method == http.MethodGet:
			response.Header().Set("Content-Type", "application/json; charset=utf-8")
			response.WriteHeader(http.StatusOK)
			_, _ = response.Write([]byte(`{"code":"ok","surface":"management"}`))
		case (request.URL.Path == "/api/fixture/document" || request.URL.Path == "/api/fixture/status/remove") && request.Method == http.MethodDelete:
			response.Header().Set("Content-Type", "application/json; charset=utf-8")
			response.WriteHeader(http.StatusOK)
			_, _ = response.Write([]byte(`{"code":"ok","surface":"removal"}`))
		case strings.HasPrefix(request.URL.Path, "/api/fixture/artifacts/") && request.Method == http.MethodGet:
			rest := strings.TrimPrefix(request.URL.Path, "/api/fixture/artifacts/")
			parts := strings.Split(rest, "/")
			if len(parts) == 2 && parts[0] != "" && parts[1] == "cover.png" {
				response.Header().Set("Content-Type", "image/png")
				response.WriteHeader(http.StatusOK)
				_, _ = response.Write([]byte{137, 80, 78, 71, 13, 10, 26, 10})
				return
			}
			response.Header().Set("Content-Type", "application/json; charset=utf-8")
			response.WriteHeader(http.StatusNotFound)
			_, _ = response.Write([]byte(`{"code":"not_found"}`))
		case request.URL.Path == "/mcp/fixture" || request.URL.Path == "/mcp/fixture/guide" || request.URL.Path == "/api/fixture/status" || strings.HasPrefix(request.URL.Path, "/api/fixture/artifacts/"):
			response.Header().Set("Content-Type", "application/json; charset=utf-8")
			response.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = response.Write([]byte(`{"code":"method_not_allowed"}`))
		default:
			response.Header().Set("Content-Type", "application/json; charset=utf-8")
			response.WriteHeader(http.StatusNotFound)
			_, _ = response.Write([]byte(`{"code":"not_found"}`))
		}
	})
}

func TestRegistryFixtureModuleUsesGenericDefinitionDrivenRouting(t *testing.T) {
	root := t.TempDir()
	registry, err := mcpserver.NewRegistry(mcpserver.Config{
		Host: "127.0.0.1", Port: 17878, DataDir: root,
	}, diagnostics.NormalizeBuild("test", "test"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Shutdown)
	ready := true
	definition := mcpserver.FixtureModuleDefinition()
	if err := registry.RegisterTestModule(definition, fixtureStubHandler(), func() bool { return ready }, func() string { return "fixture unavailable" }); err != nil {
		t.Fatal(err)
	}
	// Duplicate and overlapping registrations conflict through the same gate.
	if err := registry.RegisterTestModule(definition, fixtureStubHandler(), nil, nil); err == nil {
		t.Fatal("duplicate fixture registration was accepted")
	}
	overlapping := mcpserver.ModuleDefinition{
		ID: "overlap", ExtensionName: "pixie-overlap", DisplayName: "Overlap", Description: "Overlap.",
		Path: "/mcp/fixture/sub", Transport: "streamable_http", ManagementPaths: []string{"/api/overlap"},
		Frontend: mcpserver.FrontendDescriptor{ModuleID: "overlap", Version: "1.0.0", Label: "Overlap", Icon: "overlap", Sidebar: true, Viewer: true, ContextScope: "instance", ResourceNamespace: "overlap"},
	}
	if err := registry.RegisterTestModule(overlapping, fixtureStubHandler(), nil, nil); err == nil {
		t.Fatal("overlapping fixture registration was accepted")
	}
	takeover := mcpserver.ModuleDefinition{
		ID: "takeover-ws", ExtensionName: "pixie-takeover", DisplayName: "Takeover", Description: "Takeover.",
		Path: "/ws", Transport: "streamable_http", ManagementPaths: []string{"/api/takeover-ws2"},
		Frontend: mcpserver.FrontendDescriptor{ModuleID: "takeover-ws", Version: "1.0.0", Label: "Takeover", Icon: "takeover", Sidebar: true, Viewer: true, ContextScope: "instance", ResourceNamespace: "takeover-ws"},
	}
	if err := registry.RegisterTestModule(takeover, fixtureStubHandler(), nil, nil); err == nil {
		t.Fatal("core-takeover fixture registration was accepted")
	}
	if err := registry.SetEnabled("fixture", true); err != nil {
		t.Fatal(err)
	}
	// Generic MCP, guide, management and artifact surfaces all resolve through
	// the shared definition-driven router with no module-name switch.
	for _, item := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/mcp/fixture", http.StatusOK},
		{http.MethodGet, "/mcp/fixture/guide", http.StatusOK},
		{http.MethodGet, "/api/fixture/status", http.StatusOK},
	} {
		response := serveRegistry(t, registry, item.method, item.path)
		if response.Code != item.want {
			t.Fatalf("fixture %s %s = %d, want %d body=%s", item.method, item.path, response.Code, item.want, response.Body.String())
		}
		assertRegistryJSONNotSPA(t, response, item.path)
	}
	artifact := serveRegistry(t, registry, http.MethodGet, "/api/fixture/artifacts/doc-1/cover.png")
	if artifact.Code != http.StatusOK || artifact.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("fixture artifact = %d %q body=%q", artifact.Code, artifact.Header().Get("Content-Type"), artifact.Body.String())
	}
	// Readiness gates only model-facing MCP: guide and management stay
	// reachable while MCP fails closed with 503 JSON.
	ready = false
	if response := serveRegistry(t, registry, http.MethodPost, "/mcp/fixture"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready fixture MCP = %d body=%s", response.Code, response.Body.String())
	}
	if response := serveRegistry(t, registry, http.MethodGet, "/mcp/fixture/guide"); response.Code != http.StatusOK {
		t.Fatalf("not-ready fixture guide = %d body=%s", response.Code, response.Body.String())
	}
	if response := serveRegistry(t, registry, http.MethodGet, "/api/fixture/status"); response.Code != http.StatusOK {
		t.Fatalf("not-ready fixture management = %d body=%s", response.Code, response.Body.String())
	}
	ready = true
	// Disablement gates MCP with 404 while retained management stays reachable.
	if err := registry.SetEnabled("fixture", false); err != nil {
		t.Fatal(err)
	}
	if response := serveRegistry(t, registry, http.MethodPost, "/mcp/fixture"); response.Code != http.StatusNotFound {
		t.Fatalf("disabled fixture MCP = %d body=%s", response.Code, response.Body.String())
	}
	if response := serveRegistry(t, registry, http.MethodGet, "/api/fixture/status"); response.Code != http.StatusOK {
		t.Fatalf("disabled fixture management = %d body=%s", response.Code, response.Body.String())
	}
	// Overlap and unknown fixture-adjacent paths stay registry-owned 404 JSON.
	for _, route := range []string{"/mcp/fixture-evil", "/api/fixture-evil", "/mcp/fixture/unknown", "/api/fixture/unknown"} {
		response := serveRegistry(t, registry, http.MethodGet, route)
		if response.Code != http.StatusNotFound {
			t.Fatalf("fixture-adjacent %q = %d body=%s", route, response.Code, response.Body.String())
		}
		var decoded map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || decoded["code"] != "not_found" {
			t.Fatalf("fixture-adjacent %q body = %q err=%v", route, response.Body.String(), err)
		}
		assertRegistryJSONNotSPA(t, response, route)
	}
}
