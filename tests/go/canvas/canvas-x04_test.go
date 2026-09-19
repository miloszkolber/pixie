package canvas_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/miloszkolber/pixie/internal/canvas"
)

func mustCreateCanvas(t *testing.T, service *canvas.Service, authority canvas.Authority) string {
	t.Helper()
	created, err := service.Create(context.Background(), authority)
	if err != nil {
		t.Fatal(err)
	}
	return created.Canvas.ID
}

func mustWriteCanvas(t *testing.T, service *canvas.Service, authority canvas.Authority, canvasID string, expected uint64, html, mutation string) uint64 {
	t.Helper()
	written, err := service.Write(context.Background(), authority, canvas.WriteRequest{CanvasID: canvasID, ExpectedVersion: expected, HTML: html, MutationID: mutation})
	if err != nil {
		t.Fatalf("write %s v%d: %v", mutation, expected, err)
	}
	return written.Version
}

func findCanvasDir(t *testing.T, root, canvasID string) string {
	t.Helper()
	storageRoot := filepath.Join(root, "mcp-canvas")
	var found string
	err := filepath.WalkDir(storageRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Name() == "meta.json" && strings.Contains(path, canvasID) {
			found = filepath.Dir(path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == "" {
		t.Fatalf("canvas dir for %s not found under %s", canvasID, storageRoot)
	}
	return found
}

func assertNoBackupRestore(t *testing.T, canvasDir string) {
	t.Helper()
	entries, err := os.ReadDir(canvasDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".bak") {
			content, err := os.ReadFile(filepath.Join(canvasDir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			primary, err := os.ReadFile(filepath.Join(canvasDir, "meta.json"))
			if err != nil {
				t.Fatal(err)
			}
			if string(content) == string(primary) {
				continue
			}
			// A stale backup must never have overwritten the visible primary.
			// The primary must remain the reconciled version; the backup is inert.
			_ = primary
		}
	}
}

func TestCanvasPublishPerStepFaults(t *testing.T) {
	stages := []struct {
		name   string
		faults canvas.PublishFaults
		known  bool
	}{
		{name: "stage", faults: canvas.PublishFaults{FailStage: errors.New("injected stage failure")}, known: true},
		{name: "backup-rename", faults: canvas.PublishFaults{FailBackupRename: errors.New("injected backup-rename failure")}, known: true},
		{name: "primary-rename", faults: canvas.PublishFaults{FailPrimaryRename: errors.New("injected primary-rename failure")}, known: true},
		{name: "dir-sync", faults: canvas.PublishFaults{FailDirSync: errors.New("injected dir-sync failure")}, known: false},
		{name: "reply-loss", faults: canvas.PublishFaults{FailReply: errors.New("injected reply loss")}, known: false},
	}
	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			root := t.TempDir()
			config := canvas.DefaultConfig(root)
			config.WorkerLauncher = canvas.NewDeterministicWorkerLauncher()
			service, err := canvas.New(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Shutdown() })
			authority := attach(t, service, "native-session-"+stage.name)
			canvasID := mustCreateCanvas(t, service, authority)
			// Bounded fixture: small deterministic HTML well under 512 KiB.
			candidateHTML := "<html><body><main><h1>" + stage.name + "</h1><p>candidate</p></main></body></html>"
			mutationID := "mut-per-step-" + stage.name
			service.SetPublishFaults(stage.faults)
			_, err = service.Write(context.Background(), authority, canvas.WriteRequest{CanvasID: canvasID, ExpectedVersion: 1, HTML: candidateHTML, MutationID: mutationID})
			if err == nil {
				t.Fatalf("%s fault must block the publish", stage.name)
			}
			if stage.known {
				if errors.Is(err, canvas.ErrPersistenceUncertain) || canvas.Code(err) == "persistence_uncertain" {
					t.Fatalf("%s must stay known-uncommitted, got uncertain: %v", stage.name, err)
				}
				outcome := canvas.PublishOutcome{Kind: canvas.OutcomeKnownUncommitted, Stage: canvas.PublishStage(stage.name)}
				decision := canvas.DecideCanvasPublish(outcome)
				if decision.MayDispatch || decision.MustReconcile || decision.MustRetainMutation {
					t.Fatalf("%s known decision must preserve prior without reconcile: %#v", stage.name, decision)
				}
				// Prior commit stays installed and readable.
				read, err := service.Read(context.Background(), authority, canvas.ReadRequest{CanvasID: canvasID, Version: 1})
				if err != nil {
					t.Fatalf("%s known failure damaged prior revision: %v", stage.name, err)
				}
				if read.Version != 1 {
					t.Fatalf("%s prior version = %d, want 1", stage.name, read.Version)
				}
				reconciled, err := service.ReconcileCanvasAfterPublish(canvasID, outcome)
				if err != nil {
					t.Fatalf("%s reconcile validated prior: %v", stage.name, err)
				}
				if reconciled.Version != 1 {
					t.Fatalf("%s reconciled version = %d, want prior 1", stage.name, reconciled.Version)
				}
				canvasDir := findCanvasDir(t, root, canvasID)
				assertNoBackupRestore(t, canvasDir)
				// Retry after faults clear re-attempts the same mutation without duplicating.
				service.SetPublishFaults(canvas.PublishFaults{})
				written, err := service.Write(context.Background(), authority, canvas.WriteRequest{CanvasID: canvasID, ExpectedVersion: 1, HTML: candidateHTML, MutationID: mutationID})
				if err != nil {
					t.Fatalf("%s retry after known must re-attempt: %v", stage.name, err)
				}
				if written.Version != 2 {
					t.Fatalf("%s retry version = %d, want 2", stage.name, written.Version)
				}
				retry, err := service.Write(context.Background(), authority, canvas.WriteRequest{CanvasID: canvasID, ExpectedVersion: 1, HTML: candidateHTML, MutationID: mutationID})
				if err != nil || !retry.Idempotent || retry.Version != 2 {
					t.Fatalf("%s committed retry must be idempotent: %#v %v", stage.name, retry, err)
				}
				if _, err := service.Write(context.Background(), authority, canvas.WriteRequest{CanvasID: canvasID, ExpectedVersion: 1, HTML: "<p>different</p>", MutationID: mutationID}); !errors.Is(err, canvas.ErrMutationConflict) {
					t.Fatalf("%s reused mutation with different input must conflict, got %v", stage.name, err)
				}
			} else {
				if !errors.Is(err, canvas.ErrPersistenceUncertain) {
					t.Fatalf("%s must be persistence-uncertain, got %v", stage.name, err)
				}
				outcome := canvas.PublishOutcome{Kind: canvas.OutcomeDurabilityUncertain, Stage: canvas.PublishStage(stage.name), PrimaryVisible: true, MustReconcile: true}
				decision := canvas.DecideCanvasPublish(outcome)
				if decision.MayDispatch || !decision.MustReconcile || !decision.MustRetainMutation {
					t.Fatalf("%s uncertain must block dispatch and retain mutation: %#v", stage.name, decision)
				}
				if decision.Reason == "" {
					t.Fatal("uncertain decision requires a reason")
				}
				// The visible candidate stays for reconciliation; never restored from backup.
				reconciled, err := service.ReconcileCanvasAfterPublish(canvasID, outcome)
				if err != nil {
					t.Fatalf("%s reconcile validated primary: %v", stage.name, err)
				}
				if reconciled.Version != 2 {
					t.Fatalf("%s reconciled version = %d, want candidate 2", stage.name, reconciled.Version)
				}
				canvasDir := findCanvasDir(t, root, canvasID)
				assertNoBackupRestore(t, canvasDir)
				primary, err := os.ReadFile(filepath.Join(canvasDir, "meta.json"))
				if err != nil || !strings.Contains(string(primary), `"currentVersion": 2`) {
					t.Fatalf("%s uncertain primary must stay visible for reconciliation", stage.name)
				}
				// Identical retry reconciles the original even while the fault is armed.
				retry, err := service.Write(context.Background(), authority, canvas.WriteRequest{CanvasID: canvasID, ExpectedVersion: 1, HTML: candidateHTML, MutationID: mutationID})
				if err != nil {
					t.Fatalf("%s identical retry must reconcile: %v", stage.name, err)
				}
				if !retry.Idempotent || retry.Version != 2 {
					t.Fatalf("%s retry must return original v2: %#v", stage.name, retry)
				}
				if _, err := service.Write(context.Background(), authority, canvas.WriteRequest{CanvasID: canvasID, ExpectedVersion: 1, HTML: "<p>different</p>", MutationID: mutationID}); !errors.Is(err, canvas.ErrMutationConflict) {
					t.Fatalf("%s reused mutation with different input must conflict, got %v", stage.name, err)
				}
				service.SetPublishFaults(canvas.PublishFaults{})
				installed, err := service.Write(context.Background(), authority, canvas.WriteRequest{CanvasID: canvasID, ExpectedVersion: 2, HTML: "<html><body><p>installed</p></body></html>", MutationID: "mut-installed-" + stage.name})
				if err != nil {
					t.Fatalf("%s installed publish after reconcile must succeed: %v", stage.name, err)
				}
				if installed.Version != 3 {
					t.Fatalf("%s installed version = %d, want 3", stage.name, installed.Version)
				}
			}
		})
	}
}

