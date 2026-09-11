package design

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type cursorPayload struct {
	DocumentID string `json:"d"`
	Generation uint64 `json:"g"`
	Kind       string `json:"k"`
	PageID     string `json:"p,omitempty"`
	ParentID   string `json:"r,omitempty"`
	RootID     string `json:"n,omitempty"`
	Depth      int    `json:"x,omitempty"`
	Offset     int    `json:"o"`
	MAC        string `json:"m"`
}

func makeCursor(documentID string, generation uint64, kind, pageID, parentID, rootID string, depth, offset int) string {
	payload := cursorPayload{DocumentID: documentID, Generation: generation, Kind: kind, PageID: pageID, ParentID: parentID, RootID: rootID, Depth: depth, Offset: offset}
	encoded, _ := json.Marshal(payload)
	digest := sha256.Sum256(append([]byte("pixie-design-cursor\x00"), encoded...))
	payload.MAC = base64.RawURLEncoding.EncodeToString(digest[:12])
	encoded, _ = json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func parseCursor(value, documentID string, generation uint64, kind, pageID, parentID, rootID string, depth int) (int, error) {
	if value == "" {
		return 0, nil
	}
	if len(value) > MaxCursorBytes {
		return 0, designError("limit_exceeded", ErrLimit)
	}
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0, designError("invalid_request", ErrInvalidRequest)
	}
	var payload cursorPayload
	if json.Unmarshal(encoded, &payload) != nil || payload.MAC == "" || payload.Offset < 0 || payload.DocumentID != documentID || payload.Generation != generation || payload.Kind != kind || payload.PageID != pageID || payload.ParentID != parentID || payload.RootID != rootID || payload.Depth != depth {
		return 0, designError("invalid_request", ErrInvalidRequest)
	}
	unsigned := payload
	unsigned.MAC = ""
	withoutMAC, _ := json.Marshal(unsigned)
	digest := sha256.Sum256(append([]byte("pixie-design-cursor\x00"), withoutMAC...))
	want := base64.RawURLEncoding.EncodeToString(digest[:12])
	if payload.MAC != want {
		return 0, designError("invalid_request", ErrInvalidRequest)
	}
	return payload.Offset, nil
}

func (s *Service) snapshotQuery(documentID string, generation, selectionRevision uint64) (slotState, indexDocument, error) {
	if !validOpaqueID(documentID) {
		return slotState{}, indexDocument{}, designError("invalid_request", ErrInvalidRequest)
	}
	s.mu.RLock()
	slot := cloneSlot(s.slot)
	loaded := s.loaded
	loadErr := s.loadErr
	closed := s.closed
	enabled := s.config.Enabled
	s.mu.RUnlock()
	if closed {
		return slotState{}, indexDocument{}, designError("shutting_down", ErrShuttingDown)
	}
	if !enabled {
		return slotState{}, indexDocument{}, designError("disabled", ErrDisabled)
	}
	if slot.State != "active" || slot.DocumentID != documentID {
		if slot.DocumentID == documentID && slot.State == "tombstone" {
			return slotState{}, indexDocument{}, designError("removed", ErrRemoved)
		}
		return slotState{}, indexDocument{}, designError("stale_document", ErrStaleDocument)
	}
	if generation != 0 && generation != slot.Generation {
		return slotState{}, indexDocument{}, designError("stale_document", ErrStaleDocument)
	}
	if selectionRevision != 0 && selectionRevision != slot.SelectionRevision {
		return slotState{}, indexDocument{}, designError("stale_selection", ErrStaleSelection)
	}
	if loadErr != nil || !loaded {
		if loadErr != nil {
			return slotState{}, indexDocument{}, designError("corrupt", fmt.Errorf("design index unavailable: %w", loadErr))
		}
		return slotState{}, indexDocument{}, designError("unavailable", ErrUnavailable)
	}
	s.mu.RLock()
	if s.slot.State != "active" || s.slot.DocumentID != slot.DocumentID || s.slot.Generation != slot.Generation {
		removed := s.slot.DocumentID == slot.DocumentID && s.slot.State == "tombstone"
		s.mu.RUnlock()
		if removed {
			return slotState{}, indexDocument{}, designError("removed", ErrRemoved)
		}
		return slotState{}, indexDocument{}, designError("stale_document", ErrStaleDocument)
	}
	index := cloneIndex(s.index)
	s.mu.RUnlock()
	return slot, index, nil
}

