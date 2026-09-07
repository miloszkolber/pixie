package browser

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	appViewPath          = "/v1/app-views"
	appViewTTL           = 5 * time.Minute
	appViewMaxEntries    = 64
	appViewMaxHTMLBytes  = 5 * 1024 * 1024
	appViewMaxCSPDomains = 32
	appViewMaxCSPSource  = 2_048
)

var appViewTicketPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type appViewPolicy struct {
	ParentOrigin string
	CSP          appViewCSP
	Permissions  []string
}

type appViewCSP struct {
	ConnectDomains  []string
	ResourceDomains []string
	FrameDomains    []string
	BaseURIDomains  []string
}

type appViewEntry struct {
	policy    appViewPolicy
	expiresAt time.Time
}

type appViewStore struct {
	mu         sync.Mutex
	entries    map[string]appViewEntry
	now        func() time.Time
	ttl        time.Duration
	maxEntries int
	closed     bool
}

func newAppViewStore() *appViewStore {
	return &appViewStore{entries: make(map[string]appViewEntry), now: time.Now, ttl: appViewTTL, maxEntries: appViewMaxEntries}
}

func (s *appViewStore) pruneLocked(now time.Time) {
	for ticket, entry := range s.entries {
		if !now.Before(entry.expiresAt) {
			delete(s.entries, ticket)
		}
	}
}

func (s *appViewStore) register(policy appViewPolicy) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.pruneLocked(now)
	if s.closed {
		return "", time.Time{}, serviceFailure("shutting_down", "browser service is shutting down", "", http.StatusServiceUnavailable, nil)
	}
	if len(s.entries) >= s.maxEntries {
		return "", time.Time{}, serviceFailure("app_view_limit", "interactive app view limit has been reached", "close an existing app view or wait for it to expire", http.StatusTooManyRequests, nil)
	}
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", time.Time{}, fmt.Errorf("create app view ticket: %w", err)
	}
	ticket := hex.EncodeToString(bytes[:])
	expiresAt := now.Add(s.ttl)
	s.entries[ticket] = appViewEntry{policy: policy, expiresAt: expiresAt}
	return ticket, expiresAt, nil
}

func (s *appViewStore) get(ticket string) (appViewEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	entry, ok := s.entries[ticket]
	return entry, ok
}

func (s *appViewStore) delete(ticket string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	_, found := s.entries[ticket]
	delete(s.entries, ticket)
	return found
}

func (s *appViewStore) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	clear(s.entries)
}

func appViewTicket(path string) (string, bool) {
	if !strings.HasPrefix(path, appViewPath+"/") {
		return "", false
	}
	ticket := strings.TrimPrefix(path, appViewPath+"/")
	return ticket, appViewTicketPattern.MatchString(ticket)
}

func parseAppViewPolicy(value any) (appViewPolicy, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return appViewPolicy{}, invalidAppView("app view registration must contain one JSON object")
	}
	if !onlyKeys(object, "parentOrigin", "csp", "permissions") {
		return appViewPolicy{}, invalidAppView("app view registration contains an unknown field")
	}
	parentOrigin, ok := object["parentOrigin"].(string)
	if !ok {
		return appViewPolicy{}, invalidAppView("parentOrigin is required")
	}
	parentOrigin, err := normalizeAppViewOrigin(parentOrigin)
	if err != nil {
		return appViewPolicy{}, err
	}
	policy := appViewPolicy{ParentOrigin: parentOrigin}
	if raw, exists := object["csp"]; exists {
		policy.CSP, err = parseAppViewCSP(raw)
		if err != nil {
			return appViewPolicy{}, err
		}
	}
	if raw, exists := object["permissions"]; exists {
		policy.Permissions, err = parseAppViewPermissions(raw)
		if err != nil {
			return appViewPolicy{}, err
		}
	}
	return policy, nil
}

func invalidAppView(message string) error {
	return serviceFailure("invalid_app_view", message, "", http.StatusBadRequest, nil)
}

func onlyKeys(object map[string]any, allowed ...string) bool {
	for key := range object {
		if !slices.Contains(allowed, key) {
			return false
		}
	}
	return true
}

func normalizeAppViewOrigin(value string) (string, error) {
	if value == "" || len(value) > appViewMaxCSPSource || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n;,\"'") {
		return "", invalidAppView("parentOrigin must be one absolute HTTP(S) origin")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Hostname() == "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", invalidAppView("parentOrigin must be one absolute HTTP(S) origin")
	}
	hostname := strings.ToLower(parsed.Hostname())
	if strings.Contains(hostname, "*") || !validAppViewHost(hostname) {
		return "", invalidAppView("parentOrigin contains an invalid host")
	}
	port := parsed.Port()
	if port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65_535 {
			return "", invalidAppView("parentOrigin contains an invalid port")
		}
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "http" && port == "80" || scheme == "https" && port == "443" {
		port = ""
	}
	host := hostname
	if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	}
	return scheme + "://" + host, nil
}