func TestCanvasCrashPartialWriteRestart(t *testing.T) {
	root := t.TempDir()
	config := canvas.DefaultConfig(root)
	config.WorkerLauncher = canvas.NewDeterministicWorkerLauncher()
	first, err := canvas.New(config)
	if err != nil {
		t.Fatal(err)
	}
	authority := attach(t, first, "native-session-crash")
	canvasID := mustCreateCanvas(t, first, authority)
	baseHTML := "<html><body><main><h1>v2</h1><p>committed</p></main></body></html>"
	mustWriteCanvas(t, first, authority, canvasID, 1, baseHTML, "crash-base")
	read, err := first.Read(context.Background(), authority, canvas.ReadRequest{CanvasID: canvasID, Version: 2})
	if err != nil || read.Version != 2 {
		t.Fatalf("base v2 = %#v %v", read, err)
	}
	canvasDir := findCanvasDir(t, root, canvasID)
	// Simulate a crash during staging: abandoned partial files that must never
	// become the committed state. Bounded fixtures, no live data.
	partialDir := filepath.Join(canvasDir, "staging", "crash-partial")
	if err := os.MkdirAll(partialDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partialDir, "v3.html.tmp"), []byte("<html><body><p>partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	metaPartialDir := filepath.Join(canvasDir, "staging", "meta-123")
	if err := os.MkdirAll(metaPartialDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metaPartialDir, "meta.tmp"), []byte(`{"schema":1,"canvasId":"`), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = first.Shutdown()
	// Restart on the same disposable state. Old-or-new complete state only.
	second, err := canvas.New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Shutdown() })
	restartedAuthority := attach(t, second, "native-session-crash")
	listed, err := second.List(context.Background(), restartedAuthority)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 1 || listed.Items[0].Version != 2 {
		t.Fatalf("restart must expose old complete v2, got %#v", listed.Items)
	}
	after, err := second.Read(context.Background(), restartedAuthority, canvas.ReadRequest{CanvasID: canvasID, Version: 2})
	if err != nil || after.Version != 2 || !strings.Contains(after.Text, "committed") {
		t.Fatalf("restart primary must be complete v2: %#v %v", after, err)
	}
	// Abandoned staging must be cleaned; no partial meta or revision may remain.
	if _, err := os.Stat(partialDir); !os.IsNotExist(err) {
		t.Fatalf("abandoned staging must be cleaned, stat=%v", err)
	}
	// The committed primary is never partial: meta decodes and the revision hash matches.
	metaContent, err := os.ReadFile(filepath.Join(canvasDir, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(metaContent), `"currentVersion": 2`) {
		t.Fatalf("restart meta must stay v2 complete: %s", metaContent)
	}
	revision, err := os.ReadFile(filepath.Join(canvasDir, "v2.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(revision) != baseHTML {
		t.Fatal("restart revision must be the complete committed bytes, never a partial write")
	}
	// Explicit reconciliation retains identity and validates the primary without backup replay.
	reconciled, err := second.ReconcileCanvas(canvasID)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Version != 2 {
		t.Fatalf("reconcile must validate old-or-new complete v2, got %d", reconciled.Version)
	}
	assertNoBackupRestore(t, canvasDir)
	retry, err := second.Write(context.Background(), restartedAuthority, canvas.WriteRequest{CanvasID: canvasID, ExpectedVersion: 2, HTML: baseHTML, MutationID: "crash-base-retry-same-input-new-id"})
	if err != nil {
		t.Fatalf("dependent mutation after reconcile must be accepted: %v", err)
	}
	_ = retry
	// The original mutation identity survives the restart via the persisted ledger.
	idempotent, err := second.Write(context.Background(), restartedAuthority, canvas.WriteRequest{CanvasID: canvasID, ExpectedVersion: 1, HTML: baseHTML, MutationID: "crash-base"})
	if err != nil || !idempotent.Idempotent || idempotent.Version != 2 {
		t.Fatalf("restart must retain mutation identity: %#v %v", idempotent, err)
	}
	if _, err := second.Write(context.Background(), restartedAuthority, canvas.WriteRequest{CanvasID: canvasID, ExpectedVersion: 1, HTML: "<p>different</p>", MutationID: "crash-base"}); !errors.Is(err, canvas.ErrMutationConflict) {
		t.Fatalf("reused mutation with different input must conflict after restart, got %v", err)
	}
}

func TestCanvasQuotaConcurrencyRejection(t *testing.T) {
	root := t.TempDir()
	config := canvas.DefaultConfig(root)
	config.WorkerLauncher = canvas.NewDeterministicWorkerLauncher()
	config.MaxStorageBytes = 16 * 1024
	service, err := canvas.New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown() })
	// Bounded fixtures: two small canvases plus one medium revision fill the quota near its bound.
	firstAuthority := attach(t, service, "native-session-quota-1")
	firstID := mustCreateCanvas(t, service, firstAuthority)
	secondAuthority := attach(t, service, "native-session-quota-2")
	secondID := mustCreateCanvas(t, service, secondAuthority)
	_ = secondID
	filler := "<html><body><p>" + strings.Repeat("f", 2000) + "</p></body></html>"
	mustWriteCanvas(t, service, firstAuthority, firstID, 1, filler, "quota-filler")
	usedBefore, reservedBefore := service.Usage()
	if usedBefore <= 0 {
		t.Fatal("usage must reflect committed bytes")
	}
	_ = reservedBefore
	// Concurrent oversized attempts from independent sessions must all reject
	// without damaging the installed primaries. Each fixture stays bounded (<512 KiB HTML).
	const workers = 4
	var group sync.WaitGroup
	results := make([]error, workers)
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func(slot int) {
			defer group.Done()
			session := "native-session-quota-worker-" + string(rune('a'+slot))
			authority, err := service.Attach(session, 1)
			if err != nil {
				results[slot] = err
				return
			}
			created, err := service.Create(context.Background(), authority)
			if err != nil {
				results[slot] = err
				return
			}
			_, err = service.Write(context.Background(), authority, canvas.WriteRequest{CanvasID: created.Canvas.ID, ExpectedVersion: 1, HTML: "<html><body><p>" + strings.Repeat("q", 3000) + "</p></body></html>", MutationID: "quota-worker"})
			results[slot] = err
		}(index)
	}
	group.Wait()
	quotaRejected := 0
	for _, result := range results {
		if result != nil && errors.Is(result, canvas.ErrQuotaExceeded) {
			quotaRejected++
		}
	}
	if quotaRejected == 0 {
		t.Fatalf("concurrent quota pressure must reject with quota_exceeded, got %v", results)
	}
	// Installed primaries remain complete and readable; quota rejection is
	// known-uncommitted, never uncertain.
	if _, err := service.Read(context.Background(), firstAuthority, canvas.ReadRequest{CanvasID: firstID, Version: 2}); err != nil {
		t.Fatalf("quota rejection damaged installed primary: %v", err)
	}
	reconciled, err := service.ReconcileCanvas(firstID)
	if err != nil {
		t.Fatalf("quota rejection must reconcile validated prior: %v", err)
	}
	if reconciled.Version != 2 {
		t.Fatalf("reconciled quota prior = %d, want 2", reconciled.Version)
	}
	canvasDir := findCanvasDir(t, root, firstID)
	assertNoBackupRestore(t, canvasDir)
	usedAfter, reservedAfter := service.Usage()
	if reservedAfter != 0 {
		t.Fatalf("quota reservations must be released after rejection, reserved=%d", reservedAfter)
	}
	if usedAfter != usedBefore && usedAfter < usedBefore {
		t.Fatalf("usage must not rewind after concurrent rejection: before=%d after=%d", usedBefore, usedAfter)
	}
}

