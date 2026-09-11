package canvas

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"time"
)

// DeterministicWorkerLauncher produces a valid bounded PNG from immutable
// metadata without executing the draft. It is useful for offline fixtures and
// explicitly does not claim sandboxing or browser fidelity. Deployments that
// need HTML execution must supply a tested contained launcher.
type DeterministicWorkerLauncher struct{}

// NewDeterministicWorkerLauncher returns the safe metadata-only renderer used
// by focused tests and offline previews.
func NewDeterministicWorkerLauncher() WorkerLauncher { return DeterministicWorkerLauncher{} }

func (DeterministicWorkerLauncher) Render(ctx context.Context, job RenderJob) (RenderResult, error) {
	if err := ctx.Err(); err != nil {
		return RenderResult{}, err
	}
	if job.Width <= 0 || job.Height <= 0 || int64(job.Width)*int64(job.Height) > MaxViewportPixels {
		return RenderResult{}, category("limit_exceeded", ErrLimit)
	}
	// Hashing content is deterministic and avoids using timestamps or renderer
	// state in the image. A small solid/checker pattern keeps PNG encoding quick.
	content := append(copyBytes(job.HTML), 0)
	digest := hashBytes(content)
	seedBytes, _ := hex.DecodeString(digest[:16])
	seed := uint64(0)
	for _, value := range seedBytes {
		seed = seed*257 + uint64(value)
	}
	base := color.RGBA{R: uint8(40 + seed%80), G: uint8(55 + (seed/11)%90), B: uint8(80 + (seed/37)%100), A: 255}
	accent := color.RGBA{R: 220, G: 232, B: 242, A: 255}
	picture := image.NewRGBA(image.Rect(0, 0, job.Width, job.Height))
	for y := 0; y < job.Height; y++ {
		if y%32 == 0 {
			if err := ctx.Err(); err != nil {
				return RenderResult{}, err
			}
		}
		for x := 0; x < job.Width; x++ {
			value := base
			if ((x/32)+(y/32))%2 == 0 {
				value = accent
				value.R = uint8((int(value.R) + int(base.R)) / 2)
				value.G = uint8((int(value.G) + int(base.G)) / 2)
				value.B = uint8((int(value.B) + int(base.B)) / 2)
			}
			picture.SetRGBA(x, y, value)
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		return RenderResult{}, err
	}
	return RenderResult{PNG: encoded.Bytes(), MIME: "image/png", Width: job.Width, Height: job.Height}, nil
}

// unavailableLauncher is used only to keep diagnostics explicit when a
// caller intentionally configures a nil/unsupported launcher.
type unavailableLauncher struct{}

func (unavailableLauncher) Render(context.Context, RenderJob) (RenderResult, error) {
	return RenderResult{}, category("unavailable", ErrUnavailable)
}

func launcherRender(ctx context.Context, launcher WorkerLauncher, job RenderJob) (RenderResult, error) {
	if launcher == nil || isNilLauncher(launcher) {
		return RenderResult{}, category("unavailable", ErrUnavailable)
	}
	switch typed := launcher.(type) {
	case RenderLauncher:
		return typed.Render(ctx, job)
	case LaunchWorker:
		return typed.Launch(ctx, job)
	case func(context.Context, RenderJob) (RenderResult, error):
		return typed(ctx, job)
	default:
		return RenderResult{}, category("unavailable", fmt.Errorf("worker launcher does not implement Render or Launch: %w", ErrUnavailable))
	}
}

func launcherRenderBounded(ctx context.Context, launcher WorkerLauncher, job RenderJob) (result RenderResult, err error) {
	type response struct {
		result RenderResult
		err    error
	}
	completed := make(chan response, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				select {
				case completed <- response{err: fmt.Errorf("worker launcher panicked: %w", ErrUnavailable)}:
				default:
				}
			}
		}()
		workerResult, workerErr := launcherRender(ctx, launcher, job)
		completed <- response{result: workerResult, err: workerErr}
	}()
	select {
	case <-ctx.Done():
		return RenderResult{}, ctx.Err()
	case output := <-completed:
		return output.result, output.err
	}
}

