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
	maxTrustedProxies   = 32
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
	MCPToken            string
	ControllerHost      string
	// ControllerPort is the port on which the application listener accepts
	// requests. ReadAuthConfig supplies the documented default and Runtime
	// overwrites it with the effective configured port. A zero value does not
	// admit local authorities because the listener port is then unknown;
	// directly constructed handlers should set it explicitly.
	ControllerPort int
	// TrustedProxyCIDRs identifies TLS-terminating peers that may forward a
	// cleartext request. Such a peer must rewrite Host to PublicOrigin's
	// authority and set exactly X-Forwarded-Proto: https. Forwarded and
	// X-Forwarded-Host remain ignored.
	TrustedProxyCIDRs  []net.IPNet
	PublicOrigin       string
	AllowRemoteWithout bool
}

// BrowserServiceAuth selects the credential for controller-owned requests to
// the Browser HTTP surface. Browser-specific fields remain supported for
// compatibility with older deployments; without them the in-process module's
// publisher credential (PIXIE_MCP_TOKEN) applies.
func (c AuthConfig) BrowserServiceAuth() (bool, string) {
	if c.BrowserEnabled || c.BrowserToken != "" {
		return c.BrowserEnabled, c.BrowserToken
	}
	return c.MCPToken != "", c.MCPToken
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
	host := strings.TrimSpace(getenv("PIXIE_CONTROLLER_HOST"))
	if host == "" {
		host = "127.0.0.1"
	}
	if err := validateControllerHost(host); err != nil {
		return AuthConfig{}, err
	}
	allowRemote, err := strictBool(getenv("PIXIE_ALLOW_UNAUTHENTICATED_REMOTE"), false, "PIXIE_ALLOW_UNAUTHENTICATED_REMOTE")
	if err != nil {
		return AuthConfig{}, err
	}
	publicOrigin := strings.TrimSpace(getenv("PIXIE_PUBLIC_ORIGIN"))
	if publicOrigin != "" {
		publicOrigin, err = normalizeOrigin(publicOrigin)
		if err != nil {
			return AuthConfig{}, fmt.Errorf("PIXIE_PUBLIC_ORIGIN must be an absolute http(s) origin without a path")
		}
	}
	if !isLoopbackControllerHost(host) {
		if publicOrigin == "" {
			return AuthConfig{}, fmt.Errorf("a non-loopback PIXIE_CONTROLLER_HOST requires PIXIE_PUBLIC_ORIGIN")
		}
		if !enabled && !allowRemote {
			return AuthConfig{}, fmt.Errorf("a non-loopback PIXIE_CONTROLLER_HOST requires controller authentication or explicit PIXIE_ALLOW_UNAUTHENTICATED_REMOTE=true")
		}
	}
	trustedProxyCIDRs, err := parseTrustedProxyCIDRs(getenv("PIXIE_TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return AuthConfig{}, err
	}
	if err := validateTrustedProxyAuth(enabled, trustedProxyCIDRs); err != nil {
		return AuthConfig{}, err
	}
	// PIXIE_MCP_TOKEN still authenticates the in-process MCP publisher and
	// the adapter registration flow.
	mcpToken := strings.TrimSpace(getenv("PIXIE_MCP_TOKEN"))
	if mcpToken != "" && !strongToken(mcpToken) {
		return AuthConfig{}, fmt.Errorf("PIXIE_MCP_TOKEN must be a strong printable random token")
	}
	if mcpToken == "" && (enabled || !isLoopbackControllerHost(host)) {
		return AuthConfig{}, fmt.Errorf("PIXIE_MCP_TOKEN is required when the MCP publisher is exposed beyond an unauthenticated loopback controller")
	}
	if enabled && mcpToken != "" && constantTimeStringEqual(controllerToken, mcpToken) {
		return AuthConfig{}, fmt.Errorf("PIXIE_TOKEN and PIXIE_MCP_TOKEN must be different")
	}
	// Empty PIXIE_BROWSER_URL means the controller's in-process Browser
	// module (merged publisher); an explicit URL keeps proxying to an
	// external, isolated Browser service for panel and artifact viewing.
	browserURL := strings.TrimSpace(getenv("PIXIE_BROWSER_URL"))
	if browserURL != "" {
		var normalizeErr error
		browserURL, normalizeErr = normalizeOrigin(browserURL)
		if normalizeErr != nil {
			return AuthConfig{}, fmt.Errorf("PIXIE_BROWSER_URL must be an absolute http(s) origin without credentials or a path")
		}
	}
	if browserURL != "" {
		parsedBrowser, _ := url.Parse(browserURL)
		if !browserEnabled && parsedBrowser.Hostname() != "localhost" && !net.ParseIP(parsedBrowser.Hostname()).IsLoopback() {
			return AuthConfig{}, fmt.Errorf("a non-loopback PIXIE_BROWSER_URL requires browser authentication")
		}
	}
	browserPublicOrigin := strings.TrimSpace(getenv("PIXIE_BROWSER_PUBLIC_ORIGIN"))
	publicOriginSetting := "PIXIE_BROWSER_PUBLIC_ORIGIN"
	if browserPublicOrigin != "" {
		if !browserEnabled {
			return AuthConfig{}, fmt.Errorf("%s requires Browser module authentication", publicOriginSetting)
		}
		browserPublicOrigin, err = normalizeOrigin(browserPublicOrigin)
		if err != nil {
			return AuthConfig{}, fmt.Errorf("%s must be an absolute http(s) origin without credentials or a path", publicOriginSetting)
		}
		// Both origins are normalized above, so plain equality is exact.
		// The Browser surface stays isolated from the application origin
		// for panel and artifact framing.
		if publicOrigin != "" && browserPublicOrigin == publicOrigin {
			return AuthConfig{}, fmt.Errorf("%s must differ from PIXIE_PUBLIC_ORIGIN", publicOriginSetting)
		}
	}
	return AuthConfig{Enabled: enabled, BrowserEnabled: browserEnabled, ControllerToken: controllerToken, BrowserToken: browserToken, BrowserURL: browserURL, BrowserPublicOrigin: browserPublicOrigin, MCPToken: mcpToken, ControllerHost: host, ControllerPort: DefaultControllerPort, TrustedProxyCIDRs: trustedProxyCIDRs, PublicOrigin: publicOrigin, AllowRemoteWithout: allowRemote}, nil
}

func validateControllerHost(host string) error {
	if !map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true, "0.0.0.0": true, "::": true}[host] {
		return fmt.Errorf("PIXIE_CONTROLLER_HOST must be exactly localhost, 127.0.0.1, ::1, 0.0.0.0, or ::")
	}
	return nil
}

func isLoopbackControllerHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// mcpTokenRequired reports whether the in-process publisher must have its
// bearer credential. Unauthenticated loopback mode is the one intentionally
// retained local policy; authenticated or remotely bound controllers must not
// expose MCP routes without PIXIE_MCP_TOKEN.
func mcpTokenRequired(config AuthConfig) bool {
	if config.Enabled {
		return true
	}
	if host := strings.TrimSpace(config.ControllerHost); host != "" {
		return !isLoopbackControllerHost(host)
	}
	if origin := strings.TrimSpace(config.PublicOrigin); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil {
			return true
		}
		host := strings.ToLower(parsed.Hostname())
		if host == "localhost" {
			return false
		}
		if ip := net.ParseIP(host); ip != nil {
			return !ip.IsLoopback()
		}
		return true
	}
	return false
}

func mcpPublisherAuthConfigured(config AuthConfig) bool {
	if config.MCPToken == "" {
		return !mcpTokenRequired(config)
	}
	return strongToken(config.MCPToken)
}

// IsAllowedAuthority checks the HTTP Host independently of Origin. Public
// origins describe the browser-facing authority, while local loopback forms
// are admitted on the configured listener port. An omitted local port is only
// equivalent when that listener uses the HTTP or HTTPS default port.
// Forwarded host headers are deliberately not consulted; cleartext TLS
// termination is handled only by the explicit trusted-peer transport policy.
func (c AuthConfig) IsAllowedAuthority(request *http.Request) bool {
	if request == nil {
		return false
	}
	actual, err := normalizeHostHeader(request.Host)
	if err != nil {
		return false
	}
	matchers, err := c.authorityMatchers()
	if err != nil {
		return false
	}
	for _, matcher := range matchers {
		if matcher.matches(actual) {
			return true
		}
	}
	return false
}

