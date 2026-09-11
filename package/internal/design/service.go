package design

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

var opaqueIDPattern = regexp.MustCompile(`^[a-z2-7]{16,128}$`)

type slotState struct {
	Schema            int       `json:"schema"`
	State             string    `json:"state"`
	Availability      string    `json:"availability,omitempty"`
	DocumentID        string    `json:"documentId,omitempty"`
	Generation        uint64    `json:"generation"`
	SelectionRevision uint64    `json:"selectionRevision"`
	Name              string    `json:"name,omitempty"`
	SourceName        string    `json:"sourceName,omitempty"`
	SourceBytes       int64     `json:"sourceBytes,omitempty"`
	SHA256            string    `json:"sha256,omitempty"`
	UploadedAt        time.Time `json:"uploadedAt,omitempty"`
	RemovedAt         time.Time `json:"removedAt,omitempty"`
	ParserVersion     string    `json:"parserVersion,omitempty"`
	IndexVersion      string    `json:"indexVersion,omitempty"`
	IndexBytes        int64     `json:"indexBytes,omitempty"`
	IndexSHA256       string    `json:"indexSha256,omitempty"`
	PageCount         int       `json:"pageCount,omitempty"`
	NodeCount         int       `json:"nodeCount,omitempty"`
	CoverAvailable    bool      `json:"coverAvailable"`
	Warnings          []string  `json:"warnings,omitempty"`
	Selection         Selection `json:"selection"`
	CleanupPending    bool      `json:"cleanupPending,omitempty"`
	LastOperationID   string    `json:"lastOperationId,omitempty"`
	LastFingerprint   string    `json:"lastFingerprint,omitempty"`
}

type indexDocument struct {
	Schema        int      `json:"schema"`
	IndexVersion  string   `json:"indexVersion"`
	DocumentID    string   `json:"documentId"`
	Generation    uint64   `json:"generation"`
	Name          string   `json:"name,omitempty"`
	ParserVersion string   `json:"parserVersion,omitempty"`
	Pages         []Page   `json:"pages"`
	Nodes         []Node   `json:"nodes"`
	Warnings      []string `json:"warnings,omitempty"`
}

type loadedSlot struct {
	meta  slotState
	index indexDocument
}

// Service owns the instance-wide Design slot, immutable source/index payloads,
// normalized queries and optional parser worker admission.
type Service struct {
	config Config
	root   string
	store  *IndexStore
	logger *slog.Logger
	now    func() time.Time

	mu         sync.RWMutex
	mutationMu sync.Mutex
	slot       slotState
	index      indexDocument
	loaded     bool
	pending    bool
	closed     bool
	loadErr    error

	runCtx     context.Context
	stop       context.CancelFunc
	workCtx    context.Context
	workCancel context.CancelFunc
}

