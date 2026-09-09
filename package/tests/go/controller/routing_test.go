package controller_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
