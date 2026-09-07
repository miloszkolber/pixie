package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	AuthCookieName      = "pixie_auth"
	SessionMaxAge       = 90 * 24 * time.Hour
	maxAuthHeaderLength = 4096
	maxOriginLength     = 512
	maxHostLength       = 255
)

type Auth struct {
	token string
	now   func() time.Time
}

func NewAuth(token string) (*Auth, error) {
	if token == "" {
		return nil, fmt.Errorf("PIXIE_TOKEN is required")
	}
	return &Auth{token: token, now: time.Now}, nil
}

func (a *Auth) Login(candidate string) (string, bool) {
	if !constantTimeStringEqual(a.token, candidate) {
		return "", false
	}
	expires := a.now().Add(SessionMaxAge).Unix()
	return a.cookieFor(expires), true
}

func (a *Auth) SessionExpiresAt(session string) (time.Time, bool) {
	parts := strings.Split(session, ".")
	if len(parts) != 2 || len(parts[0]) < 1 || len(parts[0]) > 16 || len(parts[1]) != 43 {
		return time.Time{}, false
	}
	expires, err := strconv.ParseInt(parts[0], 36, 64)
	if err != nil || !constantTimeStringEqual(a.cookieFor(expires), session) {
		return time.Time{}, false
	}
	result := time.Unix(expires, 0)
	return result, result.After(a.now())
}

func (a *Auth) cookieFor(expires int64) string {
	encoded := strconv.FormatInt(expires, 36)
	digest := hmac.New(sha256.New, []byte(a.token))
	digest.Write([]byte("pixie-controller-cookie-v1\x00" + encoded))
	signature := base64.RawURLEncoding.EncodeToString(digest.Sum(nil))
	return encoded + "." + signature
}

func SessionCookie(value string, secure bool) string {
	result := fmt.Sprintf("%s=%s; Max-Age=%d; Path=/; HttpOnly; SameSite=Strict", AuthCookieName, value, int(SessionMaxAge.Seconds()))
	if secure {
		result += "; Secure"
	}
	return result
}

func ExpiredSessionCookie(secure bool) string {
	result := AuthCookieName + "=; Max-Age=0; Path=/; HttpOnly; SameSite=Strict"
	if secure {
		result += "; Secure"
	}
	return result
}

type AuthConfig struct {
	Enabled             bool
	BrowserEnabled      bool
	ControllerToken     string
	BrowserToken        string
	BrowserURL          string
	BrowserPublicOrigin string
	MCPURL              string
	MCPToken            string
	ControllerHost      string
	PublicOrigin        string
	AllowRemoteWithout  bool
}

// BrowserServiceAuth selects the credential for controller-owned requests to
// the Browser HTTP surface. The MCP host is the canonical Browser surface, so
// its token is authoritative when it owns that origin. Browser-specific fields
// remain supported for compatibility with older deployments.
func (c AuthConfig) BrowserServiceAuth() (bool, string) {
	if c.MCPURL != "" && sameAppViewOrigin(c.MCPURL, c.BrowserURL) {
		return c.MCPToken != "", c.MCPToken
	}
	return c.BrowserEnabled, c.BrowserToken
}

