package controller_test

import (
	"crypto/tls"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

// AUX-20 coverage for the optional authenticated-mode hardening: persisted
// signing secret, scrypt password hashing, per-client lockout, inactivity
// timeout, and strict Fetch Metadata/Host checks. The expected values below
// come from RFC 7914 and from the published behavior of the auth boundary, not
// from the implementation under test.

// fakeClock advances the Auth clock deterministically so lockout and inactivity
// expiry are tested without sleeps.
type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Advance(duration time.Duration) { c.now = c.now.Add(duration) }

// TestScryptKeyMatchesRFC7914Vectors independently verifies the no-dependency
// scrypt implementation against the RFC 7914 test vectors.
func TestScryptKeyMatchesRFC7914Vectors(t *testing.T) {
	for _, test := range []struct {
		name     string
		password string
		salt     string
		n        int
		r        int
		p        int
		keyLen   int
		wantHex  string
	}{
		{
			name: "rfc 7914 empty password and salt", password: "", salt: "", n: 16, r: 1, p: 1, keyLen: 64,
			wantHex: "77d6576238657b203b19ca42c18a0497f16b4844e3074ae8dfdffa3fede21442fcd0069ded0948f8326a753a0fc81f17e8d3e0fb2e0d3628cf35e20c38d18906",
		},
		{
			name: "rfc 7914 short password and salt", password: "p", salt: "s", n: 2, r: 1, p: 1, keyLen: 16,
			wantHex: "48b0d2a8a3272611984c50ebd630af52",
		},
		{
			name: "rfc 7914 password and salt", password: "password", salt: "salt", n: 2, r: 10, p: 10, keyLen: 32,
			wantHex: "482c858e229055e62f41e0ec819a5ee18bdb87251a534f75acd95ac5e50aa15f",
		},
		{
			name: "rfc 7914 NaCl", password: "password", salt: "NaCl", n: 1024, r: 8, p: 16, keyLen: 64,
			wantHex: "fdbabe1c9d3472007856e7190d01e9fe7c6ad7cbc8237830e77376634b3731622eaf30d92e22a3886ff109279d9830dac727afb94a83ee6d8360cbdfa2cc0640",
		},
		{
			name: "rfc 7914 SodiumChloride", password: "pleaseletmein", salt: "SodiumChloride", n: 16384, r: 8, p: 1, keyLen: 64,
			wantHex: "7023bdcb3afd7348461c06cd81fd38ebfda8fbba904f8e3ea9b543f6545da1f2d5432955613f0fcf62d49705242a9af9e61e85dc0d651e40dfcf017b45575887",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			derived, err := controller.ScryptKey([]byte(test.password), []byte(test.salt), test.n, test.r, test.p, test.keyLen)
			if err != nil {
				t.Fatalf("ScryptKey returned an error: %v", err)
			}
			if actual := hex.EncodeToString(derived); actual != test.wantHex {
				t.Fatalf("scrypt output mismatch:\n got %s\nwant %s", actual, test.wantHex)
			}
		})
	}
	for _, n := range []int{0, 1, 3} {
		if _, err := controller.ScryptKey([]byte("password"), []byte("salt"), n, 1, 1, 16); err == nil {
			t.Fatalf("non-power-of-two N=%d was accepted", n)
		}
	}
	if _, err := controller.ScryptKey([]byte("password"), []byte("salt"), 1<<20, 32, 1, 16); err == nil {
		t.Fatal("scrypt parameters above the memory budget were accepted")
	}
}

// TestPasswordHashesRecordParametersAndVerifyConstantly checks the encoded
// credential format, the stored cost parameters, and wrong/malformed rejection.
func TestPasswordHashesRecordParametersAndVerifyConstantly(t *testing.T) {
	const password = "correct horse battery staple"
	encoded, err := controller.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, controller.PasswordHashPrefix+"$") {
		t.Fatalf("password hash does not record its scheme: %q", encoded)
	}
	if !controller.PasswordHashValid(encoded) {
		t.Fatalf("generated password hash is not valid: %q", encoded)
	}
	if !controller.VerifyPassword(encoded, password) {
		t.Fatal("correct password was rejected")
	}
	if controller.VerifyPassword(encoded, password+" ") {
		t.Fatal("wrong password was accepted")
	}
	for _, malformed := range []string{
		"",
		"plaintext",
		"scrypt$16384$8$1$",
		"scrypt$notanumber$8$1$AAAA$BBBB",
		"scrypt$16384$8$1$not-base64$also-not",
		"scrypt$0$8$1$AAAA$BBBB",
	} {
		if controller.PasswordHashValid(malformed) {
			t.Fatalf("malformed hash %q was accepted", malformed)
		}
		if controller.VerifyPassword(malformed, password) {
			t.Fatalf("malformed hash %q verified", malformed)
		}
	}
	second, err := controller.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if second == encoded {
		t.Fatal("two password hashes reused the same salt")
	}
	if _, err := controller.HashPassword(""); err == nil {
		t.Fatal("empty password was hashed")
	}
}

