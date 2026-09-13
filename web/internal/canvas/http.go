package canvas

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
)

var artifactKeyPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

// ServeHTTP owns only Canvas routes. The top-level controller remains
// responsible for listener/Host/Origin policy; this handler never treats a
// URL ID as authority.
func (s *Service) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if response == nil || request == nil {
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	if canvasCredentialQuery(request) {
		writeCanvasJSON(response, http.StatusBadRequest, map[string]any{"outcome": "rejected", "code": "credentials_in_url"})
		return
	}
	switch {
	case request.URL.Path == "/health" || request.URL.Path == "/readyz":
		s.serveHealth(response)
	case request.URL.Path == "/api/canvas/status" || strings.HasPrefix(request.URL.Path, "/api/canvas/status/"):
		s.serveManagementStatus(response, request)
	case request.URL.Path == "/api/canvas/remove" || strings.HasPrefix(request.URL.Path, "/api/canvas/remove/") || request.URL.Path == "/api/canvas/document":
		s.serveManagementRemove(response, request)
	case strings.HasPrefix(request.URL.Path, "/api/canvas/artifact/") || strings.HasPrefix(request.URL.Path, "/api/canvas/artifacts/"):
		s.serveManagementArtifact(response, request)
	case request.URL.Path == "/mcp/canvas" || request.URL.Path == "/mcp/canvas/":
		s.serveMCP(response, request)
	case request.URL.Path == "/mcp/canvas/guide":
		if request.Method != http.MethodGet {
			writeCanvasJSON(response, http.StatusMethodNotAllowed, map[string]any{"outcome": "rejected", "code": "method_not_allowed"})
			return
		}
		response.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = response.Write([]byte(canvasGuide))
	case strings.HasPrefix(request.URL.Path, "/mcp/canvas/artifact/"):
		s.serveArtifact(response, request)
	default:
		writeCanvasJSON(response, http.StatusNotFound, map[string]any{"outcome": "rejected", "code": "not_found"})
	}
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

func (s *Service) serveHealth(response http.ResponseWriter) {
	s.mu.RLock()
	closed := s.closed
	s.mu.RUnlock()
	enabled := s.isEnabled()
	ready := s.Ready()
	status := http.StatusOK
	if closed || !ready {
		status = http.StatusServiceUnavailable
	}
	availability := "ready"
	if !enabled {
		availability = "disabled"
	} else if !ready {
		availability = "corrupt"
	}
	writeCanvasJSON(response, status, map[string]any{"outcome": "ready", "ready": ready, "enabled": enabled, "availability": availability, "renderer": s.config.WorkerLauncher != nil && !isNilLauncher(s.config.WorkerLauncher)})
}

func (s *Service) serveMCP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost && request.Method != http.MethodGet && request.Method != http.MethodDelete {
		writeCanvasJSON(response, http.StatusMethodNotAllowed, map[string]any{"outcome": "rejected", "code": "method_not_allowed"})
		return
	}
	authority, err := s.authorityFromBearer(request)
	if err != nil {
		writeCanvasJSON(response, http.StatusUnauthorized, map[string]any{"outcome": "rejected", "code": Code(err)})
		return
	}
	handler := s.MCPHandler()
	handler.ServeHTTP(response, withAuthority(request, authority))
}

func (s *Service) authorityFromBearer(request *http.Request) (Authority, error) {
	header := strings.TrimSpace(request.Header.Get("Authorization"))
	if len(header) < len("Bearer ") || !strings.EqualFold(header[:len("Bearer ")], "Bearer ") {
		return Authority{}, category("unauthorized", ErrUnauthorized)
	}
	token := strings.TrimSpace(header[len("Bearer "):])
	if token == "" {
		return Authority{}, category("unauthorized", ErrUnauthorized)
	}
	s.mu.RLock()
	entry, found := s.tokens[tokenDigest(token)]
	s.mu.RUnlock()
	if !found {
		return Authority{}, category("authority_revoked", ErrRevoked)
	}
	authority := Authority{Token: token, SessionKey: entry.sessionKey, Generation: entry.generation, ExpiresAt: entry.expiresAt}
	if err := s.authorizeAuthority(authority); err != nil {
		return Authority{}, err
	}
	return authority, nil
}

func (s *Service) serveArtifact(response http.ResponseWriter, request *http.Request) {
	s.serveArtifactWithMode(response, request, false)
}

