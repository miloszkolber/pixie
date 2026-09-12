package controller_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

const (
	postureControllerToken = "controller-token-0123456789abcdef0123456789"
	postureMCPToken        = "mcp-token-0123456789abcdef0123456789"
)

func postureNoAuthLANConfig() controller.AuthConfig {
	return controller.AuthConfig{
		ControllerPort:     7312,
		PublicOrigin:       "http://pixie.example",
		AllowRemoteWithout: true,
	}
}

func postureAuthenticatedConfig() controller.AuthConfig {
	return controller.AuthConfig{
		Enabled:         true,
		ControllerToken: postureControllerToken,
		MCPToken:        postureMCPToken,
		ControllerPort:  7312,
		PublicOrigin:    "http://127.0.0.1:7312",
	}
}

func postureFileFixture(t *testing.T, config controller.AuthConfig) (*controller.HTTPHandler, string) {
	t.Helper()
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
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, projects, workspace.NewFiles(projects, policy), config, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return handler, project.ID
}

func postureLoginCookie(t *testing.T, handler *controller.HTTPHandler, origin string) *http.Cookie {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7312/auth/login", strings.NewReader(`{"token":"`+postureControllerToken+`"}`))
	request.Host = "127.0.0.1:7312"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login for posture fixture returned %d: %s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login did not issue exactly one cookie: %v", cookies)
	}
	return cookies[0]
}

