package controller

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math/bits"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miloszkolber/pixie/internal/persist"
)

const (
	AuthCookieName      = "pixie_auth"
	SessionMaxAge       = 90 * 24 * time.Hour
	maxAuthHeaderLength = 4096
	maxOriginLength     = 512
	maxHostLength       = 255
	maxTrustedProxies   = 32

	// PasswordHashPrefix identifies the encoded scrypt credential format.
	PasswordHashPrefix  = "scrypt"
	defaultScryptN      = 1 << 14
	defaultScryptR      = 8
	defaultScryptP      = 1
	scryptKeyLength     = 64
	scryptSaltBytes     = 16
	maxPasswordHash     = 512
	maxScryptSaltLength = 64
	maxScryptN          = 1 << 20
	maxScryptR          = 32
	maxScryptP          = 16
	maxScryptKeyLength  = 1024
	maxScryptMemory     = 256 << 20
	maxTrackedClients   = 4096
	maxTrackedSessions  = 4096
)

type Auth struct {
	token        string
	passwordHash string
	signingKey   []byte
	now          func() time.Time

	mu                sync.Mutex
	sessions          map[string]time.Time
	failures          map[string]loginFailure
	loginAttemptLimit int
	loginLockout      time.Duration
	inactivityTimeout time.Duration
}

type loginFailure struct {
	failures    int
	lockedUntil time.Time
}

// AuthOptions configures an Auth instance. Zero hardening durations and limits
// disable the optional protections, preserving the legacy token-only behavior.
type AuthOptions struct {
	Token             string
	PasswordHash      string
	SigningSecret     string
	LoginAttemptLimit int
	LoginLockout      time.Duration
	InactivityTimeout time.Duration
	// DataDir loads or persists the signing secret and password hash when they
	// are not supplied explicitly.
	DataDir string
	// Now overrides the clock; nil uses time.Now.
	Now func() time.Time
}

func NewAuth(token string) (*Auth, error) {
	if token == "" {
		return nil, fmt.Errorf("PIXIE_TOKEN is required")
	}
	return NewAuthWithOptions(AuthOptions{Token: token})
}

// NewAuthWithOptions builds an Auth with the optional hardening. A signing
// secret is required (explicit, token-derived, or generated in DataDir), and
// the login credential is either the controller token or a stored scrypt hash.
func NewAuthWithOptions(options AuthOptions) (*Auth, error) {
	if options.LoginAttemptLimit < 0 || options.LoginLockout < 0 || options.InactivityTimeout < 0 {
		return nil, fmt.Errorf("controller auth hardening limits and durations must not be negative")
	}
	signingSecret := strings.TrimSpace(options.SigningSecret)
	passwordHash := strings.TrimSpace(options.PasswordHash)
	if options.DataDir != "" {
		if passwordHash == "" {
			stored, ok, err := persist.ReadPasswordHash(options.DataDir)
			if err != nil {
				return nil, err
			}
			if ok {
				passwordHash = stored
			}
		}
		if signingSecret == "" {
			generated, err := persist.LoadOrCreateAuthSecret(options.DataDir)
			if err != nil {
				return nil, err
			}
			signingSecret = generated
		}
	}
	if passwordHash == "" && options.Token == "" {
		return nil, fmt.Errorf("a controller token or password hash is required")
	}
	if passwordHash != "" && !PasswordHashValid(passwordHash) {
		return nil, fmt.Errorf("controller password hash is not a valid %s encoding", PasswordHashPrefix)
	}
	if signingSecret == "" {
		signingSecret = options.Token
	}
	if signingSecret == "" {
		return nil, fmt.Errorf("a controller signing secret is required")
	}
	result := &Auth{
		token:             options.Token,
		passwordHash:      passwordHash,
		signingKey:        []byte(signingSecret),
		now:               time.Now,
		sessions:          make(map[string]time.Time),
		failures:          make(map[string]loginFailure),
		loginAttemptLimit: options.LoginAttemptLimit,
		loginLockout:      options.LoginLockout,
		inactivityTimeout: options.InactivityTimeout,
	}
	if options.Now != nil {
		result.now = options.Now
	}
	return result, nil
}