// serveManagementArtifact is reached only through the controller's
// cookie-authenticated delegation. It permits retained artifact reads while
// Canvas is disabled or its renderer is unavailable, but keeps all storage,
// generation and revocation checks in the Canvas service.
func (s *Service) serveManagementArtifact(response http.ResponseWriter, request *http.Request) {
	s.serveArtifactWithMode(response, request, true)
}

func (s *Service) serveArtifactWithMode(response http.ResponseWriter, request *http.Request, management bool) {
	if request.Method != http.MethodGet {
		writeCanvasJSON(response, http.StatusMethodNotAllowed, map[string]any{"outcome": "rejected", "code": "method_not_allowed"})
		return
	}
	var authority Authority
	var err error
	if management {
		authority, err = s.managementAuthorityFromContext(request.Context())
	} else {
		authority, err = s.authorityFromBearer(request)
	}
	if err != nil {
		writeCanvasError(response, err)
		return
	}
	if !management && !s.isEnabled() {
		writeCanvasJSON(response, http.StatusServiceUnavailable, map[string]any{"outcome": "rejected", "code": "disabled"})
		return
	}
	if !management && !s.Ready() {
		writeCanvasJSON(response, http.StatusServiceUnavailable, map[string]any{"outcome": "rejected", "code": "corrupt"})
		return
	}
	prefix := "/mcp/canvas/artifact/"
	if management {
		prefix = "/api/canvas/artifact/"
		if strings.HasPrefix(request.URL.Path, "/api/canvas/artifacts/") {
			prefix = "/api/canvas/artifacts/"
		}
	}
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, prefix), "/")
	if management && len(parts) > 2 {
		// The controller/registry strips the verified project/session path
		// before delegation. Accepting the full scoped form here keeps the
		// service boundary useful for direct in-process callers; authority still
		// binds the final Canvas ID to one session.
		parts = parts[len(parts)-2:]
	}
	if len(parts) != 2 || !validIdentity(parts[0]) || !strings.HasSuffix(parts[1], ".png") {
		writeCanvasJSON(response, http.StatusNotFound, map[string]any{"outcome": "rejected", "code": "not_found"})
		return
	}
	canvasID, key := parts[0], strings.TrimSuffix(parts[1], ".png")
	if !artifactKeyPattern.MatchString(key) {
		writeCanvasJSON(response, http.StatusNotFound, map[string]any{"outcome": "rejected", "code": "not_found"})
		return
	}
	var session *sessionState
	if management {
		session, err = s.managementSessionForAuthority(authority)
	} else {
		session, err = s.sessionForAuthority(authority)
	}
	if err != nil {
		writeCanvasJSON(response, http.StatusUnauthorized, map[string]any{"outcome": "rejected", "code": Code(err)})
		return
	}
	documentGeneration := uint64(0)
	if management {
		documentGeneration, err = s.managementDocumentGeneration(authority)
	} else {
		documentGeneration, err = s.authorityDocumentGeneration(authority)
	}
	if err != nil {
		writeCanvasError(response, err)
		return
	}
	canvas, err := s.canvasFor(session, canvasID, documentGeneration)
	if err != nil {
		status := http.StatusNotFound
		if errors.Is(err, ErrUnauthorized) {
			status = http.StatusUnauthorized
		}
		writeCanvasJSON(response, status, map[string]any{"outcome": "rejected", "code": Code(err)})
		return
	}
	canvas.mu.RLock()
	pathName := path.Join(canvas.dir, shotsDirName, key+".png")
	canvas.mu.RUnlock()
	content, err := readBoundedFile(pathName, s.config.MaxImageBytes)
	if err != nil {
		writeCanvasJSON(response, http.StatusNotFound, map[string]any{"outcome": "rejected", "code": Code(err)})
		return
	}
	// Artifact reads can race revocation, disablement or logical removal. The
	// file descriptor is bounded and no-follow, but the response must still
	// revalidate the capability and document identity before returning bytes.
	if management {
		err = s.authorizeManagementAuthority(authority)
	} else {
		err = s.authorizeAuthority(authority)
	}
	if err != nil {
		writeCanvasError(response, err)
		return
	}
	if !management && !s.isEnabled() {
		writeCanvasJSON(response, http.StatusNotFound, map[string]any{"outcome": "rejected", "code": "not_found"})
		return
	}
	canvas.mu.RLock()
	stillCurrent := canvas.meta.Generation == documentGeneration && canvas.meta.RemovedAt == nil && !canvas.meta.Corrupt && !canvas.uncertain
	canvas.mu.RUnlock()
	if !stillCurrent {
		writeCanvasJSON(response, http.StatusNotFound, map[string]any{"outcome": "rejected", "code": "not_found"})
		return
	}
	response.Header().Set("Content-Type", "image/png")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	response.Header().Set("Content-Length", strconv.Itoa(len(content)))
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(content)
}

