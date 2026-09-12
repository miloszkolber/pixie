// Package canvas implements the session-scoped Canvas module.
//
// Canvas deliberately keeps its authority, storage and worker boundary local
// to this package. Canvas IDs, session keys and credentials are opaque values;
// callers cannot turn any of them into a filesystem path or use an ID as a
// credential. Rendering is delegated to a WorkerLauncher. A launcher is an
// integration boundary, not a claim that a same-process implementation is a
// sandbox.
package canvas

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	// MaxHTMLBytes is the maximum UTF-8 HTML draft accepted by Canvas.
	MaxHTMLBytes int64 = 512 * 1024
	// MaxStorageBytes is the initial global authored/cache quota.
	MaxStorageBytes int64 = 64 * 1024 * 1024
	// MaxImageBytes is the compressed image budget before MCP base64 encoding.
	MaxImageBytes int64 = 2 * 1024 * 1024
	// Compatibility aliases make the budget names easy to discover from
	// controller code while retaining the roadmap terminology above.
	MaxHTMLSize        = MaxHTMLBytes
	MaxQuotaBytes      = MaxStorageBytes
	MaxScreenshotBytes = MaxImageBytes
	// MaxSelectorBytes bounds selectors before parsing.
	MaxSelectorBytes = 512
	// MaxMatches is the maximum number of selected results returned by a read.
	MaxMatches = 4096
	// MaxReadBytes is the maximum text/DOM returned by one read.
	MaxReadBytes = 64 * 1024
	// MaxViewportDimension is the maximum width or height accepted by a job.
	MaxViewportDimension = 2048
	// MaxViewportPixels is the maximum number of pixels allocated by a job.
	MaxViewportPixels int64 = 4_194_304
	// DefaultViewportWidth and DefaultViewportHeight are the contract defaults.
	DefaultViewportWidth  = 1280
	DefaultViewportHeight = 800
	// MaxWorkerJobs is the global active plus queued job bound.
	MaxWorkerJobs = 3 // one active, two queued
	// MaxMetaBytes is the bounded metadata/mutation-ledger ceiling.
	MaxMetaBytes int64 = 16 * 1024 * 1024

	defaultAuthorityTTL = 10 * time.Minute
	// Management capabilities are minted only for one controller-mediated
	// request.  They are intentionally shorter-lived than the native MCP
	// capability and are revoked by the registry after delegation returns.
	managementAuthorityTTL = time.Minute
	defaultWorkerTTL       = 30 * time.Second
	maxMutationIDBytes     = 256
	maxSessionIDBytes      = 512
	maxCanvasIDBytes       = 128
	maxMetaBytes           = 16 * 1024 * 1024
	metadataReserve        = 4 * 1024
)

var (
	// Errors are stable categories for controller/MCP translation. Callers can
	// use errors.Is or Code to keep transport details out of application logic.
	ErrUnauthorized          = errors.New("canvas unauthorized")
	ErrExpired               = errors.New("canvas authority expired")
	ErrRevoked               = errors.New("canvas authority revoked")
	ErrNotFound              = errors.New("canvas not found")
	ErrRemoved               = errors.New("canvas removed")
	ErrConflict              = errors.New("canvas revision conflict")
	ErrMutationConflict      = errors.New("canvas mutation ID conflict")
	ErrQuotaExceeded         = errors.New("canvas quota exceeded")
	ErrLimit                 = errors.New("canvas limit exceeded")
	ErrInvalidSelector       = errors.New("canvas selector invalid")
	ErrUnavailable           = errors.New("canvas unavailable")
	ErrDisabled              = errors.New("canvas disabled")
	ErrBusy                  = errors.New("canvas worker busy")
	ErrShuttingDown          = errors.New("canvas shutting down")
	ErrGenerationRevoked     = errors.New("canvas generation revoked")
	ErrPersistenceUncertain  = errors.New("canvas persistence outcome uncertain")
	ErrCorrupt               = errors.New("canvas storage corrupt")
	ErrInvalidRequest        = errors.New("canvas request invalid")
	ErrInvalidIdentity       = errors.New("canvas identity invalid")
	ErrInvalidWorkerResponse = errors.New("canvas worker response invalid")
)

// ServiceError preserves a stable machine-readable category and HTTP status.
// The wrapped cause is intentionally short and never includes credentials.
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

func (e *ServiceError) MarshalJSON() ([]byte, error) {
	type response struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable,omitempty"`
	}
	return json.Marshal(response{Code: e.Code, Message: e.Message, Retryable: e.Retryable})
}

func serviceError(code, message string, status int, retryable bool, cause error) *ServiceError {
	return &ServiceError{Code: code, Message: message, Status: status, Retryable: retryable, Cause: cause}
}

func category(code string, cause error) *ServiceError {
	status := 400
	retryable := false
	switch code {
	case "unauthorized", "authority_expired", "authority_revoked":
		status = 401
	case "forbidden":
		status = 403
	case "not_found", "removed":
		status = 404
	case "conflict", "mutation_conflict", "generation_revoked":
		status = 409
	case "quota_exceeded", "limit_exceeded":
		status = 413
	case "busy":
		status, retryable = 429, true
	case "unavailable", "disabled":
		status, retryable = 503, true
	case "persistence_uncertain", "corrupt":
		status = 500
	case "invalid_worker_response":
		status = 502
	}
	message := code
	if cause != nil {
		message = cause.Error()
	}
	return serviceError(code, message, status, retryable, cause)
}

// Code returns a stable error code for an error produced by this package.
func Code(err error) string {
	if err == nil {
		return ""
	}
	var typed *ServiceError
	if errors.As(err, &typed) {
		return typed.Code
	}
	switch {
	case errors.Is(err, ErrUnauthorized):
		return "unauthorized"
	case errors.Is(err, ErrExpired):
		return "authority_expired"
	case errors.Is(err, ErrRevoked):
		return "authority_revoked"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrRemoved):
		return "removed"
	case errors.Is(err, ErrConflict):
		return "conflict"
	case errors.Is(err, ErrMutationConflict):
		return "mutation_conflict"
	case errors.Is(err, ErrQuotaExceeded):
		return "quota_exceeded"
	case errors.Is(err, ErrLimit):
		return "limit_exceeded"
	case errors.Is(err, ErrInvalidSelector):
		return "invalid_selector"
	case errors.Is(err, ErrUnavailable):
		return "unavailable"
	case errors.Is(err, ErrDisabled):
		return "disabled"
	case errors.Is(err, ErrBusy):
		return "busy"
	case errors.Is(err, ErrShuttingDown):
		return "shutting_down"
	case errors.Is(err, ErrGenerationRevoked):
		return "generation_revoked"
	case errors.Is(err, ErrPersistenceUncertain):
		return "persistence_uncertain"
	case errors.Is(err, ErrCorrupt):
		return "corrupt"
	case errors.Is(err, ErrInvalidRequest):
		return "invalid_request"
	case errors.Is(err, ErrInvalidIdentity):
		return "invalid_identity"
	case errors.Is(err, ErrInvalidWorkerResponse):
		return "invalid_worker_response"
	default:
		return "internal"
	}
}