// New creates or recovers the persistent instance-wide Design slot. Pending
// staging is always discarded on restart; a committed document or tombstone is
// never replaced by an interrupted upload.
func New(config Config) (*Service, error) {
	if strings.TrimSpace(config.DataDir) == "" {
		return nil, fmt.Errorf("design data directory is required")
	}
	root, err := filepath.Abs(filepath.Join(config.DataDir, "mcp-design"))
	if err != nil {
		return nil, fmt.Errorf("resolve design data directory: %w", err)
	}
	if err := rejectSymlinkParents(root); err != nil {
		return nil, fmt.Errorf("validate design data directory: %w", err)
	}
	if config.ParseTimeout == 0 {
		config.ParseTimeout = defaultParseTimeout
	}
	if config.ParseTimeout <= 0 || config.ParseTimeout > defaultParseTimeout {
		return nil, fmt.Errorf("design parse timeout must be between 1ns and %s", defaultParseTimeout)
	}
	if config.MaxUploadBytes == 0 {
		config.MaxUploadBytes = MaxUploadBytes
	}
	if config.MaxArchiveExpansion == 0 {
		config.MaxArchiveExpansion = MaxArchiveExpansionBytes
	}
	if config.MaxArchiveEntries == 0 {
		config.MaxArchiveEntries = MaxArchiveEntries
	}
	if config.MaxNodes == 0 {
		config.MaxNodes = MaxNodes
	}
	if config.MaxGraphDepth == 0 {
		config.MaxGraphDepth = MaxGraphDepth
	}
	if config.MaxQueryNodes == 0 {
		config.MaxQueryNodes = MaxQueryNodes
	}
	if config.MaxQueryBytes == 0 {
		config.MaxQueryBytes = MaxQueryBytes
	}
	if config.MaxPreviewBytes == 0 {
		config.MaxPreviewBytes = MaxPreviewBytes
	}
	if config.MaxPreviewDimension == 0 {
		config.MaxPreviewDimension = MaxPreviewDimension
	}
	if config.MaxPreviewPixels == 0 {
		config.MaxPreviewPixels = MaxPreviewPixels
	}
	if config.MaxUploadBytes <= 0 || config.MaxUploadBytes > MaxUploadBytes ||
		config.MaxArchiveExpansion <= 0 || config.MaxArchiveExpansion > MaxArchiveExpansionBytes ||
		config.MaxArchiveEntries <= 0 || config.MaxArchiveEntries > MaxArchiveEntries ||
		config.MaxNodes <= 0 || config.MaxNodes > MaxNodes ||
		config.MaxGraphDepth <= 0 || config.MaxGraphDepth > MaxGraphDepth ||
		config.MaxQueryNodes <= 0 || config.MaxQueryNodes > MaxQueryNodes ||
		config.MaxQueryBytes <= 0 || config.MaxQueryBytes > MaxQueryBytes ||
		config.MaxPreviewBytes <= 0 || config.MaxPreviewBytes > MaxPreviewBytes ||
		config.MaxPreviewDimension <= 0 || config.MaxPreviewDimension > MaxPreviewDimension ||
		config.MaxPreviewPixels <= 0 || config.MaxPreviewPixels > MaxPreviewPixels {
		return nil, fmt.Errorf("design configured limits exceed roadmap bounds")
	}
	if err := os.MkdirAll(filepath.Join(root, "documents"), 0o700); err != nil {
		return nil, fmt.Errorf("create design document storage: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "staging"), 0o700); err != nil {
		return nil, fmt.Errorf("create design staging storage: %w", err)
	}
	if err := ensureDir(root); err != nil {
		return nil, err
	}
	if err := ensureDir(filepath.Join(root, "documents")); err != nil {
		return nil, err
	}
	if err := ensureDir(filepath.Join(root, "staging")); err != nil {
		return nil, err
	}
	store, err := NewIndexStore(root)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	workCtx, workCancel := context.WithCancel(runCtx)
	s := &Service{config: config, root: root, store: store, logger: config.Logger, now: config.Now, runCtx: runCtx, stop: cancel, workCtx: workCtx, workCancel: workCancel}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	if err := s.load(); err != nil {
		cancel()
		return nil, err
	}
	return s, nil
}

// NewService is an explicit constructor alias for controller integrations.
func NewService(config Config) (*Service, error) { return New(config) }

// DefaultConfig opts into the module while keeping New's zero-value disabled.
func DefaultConfig(dataDir string) Config { return Config{DataDir: dataDir, Enabled: true} }

func (s *Service) currentTime() time.Time { return s.now().UTC() }

func validOpaqueID(value string) bool { return opaqueIDPattern.MatchString(value) }

func newOpaqueID() (string, error) {
	buffer := make([]byte, 20)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate opaque document ID: %w", err)
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buffer)), nil
}

func hashBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func firstUint64(primary, alias uint64) uint64 {
	if primary != 0 {
		return primary
	}
	return alias
}

func selectionRevision(primary uint64, aliases ...uint64) uint64 {
	if primary != 0 {
		return primary
	}
	for _, alias := range aliases {
		if alias != 0 {
			return alias
		}
	}
	return 0
}

func validateName(name string) error {
	if name == "" {
		return nil
	}
	if len(name) > 256 || !utf8.ValidString(name) || strings.TrimSpace(name) != name || strings.IndexByte(name, 0) >= 0 {
		return designError("invalid_request", ErrInvalidRequest)
	}
	for _, runeValue := range name {
		if runeValue < 0x20 || runeValue == 0x7f {
			return designError("invalid_request", ErrInvalidRequest)
		}
	}
	if name != "" && !strings.EqualFold(filepath.Ext(name), ".fig") {
		return designError("invalid_request", fmt.Errorf("Design uploads must use a .fig name: %w", ErrInvalidRequest))
	}
	return nil
}

func copyStrings(values []string) []string { return append([]string(nil), values...) }

func copyNode(node Node) Node {
	node.Children = append([]string(nil), node.Children...)
	if node.Style != nil {
		node.Style = mapsClone(node.Style)
	}
	if node.Overrides != nil {
		node.Overrides = mapsClone(node.Overrides)
	}
	return node
}

func mapsClone(values map[string]string) map[string]string {
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func cloneIndex(value indexDocument) indexDocument {
	clone := value
	clone.Pages = append([]Page(nil), value.Pages...)
	clone.Nodes = make([]Node, len(value.Nodes))
	for index, node := range value.Nodes {
		clone.Nodes[index] = copyNode(node)
	}
	clone.Warnings = copyStrings(value.Warnings)
	return clone
}

func cloneSlot(value slotState) slotState {
	clone := value
	clone.Warnings = copyStrings(value.Warnings)
	clone.Selection = value.Selection
	return clone
}

func (s *Service) ensureOpen() error {
	s.mu.RLock()
	closed := s.closed
	s.mu.RUnlock()
	if closed {
		return designError("shutting_down", ErrShuttingDown)
	}
	return nil
}

func (s *Service) requireEnabled() error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	s.mu.RLock()
	enabled := s.config.Enabled
	s.mu.RUnlock()
	if !enabled {
		return designError("disabled", ErrDisabled)
	}
	return nil
}