// NewAuthFromConfig applies the optional AuthConfig hardening and loads the
// persisted signing secret and password hash from dataDir when they are not
// supplied explicitly.
func NewAuthFromConfig(config AuthConfig, dataDir string) (*Auth, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		dataDir = config.DataDir
	}
	return NewAuthWithOptions(AuthOptions{
		Token:             config.ControllerToken,
		PasswordHash:      config.Hardening.PasswordHash,
		SigningSecret:     config.Hardening.SigningSecret,
		LoginAttemptLimit: config.Hardening.LoginAttemptLimit,
		LoginLockout:      config.Hardening.LoginLockout,
		InactivityTimeout: config.Hardening.InactivityTimeout,
		DataDir:           dataDir,
	})
}

func (a *Auth) Login(candidate string) (string, bool) {
	if !a.verifyCredential(candidate) {
		return "", false
	}
	return a.issueSession()
}

// LoginWithClient adds the per-client lockout to Login. clientKey is the
// request peer identity (for example the remote IP); a locked client is
// refused even when it presents the correct credential.
func (a *Auth) LoginWithClient(clientKey, candidate string) (string, bool) {
	if _, locked := a.LockedUntil(clientKey); locked {
		return "", false
	}
	if !a.verifyCredential(candidate) {
		a.recordLoginFailure(clientKey)
		return "", false
	}
	a.clearLoginFailures(clientKey)
	return a.issueSession()
}

func (a *Auth) verifyCredential(candidate string) bool {
	if a.passwordHash != "" {
		return VerifyPassword(a.passwordHash, candidate)
	}
	return constantTimeStringEqual(a.token, candidate)
}

func (a *Auth) issueSession() (string, bool) {
	expires := a.now().Add(SessionMaxAge)
	session := a.cookieFor(expires.Unix())
	if a.inactivityTimeout > 0 {
		a.mu.Lock()
		a.sessions[session] = a.now()
		a.mu.Unlock()
	}
	return session, true
}

