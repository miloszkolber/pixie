package controller_test

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/miloszkolber/pixie/internal/browser"
	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/diagnostics"
)

type artifactBrowserRegistry struct {
	lookup func() http.Handler
}

func (r artifactBrowserRegistry) BrowserLegacyHandler() func() http.Handler { return r.lookup }
func (r artifactBrowserRegistry) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	http.NotFound(w, request)
}

func TestBrowserArtifactInProcessAuthenticatedImage(t *testing.T) {
	// No HTTP client should be involved in either the login or artifact flow.
	var externalCalls atomic.Int32
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripper(func(*http.Request) (*http.Response, error) {
		externalCalls.Add(1)
		return nil, fmt.Errorf("unexpected external HTTP request")
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	root := t.TempDir()
	configFile := filepath.Join(root, "browser.json")
	if err := os.WriteFile(configFile, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	artifactRoot := filepath.Join(root, "artifacts")
	service, err := browser.NewService(browser.Config{
		Host: "127.0.0.1", Port: 7312, Authentication: true, Token: browserPanelToken,
		ArtifactRoot: artifactRoot, StateRoot: filepath.Join(root, "state"),
		AgentBrowser: "/bin/true", BrowserConfig: configFile, MaxArtifactBytes: 1024,
	}, diagnostics.BuildInfo{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Shutdown)
	var fixture bytes.Buffer
	if err := png.Encode(&fixture, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(artifactRoot, "panel"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactRoot, "panel", "screen.png"), fixture.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	const controllerToken = "distinct-artifact-ui-token-0123456789"
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, controller.AuthConfig{
		Enabled: true, ControllerToken: controllerToken, MCPToken: browserPanelToken,
		PublicOrigin: "http://127.0.0.1:7312",
	}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7312/auth/login", strings.NewReader(`{"token":"`+controllerToken+`"}`))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Sec-Fetch-Site", "same-origin")
	login.Header.Set("Origin", "http://127.0.0.1:7312")
	loggedIn := httptest.NewRecorder()
	handler.ServeHTTP(loggedIn, login)
	if loggedIn.Code != http.StatusOK || len(loggedIn.Result().Cookies()) != 1 {
		t.Fatalf("controller login failed: %d", loggedIn.Code)
	}
	cookie := loggedIn.Result().Cookies()[0]
	var moduleCalls atomic.Int32
	enabled := artifactBrowserRegistry{lookup: func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			moduleCalls.Add(1)
			if r.Method != http.MethodGet || r.URL.Path != "/v1/artifacts/panel/screen.png" || r.RequestURI != r.URL.Path || r.Host != "127.0.0.1" {
				t.Errorf("invalid module request: method=%s URL=%v URI=%q Host=%q", r.Method, r.URL, r.RequestURI, r.Host)
			}
			if r.Header.Get("Authorization") != "Bearer "+browserPanelToken || r.Header.Get("Cookie") != "" {
				t.Error("module credential missing or controller cookie forwarded")
			}
			service.ServeHTTP(w, r)
		})
	}}
	for _, tc := range []struct {
		name       string
		registry   http.Handler
		authorized bool
		status     int
		calls      int32
	}{
		{"enabled", enabled, true, http.StatusOK, 1},
		{"unauthenticated", enabled, false, http.StatusUnauthorized, 0},
		{"absent registry", nil, true, http.StatusBadGateway, 0},
		{"missing legacy surface", http.NotFoundHandler(), true, http.StatusBadGateway, 0},
		{"missing callback", artifactBrowserRegistry{}, true, http.StatusBadGateway, 0},
		{"disabled module", artifactBrowserRegistry{lookup: func() http.Handler { return nil }}, true, http.StatusBadGateway, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler.MCPRegistry = tc.registry
			before := moduleCalls.Load()
			request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7312/v1/artifacts/panel/screen.png", nil)
			request.Header.Set("Sec-Fetch-Site", "same-origin")
			if tc.authorized {
				request.AddCookie(cookie)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status || moduleCalls.Load()-before != tc.calls {
				t.Fatalf("artifact status=%d calls=%d body=%s", response.Code, moduleCalls.Load()-before, response.Body.String())
			}
			if tc.status == http.StatusOK {
				if !bytes.Equal(response.Body.Bytes(), fixture.Bytes()) {
					t.Fatal("artifact image bytes changed")
				}
				for key, want := range map[string]string{
					"Content-Type": "image/png", "Content-Length": strconv.Itoa(fixture.Len()),
					"Cache-Control": "no-store", "Cross-Origin-Resource-Policy": "same-origin", "X-Content-Type-Options": "nosniff",
				} {
					if got := response.Header().Get(key); got != want {
						t.Errorf("%s = %q, want %q", key, got, want)
					}
				}
			}
			if strings.Contains(response.Header().Get("Authorization"), browserPanelToken) || bytes.Contains(response.Body.Bytes(), []byte(browserPanelToken)) {
				t.Error("publisher credential leaked to viewer")
			}
		})
	}
	if externalCalls.Load() != 0 {
		t.Fatalf("in-process artifact flow made %d external requests", externalCalls.Load())
	}
}
