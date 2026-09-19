package design

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ServeHTTP owns the self-contained Design management/query/artifact/MCP
// routes. Listener Host/Origin policy remains the top-level controller's
// responsibility; optional Config authorizers provide route-role checks.
func (s *Service) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if response == nil || request == nil {
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	switch {
	case request.URL.Path == "/health" || request.URL.Path == "/readyz":
		s.serveHealth(response)
	case request.URL.Path == "/mcp/design" || request.URL.Path == "/mcp/design/":
		s.serveMCP(response, request)
	case request.URL.Path == "/mcp/design/guide":
		s.serveGuide(response, request)
	case strings.HasPrefix(request.URL.Path, "/api/design/artifacts/") || strings.HasPrefix(request.URL.Path, "/api/design/artifact/"):
		s.serveArtifact(response, request)
	case request.URL.Path == "/api/design/document":
		s.serveDocument(response, request)
	case request.URL.Path == "/api/design/status":
		s.serveStatus(response, request)
	case request.URL.Path == "/api/design/structure":
		s.serveStructure(response, request)
	case request.URL.Path == "/api/design/node":
		s.serveNode(response, request)
	case request.URL.Path == "/api/design/text":
		s.serveText(response, request)
	case request.URL.Path == "/api/design/preview":
		s.servePreview(response, request)
	case request.URL.Path == "/api/design/selection":
		s.serveSelection(response, request)
	default:
		writeJSON(response, http.StatusNotFound, map[string]any{"outcome": "rejected", "code": "not_found"})
	}
}

func (s *Service) serveHealth(response http.ResponseWriter) {
	s.mu.RLock()
	closed, enabled, loadErr := s.closed, s.config.Enabled, s.loadErr
	s.mu.RUnlock()
	status := http.StatusOK
	if closed || loadErr != nil {
		status = http.StatusServiceUnavailable
	}
	availability := "ready"
	if !enabled {
		availability = "disabled"
	} else if loadErr != nil {
		availability = "corrupt"
	}
	writeJSON(response, status, map[string]any{"outcome": "health", "ready": !closed && loadErr == nil, "enabled": enabled, "availability": availability})
}

func (s *Service) authorize(request *http.Request, management bool) error {
	s.mu.RLock()
	hook := s.config.ReaderAuthorizer
	if management {
		hook = s.config.ManagementAuthorizer
	}
	s.mu.RUnlock()
	if hook == nil {
		return nil
	}
	if err := hook(request); err != nil {
		return err
	}
	return nil
}

func (s *Service) serveGuide(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"outcome": "rejected", "code": "method_not_allowed"})
		return
	}
	if err := s.authorize(request, false); err != nil {
		writeError(response, err)
		return
	}
	response.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = response.Write([]byte(designGuide))
}

func (s *Service) serveDocument(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		if err := s.authorize(request, false); err != nil {
			writeError(response, err)
			return
		}
		result, err := s.Status(request.Context())
		writeResult(response, result, err)
	case http.MethodPost:
		if err := s.authorize(request, true); err != nil {
			writeError(response, err)
			return
		}
		result, err := s.uploadHTTP(request, response)
		writeResult(response, result, err)
	case http.MethodDelete:
		if err := s.authorize(request, true); err != nil {
			writeError(response, err)
			return
		}
		var remove RemoveRequest
		if request.Body != nil && request.ContentLength != 0 {
			if decodeErr := json.NewDecoder(io.LimitReader(request.Body, 64*1024)).Decode(&remove); decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
				writeJSON(response, http.StatusBadRequest, map[string]any{"outcome": "rejected", "code": "invalid_request"})
				return
			}
		}
		if remove.DocumentID == "" {
			remove.DocumentID = request.URL.Query().Get("documentId")
		}
		remove.ExpectedGeneration = queryUint64(request, "expectedGeneration", remove.ExpectedGeneration)
		remove.SelectionRevision = queryUint64(request, "selectionRevision", remove.SelectionRevision)
		remove.OperationID = queryString(request, "operationId", remove.OperationID)
		result, err := s.Remove(request.Context(), remove)
		writeResult(response, result, err)
	default:
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"outcome": "rejected", "code": "method_not_allowed"})
	}
}