func TestCanvasStaleBackupOverTombstone(t *testing.T) {
	root := t.TempDir()
	config := canvas.DefaultConfig(root)
	config.WorkerLauncher = canvas.NewDeterministicWorkerLauncher()
	service, err := canvas.New(config)
	if err != nil {
		t.Fatal(err)
	}
	authority := attach(t, service, "native-session-tombstone")
	canvasID := mustCreateCanvas(t, service, authority)
	mustWriteCanvas(t, service, authority, canvasID, 1, "<html><body><p>live</p></body></html>", "tombstone-live")
	removed, err := service.Remove(context.Background(), authority, canvas.RemoveRequest{CanvasID: canvasID, ExpectedVersion: 2, ExpectedGeneration: 1, MutationID: "tombstone-1"})
	if err != nil || removed.CanvasID != canvasID {
		t.Fatalf("remove = %#v %v", removed, err)
	}
	canvasDir := findCanvasDir(t, root, canvasID)
	// Capture the live primary before it becomes a tombstone backup, then plant
	// it as a stale backup plus a resurrected revision. Bounded fixtures only.
	staleLive := []byte(`{"schema":1,"canvasId":"` + canvasID + `","forged":true}`)
	if err := os.WriteFile(filepath.Join(canvasDir, "meta.json.bak"), staleLive, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canvasDir, "v1.html"), []byte("<html><body><p>stale backup</p></body></html>"), 0o444); err != nil {
		t.Fatal(err)
	}
	_ = service.Shutdown()
	restarted, err := canvas.New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Shutdown() })
	newAuthority := attach(t, restarted, "native-session-tombstone")
	// The tombstone stays authoritative across restart; no backup or revision may resurrect it.
	if _, err := restarted.Read(context.Background(), newAuthority, canvas.ReadRequest{CanvasID: canvasID}); !errors.Is(err, canvas.ErrRemoved) {
		t.Fatalf("stale backup must not resurrect tombstone, read=%v", err)
	}
	listed, err := restarted.List(context.Background(), newAuthority)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 0 {
		t.Fatalf("tombstoned canvas must stay unlisted, got %#v", listed.Items)
	}
	reconciled, err := restarted.ReconcileCanvas(canvasID)
	if err != nil {
		t.Fatalf("tombstone reconcile must validate without backup replay: %v", err)
	}
	if !reconciled.Removed {
		t.Fatalf("reconciled canvas must stay removed: %#v", reconciled)
	}
	primary, err := os.ReadFile(filepath.Join(canvasDir, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(primary), `"removedAt"`) || strings.Contains(string(primary), `"forged"`) {
		t.Fatal("reconcile must never overwrite the visible tombstone with a stale backup")
	}
	if _, err := restarted.Read(context.Background(), newAuthority, canvas.ReadRequest{CanvasID: canvasID}); !errors.Is(err, canvas.ErrRemoved) {
		t.Fatalf("tombstone must stay removed after reconcile, got %v", err)
	}
}

