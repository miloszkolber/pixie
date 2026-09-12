package design

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"
)

const (
	// MaxUploadBytes is the maximum complete uploaded .fig source.
	MaxUploadBytes int64 = 50 * 1024 * 1024
	// MaxArchiveExpansionBytes bounds declared and observed archive expansion.
	MaxArchiveExpansionBytes int64 = 256 * 1024 * 1024
	// MaxArchiveEntries bounds ZIP central-directory entries.
	MaxArchiveEntries = 4096
	// MaxNodes is the normalized graph node limit.
	MaxNodes = 100_000
	// MaxGraphDepth is the maximum normalized graph depth.
	MaxGraphDepth = 128
	// MaxQueryNodes is the maximum number of structure/text records in one read.
	MaxQueryNodes = 100
	// MaxQueryBytes bounds one normalized query response before JSON encoding.
	MaxQueryBytes = 256 * 1024
	// MaxPreviewBytes is the encoded PNG limit for a cover or future frame.
	MaxPreviewBytes int64 = 2 * 1024 * 1024
	// MaxPreviewDimension is the maximum cover/frame dimension.
	MaxPreviewDimension = 2048
	// MaxPreviewPixels bounds decoded image allocation.
	MaxPreviewPixels int64 = 4_194_304
	// MaxWorkerDiagnostics bounds parser/worker error detail retained by the service.
	MaxWorkerDiagnostics = 64 * 1024
	// MaxCursorBytes keeps cursors bounded even when copied between clients.
	MaxCursorBytes = 512
	// Compatibility aliases use the roadmap terminology used by integrations.
	MaxSourceBytes            = MaxUploadBytes
	MaxDeclaredExpansionBytes = MaxArchiveExpansionBytes
	MaxNormalizedNodes        = MaxNodes
	MaxNormalizedGraphDepth   = MaxGraphDepth
	MaxQueryResultNodes       = MaxQueryNodes
	MaxQueryResultBytes       = MaxQueryBytes
	MaxImageBytes             = MaxPreviewBytes

	defaultParseTimeout = 30 * time.Second
	defaultIndexVersion = "design-index-v1"
)

var (
	ErrDisabled              = errors.New("design module disabled")
	ErrUnavailable           = errors.New("design worker unavailable")
	ErrBusy                  = errors.New("design upload already in progress")
	ErrConflict              = errors.New("design slot conflict")
	ErrStaleDocument         = errors.New("stale design document")
	ErrStaleSelection        = errors.New("stale design selection")
	ErrNotFound              = errors.New("design document not found")
	ErrRemoved               = errors.New("design document removed")
	ErrLimit                 = errors.New("design limit exceeded")
	ErrInvalidRequest        = errors.New("invalid design request")
	ErrCorrupt               = errors.New("design storage corrupt")
	ErrInvalidWorkerResponse = errors.New("invalid design worker response")
	ErrShuttingDown          = errors.New("design service shutting down")
)

// ServiceError carries a stable category suitable for HTTP/MCP translation.
// The wrapped error is intentionally bounded before it is exposed to callers.
type ServiceError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Status    int    `json:"-"`
	Retryable bool   `json:"retryable,omitempty"`
	Cause     error  `json:"-"`
}

func (e *ServiceError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *ServiceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Code returns the machine-readable category for an error produced by Design.
func Code(err error) string {
	if err == nil {
		return ""
	}
	var typed *ServiceError
	if errors.As(err, &typed) {
		return typed.Code
	}
	switch {
	case errors.Is(err, ErrDisabled):
		return "disabled"
	case errors.Is(err, ErrUnavailable):
		return "unavailable"
	case errors.Is(err, ErrBusy):
		return "busy"
	case errors.Is(err, ErrConflict):
		return "conflict"
	case errors.Is(err, ErrStaleDocument):
		return "stale_document"
	case errors.Is(err, ErrStaleSelection):
		return "stale_selection"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrRemoved):
		return "removed"
	case errors.Is(err, ErrLimit):
		return "limit_exceeded"
	case errors.Is(err, ErrInvalidRequest):
		return "invalid_request"
	case errors.Is(err, ErrCorrupt):
		return "corrupt"
	case errors.Is(err, ErrInvalidWorkerResponse):
		return "invalid_worker_response"
	case errors.Is(err, ErrShuttingDown):
		return "shutting_down"
	default:
		return "internal"
	}
}

func category(code string, status int, retryable bool, cause error) *ServiceError {
	message := code
	if cause != nil {
		message = cause.Error()
	}
	if len(message) > MaxWorkerDiagnostics {
		message = message[:MaxWorkerDiagnostics]
	}
	return &ServiceError{Code: code, Message: message, Status: status, Retryable: retryable, Cause: cause}
}

