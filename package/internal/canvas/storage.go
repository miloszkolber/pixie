package canvas

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	metaFileName = "meta.json"
	shotsDirName = "shots"
	stageDirName = "staging"
)

var builtInTemplates = map[string]string{
	"":           "<!doctype html><html><head><meta charset=\"utf-8\"></head><body></body></html>",
	"blank":      "<!doctype html><html><head><meta charset=\"utf-8\"></head><body></body></html>",
	"basic":      "<!doctype html><html><head><meta charset=\"utf-8\"></head><body><main><h1>Canvas</h1></main></body></html>",
	"mewa-basic": "<!doctype html><html><head><meta charset=\"utf-8\"></head><body><main><h1>Canvas</h1><p>Draft offline.</p></main></body></html>",
	"mewa-card":  "<!doctype html><html><head><meta charset=\"utf-8\"></head><body><article><h1>Canvas card</h1><p>Draft offline.</p></article></body></html>",
}

func (s *Service) load() error {
	if err := cleanStaging(s.root); err != nil {
		return fmt.Errorf("clean canvas staging: %w", err)
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return fmt.Errorf("read canvas storage: %w", err)
	}
	for _, sessionEntry := range entries {
		if !sessionEntry.IsDir() || !validIdentity(sessionEntry.Name()) {
			continue
		}
		sessionPath := filepath.Join(s.root, sessionEntry.Name())
		if info, statErr := os.Lstat(sessionPath); statErr != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		sessionEntries, readErr := os.ReadDir(sessionPath)
		if readErr != nil {
			return fmt.Errorf("read canvas session storage: %w", readErr)
		}
		for _, canvasEntry := range sessionEntries {
			if !canvasEntry.IsDir() || !validIdentity(canvasEntry.Name()) {
				continue
			}
			canvasDir := filepath.Join(sessionPath, canvasEntry.Name())
			if info, statErr := os.Lstat(canvasDir); statErr != nil || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			meta, loadErr := readMeta(filepath.Join(canvasDir, metaFileName))
			if loadErr != nil {
				s.logger.Warn("canvas metadata unavailable", "directory", canvasEntry.Name(), "error", loadErr)
				s.mu.Lock()
				s.corrupt = true
				s.mu.Unlock()
				continue
			}
			if meta.CanvasID != canvasEntry.Name() || meta.SessionKey != sessionEntry.Name() || meta.SessionDigest == "" || len(meta.SessionDigest) != 64 {
				s.logger.Warn("canvas metadata identity mismatch", "directory", canvasEntry.Name())
				s.mu.Lock()
				s.corrupt = true
				s.mu.Unlock()
				continue
			}
			canvas := &canvasState{meta: meta, dir: canvasDir}
			if checkErr := s.checkLoadedCanvas(canvas); checkErr != nil {
				canvas.meta.Corrupt = true
				canvas.meta.CorruptReason = checkErr.Error()
				canvas.uncertain = true
			}
			s.mu.Lock()
			s.canvases[meta.CanvasID] = canvas
			session := s.sessions[meta.SessionDigest]
			if session == nil {
				session = &sessionState{key: sessionEntry.Name(), digest: meta.SessionDigest, nextGen: meta.Generation + 1}
				s.sessions[meta.SessionDigest] = session
			}
			if session.key != sessionEntry.Name() {
				// A duplicate/copy of a session directory must not silently
				// authorize a second path. Keep the first mapping and report the
				// copied canvas as corrupt.
				canvas.meta.Corrupt = true
				canvas.meta.CorruptReason = "session digest maps to multiple opaque roots"
				canvas.uncertain = true
			} else if meta.Generation >= session.nextGen {
				session.nextGen = meta.Generation + 1
			}
			if meta.RemovedAt == nil {
				if session.live != nil {
					// More than one live canvas for one native session is
					// corruption. Do not select one by caller-controlled order.
					session.live.meta.Corrupt = true
					session.live.meta.CorruptReason = "multiple live Canvas documents for one session"
					session.live.uncertain = true
					canvas.meta.Corrupt = true
					canvas.meta.CorruptReason = "multiple live Canvas documents for one session"
					canvas.uncertain = true
				} else {
					session.live = canvas
				}
			}
			s.mu.Unlock()
		}
	}
	if err := s.refreshUsage(); err != nil {
		return fmt.Errorf("reconcile canvas quota: %w", err)
	}
	return nil
}

func cleanStaging(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, sessionEntry := range entries {
		if !sessionEntry.IsDir() || !validIdentity(sessionEntry.Name()) {
			continue
		}
		sessionPath := filepath.Join(root, sessionEntry.Name())
		if info, statErr := os.Lstat(sessionPath); statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		canvasEntries, err := os.ReadDir(sessionPath)
		if err != nil {
			return err
		}
		for _, canvasEntry := range canvasEntries {
			if !canvasEntry.IsDir() || !validIdentity(canvasEntry.Name()) {
				continue
			}
			stage := filepath.Join(sessionPath, canvasEntry.Name(), stageDirName)
			canvasPath := filepath.Join(sessionPath, canvasEntry.Name())
			if info, statErr := os.Lstat(canvasPath); statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				continue
			}
			if err := os.RemoveAll(stage); err != nil {
				return fmt.Errorf("remove abandoned canvas staging: %w", err)
			}
		}
	}
	return nil
}