func ReadAuthConfig(getenv func(string) string) (AuthConfig, error) {
	enabled, err := strictBool(getenv("PIXIE_AUTH_ENABLED"), false, "PIXIE_AUTH_ENABLED")
	if err != nil {
		return AuthConfig{}, err
	}
	browserEnabled, err := strictBool(getenv("PIXIE_BROWSER_AUTH"), false, "PIXIE_BROWSER_AUTH")
	if err != nil {
		return AuthConfig{}, err
	}
	controllerToken := strings.TrimSpace(getenv("PIXIE_TOKEN"))
	browserToken := strings.TrimSpace(getenv("PIXIE_BROWSER_TOKEN"))
	if enabled && !strongToken(controllerToken) {
		return AuthConfig{}, fmt.Errorf("PIXIE_TOKEN must be a strong printable random token")
	}
	if browserEnabled && !strongToken(browserToken) {
		return AuthConfig{}, fmt.Errorf("PIXIE_BROWSER_TOKEN must be a strong printable random token")
	}
	if enabled && browserEnabled && constantTimeStringEqual(controllerToken, browserToken) {
		return AuthConfig{}, fmt.Errorf("PIXIE_TOKEN and PIXIE_BROWSER_TOKEN must be different")
	}
	if strings.TrimSpace(getenv("PIXIE_ALLOWED_ORIGINS")) != "" {
		return AuthConfig{}, fmt.Errorf("PIXIE_ALLOWED_ORIGINS is unsupported with cookie authentication. Use PIXIE_PUBLIC_ORIGIN")
	}
	host := strings.TrimSpace(getenv("PIXIE_CONTROLLER_HOST"))
	if host == "" {
		host = "127.0.0.1"
	}
	if !map[string]bool{"127.0.0.1": true, "::1": true, "0.0.0.0": true, "::": true}[host] {
		return AuthConfig{}, fmt.Errorf("PIXIE_CONTROLLER_HOST must be exactly 127.0.0.1, ::1, 0.0.0.0, or ::")
	}
	allowRemote, err := strictBool(getenv("PIXIE_ALLOW_UNAUTHENTICATED_REMOTE"), false, "PIXIE_ALLOW_UNAUTHENTICATED_REMOTE")
	if err != nil {
		return AuthConfig{}, err
	}
	if host != "127.0.0.1" && host != "::1" && !enabled && !allowRemote {
		return AuthConfig{}, fmt.Errorf("a non-loopback PIXIE_CONTROLLER_HOST requires controller authentication or explicit PIXIE_ALLOW_UNAUTHENTICATED_REMOTE=true")
	}
	publicOrigin := strings.TrimSpace(getenv("PIXIE_PUBLIC_ORIGIN"))
	if publicOrigin != "" {
		publicOrigin, err = normalizeOrigin(publicOrigin)
		if err != nil {
			return AuthConfig{}, fmt.Errorf("PIXIE_PUBLIC_ORIGIN must be an absolute http(s) origin without a path")
		}
	}
	mcpURL := strings.TrimSpace(getenv("PIXIE_MCP_URL"))
	mcpToken := strings.TrimSpace(getenv("PIXIE_MCP_TOKEN"))
	if mcpURL != "" {
		mcpURL, err = normalizeOrigin(mcpURL)
		if err != nil {
			return AuthConfig{}, fmt.Errorf("PIXIE_MCP_URL must be an absolute http(s) origin without credentials or a path")
		}
		if mcpToken != "" && !strongToken(mcpToken) {
			return AuthConfig{}, fmt.Errorf("PIXIE_MCP_TOKEN must be a strong printable random token")
		}
		parsedMCP, _ := url.Parse(mcpURL)
		if parsedMCP.Hostname() != "localhost" && !net.ParseIP(parsedMCP.Hostname()).IsLoopback() && !strongToken(mcpToken) {
			return AuthConfig{}, fmt.Errorf("a non-loopback PIXIE_MCP_URL requires a strong MCP token")
		}
	} else if mcpToken != "" && !strongToken(mcpToken) {
		return AuthConfig{}, fmt.Errorf("PIXIE_MCP_TOKEN must be a strong printable random token")
	}
	if enabled && mcpToken != "" && constantTimeStringEqual(controllerToken, mcpToken) {
		return AuthConfig{}, fmt.Errorf("PIXIE_TOKEN and PIXIE_MCP_TOKEN must be different")
	}
	browserURL := strings.TrimSpace(getenv("PIXIE_BROWSER_URL"))
	if browserURL == "" {
		browserURL = mcpURL
		if browserURL == "" {
			browserURL = "http://127.0.0.1:8787"
		}
	}
	browserURL, err = normalizeOrigin(browserURL)
	if err != nil {
		return AuthConfig{}, fmt.Errorf("PIXIE_BROWSER_URL must be an absolute http(s) origin without credentials or a path")
	}
	parsedBrowser, _ := url.Parse(browserURL)
	sharedMCPOrigin := mcpURL != "" && sameAppViewOrigin(mcpURL, browserURL) && strongToken(mcpToken)
	if !browserEnabled && !sharedMCPOrigin && parsedBrowser.Hostname() != "localhost" && !net.ParseIP(parsedBrowser.Hostname()).IsLoopback() {
		return AuthConfig{}, fmt.Errorf("a non-loopback PIXIE_BROWSER_URL requires browser authentication")
	}
	browserPublicOrigin := strings.TrimSpace(getenv("PIXIE_BROWSER_PUBLIC_ORIGIN"))
	publicOriginSetting := "PIXIE_BROWSER_PUBLIC_ORIGIN"
	if browserPublicOrigin == "" && mcpURL != "" && sameAppViewOrigin(mcpURL, browserURL) {
		// The embedded Browser module shares the host's public origin.
		publicOriginSetting = "PIXIE_MCP_PUBLIC_ORIGIN"
		browserPublicOrigin = strings.TrimSpace(getenv("PIXIE_MCP_PUBLIC_ORIGIN"))
	}
	if browserPublicOrigin != "" {
		if !browserEnabled && !sharedMCPOrigin {
			return AuthConfig{}, fmt.Errorf("%s requires Browser module authentication", publicOriginSetting)
		}
		browserPublicOrigin, err = normalizeOrigin(browserPublicOrigin)
		if err != nil {
			return AuthConfig{}, fmt.Errorf("%s must be an absolute http(s) origin without credentials or a path", publicOriginSetting)
		}
		if publicOrigin != "" && sameAppViewOrigin(browserPublicOrigin, publicOrigin) {
			return AuthConfig{}, fmt.Errorf("%s must differ from PIXIE_PUBLIC_ORIGIN", publicOriginSetting)
		}
	}
	return AuthConfig{Enabled: enabled, BrowserEnabled: browserEnabled, ControllerToken: controllerToken, BrowserToken: browserToken, BrowserURL: browserURL, BrowserPublicOrigin: browserPublicOrigin, MCPURL: mcpURL, MCPToken: mcpToken, ControllerHost: host, PublicOrigin: publicOrigin, AllowRemoteWithout: allowRemote}, nil
}

