package controller_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/design"
	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/mcpserver"
	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

func assertReservedJSONNotSPA(t *testing.T, response *httptest.ResponseRecorder, target string) {
	t.Helper()
	if response.Code < http.StatusBadRequest {
		t.Fatalf("reserved route %q returned success: %d", target, response.Code)
	}
	body := response.Body.String()
	if strings.Contains(body, "<main>application</main>") || strings.Contains(strings.ToLower(body), "<!doctype html") {
		t.Fatalf("reserved route %q returned the SPA document: %q", target, body)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("reserved route %q content type = %q body=%q", target, contentType, body)
	}
	if cache := response.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("reserved route %q cache policy = %q", target, cache)
	}
	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("reserved route %q body is not JSON: %v (%q)", target, err, body)
	}
}

func TestAssembledReservedRoutesNeverServeSPAWithRealIndex(t *testing.T) {
	handler := newStaticHandler(t)
	targets := []string{
		"https://pixie.example/api/unknown",
		"https://pixie.example/api",
		"https://pixie.example/api/",
		"https://pixie.example/mcp/unknown",
		"https://pixie.example/mcp",
		"https://pixie.example/mcp/",
		"https://pixie.example/api/mcp/modules/extra",
		"https://pixie.example/api/unknown.json",
		"https://pixie.example/mcp/browser-evil",
		"https://pixie.example/mcp/browserfoo/bar",
		"https://pixie.example/api/unknown?revision=ignored",
		"https://pixie.example//api/unknown",
		"https://pixie.example//mcp/unknown",
		"https://pixie.example/./api/unknown",
		"https://pixie.example/./mcp/unknown",
		"https://pixie.example/a/../api/unknown",
		"https://pixie.example/a/../mcp/unknown",
	}
	for _, target := range targets {
		response := requestStatic(t, handler, http.MethodGet, target)
		assertReservedJSONNotSPA(t, response, target)
		if response.Code != http.StatusNotFound {
			t.Fatalf("reserved route %q status = %d, want %d", target, response.Code, http.StatusNotFound)
		}
	}
}

func fixtureRegistryWithJSONNotFound() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/mcp/fixture" || request.URL.Path == "/api/fixture" {
			writeFixtureJSON(response, http.StatusOK)
			return
		}
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		response.Header().Set("Cache-Control", "no-store")
		response.WriteHeader(http.StatusNotFound)
		_, _ = response.Write([]byte(`{"code":"not_found"}`))
	})
}

func TestAssembledReservedRoutesWithRegistryNeverServeSPA(t *testing.T) {
	handler := newStaticHandler(t)
	const token = "mcp-token-0123456789abcdef0123456789"
	handler.Auth.MCPToken = token
	handler.MCPRegistry = fixtureRegistryWithJSONNotFound()

	for _, path := range []string{"/mcp/fixture", "/api/fixture"} {
		request := httptest.NewRequest(http.MethodGet, "https://pixie.example"+path, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "<main>application</main>") {
			t.Fatalf("registered route %q = %d %q", path, response.Code, response.Body.String())
		}
	}

	unknown := []string{
		"https://pixie.example/mcp/missing",
		"https://pixie.example/api/unknown",
		"https://pixie.example/api",
		"https://pixie.example/mcp",
		"https://pixie.example/api/mcp/modules/extra",
		"https://pixie.example/mcp/browser-evil",
		"https://pixie.example//api/unknown",
		"https://pixie.example//mcp/unknown",
		"https://pixie.example/./api/unknown",
		"https://pixie.example/a/../mcp/unknown",
	}
	for _, target := range unknown {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		assertReservedJSONNotSPA(t, response, target)
	}

	withoutAuth := httptest.NewRequest(http.MethodGet, "https://pixie.example/mcp/missing", nil)
	withoutAuthResponse := httptest.NewRecorder()
	handler.ServeHTTP(withoutAuthResponse, withoutAuth)
	assertReservedJSONNotSPA(t, withoutAuthResponse, "https://pixie.example/mcp/missing without credentials")

	frontend := requestStatic(t, handler, http.MethodGet, "https://pixie.example/projects/example")
	if frontend.Code != http.StatusOK || !strings.Contains(frontend.Body.String(), "<main>application</main>") {
		t.Fatalf("frontend navigation with registry = %d %q", frontend.Code, frontend.Body.String())
	}
}

