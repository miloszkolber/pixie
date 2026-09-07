package browser_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/browser"
)

type appViewRegistration struct {
	Ticket, Path, URL, ExpiresAt string
}

func validAppViewRegistration() map[string]any {
	return map[string]any{
		"parentOrigin": "https://pixie.example",
		"csp": map[string]any{
			"connectDomains":  []string{"https://api.example.com", "wss://events.example.com"},
			"resourceDomains": []string{"https://cdn.example.com"},
			"frameDomains":    []string{"https://frames.example.com"},
			"baseUriDomains":  []string{"https://base.example.com"},
		},
		"permissions": map[string]any{"camera": map[string]any{}, "clipboardWrite": map[string]any{}},
	}
}

func registerAppView(t *testing.T, handler http.Handler, token string, body any) (*httptest.ResponseRecorder, appViewRegistration) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/app-views", bytes.NewReader(encoded))
	request.Host = "127.0.0.1:8787"
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var result appViewRegistration
	if response.Code >= 200 && response.Code < 300 {
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
	}
	return response, result
}

func getAppView(handler http.Handler, path, host string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Host = host
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestAppViewTicketKeepsMutationAuthenticatedAndSandboxPublic(t *testing.T) {
	runtime := newTestRuntime(t, true, nil)
	unauthorized, _ := registerAppView(t, runtime.service, "", validAppViewRegistration())
	if unauthorized.Code != http.StatusUnauthorized || responseCode(t, unauthorized) != "unauthorized" {
		t.Fatalf("unauthorized registration = %d %s", unauthorized.Code, unauthorized.Body.String())
	}

	created, registration := registerAppView(t, runtime.service, testToken, validAppViewRegistration())
	if created.Code != http.StatusCreated || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(registration.Ticket) {
		t.Fatalf("registration = %d %#v", created.Code, registration)
	}
	if registration.Path != "/v1/app-views/"+registration.Ticket || registration.URL != "http://127.0.0.1:8787"+registration.Path {
		t.Fatalf("registration URL = %#v", registration)
	}
	if _, err := time.Parse(time.RFC3339Nano, registration.ExpiresAt); err != nil {
		t.Fatalf("registration expiry = %q: %v", registration.ExpiresAt, err)
	}

	view := getAppView(runtime.service, registration.Path, "127.0.0.1:8787")
	if view.Code != http.StatusOK || view.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("public app view = %d %#v", view.Code, view.Header())
	}
	for name, expected := range map[string]string{
		"Cache-Control":          "no-store",
		"Referrer-Policy":        "no-referrer",
		"X-Content-Type-Options": "nosniff",
		"Permissions-Policy":     "camera=(self), microphone=(), geolocation=(), clipboard-write=(self)",
	} {
		if view.Header().Get(name) != expected {
			t.Fatalf("%s = %q", name, view.Header().Get(name))
		}
	}
	csp := view.Header().Get("Content-Security-Policy")
	if !containsAll(csp,
		"frame-ancestors 'self' https://pixie.example",
		"connect-src https://api.example.com wss://events.example.com",
		"script-src 'self' 'unsafe-inline' https://cdn.example.com",
		"frame-src https://frames.example.com",
		"base-uri https://base.example.com",
		"sandbox allow-scripts allow-same-origin allow-forms",
	) || strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "blob:") {
		t.Fatalf("unexpected App CSP: %s", csp)
	}
	document := view.Body.String()
	if !containsAll(document,
		`event.origin !== expectedParentOrigin`,
		`event.source !== inner.contentWindow`,
		`ui/notifications/sandbox-proxy-ready`,
		`new TextEncoder().encode(html).byteLength > maxHTMLBytes`,
		`inner.setAttribute("sandbox", "allow-scripts allow-same-origin allow-forms")`,
		`const allowedPermissions = new Set(["camera","clipboardWrite"]);`,
	) {
		t.Fatal("sandbox relay omitted a required authority or size guard")
	}
	for _, forbidden := range []string{testToken, "Authorization", "Bearer ", "/mcp", "/v1/browser", "fetch(", "XMLHttpRequest", "WebSocket"} {
		if strings.Contains(document, forbidden) {
			t.Fatalf("sandbox relay contains authority %q", forbidden)
		}
	}
	if rebound := getAppView(runtime.service, registration.Path, "evil.example:8787"); rebound.Code != http.StatusNotFound {
		t.Fatalf("ticket served through an unexpected Host: %d", rebound.Code)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, registration.Path, nil)
	withoutAuth := httptest.NewRecorder()
	runtime.service.ServeHTTP(withoutAuth, deleteRequest)
	if withoutAuth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized deletion = %d", withoutAuth.Code)
	}
	deleteRequest = httptest.NewRequest(http.MethodDelete, registration.Path, nil)
	deleteRequest.Header.Set("Authorization", "Bearer "+testToken)
	deleted := httptest.NewRecorder()
	runtime.service.ServeHTTP(deleted, deleteRequest)
	if deleted.Code != http.StatusOK || getAppView(runtime.service, registration.Path, "127.0.0.1:8787").Code != http.StatusNotFound {
		t.Fatalf("authenticated deletion = %d %s", deleted.Code, deleted.Body.String())
	}
}