func designError(code string, cause error) *ServiceError {
	status := 400
	retryable := false
	switch code {
	case "disabled", "unavailable":
		status, retryable = 503, true
	case "busy":
		status, retryable = 409, true
	case "conflict", "stale_document", "stale_selection":
		status = 409
	case "not_found", "removed":
		status = 404
	case "limit_exceeded":
		status = 413
	case "corrupt", "internal":
		status = 500
	case "invalid_worker_response":
		status = 502
	case "shutting_down":
		status = 503
	}
	return category(code, status, retryable, cause)
}

// Parser is the offline parser/normalizer boundary. An implementation may be
// backed by a contained worker. The Design package never pretends that its
// deterministic fixture implementation is openfig-core.
type Parser interface {
	Parse(context.Context, []byte) (NormalizedDocument, error)
}

// OfflineParser is an explicit alias used by integrations that call the
// parser a worker adapter.
type OfflineParser = Parser

// Worker and WorkerAdapter are descriptive aliases for contained parser
// integrations. A missing adapter always fails closed at upload admission.
type Worker = Parser
type WorkerAdapter = Parser

// ParserFunc adapts a function to Parser.
type ParserFunc func(context.Context, []byte) (NormalizedDocument, error)

func (f ParserFunc) Parse(ctx context.Context, source []byte) (NormalizedDocument, error) {
	if f == nil {
		return NormalizedDocument{}, designError("unavailable", ErrUnavailable)
	}
	return f(ctx, source)
}

// Config controls persistent storage and bounded parsing/query behavior.
// Enabled defaults to false; callers opt in explicitly, as Design is optional.
type Config struct {
	DataDir string
	Enabled bool

	Parser       Parser
	Worker       Worker
	ParseTimeout time.Duration
	Now          func() time.Time
	Logger       *slog.Logger

	MaxUploadBytes      int64
	MaxArchiveExpansion int64
	MaxArchiveEntries   int
	MaxNodes            int
	MaxGraphDepth       int
	MaxQueryNodes       int
	MaxQueryBytes       int
	MaxPreviewBytes     int64
	MaxPreviewDimension int
	MaxPreviewPixels    int64

	// ManagementAuthorizer, when set, is called for upload/removal/focus HTTP
	// routes. ReaderAuthorizer, when set, is called for read and artifact routes.
	// The top-level controller normally supplies these checks; nil keeps the
	// self-contained handler useful in tests and embedded local deployments.
	ManagementAuthorizer func(*http.Request) error
	ReaderAuthorizer     func(*http.Request) error
}

// UploadRequest supplies one complete local .fig stream. Bytes is convenient
// for small callers; Reader takes precedence when both are present.
type UploadRequest struct {
	Name        string
	FileName    string
	Filename    string
	Reader      io.Reader
	Bytes       []byte
	Source      []byte
	OperationID string
}

// UploadResult names the committed opaque document and generation.
type UploadResult struct {
	Outcome           string    `json:"outcome"`
	OperationID       string    `json:"operationId,omitempty"`
	DocumentID        string    `json:"documentId"`
	Generation        uint64    `json:"generation"`
	SelectionRevision uint64    `json:"selectionRevision"`
	Name              string    `json:"name"`
	SourceName        string    `json:"sourceName,omitempty"`
	Bytes             int64     `json:"bytes"`
	SHA256            string    `json:"sha256"`
	UploadedAt        time.Time `json:"uploadedAt"`
	IndexVersion      string    `json:"indexVersion"`
	ParserVersion     string    `json:"parserVersion"`
	PageCount         int       `json:"pageCount"`
	NodeCount         int       `json:"nodeCount"`
	Warnings          []string  `json:"warnings,omitempty"`
	CoverAvailable    bool      `json:"coverAvailable"`
	Idempotent        bool      `json:"idempotent,omitempty"`
}

// RemoveRequest uses document and generation CAS guards. SelectionRevision is
// optional, but callers that observed focus should send it to avoid stale UI
// removal races.
type RemoveRequest struct {
	DocumentID                string `json:"documentId"`
	ExpectedGeneration        uint64 `json:"expectedGeneration,omitempty"`
	Generation                uint64 `json:"-"`
	SelectionRevision         uint64 `json:"selectionRevision,omitempty"`
	ExpectedSelectionRevision uint64 `json:"-"`
	OperationID               string `json:"operationId,omitempty"`
}

