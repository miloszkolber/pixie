package controller_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/controller"
)

func TestAuthenticationBindsCookiesToTheConfiguredOrigin(t *testing.T) {
	token := "controller-token-0123456789abcdef0123456789"
	auth, err := controller.NewAuth(token)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := auth.Login("wrong"); ok {
		t.Fatal("invalid controller token was accepted")
	}
	session, ok := auth.Login(token)
	if !ok {
		t.Fatal("valid controller token was rejected")
	}
	expires, ok := auth.SessionExpiresAt(session)
	if !ok || time.Until(expires) < controller.SessionMaxAge-time.Minute || time.Until(expires) > controller.SessionMaxAge+time.Minute {
		t.Fatalf("unexpected session expiry: %v, %v", expires, ok)
	}

	config := controller.AuthConfig{Enabled: true, ControllerToken: token, PublicOrigin: "https://pixie.example"}
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, config, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	login := func(origin, fetchSite string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "https://pixie.example/auth/login", strings.NewReader(`{"token":"`+token+`"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", origin)
		request.Header.Set("Sec-Fetch-Site", fetchSite)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := login("https://untrusted.example", "same-origin"); response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin login returned %d", response.Code)
	}
	if response := login(config.PublicOrigin, "cross-site"); response.Code != http.StatusForbidden {
		t.Fatalf("cross-site login returned %d", response.Code)
	}
	response := login(config.PublicOrigin, "same-origin")
	if response.Code != http.StatusOK {
		t.Fatalf("same-origin login returned %d: %s", response.Code, response.Body.String())
	}
	cookie := response.Header().Get("Set-Cookie")
	for _, attribute := range []string{"HttpOnly", "SameSite=Strict", "Secure", "Path=/"} {
		if !strings.Contains(cookie, attribute) {
			t.Fatalf("session cookie omitted %s: %q", attribute, cookie)
		}
	}
}

func TestHTTPAuthorityIsIndependentFromOriginAndForwardedHeaders(t *testing.T) {
	const token = "controller-token-0123456789abcdef0123456789"
	config := controller.AuthConfig{
		Enabled:         true,
		ControllerToken: token,
		ControllerPort:  7312,
		PublicOrigin:    "https://pixie.example",
	}
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, config, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	request := func(host, origin string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "https://pixie.example/auth/status", nil)
		request.Host = host
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := request("evil.example", config.PublicOrigin, nil); response.Code != http.StatusForbidden {
		t.Fatalf("evil Host with valid Origin returned %d", response.Code)
	}
	if response := request("pixie.example", "", nil); response.Code != http.StatusOK {
		t.Fatalf("approved Host without Origin returned %d", response.Code)
	}
	if response := request("pixie.example", config.PublicOrigin, map[string]string{
		"Forwarded":         "for=192.0.2.1;host=evil.example;proto=http",
		"X-Forwarded-Host":  "evil.example",
		"X-Forwarded-Proto": "http",
	}); response.Code != http.StatusOK {
		t.Fatalf("forwarded headers changed approved authority: %d", response.Code)
	}
	if response := request("evil.example", config.PublicOrigin, map[string]string{
		"Forwarded":         "for=192.0.2.1;host=pixie.example;proto=https",
		"X-Forwarded-Host":  "pixie.example",
		"X-Forwarded-Proto": "https",
	}); response.Code != http.StatusForbidden {
		t.Fatalf("forwarded headers authorized an unapproved Host: %d", response.Code)
	}

	login := func(origin string, forwarded bool) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "https://pixie.example/auth/login", strings.NewReader(`{"token":"`+token+`"}`))
		request.Host = "pixie.example"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", origin)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		if forwarded {
			request.Header.Set("X-Forwarded-Proto", "http")
			request.Header.Set("X-Forwarded-Host", "evil.example")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := login("https://evil.example", false); response.Code != http.StatusForbidden {
		t.Fatalf("mismatched Origin returned %d", response.Code)
	}
	if response := login("", false); response.Code != http.StatusForbidden {
		t.Fatalf("missing login Origin returned %d", response.Code)
	}
	if response := login(config.PublicOrigin, true); response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Set-Cookie"), "Secure") {
		t.Fatalf("forwarded proto changed secure cookie policy: status=%d cookie=%q", response.Code, response.Header().Get("Set-Cookie"))
	}
	plainConfig := controller.AuthConfig{
		Enabled:         true,
		ControllerToken: token,
		ControllerPort:  7312,
		PublicOrigin:    "http://pixie.example",
	}
	plainHandler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, plainConfig, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	plainRequest := httptest.NewRequest(http.MethodPost, "http://pixie.example/auth/login", strings.NewReader(`{"token":"`+token+`"}`))
	plainRequest.Host = "pixie.example"
	plainRequest.Header.Set("Content-Type", "application/json")
	plainRequest.Header.Set("Origin", plainConfig.PublicOrigin)
	plainRequest.Header.Set("Sec-Fetch-Site", "same-origin")
	plainRequest.Header.Set("X-Forwarded-Proto", "https")
	plainResponse := httptest.NewRecorder()
	plainHandler.ServeHTTP(plainResponse, plainRequest)
	if plainResponse.Code != http.StatusOK || strings.Contains(plainResponse.Header().Get("Set-Cookie"), "Secure") {
		t.Fatalf("forwarded proto enabled a Secure cookie: status=%d cookie=%q", plainResponse.Code, plainResponse.Header().Get("Set-Cookie"))
	}
}

func TestHTTPAuthorityRejectsUnapprovedHostBeforeEveryRoute(t *testing.T) {
	const publicOrigin = "https://pixie.example"
	var moduleCalls int
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, controller.AuthConfig{
		ControllerPort: 7312,
		PublicOrigin:   publicOrigin,
	}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	handler.MCPRegistry = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		moduleCalls++
	})
	for _, path := range []string{
		"/",
		"/mcp/objective",
		"/mcp/browser",
		"/api/mcp/modules",
		"/api/mcp/status",
		"/auth/status",
		"/ws",
		"/files/project/image.png",
		"/v1/artifacts/panel/image.png",
	} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, publicOrigin+path, nil)
			request.Host = "evil.example"
			request.Header.Set("Origin", publicOrigin)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("unapproved Host for %s returned %d", path, response.Code)
			}
		})
	}
	if moduleCalls != 0 {
		t.Fatalf("module handler ran before authority validation: %d calls", moduleCalls)
	}
}

func TestHealthRoutesRemainAvailableWithoutApplicationAuthority(t *testing.T) {
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, controller.AuthConfig{}, "", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path   string
		status int
	}{
		{path: "/health", status: http.StatusOK},
		{path: "/livez", status: http.StatusOK},
		{path: "/readyz", status: http.StatusNoContent},
	} {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://evil.example"+test.path, nil)
			request.Host = "evil.example"
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("health route returned %d, want %d", response.Code, test.status)
			}
		})
	}
}

func TestLocalOriginAndCookiePolicyIgnoreForwardedScheme(t *testing.T) {
	config := controller.AuthConfig{ControllerPort: 7312}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7312/auth/login", nil)
	request.Host = "127.0.0.1:7312"
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("Forwarded", "for=192.0.2.1;proto=https")
	request.Header.Set("Origin", "http://127.0.0.1:7312")
	if !config.IsExpectedOrigin(request) || config.SecureCookie(request) {
		t.Fatal("forwarded scheme changed local Origin or cookie policy")
	}
	request.Header.Set("Origin", "https://127.0.0.1:7312")
	if config.IsExpectedOrigin(request) {
		t.Fatal("forwarded scheme authorized an HTTPS Origin on an HTTP request")
	}
}

func TestHTTPSPublicOriginRequiresTLSOrAnExplicitTrustedProxy(t *testing.T) {
	const (
		token        = "controller-token-0123456789abcdef0123456789"
		publicOrigin = "https://pixie.example"
	)
	_, trustedProxy, err := net.ParseCIDR("127.0.0.1/32")
	if err != nil {
		t.Fatal(err)
	}
	base := controller.AuthConfig{
		Enabled:         true,
		ControllerToken: token,
		ControllerPort:  7312,
		PublicOrigin:    publicOrigin,
	}
	trusted := base
	trusted.TrustedProxyCIDRs = []net.IPNet{*trustedProxy}
	newHandler := func(config controller.AuthConfig) *controller.HTTPHandler {
		t.Helper()
		handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, config, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		return handler
	}
	login := func(t *testing.T, handler *controller.HTTPHandler, target, host, remoteAddr, forwardedProto string, forgedForwarding bool) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(`{"token":"`+token+`"}`))
		request.Host = host
		request.RemoteAddr = remoteAddr
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", publicOrigin)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		if forwardedProto != "" {
			request.Header.Set("X-Forwarded-Proto", forwardedProto)
		}
		if forgedForwarding {
			request.Header.Set("Forwarded", "for=192.0.2.1;host=evil.example;proto=http")
			request.Header.Set("X-Forwarded-Host", "evil.example")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	if response := login(t, newHandler(base), "http://127.0.0.1:7312/auth/login", "127.0.0.1:7312", "192.0.2.10:1234", "https", true); response.Code != http.StatusForbidden {
		t.Fatalf("direct HTTP loopback login returned %d", response.Code)
	}
	if response := login(t, newHandler(base), "http://pixie.example/auth/login", "pixie.example", "192.0.2.10:1234", "https", true); response.Code != http.StatusForbidden {
		t.Fatalf("direct HTTP public login returned %d", response.Code)
	}
	if response := login(t, newHandler(base), "https://pixie.example/auth/login", "pixie.example", "192.0.2.10:1234", "", false); response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Set-Cookie"), "Secure") {
		t.Fatalf("direct HTTPS login failed: status=%d cookie=%q", response.Code, response.Header().Get("Set-Cookie"))
	}
	if response := login(t, newHandler(trusted), "http://127.0.0.1:7312/auth/login", "pixie.example", "192.0.2.10:1234", "https", false); response.Code != http.StatusForbidden {
		t.Fatalf("untrusted proxy headers authorized HTTP login: %d", response.Code)
	}
	if response := login(t, newHandler(trusted), "http://127.0.0.1:7312/auth/login", "127.0.0.1:7312", "127.0.0.1:1234", "https", false); response.Code != http.StatusOK {
		t.Fatalf("loopback service exception rejected authenticated HTTP login: %d", response.Code)
	}
	if response := login(t, newHandler(trusted), "http://127.0.0.1:7312/auth/login", "pixie.example", "127.0.0.1:1234", "http", false); response.Code != http.StatusForbidden {
		t.Fatalf("trusted peer with cleartext forwarded scheme authorized login: %d", response.Code)
	}
	_, remoteProxy, err := net.ParseCIDR("192.0.2.0/24")
	if err != nil {
		t.Fatal(err)
	}
	remoteProxyConfig := base
	remoteProxyConfig.TrustedProxyCIDRs = []net.IPNet{*remoteProxy}
	if response := login(t, newHandler(remoteProxyConfig), "http://127.0.0.1:7312/auth/login", "127.0.0.1:7312", "192.0.2.10:1234", "https", false); response.Code != http.StatusForbidden {
		t.Fatalf("trusted proxy without Host rewriting authorized HTTP login: %d", response.Code)
	}
	if response := login(t, newHandler(trusted), "http://127.0.0.1:7312/auth/login", "pixie.example", "127.0.0.1:1234", "https", true); response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Set-Cookie"), "Secure") {
		t.Fatalf("approved proxy Host rewrite failed: status=%d cookie=%q", response.Code, response.Header().Get("Set-Cookie"))
	}
}

func TestLoopbackServiceTransportExceptionPreservesObjectiveAuthorization(t *testing.T) {
	config := controller.AuthConfig{ControllerPort: 7312, PublicOrigin: "https://pixie.example"}
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{Sessions: &controller.SessionManager{}}, nil, nil, config, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		host       string
		remoteAddr string
		status     int
	}{
		{name: "IPv4 loopback", host: "127.0.0.1:7312", remoteAddr: "127.0.0.1:41000", status: http.StatusUnauthorized},
		{name: "localhost", host: "localhost:7312", remoteAddr: "127.0.0.1:41001", status: http.StatusUnauthorized},
		{name: "IPv6 loopback", host: "[::1]:7312", remoteAddr: "[::1]:41002", status: http.StatusUnauthorized},
		{name: "loopback Host without listener port", host: "localhost", remoteAddr: "127.0.0.1:41003", status: http.StatusForbidden},
		{name: "remote peer forged loopback Host", host: "127.0.0.1:7312", remoteAddr: "192.0.2.10:41004", status: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7312/mcp/objective", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
			request.Host = test.host
			request.RemoteAddr = test.remoteAddr
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer invalid-objective-token")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("objective request returned %d, want %d: %s", response.Code, test.status, response.Body.String())
			}
		})
	}
}

func TestTrustedProxyRequiresControllerAuthAtHTTPAdmission(t *testing.T) {
	_, trustedProxy, err := net.ParseCIDR("192.0.2.0/24")
	if err != nil {
		t.Fatal(err)
	}
	trustedWithoutAuth := controller.AuthConfig{
		ControllerPort:    7312,
		PublicOrigin:      "https://pixie.example",
		TrustedProxyCIDRs: []net.IPNet{*trustedProxy},
	}
	trustedHandler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, trustedWithoutAuth, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/auth/status"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "https://pixie.example"+path, nil)
			request.Host = "pixie.example"
			request.RemoteAddr = "192.0.2.10:41000"
			response := httptest.NewRecorder()
			trustedHandler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("unauthenticated trusted-proxy HTTP request returned %d", response.Code)
			}
		})
	}

	lanWithoutAuth := controller.AuthConfig{ControllerPort: 7312, PublicOrigin: "http://pixie.example", AllowRemoteWithout: true}
	lanHandler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, lanWithoutAuth, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://pixie.example/auth/status", nil)
	request.Host = "pixie.example"
	request.RemoteAddr = "192.0.2.10:41001"
	response := httptest.NewRecorder()
	lanHandler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unauthenticated LAN HTTP request returned %d, want %d", response.Code, http.StatusOK)
	}
}

func TestAuthorityNormalizesLocalHostsAndDefaultPorts(t *testing.T) {
	config := controller.AuthConfig{ControllerPort: 7312, PublicOrigin: "https://Public.Example:443"}
	for _, test := range []struct {
		name   string
		host   string
		origin string
		allow  bool
		local  bool
	}{
		{name: "public hostname", host: "public.example", origin: "https://public.example", allow: true},
		{name: "public default port", host: "PUBLIC.EXAMPLE:443", origin: "https://public.example:443", allow: true},
		{name: "wrong public port", host: "public.example:7312", origin: "https://public.example", allow: false},
		{name: "loopback IPv4", host: "127.0.0.1:7312", origin: "http://127.0.0.1:7312", allow: true, local: true},
		{name: "loopback localhost", host: "LOCALHOST:7312", origin: "http://localhost:7312", allow: true, local: true},
		{name: "loopback omitted port at non-default listener", host: "LOCALHOST", origin: "http://localhost:7312", allow: false, local: true},
		{name: "loopback IPv6", host: "[0:0:0:0:0:0:0:1]:7312", origin: "http://[::1]:7312", allow: true, local: true},
		{name: "loopback wrong port", host: "[::1]:443", origin: "http://[::1]:7312", allow: false, local: true},
		{name: "bracketed IPv4 host", host: "[127.0.0.1]:7312", origin: "http://127.0.0.1:7312", allow: false, local: true},
		{name: "bracketed DNS host", host: "[localhost]:7312", origin: "http://localhost:7312", allow: false, local: true},
		{name: "unapproved DNS", host: "loopback.example:7312", origin: "https://public.example", allow: false},
		{name: "malformed port", host: "127.0.0.1:not-a-port", origin: "http://127.0.0.1:7312", allow: false, local: true},
		{name: "unbracketed IPv6 port", host: "::1:7312", origin: "http://[::1]:7312", allow: false, local: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := config
			if test.local {
				policy = controller.AuthConfig{ControllerPort: 7312}
			}
			target := "http://unused/"
			if !test.local {
				target = "https://unused/"
			}
			request := httptest.NewRequest(http.MethodGet, target, nil)
			request.Host = test.host
			request.Header.Set("Origin", test.origin)
			if actual := policy.IsAllowedAuthority(request); actual != test.allow {
				t.Fatalf("Host %q allowed=%v, want %v", test.host, actual, test.allow)
			}
			if test.allow && !policy.IsExpectedOrigin(request) {
				t.Fatalf("normalized Origin %q was rejected for Host %q", test.origin, test.host)
			}
		})
	}
}

func TestAuthorityAllowsOmittedLocalPortOnlyAtDefaultListenerPorts(t *testing.T) {
	for _, test := range []struct {
		name string
		port int
		host string
		want bool
	}{
		{name: "HTTP default", port: 80, host: "localhost", want: true},
		{name: "HTTPS default", port: 443, host: "127.0.0.1", want: true},
		{name: "non-default", port: 7312, host: "::1", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := controller.AuthConfig{ControllerPort: test.port}
			request := httptest.NewRequest(http.MethodGet, "http://unused/", nil)
			request.Host = test.host
			if actual := config.IsAllowedAuthority(request); actual != test.want {
				t.Fatalf("listener port %d, Host %q allowed=%v, want %v", test.port, test.host, actual, test.want)
			}
		})
	}
}

func TestLoopbackServiceTransportUsesEquivalentDefaultPorts(t *testing.T) {
	for _, test := range []struct {
		name       string
		port       int
		host       string
		remoteAddr string
		want       bool
	}{
		{name: "HTTP default", port: 80, host: "localhost", remoteAddr: "127.0.0.1:41000", want: true},
		{name: "HTTPS default", port: 443, host: "127.0.0.1", remoteAddr: "127.0.0.1:41001", want: true},
		{name: "non-default omitted port", port: 7312, host: "localhost", remoteAddr: "127.0.0.1:41002", want: false},
		{name: "non-default explicit port", port: 7312, host: "localhost:7312", remoteAddr: "127.0.0.1:41003", want: true},
		{name: "wrong explicit port", port: 80, host: "localhost:7312", remoteAddr: "127.0.0.1:41004", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := controller.AuthConfig{ControllerPort: test.port, PublicOrigin: "https://pixie.example"}
			request := httptest.NewRequest(http.MethodGet, "http://unused/", nil)
			request.Host = test.host
			request.RemoteAddr = test.remoteAddr
			if actual := config.IsAllowedTransport(request); actual != test.want {
				t.Fatalf("listener port %d, Host %q, RemoteAddr %q allowed=%v, want %v", test.port, test.host, test.remoteAddr, actual, test.want)
			}
		})
	}
}

func TestReadAuthConfigUsesTheControllerPortDefaultForLocalAuthority(t *testing.T) {
	config, err := controller.ReadAuthConfig(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7312/auth/status", nil)
	if !config.IsAllowedAuthority(request) {
		t.Fatalf("default local authority was rejected: %#v", config)
	}
}

func TestRuntimeOverridesRevalidateEffectiveControllerBind(t *testing.T) {
	const token = "controller-token-0123456789abcdef0123456789"
	for _, test := range []struct {
		name   string
		config controller.RuntimeConfig
		env    map[string]string
	}{
		{name: "remote override without public origin", config: controller.RuntimeConfig{Host: "0.0.0.0", Port: 7312}},
		{name: "remote override without auth policy", config: controller.RuntimeConfig{Host: "0.0.0.0", Port: 7312}, env: map[string]string{"PIXIE_PUBLIC_ORIGIN": "http://pixie.example"}},
		{name: "remote override with auth but no public origin", config: controller.RuntimeConfig{Host: "::", Port: 7312}, env: map[string]string{"PIXIE_AUTH_ENABLED": "true", "PIXIE_TOKEN": token}},
		{name: "invalid effective host", config: controller.RuntimeConfig{Host: "evil.example", Port: 7312}},
		{name: "invalid effective port", config: controller.RuntimeConfig{Host: "127.0.0.1", Port: 65536}},
		{name: "negative effective port", config: controller.RuntimeConfig{Host: "127.0.0.1", Port: -1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			getenv := func(key string) string { return test.env[key] }
			test.config.Getenv = getenv
			if _, err := controller.NewRuntime(test.config); err == nil {
				t.Fatal("runtime accepted an invalid effective controller bind")
			}
		})
	}
}

func TestAuthenticatedControllerRequiresMCPPublisherToken(t *testing.T) {
	const controllerToken = "controller-token-0123456789abcdef0123456789"
	for _, test := range []struct {
		name  string
		value string
	}{
		{name: "absent", value: ""},
		{name: "weak", value: "short"},
	} {
		t.Run(test.name, func(t *testing.T) {
			values := map[string]string{
				"PIXIE_AUTH_ENABLED": "true",
				"PIXIE_TOKEN":        controllerToken,
				"PIXIE_MCP_TOKEN":    test.value,
			}
			if _, err := controller.ReadAuthConfig(func(key string) string { return values[key] }); err == nil {
				t.Fatal("authenticated controller accepted an unusable MCP publisher token")
			}
		})
	}
	local, err := controller.ReadAuthConfig(func(string) string { return "" })
	if err != nil || local.MCPToken != "" {
		t.Fatalf("unauthenticated loopback policy changed: config=%#v err=%v", local, err)
	}
}

func TestAuthenticatedRuntimeRejectsMCPPublisherWithoutToken(t *testing.T) {
	const controllerToken = "controller-token-0123456789abcdef0123456789"
	_, err := controller.NewRuntime(controller.RuntimeConfig{
		Host: "127.0.0.1", Port: controller.DefaultControllerPort,
		Getenv: func(key string) string {
			if key == "PIXIE_AUTH_ENABLED" {
				return "true"
			}
			if key == "PIXIE_TOKEN" {
				return controllerToken
			}
			return ""
		},
	})
	if err == nil || !strings.Contains(err.Error(), "PIXIE_MCP_TOKEN") {
		t.Fatalf("authenticated runtime accepted missing MCP token: %v", err)
	}
}

func TestMCPPublisherRoutesFailClosedWithoutItsToken(t *testing.T) {
	const controllerToken = "controller-token-0123456789abcdef0123456789"
	for _, test := range []struct {
		name     string
		mcpToken string
	}{
		{name: "absent"},
		{name: "weak", mcpToken: "short"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, controller.AuthConfig{
				Enabled: true, ControllerToken: controllerToken, MCPToken: test.mcpToken,
				ControllerPort: 7312, PublicOrigin: "http://127.0.0.1:7312",
			}, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			var calls int
			handler.MCPRegistry = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ })
			for _, path := range []string{"/mcp/browser", "/api/mcp/modules", "/api/mcp/status"} {
				request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7312"+path, nil)
				request.Host = "127.0.0.1:7312"
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code < http.StatusBadRequest {
					t.Fatalf("%s returned success without MCP token: %d", path, response.Code)
				}
			}
			if calls != 0 {
				t.Fatalf("MCP publisher received unauthenticated requests: %d", calls)
			}
		})
	}
	validToken := "mcp-token-0123456789abcdef0123456789"
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, controller.AuthConfig{
		Enabled: true, ControllerToken: controllerToken, MCPToken: validToken,
		ControllerPort: 7312, PublicOrigin: "http://127.0.0.1:7312",
	}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	handler.MCPRegistry = http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		calls++
		response.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7312/api/mcp/status", nil)
	request.Host = "127.0.0.1:7312"
	request.Header.Set("Authorization", "Bearer "+validToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || calls != 1 {
		t.Fatalf("valid MCP token was not admitted: status=%d calls=%d", response.Code, calls)
	}
}

func TestRemoteAndBrowserConfigurationFailsClosed(t *testing.T) {
	token := "browser-token-0123456789abcdef0123456789"
	for _, test := range []struct {
		name   string
		values map[string]string
		valid  bool
	}{
		{name: "loopback defaults", values: map[string]string{}, valid: true},
		{name: "unauthenticated remote controller", values: map[string]string{"PIXIE_CONTROLLER_HOST": "0.0.0.0"}},
		{name: "unauthenticated remote controller with public origin", values: map[string]string{
			"PIXIE_CONTROLLER_HOST": "0.0.0.0", "PIXIE_PUBLIC_ORIGIN": "https://pixie.example",
		}},
		{name: "authenticated remote controller without public origin", values: map[string]string{
			"PIXIE_CONTROLLER_HOST": "0.0.0.0", "PIXIE_AUTH_ENABLED": "true", "PIXIE_TOKEN": "controller-token-0123456789abcdef0123456789",
		}},
		{name: "authenticated remote controller", values: map[string]string{
			"PIXIE_CONTROLLER_HOST": "0.0.0.0", "PIXIE_AUTH_ENABLED": "true", "PIXIE_TOKEN": "controller-token-0123456789abcdef0123456789", "PIXIE_MCP_TOKEN": "mcp-token-0123456789abcdef0123456789", "PIXIE_PUBLIC_ORIGIN": "https://pixie.example",
		}, valid: true},
		{name: "unauthenticated remote controller with explicit policy", values: map[string]string{
			"PIXIE_CONTROLLER_HOST": "0.0.0.0", "PIXIE_ALLOW_UNAUTHENTICATED_REMOTE": "true", "PIXIE_MCP_TOKEN": "mcp-token-0123456789abcdef0123456789", "PIXIE_PUBLIC_ORIGIN": "http://pixie.example",
		}, valid: true},
		{name: "unauthenticated remote browser", values: map[string]string{"PIXIE_BROWSER_URL": "http://browser:8787"}},
		{name: "browser URL with credentials", values: map[string]string{
			"PIXIE_BROWSER_AUTH": "true", "PIXIE_BROWSER_TOKEN": token, "PIXIE_BROWSER_URL": "http://user:secret@browser:8787",
		}},
		{name: "same application and sandbox origin", values: map[string]string{
			"PIXIE_BROWSER_AUTH": "true", "PIXIE_BROWSER_TOKEN": token,
			"PIXIE_PUBLIC_ORIGIN": "https://same.example", "PIXIE_BROWSER_PUBLIC_ORIGIN": "https://same.example",
		}},
		{name: "public origin with query delimiter", values: map[string]string{
			"PIXIE_PUBLIC_ORIGIN": "https://pixie.example?",
		}},
		{name: "public origin with fragment delimiter", values: map[string]string{
			"PIXIE_PUBLIC_ORIGIN": "https://pixie.example#",
		}},
		{name: "public origin with bracketed DNS", values: map[string]string{
			"PIXIE_PUBLIC_ORIGIN": "https://[pixie.example]",
		}},
		{name: "public origin with bracketed IPv4", values: map[string]string{
			"PIXIE_PUBLIC_ORIGIN": "https://[127.0.0.1]:7312",
		}},
		{name: "invalid trusted proxy", values: map[string]string{
			"PIXIE_TRUSTED_PROXY_CIDRS": "not-an-ip",
		}},
		{name: "unspecified trusted proxy", values: map[string]string{
			"PIXIE_TRUSTED_PROXY_CIDRS": "0.0.0.0/0",
		}},
		{name: "unauthenticated trusted proxy", values: map[string]string{
			"PIXIE_TRUSTED_PROXY_CIDRS": "127.0.0.1/32,::1",
		}},
		{name: "unauthenticated trusted proxy with LAN policy", values: map[string]string{
			"PIXIE_CONTROLLER_HOST": "0.0.0.0", "PIXIE_ALLOW_UNAUTHENTICATED_REMOTE": "true", "PIXIE_PUBLIC_ORIGIN": "https://pixie.example",
			"PIXIE_TRUSTED_PROXY_CIDRS": "192.0.2.0/24",
		}},
		{name: "authenticated trusted proxy", values: map[string]string{
			"PIXIE_CONTROLLER_HOST": "0.0.0.0", "PIXIE_AUTH_ENABLED": "true", "PIXIE_TOKEN": "controller-token-0123456789abcdef0123456789", "PIXIE_MCP_TOKEN": "mcp-token-0123456789abcdef0123456789", "PIXIE_PUBLIC_ORIGIN": "https://pixie.example",
			"PIXIE_TRUSTED_PROXY_CIDRS": "192.0.2.0/24",
		}, valid: true},
		{name: "isolated authenticated sandbox", values: map[string]string{
			"PIXIE_BROWSER_AUTH": "true", "PIXIE_BROWSER_TOKEN": token,
			"PIXIE_PUBLIC_ORIGIN": "https://pixie.example:443", "PIXIE_BROWSER_PUBLIC_ORIGIN": "https://sandbox.example:443",
		}, valid: true},
		{name: "same controller and MCP token", values: map[string]string{
			"PIXIE_AUTH_ENABLED": "true", "PIXIE_TOKEN": token, "PIXIE_MCP_TOKEN": token,
		}},
		{name: "weak MCP token", values: map[string]string{"PIXIE_MCP_TOKEN": "short"}},
		{name: "distinct MCP token", values: map[string]string{
			"PIXIE_AUTH_ENABLED": "true", "PIXIE_TOKEN": "controller-token-0123456789abcdef0123456789",
			"PIXIE_MCP_TOKEN": "mcp-token-0123456789abcdef0123456789",
		}, valid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := controller.ReadAuthConfig(func(key string) string { return test.values[key] })
			if (err == nil) != test.valid {
				t.Fatalf("configuration %#v: %v", config, err)
			}
			if test.valid && strings.HasSuffix(config.BrowserURL, "/") {
				t.Fatalf("browser origin was not normalized: %q", config.BrowserURL)
			}
		})
	}
}