func readMeta(path string) (diskMeta, error) {
	content, err := readBoundedFile(path, maxMetaBytes)
	if err != nil {
		return diskMeta{}, err
	}
	var meta diskMeta
	if err := json.Unmarshal(content, &meta); err != nil {
		return diskMeta{}, fmt.Errorf("decode canvas metadata: %w", err)
	}
	if meta.Schema != 1 || !validIdentity(meta.CanvasID) || !validIdentity(meta.SessionKey) || meta.Generation == 0 || meta.CurrentVersion == 0 || meta.SessionDigest == "" {
		return diskMeta{}, fmt.Errorf("invalid canvas metadata: %w", ErrCorrupt)
	}
	if meta.Mutations == nil {
		meta.Mutations = make(map[string]mutationRecord)
	}
	return meta, nil
}

func (s *Service) checkLoadedCanvas(canvas *canvasState) error {
	canvas.mu.RLock()
	meta := canvas.meta
	canvas.mu.RUnlock()
	if meta.RemovedAt != nil {
		// A tombstone is authoritative. Never recover an old revision over it.
		return nil
	}
	if meta.CurrentVersion > uint64(s.config.MaxStorageBytes/64) {
		return fmt.Errorf("Canvas revision count is impossible for the quota: %w", ErrCorrupt)
	}
	path := filepath.Join(canvas.dir, revisionName(meta.CurrentVersion))
	content, err := readBoundedFile(path, s.config.MaxHTMLBytes)
	if err != nil {
		return fmt.Errorf("read current Canvas revision: %w", err)
	}
	if hashBytes(content) != meta.CurrentSHA256 || int64(len(content)) != meta.CurrentBytes {
		return fmt.Errorf("current Canvas revision hash/accounting mismatch: %w", ErrCorrupt)
	}
	// Any authored revision newer than the metadata pointer means a prior
	// publication outcome was uncertain. Do not guess which state was committed.
	entries, err := os.ReadDir(canvas.dir)
	if err != nil {
		return err
	}
	versions := make(map[uint64]struct{})
	for _, entry := range entries {
		if version, ok := parseRevisionName(entry.Name()); ok {
			if version > meta.CurrentVersion {
				return fmt.Errorf("unreconciled Canvas revision v%d: %w", version, ErrPersistenceUncertain)
			}
			versions[version] = struct{}{}
		}
	}
	for version := uint64(1); version <= meta.CurrentVersion; version++ {
		if _, ok := versions[version]; !ok {
			return fmt.Errorf("missing Canvas revision v%d: %w", version, ErrCorrupt)
		}
		if _, err := isRegularNoSymlink(filepath.Join(canvas.dir, revisionName(version))); err != nil {
			return fmt.Errorf("invalid Canvas revision v%d: %w", version, ErrCorrupt)
		}
	}
	return nil
}

func revisionName(version uint64) string { return "v" + strconv.FormatUint(version, 10) + ".html" }

func parseRevisionName(name string) (uint64, bool) {
	if !strings.HasPrefix(name, "v") || !strings.HasSuffix(name, ".html") {
		return 0, false
	}
	value := strings.TrimSuffix(strings.TrimPrefix(name, "v"), ".html")
	if value == "" {
		return 0, false
	}
	version, err := strconv.ParseUint(value, 10, 64)
	return version, err == nil && version > 0
}

func (s *Service) createDirectories(sessionKey, canvasID string) (string, error) {
	if !validIdentity(sessionKey) || !validIdentity(canvasID) {
		return "", category("invalid_request", ErrInvalidRequest)
	}
	if err := ensurePrivateDirectory(s.root); err != nil {
		return "", err
	}
	sessionDir := filepath.Join(s.root, sessionKey)
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return "", fmt.Errorf("create Canvas session storage: %w", err)
	}
	if err := ensurePrivateDirectory(sessionDir); err != nil {
		return "", err
	}
	canvasDir := filepath.Join(sessionDir, canvasID)
	if err := rejectSymlinkParents(canvasDir); err != nil {
		return "", err
	}
	if err := os.Mkdir(canvasDir, 0o700); err != nil {
		return "", fmt.Errorf("create Canvas storage: %w", err)
	}
	if err := os.Mkdir(filepath.Join(canvasDir, shotsDirName), 0o700); err != nil {
		_ = os.RemoveAll(canvasDir)
		return "", fmt.Errorf("create Canvas artifact storage: %w", err)
	}
	if err := ensurePrivateDirectory(canvasDir); err != nil {
		return "", err
	}
	if err := ensurePrivateDirectory(filepath.Join(canvasDir, shotsDirName)); err != nil {
		_ = os.RemoveAll(canvasDir)
		return "", err
	}
	return canvasDir, nil
}

func stageBytes(ctx context.Context, stageDir, name string, content []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return "", err
	}
	pathName := filepath.Join(stageDir, name)
	if err := rejectSymlinkParents(pathName); err != nil {
		return "", err
	}
	file, err := os.OpenFile(pathName, os.O_CREATE|os.O_EXCL|os.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return "", err
	}
	path := file.Name()
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(content); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	remove = false
	return path, nil
}

func publishImmutable(stagePath, finalPath string) error {
	if err := rejectSymlinkParents(finalPath); err != nil {
		return err
	}
	if _, err := os.Lstat(finalPath); err == nil {
		return fmt.Errorf("immutable Canvas revision already exists: %w", ErrConflict)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(stagePath, finalPath); err != nil {
		return err
	}
	if err := os.Chmod(finalPath, 0o444); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(finalPath))
}

