package design

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const (
	slotFileName   = "slot.json"
	sourceFileName = "source.fig"
	coverFileName  = "thumbnail.png"
)

func (s *Service) load() error {
	if err := cleanStaging(filepath.Join(s.root, "staging")); err != nil {
		return fmt.Errorf("clean design staging: %w", err)
	}
	s.mu.Lock()
	s.loadErr = nil
	s.loaded = false
	s.index = indexDocument{}
	s.mu.Unlock()
	pathName := filepath.Join(s.root, slotFileName)
	data, err := readBounded(pathName, 16*1024*1024)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.mu.Lock()
			s.slot = slotState{}
			s.mu.Unlock()
			return nil
		}
		return fmt.Errorf("read design slot: %w", err)
	}
	var slot slotState
	if err := json.Unmarshal(data, &slot); err != nil {
		return fmt.Errorf("decode design slot: %w", err)
	}
	if err := validateSlot(slot); err != nil {
		return fmt.Errorf("validate design slot: %w", err)
	}
	if slot.SelectionRevision == 0 && slot.State != "empty" {
		slot.SelectionRevision = 1
	}
	if slot.State == "pending" {
		// A pending upload is never authoritative after restart. Preserve a
		// committed predecessor only if this metadata recorded one.
		if slot.DocumentID == "" && slot.Generation == 0 {
			slot.State = "empty"
			slot.SelectionRevision = 0
			slot.Selection = Selection{}
		} else {
			slot.State = "tombstone"
			slot.Selection = Selection{Revision: maxUint64(slot.SelectionRevision, 1)}
		}
		slot.Warnings = append(slot.Warnings, "interrupted upload discarded during restart")
		if _, writeErr := writeSlot(s.root, slot); writeErr != nil {
			return fmt.Errorf("publish recovered design tombstone: %w", writeErr)
		}
	}
	s.mu.Lock()
	s.slot = slot
	s.mu.Unlock()
	if slot.State == "tombstone" && slot.DocumentID != "" {
		cleanupErr := s.removeDocumentPayload(slot.DocumentID)
		if cleanupErr == nil && slot.CleanupPending {
			slot.CleanupPending = false
			if _, writeErr := writeSlot(s.root, slot); writeErr == nil {
				s.mu.Lock()
				s.slot = slot
				s.mu.Unlock()
			}
		} else if cleanupErr != nil {
			s.mu.Lock()
			s.slot.Warnings = normalizeWarnings(append(s.slot.Warnings, "document payload cleanup pending"))
			s.mu.Unlock()
		}
	}
	if slot.State != "active" {
		return nil
	}
	documentDir, documentDirErr := s.checkedDocumentDir(slot.DocumentID)
	if documentDirErr != nil {
		s.markLoadError(fmt.Errorf("open design document directory: %w", documentDirErr))
		return nil
	}
	indexFile, err := s.store.OpenIndex(slot.DocumentID)
	if err != nil {
		s.markLoadError(fmt.Errorf("open design index: %w", err))
		return nil
	}
	indexBytes, readErr := io.ReadAll(io.LimitReader(indexFile, MaxIndexArtifactBytes+1))
	closeErr := indexFile.Close()
	if readErr != nil {
		s.markLoadError(fmt.Errorf("read design index: %w", readErr))
		return nil
	}
	if closeErr != nil {
		s.markLoadError(fmt.Errorf("close design index: %w", closeErr))
		return nil
	}
	if int64(len(indexBytes)) > MaxIndexArtifactBytes || slot.IndexBytes != int64(len(indexBytes)) || (slot.IndexSHA256 != "" && slot.IndexSHA256 != hashBytes(indexBytes)) {
		s.markLoadError(designError("corrupt", ErrCorrupt))
		return nil
	}
	var index indexDocument
	if err := json.Unmarshal(indexBytes, &index); err != nil {
		s.markLoadError(fmt.Errorf("decode design index: %w", err))
		return nil
	}
	if err := validateIndex(index, slot.DocumentID, slot.Generation, s.config); err != nil {
		s.markLoadError(err)
		return nil
	}
	if index.Schema != 1 {
		s.markLoadError(designError("corrupt", ErrCorrupt))
		return nil
	}
	sourcePath := filepath.Join(documentDir, sourceFileName)
	sourceInfo, sourceErr := os.Lstat(sourcePath)
	if sourceErr != nil || sourceInfo.Mode()&os.ModeSymlink != 0 || !sourceInfo.Mode().IsRegular() || sourceInfo.Size() != slot.SourceBytes {
		s.markLoadError(fmt.Errorf("design source is missing or invalid: %w", ErrCorrupt))
		return nil
	}
	if slot.SHA256 != "" {
		sourceBytes, sourceReadErr := readBounded(sourcePath, s.config.MaxUploadBytes)
		if sourceReadErr != nil || hashBytes(sourceBytes) != slot.SHA256 {
			s.markLoadError(fmt.Errorf("design source hash mismatch: %w", ErrCorrupt))
			return nil
		}
	}
	if slot.CoverAvailable {
		coverPath := filepath.Join(documentDir, coverFileName)
		cover, coverErr := readBounded(coverPath, s.config.MaxPreviewBytes)
		if coverErr != nil || validatePNGBytes(cover, s.config) != nil {
			// The document remains valid; a bad optional cover is a warning.
			s.mu.Lock()
			s.slot.CoverAvailable = false
			s.slot.Warnings = normalizeWarnings(append(s.slot.Warnings, "saved document thumbnail is missing or invalid"))
			s.mu.Unlock()
		}
	}
	s.mu.Lock()
	s.index = cloneIndex(index)
	s.loaded = true
	s.mu.Unlock()
	return nil
}