// LockedUntil reports the client's lockout expiry. An expired lockout is
// cleared so the next failure starts a fresh window.
func (a *Auth) LockedUntil(clientKey string) (time.Time, bool) {
	if a.loginAttemptLimit <= 0 || a.loginLockout <= 0 || clientKey == "" {
		return time.Time{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.failures[clientKey]
	if state.lockedUntil.IsZero() {
		return time.Time{}, false
	}
	if !a.now().Before(state.lockedUntil) {
		delete(a.failures, clientKey)
		return time.Time{}, false
	}
	return state.lockedUntil, true
}

func (a *Auth) recordLoginFailure(clientKey string) {
	if a.loginAttemptLimit <= 0 || a.loginLockout <= 0 || clientKey == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.failures[clientKey]
	state.failures++
	if state.failures >= a.loginAttemptLimit {
		state.failures = 0
		state.lockedUntil = a.now().Add(a.loginLockout)
	}
	a.failures[clientKey] = state
	a.pruneLoginFailuresLocked()
}

func (a *Auth) clearLoginFailures(clientKey string) {
	if a.loginAttemptLimit <= 0 || a.loginLockout <= 0 || clientKey == "" {
		return
	}
	a.mu.Lock()
	delete(a.failures, clientKey)
	a.mu.Unlock()
}

// pruneLoginFailuresLocked keeps the per-client map bounded without evicting an
// active lockout unless every tracked client is locked.
func (a *Auth) pruneLoginFailuresLocked() {
	if len(a.failures) <= maxTrackedClients {
		return
	}
	now := a.now()
	for key, state := range a.failures {
		if state.lockedUntil.IsZero() || !now.Before(state.lockedUntil) {
			delete(a.failures, key)
			return
		}
	}
	for key := range a.failures {
		delete(a.failures, key)
		return
	}
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
	if !result.After(a.now()) {
		return time.Time{}, false
	}
	if a.inactivityTimeout > 0 && !a.touchSession(session) {
		return time.Time{}, false
	}
	return result, true
}

func (a *Auth) touchSession(session string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	last, ok := a.sessions[session]
	if !ok {
		// A valid session without local activity state was issued before a
		// restart. Seed the window now rather than accept an unbounded idle
		// session, then keep sliding it from this point.
		a.sessions[session] = now
		return true
	}
	if last.IsZero() {
		return false
	}
	if now.Sub(last) > a.inactivityTimeout {
		// Keep a tombstone so the already-expired session cannot be re-seeded.
		a.sessions[session] = time.Time{}
		a.pruneSessionsLocked()
		return false
	}
	a.sessions[session] = now
	return true
}

func (a *Auth) pruneSessionsLocked() {
	if len(a.sessions) <= maxTrackedSessions {
		return
	}
	for session, last := range a.sessions {
		if last.IsZero() {
			delete(a.sessions, session)
			return
		}
	}
}

func (a *Auth) cookieFor(expires int64) string {
	encoded := strconv.FormatInt(expires, 36)
	digest := hmac.New(sha256.New, a.signingKey)
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
	Enabled         bool
	ControllerToken string
	MCPToken        string
	ControllerHost  string
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
	// DataDir is the controller state directory used to load or create the
	// persisted signing secret and stored password hash. Empty keeps the
	// legacy in-memory token-only behavior.
	DataDir string
	// Hardening carries the optional authenticated-mode protections. The zero
	// value keeps the legacy token-only behavior.
	Hardening AuthHardening
}

// AuthHardening is the optional authenticated-mode protection set: an explicit
// signing secret, a stored scrypt password hash, per-client login lockout, a
// sliding inactivity timeout, and the strict Host/Fetch-Metadata policy applied
// by IsStrictAuthorizedHTTPRequest.
type AuthHardening struct {
	SigningSecret     string
	PasswordHash      string
	LoginAttemptLimit int
	LoginLockout      time.Duration
	InactivityTimeout time.Duration
}

func ReadAuthConfig(getenv func(string) string) (AuthConfig, error) {
	enabled, err := strictBool(getenv("PIXIE_AUTH_ENABLED"), false, "PIXIE_AUTH_ENABLED")
	if err != nil {
		return AuthConfig{}, err
	}
	controllerToken := strings.TrimSpace(getenv("PIXIE_TOKEN"))
	if enabled && !strongToken(controllerToken) {
		return AuthConfig{}, fmt.Errorf("PIXIE_TOKEN must be a strong printable random token")
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
	hardening, err := readAuthHardening(enabled, getenv)
	if err != nil {
		return AuthConfig{}, err
	}
	return AuthConfig{Enabled: enabled, ControllerToken: controllerToken, MCPToken: mcpToken, ControllerHost: host, ControllerPort: DefaultControllerPort, TrustedProxyCIDRs: trustedProxyCIDRs, PublicOrigin: publicOrigin, AllowRemoteWithout: allowRemote, Hardening: hardening}, nil
}

// readAuthHardening parses the optional authenticated-mode protections. The
// defaults apply only when authentication is enabled; an explicit `off` or `0`
// disables a control.
func readAuthHardening(enabled bool, getenv func(string) string) (AuthHardening, error) {
	if !enabled {
		return AuthHardening{}, nil
	}
	var result AuthHardening
	result.SigningSecret = strings.TrimSpace(getenv("PIXIE_AUTH_SIGNING_SECRET"))
	result.PasswordHash = strings.TrimSpace(getenv("PIXIE_AUTH_PASSWORD_HASH"))
	if result.SigningSecret != "" && !strongToken(result.SigningSecret) {
		return AuthHardening{}, fmt.Errorf("PIXIE_AUTH_SIGNING_SECRET must be a strong printable random secret")
	}
	if result.PasswordHash != "" && !PasswordHashValid(result.PasswordHash) {
		return AuthHardening{}, fmt.Errorf("PIXIE_AUTH_PASSWORD_HASH must be an encoded scrypt credential")
	}
	limit, err := parseAuthCount("PIXIE_AUTH_LOGIN_ATTEMPT_LIMIT", getenv("PIXIE_AUTH_LOGIN_ATTEMPT_LIMIT"))
	if err != nil {
		return AuthHardening{}, err
	}
	lockout, err := parseAuthDuration("PIXIE_AUTH_LOGIN_LOCKOUT", getenv("PIXIE_AUTH_LOGIN_LOCKOUT"), 15*time.Minute)
	if err != nil {
		return AuthHardening{}, err
	}
	inactivity, err := parseAuthDuration("PIXIE_AUTH_INACTIVITY_TIMEOUT", getenv("PIXIE_AUTH_INACTIVITY_TIMEOUT"), 12*time.Hour)
	if err != nil {
		return AuthHardening{}, err
	}
	result.LoginAttemptLimit, result.LoginLockout, result.InactivityTimeout = limit, lockout, inactivity
	return result, nil
}

func parseAuthCount(name, value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 8, nil
	}
	if value == "off" || value == "0" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 || parsed > 1_000_000 {
		return 0, fmt.Errorf("%s must be a non-negative integer or off", name)
	}
	return parsed, nil
}

func parseAuthDuration(name, value string, fallback time.Duration) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	if value == "off" {
		return 0, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative duration or off", name)
	}
	return parsed, nil
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

// IsStrictAllowedAuthority tightens IsAllowedAuthority for the optional
// authenticated mode: a loopback authority is admitted only from a loopback
// peer, so a remote client cannot claim the local authority with a rewritten
// Host header.
func (c AuthConfig) IsStrictAllowedAuthority(request *http.Request) bool {
	if !c.IsAllowedAuthority(request) {
		return false
	}
	actual, err := normalizeHostHeader(request.Host)
	if err != nil {
		return false
	}
	for _, matcher := range c.localAuthorityMatchers() {
		if matcher.matches(actual) {
			return c.isLoopbackServiceRequest(request)
		}
	}
	return true
}

// IsStrictAuthorizedHTTPRequest layers Fetch Metadata onto
// IsAuthorizedHTTPRequest. Safe methods keep the existing policy; an unsafe
// method must carry an explicit same-origin fetch site and must not be a
// top-level navigation or a no-cors fetch. It also applies the strict Host
// check so a remote peer cannot use the loopback authority.
func (c AuthConfig) IsStrictAuthorizedHTTPRequest(request *http.Request, auth *Auth) bool {
	if request == nil || !c.IsStrictAllowedAuthority(request) || !c.IsAllowedTransport(request) {
		return false
	}
	if !c.IsAuthorizedHTTPRequest(request, auth) {
		return false
	}
	switch request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if request.Header.Get("Sec-Fetch-Site") != "same-origin" {
		return false
	}
	switch request.Header.Get("Sec-Fetch-Mode") {
	case "navigate", "no-cors":
		return false
	}
	return true
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

// loginClientKey derives a stable, bounded per-client identity for login
// lockout. It uses the actual peer address only; a forwarded header is never
// trusted here because it is attacker-controlled on a direct connection.
func loginClientKey(request *http.Request) string {
	if ip := requestPeerIP(request); ip != nil {
		return ip.String()
	}
	return ""
}

// IsAuthorizedRoute applies the strict Fetch Metadata and Host policy to unsafe
// write methods and the existing policy to safe methods. This is the boundary
// used by authenticated HTTP routes so a cross-site or navigation-style write
// cannot ride a valid session cookie.
func (c AuthConfig) IsAuthorizedRoute(request *http.Request, auth *Auth) bool {
	if request == nil {
		return false
	}
	switch request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return c.IsAuthorizedHTTPRequest(request, auth)
	default:
		return c.IsStrictAuthorizedHTTPRequest(request, auth)
	}
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

// HashPassword derives a fresh scrypt credential with a random salt. The
// returned encoding records the cost parameters so a later parameter bump
// still verifies existing credentials.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("password must not be empty")
	}
	salt := make([]byte, scryptSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	derived, err := ScryptKey([]byte(password), salt, defaultScryptN, defaultScryptR, defaultScryptP, scryptKeyLength)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		PasswordHashPrefix,
		strconv.Itoa(defaultScryptN),
		strconv.Itoa(defaultScryptR),
		strconv.Itoa(defaultScryptP),
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(derived),
	}, "$"), nil
}