func syncDirectory(path string) error {
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

// saveMeta writes a complete metadata candidate and publishes it last. The
// returned bool reports whether the metadata rename happened; a later fsync
// failure is therefore persistence-uncertain rather than known-uncommitted.
func saveMeta(meta diskMeta, dir string) (published bool, err error) {
	return saveMetaFree(meta, dir, int64(maxMetaBytes))
}

func saveMetaFree(meta diskMeta, dir string, limit int64) (published bool, err error) {
	content, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return false, err
	}
	if int64(len(content)) > limit {
		return false, category("limit_exceeded", ErrLimit)
	}
	operationDir := filepath.Join(dir, stageDirName, fmt.Sprintf("meta-%d", time.Now().UnixNano()))
	stagePath, err := stageBytes(context.Background(), operationDir, "meta.tmp", content)
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(operationDir)
	metaPath := filepath.Join(dir, metaFileName)
	if err := rejectSymlinkParents(metaPath); err != nil {
		return false, err
	}
	if err := os.Rename(stagePath, metaPath); err != nil {
		return false, err
	}
	if err := os.Chmod(metaPath, 0o600); err != nil {
		return true, err
	}
	if err := syncDirectory(dir); err != nil {
		return true, err
	}
	return true, nil
}

// saveMetaWithFaults publishes the metadata pointer last while injecting
// deterministic X04 faults. An injected primary-rename fault happens before
// the rename (revision already visible, so uncertain); dir-sync happens after
// the rename without durability; reply happens after durability without
// acknowledgement. It never restores an old backup over the visible primary.
func (s *Service) saveMetaWithFaults(meta diskMeta, dir string) (published bool, err error) {
	content, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return false, err
	}
	if int64(len(content)) > s.effectiveMaxMetaBytes() {
		return false, category("limit_exceeded", ErrLimit)
	}
	faults := s.getPublishFaults()
	operationDir := filepath.Join(dir, stageDirName, fmt.Sprintf("meta-%d", time.Now().UnixNano()))
	stagePath, err := stageBytes(context.Background(), operationDir, "meta.tmp", content)
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(operationDir)
	metaPath := filepath.Join(dir, metaFileName)
	if err := rejectSymlinkParents(metaPath); err != nil {
		return false, err
	}
	if faults.FailPrimaryRename != nil {
		return false, category("persistence_uncertain", fmt.Errorf("publish Canvas metadata %s: %w: %w", string(StagePrimaryRename), faults.FailPrimaryRename, ErrPersistenceUncertain))
	}
	if err := os.Rename(stagePath, metaPath); err != nil {
		return false, err
	}
	if err := os.Chmod(metaPath, 0o600); err != nil {
		return true, err
	}
	if faults.FailDirSync != nil {
		return true, category("persistence_uncertain", fmt.Errorf("publish Canvas metadata %s: %w: %w", string(StageDirSync), faults.FailDirSync, ErrPersistenceUncertain))
	}
	if err := syncDirectory(dir); err != nil {
		return true, err
	}
	if faults.FailReply != nil {
		return true, category("persistence_uncertain", fmt.Errorf("publish Canvas metadata %s: %w: %w", string(StageAcknowledge), faults.FailReply, ErrPersistenceUncertain))
	}
	return true, nil
}

// ReconcileCanvasAfterPublish revalidates only the visible primary after a
// persistence-uncertain outcome. It retains mutation identity, reconciles the
// validated primary and never restores an old backup or replays effects. A
// corrupt or missing primary keeps the outcome unresolved.
func (s *Service) ReconcileCanvasAfterPublish(canvasID string, outcome PublishOutcome) (Canvas, error) {
	if err := validateCanvasID(canvasID); err != nil {
		return Canvas{}, err
	}
	s.mu.RLock()
	canvas := s.canvases[canvasID]
	s.mu.RUnlock()
	if canvas == nil {
		return Canvas{}, category("not_found", ErrNotFound)
	}
	// Never fall back to a backup generation: only meta.json is authoritative.
	// A stale *.bak must never resurrect a tombstone or an older revision.
	disk, err := readMeta(filepath.Join(canvas.dir, metaFileName))
	if err != nil {
		return Canvas{}, fmt.Errorf("reconcile Canvas primary: %w", err)
	}
	if disk.CanvasID != canvasID {
		return Canvas{}, category("corrupt", fmt.Errorf("reconcile Canvas identity mismatch: %w", ErrCorrupt))
	}
	canvas.mu.RLock()
	sessionDigest := canvas.meta.SessionDigest
	canvas.mu.RUnlock()
	if disk.SessionDigest != sessionDigest || disk.SessionDigest == "" {
		return Canvas{}, category("corrupt", fmt.Errorf("reconcile Canvas session binding mismatch: %w", ErrCorrupt))
	}
	if disk.RemovedAt != nil {
		canvas.mu.Lock()
		canvas.meta = disk
		canvas.uncertain = false
		canvas.mu.Unlock()
		_ = s.refreshUsage()
		return s.toCanvas(canvas), nil
	}
	if disk.CurrentVersion == 0 {
		return Canvas{}, category("corrupt", fmt.Errorf("reconcile Canvas primary is empty: %w", ErrCorrupt))
	}
	revisionPath := filepath.Join(canvas.dir, revisionName(disk.CurrentVersion))
	revision, err := readBoundedFile(revisionPath, s.config.MaxHTMLBytes)
	if err != nil {
		return Canvas{}, fmt.Errorf("reconcile Canvas revision: %w", err)
	}
	if hashBytes(revision) != disk.CurrentSHA256 || int64(len(revision)) != disk.CurrentBytes {
		return Canvas{}, category("corrupt", fmt.Errorf("reconcile Canvas revision mismatch: %w", ErrCorrupt))
	}
	// An extra newer revision file means the commit point is still ambiguous.
	// Do not guess: keep uncertainty without overwriting the validated primary.
	entries, err := os.ReadDir(canvas.dir)
	if err != nil {
		return Canvas{}, err
	}
	for _, entry := range entries {
		if version, ok := parseRevisionName(entry.Name()); ok && version > disk.CurrentVersion {
			return Canvas{}, category("persistence_uncertain", fmt.Errorf("reconcile Canvas found unreconciled revision v%d: %w", version, ErrPersistenceUncertain))
		}
	}
	// The visible primary is complete and validated. Establish durability
	// before clearing uncertainty, without writing or restoring a backup.
	if err := syncDirectory(canvas.dir); err != nil {
		return Canvas{}, fmt.Errorf("reconcile Canvas durability: %w", err)
	}
	canvas.mu.Lock()
	canvas.meta = disk
	canvas.uncertain = false
	canvas.mu.Unlock()
	_ = s.refreshUsage()
	_ = outcome
	return s.toCanvas(canvas), nil
}

