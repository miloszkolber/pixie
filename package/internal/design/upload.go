package design

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

func (s *Service) readUpload(ctx context.Context, request UploadRequest) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request.Reader == nil && request.Bytes == nil && request.Source == nil {
		return nil, designError("invalid_request", ErrInvalidRequest)
	}
	if request.Reader != nil {
		data, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: request.Reader}, s.config.MaxUploadBytes+1))
		if err != nil {
			if int64(len(data)) > s.config.MaxUploadBytes {
				return nil, designError("limit_exceeded", ErrLimit)
			}
			return nil, fmt.Errorf("read design upload: %w", err)
		}
		if int64(len(data)) > s.config.MaxUploadBytes {
			return nil, designError("limit_exceeded", ErrLimit)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}
	data := request.Bytes
	if data == nil {
		data = request.Source
	}
	if int64(len(data)) > s.config.MaxUploadBytes {
		return nil, designError("limit_exceeded", ErrLimit)
	}
	copy := append([]byte(nil), data...)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return copy, nil
}

func validateOperationID(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 256 || strings.TrimSpace(value) != value {
		return designError("invalid_request", ErrInvalidRequest)
	}
	for _, runeValue := range value {
		if runeValue < 0x20 || runeValue == 0x7f {
			return designError("invalid_request", ErrInvalidRequest)
		}
	}
	return nil
}

func operationFingerprint(name, sourceHash string) string {
	digest := sha256.Sum256([]byte(name + "\x00" + sourceHash))
	return hex.EncodeToString(digest[:])
}

func (s *Service) normalizeDocument(document NormalizedDocument) (NormalizedDocument, error) {
	return s.normalizeDocumentContext(context.Background(), document)
}

