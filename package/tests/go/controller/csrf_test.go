package controller_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

const (
	csrfControllerToken = "controller-token-0123456789abcdef0123456789"
	csrfMCPToken        = "mcp-token-0123456789abcdef0123456789"
)

func csrfAuthenticatedConfig() controller.AuthConfig {
	return controller.AuthConfig{
		Enabled:         true,
		ControllerToken: csrfControllerToken,
		MCPToken:        csrfMCPToken,
		ControllerPort:  7312,
		PublicOrigin:    "http://127.0.0.1:7312",
	}
}

func csrfLoginCookie(t *testing.T, handler *controller.HTTPHandler) *http.Cookie {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7312/auth/login", strings.NewReader(`{"token":"`+csrfControllerToken+`"}`))
	request.Host = "127.0.0.1:7312"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://127.0.0.1:7312")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("CSRF login fixture returned %d: %s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("CSRF login issued %d cookies", len(cookies))
	}
	return cookies[0]
}

func TestCSRFLoginRequiresExactOriginAndFetchSite(t *testing.T) {
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, csrfAuthenticatedConfig(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	const origin = "http://127.0.0.1:7312"
	doLogin := func(setHeaders func(*http.Request)) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, origin+"/auth/login", strings.NewReader(`{"token":"`+csrfControllerToken+`"}`))
		request.Host = "127.0.0.1:7312"
		request.Header.Set("Content-Type", "application/json")
		setHeaders(request)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := doLogin(func(request *http.Request) {
		request.Header.Set("Origin", origin)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
	}); response.Code != http.StatusOK {
		t.Fatalf("exact-origin CSRF login returned %d: %s", response.Code, response.Body.String())
	}
	for _, tc := range []struct {
		name    string
		headers func(*http.Request)
	}{
		{name: "cross origin", headers: func(request *http.Request) {
			request.Header.Set("Origin", "http://evil.example")
			request.Header.Set("Sec-Fetch-Site", "same-origin")
		}},
		{name: "missing origin", headers: func(request *http.Request) {
			request.Header.Set("Sec-Fetch-Site", "same-origin")
		}},
		{name: "missing fetch-site", headers: func(request *http.Request) {
			request.Header.Set("Origin", origin)
		}},
		{name: "cross fetch-site", headers: func(request *http.Request) {
			request.Header.Set("Origin", origin)
			request.Header.Set("Sec-Fetch-Site", "cross-site")
		}},
		{name: "none fetch-site", headers: func(request *http.Request) {
			request.Header.Set("Origin", origin)
			request.Header.Set("Sec-Fetch-Site", "none")
		}},
		{name: "evil origin", headers: func(request *http.Request) {
			request.Header.Set("Origin", "http://EVIL.example")
			request.Header.Set("Sec-Fetch-Site", "same-origin")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if response := doLogin(tc.headers); response.Code != http.StatusForbidden {
				t.Fatalf("CSRF login %s returned %d, want %d", tc.name, response.Code, http.StatusForbidden)
			}
		})
	}
	if response := doLogin(func(request *http.Request) {
		request.Header.Set("Origin", origin+"/")
		request.Header.Set("Sec-Fetch-Site", "same-origin")
	}); response.Code != http.StatusOK {
		t.Fatalf("normalized trailing-slash Origin returned %d, want %d", response.Code, http.StatusOK)
	}
	duplicate := httptest.NewRequest(http.MethodPost, origin+"/auth/login", strings.NewReader(`{"token":"`+csrfControllerToken+`"}`))
	duplicate.Host = "127.0.0.1:7312"
	duplicate.Header.Set("Content-Type", "application/json")
	duplicate.Header.Add("Origin", origin)
	duplicate.Header.Add("Origin", origin)
	duplicate.Header.Set("Sec-Fetch-Site", "same-origin")
	duplicateResponse := httptest.NewRecorder()
	handler.ServeHTTP(duplicateResponse, duplicate)
	if duplicateResponse.Code != http.StatusForbidden {
		t.Fatalf("duplicate Origin CSRF login returned %d, want %d", duplicateResponse.Code, http.StatusForbidden)
	}
}