// ensureSnapshotCurrent closes the small window between query admission and
// response construction. Removal, disablement or a replacement upload must not
// allow an already-admitted query to return stale document content after the
// state transition has become visible.
func (s *Service) ensureSnapshotCurrent(snapshot slotState, expectedSelection uint64) error {
	s.mu.RLock()
	closed := s.closed
	enabled := s.config.Enabled
	loadErr := s.loadErr
	loaded := s.loaded
	current := cloneSlot(s.slot)
	s.mu.RUnlock()
	if closed {
		return designError("shutting_down", ErrShuttingDown)
	}
	if !enabled {
		return designError("disabled", ErrDisabled)
	}
	if current.State == "tombstone" && current.DocumentID == snapshot.DocumentID {
		return designError("removed", ErrRemoved)
	}
	if current.State != "active" || current.DocumentID != snapshot.DocumentID || current.Generation != snapshot.Generation {
		return designError("stale_document", ErrStaleDocument)
	}
	if expectedSelection != 0 && current.SelectionRevision != snapshot.SelectionRevision {
		return designError("stale_selection", ErrStaleSelection)
	}
	if loadErr != nil {
		return designError("corrupt", fmt.Errorf("design index unavailable: %w", loadErr))
	}
	if !loaded {
		return designError("unavailable", ErrUnavailable)
	}
	return nil
}

// Status reports metadata and availability without returning normalized content.
func (s *Service) Status(ctx context.Context, requests ...StatusRequest) (Status, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(requests) > 1 {
		return Status{}, designError("invalid_request", ErrInvalidRequest)
	}
	request := StatusRequest{}
	if len(requests) == 1 {
		request = requests[0]
	}
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	s.mu.RLock()
	slot := cloneSlot(s.slot)
	closed := s.closed
	enabled := s.config.Enabled
	loadErr := s.loadErr
	s.mu.RUnlock()
	if closed {
		return Status{}, designError("shutting_down", ErrShuttingDown)
	}
	if request.DocumentID != "" && !validOpaqueID(request.DocumentID) {
		return Status{}, designError("invalid_request", ErrInvalidRequest)
	}
	if request.DocumentID != "" && slot.DocumentID != request.DocumentID {
		return Status{}, designError("stale_document", ErrStaleDocument)
	}
	expectedGeneration := firstUint64(request.ExpectedGeneration, request.Generation)
	if expectedGeneration != 0 && expectedGeneration != slot.Generation {
		return Status{}, designError("stale_document", ErrStaleDocument)
	}
	requestedSelection := selectionRevision(request.SelectionRevision, request.ExpectedSelectionRevision)
	if requestedSelection != 0 && requestedSelection != slot.SelectionRevision {
		return Status{}, designError("stale_selection", ErrStaleSelection)
	}
	result := Status{Outcome: "status", Enabled: enabled, Availability: "empty", Selection: cloneSelection(slot.Selection)}
	if !enabled {
		result.Availability = "disabled"
	}
	if slot.State == "tombstone" {
		result.Availability = "removed"
		result.DocumentID = slot.DocumentID
		result.Generation = slot.Generation
		result.SelectionRevision = slot.SelectionRevision
		result.Name = slot.Name
		result.SourceName = slot.SourceName
		result.SourceBytes = slot.SourceBytes
		result.SHA256 = slot.SHA256
		result.UploadedAt = slot.UploadedAt
		result.RemovedAt = slot.RemovedAt
		result.ParserVersion = slot.ParserVersion
		result.IndexVersion = slot.IndexVersion
		result.PageCount = slot.PageCount
		result.NodeCount = slot.NodeCount
		result.Warnings = copyStrings(slot.Warnings)
	} else if slot.State == "active" {
		result.DocumentID = slot.DocumentID
		result.Generation = slot.Generation
		result.SelectionRevision = slot.SelectionRevision
		result.Name = slot.Name
		result.SourceName = slot.SourceName
		result.SourceBytes = slot.SourceBytes
		result.SHA256 = slot.SHA256
		result.UploadedAt = slot.UploadedAt
		result.ParserVersion = slot.ParserVersion
		result.IndexVersion = slot.IndexVersion
		result.PageCount = slot.PageCount
		result.NodeCount = slot.NodeCount
		result.CoverAvailable = slot.CoverAvailable
		result.Warnings = copyStrings(slot.Warnings)
		if slot.CoverAvailable {
			result.CoverArtifact = artifactReference(slot.DocumentID, "cover")
		}
		result.Availability = "ready"
		if !enabled {
			result.Availability = "disabled"
		} else if loadErr != nil {
			result.Availability = "corrupt"
			result.Warnings = normalizeWarnings(append(result.Warnings, "normalized index unavailable: "+loadErr.Error()))
		}
	}
	return result, nil
}