// PasswordHashValid reports whether encoded is a well-formed scrypt credential
// within the accepted parameter range.
func PasswordHashValid(encoded string) bool {
	_, _, _, _, _, ok := decodePasswordHash(encoded)
	return ok
}

// VerifyPassword derives the candidate with the stored parameters and compares
// it in constant time. Any parse or parameter failure fails closed.
func VerifyPassword(encoded, candidate string) bool {
	n, r, p, salt, expected, ok := decodePasswordHash(encoded)
	if !ok {
		return false
	}
	derived, err := ScryptKey([]byte(candidate), salt, n, r, p, len(expected))
	if err != nil || len(derived) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare(derived, expected) == 1
}

func decodePasswordHash(encoded string) (int, int, int, []byte, []byte, bool) {
	if encoded == "" || len(encoded) > maxPasswordHash || strings.TrimSpace(encoded) != encoded {
		return 0, 0, 0, nil, nil, false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != PasswordHashPrefix {
		return 0, 0, 0, nil, nil, false
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, nil, nil, false
	}
	r, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, 0, nil, nil, false
	}
	p, err := strconv.Atoi(parts[3])
	if err != nil {
		return 0, 0, 0, nil, nil, false
	}
	if err := validateScryptParameters(n, r, p, scryptKeyLength); err != nil {
		return 0, 0, 0, nil, nil, false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 || len(salt) > maxScryptSaltLength {
		return 0, 0, 0, nil, nil, false
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(hash) == 0 || len(hash) > maxScryptKeyLength {
		return 0, 0, 0, nil, nil, false
	}
	return n, r, p, salt, hash, true
}

func validateScryptParameters(n, r, p, keyLen int) error {
	if n <= 1 || n&(n-1) != 0 {
		return fmt.Errorf("scrypt: N must be a power of two greater than 1")
	}
	if r < 1 || p < 1 || keyLen < 1 {
		return fmt.Errorf("scrypt: parameters must be positive")
	}
	if n > maxScryptN || r > maxScryptR || p > maxScryptP || keyLen > maxScryptKeyLength {
		return fmt.Errorf("scrypt: parameters exceed the accepted range")
	}
	if uint64(r)*uint64(p) >= 1<<30 {
		return fmt.Errorf("scrypt: parameters are too large")
	}
	if uint64(128)*uint64(r)*uint64(n) > maxScryptMemory {
		return fmt.Errorf("scrypt: parameters exceed the memory budget")
	}
	return nil
}

// ScryptKey derives a key using the scrypt construction from RFC 7914. It is
// implemented with the standard library so the controller adds no dependency;
// the RFC test vectors in the controller tests pin the output.
func ScryptKey(password, salt []byte, n, r, p, keyLen int) ([]byte, error) {
	if err := validateScryptParameters(n, r, p, keyLen); err != nil {
		return nil, err
	}
	blocks, err := pbkdf2.Key(sha256.New, string(password), salt, 1, p*128*r)
	if err != nil {
		return nil, err
	}
	memory := 32 * n * r
	scratch := make([]uint32, memory+64*r)
	v := scratch[:memory]
	xy := scratch[memory:]
	for i := 0; i < p; i++ {
		scryptROMix(blocks[i*128*r:], r, n, v, xy)
	}
	return pbkdf2.Key(sha256.New, string(password), blocks, 1, keyLen)
}

func scryptROMix(block []byte, r, n int, v, xy []uint32) {
	var tmp [16]uint32
	words := 32 * r
	x := xy[:words]
	y := xy[words:]
	offset := 0
	for i := range x {
		x[i] = binary.LittleEndian.Uint32(block[offset:])
		offset += 4
	}
	for i := 0; i < n; i += 2 {
		copy(v[i*words:], x)
		scryptBlockMix(&tmp, x, y, r)
		copy(v[(i+1)*words:], y)
		scryptBlockMix(&tmp, y, x, r)
	}
	for i := 0; i < n; i += 2 {
		index := int(scryptInteger(x, r) & uint64(n-1))
		scryptBlockXOR(x, v[index*words:])
		scryptBlockMix(&tmp, x, y, r)
		index = int(scryptInteger(y, r) & uint64(n-1))
		scryptBlockXOR(y, v[index*words:])
		scryptBlockMix(&tmp, y, x, r)
	}
	offset = 0
	for _, word := range x {
		binary.LittleEndian.PutUint32(block[offset:], word)
		offset += 4
	}
}

func scryptBlockXOR(dst, src []uint32) {
	for i := range dst {
		dst[i] ^= src[i]
	}
}

func scryptInteger(block []uint32, r int) uint64 {
	index := (2*r - 1) * 16
	return uint64(block[index]) | uint64(block[index+1])<<32
}

func scryptBlockMix(tmp *[16]uint32, in, out []uint32, r int) {
	copy(tmp[:], in[(2*r-1)*16:])
	for i := 0; i < 2*r; i += 2 {
		scryptSalsa(tmp, in[i*16:], out[i*8:])
		scryptSalsa(tmp, in[i*16+16:], out[i*8+r*16:])
	}
}

// scryptSalsa applies the Salsa20/8 core to tmp XOR in and writes the result to
// both tmp and out, matching the scrypt BlockMix state chaining.
func scryptSalsa(tmp *[16]uint32, in, out []uint32) {
	var w [16]uint32
	for i := range w {
		w[i] = tmp[i] ^ in[i]
	}
	x := w
	for i := 0; i < 8; i += 2 {
		x[4] ^= bits.RotateLeft32(x[0]+x[12], 7)
		x[8] ^= bits.RotateLeft32(x[4]+x[0], 9)
		x[12] ^= bits.RotateLeft32(x[8]+x[4], 13)
		x[0] ^= bits.RotateLeft32(x[12]+x[8], 18)

		x[9] ^= bits.RotateLeft32(x[5]+x[1], 7)
		x[13] ^= bits.RotateLeft32(x[9]+x[5], 9)
		x[1] ^= bits.RotateLeft32(x[13]+x[9], 13)
		x[5] ^= bits.RotateLeft32(x[1]+x[13], 18)

		x[14] ^= bits.RotateLeft32(x[10]+x[6], 7)
		x[2] ^= bits.RotateLeft32(x[14]+x[10], 9)
		x[6] ^= bits.RotateLeft32(x[2]+x[14], 13)
		x[10] ^= bits.RotateLeft32(x[6]+x[2], 18)

		x[3] ^= bits.RotateLeft32(x[15]+x[11], 7)
		x[7] ^= bits.RotateLeft32(x[3]+x[15], 9)
		x[11] ^= bits.RotateLeft32(x[7]+x[3], 13)
		x[15] ^= bits.RotateLeft32(x[11]+x[7], 18)

		x[1] ^= bits.RotateLeft32(x[0]+x[3], 7)
		x[2] ^= bits.RotateLeft32(x[1]+x[0], 9)
		x[3] ^= bits.RotateLeft32(x[2]+x[1], 13)
		x[0] ^= bits.RotateLeft32(x[3]+x[2], 18)

		x[6] ^= bits.RotateLeft32(x[5]+x[4], 7)
		x[7] ^= bits.RotateLeft32(x[6]+x[5], 9)
		x[4] ^= bits.RotateLeft32(x[7]+x[6], 13)
		x[5] ^= bits.RotateLeft32(x[4]+x[7], 18)

		x[11] ^= bits.RotateLeft32(x[10]+x[9], 7)
		x[8] ^= bits.RotateLeft32(x[11]+x[10], 9)
		x[9] ^= bits.RotateLeft32(x[8]+x[11], 13)
		x[10] ^= bits.RotateLeft32(x[9]+x[8], 18)

		x[12] ^= bits.RotateLeft32(x[15]+x[14], 7)
		x[13] ^= bits.RotateLeft32(x[12]+x[15], 9)
		x[14] ^= bits.RotateLeft32(x[13]+x[12], 13)
		x[15] ^= bits.RotateLeft32(x[14]+x[13], 18)
	}
	for i := range x {
		x[i] += w[i]
		tmp[i] = x[i]
		out[i] = x[i]
	}
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