// Reconcile cleans interrupted staging and revalidates the committed source,
// index and optional cover without ever promoting an old payload over a
// tombstone. It is safe to call after an operator repairs durable files.
//
// Gap note (FIG-02/FIG-05, roadmap/README.md): Reconcile does not rebuild
// a missing or corrupt normalized index
// from the retained source.fig. A retained-source reindex under the same
// preflight/parser/dedicated-artifact bounds remains unimplemented; do not
// treat Reconcile as a reindex. Missing or corrupt derived state stays a
// recoverable corrupt/unavailable error rather than an empty success, a
// silent source deletion, or a frame render.
func (s *Service) Reconcile() error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if err := s.ensureOpen(); err != nil {
		return err
	}
	s.mu.RLock()
	pending := s.pending
	s.mu.RUnlock()
	if pending {
		return designError("busy", ErrBusy)
	}
	return s.load()
}

func (s *Service) markLoadError(err error) {
	s.mu.Lock()
	s.loadErr = err
	s.loaded = false
	s.mu.Unlock()
}

func validateSlot(slot slotState) error {
	if slot.Schema != 1 {
		return designError("corrupt", ErrCorrupt)
	}
	switch slot.State {
	case "active", "tombstone", "empty", "pending":
	default:
		return designError("corrupt", ErrCorrupt)
	}
	if slot.Generation == 0 && slot.State != "empty" && slot.State != "pending" {
		return designError("corrupt", ErrCorrupt)
	}
	if slot.DocumentID != "" && !validOpaqueID(slot.DocumentID) {
		return designError("corrupt", ErrCorrupt)
	}
	if slot.State == "active" && slot.DocumentID == "" {
		return designError("corrupt", ErrCorrupt)
	}
	if slot.SelectionRevision == 0 && slot.State != "empty" {
		slot.SelectionRevision = 1
	}
	return nil
}