func TestAppViewPolicyQuotaAndShutdownFailClosed(t *testing.T) {
	withoutAuthentication := newTestRuntime(t, false, nil)
	if response, _ := registerAppView(t, withoutAuthentication.service, "", validAppViewRegistration()); response.Code != http.StatusUnauthorized {
		t.Fatalf("registration without configured bearer auth = %d", response.Code)
	}
	runtime := newTestRuntime(t, true, func(config *browser.Config) {
		config.PublicOrigin = "https://browser.example"
	})
	for _, body := range []map[string]any{
		{"parentOrigin": "https://pixie.example; frame-src *"},
		{"parentOrigin": "https://pixie.example", "csp": map[string]any{"resourceDomains": []string{"https://cdn.example.com; script-src *"}}},
		{"parentOrigin": "https://pixie.example", "permissions": map[string]any{"downloads": map[string]any{}}},
		{"parentOrigin": "https://pixie.example", "unknown": true},
		{"parentOrigin": "https://browser.example:443"},
		{"parentOrigin": "https://*.example.com"},
	} {
		response, _ := registerAppView(t, runtime.service, testToken, body)
		if response.Code != http.StatusBadRequest || responseCode(t, response) != "invalid_app_view" {
			t.Fatalf("unsafe policy accepted: %d %s", response.Code, response.Body.String())
		}
	}

	first, registration := registerAppView(t, runtime.service, testToken, map[string]any{"parentOrigin": "https://pixie.example"})
	if first.Code != http.StatusCreated || registration.URL != "https://browser.example"+registration.Path {
		t.Fatalf("public-origin registration = %d %#v", first.Code, registration)
	}
	defaults := getAppView(runtime.service, registration.Path, "browser.example")
	defaultCSP := defaults.Header().Get("Content-Security-Policy")
	if defaults.Code != http.StatusOK || !containsAll(defaultCSP, "connect-src 'none'", "frame-src 'none'", "font-src 'none'", "base-uri 'self'") || strings.Contains(defaultCSP, "blob:") {
		t.Fatalf("restrictive App defaults = %d %s", defaults.Code, defaultCSP)
	}
	for range 63 {
		response, _ := registerAppView(t, runtime.service, testToken, validAppViewRegistration())
		if response.Code != http.StatusCreated {
			t.Fatalf("ticket registration before quota = %d %s", response.Code, response.Body.String())
		}
	}
	overflow, _ := registerAppView(t, runtime.service, testToken, validAppViewRegistration())
	if overflow.Code != http.StatusTooManyRequests || responseCode(t, overflow) != "app_view_limit" {
		t.Fatalf("ticket quota = %d %s", overflow.Code, overflow.Body.String())
	}
	runtime.service.Shutdown()
	if response := getAppView(runtime.service, registration.Path, "browser.example"); response.Code != http.StatusServiceUnavailable || responseCode(t, response) != "shutting_down" {
		t.Fatalf("shutdown served an App view: %d %s", response.Code, response.Body.String())
	}
	if response, _ := registerAppView(t, runtime.service, testToken, validAppViewRegistration()); response.Code != http.StatusServiceUnavailable || responseCode(t, response) != "shutting_down" {
		t.Fatalf("shutdown accepted an App registration: %d %s", response.Code, response.Body.String())
	}
}