func TestPostureAuthenticatedLoginExactOrigin(t *testing.T) {
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, postureAuthenticatedConfig(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	const origin = "http://127.0.0.1:7312"
	doLogin := func(originHeaders []string, fetchSite, token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, origin+"/auth/login", strings.NewReader(`{"token":"`+token+`"}`))
		request.Host = "127.0.0.1:7312"
		request.Header.Set("Content-Type", "application/json")
		for _, value := range originHeaders {
			request.Header.Add("Origin", value)
		}
		if fetchSite != "" {
			request.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := doLogin([]string{origin}, "same-origin", postureControllerToken); response.Code != http.StatusOK {
		t.Fatalf("exact-origin login returned %d: %s", response.Code, response.Body.String())
	}
	if response := doLogin([]string{"http://evil.example"}, "same-origin", postureControllerToken); response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin login returned %d, want %d", response.Code, http.StatusForbidden)
	}
	if response := doLogin(nil, "same-origin", postureControllerToken); response.Code != http.StatusForbidden {
		t.Fatalf("missing-origin login returned %d, want %d", response.Code, http.StatusForbidden)
	}
	if response := doLogin([]string{origin, origin}, "same-origin", postureControllerToken); response.Code != http.StatusForbidden {
		t.Fatalf("duplicate-origin login returned %d, want %d", response.Code, http.StatusForbidden)
	}
	if response := doLogin([]string{origin}, "cross-site", postureControllerToken); response.Code != http.StatusForbidden {
		t.Fatalf("cross-site login returned %d, want %d", response.Code, http.StatusForbidden)
	}
	if response := doLogin([]string{origin}, "same-origin", "wrong-token-0123456789abcdef0123456789"); response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-token login returned %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestPostureNoAuthLANFileOriginEnforcement(t *testing.T) {
	handler, projectID := postureFileFixture(t, postureNoAuthLANConfig())
	const targetBase = "http://pixie.example/files/"
	doFile := func(origin, fetchSite string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, targetBase+projectID+"/hello.png", nil)
		request.Host = "pixie.example"
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if fetchSite != "" {
			request.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	const exactOrigin = "http://pixie.example"
	if response := doFile(exactOrigin, "same-origin"); response.Code != http.StatusOK {
		t.Fatalf("exact-origin no-auth file read returned %d: %s", response.Code, response.Body.String())
	}
	if response := doFile("", "same-origin"); response.Code != http.StatusOK {
		t.Fatalf("same-fetch-site without Origin returned %d, want %d", response.Code, http.StatusOK)
	}
	if response := doFile("http://evil.example", "cross-site"); response.Code != http.StatusUnauthorized {
		t.Fatalf("cross-origin no-auth file read returned %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if response := doFile(exactOrigin, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("exact Origin without fetch-site returned %d, want %d", response.Code, http.StatusUnauthorized)
	}
	evilHost := httptest.NewRequest(http.MethodGet, targetBase+projectID+"/hello.png", nil)
	evilHost.Host = "evil.example"
	evilHost.Header.Set("Origin", exactOrigin)
	evilHost.Header.Set("Sec-Fetch-Site", "same-origin")
	evilResponse := httptest.NewRecorder()
	handler.ServeHTTP(evilResponse, evilHost)
	if evilResponse.Code != http.StatusForbidden {
		t.Fatalf("unapproved Host with matching Origin returned %d, want %d", evilResponse.Code, http.StatusForbidden)
	}
}

func TestPostureAuthenticatedFileAndArtifactAuthGating(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "image/png")
		response.Header().Set("Content-Length", "3")
		_, _ = response.Write([]byte("png"))
	}))
	defer upstream.Close()
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
	config := postureAuthenticatedConfig()
	config.BrowserURL = upstream.URL
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, projects, workspace.NewFiles(projects, policy), config, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	const origin = "http://127.0.0.1:7312"
	cookie := postureLoginCookie(t, handler, origin)
	fileTarget := "http://127.0.0.1:7312/files/" + project.ID + "/hello.png"
	doFile := func(withCookie bool, origin, fetchSite string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, fileTarget, nil)
		request.Host = "127.0.0.1:7312"
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
	if response := doFile(false, "", "same-origin"); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated file read returned %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if response := doFile(true, "", "same-origin"); response.Code != http.StatusOK {
		t.Fatalf("authenticated file read returned %d: %s", response.Code, response.Body.String())
	}
	if response := doFile(true, "http://evil.example", "cross-site"); response.Code != http.StatusUnauthorized {
		t.Fatalf("cross-origin credentialed file read returned %d, want %d", response.Code, http.StatusUnauthorized)
	}
	artifactTarget := "http://127.0.0.1:7312/v1/artifacts/panel/screen.png"
	doArtifact := func(withCookie bool, origin, fetchSite string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, artifactTarget, nil)
		request.Host = "127.0.0.1:7312"
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
	if response := doArtifact(false, "", "same-origin"); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated artifact read returned %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if response := doArtifact(true, "", "same-origin"); response.Code != http.StatusOK {
		t.Fatalf("authenticated artifact read returned %d: %s", response.Code, response.Body.String())
	}
	if response := doArtifact(true, "http://evil.example", "cross-site"); response.Code != http.StatusUnauthorized {
		t.Fatalf("cross-origin credentialed artifact read returned %d, want %d", response.Code, http.StatusUnauthorized)
	}
	badName := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7312/v1/artifacts/panel/evil.txt", nil)
	badName.Host = "127.0.0.1:7312"
	badName.Header.Set("Sec-Fetch-Site", "same-origin")
	badName.AddCookie(cookie)
	badResponse := httptest.NewRecorder()
	handler.ServeHTTP(badResponse, badName)
	if badResponse.Code != http.StatusNotFound {
		t.Fatalf("invalid artifact name returned %d, want %d", badResponse.Code, http.StatusNotFound)
	}
}