func isNilLauncher(launcher WorkerLauncher) bool {
	value := reflect.ValueOf(launcher)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func validatePNG(result RenderResult, request ScreenshotRequest, config Config) error {
	if result.MIME != "image/png" {
		return category("invalid_worker_response", fmt.Errorf("worker MIME must be image/png: %w", ErrInvalidWorkerResponse))
	}
	if result.Width != request.Width || result.Height != request.Height {
		return category("invalid_worker_response", fmt.Errorf("worker dimensions do not match request: %w", ErrInvalidWorkerResponse))
	}
	if len(result.PNG) == 0 || int64(len(result.PNG)) > config.MaxImageBytes {
		return category("limit_exceeded", ErrLimit)
	}
	decoded, err := png.DecodeConfig(bytes.NewReader(result.PNG))
	if err != nil {
		return category("invalid_worker_response", fmt.Errorf("worker returned invalid PNG: %w", ErrInvalidWorkerResponse))
	}
	if decoded.ColorModel == nil || decoded.Width != request.Width || decoded.Height != request.Height || int64(decoded.Width)*int64(decoded.Height) > config.MaxPixels {
		return category("invalid_worker_response", fmt.Errorf("worker PNG dimensions are invalid: %w", ErrInvalidWorkerResponse))
	}
	return nil
}

func (s *Service) acquireJob(ctx context.Context, authorityDigest, sessionDigest string, nativeGeneration uint64, canvasID string, generation uint64) (context.Context, func(), error) {
	s.jobMu.Lock()
	if s.activeJobs+s.queuedJobs >= s.config.MaxActiveJobs+s.config.MaxQueuedJobs {
		s.jobMu.Unlock()
		return nil, nil, category("busy", ErrBusy)
	}
	s.queuedJobs++
	jobCtx, cancel := context.WithTimeout(ctx, s.config.WorkerTimeout)
	job := &runningJob{cancel: cancel, authorityDigest: authorityDigest, digest: sessionDigest, nativeGeneration: nativeGeneration, canvas: canvasID, generation: generation}
	if s.jobs[canvasID] == nil {
		s.jobs[canvasID] = make(map[*runningJob]struct{})
	}
	s.jobs[canvasID][job] = struct{}{}
	s.jobMu.Unlock()
	// One active job is admitted at a time. The queue counters are bounded
	// before waiting, so a slow launcher cannot consume unbounded memory.
	for {
		s.jobMu.Lock()
		if s.activeJobs < s.config.MaxActiveJobs {
			s.activeJobs++
			s.queuedJobs--
			s.jobMu.Unlock()
			finish := func() {
				cancel()
				s.jobMu.Lock()
				s.activeJobs--
				delete(s.jobs[canvasID], job)
				if len(s.jobs[canvasID]) == 0 {
					delete(s.jobs, canvasID)
				}
				s.jobMu.Unlock()
			}
			return jobCtx, finish, nil
		}
		s.jobMu.Unlock()
		select {
		case <-jobCtx.Done():
			s.jobMu.Lock()
			s.queuedJobs--
			delete(s.jobs[canvasID], job)
			if len(s.jobs[canvasID]) == 0 {
				delete(s.jobs, canvasID)
			}
			s.jobMu.Unlock()
			return nil, nil, jobCtx.Err()
		case <-time.After(2 * time.Millisecond):
		}
	}
}

func (s *Service) cancelCanvasJobs(canvasID string, generation uint64) {
	s.jobMu.Lock()
	for job := range s.jobs[canvasID] {
		if job.generation == generation {
			job.cancel()
		}
	}
	s.jobMu.Unlock()
}

func (s *Service) cancelSessionJobs(sessionDigest string) {
	s.jobMu.Lock()
	for _, jobs := range s.jobs {
		for job := range jobs {
			if constantEqual(job.digest, sessionDigest) {
				job.cancel()
			}
		}
	}
	s.jobMu.Unlock()
}

func (s *Service) cancelAuthorityJobs(authorityDigest string) {
	if authorityDigest == "" {
		return
	}
	s.jobMu.Lock()
	for _, jobs := range s.jobs {
		for job := range jobs {
			if constantEqual(job.authorityDigest, authorityDigest) {
				job.cancel()
			}
		}
	}
	s.jobMu.Unlock()
}

func (s *Service) cancelSessionGeneration(sessionDigest string, nativeGeneration uint64) {
	if nativeGeneration == 0 {
		return
	}
	s.jobMu.Lock()
	for _, jobs := range s.jobs {
		for job := range jobs {
			if constantEqual(job.digest, sessionDigest) && job.nativeGeneration == nativeGeneration {
				job.cancel()
			}
		}
	}
	s.jobMu.Unlock()
}

func (s *Service) renderScreenshot(ctx context.Context, session *sessionState, authority Authority, request ScreenshotRequest) (ScreenshotResult, error) {
	if !s.isEnabled() {
		return ScreenshotResult{}, category("disabled", ErrDisabled)
	}
	if err := validateCanvasID(request.CanvasID); err != nil {
		return ScreenshotResult{}, err
	}
	documentGeneration, err := s.authorityDocumentGeneration(authority)
	if err != nil {
		return ScreenshotResult{}, err
	}
	width, height := request.Width, request.Height
	if width == 0 {
		width = DefaultViewportWidth
	}
	if height == 0 {
		height = DefaultViewportHeight
	}
	dpr := request.DPR
	if dpr == 0 {
		dpr = 1
	}
	if dpr != 1 || width <= 0 || height <= 0 || width > s.config.MaxWidth || height > s.config.MaxHeight || int64(width)*int64(height) > s.config.MaxPixels {
		return ScreenshotResult{}, category("limit_exceeded", ErrLimit)
	}
	if s.config.WorkerLauncher == nil || isNilLauncher(s.config.WorkerLauncher) {
		return ScreenshotResult{}, category("unavailable", ErrUnavailable)
	}
	request.Width, request.Height, request.DPR = width, height, dpr
	canvas, err := s.canvasFor(session, request.CanvasID, documentGeneration)
	if err != nil {
		return ScreenshotResult{}, err
	}
	content, meta, err := s.readRevision(canvas, request.Version)
	if err != nil {
		return ScreenshotResult{}, err
	}
	version := requestVersion(request.Version, meta.CurrentVersion)
	canvas.mu.RLock()
	generation := canvas.meta.Generation
	removed := canvas.meta.RemovedAt != nil
	uncertain := canvas.uncertain
	corrupt := canvas.meta.Corrupt
	canvas.mu.RUnlock()
	if removed {
		return ScreenshotResult{}, category("removed", ErrRemoved)
	}
	if uncertain {
		return ScreenshotResult{}, category("persistence_uncertain", ErrPersistenceUncertain)
	}
	if corrupt {
		return ScreenshotResult{}, category("corrupt", ErrCorrupt)
	}
	renderKey := screenshotKey(request.CanvasID, generation, version, width, height, dpr, s.config.RendererVersion, s.config.AssetSet)
	artifactPath := filepath.Join(canvas.dir, shotsDirName, renderKey+".png")
	if cached, readErr := readBoundedFile(artifactPath, s.config.MaxImageBytes); readErr == nil {
		result := RenderResult{PNG: cached, MIME: "image/png", Width: width, Height: height}
		if validatePNG(result, request, s.config) == nil {
			// A cache hit still crosses the same revocation boundary as a fresh
			// render. Removal/disable/revocation may race the file read, so do not
			// return a previously committed artifact after access has ended.
			if err := ctx.Err(); err != nil {
				return ScreenshotResult{}, err
			}
			if err := s.authorizeAuthority(authority); err != nil {
				return ScreenshotResult{}, err
			}
			if !s.isEnabled() {
				return ScreenshotResult{}, category("disabled", ErrDisabled)
			}
			canvas.mu.RLock()
			stillCurrent := canvas.meta.Generation == generation && canvas.meta.RemovedAt == nil && !canvas.meta.Corrupt && !canvas.uncertain
			canvas.mu.RUnlock()
			if !stillCurrent {
				return ScreenshotResult{}, category("generation_revoked", ErrGenerationRevoked)
			}
			return ScreenshotResult{Outcome: "screenshot", CanvasID: request.CanvasID, Generation: generation, Version: version, MIME: "image/png", Width: width, Height: height, Bytes: int64(len(cached)), PNG: copyBytes(cached), Artifact: artifactReference(request.CanvasID, renderKey), Cached: true}, nil
		}
		_ = osRemoveNoFollow(artifactPath)
	}
	jobCtx, finish, err := s.acquireJob(ctx, tokenDigest(authority.Token), session.digest, authority.Generation, request.CanvasID, generation)
	if err != nil {
		return ScreenshotResult{}, err
	}
	defer finish()
	job := RenderJob{CanvasID: request.CanvasID, Generation: generation, Version: version, HTML: copyBytes(content), Width: width, Height: height, DPR: dpr, OfflineOnly: true, ContentPolicy: "default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; script-src 'none'", RendererVersion: s.config.RendererVersion, AssetSet: s.config.AssetSet}
	result, err := launcherRenderBounded(jobCtx, s.config.WorkerLauncher, job)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			canvas.mu.RLock()
			removed := canvas.meta.RemovedAt != nil
			canvas.mu.RUnlock()
			if removed {
				return ScreenshotResult{}, category("generation_revoked", ErrGenerationRevoked)
			}
			if !s.isEnabled() {
				return ScreenshotResult{}, category("disabled", ErrDisabled)
			}
			return ScreenshotResult{}, err
		}
		return ScreenshotResult{}, err
	}
	if err := validatePNG(result, request, s.config); err != nil {
		return ScreenshotResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ScreenshotResult{}, err
	}
	// Removal/disable can race an uncooperative worker. Identity is checked
	// after completion before any artifact publication.
	if err := s.authorizeAuthority(authority); err != nil {
		return ScreenshotResult{}, err
	}
	if err := s.ensureOpen(); err != nil {
		return ScreenshotResult{}, err
	}
	canvas.mu.RLock()
	stillCurrent := canvas.meta.Generation == generation && canvas.meta.RemovedAt == nil && !canvas.meta.Corrupt && !canvas.uncertain
	canvas.mu.RUnlock()
	if !stillCurrent || !s.isEnabled() {
		return ScreenshotResult{}, category("generation_revoked", ErrGenerationRevoked)
	}
	reservation := s.config.MaxImageBytes
	if err := s.reserve(reservation); err != nil {
		return ScreenshotResult{}, err
	}
	defer s.release(reservation)
	canvas.mu.RLock()
	if canvas.meta.RemovedAt != nil || canvas.uncertain || canvas.meta.Corrupt {
		canvas.mu.RUnlock()
		return ScreenshotResult{}, category("generation_revoked", ErrGenerationRevoked)
	}
	stageDir := filepath.Join(canvas.dir, stageDirName, "render-"+renderKey)
	defer os.RemoveAll(stageDir)
	stagePath, err := stageBytes(jobCtx, stageDir, renderKey+".png.tmp", result.PNG)
	if err != nil {
		canvas.mu.RUnlock()
		return ScreenshotResult{}, fmt.Errorf("stage Canvas screenshot: %w", err)
	}
	if err := ctx.Err(); err != nil {
		canvas.mu.RUnlock()
		return ScreenshotResult{}, err
	}
	if err := s.authorizeAuthority(authority); err != nil {
		canvas.mu.RUnlock()
		return ScreenshotResult{}, err
	}
	if !s.isEnabled() {
		canvas.mu.RUnlock()
		return ScreenshotResult{}, category("disabled", ErrDisabled)
	}
	if canvas.meta.Generation != generation || canvas.meta.RemovedAt != nil || canvas.meta.Corrupt || canvas.uncertain {
		canvas.mu.RUnlock()
		return ScreenshotResult{}, category("generation_revoked", ErrGenerationRevoked)
	}
	if err := publishImmutable(stagePath, artifactPath); err != nil {
		if errors.Is(err, ErrConflict) {
			if cached, readErr := readBoundedFile(artifactPath, s.config.MaxImageBytes); readErr == nil {
				canvas.mu.RUnlock()
				// Another renderer may have published the same immutable key while
				// this job was running. Treat that as a cache hit only after the
				// same cancellation, capability and generation checks as the
				// ordinary cache path above.
				if err := ctx.Err(); err != nil {
					return ScreenshotResult{}, err
				}
				if err := s.authorizeAuthority(authority); err != nil {
					return ScreenshotResult{}, err
				}
				if !s.isEnabled() {
					return ScreenshotResult{}, category("disabled", ErrDisabled)
				}
				canvas.mu.RLock()
				stillCurrent := canvas.meta.Generation == generation && canvas.meta.RemovedAt == nil && !canvas.meta.Corrupt && !canvas.uncertain
				canvas.mu.RUnlock()
				if !stillCurrent {
					return ScreenshotResult{}, category("generation_revoked", ErrGenerationRevoked)
				}
				return ScreenshotResult{Outcome: "screenshot", CanvasID: request.CanvasID, Generation: generation, Version: version, MIME: "image/png", Width: width, Height: height, Bytes: int64(len(cached)), PNG: copyBytes(cached), Artifact: artifactReference(request.CanvasID, renderKey), Cached: true}, nil
			}
		}
		canvas.mu.RUnlock()
		return ScreenshotResult{}, fmt.Errorf("publish Canvas screenshot: %w", err)
	}
	canvas.mu.RUnlock()
	_ = s.refreshUsage()
	return ScreenshotResult{Outcome: "screenshot", CanvasID: request.CanvasID, Generation: generation, Version: version, MIME: "image/png", Width: width, Height: height, Bytes: int64(len(result.PNG)), PNG: copyBytes(result.PNG), Artifact: artifactReference(request.CanvasID, renderKey)}, nil
}