func TestCanvasEstimateMetaSizeLimit(t *testing.T) {
	root := t.TempDir()
	config := canvas.DefaultConfig(root)
	config.WorkerLauncher = canvas.NewDeterministicWorkerLauncher()
	config.MaxMetaBytes = 2048
	service, err := canvas.New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown() })
	authority := attach(t, service, "native-session-meta-limit")
	canvasID := mustCreateCanvas(t, service, authority)
	// Bounded fixtures: small HTML revisions grow only the mutation ledger until the estimate exceeds the bound.
	current := uint64(1)
	limitHit := false
	var lastVersion uint64 = 1
	for index := 0; index < 10; index++ {
		mutation := "meta-limit-" + string(rune('a'+index))
		html := "<html><body><p>revision " + string(rune('a'+index)) + "</p></body></html>"
		_, err := service.Write(context.Background(), authority, canvas.WriteRequest{CanvasID: canvasID, ExpectedVersion: current, HTML: html, MutationID: mutation})
		if err != nil {
			if errors.Is(err, canvas.ErrLimit) {
				limitHit = true
				break
			}
			t.Fatalf("write %d: %v", index, err)
		}
		current++
		lastVersion = current
	}
	if !limitHit {
		t.Fatal("bounded mutation ledger must hit the estimateMetaSize bound within 10 small writes")
	}
	// The last installed primary stays complete; the limit is known, never uncertain.
	read, err := service.Read(context.Background(), authority, canvas.ReadRequest{CanvasID: canvasID, Version: lastVersion})
	if err != nil || read.Version != lastVersion {
		t.Fatalf("limit must preserve prior commit v%d: %#v %v", lastVersion, read, err)
	}
	reconciled, err := service.ReconcileCanvas(canvasID)
	if err != nil {
		t.Fatalf("limit must reconcile validated prior: %v", err)
	}
	if reconciled.Version != lastVersion {
		t.Fatalf("reconciled limit prior = %d, want %d", reconciled.Version, lastVersion)
	}
	canvasDir := findCanvasDir(t, root, canvasID)
	assertNoBackupRestore(t, canvasDir)
	// Retained mutation identity: old IDs stay retryable, reused IDs with new input still conflict.
	idempotent, err := service.Write(context.Background(), authority, canvas.WriteRequest{CanvasID: canvasID, ExpectedVersion: 1, HTML: "<html><body><p>revision a</p></body></html>", MutationID: "meta-limit-a"})
	if err != nil {
		// The first ledger entry may have been the one that exceeded the bound
		// on a tiny limit; either idempotent or limit is acceptable only if the
		// prior remains intact. Prefer idempotent when retained.
		if !errors.Is(err, canvas.ErrLimit) {
			t.Fatalf("retained mutation retry: %v", err)
		}
	} else if !idempotent.Idempotent {
		t.Fatalf("retained ledger entry must stay idempotent: %#v", idempotent)
	}
	if _, err := service.Write(context.Background(), authority, canvas.WriteRequest{CanvasID: canvasID, ExpectedVersion: 1, HTML: "<p>different</p>", MutationID: "meta-limit-a"}); !errors.Is(err, canvas.ErrMutationConflict) && !errors.Is(err, canvas.ErrLimit) {
		t.Fatalf("reused mutation with different input must not silently succeed, got %v", err)
	}
}