func TestPostureTraversalContainmentInFileReads(t *testing.T) {
	for _, mode := range []struct {
		name   string
		config controller.AuthConfig
		authed bool
	}{
		{name: "no-auth LAN", config: postureNoAuthLANConfig()},
		{name: "authenticated", config: postureAuthenticatedConfig(), authed: true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "hello.png"), []byte("hello-image"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(outside, "secret.png"), []byte("top-secret-bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, "secret.png"), filepath.Join(root, "evil.png")); err != nil {
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
			handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, projects, workspace.NewFiles(projects, policy), mode.config, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			var cookie *http.Cookie
			host, origin, base := "", "", ""
			if mode.authed {
				host = "127.0.0.1:7312"
				origin = "http://127.0.0.1:7312"
				base = "http://" + host
				cookie = postureLoginCookie(t, handler, origin)
			} else {
				host = "pixie.example"
				origin = "http://pixie.example"
				base = "http://" + host
			}
			doGet := func(escapedPath string) *httptest.ResponseRecorder {
				request := httptest.NewRequest(http.MethodGet, base+"/files/"+project.ID+"/"+escapedPath, nil)
				request.Host = host
				request.Header.Set("Sec-Fetch-Site", "same-origin")
				if mode.authed {
					request.AddCookie(cookie)
					request.Header.Set("Origin", origin)
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				return response
			}
			if response := doGet("hello.png"); response.Code != http.StatusOK || response.Body.String() != "hello-image" {
				t.Fatalf("valid file read returned %d %q", response.Code, response.Body.String())
			}
			for _, traversal := range []string{
				"../secret.png",
				"..%2Fsecret.png",
				"%2e%2e/secret.png",
				"%2e%2e%2fsecret.png",
				"subdir/../../secret.png",
				"..%252fsecret.png",
				"evil.png",
				"%65vil.png",
			} {
				response := doGet(traversal)
				if response.Code != http.StatusNotFound {
					t.Fatalf("traversal %q returned %d, want %d", traversal, response.Code, http.StatusNotFound)
				}
				if strings.Contains(response.Body.String(), "top-secret-bytes") {
					t.Fatalf("traversal %q leaked outside bytes", traversal)
				}
			}
		})
	}
}

func TestPostureBrowserUnavailableFailClosed(t *testing.T) {
	config := postureAuthenticatedConfig()
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, config, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	const origin = "http://127.0.0.1:7312"
	cookie := postureLoginCookie(t, handler, origin)
	artifactTarget := "http://127.0.0.1:7312/v1/artifacts/panel/screen.png"
	doArtifact := func(withCookie bool, fetchSite string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, artifactTarget, nil)
		request.Host = "127.0.0.1:7312"
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
	if response := doArtifact(true, "same-origin"); response.Code != http.StatusBadGateway {
		t.Fatalf("unavailable Browser with credentials returned %d, want %d", response.Code, http.StatusBadGateway)
	}
	if response := doArtifact(false, "same-origin"); response.Code != http.StatusUnauthorized {
		t.Fatalf("unavailable Browser without credentials returned %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if response := doArtifact(true, "cross-site"); response.Code != http.StatusUnauthorized {
		t.Fatalf("cross-site unavailable Browser returned %d, want %d", response.Code, http.StatusUnauthorized)
	}
	health := httptest.NewRequest(http.MethodGet, "http://evil.example/health", nil)
	health.Host = "evil.example"
	healthResponse := httptest.NewRecorder()
	handler.ServeHTTP(healthResponse, health)
	if healthResponse.Code != http.StatusOK {
		t.Fatalf("health during Browser outage returned %d, want %d", healthResponse.Code, http.StatusOK)
	}
	closedUpstream, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, controller.AuthConfig{
		Enabled:         true,
		ControllerToken: postureControllerToken,
		MCPToken:        postureMCPToken,
		ControllerPort:  7312,
		PublicOrigin:    "http://127.0.0.1:7312",
		BrowserURL:      "http://127.0.0.1:1",
	}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	unreachable := httptest.NewRequest(http.MethodGet, artifactTarget, nil)
	unreachable.Host = "127.0.0.1:7312"
	unreachable.Header.Set("Sec-Fetch-Site", "same-origin")
	unreachable.AddCookie(cookie)
	unreachableResponse := httptest.NewRecorder()
	closedUpstream.ServeHTTP(unreachableResponse, unreachable)
	if unreachableResponse.Code != http.StatusBadGateway {
		t.Fatalf("unreachable Browser returned %d, want %d", unreachableResponse.Code, http.StatusBadGateway)
	}
}