func parseAppViewCSP(value any) (appViewCSP, error) {
	object, ok := value.(map[string]any)
	if !ok || !onlyKeys(object, "connectDomains", "resourceDomains", "frameDomains", "baseUriDomains") {
		return appViewCSP{}, invalidAppView("csp must contain only supported domain lists")
	}
	var result appViewCSP
	var err error
	if result.ConnectDomains, err = parseAppViewSources(object["connectDomains"], true); err != nil {
		return appViewCSP{}, err
	}
	if result.ResourceDomains, err = parseAppViewSources(object["resourceDomains"], false); err != nil {
		return appViewCSP{}, err
	}
	if result.FrameDomains, err = parseAppViewSources(object["frameDomains"], false); err != nil {
		return appViewCSP{}, err
	}
	if result.BaseURIDomains, err = parseAppViewSources(object["baseUriDomains"], false); err != nil {
		return appViewCSP{}, err
	}
	return result, nil
}

func parseAppViewSources(value any, allowWebSocket bool) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	values, ok := value.([]any)
	if !ok || len(values) > appViewMaxCSPDomains {
		return nil, invalidAppView("CSP domain lists must be bounded string arrays")
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, raw := range values {
		source, ok := raw.(string)
		if !ok {
			return nil, invalidAppView("CSP domain lists must contain only strings")
		}
		normalized, err := normalizeAppViewCSPSource(source, allowWebSocket)
		if err != nil {
			return nil, err
		}
		if !seen[normalized] {
			seen[normalized] = true
			result = append(result, normalized)
		}
	}
	return result, nil
}

func normalizeAppViewCSPSource(value string, allowWebSocket bool) (string, error) {
	if value == "" || len(value) > appViewMaxCSPSource || strings.TrimSpace(value) != value || strings.ContainsAny(value, " \t\r\n;,\"'") {
		return "", invalidAppView("CSP domains must be individual HTTP(S) origins")
	}
	parsed, err := url.Parse(value)
	allowedScheme := parsed.Scheme == "http" || parsed.Scheme == "https" || allowWebSocket && (parsed.Scheme == "ws" || parsed.Scheme == "wss")
	if err != nil || !allowedScheme || parsed.User != nil || parsed.Hostname() == "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", invalidAppView("CSP domains must be individual HTTP(S) origins")
	}
	hostname := strings.ToLower(parsed.Hostname())
	dnsName := strings.TrimPrefix(hostname, "*.")
	if (strings.Contains(hostname, "*") && dnsName == hostname) || !validAppViewHost(dnsName) {
		return "", invalidAppView("CSP domain contains an invalid host")
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65_535 {
			return "", invalidAppView("CSP domain contains an invalid port")
		}
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), nil
}

func validAppViewHost(host string) bool {
	if host == "localhost" || net.ParseIP(host) != nil {
		return true
	}
	if host == "" || len(host) > 253 || !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, character := range label {
			if character != '-' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
				return false
			}
		}
	}
	return true
}

func parseAppViewPermissions(value any) ([]string, error) {
	object, ok := value.(map[string]any)
	if !ok || !onlyKeys(object, "camera", "microphone", "geolocation", "clipboardWrite") {
		return nil, invalidAppView("permissions contain an unsupported capability")
	}
	result := make([]string, 0, len(object))
	for _, permission := range []string{"camera", "microphone", "geolocation", "clipboardWrite"} {
		if raw, exists := object[permission]; exists {
			settings, ok := raw.(map[string]any)
			if !ok || len(settings) != 0 {
				return nil, invalidAppView("permission values must be empty objects")
			}
			result = append(result, permission)
		}
	}
	return result, nil
}

func appViewCSPHeader(policy appViewPolicy) string {
	resources := strings.Join(policy.CSP.ResourceDomains, " ")
	connections := strings.Join(policy.CSP.ConnectDomains, " ")
	frames := strings.Join(policy.CSP.FrameDomains, " ")
	bases := strings.Join(policy.CSP.BaseURIDomains, " ")
	add := func(prefix, sources string) string {
		if sources == "" {
			return prefix
		}
		return prefix + " " + sources
	}
	frameSource := "frame-src 'none'"
	if frames != "" {
		frameSource = "frame-src " + frames
	}
	baseSource := "base-uri 'self'"
	if bases != "" {
		baseSource = "base-uri " + bases
	}
	connectSource := "connect-src 'none'"
	if connections != "" {
		connectSource = "connect-src " + connections
	}
	fontSource := "font-src 'none'"
	if resources != "" {
		fontSource = "font-src " + resources
	}
	return strings.Join([]string{
		"default-src 'none'",
		add("script-src 'self' 'unsafe-inline'", resources),
		add("style-src 'self' 'unsafe-inline'", resources),
		add("img-src 'self' data:", resources),
		fontSource,
		add("media-src 'self' data:", resources),
		connectSource,
		frameSource,
		"object-src 'none'",
		"form-action 'self'",
		baseSource,
		"frame-ancestors 'self' " + policy.ParentOrigin,
		"sandbox allow-scripts allow-same-origin allow-forms",
	}, "; ")
}