// IsAllowedTransport rejects cleartext access to an HTTPS public origin unless
// the request is an actual loopback service call or came from an explicitly
// configured TLS-terminating peer. The peer policy requires Host rewriting at
// the edge, so a remote request cannot opt into HTTPS with Origin or token
// headers alone.
func (c AuthConfig) IsAllowedTransport(request *http.Request) bool {
	if request == nil {
		return false
	}
	if len(c.TrustedProxyCIDRs) > 0 && !c.Enabled {
		return false
	}
	if c.PublicOrigin == "" {
		return true
	}
	normalized, err := normalizeOrigin(c.PublicOrigin)
	if err != nil {
		return false
	}
	parsed, err := url.Parse(normalized)
	if err != nil || parsed.Scheme != "https" {
		return true
	}
	if request.TLS != nil {
		return true
	}
	if c.isLoopbackServiceRequest(request) {
		return true
	}
	return c.isTrustedHTTPSProxyRequest(request)
}

// ExpectedOrigin returns the separately configured browser origin. Without a
// public origin, it derives the origin from an already-approved local Host;
// an arbitrary Host can therefore never manufacture an expected Origin.
func (c AuthConfig) ExpectedOrigin(request *http.Request) (string, error) {
	if !c.IsAllowedAuthority(request) || !c.IsAllowedTransport(request) {
		return "", fmt.Errorf("unapproved authority")
	}
	if c.PublicOrigin != "" {
		return normalizeOrigin(c.PublicOrigin)
	}
	actual, err := normalizeHostHeader(request.Host)
	if err != nil {
		return "", err
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	return normalizeOrigin(scheme + "://" + actual.value(c.ControllerPort))
}

// IsExpectedOrigin enforces the browser Origin check after the independent
// Host check. A missing Origin is intentionally not accepted here; callers
// that represent explicitly authenticated service traffic use
// IsAuthorizedHTTPRequest, which permits an absent Origin after authority and
// credential checks.
func (c AuthConfig) IsExpectedOrigin(request *http.Request) bool {
	if request == nil || !c.IsAllowedAuthority(request) || !c.IsAllowedTransport(request) {
		return false
	}
	origins := request.Header.Values("Origin")
	if len(origins) != 1 {
		return false
	}
	origin, err := normalizeOrigin(origins[0])
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
	if !c.IsAllowedAuthority(request) || !c.IsAllowedTransport(request) {
		return false
	}
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
	origins := request.Header.Values("Origin")
	if len(origins) == 0 {
		return true
	}
	if len(origins) != 1 || origins[0] == "" {
		return false
	}
	return c.IsExpectedOrigin(request)
}

// SecureCookie derives the cookie transport policy from the real TLS state or
// the configured public origin. Forwarded proto headers from an untrusted peer
// cannot turn an HTTP request into a Secure-cookie request.
func (c AuthConfig) SecureCookie(request *http.Request) bool {
	if request == nil || !c.IsAllowedTransport(request) {
		return false
	}
	if request.TLS != nil {
		return true
	}
	if c.PublicOrigin == "" {
		return false
	}
	normalized, err := normalizeOrigin(c.PublicOrigin)
	if err != nil {
		return false
	}
	parsed, err := url.Parse(normalized)
	return err == nil && parsed.Scheme == "https"
}

func (c AuthConfig) isTrustedHTTPSProxyRequest(request *http.Request) bool {
	if len(c.TrustedProxyCIDRs) == 0 {
		return false
	}
	peer := requestPeerIP(request)
	if peer == nil {
		return false
	}
	trusted := false
	for _, network := range c.TrustedProxyCIDRs {
		if network.Contains(peer) {
			trusted = true
			break
		}
	}
	if !trusted {
		return false
	}
	public, err := c.publicAuthorityMatcher()
	if err != nil {
		return false
	}
	actual, err := normalizeHostHeader(request.Host)
	if err != nil || !public.matches(actual) {
		return false
	}
	forwardedProto := request.Header.Values("X-Forwarded-Proto")
	return len(forwardedProto) == 1 && strings.EqualFold(strings.TrimSpace(forwardedProto[0]), "https")
}

func (c AuthConfig) isLoopbackServiceRequest(request *http.Request) bool {
	// An omitted Host port is only enough when it is equivalent to the
	// configured HTTP or HTTPS default; non-default listener ports must be
	// identified explicitly before cleartext service traffic is admitted.
	if c.ControllerPort <= 0 || request == nil {
		return false
	}
	peer := requestPeerIP(request)
	if peer == nil || !peer.IsLoopback() {
		return false
	}
	actual, err := normalizeHostHeader(request.Host)
	if err != nil {
		return false
	}
	if actual.hasPort && actual.port != c.ControllerPort {
		return false
	}
	if !actual.hasPort && c.ControllerPort != 80 && c.ControllerPort != 443 {
		return false
	}
	for _, matcher := range c.localAuthorityMatchers() {
		if matcher.matches(actual) {
			return true
		}
	}
	return false
}

func requestPeerIP(request *http.Request) net.IP {
	if request == nil {
		return nil
	}
	value := strings.TrimSpace(request.RemoteAddr)
	if value == "" {
		return nil
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	} else {
		value = strings.Trim(value, "[]")
	}
	return net.ParseIP(value)
}

type normalizedHost struct {
	host    string
	port    int
	hasPort bool
}

func (h normalizedHost) value(defaultPort int) string {
	port := h.port
	if !h.hasPort {
		port = defaultPort
	}
	if port <= 0 {
		if strings.Contains(h.host, ":") {
			return "[" + h.host + "]"
		}
		return h.host
	}
	return net.JoinHostPort(h.host, strconv.Itoa(port))
}

type authorityMatcher struct {
	host                 string
	port                 int
	allowUnspecifiedPort bool
}

func (m authorityMatcher) matches(actual normalizedHost) bool {
	if m.host != actual.host {
		return false
	}
	if !actual.hasPort {
		return m.allowUnspecifiedPort
	}
	return m.port == actual.port
}

func (c AuthConfig) authorityMatchers() ([]authorityMatcher, error) {
	matchers := c.localAuthorityMatchers()
	if c.PublicOrigin == "" {
		return matchers, nil
	}
	public, err := c.publicAuthorityMatcher()
	if err != nil {
		return nil, err
	}
	return append(matchers, public), nil
}

func (c AuthConfig) localAuthorityMatchers() []authorityMatcher {
	if c.ControllerPort <= 0 {
		return nil
	}
	allowUnspecifiedPort := c.ControllerPort == 80 || c.ControllerPort == 443
	return []authorityMatcher{
		{host: "localhost", port: c.ControllerPort, allowUnspecifiedPort: allowUnspecifiedPort},
		{host: "127.0.0.1", port: c.ControllerPort, allowUnspecifiedPort: allowUnspecifiedPort},
		{host: "::1", port: c.ControllerPort, allowUnspecifiedPort: allowUnspecifiedPort},
	}
}

func (c AuthConfig) publicAuthorityMatcher() (authorityMatcher, error) {
	normalized, err := normalizeOrigin(c.PublicOrigin)
	if err != nil {
		return authorityMatcher{}, err
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return authorityMatcher{}, err
	}
	host, err := normalizeHostHeader(parsed.Host)
	if err != nil {
		return authorityMatcher{}, err
	}
	port := defaultOriginPort(parsed.Scheme)
	allowUnspecifiedPort := !host.hasPort
	if host.hasPort {
		port = host.port
	}
	return authorityMatcher{host: host.host, port: port, allowUnspecifiedPort: allowUnspecifiedPort}, nil
}

func parseTrustedProxyCIDRs(value string) ([]net.IPNet, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	if len(parts) > maxTrustedProxies {
		return nil, fmt.Errorf("PIXIE_TRUSTED_PROXY_CIDRS may contain at most %d peers", maxTrustedProxies)
	}
	result := make([]net.IPNet, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("PIXIE_TRUSTED_PROXY_CIDRS contains an empty peer")
		}
		if ip := net.ParseIP(part); ip != nil {
			if ip.IsUnspecified() {
				return nil, fmt.Errorf("PIXIE_TRUSTED_PROXY_CIDRS cannot trust an unspecified peer")
			}
			bits := 128
			if ipv4 := ip.To4(); ipv4 != nil {
				ip, bits = ipv4, 32
			}
			mask := net.CIDRMask(bits, bits)
			result = append(result, net.IPNet{IP: ip.Mask(mask), Mask: mask})
			continue
		}
		_, network, err := net.ParseCIDR(part)
		if err != nil {
			return nil, fmt.Errorf("PIXIE_TRUSTED_PROXY_CIDRS contains invalid peer %q", part)
		}
		ones, bits := network.Mask.Size()
		if bits == 0 || ones == 0 {
			return nil, fmt.Errorf("PIXIE_TRUSTED_PROXY_CIDRS cannot trust an unspecified network")
		}
		result = append(result, *network)
	}
	return result, nil
}

