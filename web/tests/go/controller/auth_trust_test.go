package controller_test

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/controller"
)

// AUX-02 regression coverage for the local-request trust boundary. The tests
// exercise the existing HMAC session token, Host authority and PublicOrigin
// handling against DNS-rebinding and cross-site write attempts. They are
// intentionally implementation-independent: a legitimate refactor of
// auth.go must keep every case below.

const trustControllerToken = "controller-token-0123456789abcdef0123456789"

func trustSession(t *testing.T, auth *controller.Auth) string {
	t.Helper()
	session, ok := auth.Login(trustControllerToken)
	if !ok {
		t.Fatal("valid controller token was rejected")
	}
	return session
}

// TestRebindingStyleRequestIsRejectedWithoutUnauthenticatedTrust models DNS
// rebinding: an attacker page uses a name that resolves to loopback, so the
// peer address is 127.0.0.1 while Host and Origin name the attacker. Only the
// literal loopback authorities are trusted, so the request is denied.
func TestRebindingStyleRequestIsRejectedWithoutUnauthenticatedTrust(t *testing.T) {
	config := controller.AuthConfig{ControllerPort: 7312}
	request := httptest.NewRequest(http.MethodPost, "http://attacker.example:7312/auth/status", nil)
	request.Host = "attacker.example:7312"
	request.RemoteAddr = "127.0.0.1:51000"
	request.Header.Set("Origin", "http://attacker.example:7312")
	request.Header.Set("Sec-Fetch-Site", "same-origin")

	if config.IsAllowedAuthority(request) {
		t.Fatal("rebinding Host authority was admitted")
	}
	if config.IsExpectedOrigin(request) {
		t.Fatal("rebinding Origin was admitted")
	}
	if config.IsAuthorizedHTTPRequest(request, nil) {
		t.Fatal("rebinding-style request was authorized")
	}

	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, config, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("rebinding-style request returned %d, want %d", response.Code, http.StatusForbidden)
	}
}

// TestNonLoopbackHostIsRejectedWheneverUnauthenticatedTrustIsOff covers the
// unauthenticated loopback policy: a non-loopback authority is refused even
// when its name resolves to loopback, while the literal loopback authority for
// the configured listener port is admitted. The peer-address dimension of the
// same gap is covered by the strict Host check in auth_hardening_test.go.
func TestNonLoopbackHostIsRejectedWheneverUnauthenticatedTrustIsOff(t *testing.T) {
	config := controller.AuthConfig{ControllerPort: 7312}
	for _, test := range []struct {
		name       string
		host       string
		remoteAddr string
		allow      bool
	}{
		{name: "loopback IPv4", host: "127.0.0.1:7312", remoteAddr: "127.0.0.1:51001", allow: true},
		{name: "localhost", host: "localhost:7312", remoteAddr: "127.0.0.1:51002", allow: true},
		{name: "loopback IPv6", host: "[::1]:7312", remoteAddr: "[::1]:51003", allow: true},
		{name: "non-loopback DNS", host: "pixie.example:7312", remoteAddr: "192.0.2.10:51004"},
		{name: "attacker DNS resolving to loopback", host: "attacker.example:7312", remoteAddr: "127.0.0.1:51005"},
		{name: "wrong listener port", host: "127.0.0.1:9999", remoteAddr: "127.0.0.1:51007"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://unused/auth/status", nil)
			request.Host = test.host
			request.RemoteAddr = test.remoteAddr
			if actual := config.IsAllowedAuthority(request); actual != test.allow {
				t.Fatalf("Host %q from %q allowed=%v, want %v", test.host, test.remoteAddr, actual, test.allow)
			}
		})
	}
}