func TestCSRFLogoutRequiresExactOrigin(t *testing.T) {
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, csrfAuthenticatedConfig(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	cookie := csrfLoginCookie(t, handler)
	doLogout := func(origin, fetchSite string, withCookie bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7312/auth/logout", strings.NewReader(`{}`))
		request.Host = "127.0.0.1:7312"
		request.Header.Set("Content-Type", "application/json")
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if fetchSite != "" {
			request.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		if withCookie {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	const origin = "http://127.0.0.1:7312"
	if response := doLogout(origin, "same-origin", true); response.Code != http.StatusOK {
		t.Fatalf("exact-origin CSRF logout returned %d: %s", response.Code, response.Body.String())
	}
	if response := doLogout("http://evil.example", "same-origin", true); response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin CSRF logout returned %d, want %d", response.Code, http.StatusForbidden)
	}
	if response := doLogout("", "same-origin", true); response.Code != http.StatusForbidden {
		t.Fatalf("missing-origin CSRF logout returned %d, want %d", response.Code, http.StatusForbidden)
	}
	if response := doLogout(origin, "cross-site", true); response.Code != http.StatusForbidden {
		t.Fatalf("cross-site CSRF logout returned %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestCSRFWebSocketOriginEnforcement(t *testing.T) {
	newServer := func(t *testing.T, config controller.AuthConfig) (*controller.WebSocketServer, *httptest.Server) {
		t.Helper()
		server, err := controller.NewWebSocketServer(&countingHandler{}, nil, config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { server.Close(context.Background()) })
		host := httptest.NewServer(server)
		t.Cleanup(host.Close)
		parsed, err := url.Parse(host.URL)
		if err != nil {
			t.Fatal(err)
		}
		port, err := strconv.Atoi(parsed.Port())
		if err != nil {
			t.Fatal(err)
		}
		server.Auth.ControllerPort = port
		return server, host
	}
	t.Run("no-auth LAN", func(t *testing.T) {
		_, host := newServer(t, controller.AuthConfig{ControllerPort: 0})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		exactDial, _, exactErr := websocket.Dial(ctx, "ws"+strings.TrimPrefix(host.URL, "http"), &websocket.DialOptions{
			HTTPHeader: http.Header{"Origin": {host.URL}},
		})
		if exactErr != nil {
			t.Fatalf("exact-origin no-auth WebSocket dial failed: %v", exactErr)
		}
		exactDial.CloseNow()
		crossCtx, crossCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer crossCancel()
		crossConn, crossResponse, crossErr := websocket.Dial(crossCtx, "ws"+strings.TrimPrefix(host.URL, "http"), &websocket.DialOptions{
			HTTPHeader: http.Header{"Origin": {"http://evil.example"}},
		})
		if crossConn != nil {
			crossConn.CloseNow()
		}
		if crossErr == nil || crossResponse == nil || crossResponse.StatusCode != http.StatusForbidden {
			t.Fatalf("cross-origin no-auth WebSocket accepted: response=%v err=%v", crossResponse, crossErr)
		}
		missingCtx, missingCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer missingCancel()
		missingConn, missingResponse, missingErr := websocket.Dial(missingCtx, "ws"+strings.TrimPrefix(host.URL, "http"), &websocket.DialOptions{
			HTTPHeader: http.Header{},
		})
		if missingConn != nil {
			missingConn.CloseNow()
		}
		if missingErr == nil || missingResponse == nil || missingResponse.StatusCode != http.StatusForbidden {
			t.Fatalf("missing-origin no-auth WebSocket accepted: response=%v err=%v", missingResponse, missingErr)
		}
	})
	t.Run("authenticated", func(t *testing.T) {
		config := csrfAuthenticatedConfig()
		auth, err := controller.NewAuth(csrfControllerToken)
		if err != nil {
			t.Fatal(err)
		}
		session, ok := auth.Login(csrfControllerToken)
		if !ok {
			t.Fatal("controller token rejected")
		}
		_, host := newServer(t, controller.AuthConfig{
			Enabled:         true,
			ControllerToken: csrfControllerToken,
			ControllerPort:  0,
			PublicOrigin:    "http://127.0.0.1:7312",
		})
		_ = config
		parsed, err := url.Parse(host.URL)
		if err != nil {
			t.Fatal(err)
		}
		_ = parsed
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		crossConn, crossResponse, crossErr := websocket.Dial(ctx, "ws"+strings.TrimPrefix(host.URL, "http"), &websocket.DialOptions{
			HTTPHeader: http.Header{
				"Origin": {"http://evil.example"},
				"Cookie": {controller.AuthCookieName + "=" + session},
			},
		})
		if crossConn != nil {
			crossConn.CloseNow()
		}
		if crossErr == nil || crossResponse == nil || crossResponse.StatusCode != http.StatusForbidden {
			t.Fatalf("cross-origin credentialed WebSocket accepted: response=%v err=%v", crossResponse, crossErr)
		}
		missingCtx, missingCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer missingCancel()
		missingConn, missingResponse, missingErr := websocket.Dial(missingCtx, "ws"+strings.TrimPrefix(host.URL, "http"), &websocket.DialOptions{
			HTTPHeader: http.Header{
				"Cookie": {controller.AuthCookieName + "=" + session},
			},
		})
		if missingConn != nil {
			missingConn.CloseNow()
		}
		if missingErr == nil || missingResponse == nil || missingResponse.StatusCode != http.StatusForbidden {
			t.Fatalf("missing-origin credentialed WebSocket accepted: response=%v err=%v", missingResponse, missingErr)
		}
	})
}

func TestCSRFFileReadsRejectCrossOriginWithCredentials(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.png"), []byte("hello-image"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err := workspace.NewPathPolicy([]string{root}, false)
	if err != nil {
		t.Fatal(err)
	}
	projects := workspace.NewProjects(persist.Store{Dir: t.TempDir()}, policy)
	project, err := projects.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, projects, workspace.NewFiles(projects, policy), csrfAuthenticatedConfig(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	cookie := csrfLoginCookie(t, handler)
	target := "http://127.0.0.1:7312/files/" + project.ID + "/hello.png"
	doFile := func(origin, fetchSite string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Host = "127.0.0.1:7312"
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if fetchSite != "" {
			request.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	const exactOrigin = "http://127.0.0.1:7312"
	if response := doFile(exactOrigin, "same-origin"); response.Code != http.StatusOK {
		t.Fatalf("exact-origin credentialed file read returned %d: %s", response.Code, response.Body.String())
	}
	if response := doFile("", "same-origin"); response.Code != http.StatusOK {
		t.Fatalf("same-fetch-site credentialed file read returned %d: %s", response.Code, response.Body.String())
	}
	if response := doFile("http://evil.example", "cross-site"); response.Code != http.StatusUnauthorized {
		t.Fatalf("cross-origin credentialed file read returned %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if response := doFile("http://evil.example", "same-origin"); response.Code != http.StatusUnauthorized {
		t.Fatalf("mismatched-origin credentialed file read returned %d, want %d", response.Code, http.StatusUnauthorized)
	}
	duplicate := httptest.NewRequest(http.MethodGet, target, nil)
	duplicate.Host = "127.0.0.1:7312"
	duplicate.Header.Add("Origin", exactOrigin)
	duplicate.Header.Add("Origin", exactOrigin)
	duplicate.Header.Set("Sec-Fetch-Site", "same-origin")
	duplicate.AddCookie(cookie)
	duplicateResponse := httptest.NewRecorder()
	handler.ServeHTTP(duplicateResponse, duplicate)
	if duplicateResponse.Code != http.StatusUnauthorized {
		t.Fatalf("duplicate-origin credentialed file read returned %d, want %d", duplicateResponse.Code, http.StatusUnauthorized)
	}
}

func TestCSRFArtifactReadsRejectCrossOriginWithCredentials(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "image/png")
		response.Header().Set("Content-Length", "3")
		_, _ = response.Write([]byte("png"))
	}))
	defer upstream.Close()
	config := csrfAuthenticatedConfig()
	config.BrowserURL = upstream.URL
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, config, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	cookie := csrfLoginCookie(t, handler)
	target := "http://127.0.0.1:7312/v1/artifacts/panel/screen.png"
	doArtifact := func(origin, fetchSite string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Host = "127.0.0.1:7312"
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if fetchSite != "" {
			request.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	const exactOrigin = "http://127.0.0.1:7312"
	if response := doArtifact(exactOrigin, "same-origin"); response.Code != http.StatusOK {
		t.Fatalf("exact-origin credentialed artifact read returned %d: %s", response.Code, response.Body.String())
	}
	if response := doArtifact("", "same-origin"); response.Code != http.StatusOK {
		t.Fatalf("same-fetch-site credentialed artifact read returned %d: %s", response.Code, response.Body.String())
	}
	if response := doArtifact("http://evil.example", "cross-site"); response.Code != http.StatusUnauthorized {
		t.Fatalf("cross-origin credentialed artifact read returned %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if response := doArtifact("https://127.0.0.1:7312", "same-origin"); response.Code != http.StatusUnauthorized {
		t.Fatalf("scheme-mismatched credentialed artifact read returned %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestCSRFServiceBearerWithoutOriginAllowed(t *testing.T) {
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, controller.AuthConfig{
		ControllerPort: 7312,
		PublicOrigin:   "http://127.0.0.1:7312",
		MCPToken:       csrfMCPToken,
	}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	handler.MCPRegistry = http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})
	serviceTarget := "http://127.0.0.1:7312/api/mcp/status"
	bearer := httptest.NewRequest(http.MethodGet, serviceTarget, nil)
	bearer.Host = "127.0.0.1:7312"
	bearer.Header.Set("Authorization", "Bearer "+csrfMCPToken)
	bearerResponse := httptest.NewRecorder()
	handler.ServeHTTP(bearerResponse, bearer)
	if bearerResponse.Code != http.StatusNoContent {
		t.Fatalf("authenticated service call without Origin returned %d, want %d", bearerResponse.Code, http.StatusNoContent)
	}
	crossService := httptest.NewRequest(http.MethodGet, serviceTarget, nil)
	crossService.Host = "127.0.0.1:7312"
	crossService.Header.Set("Authorization", "Bearer "+csrfMCPToken)
	crossService.Header.Set("Origin", "http://evil.example")
	crossResponse := httptest.NewRecorder()
	handler.ServeHTTP(crossResponse, crossService)
	if crossResponse.Code == http.StatusNoContent {
		t.Fatalf("cross-origin service call was accepted: %d", crossResponse.Code)
	}
	unauthenticated := httptest.NewRequest(http.MethodGet, serviceTarget, nil)
	unauthenticated.Host = "127.0.0.1:7312"
	unauthenticatedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticatedResponse, unauthenticated)
	if unauthenticatedResponse.Code == http.StatusNoContent {
		t.Fatalf("unauthenticated service call was accepted: %d", unauthenticatedResponse.Code)
	}
}