func (s *Service) normalizeDocumentContext(ctx context.Context, document NormalizedDocument) (NormalizedDocument, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return NormalizedDocument{}, err
	}
	if len(document.Pages) == 0 {
		return NormalizedDocument{}, designError("invalid_worker_response", fmt.Errorf("parser returned no pages: %w", ErrInvalidWorkerResponse))
	}
	if len(document.Pages) > s.config.MaxNodes {
		return NormalizedDocument{}, designError("limit_exceeded", ErrLimit)
	}
	if len(document.Nodes) > s.config.MaxNodes {
		return NormalizedDocument{}, designError("limit_exceeded", ErrLimit)
	}
	result := NormalizedDocument{Name: strings.TrimSpace(document.Name), ParserVersion: strings.TrimSpace(document.ParserVersion), Warnings: normalizeWarnings(document.Warnings)}
	if result.ParserVersion == "" {
		result.ParserVersion = "adapter-unknown"
	}
	if len(result.ParserVersion) > 128 || !utf8.ValidString(result.ParserVersion) || strings.TrimSpace(result.ParserVersion) != result.ParserVersion {
		return NormalizedDocument{}, designError("invalid_worker_response", ErrInvalidWorkerResponse)
	}
	if len(result.Name) > 512 || !utf8.ValidString(result.Name) || !validateID(result.Name, false) {
		return NormalizedDocument{}, designError("invalid_worker_response", ErrInvalidWorkerResponse)
	}
	pageIDs := make(map[string]struct{}, len(document.Pages))
	result.Pages = make([]Page, len(document.Pages))
	for pageIndex, page := range document.Pages {
		if pageIndex%256 == 0 {
			if err := ctx.Err(); err != nil {
				return NormalizedDocument{}, err
			}
		}
		page.ID = strings.TrimSpace(page.ID)
		page.Name = strings.TrimSpace(page.Name)
		if !validateID(page.ID, true) || page.ID == "" || !utf8.ValidString(page.ID) || !utf8.ValidString(page.Name) || len(page.Name) > 512 || !validateID(page.Name, false) {
			return NormalizedDocument{}, designError("invalid_worker_response", ErrInvalidWorkerResponse)
		}
		if _, exists := pageIDs[page.ID]; exists {
			return NormalizedDocument{}, designError("invalid_worker_response", fmt.Errorf("duplicate page ID %q: %w", page.ID, ErrInvalidWorkerResponse))
		}
		pageIDs[page.ID] = struct{}{}
		page.Position = pageIndex
		result.Pages[pageIndex] = page
	}
	nodes := make([]Node, len(document.Nodes))
	nodeIDs := make(map[string]int, len(document.Nodes))
	inputPositions := make([]int, len(document.Nodes))
	for index, node := range document.Nodes {
		if index%256 == 0 {
			if err := ctx.Err(); err != nil {
				return NormalizedDocument{}, err
			}
		}
		if node.Removed {
			return NormalizedDocument{}, designError("invalid_worker_response", fmt.Errorf("removed node %q was returned: %w", node.ID, ErrInvalidWorkerResponse))
		}
		node.ID = strings.TrimSpace(node.ID)
		node.PageID = strings.TrimSpace(node.PageID)
		node.ParentID = strings.TrimSpace(node.ParentID)
		node.Type = strings.TrimSpace(node.Type)
		node.Name = strings.TrimSpace(node.Name)
		if !validateID(node.ID, true) || !validateID(node.PageID, true) || !validateID(node.ParentID, false) || !validateID(node.Type, false) || !validateID(node.Name, false) || !utf8.ValidString(node.ID) || !utf8.ValidString(node.PageID) || !utf8.ValidString(node.ParentID) || !utf8.ValidString(node.Type) || !utf8.ValidString(node.Name) || !utf8.ValidString(node.Text) || len(node.Name) > 512 || len(node.Type) > 128 || len(node.Text) > 64*1024 {
			return NormalizedDocument{}, designError("invalid_worker_response", ErrInvalidWorkerResponse)
		}
		if _, exists := pageIDs[node.PageID]; !exists {
			return NormalizedDocument{}, designError("invalid_worker_response", ErrInvalidWorkerResponse)
		}
		if _, exists := nodeIDs[node.ID]; exists {
			return NormalizedDocument{}, designError("invalid_worker_response", fmt.Errorf("duplicate node ID %q: %w", node.ID, ErrInvalidWorkerResponse))
		}
		if !validateFinite(node.X) || !validateFinite(node.Y) || !validateFinite(node.Width) || !validateFinite(node.Height) || !validateFinite(node.Rotation) {
			return NormalizedDocument{}, designError("invalid_worker_response", fmt.Errorf("non-finite geometry for %q: %w", node.ID, ErrInvalidWorkerResponse))
		}
		if node.Width < 0 || node.Height < 0 {
			return NormalizedDocument{}, designError("invalid_worker_response", ErrInvalidWorkerResponse)
		}
		if len(node.Style) > 32 || len(node.Overrides) > 32 {
			return NormalizedDocument{}, designError("invalid_worker_response", ErrInvalidWorkerResponse)
		}
		for key, value := range node.Style {
			if len(key) > 128 || len(value) > 4096 || !utf8.ValidString(key) || !utf8.ValidString(value) || !validateID(key, true) || !validateID(value, false) {
				return NormalizedDocument{}, designError("invalid_worker_response", ErrInvalidWorkerResponse)
			}
		}
		for key, value := range node.Overrides {
			if len(key) > 128 || len(value) > 4096 || !utf8.ValidString(key) || !utf8.ValidString(value) || !validateID(key, true) || !validateID(value, false) {
				return NormalizedDocument{}, designError("invalid_worker_response", ErrInvalidWorkerResponse)
			}
		}
		node.Children = nil
		inputPositions[index] = node.Position
		node.Position = index
		nodes[index] = copyNode(node)
		nodeIDs[node.ID] = index
	}
	children := make(map[string][]int)
	for index := range nodes {
		if index%256 == 0 {
			if err := ctx.Err(); err != nil {
				return NormalizedDocument{}, err
			}
		}
		node := &nodes[index]
		if node.ParentID != "" {
			parentIndex, exists := nodeIDs[node.ParentID]
			if !exists || nodes[parentIndex].PageID != node.PageID || parentIndex == index {
				return NormalizedDocument{}, designError("invalid_worker_response", ErrInvalidWorkerResponse)
			}
			children[node.ParentID] = append(children[node.ParentID], index)
		}
	}
	// Input order is the deterministic sibling order. The source adapter may
	// provide position values, but its normalized output is already ordered and
	// positions are rewritten to avoid trusting an unvalidated map iteration.
	for parentID, indices := range children {
		orderByPosition, positionErr := positionOrder(indices, inputPositions)
		if positionErr != nil {
			return NormalizedDocument{}, positionErr
		}
		if orderByPosition {
			sort.SliceStable(indices, func(left, right int) bool {
				return inputPositions[indices[left]] < inputPositions[indices[right]]
			})
		}
		for position, index := range indices {
			nodes[index].Position = position
		}
		nodes[nodeIDs[parentID]].Children = make([]string, len(indices))
		for position, index := range indices {
			nodes[nodeIDs[parentID]].Children[position] = nodes[index].ID
		}
	}
	rootGroups := make(map[string][]int)
	for index := range nodes {
		if index%256 == 0 {
			if err := ctx.Err(); err != nil {
				return NormalizedDocument{}, err
			}
		}
		if nodes[index].ParentID == "" {
			rootGroups[nodes[index].PageID] = append(rootGroups[nodes[index].PageID], index)
		}
	}
	for _, indices := range rootGroups {
		orderByPosition, positionErr := positionOrder(indices, inputPositions)
		if positionErr != nil {
			return NormalizedDocument{}, positionErr
		}
		if orderByPosition {
			sort.SliceStable(indices, func(left, right int) bool {
				return inputPositions[indices[left]] < inputPositions[indices[right]]
			})
		}
		for position, index := range indices {
			nodes[index].Position = position
		}
	}
	for index := range nodes {
		depth := 0
		seen := make(map[string]struct{})
		for parent := nodes[index].ParentID; parent != ""; {
			if _, exists := seen[parent]; exists {
				return NormalizedDocument{}, designError("invalid_worker_response", fmt.Errorf("node graph cycle: %w", ErrInvalidWorkerResponse))
			}
			seen[parent] = struct{}{}
			parentIndex, exists := nodeIDs[parent]
			if !exists {
				return NormalizedDocument{}, designError("invalid_worker_response", ErrInvalidWorkerResponse)
			}
			depth++
			parent = nodes[parentIndex].ParentID
			if depth > s.config.MaxGraphDepth {
				return NormalizedDocument{}, designError("limit_exceeded", ErrLimit)
			}
		}
		nodes[index].Depth = depth
	}
	result.Nodes = nodes
	pageCounts := make(map[string]int, len(result.Pages))
	for _, node := range nodes {
		pageCounts[node.PageID]++
	}
	for pageIndex := range result.Pages {
		result.Pages[pageIndex].NodeCount = pageCounts[result.Pages[pageIndex].ID]
	}
	if len(document.CoverPNG) > 0 {
		if err := validatePNGBytes(document.CoverPNG, s.config); err != nil {
			result.Warnings = normalizeWarnings(append(result.Warnings, "parser cover thumbnail was invalid and was omitted"))
		} else {
			result.CoverPNG = append([]byte(nil), document.CoverPNG...)
		}
	}
	if err := ctx.Err(); err != nil {
		return NormalizedDocument{}, err
	}
	return result, nil
}