// Authority is a server-issued capability for one native session generation.
// Token is only returned by Attach and should not be put into prompts, URLs,
// artifacts or layout state. SessionKey is opaque and is not a filesystem
// identity supplied by a caller.
type Authority struct {
	Token      string    `json:"-"`
	SessionKey string    `json:"sessionKey"`
	Generation uint64    `json:"generation"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// Attachment is a descriptive alias used by controller integrations.
type Attachment = Authority

// Config controls storage, authority, worker and response limits. Enabled is
// intentionally false by default: a missing/disabled optional worker or
// module never becomes an unrestricted same-process renderer.
type Config struct {
	DataDir string
	Enabled bool

	AuthorityTTL  time.Duration
	WorkerTimeout time.Duration
	MaxActiveJobs int
	MaxQueuedJobs int

	MaxHTMLBytes     int64
	MaxStorageBytes  int64
	MaxImageBytes    int64
	MaxSelectorBytes int
	MaxMatches       int
	MaxReadBytes     int
	MaxWidth         int
	MaxHeight        int
	MaxPixels        int64
	MaxMetaBytes     int64

	// WorkerLauncher is deliberately an interface boundary. Implementations
	// may expose Render or Launch; this package validates either form at call
	// time and never treats an absent launcher as a sandbox.
	WorkerLauncher  WorkerLauncher
	RendererVersion string
	AssetSet        string
	Logger          *slog.Logger
	Now             func() time.Time
}

// WorkerLauncher is a pluggable contained-renderer boundary. The empty method
// set permits adapters that use either Render or Launch while keeping this
// package independent of a worker process protocol.
type WorkerLauncher interface{}

// RenderJob is the only input a worker receives. HTML is immutable revision
// content and no controller credentials or arbitrary output path are present.
type RenderJob struct {
	CanvasID        string
	Generation      uint64
	Version         uint64
	HTML            []byte
	Width           int
	Height          int
	DPR             int
	OfflineOnly     bool
	ContentPolicy   string
	RendererVersion string
	AssetSet        string
}

// RenderResult is validated before any artifact is committed.
type RenderResult struct {
	PNG    []byte
	MIME   string
	Width  int
	Height int
}

// RenderLauncher is the preferred worker adapter shape.
type RenderLauncher interface {
	Render(context.Context, RenderJob) (RenderResult, error)
}

// LaunchWorker is an alternate adapter shape for integrations that call their
// worker process launcher rather than a renderer.
type LaunchWorker interface {
	Launch(context.Context, RenderJob) (RenderResult, error)
}

// WorkerLauncherFunc adapts a function to the preferred launcher shape.
type WorkerLauncherFunc func(context.Context, RenderJob) (RenderResult, error)

func (f WorkerLauncherFunc) Render(ctx context.Context, job RenderJob) (RenderResult, error) {
	if f == nil {
		return RenderResult{}, category("unavailable", ErrUnavailable)
	}
	return f(ctx, job)
}

// CreateRequest optionally selects one of the small built-in templates.
type CreateRequest struct {
	TemplateID string `json:"templateId,omitempty"`
}

// WriteRequest publishes one full immutable HTML revision.
type WriteRequest struct {
	CanvasID        string `json:"canvasId"`
	ExpectedVersion uint64 `json:"expectedVersion"`
	HTML            string `json:"html"`
	MutationID      string `json:"mutationId"`
}

// ReadRequest selects one immutable revision and a bounded selector result.
type ReadRequest struct {
	CanvasID   string `json:"canvasId"`
	Version    uint64 `json:"version,omitempty"`
	Selector   string `json:"selector,omitempty"`
	MaxBytes   int    `json:"maxBytes,omitempty"`
	MaxMatches int    `json:"maxMatches,omitempty"`
	IncludeDOM bool   `json:"includeDOM,omitempty"`
}

// ScreenshotRequest captures one immutable revision at a bounded viewport.
type ScreenshotRequest struct {
	CanvasID string `json:"canvasId"`
	Version  uint64 `json:"version,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	DPR      int    `json:"dpr,omitempty"`
}

// RemoveRequest durably tombstones one generation. Zero preconditions mean
// "the current value" for management clients that already hold the authority.
type RemoveRequest struct {
	CanvasID           string `json:"canvasId"`
	ExpectedVersion    uint64 `json:"expectedVersion,omitempty"`
	ExpectedGeneration uint64 `json:"expectedGeneration,omitempty"`
	MutationID         string `json:"mutationId,omitempty"`
}

// Canvas is safe metadata for one live or tombstoned document.
type Canvas struct {
	ID           string    `json:"canvasId"`
	Generation   uint64    `json:"generation"`
	Version      uint64    `json:"version"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	Removed      bool      `json:"removed,omitempty"`
	Available    bool      `json:"available"`
	Availability string    `json:"availability"`
}

// CreateResult is returned for both a new and repeated create.
type CreateResult struct {
	Outcome    string `json:"outcome"`
	Canvas     Canvas `json:"canvas"`
	Hash       string `json:"sha256"`
	Bytes      int64  `json:"bytes"`
	Idempotent bool   `json:"idempotent,omitempty"`
}

// WriteResult records the committed mutation identity and exact revision.
type WriteResult struct {
	Outcome    string    `json:"outcome"`
	CanvasID   string    `json:"canvasId"`
	Generation uint64    `json:"generation"`
	Version    uint64    `json:"version"`
	SHA256     string    `json:"sha256"`
	Bytes      int64     `json:"bytes"`
	MutationID string    `json:"mutationId"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Idempotent bool      `json:"idempotent,omitempty"`
}

// ReadMatch is one bounded selector match.
type ReadMatch struct {
	Selector string `json:"selector"`
	Text     string `json:"text,omitempty"`
	DOM      string `json:"dom,omitempty"`
}

// ReadResult always names the exact version that was read.
type ReadResult struct {
	Outcome    string      `json:"outcome"`
	CanvasID   string      `json:"canvasId"`
	Generation uint64      `json:"generation"`
	Version    uint64      `json:"version"`
	Matches    []ReadMatch `json:"matches"`
	Text       string      `json:"text,omitempty"`
	DOM        string      `json:"dom,omitempty"`
	MatchCount int         `json:"matchCount"`
	Truncated  bool        `json:"truncated,omitempty"`
	Limit      int         `json:"limit"`
}