func validateTrustedProxyAuth(enabled bool, trustedProxyCIDRs []net.IPNet) error {
	if len(trustedProxyCIDRs) > 0 && !enabled {
		return fmt.Errorf("PIXIE_TRUSTED_PROXY_CIDRS requires PIXIE_AUTH_ENABLED=true")
	}
	return nil
}

func defaultOriginPort(scheme string) int {
	if scheme == "https" {
		return 443
	}
	return 80
}

func normalizeHostHeader(value string) (normalizedHost, error) {
	if value == "" || len(value) > maxHostLength || strings.TrimSpace(value) != value || strings.ContainsAny(value, " \t\r\n,") {
		return normalizedHost{}, fmt.Errorf("invalid host")
	}
	host := value
	var portText string
	hasPort := false
	if strings.HasPrefix(value, "[") {
		closing := strings.IndexByte(value, ']')
		if closing <= 1 {
			return normalizedHost{}, fmt.Errorf("invalid host")
		}
		host = value[1:closing]
		// Brackets are reserved for IP-literal authorities. Treating a
		// bracketed DNS name as equivalent to its unbracketed form would admit
		// malformed Host values that are not valid HTTP authorities.
		if ip := net.ParseIP(host); ip == nil || ip.To4() != nil {
			return normalizedHost{}, fmt.Errorf("invalid host")
		}
		rest := value[closing+1:]
		if rest != "" {
			if !strings.HasPrefix(rest, ":") || len(rest) == 1 {
				return normalizedHost{}, fmt.Errorf("invalid host")
			}
			portText, hasPort = rest[1:], true
		}
	} else {
		switch strings.Count(value, ":") {
		case 0:
		case 1:
			separator := strings.LastIndexByte(value, ':')
			host, portText, hasPort = value[:separator], value[separator+1:], true
			if host == "" || portText == "" {
				return normalizedHost{}, fmt.Errorf("invalid host")
			}
		default:
			// An unbracketed value with multiple colons is accepted only as an
			// IPv6 literal without a port. Ports on IPv6 must be bracketed.
		}
	}
	normalizedHostname, err := normalizeHostname(host)
	if err != nil {
		return normalizedHost{}, err
	}
	port := 0
	if hasPort {
		port, err = normalizePort(portText)
		if err != nil {
			return normalizedHost{}, err
		}
	}
	return normalizedHost{host: normalizedHostname, port: port, hasPort: hasPort}, nil
}

func normalizeHostname(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("invalid host")
	}
	if parsed := net.ParseIP(value); parsed != nil {
		return parsed.String(), nil
	}
	if !validHostname(value) {
		return "", fmt.Errorf("invalid host")
	}
	return strings.ToLower(value), nil
}

func normalizePort(value string) (int, error) {
	if value == "" || len(value) > 5 {
		return 0, fmt.Errorf("invalid host")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, fmt.Errorf("invalid host")
		}
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid host")
	}
	return port, nil
}

func normalizeOrigin(value string) (string, error) {
	if value == "" || len(value) > maxOriginLength || strings.TrimSpace(value) != value {
		return "", fmt.Errorf("invalid origin")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Hostname() == "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(value, "#") || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid origin")
	}
	host, err := normalizeHostHeader(parsed.Host)
	if err != nil {
		return "", fmt.Errorf("invalid origin")
	}
	port := host.port
	if (parsed.Scheme == "http" && port == 80) || (parsed.Scheme == "https" && port == 443) {
		port = 0
	}
	if port > 0 {
		host.port, host.hasPort = port, true
	} else {
		host.port, host.hasPort = 0, false
	}
	return parsed.Scheme + "://" + host.value(0), nil
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