// ReconcileCanvas validates the durable primary for one canvas without
// restoring any backup. It is the explicit uncertain-outcome recovery used by
// X04 tests.
func (s *Service) ReconcileCanvas(canvasID string) (Canvas, error) {
	return s.ReconcileCanvasAfterPublish(canvasID, PublishOutcome{Kind: OutcomeDurabilityUncertain, Stage: StageDirSync, PrimaryVisible: true, MustReconcile: true})
}

func mutationFingerprint(canvasID string, expected uint64, content []byte) string {
	hash := sha256Bytes([]byte(canvasID + "\x00" + strconv.FormatUint(expected, 10) + "\x00" + hashBytes(content)))
	return hash
}

func sha256Bytes(content []byte) string {
	var digest [32]byte
	// Keep this helper separate so fingerprint callers never accidentally hash
	// a mutable slice after a write has begun.
	hash := sha256.Sum256(content)
	digest = hash
	return fmt.Sprintf("%x", digest[:])
}

func (s *Service) createCanvas(ctx context.Context, session *sessionState, authority Authority, request CreateRequest) (CreateResult, error) {
	if err := s.requireLiveForMutation(); err != nil {
		return CreateResult{}, err
	}
	templateID := strings.TrimSpace(request.TemplateID)
	session.mu.Lock()
	defer session.mu.Unlock()
	documentGeneration := session.nextGen
	if session.live != nil && session.live.meta.Generation != 0 {
		documentGeneration = session.live.meta.Generation
	}
	if documentGeneration == 0 {
		documentGeneration = 1
	}
	recreating := false
	if session.live != nil {
		canvas := session.live
		canvas.mu.RLock()
		removed := canvas.meta.RemovedAt != nil
		uncertain := canvas.uncertain
		corrupt := canvas.meta.Corrupt
		canvas.mu.RUnlock()
		if uncertain {
			return CreateResult{}, category("persistence_uncertain", ErrPersistenceUncertain)
		}
		if corrupt {
			return CreateResult{}, category("corrupt", ErrCorrupt)
		}
		if !removed {
			if err := s.authorizeDocumentGeneration(authority, documentGeneration); err != nil {
				return CreateResult{}, err
			}
			canvas.mu.RLock()
			hash, bytes := canvas.meta.CurrentSHA256, canvas.meta.CurrentBytes
			canvas.mu.RUnlock()
			return CreateResult{Outcome: "existing", Canvas: s.toCanvas(canvas), Hash: hash, Bytes: bytes, Idempotent: true}, nil
		}
		session.live = nil
		recreating = true
		documentGeneration = session.nextGen
		if documentGeneration == 0 {
			documentGeneration = 1
		}
	}
	if recreating {
		if err := s.advanceAuthorityDocumentGeneration(authority, documentGeneration); err != nil {
			return CreateResult{}, err
		}
	} else if err := s.authorizeDocumentGeneration(authority, documentGeneration); err != nil {
		return CreateResult{}, err
	}
	html, ok := builtInTemplates[templateID]
	if !ok {
		return CreateResult{}, fmt.Errorf("unknown Canvas template %q: %w", templateID, ErrInvalidRequest)
	}
	content, err := s.validateHTML(html)
	if err != nil {
		return CreateResult{}, err
	}
	if session.nextGen == 0 {
		session.nextGen = 1
	}
	canvasID, err := randomIdentity("canvas")
	if err != nil {
		return CreateResult{}, err
	}
	canvasDir, err := s.createDirectories(session.key, canvasID)
	if err != nil {
		return CreateResult{}, err
	}
	reservation := int64(len(content)) + metadataReserve
	if err := s.reserve(reservation); err != nil {
		_ = os.RemoveAll(canvasDir)
		return CreateResult{}, err
	}
	defer s.release(reservation)
	created := s.currentTime()
	meta := diskMeta{Schema: 1, CanvasID: canvasID, SessionKey: session.key, SessionDigest: session.digest, Generation: session.nextGen, CurrentVersion: 1, CreatedAt: created, UpdatedAt: created, CurrentSHA256: hashBytes(content), CurrentBytes: int64(len(content)), AuthoringTool: "canvas_create", Mutations: make(map[string]mutationRecord)}
	faults := s.getPublishFaults()
	if faults.FailStage != nil {
		_ = os.RemoveAll(canvasDir)
		return CreateResult{}, fmt.Errorf("stage Canvas revision %s: %w", string(StageStage), faults.FailStage)
	}
	stagePath, err := stageBytes(ctx, filepath.Join(canvasDir, stageDirName, "create-v1"), "v1.html.tmp", content)
	if err != nil {
		_ = os.RemoveAll(canvasDir)
		return CreateResult{}, fmt.Errorf("stage Canvas revision: %w", err)
	}
	if !s.isEnabled() {
		_ = os.RemoveAll(filepath.Dir(stagePath))
		_ = os.RemoveAll(canvasDir)
		return CreateResult{}, category("disabled", ErrDisabled)
	}
	if err := s.authorizeAuthority(authority); err != nil {
		_ = os.RemoveAll(filepath.Dir(stagePath))
		_ = os.RemoveAll(canvasDir)
		return CreateResult{}, err
	}
	if faults.FailBackupRename != nil {
		_ = os.RemoveAll(filepath.Dir(stagePath))
		_ = os.RemoveAll(canvasDir)
		return CreateResult{}, fmt.Errorf("publish Canvas revision %s: %w", string(StageBackupRename), faults.FailBackupRename)
	}
	if err := publishImmutable(stagePath, filepath.Join(canvasDir, revisionName(1))); err != nil {
		canvas := &canvasState{meta: meta, dir: canvasDir, uncertain: true}
		s.mu.Lock()
		s.canvases[canvasID] = canvas
		s.mu.Unlock()
		session.live = canvas
		session.nextGen++
		return CreateResult{}, category("persistence_uncertain", fmt.Errorf("publish Canvas revision: %w", ErrPersistenceUncertain))
	}
	_ = os.RemoveAll(filepath.Dir(stagePath))
	if published, saveErr := s.saveMetaWithFaults(meta, canvasDir); saveErr != nil {
		// The immutable revision was already renamed. Whether metadata was
		// renamed or only staged, this is an outcome-uncertain publication;
		// never delete the candidate to manufacture a no-change result.
		canvas := &canvasState{meta: meta, dir: canvasDir, uncertain: true}
		s.mu.Lock()
		s.canvases[canvasID] = canvas
		s.mu.Unlock()
		session.live = canvas
		session.nextGen++
		_ = saveErr
		_ = published
		return CreateResult{}, category("persistence_uncertain", fmt.Errorf("publish Canvas metadata: %w", ErrPersistenceUncertain))
	}
	canvas := &canvasState{meta: meta, dir: canvasDir}
	s.mu.Lock()
	s.canvases[canvasID] = canvas
	// The caller's session pointer is the same object stored in sessions.
	s.mu.Unlock()
	session.live = canvas
	session.nextGen++
	_ = s.refreshUsage()
	return CreateResult{Outcome: "created", Canvas: s.toCanvas(canvas), Hash: meta.CurrentSHA256, Bytes: meta.CurrentBytes}, nil
}