// TestLoginLockoutIsPerClientIsolated proves a failed-login lockout keyed by
// client address, that a correct credential is still refused during lockout,
// that another client is unaffected, and that the lockout expires.
func TestLoginLockoutIsPerClientIsolated(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1700000000, 0)}
	auth, err := controller.NewAuthWithOptions(controller.AuthOptions{
		Token:             trustControllerToken,
		SigningSecret:     "signing-secret-0123456789abcdef0123456789",
		LoginAttemptLimit: 3,
		LoginLockout:      5 * time.Minute,
		Now:               clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	const (
		firstClient  = "198.51.100.1"
		secondClient = "203.0.113.9"
	)
	for range 2 {
		if _, ok := auth.LoginWithClient(firstClient, "wrong"); ok {
			t.Fatal("wrong credential was accepted")
		}
	}
	if lockedUntil, locked := auth.LockedUntil(firstClient); locked {
		t.Fatalf("client locked before reaching the attempt limit: %v", lockedUntil)
	}
	if _, ok := auth.LoginWithClient(firstClient, trustControllerToken); !ok {
		t.Fatal("correct credential was rejected before the attempt limit")
	}
	for range 3 {
		auth.LoginWithClient(secondClient, "wrong")
	}
	lockedUntil, locked := auth.LockedUntil(secondClient)
	if !locked || !lockedUntil.After(clock.now) {
		t.Fatalf("client was not locked after the attempt limit: %v, %v", lockedUntil, locked)
	}
	if _, ok := auth.LoginWithClient(secondClient, trustControllerToken); ok {
		t.Fatal("correct credential was accepted during lockout")
	}
	if _, ok := auth.LoginWithClient(firstClient, trustControllerToken); !ok {
		t.Fatal("one client's lockout affected another client")
	}
	clock.Advance(5*time.Minute + time.Second)
	if _, locked := auth.LockedUntil(secondClient); locked {
		t.Fatal("lockout did not expire")
	}
	if _, ok := auth.LoginWithClient(secondClient, trustControllerToken); !ok {
		t.Fatal("correct credential was rejected after lockout expiry")
	}
}

// TestInactivityTimeoutExpiresIdleSessions proves the sliding timeout expires
// an idle session, is refreshed by activity, and lets a persisted-signing-secret
// session survive a host restart.
func TestInactivityTimeoutExpiresIdleSessions(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1700000000, 0)}
	const signingSecret = "signing-secret-0123456789abcdef0123456789"
	auth, err := controller.NewAuthWithOptions(controller.AuthOptions{
		Token:             trustControllerToken,
		SigningSecret:     signingSecret,
		InactivityTimeout: 30 * time.Minute,
		Now:               clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, ok := auth.Login(trustControllerToken)
	if !ok {
		t.Fatal("login failed")
	}
	if _, ok := auth.SessionExpiresAt(session); !ok {
		t.Fatal("fresh session was rejected")
	}
	clock.Advance(29 * time.Minute)
	if _, ok := auth.SessionExpiresAt(session); !ok {
		t.Fatal("session expired before the inactivity window")
	}
	clock.Advance(29 * time.Minute)
	if _, ok := auth.SessionExpiresAt(session); !ok {
		t.Fatal("activity did not refresh the inactivity window")
	}
	clock.Advance(31 * time.Minute)
	if _, ok := auth.SessionExpiresAt(session); ok {
		t.Fatal("idle session outlived the inactivity window")
	}
	if _, ok := auth.SessionExpiresAt(session); ok {
		t.Fatal("expired session was accepted again")
	}

	// A fresh Auth instance shares only the persisted signing secret, so the
	// session must remain valid after a simulated restart.
	restarted, err := controller.NewAuthWithOptions(controller.AuthOptions{
		Token:             trustControllerToken,
		SigningSecret:     signingSecret,
		InactivityTimeout: 30 * time.Minute,
		Now:               clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	reissued, ok := auth.Login(trustControllerToken)
	if !ok {
		t.Fatal("second login failed")
	}
	if _, ok := restarted.SessionExpiresAt(reissued); !ok {
		t.Fatal("persisted signing secret did not survive a restart")
	}
}

// TestStrictFetchMetadataAndHostRejectUnsafeWrites covers the Fetch Metadata
// and strict-Host additions: unsafe methods need an explicit same-origin fetch
// site, navigation/no-cors writes are refused, and a remote peer cannot claim
// the loopback authority.
func TestStrictFetchMetadataAndHostRejectUnsafeWrites(t *testing.T) {
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
	request := func(method, host, remoteAddr, fetchSite, fetchMode string) *http.Request {
		t.Helper()
		request := httptest.NewRequest(method, publicOrigin+"/auth/status", nil)
		request.Host = host
		request.RemoteAddr = remoteAddr
		request.TLS = &tls.ConnectionState{}
		request.Header.Set("Origin", publicOrigin)
		if fetchSite != "" {
			request.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		if fetchMode != "" {
			request.Header.Set("Sec-Fetch-Mode", fetchMode)
		}
		request.AddCookie(&http.Cookie{Name: controller.AuthCookieName, Value: session})
		return request
	}
	const remoteAddr = "192.0.2.10:53000"
	if !config.IsStrictAuthorizedHTTPRequest(request(http.MethodGet, "pixie.example", remoteAddr, "", ""), auth) {
		t.Fatal("safe remote GET with the exact origin was rejected")
	}
	if !config.IsStrictAuthorizedHTTPRequest(request(http.MethodPost, "pixie.example", remoteAddr, "same-origin", "cors"), auth) {
		t.Fatal("same-origin remote POST was rejected")
	}
	// The base check still admits a credentialed same-origin write with no
	// Fetch Metadata; the strict check is the deliberate delta.
	if !config.IsAuthorizedHTTPRequest(request(http.MethodPost, "pixie.example", remoteAddr, "", ""), auth) {
		t.Fatal("base authorization changed for a credentialed write")
	}
	for _, test := range []struct {
		name      string
		fetchSite string
		fetchMode string
	}{
		{name: "missing fetch site", fetchSite: "", fetchMode: ""},
		{name: "cross-site fetch site", fetchSite: "cross-site", fetchMode: "cors"},
		{name: "same-site fetch site", fetchSite: "same-site", fetchMode: "cors"},
		{name: "navigation write", fetchSite: "same-origin", fetchMode: "navigate"},
		{name: "no-cors write", fetchSite: "same-origin", fetchMode: "no-cors"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if config.IsStrictAuthorizedHTTPRequest(request(http.MethodPost, "pixie.example", remoteAddr, test.fetchSite, test.fetchMode), auth) {
				t.Fatalf("%s was admitted by the strict check", test.name)
			}
		})
	}

	remoteLoopback := request(http.MethodGet, "127.0.0.1:7312", remoteAddr, "same-origin", "")
	if !config.IsAllowedAuthority(remoteLoopback) {
		t.Fatal("base authority no longer admits the loopback host form")
	}
	if config.IsStrictAllowedAuthority(remoteLoopback) {
		t.Fatal("remote peer was allowed to claim the loopback authority")
	}
	loopbackPeer := request(http.MethodGet, "127.0.0.1:7312", "127.0.0.1:53001", "same-origin", "")
	if !config.IsStrictAllowedAuthority(loopbackPeer) {
		t.Fatal("loopback peer was rejected for the loopback authority")
	}
	if !config.IsStrictAuthorizedHTTPRequest(loopbackPeer, auth) {
		t.Fatal("loopback service request was rejected by the strict check")
	}
}

// TestReadAuthConfigParsesOptionalHardening covers the environment contract:
// sane defaults in the optional authenticated mode, explicit overrides,
// disabled controls, and fail-closed rejection of malformed values.
func TestReadAuthConfigParsesOptionalHardening(t *testing.T) {
	base := map[string]string{
		"PIXIE_AUTH_ENABLED": "true",
		"PIXIE_TOKEN":        trustControllerToken,
		"PIXIE_MCP_TOKEN":    "mcp-token-0123456789abcdef0123456789",
	}
	read := func(overrides map[string]string) (controller.AuthConfig, error) {
		t.Helper()
		values := make(map[string]string, len(base)+len(overrides))
		for key, value := range base {
			values[key] = value
		}
		for key, value := range overrides {
			values[key] = value
		}
		return controller.ReadAuthConfig(func(key string) string { return values[key] })
	}
	config, err := read(nil)
	if err != nil {
		t.Fatal(err)
	}
	if config.Hardening.LoginAttemptLimit != 8 || config.Hardening.LoginLockout != 15*time.Minute || config.Hardening.InactivityTimeout != 12*time.Hour {
		t.Fatalf("unexpected hardening defaults: %#v", config.Hardening)
	}
	overridden, err := read(map[string]string{
		"PIXIE_AUTH_LOGIN_ATTEMPT_LIMIT": "3",
		"PIXIE_AUTH_LOGIN_LOCKOUT":       "1m",
		"PIXIE_AUTH_INACTIVITY_TIMEOUT":  "2h",
	})
	if err != nil {
		t.Fatal(err)
	}
	if overridden.Hardening.LoginAttemptLimit != 3 || overridden.Hardening.LoginLockout != time.Minute || overridden.Hardening.InactivityTimeout != 2*time.Hour {
		t.Fatalf("overrides were not applied: %#v", overridden.Hardening)
	}
	disabled, err := read(map[string]string{
		"PIXIE_AUTH_LOGIN_ATTEMPT_LIMIT": "off",
		"PIXIE_AUTH_LOGIN_LOCKOUT":       "off",
		"PIXIE_AUTH_INACTIVITY_TIMEOUT":  "off",
	})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Hardening.LoginAttemptLimit != 0 || disabled.Hardening.LoginLockout != 0 || disabled.Hardening.InactivityTimeout != 0 {
		t.Fatalf("disabled controls were not disabled: %#v", disabled.Hardening)
	}
	for name, overrides := range map[string]map[string]string{
		"negative attempt limit": {"PIXIE_AUTH_LOGIN_ATTEMPT_LIMIT": "-1"},
		"bad duration":           {"PIXIE_AUTH_INACTIVITY_TIMEOUT": "soon"},
		"weak signing secret":    {"PIXIE_AUTH_SIGNING_SECRET": "short"},
		"malformed password":     {"PIXIE_AUTH_PASSWORD_HASH": "not-a-hash"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := read(overrides); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

// TestPersistedAuthSecretBehavesLikeAHostKey proves the signing secret is
// generated once, reused, written with owner-only permissions, and fails closed
// when the persisted file is corrupt.
func TestPersistedAuthSecretBehavesLikeAHostKey(t *testing.T) {
	dir := t.TempDir()
	first, err := persist.LoadOrCreateAuthSecret(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 32 {
		t.Fatalf("generated signing secret is too short: %d bytes", len(first))
	}
	second, err := persist.LoadOrCreateAuthSecret(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("persisted signing secret changed between loads")
	}
	info, err := os.Stat(filepath.Join(dir, persist.AuthSecretFileName))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("signing secret permissions are %o, want 600", perm)
	}
	if err := os.WriteFile(filepath.Join(dir, persist.AuthSecretFileName), []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := persist.LoadOrCreateAuthSecret(dir); err == nil {
		t.Fatal("a corrupt signing secret was silently replaced")
	}
}

// TestNewAuthFromConfigUsesPersistedPasswordAndSecret proves the config-level
// wiring: a stored scrypt password replaces the raw token for login while the
// generated signing secret keeps the session valid across a restart.
func TestNewAuthFromConfigUsesPersistedPasswordAndSecret(t *testing.T) {
	dir := t.TempDir()
	const password = "stored-login-password"
	encoded, err := controller.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := persist.WritePasswordHash(dir, encoded); err != nil {
		t.Fatal(err)
	}
	readBack, ok, err := persist.ReadPasswordHash(dir)
	if err != nil || !ok || readBack != encoded {
		t.Fatalf("stored password hash did not round-trip: ok=%v err=%v value=%q", ok, err, readBack)
	}
	config := controller.AuthConfig{
		Enabled:         true,
		ControllerToken: trustControllerToken,
		ControllerPort:  7312,
		PublicOrigin:    "http://127.0.0.1:7312",
		Hardening: controller.AuthHardening{
			LoginAttemptLimit: 4,
			LoginLockout:      time.Minute,
			InactivityTimeout: time.Hour,
		},
	}
	auth, err := controller.NewAuthFromConfig(config, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := auth.Login(trustControllerToken); ok {
		t.Fatal("raw token logged in while a stored password hash is configured")
	}
	if _, ok := auth.Login(password); !ok {
		t.Fatal("stored password was rejected")
	}
	session, ok := auth.Login(password)
	if !ok {
		t.Fatal("stored password login failed")
	}
	restarted, err := controller.NewAuthFromConfig(config, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := restarted.SessionExpiresAt(session); !ok {
		t.Fatal("session did not survive a restart with the persisted signing secret")
	}
	if err := persist.WritePasswordHash(dir, "not-a-hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.NewAuthFromConfig(config, dir); err == nil {
		t.Fatal("an invalid persisted password hash did not fail closed")
	}
}