func TestAssembledOptionalModuleRoutesStayOwnedByRegistry(t *testing.T) {
	handler := newStaticHandler(t)
	handler.MCPRegistry = testInProcessRegistry(t)
	const token = "mcp-token-0123456789abcdef0123456789"
	handler.Auth.MCPToken = token

	for _, route := range []string{"/mcp/canvas", "/mcp/design"} {
		request := httptest.NewRequest(http.MethodGet, "https://pixie.example"+route, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("disabled optional MCP route %q status = %d body = %q", route, response.Code, response.Body.String())
		}
		assertReservedJSONNotSPA(t, response, route)
	}

	for _, route := range []string{"/api/canvas/status", "/api/design/status"} {
		request := httptest.NewRequest(http.MethodGet, "https://pixie.example"+route, nil)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("optional management route %q status = %d body = %q", route, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "<main>application</main>") {
			t.Fatalf("optional management route %q returned SPA: %q", route, response.Body.String())
		}
	}
}

func TestAssembledCanvasRouteLeavesScopedBearerToModule(t *testing.T) {
	handler := newStaticHandler(t)
	const publisherToken = "mcp-token-0123456789abcdef0123456789"
	handler.Auth.MCPToken = publisherToken
	var received *http.Request
	handler.MCPRegistry = http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		received = request
		response.WriteHeader(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodPost, "https://pixie.example/mcp/canvas", nil)
	request.Header.Set("Authorization", "Bearer canvas-session-capability")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("Canvas session route status = %d body = %q", response.Code, response.Body.String())
	}
	if received == nil || received.Header.Get("Authorization") != "Bearer canvas-session-capability" {
		t.Fatalf("Canvas session capability was not delegated unchanged: %#v", received)
	}

	wrongOrigin := httptest.NewRequest(http.MethodPost, "https://pixie.example/mcp/canvas", nil)
	wrongOrigin.Header.Set("Authorization", "Bearer canvas-session-capability")
	wrongOrigin.Header.Set("Origin", "https://attacker.example")
	wrongOriginResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongOriginResponse, wrongOrigin)
	if wrongOriginResponse.Code != http.StatusUnauthorized {
		t.Fatalf("Canvas wrong-origin status = %d body = %q", wrongOriginResponse.Code, wrongOriginResponse.Body.String())
	}
}

type countedRegistry struct {
	handler http.Handler
	hits    int
}

func (c *countedRegistry) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	c.hits++
	c.handler.ServeHTTP(response, request)
}

