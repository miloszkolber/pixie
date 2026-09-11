package canvas

import (
	"context"
	"strings"
	"unicode/utf8"
)

// Read returns bounded text and optional selected DOM for an immutable
// revision. It never evaluates HTML or invokes a worker.
func (s *Service) Read(ctx context.Context, authority Authority, request ReadRequest) (ReadResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.authorizeAuthority(authority); err != nil {
		return ReadResult{}, err
	}
	if err := s.ensureOpen(); err != nil {
		return ReadResult{}, err
	}
	if !s.isEnabled() {
		return ReadResult{}, category("disabled", ErrDisabled)
	}
	if err := ctx.Err(); err != nil {
		return ReadResult{}, err
	}
	session, err := s.sessionForAuthority(authority)
	if err != nil {
		return ReadResult{}, err
	}
	documentGeneration, err := s.authorityDocumentGeneration(authority)
	if err != nil {
		return ReadResult{}, err
	}
	canvas, err := s.canvasFor(session, request.CanvasID, documentGeneration)
	if err != nil {
		return ReadResult{}, err
	}
	content, meta, err := s.readRevision(canvas, request.Version)
	if err != nil {
		return ReadResult{}, err
	}
	canvas.mu.RLock()
	removed := canvas.meta.RemovedAt != nil
	uncertain := canvas.uncertain
	corrupt := canvas.meta.Corrupt
	canvas.mu.RUnlock()
	if removed {
		return ReadResult{}, category("removed", ErrRemoved)
	}
	if uncertain {
		return ReadResult{}, category("persistence_uncertain", ErrPersistenceUncertain)
	}
	if corrupt {
		return ReadResult{}, category("corrupt", ErrCorrupt)
	}
	if !s.isEnabled() {
		return ReadResult{}, category("disabled", ErrDisabled)
	}
	selector := strings.TrimSpace(request.Selector)
	if len(selector) > s.config.MaxSelectorBytes {
		return ReadResult{}, category("limit_exceeded", ErrLimit)
	}
	if selector == "" {
		selector = "body"
	}
	maxBytes := request.MaxBytes
	if maxBytes == 0 {
		maxBytes = s.config.MaxReadBytes
	}
	if maxBytes < 1 || maxBytes > s.config.MaxReadBytes {
		return ReadResult{}, category("limit_exceeded", ErrLimit)
	}
	maxMatches := request.MaxMatches
	if maxMatches == 0 {
		maxMatches = s.config.MaxMatches
	}
	if maxMatches < 1 || maxMatches > s.config.MaxMatches {
		return ReadResult{}, category("limit_exceeded", ErrLimit)
	}
	matches, err := selectHTML(content, selector, maxMatches, maxBytes, request.IncludeDOM)
	if err != nil {
		return ReadResult{}, err
	}
	var text strings.Builder
	for index, match := range matches.Matches {
		if index > 0 {
			text.WriteByte('\n')
		}
		text.WriteString(match.Text)
		if text.Len() >= maxBytes {
			break
		}
	}
	boundedText, truncated := boundUTF8(text.String(), maxBytes)
	if matches.Truncated {
		truncated = true
	}
	// Removal, disablement or session revocation may race the bounded selector
	// walk. Recheck the capability and document identity before returning any
	// content from a no-longer-authorized revision.
	if err := s.authorizeAuthority(authority); err != nil {
		return ReadResult{}, err
	}
	if !s.isEnabled() {
		return ReadResult{}, category("disabled", ErrDisabled)
	}
	canvas.mu.RLock()
	removedNow, uncertainNow, corruptNow := canvas.meta.RemovedAt != nil, canvas.uncertain, canvas.meta.Corrupt
	canvas.mu.RUnlock()
	if removedNow {
		return ReadResult{}, category("removed", ErrRemoved)
	}
	if uncertainNow {
		return ReadResult{}, category("persistence_uncertain", ErrPersistenceUncertain)
	}
	if corruptNow {
		return ReadResult{}, category("corrupt", ErrCorrupt)
	}
	return ReadResult{Outcome: "read", CanvasID: request.CanvasID, Generation: meta.Generation, Version: requestVersion(request.Version, meta.CurrentVersion), Matches: matches.Matches, Text: boundedText, DOM: matches.DOM, MatchCount: matches.Total, Truncated: truncated, Limit: maxBytes}, nil
}

func requestVersion(request uint64, current uint64) uint64 {
	if request == 0 {
		return current
	}
	return request
}

type selectedHTML struct {
	Matches   []ReadMatch
	DOM       string
	Total     int
	Truncated bool
}

func boundUTF8(value string, max int) (string, bool) {
	if len(value) <= max {
		return value, false
	}
	content := []byte(value[:max])
	for len(content) > 0 && !utf8.Valid(content) {
		content = content[:len(content)-1]
	}
	return string(content), true
}

// List returns at most the calling session's live Canvas. Tombstones are not
// presented as a recreatable live document, but remain inspectable via Remove
// retries using their old ID.
func (s *Service) List(ctx context.Context, authority Authority) (ListResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.authorizeAuthority(authority); err != nil {
		return ListResult{}, err
	}
	if err := s.ensureOpen(); err != nil {
		return ListResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ListResult{}, err
	}
	session, err := s.sessionForAuthority(authority)
	if err != nil {
		return ListResult{}, err
	}
	session.mu.Lock()
	live := session.live
	session.mu.Unlock()
	if live == nil {
		return ListResult{Outcome: "listed", Items: []CanvasSummary{}}, nil
	}
	return ListResult{Outcome: "listed", Items: []CanvasSummary{s.availability(live)}}, nil
}

// ManagementStatus returns the calling controller session's retained Canvas
// metadata without requiring the optional worker or enabled module state. A
// tombstone is included so an owner can see cleanup-pending state and retry a
// removal after disablement or a worker outage; no other session is ever
// enumerated.
func (s *Service) ManagementStatus(ctx context.Context, authority Authority) (ListResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.authorizeManagementAuthority(authority); err != nil {
		return ListResult{}, err
	}
	if err := s.ensureOpen(); err != nil {
		return ListResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ListResult{}, err
	}
	session, err := s.managementSessionForAuthority(authority)
	if err != nil {
		return ListResult{}, err
	}
	session.mu.Lock()
	live := session.live
	session.mu.Unlock()
	if live == nil {
		return ListResult{Outcome: "status", Items: []CanvasSummary{}}, nil
	}
	return ListResult{Outcome: "status", Items: []CanvasSummary{s.availability(live)}}, nil
}

// Status is the concise compatibility name used by controller integrations.
// It is intentionally management-scoped; model-facing callers must continue
// to use List through the Canvas MCP capability.
func (s *Service) Status(ctx context.Context, authority Authority) (ListResult, error) {
	return s.ManagementStatus(ctx, authority)
}

// ReadText is a small integration convenience for controller call sites that
// only need visible text and do not want to construct a ReadRequest.
func (s *Service) ReadText(ctx context.Context, authority Authority, canvasID string, version uint64, selector string) (ReadResult, error) {
	return s.Read(ctx, authority, ReadRequest{CanvasID: canvasID, Version: version, Selector: selector})
}