func positionOrder(indices []int, positions []int) (bool, error) {
	if len(indices) < 2 {
		return false, nil
	}
	seen := make(map[int]struct{}, len(indices))
	ordered := false
	duplicate := false
	for _, index := range indices {
		position := positions[index]
		if position < 0 {
			return false, designError("invalid_worker_response", ErrInvalidWorkerResponse)
		}
		if position != 0 {
			ordered = true
		}
		if _, exists := seen[position]; exists {
			duplicate = true
		}
		seen[position] = struct{}{}
	}
	if duplicate && ordered {
		return false, designError("invalid_worker_response", ErrInvalidWorkerResponse)
	}
	return ordered, nil
}

func (s *Service) callParserDirect(ctx context.Context, parser Parser, source []byte) (document NormalizedDocument, err error) {
	if parser == nil {
		return NormalizedDocument{}, designError("unavailable", ErrUnavailable)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			document = NormalizedDocument{}
			err = designError("unavailable", fmt.Errorf("design worker failed: %v: %w", recovered, ErrUnavailable))
		}
	}()
	result, err := parser.Parse(ctx, source)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return NormalizedDocument{}, err
		}
		return NormalizedDocument{}, err
	}
	return s.normalizeDocumentContext(ctx, result)
}

func (s *Service) callParser(ctx context.Context, parser Parser, source []byte) (NormalizedDocument, error) {
	if parser == nil {
		return NormalizedDocument{}, designError("unavailable", ErrUnavailable)
	}
	type response struct {
		document NormalizedDocument
		err      error
	}
	completed := make(chan response, 1)
	go func() {
		document, err := s.callParserDirect(ctx, parser, source)
		completed <- response{document: document, err: err}
	}()
	select {
	case <-ctx.Done():
		return NormalizedDocument{}, ctx.Err()
	case result := <-completed:
		return result.document, result.err
	}
}