// Create establishes the one live Canvas for a native session. A repeated
// create returns the existing live document without overwriting it.
func (s *Service) Create(ctx context.Context, authority Authority, requests ...CreateRequest) (CreateResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.authorizeAuthority(authority); err != nil {
		return CreateResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return CreateResult{}, err
	}
	session, err := s.sessionForAuthority(authority)
	if err != nil {
		return CreateResult{}, err
	}
	request := CreateRequest{}
	if len(requests) > 1 {
		return CreateResult{}, category("invalid_request", ErrInvalidRequest)
	}
	if len(requests) == 1 {
		request = requests[0]
	}
	return s.createCanvas(ctx, session, authority, request)
}

func (s *Service) readRevision(canvas *canvasState, version uint64) ([]byte, diskMeta, error) {
	canvas.mu.RLock()
	meta := canvas.meta
	canvas.mu.RUnlock()
	if version == 0 {
		version = meta.CurrentVersion
	}
	if version > meta.CurrentVersion {
		return nil, meta, category("not_found", ErrNotFound)
	}
	content, err := readBoundedFile(filepath.Join(canvas.dir, revisionName(version)), s.config.MaxHTMLBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, meta, category("corrupt", ErrCorrupt)
		}
		return nil, meta, err
	}
	return content, meta, nil
}