func assembledFixtureRegistry(t *testing.T, ready *bool) *mcpserver.Registry {
	t.Helper()
	root := t.TempDir()
	registry, err := mcpserver.NewRegistry(mcpserver.Config{
		Host: "127.0.0.1", Port: 17879, DataDir: filepath.Join(root, "data"),
	}, diagnostics.NormalizeBuild("test", "test"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Shutdown)
	stub := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
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
	readyFunc := func() bool { return true }
	if ready != nil {
		readyFunc = func() bool { return *ready }
	}
	if err := registry.RegisterTestModule(mcpserver.FixtureModuleDefinition(), stub, readyFunc, func() string { return "fixture unavailable" }); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetEnabled("fixture", true); err != nil {
		t.Fatal(err)
	}
	return registry
}

func doAssembled(t *testing.T, handler http.Handler, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestAssembledFixtureModuleThroughRealHandlerWithoutNameBranches(t *testing.T) {
	ready := true
	registry := assembledFixtureRegistry(t, &ready)
	handler := newStaticHandler(t)
	const token = "mcp-token-0123456789abcdef0123456789"
	handler.Auth.MCPToken = token
	handler.MCPRegistry = registry
	mcpAuth := map[string]string{"Authorization": "Bearer " + token}
	mgmtAuth := map[string]string{"Sec-Fetch-Site": "same-origin"}

	// One generic table drives MCP, management and artifact surfaces with no
	// per-module branches in test or production routing.
	for _, item := range []struct {
		name    string
		method  string
		path    string
		headers map[string]string
		want    int
	}{
		{name: "mcp", method: http.MethodPost, path: "https://pixie.example/mcp/fixture", headers: mcpAuth, want: http.StatusOK},
		{name: "guide", method: http.MethodGet, path: "https://pixie.example/mcp/fixture/guide", headers: mcpAuth, want: http.StatusOK},
		{name: "management", method: http.MethodGet, path: "https://pixie.example/api/fixture/status", headers: mgmtAuth, want: http.StatusOK},
		{name: "artifact", method: http.MethodGet, path: "https://pixie.example/api/fixture/artifacts/doc-1/cover.png", headers: mgmtAuth, want: http.StatusOK},
	} {
		response := doAssembled(t, handler, item.method, item.path, item.headers)
		if response.Code != item.want {
			t.Fatalf("fixture %s %s %s = %d, want %d body=%q", item.name, item.method, item.path, response.Code, item.want, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "<main>application</main>") {
			t.Fatalf("fixture %s returned SPA: %q", item.name, response.Body.String())
		}
	}
	// Wrong method fails closed without SPA.
	wrongMethod := doAssembled(t, handler, http.MethodPost, "https://pixie.example/api/fixture/status", mgmtAuth)
	if wrongMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("fixture wrong method = %d body=%q", wrongMethod.Code, wrongMethod.Body.String())
	}
	assertReservedJSONNotSPA(t, wrongMethod, "fixture wrong method")
	// Wrong role fails closed: management with MCP bearer but without the
	// human same-origin boundary, and MCP with human boundary but without the
	// publisher bearer.
	wrongRoleMgmt := doAssembled(t, handler, http.MethodGet, "https://pixie.example/api/fixture/status", mcpAuth)
	if wrongRoleMgmt.Code == http.StatusOK {
		t.Fatalf("fixture management accepted MCP-bearer-only role: %q", wrongRoleMgmt.Body.String())
	}
	assertReservedJSONNotSPA(t, wrongRoleMgmt, "fixture management wrong role")
	wrongRoleMCP := doAssembled(t, handler, http.MethodPost, "https://pixie.example/mcp/fixture", mgmtAuth)
	if wrongRoleMCP.Code == http.StatusOK {
		t.Fatalf("fixture MCP accepted management-only role: %q", wrongRoleMCP.Body.String())
	}
	assertReservedJSONNotSPA(t, wrongRoleMCP, "fixture MCP wrong role")
	// Unknown fixture-adjacent routes stay JSON, never SPA.
	for _, target := range []string{
		"https://pixie.example/mcp/fixture/unknown",
		"https://pixie.example/api/fixture/unknown",
		"https://pixie.example/mcp/fixture-evil",
		"https://pixie.example/api/fixture-evil",
	} {
		response := doAssembled(t, handler, http.MethodGet, target, mcpAuth)
		assertReservedJSONNotSPA(t, response, target)
		mgmtResponse := doAssembled(t, handler, http.MethodGet, target, mgmtAuth)
		assertReservedJSONNotSPA(t, mgmtResponse, target+" (mgmt role)")
	}
	// Duplicate registration conflicts through the same validation gate.
	if err := registry.RegisterTestModule(mcpserver.FixtureModuleDefinition(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), nil, nil); err == nil {
		t.Fatal("duplicate fixture registration through assembled registry was accepted")
	}
	// Secrets never travel in management URLs.
	secretURL := doAssembled(t, handler, http.MethodGet, "https://pixie.example/api/fixture/status?token=secret", mgmtAuth)
	if secretURL.Code != http.StatusBadRequest {
		t.Fatalf("fixture secret-in-URL = %d body=%q", secretURL.Code, secretURL.Body.String())
	}
	assertReservedJSONNotSPA(t, secretURL, "fixture secret-in-URL")
}

func designFixtureArchive(t *testing.T) []byte {
	t.Helper()
	var cover bytes.Buffer
	picture := image.NewRGBA(image.Rect(0, 0, 2, 2))
	picture.Set(0, 0, color.RGBA{R: 255, A: 255})
	picture.Set(1, 1, color.RGBA{G: 255, A: 255})
	if err := png.Encode(&cover, picture); err != nil {
		t.Fatal(err)
	}
	designJSON, err := json.Marshal(map[string]any{
		"name":  "Offline fixture",
		"pages": []design.Page{{ID: "page-1", Name: "Page 1"}},
		"nodes": []design.Node{
			{ID: "frame-1", PageID: "page-1", Type: "FRAME", Name: "Frame", Visible: true, Width: 120, Height: 80},
			{ID: "text-1", PageID: "page-1", ParentID: "frame-1", Type: "TEXT", Name: "Greeting", Text: "Hello Design", Visible: true, Width: 80, Height: 20},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for name, content := range map[string][]byte{"canvas.fig": []byte("fixture"), "design.json": designJSON, "thumbnail.png": cover.Bytes()} {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func newDesignAuthHandler(t *testing.T, registry *mcpserver.Registry) (*controller.HTTPHandler, *http.Cookie) {
	t.Helper()
	root := t.TempDir()
	policy, err := workspace.NewPathPolicy([]string{root}, false)
	if err != nil {
		t.Fatal(err)
	}
	store := persist.Store{Dir: t.TempDir()}
	projects := workspace.NewProjects(store, policy)
	const controllerToken = "design-controller-token-0123456789abcdef"
	const mcpToken = "design-mcp-token-0123456789abcdef0123456789"
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, projects, workspace.NewFiles(projects, policy), controller.AuthConfig{
		Enabled:         true,
		ControllerToken: controllerToken,
		MCPToken:        mcpToken,
		ControllerHost:  "127.0.0.1",
		ControllerPort:  17880,
		PublicOrigin:    "http://127.0.0.1:17880",
	}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	handler.MCPRegistry = registry
	login := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:17880/auth/login", strings.NewReader(`{"token":"`+controllerToken+`"}`))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Sec-Fetch-Site", "same-origin")
	login.Header.Set("Origin", "http://127.0.0.1:17880")
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK || len(loginResponse.Result().Cookies()) != 1 {
		t.Fatalf("design controller login = %d %q", loginResponse.Code, loginResponse.Body.String())
	}
	return handler, loginResponse.Result().Cookies()[0]
}

func doDesignManagement(t *testing.T, handler http.Handler, method, target string, cookie *http.Cookie, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request := httptest.NewRequest(method, target, reader)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	if body != nil {
		request.Header.Set("Content-Type", "application/octet-stream")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestAssembledDesignManagementAuthorityMatrix(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	registry, err := mcpserver.NewRegistry(mcpserver.Config{
		Host: "127.0.0.1", Port: 17880, DataDir: dataDir,
		DesignConfig: &design.Config{DataDir: dataDir, Parser: design.NewDeterministicParser()},
	}, diagnostics.NormalizeBuild("test", "test"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Shutdown)
	if err := registry.SetEnabled("design", true); err != nil {
		t.Fatal(err)
	}
	handler, cookie := newDesignAuthHandler(t, registry)

	// Human cookie reaches instance-wide status; the design document ID in the
	// query is a reference, not permission: a forged ID fails closed.
	status := doDesignManagement(t, handler, http.MethodGet, "http://127.0.0.1:17880/api/design/status", cookie, nil)
	if status.Code != http.StatusOK || strings.Contains(status.Body.String(), "<main>application</main>") {
		t.Fatalf("design status with cookie = %d %q", status.Code, status.Body.String())
	}
	if strings.Contains(status.Body.String(), "design-controller-token-0123456789abcdef") || strings.Contains(status.Body.String(), "design-mcp-token-0123456789abcdef0123456789") {
		t.Fatal("design status leaked controller or MCP credentials")
	}
	// Human management authority stays distinct from agent read authority:
	// the MCP guide needs the publisher bearer, not the human cookie, and
	// management needs the cookie, not the bearer.
	const mcpToken = "design-mcp-token-0123456789abcdef0123456789"
	guideWithBearer := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:17880/mcp/design/guide", nil)
	guideWithBearer.Header.Set("Authorization", "Bearer "+mcpToken)
	guideWithBearerResponse := httptest.NewRecorder()
	handler.ServeHTTP(guideWithBearerResponse, guideWithBearer)
	if guideWithBearerResponse.Code != http.StatusOK {
		t.Fatalf("design guide with bearer = %d %q", guideWithBearerResponse.Code, guideWithBearerResponse.Body.String())
	}
	guideWithCookieOnly := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:17880/mcp/design/guide", nil)
	guideWithCookieOnly.AddCookie(cookie)
	guideWithCookieOnly.Header.Set("Sec-Fetch-Site", "same-origin")
	guideWithCookieOnlyResponse := httptest.NewRecorder()
	handler.ServeHTTP(guideWithCookieOnlyResponse, guideWithCookieOnly)
	if guideWithCookieOnlyResponse.Code == http.StatusOK {
		t.Fatalf("design guide accepted human-cookie-only role: %q", guideWithCookieOnlyResponse.Body.String())
	}
	mgmtWithBearerOnly := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:17880/api/design/status", nil)
	mgmtWithBearerOnly.Header.Set("Authorization", "Bearer "+mcpToken)
	mgmtWithBearerOnlyResponse := httptest.NewRecorder()
	handler.ServeHTTP(mgmtWithBearerOnlyResponse, mgmtWithBearerOnly)
	if mgmtWithBearerOnlyResponse.Code == http.StatusOK {
		t.Fatalf("design management accepted MCP-bearer-only role: %q", mgmtWithBearerOnlyResponse.Body.String())
	}
	forgedStatus := doDesignManagement(t, handler, http.MethodGet, "http://127.0.0.1:17880/api/design/status?documentId=abcdabcdabcdabcd", cookie, nil)
	if forgedStatus.Code == http.StatusOK && !strings.Contains(forgedStatus.Body.String(), `"availability":"empty"`) && !strings.Contains(forgedStatus.Body.String(), "stale_document") {
		t.Fatalf("design forged status unexpectedly succeeded: %d %q", forgedStatus.Code, forgedStatus.Body.String())
	}
	// Missing cookie and MCP-bearer-only (wrong role) grant nothing.
	withoutCookie := doDesignManagement(t, handler, http.MethodGet, "http://127.0.0.1:17880/api/design/status", nil, nil)
	if withoutCookie.Code != http.StatusUnauthorized {
		t.Fatalf("design status without cookie = %d %q", withoutCookie.Code, withoutCookie.Body.String())
	}
	assertReservedJSONNotSPA(t, withoutCookie, "design status without cookie")
	// Secrets never enter management URLs.
	secretURL := doDesignManagement(t, handler, http.MethodGet, "http://127.0.0.1:17880/api/design/status?token=secret", cookie, nil)
	if secretURL.Code != http.StatusBadRequest {
		t.Fatalf("design secret-in-URL = %d %q", secretURL.Code, secretURL.Body.String())
	}
	// Upload one document through the human management surface.
	archive := designFixtureArchive(t)
	upload := doDesignManagement(t, handler, http.MethodPost, "http://127.0.0.1:17880/api/design/document?name=fixture.fig&operationId=matrix-1", cookie, archive)
	if upload.Code != http.StatusOK {
		t.Fatalf("design upload = %d %q", upload.Code, upload.Body.String())
	}
	var uploaded struct {
		DocumentID string `json:"documentId"`
		Generation uint64 `json:"generation"`
	}
	if err := json.Unmarshal(upload.Body.Bytes(), &uploaded); err != nil || uploaded.DocumentID == "" {
		t.Fatalf("design upload body = %q err=%v", upload.Body.String(), err)
	}
	if strings.Contains(upload.Body.String(), "design-controller-token-0123456789abcdef") || strings.Contains(upload.Body.String(), mcpToken) {
		t.Fatal("design upload leaked controller or MCP credentials")
	}
	// Forged removal (unknown document) fails closed and removes nothing.
	forgedRemove := doDesignManagement(t, handler, http.MethodDelete, "http://127.0.0.1:17880/api/design/document?documentId=abcdabcdabcdabcd", cookie, nil)
	if forgedRemove.Code == http.StatusOK {
		t.Fatalf("design forged removal succeeded: %q", forgedRemove.Body.String())
	}
	assertReservedJSONNotSPA(t, forgedRemove, "design forged removal")
	stillThere := doDesignManagement(t, handler, http.MethodGet, "http://127.0.0.1:17880/api/design/status?documentId="+uploaded.DocumentID, cookie, nil)
	if stillThere.Code != http.StatusOK {
		t.Fatalf("design status after forged removal = %d %q", stillThere.Code, stillThere.Body.String())
	}
	// Disabled management stays reachable with disabled availability, and
	// retained removal still works while disabled without invoking a parser.
	if err := registry.SetEnabled("design", false); err != nil {
		t.Fatal(err)
	}
	disabledStatus := doDesignManagement(t, handler, http.MethodGet, "http://127.0.0.1:17880/api/design/status", cookie, nil)
	if disabledStatus.Code != http.StatusOK || !strings.Contains(disabledStatus.Body.String(), `"availability":"disabled"`) {
		t.Fatalf("disabled design status = %d %q", disabledStatus.Code, disabledStatus.Body.String())
	}
	disabledRemove := doDesignManagement(t, handler, http.MethodDelete, "http://127.0.0.1:17880/api/design/document?documentId="+uploaded.DocumentID, cookie, nil)
	if disabledRemove.Code != http.StatusOK {
		t.Fatalf("disabled design removal = %d %q", disabledRemove.Code, disabledRemove.Body.String())
	}
}

func TestAssembledCoreRoutesUnclaimable(t *testing.T) {
	// Registry-level ownership: core routes are never registered, and any
	// definition claiming them fails validation.
	for _, route := range []string{"/ws", "/auth", "/auth/login", "/auth/status", "/mcp/objective", "/mcp/objective/sub"} {
		if mcpserver.IsRegisteredRoute(route) {
			t.Fatalf("core route %q reported as registered", route)
		}
	}
	registry := testInProcessRegistry(t)
	for _, route := range []string{"/ws", "/auth/status", "/mcp/objective", "/mcp", "/api", "/api/mcp/modules/extra"} {
		request := httptest.NewRequest(http.MethodGet, "https://pixie.example"+route, nil)
		response := httptest.NewRecorder()
		registry.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound && !(route == "/ws" || route == "/auth/status") {
			// /ws and /auth/* are not registry namespaces at all; every core
			// or unknown path stays registry-owned 404 JSON, never SPA and
			// never a module envelope.
			t.Fatalf("registry core/unknown %q = %d %q", route, response.Code, response.Body.String())
		}
		if response.Code == http.StatusNotFound {
			var decoded map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || decoded["code"] != "not_found" {
				t.Fatalf("registry core/unknown %q body = %q", route, response.Body.String())
			}
		}
	}
	// Assembled-handler ownership with a hit-counting registry: core routes
	// never delegate to the module registry.
	handler := newStaticHandler(t)
	counted := &countedRegistry{handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		response.Header().Set("Cache-Control", "no-store")
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte(`{"code":"registry"}`))
	})}
	handler.MCPRegistry = counted
	webSocket, err := controller.NewWebSocketServer(controller.CoreHandler{}, func(context.Context) (any, error) { return nil, nil }, handler.Auth)
	if err != nil {
		t.Fatal(err)
	}
	handler.WebSocket = webSocket
	for _, item := range []struct {
		method, target string
	}{
		{http.MethodPost, "https://pixie.example/ws"},
		{http.MethodGet, "https://pixie.example/auth/status"},
		{http.MethodGet, "https://pixie.example/mcp/objective"},
	} {
		before := counted.hits
		request := httptest.NewRequest(item.method, item.target, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if counted.hits != before {
			t.Fatalf("core route %s %s delegated to registry (%d hits)", item.method, item.target, counted.hits-before)
		}
		if strings.Contains(response.Body.String(), "<main>application</main>") {
			t.Fatalf("core route %s %s returned SPA: %q", item.method, item.target, response.Body.String())
		}
		if response.Body.String() == `{"code":"registry"}` {
			t.Fatalf("core route %s %s returned registry body", item.method, item.target)
		}
	}
	// Validation explicitly rejects every core-takeover shape.
	for _, path := range []string{"/ws", "/auth", "/mcp/objective", "/mcp"} {
		definition := mcpserver.ModuleDefinition{
			ID: "takeover-core", ExtensionName: "pixie-takeover", DisplayName: "Takeover", Description: "Takeover.",
			Path: path, Transport: "streamable_http", ManagementPaths: []string{"/api/takeover-core"},
			Frontend: mcpserver.FrontendDescriptor{ModuleID: "takeover-core", Version: "1.0.0", Label: "Takeover", Icon: "takeover", Sidebar: true, Viewer: true, ContextScope: "instance", ResourceNamespace: "takeover-core"},
		}
		candidate := append(append([]mcpserver.ModuleDefinition(nil), mcpserver.DefaultModuleDefinitions()...), definition)
		if err := mcpserver.ValidateModuleDefinitions(candidate); err == nil {
			t.Fatalf("core takeover path %q was accepted", path)
		}
	}
}

func TestAssembledCanvasDesignOverlapAndUnknownMatrix(t *testing.T) {
	registry := testInProcessRegistry(t)
	handler := newStaticHandler(t)
	const token = "mcp-token-0123456789abcdef0123456789"
	handler.Auth.MCPToken = token
	handler.MCPRegistry = registry
	mcpAuth := map[string]string{"Authorization": "Bearer " + token}
	mgmtAuth := map[string]string{"Sec-Fetch-Site": "same-origin"}
	targets := []string{
		"https://pixie.example/mcp/canvas-evil",
		"https://pixie.example/mcp/design-evil",
		"https://pixie.example/api/canvas-evil",
		"https://pixie.example/api/design-evil",
		"https://pixie.example/mcp/canvasfoo/bar",
		"https://pixie.example/mcp/designfoo/bar",
		"https://pixie.example/api/canvasfoo/bar",
		"https://pixie.example/api/designfoo/bar",
		"https://pixie.example/mcp/canvas/unknown",
		"https://pixie.example/mcp/design/unknown",
		"https://pixie.example/api/canvas/unknown",
		"https://pixie.example/api/design/unknown",
		"https://pixie.example/mcp/unknown",
		"https://pixie.example/api/unknown",
		"https://pixie.example//mcp/canvas-evil",
		"https://pixie.example//api/design-evil",
		"https://pixie.example/./mcp/design-evil",
		"https://pixie.example/./api/canvas-evil",
		"https://pixie.example/a/../mcp/canvas-evil",
		"https://pixie.example/a/../api/design-evil",
	}
	for _, target := range targets {
		for name, headers := range map[string]map[string]string{"mcp": mcpAuth, "mgmt": mgmtAuth, "none": nil} {
			request := httptest.NewRequest(http.MethodGet, target, nil)
			for key, value := range headers {
				request.Header.Set(key, value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			assertReservedJSONNotSPA(t, response, target+" ("+name+")")
		}
	}
}
