package controller

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/miloszkolber/pixie/internal/mcpserver"
	"github.com/miloszkolber/pixie/internal/workspace"
)

const (
	maxAuthBodyBytes     = 4_096
	projectImageMaxBytes = 16 * 1024 * 1024
	immutableCachePolicy = "public, max-age=31536000, immutable"
)

var (
	inlineScriptPattern = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script\s*>`)
	scriptSourcePattern = regexp.MustCompile(`(?i)(?:^|\s)src\s*=`)
)

type HTTPHandler struct {
	WebSocket     *WebSocketServer
	Objective     ObjectiveHandler
	Projects      *workspace.Projects
	Files         *workspace.Files
	Auth          AuthConfig
	auth          *Auth
	StaticDir     string
	static        staticFiles
	Ready         http.HandlerFunc
	MCPRegistry   http.Handler
	browserClient *http.Client
	// MODULE-01 module/scope wiring: scope authority plus trusted
	// descriptors gate module resource requests before any module service
	// delegation. Host/Origin/proxy handling stays untouched.
	ModuleScopes      *mcpserver.ScopeAuthority
	ModuleDescriptors map[string]mcpserver.FrontendDescriptor
	ModuleService     http.Handler
	// SessionRecords is the controller-owned project/session association used
	// before delegating Canvas management requests. A route ID alone is never
	// treated as permission.
	SessionRecords *SessionRecords
}

// inProcessBrowserHandler exposes the merged publisher's Browser REST surface
// for panel and artifact traffic when no external PIXIE_BROWSER_URL is set.
func (h *HTTPHandler) inProcessBrowserHandler() http.Handler {
	if legacy, ok := h.MCPRegistry.(interface{ BrowserLegacyHandler() func() http.Handler }); ok {
		if handler := legacy.BrowserLegacyHandler(); handler != nil {
			return handler()
		}
	}
	return nil
}

func NewHTTPHandler(webSocket *WebSocketServer, objective ObjectiveHandler, projects *workspace.Projects, files *workspace.Files, authConfig AuthConfig, staticDir string, ready http.HandlerFunc) (*HTTPHandler, error) {
	result := &HTTPHandler{WebSocket: webSocket, Objective: objective, Projects: projects, Files: files, Auth: authConfig, StaticDir: staticDir, static: resolveStaticFiles(staticDir), Ready: ready, browserClient: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	// MODULE-01 module/scope wiring: default to the committed descriptor
	// fixtures with a fresh in-memory scope authority.
	result.ModuleScopes = mcpserver.NewScopeAuthority()
	result.ModuleDescriptors = mcpserver.DefaultModuleDescriptors()
	if authConfig.Enabled {
		auth, err := NewAuth(authConfig.ControllerToken)
		if err != nil {
			return nil, err
		}
		result.auth = auth
	}
	return result, nil
}

func (h *HTTPHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	// Liveness/readiness are intentionally kept usable by service managers and
	// container health checks. Every application, API, module, file/static and
	// authenticated service route is admitted only after the independent Host
	// authority check. Origin and credentials remain additional route checks.
	if !isHealthRoute(request) && (!h.Auth.IsAllowedAuthority(request) || !h.Auth.IsAllowedTransport(request)) {
		http.Error(response, "forbidden", http.StatusForbidden)
		return
	}
	// Normalize once so "/api", "//api/unknown", "/./mcp/unknown" and similar
	// encodings resolve to the same reserved namespace before the static
	// fallback. The static fallback stays only for frontend navigation.
	route := "/"
	if request.URL != nil {
		route = normalizeRoutePath(request.URL.Path)
	}
	switch {
	case route == "/mcp/objective":
		h.Objective.ServeHTTP(response, request)
	case h.MCPRegistry != nil && isManagementOwnedRoute(h.MCPRegistry, route):
		// Human management/artifact routes use the controller cookie/session
		// authority, not the model-facing MCP bearer. The module service applies
		// its own finer reader-versus-management policy after this boundary.
		if !h.Auth.IsAuthorizedHTTPRequest(request, h.auth) {
			writeAuthJSON(response, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		// Controller credentials terminate at this boundary. The registry and
		// owning module receive only the verified internal scope, never the
		// browser cookie or an unrelated Authorization header.
		request = request.Clone(request.Context())
		request.Header.Del("Cookie")
		request.Header.Del("Authorization")
		// Secrets never travel in management URLs on any module surface.
		if canvasCredentialQuery(request) {
			writeAuthJSON(response, http.StatusBadRequest, map[string]string{"error": "credentials are not accepted in management URLs"})
			return
		}
		if definition, ok := managementOwningDefinition(h.MCPRegistry, route); ok && definition.SessionScoped {
			// Session-scoped management (currently Canvas) resolves the
			// project/session target from the route but treats it as a
			// claim, not permission: verifiedCanvasManagementScope checks
			// both IDs against the controller's durable association.
			// Instance-wide modules (Design, fixtures) skip this block and
			// delegate directly after the cookie boundary above.
			managementPrefix := ""
			if len(definition.ManagementPaths) > 0 {
				managementPrefix = definition.ManagementPaths[0]
			}
			target, parsed := parseScopedManagementRoute(managementPrefix, route, request)
			// Keep the historical unscoped status health alias for callers that
			// do not request retained session data. A status request carrying an
			// explicit project/session target takes the management path below.
			if managementPrefix != "" && route == managementPrefix+"/status" && !parsed && h.SessionRecords == nil {
				h.serveMCPRegistry(response, request, route)
				return
			}
			if !parsed {
				writeAuthJSON(response, http.StatusNotFound, map[string]string{"error": "not found"})
				return
			}
			resolvedTarget, verified := h.verifiedCanvasManagementScope(target)
			if !verified {
				writeAuthJSON(response, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			request = request.WithContext(mcpserver.ContextWithManagementScope(request.Context(), mcpserver.ManagementScope{ProjectID: resolvedTarget.projectID, SessionID: resolvedTarget.sessionID}))
		}
		h.serveMCPRegistry(response, request, route)
	case h.MCPRegistry != nil && mcpPublisherRoute(route):
		// Session-scoped modules carry a per-native-session capability in
		// their Bearer header; the registry and owning handler validate that
		// credential. Requiring the publisher-wide MCP bearer here would make
		// scoped routes unusable whenever PIXIE_MCP_TOKEN is configured.
		// Other module MCP surfaces keep the publisher credential at this
		// top-level boundary. The scoped decision consumes only the owning
		// definition's flag, never a module-name comparison.
		if isSessionScopedMCPOwnedRoute(h.MCPRegistry, route) {
			if !h.isAllowedSessionMCPRequest(request) {
				writeAuthJSON(response, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
		} else {
			if !mcpPublisherAuthConfigured(h.Auth) {
				writeAuthJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "MCP publisher authentication is not configured"})
				return
			}
			if !h.isAuthorizedMCPRequest(request) {
				writeAuthJSON(response, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
		}
		h.serveMCPRegistry(response, request, route)
	case strings.HasPrefix(route, "/auth/"):
		h.serveAuth(response, request)
	case route == "/ws":
		h.WebSocket.ServeHTTP(response, request)
	case route == "/health" || route == "/livez":
		serveHealth(response, request)
	case route == "/readyz":
		if request.Method != http.MethodGet {
			methodNotAllowed(response, http.MethodGet)
		} else if h.Ready == nil {
			http.Error(response, "not ready", http.StatusServiceUnavailable)
		} else {
			h.Ready(response, request)
		}
	case strings.HasPrefix(route, "/files/"):
		h.serveProjectImage(response, request)
	case strings.HasPrefix(route, "/v1/artifacts/"):
		h.serveBrowserArtifact(response, request)
	case mcpserver.IsModuleResourceRoute(route):
		h.serveModuleResource(response, request)
	case reservedAPIMCPRoute(route):
		writeAuthJSON(response, http.StatusNotFound, map[string]string{"error": "not found"})
	default:
		h.serveStatic(response, request)
	}
}

// serveMCPRegistry presents the normalized route to the registered owner.
// This keeps duplicate-slash and dot-segment variants inside the reserved API
// boundary instead of letting a module or the SPA interpret the raw path.
func (h *HTTPHandler) serveMCPRegistry(response http.ResponseWriter, request *http.Request, route string) {
	if request == nil || request.URL == nil || request.URL.Path == route {
		h.MCPRegistry.ServeHTTP(response, request)
		return
	}
	clone := request.Clone(request.Context())
	urlCopy := *request.URL
	urlCopy.Path, urlCopy.RawPath = route, ""
	clone.URL = &urlCopy
	clone.RequestURI = route
	if request.URL.RawQuery != "" {
		clone.RequestURI += "?" + request.URL.RawQuery
	}
	h.MCPRegistry.ServeHTTP(response, clone)
}

func canvasCredentialQuery(request *http.Request) bool {
	if request == nil || request.URL == nil {
		return false
	}
	for key := range request.URL.Query() {
		switch strings.ToLower(key) {
		case "token", "access_token", "authorization", "bearer", "credential", "credentials":
			return true
		}
	}
	return false
}

// isManagementOwnedRoute reports management ownership using the live
// registry's own definitions when available (including test fixtures),
// falling back to the compiled defaults. One ownership rule, no module-name
// branch.
func isManagementOwnedRoute(registry http.Handler, route string) bool {
	if scoped, ok := registry.(*mcpserver.Registry); ok && scoped != nil {
		return scoped.IsManagementRoute(route)
	}
	return mcpserver.IsRegisteredManagementRoute(route)
}

// managementOwningDefinition resolves the owning management definition through
// the live registry when available, otherwise through the compiled defaults.
func managementOwningDefinition(registry http.Handler, route string) (mcpserver.ModuleDefinition, bool) {
	if scoped, ok := registry.(*mcpserver.Registry); ok && scoped != nil {
		return scoped.DefinitionForManagementRoute(route)
	}
	return mcpserver.DefinitionForManagementRoute(mcpserver.DefaultModuleDefinitions(), route)
}

// isSessionScopedMCPOwnedRoute reports session-scoped MCP ownership through
// the live registry when available, otherwise through the compiled defaults.
func isSessionScopedMCPOwnedRoute(registry http.Handler, route string) bool {
	if scoped, ok := registry.(*mcpserver.Registry); ok && scoped != nil {
		if definition, found := scoped.DefinitionForRoute(route); found {
			// Only MCP routes carry the session-capability policy; a
			// management prefix that happens to resolve here must not inherit
			// it.
			if definition.Path != "" && (route == definition.Path || len(route) > len(definition.Path) && len(definition.Path) > 0 && route[:len(definition.Path)] == definition.Path && route[len(definition.Path)] == '/') {
				return definition.SessionScoped
			}
			return false
		}
		return false
	}
	return mcpserver.IsSessionScopedMCPRoute(route)
}

type canvasManagementTarget struct {
	projectID string
	sessionID string
}

// parseScopedManagementRoute accepts the operation-first public form and the
// equivalent target-first form used by older UI adapters for any
// session-scoped management prefix. Neither form is authority:
// verifiedCanvasManagementScope still checks both IDs against the
// controller's durable project/session association. Prefix-driven, never a
// Canvas-name branch.
func parseCanvasManagementRoute(route string, requests ...*http.Request) (canvasManagementTarget, bool) {
	return parseScopedManagementRoute(mcpserver.CanvasAPIPrefix, route, requests...)
}

// parseScopedManagementRoute accepts the operation-first public form and the
// equivalent target-first form used by older UI adapters for any
// session-scoped management prefix.
func parseScopedManagementRoute(prefix, route string, requests ...*http.Request) (canvasManagementTarget, bool) {
	if prefix == "" || route == "" || (route != prefix && !strings.HasPrefix(route, prefix+"/")) {
		return canvasManagementTarget{}, false
	}
	suffix := strings.Trim(strings.TrimPrefix(route, prefix), "/")
	if suffix == "" {
		return canvasManagementTarget{}, false
	}
	parts := strings.Split(suffix, "/")
	decode := func(value string) (string, bool) {
		decoded, err := url.PathUnescape(value)
		if err != nil || decoded == "" || strings.ContainsAny(decoded, "/\\?#") {
			return "", false
		}
		for _, character := range decoded {
			if character < 0x20 || character == 0x7f {
				return "", false
			}
		}
		return decoded, true
	}
	queryValue := func(name string) string {
		if len(requests) == 0 || requests[0] == nil || requests[0].URL == nil {
			return ""
		}
		return requests[0].URL.Query().Get(name)
	}
	queryTarget := func(sessionValue string) (canvasManagementTarget, bool) {
		projectValue := queryValue("projectId")
		projectID, projectOK := "", true
		if projectValue != "" {
			projectID, projectOK = decode(projectValue)
		}
		sessionID, sessionOK := decode(sessionValue)
		if !projectOK || !sessionOK || (projectID != "" && validateIdentity(projectID, "Project id") != nil) || validatePiSessionID(sessionID) != nil {
			return canvasManagementTarget{}, false
		}
		return canvasManagementTarget{projectID: projectID, sessionID: sessionID}, true
	}
	if len(parts) == 1 && (parts[0] == "status" || parts[0] == "remove") {
		return queryTarget(queryValue("sessionId"))
	}
	if len(parts) < 2 {
		return canvasManagementTarget{}, false
	}
	var projectPart, sessionPart string
	switch parts[0] {
	case "status", "remove":
		if len(parts) == 2 {
			return queryTarget(parts[1])
		}
		if len(parts) != 3 {
			return canvasManagementTarget{}, false
		}
		projectPart, sessionPart = parts[1], parts[2]
	case "artifact", "artifacts":
		if len(parts) == 4 {
			return queryTarget(parts[1])
		}
		if len(parts) != 5 {
			return canvasManagementTarget{}, false
		}
		projectPart, sessionPart = parts[1], parts[2]
	default:
		// Target-first aliases: /api/canvas/{project}/{session}/status and
		// /api/canvas/{project}/{session}/artifact/{canvas}/{key}.png.
		if len(parts) == 2 && (parts[1] == "status" || parts[1] == "remove") {
			return queryTarget(parts[0])
		}
		if len(parts) == 4 && (parts[1] == "artifact" || parts[1] == "artifacts") {
			return queryTarget(parts[0])
		}
		if len(parts) < 3 || (parts[2] != "status" && parts[2] != "remove" && parts[2] != "artifact" && parts[2] != "artifacts") {
			return canvasManagementTarget{}, false
		}
		if (parts[2] == "status" || parts[2] == "remove") && len(parts) != 3 {
			return canvasManagementTarget{}, false
		}
		if (parts[2] == "artifact" || parts[2] == "artifacts") && len(parts) != 5 {
			return canvasManagementTarget{}, false
		}
		projectPart, sessionPart = parts[0], parts[1]
	}
	projectID, ok := decode(projectPart)
	if !ok {
		return canvasManagementTarget{}, false
	}
	sessionID, ok := decode(sessionPart)
	if !ok {
		return canvasManagementTarget{}, false
	}
	if validateIdentity(projectID, "Project id") != nil || validatePiSessionID(sessionID) != nil {
		return canvasManagementTarget{}, false
	}
	return canvasManagementTarget{projectID: projectID, sessionID: sessionID}, true
}

func (h *HTTPHandler) verifiedCanvasManagementScope(target canvasManagementTarget) (canvasManagementTarget, bool) {
	if h == nil || h.Projects == nil || h.SessionRecords == nil {
		return canvasManagementTarget{}, false
	}
	records, err := h.SessionRecords.List()
	if err != nil {
		return canvasManagementTarget{}, false
	}
	if target.projectID == "" {
		for _, record := range records {
			if record.SessionID != target.sessionID {
				continue
			}
			if target.projectID != "" && target.projectID != record.ProjectID {
				return canvasManagementTarget{}, false
			}
			target.projectID = record.ProjectID
		}
	}
	if target.projectID == "" {
		return canvasManagementTarget{}, false
	}
	if _, err := h.Projects.Get(target.projectID); err != nil {
		return canvasManagementTarget{}, false
	}
	for _, record := range records {
		if record.ProjectID == target.projectID && record.SessionID == target.sessionID {
			return target, true
		}
	}
	return canvasManagementTarget{}, false
}

// normalizeRoutePath collapses duplicate slashes and dot segments so reserved
// /api/* and /mcp/* namespaces cannot bypass the top-level router into the
// SPA fallback. A trailing slash is preserved so "/api" and "/api/" keep
// their distinct reserved-versus-publisher meaning.
func normalizeRoutePath(p string) string {
	if p == "" {
		return "/"
	}
	cleaned := path.Clean(p)
	if cleaned == "." {
		return "/"
	}
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	if strings.HasSuffix(p, "/") && cleaned != "/" && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	return cleaned
}

func reservedAPIMCPRoute(p string) bool {
	cleaned := normalizeRoutePath(p)
	return cleaned == "/api" || strings.HasPrefix(cleaned, "/api/") || cleaned == "/mcp" || strings.HasPrefix(cleaned, "/mcp/")
}

func mcpPublisherRoute(p string) bool {
	cleaned := normalizeRoutePath(p)
	return strings.HasPrefix(cleaned, "/api/") || strings.HasPrefix(cleaned, "/mcp/")
}

func (h *HTTPHandler) isAuthorizedMCPRequest(request *http.Request) bool {
	if origins := request.Header.Values("Origin"); len(origins) > 0 && !h.Auth.IsExpectedOrigin(request) {
		return false
	}
	if h.Auth.MCPToken == "" {
		site := request.Header.Get("Sec-Fetch-Site")
		return site == "" || site == "same-origin"
	}
	values := request.Header.Values("Authorization")
	return len(values) == 1 && strings.HasPrefix(values[0], "Bearer ") && constantTimeStringEqual(strings.TrimPrefix(values[0], "Bearer "), h.Auth.MCPToken)
}

// isAllowedSessionMCPRequest keeps the controller's browser-origin and
// same-origin protections on session-scoped modules while leaving bearer
// capability validation to the owning service. Session capabilities are not
// interchangeable with PIXIE_MCP_TOKEN and must not be accepted by the
// publisher-wide check above.
func (h *HTTPHandler) isAllowedSessionMCPRequest(request *http.Request) bool {
	if request == nil {
		return false
	}
	if origins := request.Header.Values("Origin"); len(origins) > 0 && !h.Auth.IsExpectedOrigin(request) {
		return false
	}
	if site := request.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	return true
}

func (h *HTTPHandler) serveAuth(response http.ResponseWriter, request *http.Request) {
	route := "/"
	if request.URL != nil {
		route = normalizeRoutePath(request.URL.Path)
	}
	if route == "/auth/status" {
		if request.Method != http.MethodGet {
			methodNotAllowed(response, http.MethodGet)
			return
		}
		authenticated := true
		if h.Auth.Enabled {
			_, authenticated = h.auth.SessionExpiresAt(ReadAuthCookie(request))
		}
		writeAuthJSON(response, http.StatusOK, map[string]bool{"authenticationEnabled": h.Auth.Enabled, "authenticated": authenticated})
		return
	}
	if route != "/auth/login" && route != "/auth/logout" || !h.Auth.Enabled {
		writeAuthJSON(response, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writeAuthJSON(response, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if request.Header.Get("Sec-Fetch-Site") != "same-origin" || !h.Auth.IsExpectedOrigin(request) {
		writeAuthJSON(response, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	body, status, err := readAuthBody(request)
	if err != nil {
		writeAuthJSON(response, status, map[string]string{"error": err.Error()})
		return
	}
	secure := h.Auth.SecureCookie(request)
	if route == "/auth/login" {
		if len(body) != 1 {
			writeAuthJSON(response, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		token, ok := body["token"].(string)
		if !ok {
			writeAuthJSON(response, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		session, ok := h.auth.Login(token)
		if !ok {
			writeAuthJSON(response, http.StatusUnauthorized, map[string]string{"error": "authentication failed"})
			return
		}
		response.Header().Set("Set-Cookie", SessionCookie(session, secure))
		writeAuthJSON(response, http.StatusOK, map[string]bool{"authenticated": true})
		return
	}
	if len(body) != 0 {
		writeAuthJSON(response, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	response.Header().Set("Set-Cookie", ExpiredSessionCookie(secure))
	writeAuthJSON(response, http.StatusOK, map[string]bool{"authenticated": false})
}

func readAuthBody(request *http.Request) (map[string]any, int, error) {
	if request.Header.Get("Content-Type") != "application/json" {
		return nil, http.StatusUnsupportedMediaType, plainError("unsupported media type")
	}
	if request.ContentLength > maxAuthBodyBytes {
		return nil, http.StatusRequestEntityTooLarge, plainError("request too large")
	}
	reader := io.LimitReader(request.Body, maxAuthBodyBytes+1)
	content, err := io.ReadAll(reader)
	if err != nil || len(content) > maxAuthBodyBytes {
		return nil, http.StatusRequestEntityTooLarge, plainError("request too large")
	}
	var value map[string]any
	if json.Unmarshal(content, &value) != nil || value == nil {
		return nil, http.StatusBadRequest, plainError("invalid request")
	}
	return value, 0, nil
}

type plainError string

func (e plainError) Error() string { return string(e) }

func writeAuthJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func isHealthRoute(request *http.Request) bool {
	if request == nil || request.URL == nil {
		return false
	}
	switch normalizeRoutePath(request.URL.Path) {
	case "/health", "/livez", "/readyz":
		return true
	default:
		return false
	}
}

func serveHealth(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(response, http.MethodGet)
		return
	}
	_, _ = io.WriteString(response, "ok")
}

func methodNotAllowed(response http.ResponseWriter, method string) {
	response.Header().Set("Allow", method)
	http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
}

func (h *HTTPHandler) serveProjectImage(response http.ResponseWriter, request *http.Request) {
	if !h.Auth.IsAuthorizedHTTPRequest(request, h.auth) {
		http.Error(response, "unauthorized", http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(response, http.MethodGet)
		return
	}
	if len(request.URL.EscapedPath()) > 4_096 {
		http.NotFound(response, request)
		return
	}
	parts := strings.Split(strings.TrimPrefix(request.URL.EscapedPath(), "/files/"), "/")
	if len(parts) < 2 {
		http.NotFound(response, request)
		return
	}
	projectID, firstErr := url.PathUnescape(parts[0])
	relative, secondErr := url.PathUnescape(strings.Join(parts[1:], "/"))
	if firstErr != nil || secondErr != nil {
		http.NotFound(response, request)
		return
	}
	root, err := h.Projects.Root(projectID)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	opened, info, file, err := h.Files.OpenRegularFileInRoot(root, relative, projectImageMaxBytes)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	defer opened.Close()
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(mime.TypeByExtension(filepath.Ext(file)), ";")[0]))
	if contentType != "image/gif" && contentType != "image/jpeg" && contentType != "image/png" && contentType != "image/webp" {
		http.NotFound(response, request)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", contentType)
	response.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	response.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(response, request, filepath.Base(file), info.ModTime(), io.NewSectionReader(opened, 0, info.Size()))
}

func (h *HTTPHandler) serveStatic(response http.ResponseWriter, request *http.Request) {
	h.setStaticSecurityHeaders(response, request, nil)
	response.Header().Set("Cache-Control", "no-store")
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(response, http.MethodGet)
		return
	}
	requested := strings.TrimPrefix(path.Clean("/"+request.URL.Path), "/")
	if requested == "" || requested == "." {
		requested = "index.html"
	}
	if strings.HasSuffix(requested, ".gz") {
		http.NotFound(response, request)
		return
	}
	// Request paths are cleaned above and served from a rooted filesystem,
	// so traversal cannot escape the asset root.
	file := requested
	if _, ok := h.static.stat(file); !ok {
		if path.Ext(requested) != "" {
			http.NotFound(response, request)
			return
		}
		file = "index.html"
	}
	info, ok := h.static.stat(file)
	if !ok {
		http.NotFound(response, request)
		return
	}
	if file == "index.html" {
		content, ok := h.static.read(file)
		if !ok {
			http.NotFound(response, request)
			return
		}
		h.setStaticSecurityHeaders(response, request, inlineScriptHashes(content))
		response.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(response, request, "index.html", info.ModTime(), bytes.NewReader(content))
		return
	}
	if immutableStaticAsset(requested) {
		response.Header().Set("Cache-Control", immutableCachePolicy)
	} else {
		response.Header().Set("Cache-Control", "no-cache")
	}
	h.serveStaticFile(response, request, file, info)
}

func (h *HTTPHandler) serveStaticFile(response http.ResponseWriter, request *http.Request, file string, info fs.FileInfo) {
	compressed, compressedOK := h.static.read(file + ".gz")
	if !compressedOK || !precompressibleStaticAsset(file) {
		h.serveStaticBytes(response, request, file, info)
		return
	}
	response.Header().Add("Vary", "Accept-Encoding")
	if !acceptsContentEncoding(request.Header.Get("Accept-Encoding"), "gzip") {
		h.serveStaticBytes(response, request, file, info)
		return
	}
	response.Header().Set("Content-Encoding", "gzip")
	if contentType := mime.TypeByExtension(path.Ext(file)); contentType != "" {
		response.Header().Set("Content-Type", contentType)
	}
	http.ServeContent(response, request, path.Base(file), info.ModTime(), bytes.NewReader(compressed))
}

func (h *HTTPHandler) serveStaticBytes(response http.ResponseWriter, request *http.Request, file string, info fs.FileInfo) {
	content, ok := h.static.read(file)
	if !ok {
		http.NotFound(response, request)
		return
	}
	http.ServeContent(response, request, path.Base(file), info.ModTime(), bytes.NewReader(content))
}

func precompressibleStaticAsset(file string) bool {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".css", ".js":
		return true
	default:
		return false
	}
}

func acceptsContentEncoding(header, wanted string) bool {
	wildcard := false
	for _, raw := range strings.Split(header, ",") {
		parts := strings.Split(raw, ";")
		encoding := strings.ToLower(strings.TrimSpace(parts[0]))
		quality := 1.0
		for _, parameter := range parts[1:] {
			name, value, found := strings.Cut(strings.TrimSpace(parameter), "=")
			if !found || !strings.EqualFold(name, "q") {
				continue
			}
			parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil {
				quality = 0
			} else {
				quality = parsed
			}
		}
		if encoding == wanted {
			return quality > 0
		}
		if encoding == "*" && quality > 0 {
			wildcard = true
		}
	}
	return wildcard
}

func (h *HTTPHandler) setStaticSecurityHeaders(response http.ResponseWriter, request *http.Request, scriptHashes []string) {
	frameSources := "'self'"
	frameOrigin := h.Auth.BrowserPublicOrigin
	if frameOrigin == "" {
		frameOrigin = h.Auth.BrowserURL
	}
	if normalized, err := normalizeOrigin(frameOrigin); err == nil {
		frameSources += " " + normalized
	}
	connectSources := "'self'"
	if origin, err := h.Auth.ExpectedOrigin(request); err == nil {
		if parsed, parseErr := url.Parse(origin); parseErr == nil {
			if parsed.Scheme == "https" {
				parsed.Scheme = "wss"
			} else {
				parsed.Scheme = "ws"
			}
			connectSources += " " + parsed.String()
		}
	}
	scriptSources := "'self'"
	if len(scriptHashes) > 0 {
		scriptSources += " " + strings.Join(scriptHashes, " ")
	}
	response.Header().Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'none'",
		"base-uri 'none'",
		"connect-src " + connectSources,
		"font-src 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
		"frame-src " + frameSources,
		"img-src 'self' data:",
		"object-src 'none'",
		"script-src " + scriptSources,
		"style-src 'self' 'unsafe-inline'",
	}, "; "))
	response.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	response.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("X-Frame-Options", "DENY")
}

func inlineScriptHashes(document []byte) []string {
	var result []string
	for _, match := range inlineScriptPattern.FindAllSubmatch(document, -1) {
		if scriptSourcePattern.Match(match[1]) {
			continue
		}
		digest := sha256.Sum256(match[2])
		result = append(result, "'sha256-"+base64.StdEncoding.EncodeToString(digest[:])+"'")
	}
	return result
}

// MODULE-01 module/scope wiring: trusted contribution boundary for module
// resource requests. Scope tokens must be bound to the exact
// module/resource/session/generation tuple; forged, mismatched, expired, or
// revoked tokens fail closed here without delegating to the module service.
// Sidebar-only versus viewer-only declarations are enforced via the explicit
// surface parameter before any delegation.
func (h *HTTPHandler) serveModuleResource(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(response, http.MethodGet)
		return
	}
	route := "/"
	if request.URL != nil {
		route = normalizeRoutePath(request.URL.Path)
	}
	moduleID, ok := mcpserver.ModuleIDFromRoute(route)
	if !ok {
		writeAuthJSON(response, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	descriptors := h.ModuleDescriptors
	if descriptors == nil {
		descriptors = mcpserver.DefaultModuleDescriptors()
	}
	scoped, _, status, err := mcpserver.AuthorizeModuleHTTPRequest(h.ModuleScopes, descriptors, moduleID, request)
	if err != nil {
		writeAuthJSON(response, status, map[string]string{"error": err.Error()})
		return
	}
	if h.ModuleService != nil {
		h.ModuleService.ServeHTTP(response, request)
		return
	}
	writeAuthJSON(response, http.StatusOK, map[string]string{"moduleId": scoped.ModuleID, "resource": scoped.ResourceID, "surface": scoped.Surface})
}

func immutableStaticAsset(requested string) bool {
	extension := path.Ext(requested)
	stem := strings.TrimSuffix(path.Base(requested), extension)
	separator := strings.LastIndexByte(stem, '-')
	if extension == "" || separator < 0 || len(stem)-separator-1 < 8 {
		return false
	}
	for _, character := range stem[separator+1:] {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'z' {
				return false
			}
		}
	}
	return true
}