func screenshotKey(canvasID string, generation uint64, version uint64, width, height, dpr int, rendererVersion, assetSet string) string {
	hasher := fnv.New128a()
	_, _ = hasher.Write([]byte(canvasID + ":" + strconv.FormatUint(generation, 10) + ":" + strconv.FormatUint(version, 10) + ":" + strconv.Itoa(width) + ":" + strconv.Itoa(height) + ":" + strconv.Itoa(dpr) + ":" + rendererVersion + ":" + assetSet))
	return fmt.Sprintf("%x", hasher.Sum(nil))
}

func artifactReference(canvasID, key string) string {
	return "pixie://canvas/artifact/" + canvasID + "/" + key + ".png"
}

func osRemoveNoFollow(path string) error {
	if err := rejectSymlinkParents(filepath.Dir(path)); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to remove symlink")
	}
	return os.Remove(path)
}

// Screenshot runs one bounded worker job and captures the requested immutable
// revision. No default launcher means explicit unavailable rather than an
// uncontained fallback.
func (s *Service) Screenshot(ctx context.Context, authority Authority, request ScreenshotRequest) (ScreenshotResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.authorizeAuthority(authority); err != nil {
		return ScreenshotResult{}, err
	}
	if err := s.ensureOpen(); err != nil {
		return ScreenshotResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ScreenshotResult{}, err
	}
	session, err := s.sessionForAuthority(authority)
	if err != nil {
		return ScreenshotResult{}, err
	}
	return s.renderScreenshot(ctx, session, authority, request)
}