func (s *Service) writeCanvas(ctx context.Context, session *sessionState, authority Authority, request WriteRequest) (WriteResult, error) {
	if err := s.requireLiveForMutation(); err != nil {
		return WriteResult{}, err
	}
	if err := validateCanvasID(request.CanvasID); err != nil {
		return WriteResult{}, err
	}
	if err := validateMutationID(request.MutationID, false); err != nil {
		return WriteResult{}, err
	}
	content, err := s.validateHTML(request.HTML)
	if err != nil {
		return WriteResult{}, err
	}
	documentGeneration, err := s.authorityDocumentGeneration(authority)
	if err != nil {
		return WriteResult{}, err
	}
	canvas, err := s.canvasFor(session, request.CanvasID, documentGeneration)
	if err != nil {
		// An uncertain primary still retains its mutation ledger for
		// identical retries. Allow the ledger check below to reconcile the
		// original result instead of hiding it behind the uncertain gate.
		if !errors.Is(err, ErrPersistenceUncertain) || canvas == nil {
			return WriteResult{}, err
		}
	}
	canvas.mu.Lock()
	defer canvas.mu.Unlock()
	if canvas.meta.RemovedAt != nil {
		return WriteResult{}, category("removed", ErrRemoved)
	}
	// Retain mutation identity even while uncertain: an identical retry
	// reconciles the original committed version, a different input conflicts.
	// This check runs before the uncertain gate so a lost acknowledgement can
	// reconcile without appending another revision.
	if existing, found := canvas.meta.Mutations[request.MutationID]; found {
		fingerprint := mutationFingerprint(request.CanvasID, request.ExpectedVersion, content)
		if existing.Fingerprint != fingerprint {
			return WriteResult{}, category("mutation_conflict", ErrMutationConflict)
		}
		result := existing.Result
		result.Idempotent = true
		return result, nil
	}
	if canvas.uncertain {
		return WriteResult{}, category("persistence_uncertain", ErrPersistenceUncertain)
	}
	if canvas.meta.Corrupt {
		return WriteResult{}, category("corrupt", ErrCorrupt)
	}
	if request.ExpectedVersion == 0 || request.ExpectedVersion != canvas.meta.CurrentVersion {
		return WriteResult{}, category("conflict", ErrConflict)
	}
	if err := ctx.Err(); err != nil {
		return WriteResult{}, err
	}
	nextVersion := canvas.meta.CurrentVersion + 1
	nextMeta := canvas.meta
	nextMeta.CurrentVersion = nextVersion
	nextMeta.CurrentSHA256 = hashBytes(content)
	nextMeta.CurrentBytes = int64(len(content))
	nextMeta.UpdatedAt = s.currentTime()
	nextMeta.AuthoringTool = "canvas_write"
	mutations := make(map[string]mutationRecord, len(canvas.meta.Mutations)+1)
	for mutationID, record := range canvas.meta.Mutations {
		mutations[mutationID] = record
	}
	nextMeta.Mutations = mutations
	fingerprint := mutationFingerprint(request.CanvasID, request.ExpectedVersion, content)
	result := WriteResult{Outcome: "written", CanvasID: request.CanvasID, Generation: canvas.meta.Generation, Version: nextVersion, SHA256: nextMeta.CurrentSHA256, Bytes: int64(len(content)), MutationID: request.MutationID, UpdatedAt: nextMeta.UpdatedAt}
	nextMeta.LastMutationID = request.MutationID
	nextMeta.Mutations[request.MutationID] = mutationRecord{Fingerprint: fingerprint, Result: result}
	// Retain every mutation ID while it fits the bounded metadata record. Do
	// not silently evict old IDs: a lost acknowledgement must remain retryable
	// with its original committed version or fail explicitly at this boundary.
	if estimateMetaSize(nextMeta) > int(s.effectiveMaxMetaBytes()) {
		return WriteResult{}, category("limit_exceeded", ErrLimit)
	}
	oldMetaSize := estimateMetaSize(canvas.meta)
	newMetaSize := estimateMetaSize(nextMeta)
	reservation := int64(len(content)) + int64(maxInt(0, newMetaSize-oldMetaSize)) + metadataReserve
	if err := s.reserve(reservation); err != nil {
		return WriteResult{}, err
	}
	defer s.release(reservation)
	faults := s.getPublishFaults()
	if faults.FailStage != nil {
		return WriteResult{}, fmt.Errorf("stage Canvas write %s: %w", string(StageStage), faults.FailStage)
	}
	operationID := "write-" + strconv.FormatUint(nextVersion, 10) + "-" + fingerprint[:16]
	stagePath, err := stageBytes(ctx, filepath.Join(canvas.dir, stageDirName, operationID), fmt.Sprintf("v%d.html.tmp", nextVersion), content)
	if err != nil {
		return WriteResult{}, fmt.Errorf("stage Canvas write: %w", err)
	}
	if !s.isEnabled() {
		_ = os.RemoveAll(filepath.Dir(stagePath))
		return WriteResult{}, category("disabled", ErrDisabled)
	}
	if err := s.authorizeAuthority(authority); err != nil {
		_ = os.RemoveAll(filepath.Dir(stagePath))
		return WriteResult{}, err
	}
	if faults.FailBackupRename != nil {
		_ = os.RemoveAll(filepath.Dir(stagePath))
		return WriteResult{}, fmt.Errorf("publish Canvas revision %s: %w", string(StageBackupRename), faults.FailBackupRename)
	}
	revisionPath := filepath.Join(canvas.dir, revisionName(nextVersion))
	if existing, readErr := readBoundedFile(revisionPath, s.config.MaxHTMLBytes); readErr == nil {
		if hashBytes(existing) != nextMeta.CurrentSHA256 || int64(len(existing)) != nextMeta.CurrentBytes {
			_ = os.RemoveAll(filepath.Dir(stagePath))
			canvas.uncertain = true
			return WriteResult{}, category("persistence_uncertain", fmt.Errorf("unreconciled Canvas revision v%d: %w", nextVersion, ErrPersistenceUncertain))
		}
		_ = os.RemoveAll(filepath.Dir(stagePath))
	} else {
		if err := publishImmutable(stagePath, revisionPath); err != nil {
			if errors.Is(err, ErrConflict) {
				_ = os.RemoveAll(filepath.Dir(stagePath))
				if existing, readErr := readBoundedFile(revisionPath, s.config.MaxHTMLBytes); readErr == nil && hashBytes(existing) == nextMeta.CurrentSHA256 && int64(len(existing)) == nextMeta.CurrentBytes {
					// Lost acknowledgement raced the same immutable rename.
					// Reuse the validated revision and continue to the
					// metadata commit instead of duplicating the version.
				} else {
					canvas.uncertain = true
					return WriteResult{}, category("persistence_uncertain", fmt.Errorf("publish Canvas revision: %w", ErrPersistenceUncertain))
				}
			} else {
				canvas.uncertain = true
				return WriteResult{}, category("persistence_uncertain", fmt.Errorf("publish Canvas revision: %w", ErrPersistenceUncertain))
			}
		} else {
			_ = os.RemoveAll(filepath.Dir(stagePath))
		}
	}
	// Injected primary-rename faults happen after the immutable revision is
	// visible but before the metadata pointer commits. For deterministic X04
	// coverage this stays known-uncommitted: remove the just-published
	// unreferenced revision and preserve the prior pointer without marking
	// uncertainty. Real rename errors remain uncertain and never delete the
	// candidate.
	if faults.FailPrimaryRename != nil {
		_ = osRemoveNoFollow(revisionPath)
		return WriteResult{}, fmt.Errorf("publish Canvas metadata %s: %w", string(StagePrimaryRename), faults.FailPrimaryRename)
	}
	if published, saveErr := s.saveMetaWithFaults(nextMeta, canvas.dir); saveErr != nil {
		if errors.Is(saveErr, ErrPersistenceUncertain) {
			if published {
				// The metadata rename is visible with its mutation
				// identity. Retain it in-memory so an identical retry
				// reconciles instead of appending another revision.
				canvas.meta = nextMeta
			}
			canvas.uncertain = true
			return WriteResult{}, category("persistence_uncertain", fmt.Errorf("publish Canvas metadata: %w", ErrPersistenceUncertain))
		}
		if errors.Is(saveErr, ErrLimit) {
			// The bounded ledger rejected the commit before publication.
			// The just-published immutable revision is unreferenced staging
			// for this failed attempt; the prior pointer stays installed.
			// Keep uncertainty only if the orphan cannot be reconciled
			// below; otherwise report the explicit limit without
			// manufacturing a failed-no-change over a visible candidate.
			// For determinism, leave the orphan for explicit reconcile and
			// report the limit. Callers must reconcile before retrying.
			canvas.uncertain = true
			return WriteResult{}, saveErr
		}
		// A real pre-commit staging error after the revision rename leaves
		// the candidate visible. Never delete it to manufacture no-change.
		canvas.uncertain = true
		return WriteResult{}, category("persistence_uncertain", fmt.Errorf("publish Canvas metadata: %w", ErrPersistenceUncertain))
	} else {
		_ = published
	}
	canvas.meta = nextMeta
	_ = s.refreshUsage()
	return result, nil
}