type RemoveResult struct {
	Outcome           string    `json:"outcome"`
	DocumentID        string    `json:"documentId"`
	Generation        uint64    `json:"generation"`
	SelectionRevision uint64    `json:"selectionRevision"`
	RemovedAt         time.Time `json:"removedAt"`
	CleanupPending    bool      `json:"cleanupPending,omitempty"`
	Idempotent        bool      `json:"idempotent,omitempty"`
}

type StatusRequest struct {
	DocumentID                string `json:"documentId,omitempty"`
	ExpectedGeneration        uint64 `json:"expectedGeneration,omitempty"`
	Generation                uint64 `json:"-"`
	SelectionRevision         uint64 `json:"selectionRevision,omitempty"`
	ExpectedSelectionRevision uint64 `json:"-"`
}

// Selection is explicit shared focus, not a permission or source identity.
type Selection struct {
	DocumentID string `json:"documentId,omitempty"`
	Generation uint64 `json:"generation,omitempty"`
	PageID     string `json:"pageId,omitempty"`
	NodeID     string `json:"nodeId,omitempty"`
	Revision   uint64 `json:"revision"`
}

type SetSelectionRequest struct {
	DocumentID                string `json:"documentId"`
	ExpectedGeneration        uint64 `json:"expectedGeneration"`
	Generation                uint64 `json:"-"`
	ExpectedRevision          uint64 `json:"expectedRevision"`
	Revision                  uint64 `json:"-"`
	ExpectedSelectionRevision uint64 `json:"-"`
	PageID                    string `json:"pageId,omitempty"`
	NodeID                    string `json:"nodeId,omitempty"`
}

type Status struct {
	Outcome           string    `json:"outcome"`
	Enabled           bool      `json:"enabled"`
	Availability      string    `json:"availability"`
	DocumentID        string    `json:"documentId,omitempty"`
	Generation        uint64    `json:"generation,omitempty"`
	SelectionRevision uint64    `json:"selectionRevision,omitempty"`
	Name              string    `json:"name,omitempty"`
	SourceName        string    `json:"sourceName,omitempty"`
	SourceBytes       int64     `json:"sourceBytes,omitempty"`
	SHA256            string    `json:"sha256,omitempty"`
	UploadedAt        time.Time `json:"uploadedAt,omitempty"`
	RemovedAt         time.Time `json:"removedAt,omitempty"`
	ParserVersion     string    `json:"parserVersion,omitempty"`
	IndexVersion      string    `json:"indexVersion,omitempty"`
	PageCount         int       `json:"pageCount,omitempty"`
	NodeCount         int       `json:"nodeCount,omitempty"`
	CoverAvailable    bool      `json:"coverAvailable"`
	CoverArtifact     string    `json:"coverArtifact,omitempty"`
	Warnings          []string  `json:"warnings,omitempty"`
	Selection         Selection `json:"selection"`
}

type StructureRequest struct {
	DocumentID                string `json:"documentId"`
	ExpectedGeneration        uint64 `json:"expectedGeneration,omitempty"`
	Generation                uint64 `json:"-"`
	ExpectedRevision          uint64 `json:"selectionRevision,omitempty"`
	SelectionRevision         uint64 `json:"-"`
	ExpectedSelectionRevision uint64 `json:"-"`
	PageID                    string `json:"pageId,omitempty"`
	ParentID                  string `json:"parentId,omitempty"`
	Depth                     int    `json:"depth,omitempty"`
	Cursor                    string `json:"cursor,omitempty"`
	Limit                     int    `json:"limit,omitempty"`
}