func (s *Service) serveManagementStatus(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeCanvasJSON(response, http.StatusMethodNotAllowed, map[string]any{"outcome": "rejected", "code": "method_not_allowed"})
		return
	}
	authority, err := s.managementAuthorityFromContext(request.Context())
	if err != nil {
		writeCanvasError(response, err)
		return
	}
	result, err := s.ManagementStatus(request.Context(), authority)
	if err != nil {
		writeCanvasError(response, err)
		return
	}
	available, availability := s.managementAvailability()
	body := map[string]any{
		"outcome":      result.Outcome,
		"items":        result.Items,
		"available":    available,
		"availability": availability,
		"ready":        s.Ready(),
		"enabled":      s.isEnabled(),
	}
	if len(result.Items) > 0 {
		body["canvas"] = result.Items[0]
	} else {
		body["canvas"] = nil
	}
	writeCanvasJSON(response, http.StatusOK, body)
}

func (s *Service) serveManagementRemove(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost && request.Method != http.MethodDelete {
		writeCanvasJSON(response, http.StatusMethodNotAllowed, map[string]any{"outcome": "rejected", "code": "method_not_allowed"})
		return
	}
	authority, err := s.managementAuthorityFromContext(request.Context())
	if err != nil {
		writeCanvasError(response, err)
		return
	}
	var remove RemoveRequest
	if request.Body != nil && request.ContentLength != 0 {
		decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 64*1024))
		if decodeErr := decoder.Decode(&remove); decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
			writeCanvasJSON(response, http.StatusBadRequest, map[string]any{"outcome": "rejected", "code": "invalid_request"})
			return
		}
		if decodeErr := decoder.Decode(&struct{}{}); decodeErr != io.EOF {
			writeCanvasJSON(response, http.StatusBadRequest, map[string]any{"outcome": "rejected", "code": "invalid_request"})
			return
		}
	}
	query := request.URL.Query()
	if value := query.Get("canvasId"); value != "" {
		remove.CanvasID = value
	}
	if value := query.Get("expectedGeneration"); value != "" {
		parsed, parseErr := strconv.ParseUint(value, 10, 64)
		if parseErr != nil {
			writeCanvasJSON(response, http.StatusBadRequest, map[string]any{"outcome": "rejected", "code": "invalid_request"})
			return
		}
		remove.ExpectedGeneration = parsed
	}
	if value := query.Get("expectedVersion"); value != "" {
		parsed, parseErr := strconv.ParseUint(value, 10, 64)
		if parseErr != nil {
			writeCanvasJSON(response, http.StatusBadRequest, map[string]any{"outcome": "rejected", "code": "invalid_request"})
			return
		}
		remove.ExpectedVersion = parsed
	}
	if value := query.Get("mutationId"); value != "" {
		remove.MutationID = value
	}
	result, err := s.Remove(request.Context(), authority, remove)
	if err != nil {
		writeCanvasError(response, err)
		return
	}
	writeCanvasJSON(response, http.StatusOK, result)
}

func (s *Service) managementAvailability() (bool, string) {
	if !s.isEnabled() {
		return false, "disabled"
	}
	if !s.Ready() {
		return false, "corrupt"
	}
	if s.config.WorkerLauncher == nil || isNilLauncher(s.config.WorkerLauncher) {
		return false, "unavailable"
	}
	return true, "ready"
}

func writeCanvasError(response http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var typed *ServiceError
	if errors.As(err, &typed) && typed.Status > 0 {
		status = typed.Status
	} else {
		switch Code(err) {
		case "unauthorized", "authority_expired", "authority_revoked":
			status = http.StatusUnauthorized
		case "not_found", "removed":
			status = http.StatusNotFound
		case "conflict", "mutation_conflict", "generation_revoked":
			status = http.StatusConflict
		case "disabled", "unavailable":
			status = http.StatusServiceUnavailable
		case "corrupt", "persistence_uncertain":
			status = http.StatusInternalServerError
		}
	}
	writeCanvasJSON(response, status, map[string]any{"outcome": "rejected", "code": Code(err)})
}

func writeCanvasJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	encoded, err := json.Marshal(value)
	if err != nil {
		status = http.StatusInternalServerError
		encoded = []byte(`{"outcome":"failed","code":"internal"}`)
	}
	response.WriteHeader(status)
	_, _ = response.Write(encoded)
}