// ScreenshotResult contains actual PNG bytes as well as an authenticated
// artifact reference. The URL contains no authority token.
type ScreenshotResult struct {
	Outcome    string `json:"outcome"`
	CanvasID   string `json:"canvasId"`
	Generation uint64 `json:"generation"`
	Version    uint64 `json:"version"`
	MIME       string `json:"mime"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Bytes      int64  `json:"bytes"`
	PNG        []byte `json:"png"`
	Artifact   string `json:"artifact"`
	Cached     bool   `json:"cached,omitempty"`
}

// CanvasSummary is the only metadata exposed by List.
type CanvasSummary struct {
	ID             string    `json:"canvasId"`
	Generation     uint64    `json:"generation"`
	Version        uint64    `json:"version"`
	UpdatedAt      time.Time `json:"updatedAt"`
	Available      bool      `json:"available"`
	Availability   string    `json:"availability"`
	Removed        bool      `json:"removed,omitempty"`
	CleanupPending bool      `json:"cleanupPending,omitempty"`
}

// ListResult never enumerates another session's Canvas.
type ListResult struct {
	Outcome string          `json:"outcome"`
	Items   []CanvasSummary `json:"items"`
}

// RemoveResult reports logical removal separately from physical cleanup.
type RemoveResult struct {
	Outcome        string    `json:"outcome"`
	CanvasID       string    `json:"canvasId"`
	Generation     uint64    `json:"generation"`
	Version        uint64    `json:"version"`
	RemovedAt      time.Time `json:"removedAt"`
	CleanupPending bool      `json:"cleanupPending,omitempty"`
	Idempotent     bool      `json:"idempotent,omitempty"`
}

type diskMeta struct {
	Schema           int                       `json:"schema"`
	CanvasID         string                    `json:"canvasId"`
	SessionKey       string                    `json:"sessionKey"`
	SessionDigest    string                    `json:"sessionDigest"`
	Generation       uint64                    `json:"generation"`
	CurrentVersion   uint64                    `json:"currentVersion"`
	CreatedAt        time.Time                 `json:"createdAt"`
	UpdatedAt        time.Time                 `json:"updatedAt"`
	RemovedAt        *time.Time                `json:"removedAt,omitempty"`
	CleanupPending   bool                      `json:"cleanupPending,omitempty"`
	Corrupt          bool                      `json:"corrupt,omitempty"`
	CorruptReason    string                    `json:"corruptReason,omitempty"`
	CurrentSHA256    string                    `json:"currentSha256"`
	CurrentBytes     int64                     `json:"currentBytes"`
	AuthoringTool    string                    `json:"authoringTool"`
	LastMutationID   string                    `json:"lastMutationId,omitempty"`
	Mutations        map[string]mutationRecord `json:"mutations,omitempty"`
	RemoveMutationID string                    `json:"removeMutationId,omitempty"`
}

type mutationRecord struct {
	Fingerprint string      `json:"fingerprint"`
	Result      WriteResult `json:"result"`
}

type canvasState struct {
	mu        sync.RWMutex
	meta      diskMeta
	dir       string
	uncertain bool
}

type sessionState struct {
	mu               sync.Mutex
	key              string
	digest           string
	nextGen          uint64
	nativeGeneration uint64
	live             *canvasState
	lastUpdated      time.Time
}

type tokenState struct {
	digest             string
	sessionKey         string
	generation         uint64
	documentGeneration uint64
	expiresAt          time.Time
	revoked            bool
	managementOnly     bool
}

type runningJob struct {
	cancel           context.CancelFunc
	authorityDigest  string
	digest           string
	nativeGeneration uint64
	canvas           string
	generation       uint64
}

// PublishFaults injects deterministic storage failures at each
// publication/durability step for X04 coverage. A nil entry disables that
// fault. Faults before the metadata commit stay known-uncommitted; faults at
// or after the metadata commit report durability-uncertain. Production code
// leaves it zero-valued.
type PublishFaults struct {
	FailStage         error
	FailBackupRename  error
	FailPrimaryRename error
	FailDirSync       error
	FailReply         error
}

// PublishStage names the declared Canvas commit boundary where publication
// stopped. Staging and the immutable-revision rename happen before the
// metadata-pointer commit; directory sync and reply happen after it.
type PublishStage string

const (
	StageStage         PublishStage = "stage"
	StageBackupRename  PublishStage = "backup-rename"
	StagePrimaryRename PublishStage = "primary-rename"
	StageDirSync       PublishStage = "dir-sync"
	StageAcknowledge   PublishStage = "acknowledge"
)

// OutcomeKind distinguishes a known pre-publication failure from an
// installed commit and a durability/outcome-uncertain result.
type OutcomeKind string

const (
	OutcomeKnownUncommitted    OutcomeKind = "known-uncommitted"
	OutcomeInstalled           OutcomeKind = "installed"
	OutcomeDurabilityUncertain OutcomeKind = "durability-uncertain"
)

// PublishOutcome carries enough internal outcome information for callers to
// distinguish a known pre-publication failure from an installed-but-
// unconfirmed result.
type PublishOutcome struct {
	Kind           OutcomeKind
	Stage          PublishStage
	PrimaryVisible bool
	MustReconcile  bool
}

// MayDispatch reports whether the caller may trigger dependent mutations or
// unsafe effects. Only an installed outcome allows it.
func (outcome PublishOutcome) MayDispatch() bool { return outcome.Kind == OutcomeInstalled }

// MustReconcileLedger reports whether the caller must reconcile the validated
// primary before accepting dependent mutations.
func (outcome PublishOutcome) MustReconcileLedger() bool { return outcome.MustReconcile }

// PublishDecision is the guarded publish verdict used by Canvas callers.
type PublishDecision struct {
	MayDispatch        bool
	MustReconcile      bool
	MustRetainMutation bool
	Reason             string
}

// DecideCanvasPublish maps a publish outcome to its dispatch/reconcile guard.
// Only an installed outcome may dispatch; an uncertain outcome must retain
// mutation identity and reconcile the validated primary without restoring an
// old backup.
func DecideCanvasPublish(outcome PublishOutcome) PublishDecision {
	switch outcome.Kind {
	case OutcomeInstalled:
		return PublishDecision{MayDispatch: true, Reason: "canvas publish installed"}
	case OutcomeDurabilityUncertain:
		return PublishDecision{MustReconcile: true, MustRetainMutation: true, Reason: "canvas uncertain publish must reconcile validated primary before dependent mutations"}
	default:
		return PublishDecision{Reason: "canvas known-uncommitted publish preserves prior commit"}
	}
}

// Service owns Canvas state, authority and jobs. It is safe for concurrent
// callers; one canvas serializes commits while independent sessions can read.
type Service struct {
	config Config
	root   string
	logger *slog.Logger
	now    func() time.Time

	mu       sync.RWMutex
	sessions map[string]*sessionState
	canvases map[string]*canvasState
	tokens   map[string]tokenState // SHA-256(token) -> binding

	quotaMu  sync.Mutex
	usage    int64
	reserved int64

	publishMu     sync.Mutex
	publishFaults PublishFaults

	jobMu      sync.Mutex
	activeJobs int
	queuedJobs int
	jobs       map[string]map[*runningJob]struct{}

	shutdown context.Context
	stop     context.CancelFunc
	closed   bool
	corrupt  bool
}

// SetPublishFaults injects deterministic publication/durability faults for
// X04 coverage. Production code leaves it zero-valued.
func (s *Service) SetPublishFaults(faults PublishFaults) {
	if s == nil {
		return
	}
	s.publishMu.Lock()
	s.publishFaults = faults
	s.publishMu.Unlock()
}

func (s *Service) getPublishFaults() PublishFaults {
	if s == nil {
		return PublishFaults{}
	}
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	return s.publishFaults
}

func (s *Service) effectiveMaxMetaBytes() int64 {
	limit := s.config.MaxMetaBytes
	if limit == 0 {
		limit = int64(maxMetaBytes)
	}
	return limit
}

var identityPattern = regexp.MustCompile(`^[a-z2-7]{16,128}$`)

// New creates or loads a Canvas service. The optional worker is never inferred
// from PATH or silently substituted with an unrestricted browser process.
func New(config Config) (*Service, error) {
	if strings.TrimSpace(config.DataDir) == "" {
		return nil, fmt.Errorf("canvas data directory is required")
	}
	root, err := filepath.Abs(config.DataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve canvas data directory: %w", err)
	}
	if err := rejectSymlinkParents(filepath.Join(root, "mcp-canvas")); err != nil {
		return nil, fmt.Errorf("validate canvas data directory: %w", err)
	}
	if config.AuthorityTTL == 0 {
		config.AuthorityTTL = defaultAuthorityTTL
	}
	if config.AuthorityTTL <= 0 {
		return nil, fmt.Errorf("canvas authority TTL must be positive")
	}
	if config.WorkerTimeout == 0 {
		config.WorkerTimeout = defaultWorkerTTL
	}
	if config.WorkerTimeout <= 0 || config.WorkerTimeout > defaultWorkerTTL {
		return nil, fmt.Errorf("canvas worker timeout must be between 1ns and %s", defaultWorkerTTL)
	}
	if config.MaxActiveJobs == 0 {
		config.MaxActiveJobs = 1
	}
	if config.MaxQueuedJobs == 0 {
		config.MaxQueuedJobs = 2
	}
	if config.MaxActiveJobs != 1 || config.MaxQueuedJobs != 2 {
		return nil, fmt.Errorf("canvas jobs are limited to one active and two queued jobs")
	}
	if config.MaxHTMLBytes == 0 {
		config.MaxHTMLBytes = MaxHTMLBytes
	}
	if strings.TrimSpace(config.RendererVersion) == "" {
		config.RendererVersion = "canvas-renderer-v1"
	}
	if strings.TrimSpace(config.AssetSet) == "" {
		config.AssetSet = "offline-default"
	}
	if config.MaxStorageBytes == 0 {
		config.MaxStorageBytes = MaxStorageBytes
	}
	if config.MaxImageBytes == 0 {
		config.MaxImageBytes = MaxImageBytes
	}
	if config.MaxSelectorBytes == 0 {
		config.MaxSelectorBytes = MaxSelectorBytes
	}
	if config.MaxMatches == 0 {
		config.MaxMatches = MaxMatches
	}
	if config.MaxReadBytes == 0 {
		config.MaxReadBytes = MaxReadBytes
	}
	if config.MaxWidth == 0 {
		config.MaxWidth = MaxViewportDimension
	}
	if config.MaxHeight == 0 {
		config.MaxHeight = MaxViewportDimension
	}
	if config.MaxPixels == 0 {
		config.MaxPixels = MaxViewportPixels
	}
	if config.MaxMetaBytes == 0 {
		config.MaxMetaBytes = MaxMetaBytes
	}
	if config.MaxHTMLBytes <= 0 || config.MaxHTMLBytes > MaxHTMLBytes || config.MaxStorageBytes <= 0 || config.MaxStorageBytes > MaxStorageBytes || config.MaxImageBytes <= 0 || config.MaxImageBytes > MaxImageBytes {
		return nil, fmt.Errorf("canvas configured limits exceed the roadmap bounds")
	}
	if config.MaxMetaBytes <= 0 || config.MaxMetaBytes > MaxMetaBytes {
		return nil, fmt.Errorf("canvas metadata limit exceeds the roadmap bound")
	}
	if config.MaxSelectorBytes <= 0 || config.MaxSelectorBytes > MaxSelectorBytes || config.MaxMatches <= 0 || config.MaxMatches > MaxMatches || config.MaxReadBytes <= 0 || config.MaxReadBytes > MaxReadBytes {
		return nil, fmt.Errorf("canvas read limits exceed the roadmap bounds")
	}
	if config.MaxWidth <= 0 || config.MaxWidth > MaxViewportDimension || config.MaxHeight <= 0 || config.MaxHeight > MaxViewportDimension || config.MaxPixels <= 0 || config.MaxPixels > MaxViewportPixels {
		return nil, fmt.Errorf("canvas viewport limits exceed the roadmap bounds")
	}
	if err := os.MkdirAll(filepath.Join(root, "mcp-canvas"), 0o700); err != nil {
		return nil, fmt.Errorf("create canvas storage: %w", err)
	}
	storageRoot := filepath.Join(root, "mcp-canvas")
	if err := ensurePrivateDirectory(storageRoot); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		config:   config,
		root:     storageRoot,
		logger:   config.Logger,
		now:      config.Now,
		sessions: make(map[string]*sessionState),
		canvases: make(map[string]*canvasState),
		tokens:   make(map[string]tokenState),
		jobs:     make(map[string]map[*runningJob]struct{}),
		shutdown: ctx,
		stop:     cancel,
	}
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

// NewService is an explicit alias for integration code that uses Service
// constructors alongside the other Pixie modules.
func NewService(config Config) (*Service, error) { return New(config) }

// DefaultConfig returns an enabled storage configuration for a caller that
// deliberately opts into Canvas. New itself preserves the default-disabled
// module posture when Config.Enabled is false.
func DefaultConfig(dataDir string) Config { return Config{DataDir: dataDir, Enabled: true} }

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect canvas directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("canvas directory is not a real directory")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("protect canvas directory: %w", err)
	}
	return nil
}

func (s *Service) currentTime() time.Time { return s.now().UTC() }

func randomIdentity(prefix string) (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate %s identity: %w", prefix, err)
	}
	// Base32 without padding provides a path-safe, opaque identity.
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	var out strings.Builder
	for i := 0; i < len(b)*8; i += 5 {
		var value byte
		for bit := 0; bit < 5; bit++ {
			index := i + bit
			value <<= 1
			if index < len(b)*8 && b[index/8]&(1<<uint(7-index%8)) != 0 {
				value++
			}
		}
		out.WriteByte(alphabet[value])
	}
	return out.String(), nil
}

func validIdentity(value string) bool { return identityPattern.MatchString(value) }

func validateSessionID(value string) error {
	if strings.TrimSpace(value) == "" || len(value) > maxSessionIDBytes || strings.TrimSpace(value) != value {
		return fmt.Errorf("session ID is empty, padded or too long: %w", ErrInvalidIdentity)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("session ID contains a control character: %w", ErrInvalidIdentity)
		}
	}
	return nil
}

func validateCanvasID(value string) error {
	if len(value) > maxCanvasIDBytes || !validIdentity(value) {
		return fmt.Errorf("invalid canvas ID: %w", ErrInvalidIdentity)
	}
	return nil
}

func validateMutationID(value string, optional bool) error {
	if value == "" && optional {
		return nil
	}
	if value == "" || len(value) > maxMutationIDBytes || strings.TrimSpace(value) != value {
		return fmt.Errorf("invalid mutation ID: %w", ErrInvalidRequest)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("mutation ID contains a control character: %w", ErrInvalidRequest)
		}
	}
	return nil
}

func sessionDigest(sessionID string) string {
	digest := sha256.Sum256([]byte("pixie-canvas-session\x00" + sessionID))
	return hex.EncodeToString(digest[:])
}

func tokenDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func constantEqual(left, right string) bool {
	a := sha256.Sum256([]byte(left))
	b := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

func (s *Service) scopeForSessionLocked(sessionID string) (*sessionState, error) {
	digest := sessionDigest(sessionID)
	if scope := s.sessions[digest]; scope != nil {
		return scope, nil
	}
	key, err := randomIdentity("session")
	if err != nil {
		return nil, err
	}
	scope := &sessionState{key: key, digest: digest, nextGen: 1}
	s.sessions[digest] = scope
	return scope, nil
}

// Attach issues a fresh short-lived capability bound to the exact native
// session and child generation. Attaching a new child generation revokes
// older-generation tokens, while same-generation clients may coexist.
func (s *Service) Attach(sessionID string, generations ...uint64) (Authority, error) {
	return s.attach(sessionID, false, generations...)
}

// AttachManagement issues a short-lived capability for one controller-owned
// status/removal/artifact request. It deliberately does not require Canvas to
// be enabled or its renderer to be ready: retained documents must remain
// manageable while the optional worker is unavailable. The capability is
// management-only and cannot be used by the model-facing MCP tools.
func (s *Service) AttachManagement(sessionID string, generations ...uint64) (Authority, error) {
	return s.attach(sessionID, true, generations...)
}

func (s *Service) attach(sessionID string, managementOnly bool, generations ...uint64) (Authority, error) {
	if err := validateSessionID(sessionID); err != nil {
		return Authority{}, err
	}
	if len(generations) > 1 {
		return Authority{}, category("invalid_request", ErrInvalidRequest)
	}
	generation := uint64(0)
	if len(generations) == 1 {
		generation = generations[0]
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Authority{}, category("shutting_down", ErrShuttingDown)
	}
	scope, err := s.scopeForSessionLocked(sessionID)
	s.mu.Unlock()
	if err != nil {
		return Authority{}, err
	}

	// Session-owned document fields are protected by scope.mu. Do not hold the
	// service lock while taking it: Create/Remove publish while holding the
	// session lock and then briefly acquire s.mu, so reversing that order here
	// would introduce a shutdown/attach deadlock and a live-pointer race.
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if generation == 0 {
		generation = scope.nativeGeneration
		if generation == 0 {
			generation = 1
		}
	}
	documentGeneration := scope.nextGen
	if scope.live != nil {
		scope.live.mu.RLock()
		liveGeneration := scope.live.meta.Generation
		scope.live.mu.RUnlock()
		if liveGeneration != 0 {
			documentGeneration = liveGeneration
		}
	}
	if documentGeneration == 0 {
		documentGeneration = 1
	}
	scope.nativeGeneration = generation
	var revokedGenerations []uint64
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Authority{}, category("shutting_down", ErrShuttingDown)
	}
	for digest, token := range s.tokens {
		if !token.expiresAt.After(s.currentTime()) {
			delete(s.tokens, digest)
			continue
		}
		if constantEqual(token.digest, scope.digest) && token.generation != generation {
			delete(s.tokens, digest)
			revokedGenerations = append(revokedGenerations, token.generation)
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		s.mu.Unlock()
		return Authority{}, fmt.Errorf("issue canvas authority: %w", err)
	}
	token := hex.EncodeToString(raw)
	ttl := s.config.AuthorityTTL
	if managementOnly && ttl > managementAuthorityTTL {
		ttl = managementAuthorityTTL
	}
	expires := s.currentTime().Add(ttl)
	s.tokens[tokenDigest(token)] = tokenState{digest: scope.digest, sessionKey: scope.key, generation: generation, documentGeneration: documentGeneration, expiresAt: expires, managementOnly: managementOnly}
	authority := Authority{Token: token, SessionKey: scope.key, Generation: generation, ExpiresAt: expires}
	s.mu.Unlock()
	for _, revokedGeneration := range revokedGenerations {
		s.cancelSessionGeneration(scope.digest, revokedGeneration)
	}
	return authority, nil
}

// Authorize validates an Authority or the compatibility form
// Authorize(token, sessionKey, generation). It intentionally accepts the
// latter through variadic arguments so adapters can remain agnostic to the
// concrete capability struct without weakening server-side checks.
func (s *Service) Authorize(value any, rest ...any) error {
	authority, err := authorityFromArgs(value, rest...)
	if err != nil {
		return err
	}
	return s.authorizeAuthority(authority)
}

func authorityFromArgs(value any, rest ...any) (Authority, error) {
	switch typed := value.(type) {
	case Authority:
		if len(rest) != 0 {
			return Authority{}, category("unauthorized", ErrUnauthorized)
		}
		return typed, nil
	case *Authority:
		if typed == nil || len(rest) != 0 {
			return Authority{}, category("unauthorized", ErrUnauthorized)
		}
		return *typed, nil
	case string:
		if len(rest) != 2 {
			return Authority{}, category("unauthorized", ErrUnauthorized)
		}
		sessionKey, ok := rest[0].(string)
		if !ok {
			return Authority{}, category("unauthorized", ErrUnauthorized)
		}
		generation, ok := numericUint64(rest[1])
		if !ok {
			return Authority{}, category("unauthorized", ErrUnauthorized)
		}
		return Authority{Token: typed, SessionKey: sessionKey, Generation: generation}, nil
	default:
		return Authority{}, category("unauthorized", ErrUnauthorized)
	}
}

func numericUint64(value any) (uint64, bool) {
	switch typed := value.(type) {
	case uint64:
		return typed, true
	case uint:
		return uint64(typed), true
	case uint32:
		return uint64(typed), true
	case int:
		return uint64(typed), typed >= 0
	case int64:
		return uint64(typed), typed >= 0
	case int32:
		return uint64(typed), typed >= 0
	default:
		return 0, false
	}
}

func (s *Service) authorizeAuthority(authority Authority) error {
	if authority.Token == "" || authority.SessionKey == "" || !validIdentity(authority.SessionKey) {
		return category("unauthorized", ErrUnauthorized)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.tokens[tokenDigest(authority.Token)]
	if !ok || entry.revoked || entry.managementOnly {
		return category("authority_revoked", ErrRevoked)
	}
	if !constantEqual(entry.sessionKey, authority.SessionKey) || entry.generation != authority.Generation {
		return category("unauthorized", ErrUnauthorized)
	}
	if !entry.expiresAt.IsZero() && !s.currentTime().Before(entry.expiresAt) {
		delete(s.tokens, tokenDigest(authority.Token))
		return category("authority_expired", ErrExpired)
	}
	return nil
}

// authorizeManagementAuthority preserves the authenticated owner's ability to
// remove retained documents while the module is disabled. Disable marks old
// capabilities management-only; they cannot read, write or render through
// authorizeAuthority.
func (s *Service) authorizeManagementAuthority(authority Authority) error {
	if authority.Token == "" || authority.SessionKey == "" || !validIdentity(authority.SessionKey) {
		return category("unauthorized", ErrUnauthorized)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.tokens[tokenDigest(authority.Token)]
	// Native capabilities may remove while Canvas is enabled. Disable marks
	// those same bindings revoked+managementOnly so retained cleanup remains
	// possible without reopening model-facing reads/writes.
	if !ok || (!entry.managementOnly && entry.revoked) {
		return category("authority_revoked", ErrRevoked)
	}
	if !constantEqual(entry.sessionKey, authority.SessionKey) || entry.generation != authority.Generation {
		return category("unauthorized", ErrUnauthorized)
	}
	if !entry.expiresAt.IsZero() && !s.currentTime().Before(entry.expiresAt) {
		delete(s.tokens, tokenDigest(authority.Token))
		return category("authority_expired", ErrExpired)
	}
	return nil
}

// authorizeControllerManagementAuthority is stricter than the service-level
// management check used by the canvas_remove MCP tool. HTTP management
// delegation must carry a capability explicitly minted by AttachManagement;
// a native model capability cannot be reclassified by merely placing it in a
// context value.
func (s *Service) authorizeControllerManagementAuthority(authority Authority) error {
	if err := s.authorizeManagementAuthority(authority); err != nil {
		return err
	}
	s.mu.RLock()
	entry, ok := s.tokens[tokenDigest(authority.Token)]
	s.mu.RUnlock()
	if !ok || !entry.managementOnly {
		return category("unauthorized", ErrUnauthorized)
	}
	return nil
}

func (s *Service) managementSessionForAuthority(authority Authority) (*sessionState, error) {
	if err := s.authorizeManagementAuthority(authority); err != nil {
		return nil, err
	}
	s.mu.RLock()
	for _, session := range s.sessions {
		if constantEqual(session.key, authority.SessionKey) {
			s.mu.RUnlock()
			return session, nil
		}
	}
	s.mu.RUnlock()
	return nil, category("unauthorized", ErrUnauthorized)
}

func (s *Service) managementDocumentGeneration(authority Authority) (uint64, error) {
	if err := s.authorizeManagementAuthority(authority); err != nil {
		return 0, err
	}
	s.mu.RLock()
	entry, ok := s.tokens[tokenDigest(authority.Token)]
	s.mu.RUnlock()
	if !ok {
		return 0, category("authority_revoked", ErrRevoked)
	}
	return entry.documentGeneration, nil
}

// authorityDocumentGeneration returns the document-generation binding captured
// when the capability was issued. Native transport generations and Canvas
// document generations are intentionally separate identities.
func (s *Service) authorityDocumentGeneration(authority Authority) (uint64, error) {
	if err := s.authorizeAuthority(authority); err != nil {
		return 0, err
	}
	s.mu.RLock()
	entry, ok := s.tokens[tokenDigest(authority.Token)]
	s.mu.RUnlock()
	if !ok || entry.revoked {
		return 0, category("authority_revoked", ErrRevoked)
	}
	return entry.documentGeneration, nil
}

func (s *Service) authorizeDocumentGeneration(authority Authority, generation uint64) error {
	documentGeneration, err := s.authorityDocumentGeneration(authority)
	if err != nil {
		return err
	}
	if documentGeneration != generation {
		return category("generation_revoked", ErrGenerationRevoked)
	}
	return nil
}

// advanceAuthorityDocumentGeneration permits the authenticated native session
// owner to create the next document after its prior document was tombstoned.
// The native generation must still match; a caller cannot use this transition
// to revive an authority across a native reconnect/replacement.
func (s *Service) advanceAuthorityDocumentGeneration(authority Authority, generation uint64) error {
	if generation == 0 {
		return category("generation_revoked", ErrGenerationRevoked)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	digest := tokenDigest(authority.Token)
	entry, ok := s.tokens[digest]
	if !ok || entry.revoked || entry.managementOnly {
		return category("authority_revoked", ErrRevoked)
	}
	if !constantEqual(entry.sessionKey, authority.SessionKey) || entry.generation != authority.Generation {
		return category("unauthorized", ErrUnauthorized)
	}
	if !entry.expiresAt.IsZero() && !s.currentTime().Before(entry.expiresAt) {
		delete(s.tokens, digest)
		return category("authority_expired", ErrExpired)
	}
	if entry.documentGeneration > generation {
		return category("generation_revoked", ErrGenerationRevoked)
	}
	entry.documentGeneration = generation
	s.tokens[digest] = entry
	return nil
}

// Revoke removes a single capability. Unknown tokens are a no-op.
func (s *Service) Revoke(authority Authority) {
	if s == nil {
		return
	}
	digest := tokenDigest(authority.Token)
	s.mu.Lock()
	_, found := s.tokens[digest]
	delete(s.tokens, digest)
	s.mu.Unlock()
	if found {
		s.cancelAuthorityJobs(digest)
	}
}

// RevokeSession invalidates all capabilities for a native session and returns
// the number of credentials revoked. Durable Canvas documents remain available
// for authenticated management removal/reconnect.
func (s *Service) RevokeSession(sessionID string) int {
	if s == nil || validateSessionID(sessionID) != nil {
		return 0
	}
	digest := sessionDigest(sessionID)
	s.mu.Lock()
	count := 0
	for key, entry := range s.tokens {
		if constantEqual(entry.digest, digest) {
			delete(s.tokens, key)
			count++
		}
	}
	s.mu.Unlock()
	s.cancelSessionJobs(digest)
	return count
}

// DeleteSession durably tombstones the live Canvas owned by a native session
// after native session deletion has been confirmed. It does not require a
// bearer capability because the controller calls it at the verified lifecycle
// boundary; all session capabilities are revoked before touching the document.
func (s *Service) DeleteSession(sessionID string) error {
	if validateSessionID(sessionID) != nil {
		return category("invalid_request", ErrInvalidRequest)
	}
	if s == nil {
		return nil
	}
	s.RevokeSession(sessionID)
	digest := sessionDigest(sessionID)
	s.mu.RLock()
	scope := s.sessions[digest]
	s.mu.RUnlock()
	if scope == nil {
		return nil
	}
	scope.mu.Lock()
	canvas := scope.live
	if canvas == nil {
		scope.mu.Unlock()
		return nil
	}
	canvas.mu.Lock()
	if canvas.meta.RemovedAt != nil && !canvas.uncertain && !canvas.meta.CleanupPending {
		canvas.mu.Unlock()
		scope.mu.Unlock()
		return nil
	}
	removedAt := s.currentTime()
	if canvas.meta.RemovedAt != nil {
		removedAt = *canvas.meta.RemovedAt
	}
	next := canvas.meta
	next.RemovedAt = &removedAt
	next.UpdatedAt = removedAt
	next.CleanupPending = true
	canvas.meta = next
	s.cancelCanvasJobs(next.CanvasID, next.Generation)
	published, saveErr := saveMetaFree(next, canvas.dir, s.effectiveMaxMetaBytes())
	if saveErr != nil {
		canvas.uncertain = true
		if published {
			// The tombstone may already be visible. Keep the in-memory state
			// conservative and require reconciliation before any new mutation.
			canvas.meta = next
		}
		canvas.mu.Unlock()
		scope.mu.Unlock()
		return category("persistence_uncertain", fmt.Errorf("tombstone Canvas during session deletion: %w", ErrPersistenceUncertain))
	}
	canvas.uncertain = false
	cleanupErr := removeCanvasPayload(canvas.dir)
	if cleanupErr == nil {
		next.CleanupPending = false
	} else {
		next.CleanupPending = true
	}
	canvas.meta = next
	if cleanupErr == nil {
		if _, persistErr := saveMetaFree(next, canvas.dir, s.effectiveMaxMetaBytes()); persistErr != nil {
			cleanupErr = persistErr
			next.CleanupPending = true
			canvas.meta = next
		}
	}
	if cleanupErr == nil {
		scope.live = nil
	} else {
		// Keep the tombstone marker attached so a later confirmed deletion can
		// retry physical cleanup without restoring logical access.
		scope.live = canvas
	}
	canvas.mu.Unlock()
	scope.mu.Unlock()
	_ = s.refreshUsage()
	if cleanupErr != nil {
		s.logger.Warn("Canvas session deletion cleanup pending", "session", sessionID, "canvas", next.CanvasID, "error", cleanupErr)
		return cleanupErr
	}
	return nil
}

// RevokeAllAuthorities invalidates every Canvas capability and queued/active
// worker job. It is used by module disable, replacement and shutdown paths.
func (s *Service) RevokeAllAuthorities() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	count := len(s.tokens)
	s.tokens = make(map[string]tokenState)
	s.mu.Unlock()
	s.cancelAllJobs()
	return count
}

func (s *Service) revokeDocumentAuthorities(sessionDigest string, generation uint64) int {
	s.mu.Lock()
	count := 0
	for key, entry := range s.tokens {
		if constantEqual(entry.digest, sessionDigest) && entry.documentGeneration == generation {
			delete(s.tokens, key)
			count++
		}
	}
	s.mu.Unlock()
	return count
}

func (s *Service) sessionForAuthority(authority Authority) (*sessionState, error) {
	if err := s.authorizeAuthority(authority); err != nil {
		return nil, err
	}
	s.mu.RLock()
	for _, session := range s.sessions {
		if constantEqual(session.key, authority.SessionKey) {
			s.mu.RUnlock()
			return session, nil
		}
	}
	s.mu.RUnlock()
	return nil, category("unauthorized", ErrUnauthorized)
}

func (s *Service) requireLiveForMutation() error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if !s.isEnabled() {
		return category("disabled", ErrDisabled)
	}
	return nil
}

func (s *Service) ensureOpen() error {
	s.mu.RLock()
	closed := s.closed
	s.mu.RUnlock()
	if closed {
		return category("shutting_down", ErrShuttingDown)
	}
	return nil
}

func (s *Service) isEnabled() bool {
	s.mu.RLock()
	enabled := s.config.Enabled
	s.mu.RUnlock()
	return enabled
}

func (s *Service) canvasFor(session *sessionState, id string, expectedGenerations ...uint64) (*canvasState, error) {
	if err := validateCanvasID(id); err != nil {
		return nil, err
	}
	s.mu.RLock()
	candidate := s.canvases[id]
	s.mu.RUnlock()
	if candidate == nil {
		return nil, category("not_found", ErrNotFound)
	}
	candidate.mu.RLock()
	belongs := constantEqual(candidate.meta.SessionDigest, session.digest)
	removed := candidate.meta.RemovedAt != nil
	corrupt := candidate.meta.Corrupt
	uncertain := candidate.uncertain
	generation := candidate.meta.Generation
	candidate.mu.RUnlock()
	if !belongs {
		return nil, category("not_found", ErrNotFound)
	}
	if removed {
		return candidate, category("removed", ErrRemoved)
	}
	if len(expectedGenerations) > 1 {
		return nil, category("invalid_request", ErrInvalidRequest)
	}
	if len(expectedGenerations) == 1 && expectedGenerations[0] != 0 && expectedGenerations[0] != generation {
		return candidate, category("generation_revoked", ErrGenerationRevoked)
	}
	if corrupt {
		return candidate, category("corrupt", ErrCorrupt)
	}
	if uncertain {
		return candidate, category("persistence_uncertain", ErrPersistenceUncertain)
	}
	return candidate, nil
}

func (s *Service) validateHTML(html string) ([]byte, error) {
	if !utf8.ValidString(html) {
		return nil, fmt.Errorf("HTML must be valid UTF-8: %w", ErrInvalidRequest)
	}
	content := []byte(html)
	if int64(len(content)) > s.config.MaxHTMLBytes {
		return nil, category("limit_exceeded", fmt.Errorf("HTML exceeds %d bytes: %w", s.config.MaxHTMLBytes, ErrLimit))
	}
	return content, nil
}

func (s *Service) reserve(bytes int64) error {
	if bytes < 0 {
		return category("quota_exceeded", ErrQuotaExceeded)
	}
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	if bytes > s.config.MaxStorageBytes || s.usage+s.reserved > s.config.MaxStorageBytes-bytes {
		return category("quota_exceeded", ErrQuotaExceeded)
	}
	s.reserved += bytes
	return nil
}

func (s *Service) release(bytes int64) {
	s.quotaMu.Lock()
	s.reserved -= bytes
	if s.reserved < 0 {
		s.reserved = 0
	}
	s.quotaMu.Unlock()
}

func (s *Service) refreshUsage() error {
	usage, err := measureUsage(s.root)
	if err != nil {
		return err
	}
	s.quotaMu.Lock()
	s.usage = usage
	s.quotaMu.Unlock()
	return nil
}

func measureUsage(root string) (int64, error) {
	var usage int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		usage += info.Size()
		return nil
	})
	return usage, err
}

// Shutdown cancels active workers, rejects new mutations and waits for the
// optional context (if supplied) only as a caller convenience. Durable state
// remains on disk for a subsequent New.
func (s *Service) Shutdown(contexts ...context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	// Credentials are ephemeral and must not remain usable through a retained
	// Service pointer after shutdown.
	s.tokens = make(map[string]tokenState)
	s.mu.Unlock()
	s.stop()
	s.cancelAllJobs()
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

// Enable and Disable alter only admission. Disable leaves durable documents
// and tombstones intact; Remove remains available for authenticated cleanup.
func (s *Service) Enable() {
	s.mu.Lock()
	if !s.closed {
		s.config.Enabled = true
	}
	s.mu.Unlock()
}
func (s *Service) Disable() {
	s.mu.Lock()
	if !s.closed {
		s.config.Enabled = false
		// Re-enabling requires a fresh Attach for agent operations. Retain the
		// binding as management-only so an authenticated owner can remove
		// retained documents while the optional module is disabled.
		for key, entry := range s.tokens {
			entry.revoked = true
			entry.managementOnly = true
			s.tokens[key] = entry
		}
	}
	s.mu.Unlock()
	s.cancelAllJobs()
}

func (s *Service) cancelAllJobs() {
	s.jobMu.Lock()
	for _, jobs := range s.jobs {
		for job := range jobs {
			job.cancel()
		}
	}
	s.jobMu.Unlock()
}

// Ready reports whether service state loaded without corruption. A disabled
// module is healthy-but-unavailable, not a storage failure.
func (s *Service) Ready() bool {
	s.mu.RLock()
	closed := s.closed
	corrupt := s.corrupt
	canvases := make([]*canvasState, 0, len(s.canvases))
	for _, canvas := range s.canvases {
		canvases = append(canvases, canvas)
	}
	s.mu.RUnlock()
	if closed || corrupt {
		return false
	}
	for _, canvas := range canvases {
		canvas.mu.RLock()
		bad := canvas.meta.Corrupt || canvas.uncertain
		canvas.mu.RUnlock()
		if bad {
			return false
		}
	}
	return !s.corrupt
}

// Usage returns reconciled durable bytes and outstanding reservations.
func (s *Service) Usage() (used, reserved int64) {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	return s.usage, s.reserved
}

// Reconcile rechecks durable revision pointers, cleans abandoned staging and
// refreshes quota accounting without resurrecting tombstoned documents.
// Corruption remains visible through Ready and list availability until an
// operator repairs/removes the affected retained data.
func (s *Service) Reconcile() error {
	if err := cleanStaging(s.root); err != nil {
		return err
	}
	s.mu.RLock()
	canvases := make([]*canvasState, 0, len(s.canvases))
	for _, canvas := range s.canvases {
		canvases = append(canvases, canvas)
	}
	s.mu.RUnlock()
	var first error
	for _, canvas := range canvases {
		canvas.mu.RLock()
		removed := canvas.meta.RemovedAt != nil
		canvas.mu.RUnlock()
		if removed {
			continue
		}
		if err := s.checkLoadedCanvas(canvas); err != nil {
			canvas.mu.Lock()
			canvas.meta.Corrupt = true
			canvas.meta.CorruptReason = err.Error()
			canvas.uncertain = true
			canvas.mu.Unlock()
			if first == nil {
				first = err
			}
		}
	}
	if err := s.refreshUsage(); err != nil && first == nil {
		first = err
	}
	if first != nil {
		return category("corrupt", fmt.Errorf("reconcile Canvas state: %w", first))
	}
	return nil
}

func (s *Service) availability(canvas *canvasState) CanvasSummary {
	canvas.mu.RLock()
	defer canvas.mu.RUnlock()
	availability := "ready"
	available := true
	if canvas.meta.RemovedAt != nil {
		availability, available = "removed", false
	} else if canvas.meta.Corrupt || canvas.uncertain {
		availability, available = "corrupt", false
	} else if !s.isEnabled() {
		availability, available = "disabled", false
	} else if s.config.WorkerLauncher == nil || isNilLauncher(s.config.WorkerLauncher) {
		// Stored documents remain retained; preview availability is explicit.
		availability, available = "renderer-unavailable", false
	}
	return CanvasSummary{ID: canvas.meta.CanvasID, Generation: canvas.meta.Generation, Version: canvas.meta.CurrentVersion, UpdatedAt: canvas.meta.UpdatedAt, Available: available, Availability: availability, Removed: canvas.meta.RemovedAt != nil, CleanupPending: canvas.meta.CleanupPending}
}

func (s *Service) toCanvas(canvas *canvasState) Canvas {
	canvas.mu.RLock()
	defer canvas.mu.RUnlock()
	availability := "ready"
	available := true
	if canvas.meta.RemovedAt != nil {
		availability, available = "removed", false
	} else if canvas.meta.Corrupt || canvas.uncertain {
		availability, available = "corrupt", false
	} else if !s.isEnabled() {
		availability, available = "disabled", false
	} else if s.config.WorkerLauncher == nil || isNilLauncher(s.config.WorkerLauncher) {
		availability, available = "renderer-unavailable", false
	}
	return Canvas{ID: canvas.meta.CanvasID, Generation: canvas.meta.Generation, Version: canvas.meta.CurrentVersion, CreatedAt: canvas.meta.CreatedAt, UpdatedAt: canvas.meta.UpdatedAt, Removed: canvas.meta.RemovedAt != nil, Available: available, Availability: availability}
}

func copyBytes(content []byte) []byte { return append([]byte(nil), content...) }

func hashBytes(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func sortedCanvasIDs(values map[string]*canvasState) []string {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func isRegularNoSymlink(path string) (fs.FileInfo, error) {
	file, info, err := openRegularNoFollow(path, -1)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return info, nil
}

func readBoundedFile(path string, max int64) ([]byte, error) {
	if max < 0 {
		return nil, category("limit_exceeded", ErrLimit)
	}
	file, _, err := openRegularNoFollow(path, max)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > max {
		return nil, category("limit_exceeded", ErrLimit)
	}
	return content, nil
}

// openRegularNoFollow opens one generated artifact while checking the complete
// parent chain and the opened file descriptor. Lstat followed by os.Open alone
// leaves a symlink/replacement window for artifact and revision reads.
func openRegularNoFollow(path string, max int64) (*os.File, fs.FileInfo, error) {
	if err := rejectSymlinkParents(path); err != nil {
		return nil, nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("path is not a regular file: %w", ErrCorrupt)
	}
	if max >= 0 && info.Size() > max {
		return nil, nil, category("limit_exceeded", ErrLimit)
	}
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	opened, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		return nil, nil, statErr
	}
	if opened.Mode()&os.ModeSymlink != 0 || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		_ = file.Close()
		return nil, nil, fmt.Errorf("opened artifact changed during admission: %w", ErrCorrupt)
	}
	if max >= 0 && opened.Size() > max {
		_ = file.Close()
		return nil, nil, category("limit_exceeded", ErrLimit)
	}
	return file, opened, nil
}

func rejectSymlinkParents(path string) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("artifact path is a symlink: %w", ErrCorrupt)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	current := filepath.Clean(filepath.Dir(path))
	for {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("artifact parent is not a real directory: %w", ErrCorrupt)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}