func validateIndex(index indexDocument, documentID string, generation uint64, config Config) error {
	if index.Schema != 1 || index.DocumentID != documentID || index.Generation != generation || index.IndexVersion == "" {
		return designError("corrupt", ErrCorrupt)
	}
	if len(index.Warnings) > 64 || len(index.IndexVersion) > 128 || len(index.ParserVersion) > 128 || len(index.Name) > 512 {
		return designError("corrupt", ErrCorrupt)
	}
	if len(index.Nodes) > config.MaxNodes || len(index.Pages) == 0 {
		return designError("limit_exceeded", ErrLimit)
	}
	pageIDs := make(map[string]struct{}, len(index.Pages))
	for pageIndex, page := range index.Pages {
		if !validateID(page.ID, true) || page.ID == "" || len(page.ID) > 512 || len(page.Name) > 512 {
			return designError("corrupt", ErrCorrupt)
		}
		if _, exists := pageIDs[page.ID]; exists {
			return designError("corrupt", ErrCorrupt)
		}
		pageIDs[page.ID] = struct{}{}
		if page.Position != pageIndex {
			// Position is persisted as a stable source-order value. Older
			// adapters may omit it; a sequential mismatch is still safe.
			if page.Position < 0 {
				return designError("corrupt", ErrCorrupt)
			}
		}
	}
	nodeIDs := make(map[string]struct{}, len(index.Nodes))
	nodeByID := make(map[string]Node, len(index.Nodes))
	pageCounts := make(map[string]int, len(index.Pages))
	for _, node := range index.Nodes {
		if !validateID(node.ID, true) || !validateID(node.PageID, true) || !validateID(node.ParentID, false) || node.ID == "" || node.PageID == "" {
			return designError("corrupt", ErrCorrupt)
		}
		if _, exists := pageIDs[node.PageID]; !exists {
			return designError("corrupt", ErrCorrupt)
		}
		if _, exists := nodeIDs[node.ID]; exists {
			return designError("corrupt", ErrCorrupt)
		}
		nodeIDs[node.ID] = struct{}{}
		nodeByID[node.ID] = node
		pageCounts[node.PageID]++
		if node.Depth < 0 || node.Depth > config.MaxGraphDepth || len(node.ID) > 512 || len(node.PageID) > 512 || len(node.ParentID) > 512 || len(node.Type) > 128 || len(node.Name) > 512 || len(node.Text) > 64*1024 || len(node.Children) > config.MaxNodes || len(node.Style) > 32 || len(node.Overrides) > 32 || !validateFinite(node.X) || !validateFinite(node.Y) || !validateFinite(node.Width) || !validateFinite(node.Height) || !validateFinite(node.Rotation) {
			return designError("corrupt", ErrCorrupt)
		}
		for key, value := range node.Style {
			if len(key) > 128 || len(value) > 4096 {
				return designError("corrupt", ErrCorrupt)
			}
		}
		for key, value := range node.Overrides {
			if len(key) > 128 || len(value) > 4096 {
				return designError("corrupt", ErrCorrupt)
			}
		}
	}
	for _, node := range index.Nodes {
		if node.ParentID != "" {
			parent, exists := nodeByID[node.ParentID]
			if !exists || node.ParentID == node.ID || parent.PageID != node.PageID {
				return designError("corrupt", ErrCorrupt)
			}
		}
		children := make(map[string]struct{}, len(node.Children))
		for _, child := range node.Children {
			childNode, exists := nodeByID[child]
			if !exists || childNode.ParentID != node.ID || childNode.PageID != node.PageID {
				return designError("corrupt", ErrCorrupt)
			}
			if _, duplicate := children[child]; duplicate {
				return designError("corrupt", ErrCorrupt)
			}
			children[child] = struct{}{}
		}
	}
	for _, page := range index.Pages {
		if page.NodeCount != pageCounts[page.ID] {
			return designError("corrupt", ErrCorrupt)
		}
	}
	if err := validateNoCycles(index.Nodes, nodeIDs, config.MaxGraphDepth); err != nil {
		return err
	}
	return nil
}

func validateNoCycles(nodes []Node, known map[string]struct{}, maxDepth int) error {
	byID := make(map[string]Node, len(nodes))
	for _, node := range nodes {
		byID[node.ID] = node
	}
	for _, start := range nodes {
		seen := make(map[string]struct{})
		current := start
		expectedDepth := start.Depth
		for steps := 0; ; steps++ {
			if steps > maxDepth || expectedDepth < 0 || expectedDepth > maxDepth {
				return designError("limit_exceeded", ErrLimit)
			}
			if _, exists := seen[current.ID]; exists {
				return designError("corrupt", ErrCorrupt)
			}
			if current.Depth != expectedDepth {
				return designError("corrupt", ErrCorrupt)
			}
			seen[current.ID] = struct{}{}
			if current.ParentID == "" {
				break
			}
			if _, exists := known[current.ParentID]; !exists {
				return designError("corrupt", ErrCorrupt)
			}
			current = byID[current.ParentID]
			expectedDepth--
		}
	}
	return nil
}

func maxUint64(left, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}

func cleanStaging(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".keep" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(path, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) documentDir(documentID string) string {
	return filepath.Join(s.root, "documents", documentID)
}

func (s *Service) checkedDocumentDir(documentID string) (string, error) {
	if !validOpaqueID(documentID) {
		return "", designError("invalid_request", ErrInvalidRequest)
	}
	documents, err := s.checkedDocumentsDir()
	if err != nil {
		return "", err
	}
	directory := filepath.Join(documents, documentID)
	if err := rejectSymlinkParents(directory); err != nil {
		return "", err
	}
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return "", designError("corrupt", ErrCorrupt)
	}
	return directory, nil
}