func (c AuthConfig) ExpectedOrigin(request *http.Request) (string, error) {
	if c.PublicOrigin != "" {
		return c.PublicOrigin, nil
	}
	if len(request.Host) == 0 || len(request.Host) > maxHostLength || strings.TrimSpace(request.Host) != request.Host || strings.ContainsAny(request.Host, " \t\r\n,") {
		return "", fmt.Errorf("invalid host")
	}
	if _, _, err := net.SplitHostPort(request.Host); err != nil {
		if net.ParseIP(strings.Trim(request.Host, "[]")) == nil && !validHostname(request.Host) {
			return "", fmt.Errorf("invalid host")
		}
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + strings.ToLower(request.Host), nil
}

func (c AuthConfig) IsExpectedOrigin(request *http.Request) bool {
	origin, err := normalizeOrigin(request.Header.Get("Origin"))
	if err != nil {
		return false
	}
	expected, err := c.ExpectedOrigin(request)
	return err == nil && origin == expected
}

func ReadAuthCookie(request *http.Request) string {
	if len(request.Header.Get("Cookie")) > maxAuthHeaderLength {
		return ""
	}
	var found string
	for _, cookie := range request.Cookies() {
		if cookie.Name != AuthCookieName {
			continue
		}
		if found != "" {
			return ""
		}
		found = cookie.Value
	}
	return found
}

func (c AuthConfig) IsAuthorizedHTTPRequest(request *http.Request, auth *Auth) bool {
	if c.Enabled {
		if auth == nil {
			return false
		}
		if _, ok := auth.SessionExpiresAt(ReadAuthCookie(request)); !ok {
			return false
		}
		if site := request.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
			return false
		}
	} else if request.Header.Get("Sec-Fetch-Site") != "same-origin" {
		return false
	}
	origin := request.Header.Get("Origin")
	return origin == "" || c.IsExpectedOrigin(request)
}

func normalizeOrigin(value string) (string, error) {
	if value == "" || len(value) > maxOriginLength || strings.TrimSpace(value) != value {
		return "", fmt.Errorf("invalid origin")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Hostname() == "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid origin")
	}
	hostname := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
		port = ""
	}
	host := hostname
	if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	}
	return parsed.Scheme + "://" + host, nil
}

func strictBool(value string, fallback bool, name string) (bool, error) {
	if value == "" {
		return fallback, nil
	}
	if value == "true" {
		return true, nil
	}
	if value == "false" {
		return false, nil
	}
	return false, fmt.Errorf("%s must be exactly true or false", name)
}

func strongToken(value string) bool {
	if len(value) < 32 || len(value) > 256 || strings.HasPrefix(value, "INVALID_REPLACE_WITH_RANDOM_") || strings.HasPrefix(value, "replace-with-a-random-") {
		return false
	}
	for _, character := range []byte(value) {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func constantTimeStringEqual(left, right string) bool {
	leftDigest := sha256.Sum256([]byte("pixie-controller-token-compare-v1" + left))
	rightDigest := sha256.Sum256([]byte("pixie-controller-token-compare-v1" + right))
	return subtle.ConstantTimeCompare(leftDigest[:], rightDigest[:]) == 1
}

func validHostname(value string) bool {
	if strings.Contains(value, ":") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-') {
				return false
			}
		}
	}
	return true
}