func appViewPermissionsHeader(policy appViewPolicy) string {
	allowed := make(map[string]bool, len(policy.Permissions))
	for _, permission := range policy.Permissions {
		allowed[permission] = true
	}
	directives := make([]string, 0, 4)
	for _, permission := range []struct{ policy, registered string }{
		{policy: "camera", registered: "camera"},
		{policy: "microphone", registered: "microphone"},
		{policy: "geolocation", registered: "geolocation"},
		{policy: "clipboard-write", registered: "clipboardWrite"},
	} {
		value := "()"
		if allowed[permission.registered] {
			value = "(self)"
		}
		directives = append(directives, permission.policy+"="+value)
	}
	return strings.Join(directives, ", ")
}

func sameAppViewOrigin(left, right string) bool {
	leftURL, leftErr := url.Parse(left)
	rightURL, rightErr := url.Parse(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	port := func(value *url.URL) string {
		if value.Port() != "" {
			return value.Port()
		}
		if strings.EqualFold(value.Scheme, "https") {
			return "443"
		}
		return "80"
	}
	return strings.EqualFold(leftURL.Scheme, rightURL.Scheme) &&
		strings.EqualFold(leftURL.Hostname(), rightURL.Hostname()) &&
		port(leftURL) == port(rightURL)
}

func renderAppView(policy appViewPolicy) (string, error) {
	parent, err := json.Marshal(policy.ParentOrigin)
	if err != nil {
		return "", err
	}
	permissions, err := json.Marshal(policy.Permissions)
	if err != nil {
		return "", err
	}
	return strings.NewReplacer(
		"__EXPECTED_PARENT_ORIGIN__", string(parent),
		"__ALLOWED_PERMISSIONS__", string(permissions),
		"__MAX_HTML_BYTES__", fmt.Sprintf("%d", appViewMaxHTMLBytes),
	).Replace(appViewDocument), nil
}

const appViewDocument = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="referrer" content="no-referrer">
<meta name="color-scheme" content="light dark">
<title>Pixie App Sandbox</title>
<style>html,body,iframe{border:0;box-sizing:border-box;height:100%;margin:0;padding:0;width:100%}body{background:transparent;overflow:hidden}iframe{display:block}</style>
</head>
<body>
<script>
(() => {
  "use strict";
  // This proxy and the app resource share the dedicated browser sandbox
  // origin, as required by MCP Apps. It must therefore hold no credential or
  // authority; the host validates every relayed JSON-RPC method.
  const expectedParentOrigin = __EXPECTED_PARENT_ORIGIN__;
  const ownOrigin = window.location.origin;
  const allowedPermissions = new Set(__ALLOWED_PERMISSIONS__);
  const maxHTMLBytes = __MAX_HTML_BYTES__;
  const resourceReady = "ui/notifications/sandbox-resource-ready";
  const proxyReady = "ui/notifications/sandbox-proxy-ready";
  let inner = null;
  let resourceLoaded = false;

  function fail(message) {
    if (inner) inner.remove();
    inner = null;
    document.body.textContent = "";
    const alert = document.createElement("div");
    alert.setAttribute("role", "alert");
    alert.style.cssText = "box-sizing:border-box;height:100%;padding:16px;color:CanvasText;background:transparent;font:13px system-ui,sans-serif";
    alert.textContent = message;
    document.body.appendChild(alert);
  }

  function loadResource(params) {
    if (resourceLoaded) return;
    resourceLoaded = true;
    const html = params && params.html;
    if (typeof html !== "string" || html.length > maxHTMLBytes || new TextEncoder().encode(html).byteLength > maxHTMLBytes) {
      fail("Unable to load this app resource.");
      return;
    }
    inner = document.createElement("iframe");
    inner.setAttribute("sandbox", "allow-scripts allow-same-origin allow-forms");
    inner.referrerPolicy = "no-referrer";
    const allow = [];
    if (allowedPermissions.has("camera")) allow.push("camera");
    if (allowedPermissions.has("microphone")) allow.push("microphone");
    if (allowedPermissions.has("geolocation")) allow.push("geolocation");
    if (allowedPermissions.has("clipboardWrite")) allow.push("clipboard-write");
    if (allow.length) inner.setAttribute("allow", allow.join("; "));
    document.body.appendChild(inner);
    const target = inner.contentDocument || (inner.contentWindow && inner.contentWindow.document);
    if (!target) {
      fail("Unable to initialize this app resource.");
      return;
    }
    target.open();
    target.write(html);
    target.close();
    target.addEventListener("keydown", (event) => {
      if (event.isTrusted && event.key === "Escape" && !event.defaultPrevented) {
        window.parent.postMessage({jsonrpc:"2.0",method:"ui/notifications/request-teardown",params:{}}, expectedParentOrigin);
      }
    });
  }

  function isReservedSandboxMessage(data) {
    return typeof data.method === "string" && data.method.startsWith("ui/notifications/sandbox-");
  }

  window.addEventListener("message", (event) => {
    if (event.source === window.parent) {
      if (event.origin !== expectedParentOrigin || !event.data || typeof event.data !== "object") return;
      if (event.data.method === resourceReady) {
        loadResource(event.data.params || {});
      } else if (!isReservedSandboxMessage(event.data) && inner && inner.contentWindow) {
        inner.contentWindow.postMessage(event.data, ownOrigin);
      }
      return;
    }
    if (!inner || event.source !== inner.contentWindow || event.origin !== ownOrigin || !event.data || typeof event.data !== "object") return;
    if (!isReservedSandboxMessage(event.data)) window.parent.postMessage(event.data, expectedParentOrigin);
  });

  window.parent.postMessage({jsonrpc:"2.0",method:proxyReady,params:{}}, expectedParentOrigin);
})();
</script>
</body>
</html>`

func (a *app) registerAppView(response http.ResponseWriter, request *http.Request) {
	if !a.config.Authentication || !a.authorized(request) {
		writeJSON(response, http.StatusUnauthorized, map[string]any{"outcome": "rejected", "code": "unauthorized"}, nil)
		return
	}
	if err := requireJSONContentType(request); err != nil {
		respondError(response, err)
		return
	}
	publicOrigin, err := a.appViewPublicOrigin(request)
	if err != nil {
		respondError(response, err)
		return
	}
	body, err := decodeJSONBody(request)
	if err != nil {
		respondError(response, err)
		return
	}
	policy, err := parseAppViewPolicy(body)
	if err != nil {
		respondError(response, err)
		return
	}
	if sameAppViewOrigin(policy.ParentOrigin, publicOrigin) {
		respondError(response, invalidAppView("parentOrigin must differ from the browser sandbox origin"))
		return
	}
	ticket, expiresAt, err := a.appViews.register(policy)
	if err != nil {
		respondError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{
		"ticket":    ticket,
		"path":      appViewPath + "/" + ticket,
		"url":       publicOrigin + appViewPath + "/" + ticket,
		"expiresAt": expiresAt.UTC().Format(time.RFC3339Nano),
	}, nil)
}

func (a *app) appViewPublicOrigin(request *http.Request) (string, error) {
	if a.config.PublicOrigin != "" {
		return a.config.PublicOrigin, nil
	}
	if !a.expectedMCPHost(request) {
		return "", serviceFailure("invalid_app_view_origin", "app view registration used an unexpected Host", "configure PIXIE_BROWSER_PUBLIC_ORIGIN for a reverse proxy", http.StatusBadRequest, nil)
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	origin, ok := normalizedBrowserOrigin(scheme + "://" + request.Host)
	if !ok {
		return "", serviceFailure("invalid_app_view_origin", "app view registration used an invalid Host", "configure PIXIE_BROWSER_PUBLIC_ORIGIN", http.StatusBadRequest, nil)
	}
	return origin, nil
}

func (a *app) serveAppView(response http.ResponseWriter, request *http.Request, ticket string) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if !a.expectedMCPHost(request) {
		http.NotFound(response, request)
		return
	}
	entry, ok := a.appViews.get(ticket)
	if !ok {
		http.NotFound(response, request)
		return
	}
	document, err := renderAppView(entry.policy)
	if err != nil {
		http.Error(response, "app view unavailable", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Security-Policy", appViewCSPHeader(entry.policy))
	response.Header().Set("Permissions-Policy", appViewPermissionsHeader(entry.policy))
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte(document))
}

func (a *app) deleteAppView(response http.ResponseWriter, request *http.Request, ticket string) {
	if !a.config.Authentication || !a.authorized(request) {
		writeJSON(response, http.StatusUnauthorized, map[string]any{"outcome": "rejected", "code": "unauthorized"}, nil)
		return
	}
	if !a.appViews.delete(ticket) {
		writeJSON(response, http.StatusNotFound, map[string]any{"outcome": "rejected", "code": "not_found"}, nil)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true}, nil)
}