type StructureResult struct {
	Outcome           string   `json:"outcome"`
	DocumentID        string   `json:"documentId"`
	Generation        uint64   `json:"generation"`
	SelectionRevision uint64   `json:"selectionRevision"`
	IndexVersion      string   `json:"indexVersion"`
	Pages             []Page   `json:"pages,omitempty"`
	Nodes             []Node   `json:"nodes,omitempty"`
	Items             []Node   `json:"items,omitempty"`
	NextCursor        string   `json:"nextCursor,omitempty"`
	Truncated         bool     `json:"truncated,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
}

type NodeRequest struct {
	DocumentID                string `json:"documentId"`
	NodeID                    string `json:"nodeId"`
	ExpectedGeneration        uint64 `json:"expectedGeneration,omitempty"`
	Generation                uint64 `json:"-"`
	ExpectedRevision          uint64 `json:"selectionRevision,omitempty"`
	SelectionRevision         uint64 `json:"-"`
	ExpectedSelectionRevision uint64 `json:"-"`
}

type NodeResult struct {
	Outcome           string   `json:"outcome"`
	DocumentID        string   `json:"documentId"`
	Generation        uint64   `json:"generation"`
	SelectionRevision uint64   `json:"selectionRevision"`
	IndexVersion      string   `json:"indexVersion"`
	Node              Node     `json:"node"`
	Warnings          []string `json:"warnings,omitempty"`
}

type TextRequest struct {
	DocumentID                string `json:"documentId"`
	ExpectedGeneration        uint64 `json:"expectedGeneration,omitempty"`
	Generation                uint64 `json:"-"`
	ExpectedRevision          uint64 `json:"selectionRevision,omitempty"`
	SelectionRevision         uint64 `json:"-"`
	ExpectedSelectionRevision uint64 `json:"-"`
	PageID                    string `json:"pageId,omitempty"`
	RootNodeID                string `json:"rootNodeId,omitempty"`
	Cursor                    string `json:"cursor,omitempty"`
	Limit                     int    `json:"limit,omitempty"`
}

type TextEntry struct {
	NodeID string `json:"nodeId"`
	PageID string `json:"pageId"`
	Type   string `json:"type,omitempty"`
	Name   string `json:"name,omitempty"`
	Text   string `json:"text"`
}

type TextResult struct {
	Outcome           string      `json:"outcome"`
	DocumentID        string      `json:"documentId"`
	Generation        uint64      `json:"generation"`
	SelectionRevision uint64      `json:"selectionRevision"`
	IndexVersion      string      `json:"indexVersion"`
	Entries           []TextEntry `json:"entries"`
	NextCursor        string      `json:"nextCursor,omitempty"`
	Truncated         bool        `json:"truncated,omitempty"`
	Warnings          []string    `json:"warnings,omitempty"`
}

type PreviewRequest struct {
	DocumentID                string `json:"documentId"`
	ExpectedGeneration        uint64 `json:"expectedGeneration,omitempty"`
	Generation                uint64 `json:"-"`
	ExpectedRevision          uint64 `json:"selectionRevision,omitempty"`
	SelectionRevision         uint64 `json:"-"`
	ExpectedSelectionRevision uint64 `json:"-"`
	Kind                      string `json:"kind,omitempty"`
	NodeID                    string `json:"nodeId,omitempty"`
}

type PreviewResult struct {
	Outcome           string   `json:"outcome"`
	DocumentID        string   `json:"documentId"`
	Generation        uint64   `json:"generation"`
	SelectionRevision uint64   `json:"selectionRevision"`
	PreviewKind       string   `json:"previewKind"`
	MIME              string   `json:"mime"`
	Width             int      `json:"width"`
	Height            int      `json:"height"`
	Bytes             int64    `json:"bytes"`
	PNG               []byte   `json:"png"`
	Artifact          string   `json:"artifact"`
	Warnings          []string `json:"warnings,omitempty"`
}

// Page is a normalized CANVAS page in source order.
type Page struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Position  int    `json:"position"`
	NodeCount int    `json:"nodeCount,omitempty"`
	Internal  bool   `json:"internal,omitempty"`
}

// Node is a bounded normalized layer. Coordinates are local values in the
// parser-provided coordinate space; they are not claimed to be absolute.
type Node struct {
	ID        string            `json:"id"`
	PageID    string            `json:"pageId"`
	ParentID  string            `json:"parentId,omitempty"`
	Position  int               `json:"position"`
	Depth     int               `json:"depth"`
	Type      string            `json:"type,omitempty"`
	Name      string            `json:"name,omitempty"`
	Visible   bool              `json:"visible"`
	X         float64           `json:"x,omitempty"`
	Y         float64           `json:"y,omitempty"`
	Width     float64           `json:"width,omitempty"`
	Height    float64           `json:"height,omitempty"`
	Rotation  float64           `json:"rotation,omitempty"`
	Text      string            `json:"text,omitempty"`
	Removed   bool              `json:"removed,omitempty"`
	Children  []string          `json:"children,omitempty"`
	Style     map[string]string `json:"style,omitempty"`
	Overrides map[string]string `json:"overrides,omitempty"`
}

// NormalizedDocument is the parser output accepted by the service. Parser
// implementations should keep raw .fig payloads and executable values out of
// this structure.
type NormalizedDocument struct {
	Name          string   `json:"name,omitempty"`
	ParserVersion string   `json:"parserVersion,omitempty"`
	Pages         []Page   `json:"pages"`
	Nodes         []Node   `json:"nodes"`
	Warnings      []string `json:"warnings,omitempty"`
	CoverPNG      []byte   `json:"-"`
}

// DocumentStatus is a descriptive alias for callers that prefer an explicit
// result name over the shorter Status type.
type DocumentStatus = Status

// SetSelection publishes instance-wide shared focus after a revision CAS.
type SelectionResult struct {
	Outcome   string    `json:"outcome"`
	Selection Selection `json:"selection"`
}