func (s *Service) uploadHTTP(request *http.Request, response http.ResponseWriter) (UploadResult, error) {
	name := request.Header.Get("X-Filename")
	if name == "" {
		name = request.URL.Query().Get("name")
	}
	operationID := request.Header.Get("X-Design-Operation")
	if operationID == "" {
		operationID = request.URL.Query().Get("operationId")
	}
	contentType := strings.ToLower(request.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		request.Body = http.MaxBytesReader(response, request.Body, s.config.MaxUploadBytes+1*1024*1024)
		multipartReader, err := request.MultipartReader()
		if err != nil {
			return UploadResult{}, designError("invalid_request", ErrInvalidRequest)
		}
		for {
			part, nextErr := multipartReader.NextPart()
			if errors.Is(nextErr, io.EOF) {
				break
			}
			if nextErr != nil {
				return UploadResult{}, designError("invalid_request", ErrInvalidRequest)
			}
			if part.FormName() != "file" && part.FormName() != "source" {
				_, _ = io.Copy(io.Discard, io.LimitReader(part, 64*1024))
				_ = part.Close()
				continue
			}
			if name == "" {
				name = part.FileName()
			}
			result, err := s.Upload(request.Context(), UploadRequest{Name: name, Reader: part, OperationID: operationID})
			_ = part.Close()
			return result, err
		}
		return UploadResult{}, designError("invalid_request", ErrInvalidRequest)
	}
	if request.Body == nil {
		return UploadResult{}, designError("invalid_request", ErrInvalidRequest)
	}
	limited := http.MaxBytesReader(response, request.Body, s.config.MaxUploadBytes+1)
	return s.Upload(request.Context(), UploadRequest{Name: name, Reader: limited, OperationID: operationID})
}

func (s *Service) serveStatus(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"outcome": "rejected", "code": "method_not_allowed"})
		return
	}
	if err := s.authorize(request, false); err != nil {
		writeError(response, err)
		return
	}
	result, err := s.Status(request.Context(), StatusRequest{DocumentID: request.URL.Query().Get("documentId"), ExpectedGeneration: queryUint64(request, "expectedGeneration", 0), SelectionRevision: queryUint64(request, "selectionRevision", 0)})
	writeResult(response, result, err)
}

func (s *Service) serveStructure(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"outcome": "rejected", "code": "method_not_allowed"})
		return
	}
	if err := s.authorize(request, false); err != nil {
		writeError(response, err)
		return
	}
	query := request.URL.Query()
	result, err := s.Structure(request.Context(), StructureRequest{DocumentID: query.Get("documentId"), ExpectedGeneration: queryUint64(request, "expectedGeneration", 0), ExpectedRevision: queryUint64(request, "selectionRevision", 0), PageID: query.Get("pageId"), ParentID: query.Get("parentId"), Depth: queryInt(request, "depth", 0), Cursor: query.Get("cursor"), Limit: queryInt(request, "limit", 0)})
	writeResult(response, result, err)
}

func (s *Service) serveNode(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"outcome": "rejected", "code": "method_not_allowed"})
		return
	}
	if err := s.authorize(request, false); err != nil {
		writeError(response, err)
		return
	}
	query := request.URL.Query()
	result, err := s.Node(request.Context(), NodeRequest{DocumentID: query.Get("documentId"), NodeID: query.Get("nodeId"), ExpectedGeneration: queryUint64(request, "expectedGeneration", 0), ExpectedRevision: queryUint64(request, "selectionRevision", 0)})
	writeResult(response, result, err)
}

func (s *Service) serveText(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"outcome": "rejected", "code": "method_not_allowed"})
		return
	}
	if err := s.authorize(request, false); err != nil {
		writeError(response, err)
		return
	}
	query := request.URL.Query()
	result, err := s.Text(request.Context(), TextRequest{DocumentID: query.Get("documentId"), ExpectedGeneration: queryUint64(request, "expectedGeneration", 0), ExpectedRevision: queryUint64(request, "selectionRevision", 0), PageID: query.Get("pageId"), RootNodeID: query.Get("rootNodeId"), Cursor: query.Get("cursor"), Limit: queryInt(request, "limit", 0)})
	writeResult(response, result, err)
}

func (s *Service) servePreview(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"outcome": "rejected", "code": "method_not_allowed"})
		return
	}
	if err := s.authorize(request, false); err != nil {
		writeError(response, err)
		return
	}
	query := request.URL.Query()
	result, err := s.Preview(request.Context(), PreviewRequest{DocumentID: query.Get("documentId"), ExpectedGeneration: queryUint64(request, "expectedGeneration", 0), ExpectedRevision: queryUint64(request, "selectionRevision", 0), Kind: query.Get("kind"), NodeID: query.Get("nodeId")})
	writeResult(response, result, err)
}