// Shutdown stops admission. Durable source, index and tombstones remain for a
// later New call.
func (s *Service) Shutdown(contexts ...context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	workCancel := s.workCancel
	s.workCancel = nil
	s.mu.Unlock()
	if workCancel != nil {
		workCancel()
	}
	if s.stop != nil {
		s.stop()
	}
	if len(contexts) == 0 || contexts[0] == nil {
		return nil
	}
	select {
	case <-contexts[0].Done():
		return contexts[0].Err()
	case <-time.After(10 * time.Second):
		return nil
	}
}

// Enable and Disable change admission only. Disable retains source and allows
// authenticated management removal while reads report unavailable.
func (s *Service) Enable() {
	s.mu.Lock()
	if !s.closed {
		s.config.Enabled = true
		if s.workCancel == nil {
			s.workCtx, s.workCancel = context.WithCancel(s.runCtx)
		}
	}
	s.mu.Unlock()
}

func (s *Service) Disable() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.config.Enabled = false
	workCancel := s.workCancel
	s.workCancel = nil
	s.mu.Unlock()
	if workCancel != nil {
		workCancel()
	}
}

// Ready reports whether service state loaded without a fatal metadata failure.
func (s *Service) Ready() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.closed && s.loadErr == nil
}

func (s *Service) slotSnapshot() (slotState, indexDocument, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return slotState{}, indexDocument{}, false, designError("shutting_down", ErrShuttingDown)
	}
	return cloneSlot(s.slot), cloneIndex(s.index), s.loaded, s.loadErr
}

func normalizeWarnings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(value) > 1024 {
			value = value[:1024]
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if len(result) >= 64 {
			break
		}
	}
	return result
}

func validateFinite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func validateID(value string, required bool) bool {
	if value == "" {
		return !required
	}
	if len(value) > 512 || strings.TrimSpace(value) != value {
		return false
	}
	for _, runeValue := range value {
		if runeValue < 0x20 || runeValue == 0x7f {
			return false
		}
	}
	return true
}

func boundedJSON(value any, max int) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(encoded) > max {
		return nil, designError("limit_exceeded", ErrLimit)
	}
	return encoded, nil
}

// rejectSymlinkParents validates every existing path component before a
// generated Design path is opened or created. Callers still use O_NOFOLLOW for
// the final file component; this check prevents storage roots or document
// directories from redirecting operations outside the configured tree.
func rejectSymlinkParents(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				if parent := filepath.Dir(current); parent != current {
					continue
				}
			} else {
				return statErr
			}
		} else if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("design path contains symlink: %s", current)
		} else if current != absolute && !info.IsDir() {
			return fmt.Errorf("design path parent is not a directory: %s", current)
		}
		if parent := filepath.Dir(current); parent == current {
			break
		}
	}
	return nil
}

func readBounded(path string, max int64) ([]byte, error) {
	if err := rejectSymlinkParents(path); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > max {
		return nil, designError("corrupt", ErrCorrupt)
	}
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, openedInfo) || openedInfo.Mode()&os.ModeSymlink != 0 || !openedInfo.Mode().IsRegular() {
		return nil, designError("corrupt", ErrCorrupt)
	}
	data, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, designError("limit_exceeded", ErrLimit)
	}
	return data, nil
}

func ensureDir(path string) error {
	if err := rejectSymlinkParents(path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	if err := rejectSymlinkParents(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("design path is not a private directory: %s", path)
	}
	return os.Chmod(path, 0o700)
}

func syncDir(path string) error {
	if err := rejectSymlinkParents(path); err != nil {
		return err
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func stageFile(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := rejectSymlinkParents(path); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, mode)
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err := io.Copy(file, bytes.NewReader(data)); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	remove = false
	return nil
}

func writeSlot(root string, slot slotState) (bool, error) {
	data, err := json.MarshalIndent(slot, "", "  ")
	if err != nil {
		return false, err
	}
	if len(data) > 16*1024*1024 {
		return false, designError("limit_exceeded", ErrLimit)
	}
	stageDir := filepath.Join(root, "staging", "slot")
	if err := ensureDir(stageDir); err != nil {
		return false, err
	}
	stagePath := filepath.Join(stageDir, fmt.Sprintf("slot-%d.tmp", time.Now().UnixNano()))
	if err := stageFile(context.Background(), stagePath, data, 0o600); err != nil {
		return false, err
	}
	defer os.Remove(stagePath)
	primary := filepath.Join(root, "slot.json")
	if err := rejectSymlinkParents(primary); err != nil {
		return false, err
	}
	if err := os.Rename(stagePath, primary); err != nil {
		return false, err
	}
	if err := os.Chmod(primary, 0o600); err != nil {
		return true, err
	}
	if err := syncDir(root); err != nil {
		return true, err
	}
	return true, nil
}