// TestCrossSiteWritesAreRejectedWithoutUnauthenticatedTrust proves that a
// request carrying a loopback Host is still refused when either Fetch Metadata
// or Origin shows a cross-site origin. The Fetch Metadata requirement applies
// even when the Origin header is omitted.
func TestCrossSiteWritesAreRejectedWithoutUnauthenticatedTrust(t *testing.T) {
	config := controller.AuthConfig{ControllerPort: 7312}
	request := func(origin, fetchSite string) *http.Request {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7312/auth/status", nil)
		request.Host = "127.0.0.1:7312"
		request.RemoteAddr = "127.0.0.1:51010"
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if fetchSite != "" {
			request.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		return request
	}
	if !config.IsAuthorizedHTTPRequest(request("http://127.0.0.1:7312", "same-origin"), nil) {
		t.Fatal("same-origin loopback request was rejected")
	}
	for _, test := range []struct {
		name      string
		origin    string
		fetchSite string
	}{
		{name: "cross-site Origin", origin: "http://evil.example", fetchSite: "same-origin"},
		{name: "cross-site Fetch Metadata", origin: "", fetchSite: "cross-site"},
		{name: "same-site Fetch Metadata", origin: "", fetchSite: "same-site"},
		{name: "missing Fetch Metadata", origin: "", fetchSite: ""},
		{name: "multiple Origins", origin: "http://127.0.0.1:7312,http://evil.example", fetchSite: "same-origin"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if config.IsAuthorizedHTTPRequest(request(test.origin, test.fetchSite), nil) {
				t.Fatalf("%s was authorized", test.name)
			}
		})
	}
}

// TestAuthenticatedRemoteAccessRequiresTheExactPublicOrigin checks that
// authentication never relaxes the PublicOrigin rule: a valid session still
// needs the configured public authority, an exact Origin, and an approved
// transport.
func TestAuthenticatedRemoteAccessRequiresTheExactPublicOrigin(t *testing.T) {
	const publicOrigin = "https://pixie.example"
	auth, err := controller.NewAuth(trustControllerToken)
	if err != nil {
		t.Fatal(err)
	}
	session := trustSession(t, auth)
	config := controller.AuthConfig{
		Enabled:         true,
		ControllerToken: trustControllerToken,
		ControllerPort:  7312,
		PublicOrigin:    publicOrigin,
	}
	request := func(host, origin, fetchSite, cookie string, secure bool) *http.Request {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, publicOrigin+"/auth/status", nil)
		request.Host = host
		request.RemoteAddr = "192.0.2.10:52000"
		if secure {
			request.TLS = &tls.ConnectionState{}
		} else {
			// httptest sets a dummy TLS state for an https target; clear it to
			// model a real cleartext request to an HTTPS public origin.
			request.TLS = nil
		}
		request.Header.Set("Content-Type", "application/json")
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if fetchSite != "" {
			request.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		if cookie != "" {
			request.AddCookie(&http.Cookie{Name: controller.AuthCookieName, Value: cookie})
		}
		return request
	}
	if !config.IsAuthorizedHTTPRequest(request("pixie.example", publicOrigin, "same-origin", session, true), auth) {
		t.Fatal("exact public origin with a valid session was rejected")
	}
	if !config.IsExpectedOrigin(request("pixie.example", publicOrigin, "same-origin", session, true)) {
		t.Fatal("exact public origin was not recognized")
	}
	for _, test := range []struct {
		name      string
		host      string
		origin    string
		fetchSite string
		cookie    string
		tls       bool
	}{
		{name: "unapproved Host", host: "evil.example", origin: publicOrigin, fetchSite: "same-origin", cookie: session, tls: true},
		{name: "cross-site Origin", host: "pixie.example", origin: "https://evil.example", fetchSite: "same-origin", cookie: session, tls: true},
		{name: "cross-site Fetch Metadata", host: "pixie.example", origin: publicOrigin, fetchSite: "cross-site", cookie: session, tls: true},
		{name: "missing session", host: "pixie.example", origin: publicOrigin, fetchSite: "same-origin", tls: true},
		{name: "cleartext request to HTTPS origin", host: "pixie.example", origin: publicOrigin, fetchSite: "same-origin", cookie: session},
	} {
		t.Run(test.name, func(t *testing.T) {
			if config.IsAuthorizedHTTPRequest(request(test.host, test.origin, test.fetchSite, test.cookie, test.tls), auth) {
				t.Fatalf("%s was authorized", test.name)
			}
		})
	}

	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, config, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	helper := request("pixie.example", publicOrigin, "same-origin", session, true)
	helper.Method = http.MethodGet
	helper.URL.Path = "/auth/status"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, helper)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated status request returned %d: %s", response.Code, response.Body.String())
	}
	forged := request("evil.example", publicOrigin, "same-origin", session, true)
	forged.Method = http.MethodGet
	forged.URL.Path = "/auth/status"
	forgedResponse := httptest.NewRecorder()
	handler.ServeHTTP(forgedResponse, forged)
	if forgedResponse.Code != http.StatusForbidden {
		t.Fatalf("unapproved Host for authenticated status returned %d, want %d", forgedResponse.Code, http.StatusForbidden)
	}
}