func (s *Service) serveSelection(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"outcome": "rejected", "code": "method_not_allowed"})
		return
	}
	if err := s.authorize(request, true); err != nil {
		writeError(response, err)
		return
	}
	var selection SetSelectionRequest
	if request.Body == nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"outcome": "rejected", "code": "invalid_request"})
		return
	}
	if err := json.NewDecoder(io.LimitReader(request.Body, 64*1024)).Decode(&selection); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"outcome": "rejected", "code": "invalid_request"})
		return
	}
	result, err := s.SetSelection(request.Context(), selection)
	writeResult(response, result, err)
}

func (s *Service) serveArtifact(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"outcome": "rejected", "code": "method_not_allowed"})
		return
	}
	if err := s.authorize(request, false); err != nil {
		writeError(response, err)
		return
	}
	prefix := "/api/design/artifacts/"
	if strings.HasPrefix(request.URL.Path, "/api/design/artifact/") {
		prefix = "/api/design/artifact/"
	}
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, prefix), "/")
	if len(parts) != 2 || !validOpaqueID(parts[0]) || (parts[1] != coverFileName && parts[1] != "cover.png") {
		writeJSON(response, http.StatusNotFound, map[string]any{"outcome": "rejected", "code": "not_found"})
		return
	}
	s.mu.RLock()
	slot := cloneSlot(s.slot)
	enabled := s.config.Enabled
	maxBytes := s.config.MaxPreviewBytes
	s.mu.RUnlock()
	if !enabled || slot.State != "active" || slot.DocumentID != parts[0] || !slot.CoverAvailable {
		writeJSON(response, http.StatusNotFound, map[string]any{"outcome": "rejected", "code": "not_found"})
		return
	}
	documentDir, dirErr := s.checkedDocumentDir(parts[0])
	if dirErr != nil {
		if errors.Is(dirErr, os.ErrNotExist) {
			writeJSON(response, http.StatusNotFound, map[string]any{"outcome": "not_found", "code": "not_found"})
			return
		}
		writeError(response, dirErr)
		return
	}
	pathName := filepath.Join(documentDir, coverFileName)
	info, err := os.Lstat(pathName)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxBytes {
		writeJSON(response, http.StatusNotFound, map[string]any{"outcome": "rejected", "code": "not_found"})
		return
	}
	content, err := readBounded(pathName, maxBytes)
	if err != nil || validatePNGBytes(content, s.config) != nil {
		writeJSON(response, http.StatusNotFound, map[string]any{"outcome": "rejected", "code": "not_found"})
		return
	}
	// The bounded no-follow read may overlap a removal or replacement. Recheck
	// the committed slot before returning bytes so a deleted document's cover
	// cannot leak through an already-admitted HTTP request.
	s.mu.RLock()
	current := cloneSlot(s.slot)
	s.mu.RUnlock()
	if !s.isActiveDocument(current, parts[0]) {
		writeJSON(response, http.StatusNotFound, map[string]any{"outcome": "rejected", "code": "not_found"})
		return
	}
	response.Header().Set("Content-Type", "image/png")
	response.Header().Set("Content-Length", strconv.Itoa(len(content)))
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(content)
}

func (s *Service) isActiveDocument(slot slotState, documentID string) bool {
	s.mu.RLock()
	enabled := s.config.Enabled
	s.mu.RUnlock()
	return enabled && slot.State == "active" && slot.DocumentID == documentID && slot.CoverAvailable
}

func (s *Service) serveMCP(response http.ResponseWriter, request *http.Request) {
	if err := s.authorize(request, false); err != nil {
		writeError(response, err)
		return
	}
	s.MCPHandler().ServeHTTP(response, request)
}

func queryString(request *http.Request, key, fallback string) string {
	if value := request.URL.Query().Get(key); value != "" {
		return value
	}
	return fallback
}

func queryUint64(request *http.Request, key string, fallback uint64) uint64 {
	value := request.URL.Query().Get(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return ^uint64(0)
	}
	return parsed
}

func queryInt(request *http.Request, key string, fallback int) int {
	value := request.URL.Query().Get(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return -1
	}
	return parsed
}

func writeResult(response http.ResponseWriter, value any, err error) {
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, value)
}

func writeError(response http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var typed *ServiceError
	if errors.As(err, &typed) && typed.Status != 0 {
		status = typed.Status
	}
	writeJSON(response, status, map[string]any{"outcome": "rejected", "code": Code(err), "message": err.Error()})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	encoded, err := json.Marshal(value)
	if err != nil {
		status = http.StatusInternalServerError
		encoded = []byte(`{"outcome":"rejected","code":"internal"}`)
	}
	response.WriteHeader(status)
	_, _ = response.Write(encoded)
}