func cloneSelection(value Selection) Selection { return value }

func validateQueryLimit(limit, max int) (int, error) {
	if limit == 0 {
		return max, nil
	}
	if limit < 1 || limit > max {
		return 0, designError("limit_exceeded", ErrLimit)
	}
	return limit, nil
}

func pageFor(index indexDocument, pageID string) (Page, error) {
	for _, page := range index.Pages {
		if page.ID == pageID {
			return page, nil
		}
	}
	return Page{}, designError("not_found", ErrNotFound)
}

func structureNodes(ctx context.Context, index indexDocument, pageID, parentID string, depth int) ([]Node, error) {
	if _, err := pageFor(index, pageID); err != nil {
		return nil, err
	}
	byParent := make(map[string][]Node)
	for nodeIndex, node := range index.Nodes {
		if nodeIndex%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if node.PageID == pageID {
			byParent[node.ParentID] = append(byParent[node.ParentID], copyNode(node))
		}
	}
	for parent, nodes := range byParent {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sort.SliceStable(nodes, func(left, right int) bool {
			if nodes[left].Position != nodes[right].Position {
				return nodes[left].Position < nodes[right].Position
			}
			return nodes[left].ID < nodes[right].ID
		})
		byParent[parent] = nodes
	}
	if parentID != "" {
		found := false
		for _, node := range index.Nodes {
			if node.ID == parentID && node.PageID == pageID {
				found = true
				break
			}
		}
		if !found {
			return nil, designError("not_found", ErrNotFound)
		}
	}
	if depth == 0 {
		depth = 1
	}
	if depth < 1 || depth > MaxGraphDepth {
		return nil, designError("limit_exceeded", ErrLimit)
	}
	result := make([]Node, 0)
	var visit func(string, int)
	cancelled := false
	visit = func(parent string, level int) {
		if cancelled || level > depth {
			return
		}
		for index, node := range byParent[parent] {
			if index%256 == 0 {
				if ctx.Err() != nil {
					cancelled = true
					return
				}
			}
			result = append(result, copyNode(node))
			visit(node.ID, level+1)
		}
	}
	visit(parentID, 1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func trimStructure(value *StructureResult, maxBytes int) {
	for {
		encoded, err := json.Marshal(value)
		if err == nil && len(encoded) <= maxBytes {
			return
		}
		if len(value.Nodes) == 0 {
			value.Pages = value.Pages[:minInt(len(value.Pages), 1)]
			value.Truncated = true
			return
		}
		value.Nodes = value.Nodes[:len(value.Nodes)-1]
		value.Items = value.Nodes
		value.Truncated = true
	}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

// Structure returns paged pages or bounded layer trees. Page and node IDs are
// always returned with the exact document/generation that was read.
func (s *Service) Structure(ctx context.Context, request StructureRequest) (StructureResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	expectedSelection := selectionRevision(request.ExpectedRevision, request.SelectionRevision, request.ExpectedSelectionRevision)
	slot, index, err := s.snapshotQuery(request.DocumentID, firstUint64(request.ExpectedGeneration, request.Generation), expectedSelection)
	if err != nil {
		return StructureResult{}, err
	}
	limit, err := validateQueryLimit(request.Limit, s.config.MaxQueryNodes)
	if err != nil {
		return StructureResult{}, err
	}
	if request.Depth < 0 || request.Depth > s.config.MaxGraphDepth {
		return StructureResult{}, designError("limit_exceeded", ErrLimit)
	}
	result := StructureResult{Outcome: "structure", DocumentID: slot.DocumentID, Generation: slot.Generation, SelectionRevision: slot.SelectionRevision, IndexVersion: index.IndexVersion, Warnings: copyStrings(index.Warnings)}
	if request.PageID == "" {
		if request.ParentID != "" {
			return StructureResult{}, designError("invalid_request", ErrInvalidRequest)
		}
		offset, cursorErr := parseCursor(request.Cursor, slot.DocumentID, slot.Generation, "pages", "", "", "", 0)
		if cursorErr != nil {
			return StructureResult{}, cursorErr
		}
		if offset > len(index.Pages) {
			return StructureResult{}, designError("invalid_request", ErrInvalidRequest)
		}
		end := minInt(offset+limit, len(index.Pages))
		result.Pages = append([]Page(nil), index.Pages[offset:end]...)
		if end < len(index.Pages) {
			result.NextCursor = makeCursor(slot.DocumentID, slot.Generation, "pages", "", "", "", 0, end)
			result.Truncated = true
		}
		if _, err := boundedJSON(result, s.config.MaxQueryBytes); err != nil {
			return StructureResult{}, err
		}
		if err := ctx.Err(); err != nil {
			return StructureResult{}, err
		}
		if err := s.ensureSnapshotCurrent(slot, expectedSelection); err != nil {
			return StructureResult{}, err
		}
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return StructureResult{}, err
	}
	nodes, err := structureNodes(ctx, index, request.PageID, request.ParentID, request.Depth)
	if err != nil {
		return StructureResult{}, err
	}
	depth := request.Depth
	if depth == 0 {
		depth = 1
	}
	offset, cursorErr := parseCursor(request.Cursor, slot.DocumentID, slot.Generation, "nodes", request.PageID, request.ParentID, "", depth)
	if cursorErr != nil {
		return StructureResult{}, cursorErr
	}
	if offset > len(nodes) {
		return StructureResult{}, designError("invalid_request", ErrInvalidRequest)
	}
	end := minInt(offset+limit, len(nodes))
	result.Nodes = append([]Node(nil), nodes[offset:end]...)
	result.Items = result.Nodes
	if end < len(nodes) {
		result.NextCursor = makeCursor(slot.DocumentID, slot.Generation, "nodes", request.PageID, request.ParentID, "", depth, end)
		result.Truncated = true
	}
	trimStructure(&result, s.config.MaxQueryBytes)
	if encoded, marshalErr := json.Marshal(result); marshalErr != nil || len(encoded) > s.config.MaxQueryBytes {
		return StructureResult{}, designError("limit_exceeded", ErrLimit)
	}
	if err := ctx.Err(); err != nil {
		return StructureResult{}, err
	}
	if err := s.ensureSnapshotCurrent(slot, expectedSelection); err != nil {
		return StructureResult{}, err
	}
	return result, nil
}

// Node returns one bounded normalized layer.
func (s *Service) Node(ctx context.Context, request NodeRequest) (NodeResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	expectedSelection := selectionRevision(request.ExpectedRevision, request.SelectionRevision, request.ExpectedSelectionRevision)
	slot, index, err := s.snapshotQuery(request.DocumentID, firstUint64(request.ExpectedGeneration, request.Generation), expectedSelection)
	if err != nil {
		return NodeResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return NodeResult{}, err
	}
	for nodeIndex, node := range index.Nodes {
		if nodeIndex%256 == 0 {
			if err := ctx.Err(); err != nil {
				return NodeResult{}, err
			}
		}
		if node.ID == request.NodeID {
			result := NodeResult{Outcome: "node", DocumentID: slot.DocumentID, Generation: slot.Generation, SelectionRevision: slot.SelectionRevision, IndexVersion: index.IndexVersion, Node: copyNode(node), Warnings: copyStrings(index.Warnings)}
			if _, err := boundedJSON(result, s.config.MaxQueryBytes); err != nil {
				return NodeResult{}, err
			}
			if err := s.ensureSnapshotCurrent(slot, expectedSelection); err != nil {
				return NodeResult{}, err
			}
			return result, nil
		}
	}
	return NodeResult{}, designError("not_found", ErrNotFound)
}

// Text returns paged direct text values, never inferred component/instance
// text or a full parsed message.
func (s *Service) Text(ctx context.Context, request TextRequest) (TextResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	expectedSelection := selectionRevision(request.ExpectedRevision, request.SelectionRevision, request.ExpectedSelectionRevision)
	slot, index, err := s.snapshotQuery(request.DocumentID, firstUint64(request.ExpectedGeneration, request.Generation), expectedSelection)
	if err != nil {
		return TextResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return TextResult{}, err
	}
	limit, err := validateQueryLimit(request.Limit, s.config.MaxQueryNodes)
	if err != nil {
		return TextResult{}, err
	}
	if request.PageID != "" {
		if _, err := pageFor(index, request.PageID); err != nil {
			return TextResult{}, err
		}
	}
	if request.RootNodeID != "" {
		found := false
		for _, node := range index.Nodes {
			if node.ID == request.RootNodeID {
				found = true
				if request.PageID != "" && node.PageID != request.PageID {
					found = false
				}
				break
			}
		}
		if !found {
			return TextResult{}, designError("not_found", ErrNotFound)
		}
	}
	entries := make([]TextEntry, 0)
	byID := make(map[string]Node, len(index.Nodes))
	for _, node := range index.Nodes {
		byID[node.ID] = node
	}
	for nodeIndex, node := range index.Nodes {
		if nodeIndex%256 == 0 {
			if err := ctx.Err(); err != nil {
				return TextResult{}, err
			}
		}
		if node.Text == "" || (request.PageID != "" && node.PageID != request.PageID) {
			continue
		}
		if request.RootNodeID != "" && !isDescendantOf(node, request.RootNodeID, byID) {
			continue
		}
		entries = append(entries, TextEntry{NodeID: node.ID, PageID: node.PageID, Type: node.Type, Name: node.Name, Text: node.Text})
	}
	if err := ctx.Err(); err != nil {
		return TextResult{}, err
	}
	offset, cursorErr := parseCursor(request.Cursor, slot.DocumentID, slot.Generation, "text", request.PageID, "", request.RootNodeID, 0)
	if cursorErr != nil {
		return TextResult{}, cursorErr
	}
	if offset > len(entries) {
		return TextResult{}, designError("invalid_request", ErrInvalidRequest)
	}
	end := minInt(offset+limit, len(entries))
	result := TextResult{Outcome: "text", DocumentID: slot.DocumentID, Generation: slot.Generation, SelectionRevision: slot.SelectionRevision, IndexVersion: index.IndexVersion, Entries: append([]TextEntry(nil), entries[offset:end]...), Warnings: copyStrings(index.Warnings)}
	if end < len(entries) {
		result.NextCursor = makeCursor(slot.DocumentID, slot.Generation, "text", request.PageID, "", request.RootNodeID, 0, end)
		result.Truncated = true
	}
	boundTextResult(&result, s.config.MaxQueryBytes)
	if encoded, marshalErr := json.Marshal(result); marshalErr != nil || len(encoded) > s.config.MaxQueryBytes {
		return TextResult{}, designError("limit_exceeded", ErrLimit)
	}
	if err := ctx.Err(); err != nil {
		return TextResult{}, err
	}
	if err := s.ensureSnapshotCurrent(slot, expectedSelection); err != nil {
		return TextResult{}, err
	}
	return result, nil
}

func boundTextResult(result *TextResult, maxBytes int) {
	for {
		encoded, err := json.Marshal(result)
		if err == nil && len(encoded) <= maxBytes {
			return
		}
		result.Truncated = true
		if len(result.Entries) == 0 {
			return
		}
		last := &result.Entries[len(result.Entries)-1]
		if len(last.Text) > 0 {
			limit := len(last.Text) / 2
			if limit < 1 {
				result.Entries = result.Entries[:len(result.Entries)-1]
				continue
			}
			content := []byte(last.Text)
			if limit > len(content) {
				limit = len(content)
			}
			content = content[:limit]
			for len(content) > 0 && !utf8.Valid(content) {
				content = content[:len(content)-1]
			}
			last.Text = string(content)
			continue
		}
		result.Entries = result.Entries[:len(result.Entries)-1]
	}
}

func isDescendantOf(node Node, root string, byID map[string]Node) bool {
	if node.ID == root {
		return true
	}
	seen := make(map[string]struct{})
	for node.ParentID != "" {
		if _, exists := seen[node.ParentID]; exists {
			return false
		}
		seen[node.ParentID] = struct{}{}
		if node.ParentID == root {
			return true
		}
		parent, exists := byID[node.ParentID]
		if !exists {
			return false
		}
		node = parent
	}
	return false
}

func validatePNGBytes(content []byte, config Config) error {
	if len(content) == 0 || int64(len(content)) > config.MaxPreviewBytes {
		return designError("limit_exceeded", ErrLimit)
	}
	decoded, err := png.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		// png.DecodeConfig accepts io.Reader; use bytes without retaining a
		// second mutable representation in callers.
		return designError("invalid_worker_response", ErrInvalidWorkerResponse)
	}
	if decoded.Width <= 0 || decoded.Height <= 0 || decoded.Width > config.MaxPreviewDimension || decoded.Height > config.MaxPreviewDimension || int64(decoded.Width)*int64(decoded.Height) > config.MaxPreviewPixels {
		return designError("invalid_worker_response", ErrInvalidWorkerResponse)
	}
	return nil
}

func artifactReference(documentID, artifact string) string {
	return "pixie://design/artifacts/" + documentID + "/" + artifact + ".png"
}

// Preview serves the saved document cover only in the initial structure MVP.
// Frame requests fail closed; a cover is never silently repeated as a frame.
func (s *Service) Preview(ctx context.Context, request PreviewRequest) (PreviewResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	expectedSelection := selectionRevision(request.ExpectedRevision, request.SelectionRevision, request.ExpectedSelectionRevision)
	slot, _, err := s.snapshotQuery(request.DocumentID, firstUint64(request.ExpectedGeneration, request.Generation), expectedSelection)
	if err != nil {
		return PreviewResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return PreviewResult{}, err
	}
	kind := strings.ToLower(strings.TrimSpace(request.Kind))
	if kind == "" {
		kind = "cover"
	}
	if kind != "cover" {
		return PreviewResult{}, designError("unavailable", ErrUnavailable)
	}
	if request.NodeID != "" {
		return PreviewResult{}, designError("unavailable", ErrUnavailable)
	}
	if !slot.CoverAvailable {
		return PreviewResult{}, designError("not_found", ErrNotFound)
	}
	if err := ctx.Err(); err != nil {
		return PreviewResult{}, err
	}
	documentDir, dirErr := s.checkedDocumentDir(slot.DocumentID)
	if dirErr != nil {
		if errors.Is(dirErr, os.ErrNotExist) {
			return PreviewResult{}, designError("not_found", ErrNotFound)
		}
		return PreviewResult{}, dirErr
	}
	pathName := filepath.Join(documentDir, coverFileName)
	content, err := readBounded(pathName, s.config.MaxPreviewBytes)
	if err != nil {
		return PreviewResult{}, designError("not_found", ErrNotFound)
	}
	if err := validatePNGBytes(content, s.config); err != nil {
		return PreviewResult{}, err
	}
	config, err := png.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		return PreviewResult{}, designError("invalid_worker_response", ErrInvalidWorkerResponse)
	}
	if err := s.ensureSnapshotCurrent(slot, expectedSelection); err != nil {
		return PreviewResult{}, err
	}
	return PreviewResult{Outcome: "preview", DocumentID: slot.DocumentID, Generation: slot.Generation, SelectionRevision: slot.SelectionRevision, PreviewKind: "cover", MIME: "image/png", Width: config.Width, Height: config.Height, Bytes: int64(len(content)), PNG: append([]byte(nil), content...), Artifact: artifactReference(slot.DocumentID, "cover")}, nil
}