// TestSessionTokenIsHMACBoundToTheControllerToken proves the session cookie
// carries an HMAC over its expiry: a token from another controller, a changed
// expiry and a changed signature are all rejected.
func TestSessionTokenIsHMACBoundToTheControllerToken(t *testing.T) {
	auth, err := controller.NewAuth(trustControllerToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, nearMiss := range []string{
		trustControllerToken[:len(trustControllerToken)-1],
		trustControllerToken + "x",
		"x" + trustControllerToken[1:],
		strings.ToUpper(trustControllerToken),
		"",
	} {
		if _, ok := auth.Login(nearMiss); ok {
			t.Fatalf("near-miss token %q was accepted", nearMiss)
		}
	}
	session := trustSession(t, auth)
	if _, ok := auth.SessionExpiresAt(session); !ok {
		t.Fatal("fresh session was rejected")
	}
	other, err := controller.NewAuth("other-controller-token-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := other.SessionExpiresAt(session); ok {
		t.Fatal("session signed by another token was accepted")
	}
	parts := strings.SplitN(session, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected session format %q", session)
	}
	if _, ok := auth.SessionExpiresAt(replaceFirstRune(parts[0] + "." + parts[1])); ok {
		t.Fatal("tampered expiry was accepted")
	}
	tamperedSignature := parts[0] + "." + replaceFirstRune(parts[1])
	if _, ok := auth.SessionExpiresAt(tamperedSignature); ok {
		t.Fatal("tampered signature was accepted")
	}
	if _, ok := auth.SessionExpiresAt("not-a-session"); ok {
		t.Fatal("malformed session was accepted")
	}
	expires, ok := auth.SessionExpiresAt(session)
	if !ok || time.Until(expires) <= 0 {
		t.Fatalf("session expiry is not in the future: %v, %v", expires, ok)
	}
}

func replaceFirstRune(value string) string {
	if value == "" {
		return value
	}
	replacement := byte('0')
	if value[0] == '0' {
		replacement = '1'
	}
	return string(replacement) + value[1:]
}

// TestControllerHostConfigurationFailsClosed checks the ControllerHost and
// PublicOrigin configuration rules: an arbitrary hostname is never accepted,
// and a non-loopback bind needs both a public origin and an authentication
// decision.
func TestControllerHostConfigurationFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		values map[string]string
		valid  bool
	}{
		{name: "arbitrary controller host", values: map[string]string{"PIXIE_CONTROLLER_HOST": "pixie.example"}},
		{name: "non-loopback without origin", values: map[string]string{"PIXIE_CONTROLLER_HOST": "0.0.0.0"}},
		{name: "non-loopback without auth policy", values: map[string]string{"PIXIE_CONTROLLER_HOST": "0.0.0.0", "PIXIE_PUBLIC_ORIGIN": "https://pixie.example"}},
		{name: "loopback default", values: map[string]string{}, valid: true},
		{name: "non-loopback with explicit policy", values: map[string]string{
			"PIXIE_CONTROLLER_HOST": "0.0.0.0", "PIXIE_ALLOW_UNAUTHENTICATED_REMOTE": "true",
			"PIXIE_PUBLIC_ORIGIN": "https://pixie.example", "PIXIE_MCP_TOKEN": "mcp-token-0123456789abcdef0123456789",
		}, valid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := controller.ReadAuthConfig(func(key string) string { return test.values[key] })
			if (err == nil) != test.valid {
				t.Fatalf("configuration %#v returned %v", test.values, err)
			}
		})
	}
}