func (s *Service) existingUploadLocked(operationID, fingerprint string) (UploadResult, bool) {
	if operationID == "" || s.slot.State != "active" || s.slot.LastOperationID != operationID {
		return UploadResult{}, false
	}
	if s.slot.LastFingerprint != fingerprint {
		return UploadResult{}, false
	}
	return s.uploadResultFromSlotLocked(true), true
}

func (s *Service) uploadResultFromSlotLocked(idempotent bool) UploadResult {
	return UploadResult{Outcome: "uploaded", OperationID: s.slot.LastOperationID, DocumentID: s.slot.DocumentID, Generation: s.slot.Generation, SelectionRevision: s.slot.SelectionRevision, Name: s.slot.Name, SourceName: s.slot.SourceName, Bytes: s.slot.SourceBytes, SHA256: s.slot.SHA256, UploadedAt: s.slot.UploadedAt, IndexVersion: s.slot.IndexVersion, ParserVersion: s.slot.ParserVersion, PageCount: s.slot.PageCount, NodeCount: s.slot.NodeCount, Warnings: copyStrings(s.slot.Warnings), CoverAvailable: s.slot.CoverAvailable, Idempotent: idempotent}
}

// Upload validates, parses, stages and publishes one immutable instance-wide
// document. A committed active or pending slot rejects replacement.
func (s *Service) Upload(ctx context.Context, request UploadRequest) (UploadResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if err := s.requireEnabled(); err != nil {
		return UploadResult{}, err
	}
	if request.Name == "" {
		if request.FileName != "" {
			request.Name = request.FileName
		} else {
			request.Name = request.Filename
		}
	}
	if err := validateName(request.Name); err != nil {
		return UploadResult{}, err
	}
	if err := validateOperationID(request.OperationID); err != nil {
		return UploadResult{}, err
	}
	s.mu.RLock()
	if s.pending {
		s.mu.RUnlock()
		return UploadResult{}, designError("busy", ErrBusy)
	}
	if s.slot.State == "active" && (request.OperationID == "" || request.OperationID != s.slot.LastOperationID) {
		s.mu.RUnlock()
		return UploadResult{}, designError("conflict", ErrConflict)
	}
	s.mu.RUnlock()
	data, err := s.readUpload(ctx, request)
	if err != nil {
		return UploadResult{}, err
	}
	if _, err := preflightWithLimitsContext(ctx, data, s.config.MaxUploadBytes, s.config.MaxArchiveExpansion, s.config.MaxArchiveEntries); err != nil {
		return UploadResult{}, err
	}
	sourceHash := hashBytes(data)
	fingerprint := operationFingerprint(request.Name, sourceHash)
	s.mu.Lock()
	if existing, ok := s.existingUploadLocked(request.OperationID, fingerprint); ok {
		s.mu.Unlock()
		return existing, nil
	}
	if s.pending {
		s.mu.Unlock()
		return UploadResult{}, designError("busy", ErrBusy)
	}
	if s.slot.State == "tombstone" && request.OperationID != "" && s.slot.LastOperationID == request.OperationID {
		s.mu.Unlock()
		return UploadResult{}, designError("conflict", ErrConflict)
	}
	if s.slot.State == "active" {
		s.mu.Unlock()
		return UploadResult{}, designError("conflict", ErrConflict)
	}
	if !s.config.Enabled {
		s.mu.Unlock()
		return UploadResult{}, designError("disabled", ErrDisabled)
	}
	s.pending = true
	baseGeneration := s.slot.Generation
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.pending = false
		s.mu.Unlock()
	}()
	if err := ctx.Err(); err != nil {
		return UploadResult{}, err
	}
	uploadID, err := newOpaqueID()
	if err != nil {
		return UploadResult{}, err
	}
	stageDir := filepath.Join(s.root, "staging", "upload-"+uploadID)
	if err := ensureDir(stageDir); err != nil {
		return UploadResult{}, err
	}
	defer os.RemoveAll(stageDir)
	stageSource := filepath.Join(stageDir, sourceFileName)
	if err := stageFile(ctx, stageSource, data, 0o600); err != nil {
		return UploadResult{}, fmt.Errorf("stage design source: %w", err)
	}
	s.mu.RLock()
	parser := s.config.Parser
	if parser == nil {
		parser = s.config.Worker
	}
	parseTimeout := s.config.ParseTimeout
	serviceContext := s.workCtx
	s.mu.RUnlock()
	if serviceContext == nil {
		cancelledContext, serviceCancel := context.WithCancel(context.Background())
		serviceCancel()
		serviceContext = cancelledContext
	}
	parseCtx, cancel := context.WithTimeout(ctx, parseTimeout)
	serviceCancel := make(chan struct{})
	go func() {
		select {
		case <-serviceContext.Done():
			cancel()
		case <-serviceCancel:
		}
	}()
	normalized, parseErr := s.callParser(parseCtx, parser, data)
	close(serviceCancel)
	cancel()
	if parseErr != nil {
		if errors.Is(parseErr, context.DeadlineExceeded) || errors.Is(parseErr, context.Canceled) {
			return UploadResult{}, parseErr
		}
		if errors.Is(parseErr, ErrUnavailable) {
			return UploadResult{}, parseErr
		}
		return UploadResult{}, designError("invalid_worker_response", fmt.Errorf("parse design source: %w", parseErr))
	}
	if normalized.Name == "" {
		normalized.Name = request.Name
	}
	if err := ctx.Err(); err != nil {
		return UploadResult{}, err
	}
	if err := s.ensureOpen(); err != nil {
		return UploadResult{}, err
	}
	documentID, err := newOpaqueID()
	if err != nil {
		return UploadResult{}, err
	}
	generation := baseGeneration + 1
	if generation == 0 {
		generation = 1
	}
	documentsDir, documentsErr := s.checkedDocumentsDir()
	if documentsErr != nil {
		return UploadResult{}, documentsErr
	}
	documentDir := filepath.Join(documentsDir, documentID)
	if err := ensureDir(documentDir); err != nil {
		return UploadResult{}, err
	}
	cleanupDocument := true
	defer func() {
		if cleanupDocument {
			_ = s.removeDocumentPayload(documentID)
		}
	}()
	artifact, index, err := s.writeIndex(ctx, documentID, generation, normalized)
	if err != nil {
		return UploadResult{}, fmt.Errorf("publish design index: %w", err)
	}
	sourcePath := filepath.Join(documentDir, sourceFileName)
	if err := rejectSymlinkParents(sourcePath); err != nil {
		return UploadResult{}, err
	}
	if err := os.Rename(stageSource, sourcePath); err != nil {
		return UploadResult{}, fmt.Errorf("publish design source: %w", err)
	}
	if err := os.Chmod(sourcePath, 0o444); err != nil {
		return UploadResult{}, err
	}
	coverAvailable := false
	if len(normalized.CoverPNG) > 0 {
		coverStage := filepath.Join(stageDir, coverFileName)
		if err := stageFile(ctx, coverStage, normalized.CoverPNG, 0o600); err != nil {
			return UploadResult{}, err
		}
		coverPath := filepath.Join(documentDir, coverFileName)
		if err := rejectSymlinkParents(coverPath); err != nil {
			return UploadResult{}, err
		}
		if err := os.Rename(coverStage, coverPath); err != nil {
			return UploadResult{}, err
		}
		if err := os.Chmod(coverPath, 0o444); err != nil {
			return UploadResult{}, err
		}
		coverAvailable = true
	}
	if err := syncDir(documentDir); err != nil {
		return UploadResult{}, err
	}
	uploadedAt := s.currentTime()
	next := slotState{Schema: 1, State: "active", Availability: "ready", DocumentID: documentID, Generation: generation, SelectionRevision: 1, Name: normalized.Name, SourceName: request.Name, SourceBytes: int64(len(data)), SHA256: sourceHash, UploadedAt: uploadedAt, ParserVersion: normalized.ParserVersion, IndexVersion: index.IndexVersion, IndexBytes: artifact.Bytes, IndexSHA256: artifact.SHA256, PageCount: len(index.Pages), NodeCount: len(index.Nodes), CoverAvailable: coverAvailable, Warnings: normalizeWarnings(normalized.Warnings), Selection: Selection{DocumentID: documentID, Generation: generation, Revision: 1}, LastOperationID: request.OperationID, LastFingerprint: fingerprint}
	s.mu.RLock()
	closed, enabled := s.closed, s.config.Enabled
	s.mu.RUnlock()
	if closed {
		return UploadResult{}, designError("shutting_down", ErrShuttingDown)
	}
	if !enabled {
		return UploadResult{}, designError("disabled", ErrDisabled)
	}
	published, persistErr := writeSlot(s.root, next)
	s.mu.Lock()
	if persistErr != nil {
		if published {
			s.slot = cloneSlot(next)
			s.loadErr = persistErr
			s.loaded = false
		}
		s.mu.Unlock()
		if published {
			// A slot rename may have completed before directory durability
			// failed. Retain the immutable candidate for reconciliation rather
			// than deleting a source that may already be committed on disk.
			cleanupDocument = false
		}
		return UploadResult{}, fmt.Errorf("publish design slot: %w", persistErr)
	}
	s.slot = cloneSlot(next)
	s.index = cloneIndex(index)
	s.loaded = true
	s.loadErr = nil
	s.mu.Unlock()
	cleanupDocument = false
	s.mu.RLock()
	result := s.uploadResultFromSlotLocked(false)
	s.mu.RUnlock()
	return result, nil
}