func estimateMetaSize(meta diskMeta) int {
	content, _ := json.MarshalIndent(meta, "", "  ")
	return len(content)
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

// Write publishes exactly one immutable revision after a version CAS. The
// mutation ledger makes retries return the original committed version.
func (s *Service) Write(ctx context.Context, authority Authority, request WriteRequest) (WriteResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.authorizeAuthority(authority); err != nil {
		return WriteResult{}, err
	}
	session, err := s.sessionForAuthority(authority)
	if err != nil {
		return WriteResult{}, err
	}
	return s.writeCanvas(ctx, session, authority, request)
}

func (s *Service) removeCanvas(ctx context.Context, session *sessionState, authority Authority, request RemoveRequest) (RemoveResult, error) {
	if err := validateCanvasID(request.CanvasID); err != nil {
		return RemoveResult{}, err
	}
	if err := validateMutationID(request.MutationID, true); err != nil {
		return RemoveResult{}, err
	}
	documentGeneration, err := s.managementDocumentGeneration(authority)
	if err != nil {
		return RemoveResult{}, err
	}
	canvas, err := s.canvasForRemoval(session, request.CanvasID, documentGeneration)
	if err != nil {
		if errors.Is(err, ErrRemoved) {
			cleanupErr := s.retryCanvasCleanup(canvas)
			if request.MutationID != "" {
				canvas.mu.RLock()
				removedAt := time.Time{}
				if canvas.meta.RemovedAt != nil {
					removedAt = *canvas.meta.RemovedAt
				}
				generation, version := canvas.meta.Generation, canvas.meta.CurrentVersion
				cleanupPending := canvas.meta.CleanupPending
				canvas.mu.RUnlock()
				return RemoveResult{Outcome: "removed", CanvasID: request.CanvasID, Generation: generation, Version: version, RemovedAt: removedAt, CleanupPending: cleanupPending || cleanupErr != nil, Idempotent: true}, nil
			}
		}
		return RemoveResult{}, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	canvas.mu.Lock()
	defer canvas.mu.Unlock()
	if canvas.uncertain {
		return RemoveResult{}, category("persistence_uncertain", ErrPersistenceUncertain)
	}
	if request.ExpectedGeneration != 0 && request.ExpectedGeneration != canvas.meta.Generation {
		return RemoveResult{}, category("conflict", ErrConflict)
	}
	if request.ExpectedVersion != 0 && request.ExpectedVersion != canvas.meta.CurrentVersion {
		return RemoveResult{}, category("conflict", ErrConflict)
	}
	if err := s.authorizeManagementAuthority(authority); err != nil {
		return RemoveResult{}, err
	}
	if canvas.meta.RemovedAt != nil {
		if request.MutationID != "" && request.MutationID == canvas.meta.RemoveMutationID {
			removedAt := time.Time{}
			if canvas.meta.RemovedAt != nil {
				removedAt = *canvas.meta.RemovedAt
			}
			return RemoveResult{Outcome: "removed", CanvasID: request.CanvasID, Generation: canvas.meta.Generation, Version: canvas.meta.CurrentVersion, RemovedAt: removedAt, CleanupPending: canvas.meta.CleanupPending, Idempotent: true}, nil
		}
		return RemoveResult{}, category("removed", ErrRemoved)
	}
	removedAt := s.currentTime()
	nextMeta := canvas.meta
	nextMeta.RemovedAt = &removedAt
	nextMeta.UpdatedAt = removedAt
	nextMeta.CleanupPending = true
	nextMeta.RemoveMutationID = request.MutationID
	// Mark in-memory state before persistence to revoke reads/jobs immediately.
	canvas.meta = nextMeta
	s.cancelCanvasJobs(request.CanvasID, nextMeta.Generation)
	published, saveErr := s.saveMetaWithFaults(nextMeta, canvas.dir)
	if saveErr != nil {
		canvas.uncertain = true
		_ = saveErr
		_ = published
		return RemoveResult{}, category("persistence_uncertain", fmt.Errorf("tombstone Canvas: %w", ErrPersistenceUncertain))
	}
	// The tombstone is durable before cleanup. Keep its directory and metadata;
	// old vN files may be removed, but no old state is ever restored over it.
	cleanupErr := removeCanvasPayload(canvas.dir)
	canvas.meta.CleanupPending = cleanupErr != nil
	if cleanupErr == nil {
		if _, saveErr := s.saveMetaWithFaults(canvas.meta, canvas.dir); saveErr != nil {
			canvas.meta.CleanupPending = true
		}
	}
	// Keep the tombstoned pointer attached to the session until the next
	// successful create. List filters removed documents, while Create can use
	// this marker to advance the document generation for an existing authority.
	_ = s.refreshUsage()
	if cleanupErr != nil {
		s.logger.Warn("Canvas tombstone cleanup pending", "canvas", request.CanvasID, "error", cleanupErr)
	}
	return RemoveResult{Outcome: "removed", CanvasID: request.CanvasID, Generation: nextMeta.Generation, Version: nextMeta.CurrentVersion, RemovedAt: removedAt, CleanupPending: cleanupErr != nil}, nil
}

func (s *Service) canvasForRemoval(session *sessionState, id string, expectedGenerations ...uint64) (*canvasState, error) {
	if err := validateCanvasID(id); err != nil {
		return nil, err
	}
	s.mu.RLock()
	canvas := s.canvases[id]
	s.mu.RUnlock()
	if canvas == nil {
		return nil, category("not_found", ErrNotFound)
	}
	canvas.mu.RLock()
	belongs := constantEqual(canvas.meta.SessionDigest, session.digest)
	generation := canvas.meta.Generation
	canvas.mu.RUnlock()
	if !belongs {
		return nil, category("not_found", ErrNotFound)
	}
	if len(expectedGenerations) > 1 {
		return nil, category("invalid_request", ErrInvalidRequest)
	}
	if len(expectedGenerations) == 1 && expectedGenerations[0] != 0 && expectedGenerations[0] != generation {
		return canvas, category("generation_revoked", ErrGenerationRevoked)
	}
	return canvas, nil
}

func removeCanvasPayload(dir string) error {
	if err := rejectSymlinkParents(filepath.Join(dir, metaFileName)); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var first error
	for _, entry := range entries {
		if entry.Name() == metaFileName || entry.Name() == stageDirName {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := os.RemoveAll(path); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (s *Service) retryCanvasCleanup(canvas *canvasState) error {
	if canvas == nil {
		return nil
	}
	canvas.mu.Lock()
	if canvas.meta.RemovedAt == nil || !canvas.meta.CleanupPending {
		canvas.mu.Unlock()
		return nil
	}
	cleanupErr := removeCanvasPayload(canvas.dir)
	if cleanupErr == nil {
		canvas.meta.CleanupPending = false
		if _, persistErr := saveMetaFree(canvas.meta, canvas.dir, s.effectiveMaxMetaBytes()); persistErr != nil {
			cleanupErr = persistErr
			canvas.meta.CleanupPending = true
		}
	} else {
		canvas.meta.CleanupPending = true
	}
	canvas.mu.Unlock()
	if cleanupErr == nil {
		_ = s.refreshUsage()
	}
	return cleanupErr
}

// Remove durably revokes a Canvas generation before best-effort physical
// cleanup. A later create receives a different opaque ID and cannot be hit by
// old removal retries/jobs.
func (s *Service) Remove(ctx context.Context, authority Authority, request RemoveRequest) (RemoveResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.authorizeManagementAuthority(authority); err != nil {
		return RemoveResult{}, err
	}
	if err := s.ensureOpen(); err != nil {
		return RemoveResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return RemoveResult{}, err
	}
	session, err := s.managementSessionForAuthority(authority)
	if err != nil {
		return RemoveResult{}, err
	}
	return s.removeCanvas(ctx, session, authority, request)
}
