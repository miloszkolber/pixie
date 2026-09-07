package controller

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

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
	Ready         http.HandlerFunc
	browserClient *http.Client
}

func NewHTTPHandler(webSocket *WebSocketServer, objective ObjectiveHandler, projects *workspace.Projects, files *workspace.Files, authConfig AuthConfig, staticDir string, ready http.HandlerFunc) (*HTTPHandler, error) {
	if authConfig.BrowserURL == "" {
		authConfig.BrowserURL = "http://127.0.0.1:8787"
	}
	result := &HTTPHandler{WebSocket: webSocket, Objective: objective, Projects: projects, Files: files, Auth: authConfig, StaticDir: staticDir, Ready: ready, browserClient: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
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
	switch {
	case request.URL.Path == "/mcp/objective":
		h.Objective.ServeHTTP(response, request)
	case strings.HasPrefix(request.URL.Path, "/auth/"):
		h.serveAuth(response, request)
	case request.URL.Path == "/ws":
		h.WebSocket.ServeHTTP(response, request)
	case request.URL.Path == "/health" || request.URL.Path == "/livez":
		serveHealth(response, request)
	case request.URL.Path == "/readyz":
		if request.Method != http.MethodGet {
			methodNotAllowed(response, http.MethodGet)
		} else if h.Ready == nil {
			http.Error(response, "not ready", http.StatusServiceUnavailable)
		} else {
			h.Ready(response, request)
		}
	case strings.HasPrefix(request.URL.Path, "/files/"):
		h.serveProjectImage(response, request)
	case strings.HasPrefix(request.URL.Path, "/v1/artifacts/"):
		h.serveBrowserArtifact(response, request)
	default:
		h.serveStatic(response, request)
	}
}

func (h *HTTPHandler) serveAuth(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/auth/status" {
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
	if request.URL.Path != "/auth/login" && request.URL.Path != "/auth/logout" || !h.Auth.Enabled {
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
	secure := request.TLS != nil || strings.HasPrefix(h.Auth.PublicOrigin, "https://")
	if request.URL.Path == "/auth/login" {
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
	file := filepath.Join(h.StaticDir, filepath.FromSlash(requested))
	if !workspace.Within(h.StaticDir, file) || !regularFile(file) {
		if path.Ext(requested) != "" {
			http.NotFound(response, request)
			return
		}
		file = filepath.Join(h.StaticDir, "index.html")
	}
	if !regularFile(file) {
		http.NotFound(response, request)
		return
	}
	if filepath.Base(file) == "index.html" {
		content, err := os.ReadFile(file)
		if err != nil {
			http.NotFound(response, request)
			return
		}
		info, err := os.Stat(file)
		if err != nil || !info.Mode().IsRegular() {
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
	h.serveStaticFile(response, request, file)
}

func (h *HTTPHandler) serveStaticFile(response http.ResponseWriter, request *http.Request, file string) {
	compressedFile := file + ".gz"
	if !regularFile(compressedFile) || !precompressibleStaticAsset(file) {
		http.ServeFile(response, request, file)
		return
	}
	response.Header().Add("Vary", "Accept-Encoding")
	if !acceptsContentEncoding(request.Header.Get("Accept-Encoding"), "gzip") {
		http.ServeFile(response, request, file)
		return
	}
	compressed, err := os.Open(compressedFile)
	if err != nil {
		http.ServeFile(response, request, file)
		return
	}
	defer compressed.Close()
	compressedInfo, err := compressed.Stat()
	if err != nil || !compressedInfo.Mode().IsRegular() {
		http.ServeFile(response, request, file)
		return
	}
	originalInfo, err := os.Stat(file)
	if err != nil || !originalInfo.Mode().IsRegular() {
		http.NotFound(response, request)
		return
	}
	response.Header().Set("Content-Encoding", "gzip")
	if contentType := mime.TypeByExtension(filepath.Ext(file)); contentType != "" {
		response.Header().Set("Content-Type", contentType)
	}
	http.ServeContent(
		response,
		request,
		filepath.Base(file),
		originalInfo.ModTime(),
		io.NewSectionReader(compressed, 0, compressedInfo.Size()),
	)
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

func regularFile(file string) bool {
	info, err := os.Stat(file)
	return err == nil && info.Mode().IsRegular()
}