// UploadBytes is a convenience wrapper for callers with an already bounded
// source. The same limits and transaction semantics apply.
func (s *Service) UploadBytes(ctx context.Context, name string, source []byte) (UploadResult, error) {
	return s.Upload(ctx, UploadRequest{Name: name, Bytes: source})
}

// Remove durably publishes a tombstone before deleting source/index payloads.
// It remains available while disabled or when the parser worker is absent.
func (s *Service) Remove(ctx context.Context, request RemoveRequest) (RemoveResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if err := s.ensureOpen(); err != nil {
		return RemoveResult{}, err
	}
	if !validOpaqueID(request.DocumentID) {
		return RemoveResult{}, designError("invalid_request", ErrInvalidRequest)
	}
	if err := validateOperationID(request.OperationID); err != nil {
		return RemoveResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return RemoveResult{}, err
	}
	s.mu.Lock()
	if s.pending {
		s.mu.Unlock()
		return RemoveResult{}, designError("busy", ErrBusy)
	}
	current := cloneSlot(s.slot)
	expectedGeneration := firstUint64(request.ExpectedGeneration, request.Generation)
	if current.State == "tombstone" && current.DocumentID == request.DocumentID {
		if expectedGeneration != 0 && expectedGeneration != current.Generation {
			s.mu.Unlock()
			return RemoveResult{}, designError("stale_document", ErrStaleDocument)
		}
		if current.CleanupPending {
			s.pending = true
			s.mu.Unlock()
			defer func() {
				s.mu.Lock()
				s.pending = false
				s.mu.Unlock()
			}()
			cleanupErr := s.removeDocumentPayload(request.DocumentID)
			if cleanupErr == nil {
				current.CleanupPending = false
			} else {
				current.Warnings = normalizeWarnings(append(current.Warnings, "document payload cleanup pending"))
			}
			if persistErr := s.persistSlot(current); persistErr != nil {
				cleanupErr = errors.Join(cleanupErr, persistErr)
			}
			if cleanupErr != nil {
				s.logger.Warn("design tombstone cleanup pending", "document", request.DocumentID, "error", cleanupErr)
			}
			return RemoveResult{Outcome: "removed", DocumentID: current.DocumentID, Generation: current.Generation, SelectionRevision: current.SelectionRevision, RemovedAt: current.RemovedAt, CleanupPending: cleanupErr != nil, Idempotent: true}, nil
		}
		result := RemoveResult{Outcome: "removed", DocumentID: current.DocumentID, Generation: current.Generation, SelectionRevision: current.SelectionRevision, RemovedAt: current.RemovedAt, CleanupPending: current.CleanupPending, Idempotent: true}
		s.mu.Unlock()
		return result, nil
	}
	if current.State != "active" || current.DocumentID != request.DocumentID {
		s.mu.Unlock()
		return RemoveResult{}, designError("stale_document", ErrStaleDocument)
	}
	if expectedGeneration != 0 && expectedGeneration != current.Generation {
		s.mu.Unlock()
		return RemoveResult{}, designError("stale_document", ErrStaleDocument)
	}
	expectedSelection := selectionRevision(request.SelectionRevision, request.ExpectedSelectionRevision)
	if expectedSelection != 0 && expectedSelection != current.SelectionRevision {
		s.mu.Unlock()
		return RemoveResult{}, designError("stale_selection", ErrStaleSelection)
	}
	removedAt := s.currentTime()
	next := current
	next.State = "tombstone"
	next.Availability = "removed"
	next.SelectionRevision++
	if next.SelectionRevision == 0 {
		next.SelectionRevision = 1
	}
	next.Selection = Selection{Revision: next.SelectionRevision}
	next.RemovedAt = removedAt
	next.CleanupPending = true
	s.pending = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.pending = false
		s.mu.Unlock()
	}()
	published, err := writeSlot(s.root, next)
	s.mu.Lock()
	if err != nil {
		if published {
			s.slot = next
			s.loadErr = err
			s.loaded = false
		}
		s.mu.Unlock()
		return RemoveResult{}, fmt.Errorf("publish design tombstone: %w", err)
	}
	s.slot = next
	s.index = indexDocument{}
	s.loaded = false
	s.loadErr = nil
	s.mu.Unlock()
	cleanupErr := s.removeDocumentPayload(request.DocumentID)
	if cleanupErr == nil {
		next.CleanupPending = false
	} else {
		next.Warnings = normalizeWarnings(append(next.Warnings, "document payload cleanup pending"))
	}
	if persistErr := s.persistSlot(next); persistErr != nil {
		cleanupErr = errors.Join(cleanupErr, persistErr)
	}
	if cleanupErr != nil {
		s.logger.Warn("design tombstone cleanup pending", "document", request.DocumentID, "error", cleanupErr)
	}
	return RemoveResult{Outcome: "removed", DocumentID: request.DocumentID, Generation: next.Generation, SelectionRevision: next.SelectionRevision, RemovedAt: removedAt, CleanupPending: cleanupErr != nil}, nil
}