func (s *Service) checkedDocumentsDir() (string, error) {
	documents := filepath.Join(s.root, "documents")
	if err := rejectSymlinkParents(documents); err != nil {
		return "", err
	}
	documentsInfo, err := os.Lstat(documents)
	if err != nil {
		return "", err
	}
	if documentsInfo.Mode()&os.ModeSymlink != 0 || !documentsInfo.IsDir() {
		return "", designError("corrupt", ErrCorrupt)
	}
	return documents, nil
}

func (s *Service) writeIndex(ctx context.Context, documentID string, generation uint64, normalized NormalizedDocument) (IndexArtifact, indexDocument, error) {
	index := indexDocument{Schema: 1, IndexVersion: defaultIndexVersion, DocumentID: documentID, Generation: generation, Name: normalized.Name, ParserVersion: normalized.ParserVersion, Pages: append([]Page(nil), normalized.Pages...), Nodes: make([]Node, len(normalized.Nodes)), Warnings: normalizeWarnings(normalized.Warnings)}
	for i, node := range normalized.Nodes {
		index.Nodes[i] = copyNode(node)
	}
	encoded, err := json.Marshal(index)
	if err != nil {
		return IndexArtifact{}, indexDocument{}, err
	}
	if int64(len(encoded)) > MaxIndexArtifactBytes {
		return IndexArtifact{}, indexDocument{}, designError("limit_exceeded", ErrLimit)
	}
	artifact, err := s.store.WriteIndex(ctx, documentID, bytes.NewReader(encoded))
	if err != nil {
		return IndexArtifact{}, indexDocument{}, err
	}
	return artifact, index, nil
}

func (s *Service) removeDocumentPayload(documentID string) error {
	if !validOpaqueID(documentID) {
		return designError("invalid_request", ErrInvalidRequest)
	}
	dir, dirErr := s.checkedDocumentDir(documentID)
	if dirErr != nil {
		if errors.Is(dirErr, os.ErrNotExist) {
			return nil
		}
		return dirErr
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var first error
	for _, entry := range entries {
		pathName := filepath.Join(dir, entry.Name())
		info, statErr := os.Lstat(pathName)
		if statErr != nil {
			if first == nil {
				first = statErr
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if removeErr := os.Remove(pathName); removeErr != nil && first == nil {
				first = removeErr
			}
			continue
		}
		if removeErr := os.RemoveAll(pathName); removeErr != nil && first == nil {
			first = removeErr
		}
	}
	if removeErr := os.Remove(dir); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) && first == nil {
		first = removeErr
	}
	return first
}

func (s *Service) loadIndexIfNeeded() error {
	s.mu.RLock()
	loaded, state, id := s.loaded, s.slot.State, s.slot.DocumentID
	s.mu.RUnlock()
	if state != "active" {
		return nil
	}
	if loaded {
		return nil
	}
	if !validOpaqueID(id) {
		return designError("corrupt", ErrCorrupt)
	}
	file, err := s.store.OpenIndex(id)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxIndexArtifactBytes+1))
	_ = file.Close()
	if err != nil {
		return err
	}
	var index indexDocument
	if err := json.Unmarshal(data, &index); err != nil {
		return err
	}
	s.mu.RLock()
	generation := s.slot.Generation
	s.mu.RUnlock()
	if err := validateIndex(index, id, generation, s.config); err != nil {
		return err
	}
	s.mu.Lock()
	s.index = index
	s.loaded = true
	s.mu.Unlock()
	return nil
}

func (s *Service) persistSlot(next slotState) error {
	if err := validateSlot(next); err != nil {
		return err
	}
	published, err := writeSlot(s.root, next)
	if err != nil {
		if published {
			uncertain := designError("corrupt", fmt.Errorf("design slot publication durability uncertain: %w", err))
			s.mu.Lock()
			s.slot = cloneSlot(next)
			s.loadErr = uncertain
			s.loaded = false
			s.mu.Unlock()
			return uncertain
		}
		return err
	}
	s.mu.Lock()
	s.slot = cloneSlot(next)
	s.mu.Unlock()
	return nil
}

func copyFileAtomic(ctx context.Context, source, target string, max int64) error {
	if err := rejectSymlinkParents(source); err != nil {
		return err
	}
	if err := rejectSymlinkParents(target); err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > max {
		return designError("corrupt", ErrCorrupt)
	}
	in, err := os.OpenFile(source, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		_ = out.Close()
		if remove {
			_ = os.Remove(target)
		}
	}()
	if _, err := io.Copy(out, io.LimitReader(in, max+1)); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	remove = false
	return os.Chmod(target, 0o444)
}