// SetSelection changes the instance-wide focus with a document/generation and
// selection revision guard. It is intentionally not exposed as an MCP tool.
func (s *Service) SetSelection(ctx context.Context, request SetSelectionRequest) (SelectionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if err := s.requireEnabled(); err != nil {
		return SelectionResult{}, err
	}
	if !validOpaqueID(request.DocumentID) {
		return SelectionResult{}, designError("invalid_request", ErrInvalidRequest)
	}
	s.mu.Lock()
	if s.slot.State != "active" || s.slot.DocumentID != request.DocumentID {
		s.mu.Unlock()
		return SelectionResult{}, designError("stale_document", ErrStaleDocument)
	}
	expectedGeneration := firstUint64(request.ExpectedGeneration, request.Generation)
	if expectedGeneration != 0 && expectedGeneration != s.slot.Generation {
		s.mu.Unlock()
		return SelectionResult{}, designError("stale_document", ErrStaleDocument)
	}
	expectedRevision := selectionRevision(request.ExpectedRevision, request.Revision, request.ExpectedSelectionRevision)
	if expectedRevision != 0 && expectedRevision != s.slot.SelectionRevision {
		s.mu.Unlock()
		return SelectionResult{}, designError("stale_selection", ErrStaleSelection)
	}
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return SelectionResult{}, err
	}
	if request.PageID != "" {
		found := false
		for _, page := range s.index.Pages {
			if page.ID == request.PageID {
				found = true
				break
			}
		}
		if !found {
			s.mu.Unlock()
			return SelectionResult{}, designError("not_found", ErrNotFound)
		}
	}
	if request.NodeID != "" {
		nodeFound := false
		for _, node := range s.index.Nodes {
			if node.ID == request.NodeID && (request.PageID == "" || node.PageID == request.PageID) {
				nodeFound = true
				break
			}
		}
		if !nodeFound {
			s.mu.Unlock()
			return SelectionResult{}, designError("not_found", ErrNotFound)
		}
	}
	next := cloneSlot(s.slot)
	next.SelectionRevision++
	if next.SelectionRevision == 0 {
		next.SelectionRevision = 1
	}
	next.Selection = Selection{DocumentID: next.DocumentID, Generation: next.Generation, PageID: request.PageID, NodeID: request.NodeID, Revision: next.SelectionRevision}
	s.mu.Unlock()
	published, err := writeSlot(s.root, next)
	s.mu.Lock()
	if err != nil {
		if published {
			s.slot = next
			s.loadErr = err
			s.loaded = false
		}
		s.mu.Unlock()
		return SelectionResult{}, err
	}
	s.slot = next
	s.mu.Unlock()
	return SelectionResult{Outcome: "selected", Selection: next.Selection}, nil
}

// Select is a concise alias for SetSelection used by UI integrations.
func (s *Service) Select(ctx context.Context, request SetSelectionRequest) (SelectionResult, error) {
	return s.SetSelection(ctx, request)
}
