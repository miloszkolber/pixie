package design_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/design"
)

func TestStructureUploadQueryPNGAndHTTPArtifact(t *testing.T) {
	service, fixture := newService(t)
	if service.MCPHandler() == nil {
		t.Fatal("MCP handler is nil")
	}
	result, err := service.Upload(context.Background(), design.UploadRequest{Name: "fixture.fig", Bytes: fixture, OperationID: "upload-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.DocumentID == "" || result.Generation != 1 || !result.CoverAvailable {
		t.Fatalf("unexpected upload result: %#v", result)
	}
	status, err := service.Status(context.Background(), design.StatusRequest{DocumentID: result.DocumentID, ExpectedGeneration: result.Generation})
	if err != nil {
		t.Fatal(err)
	}
	if status.Availability != "ready" || status.SelectionRevision != 1 {
		t.Fatalf("unexpected status: %#v", status)
	}
	selection, err := service.SetSelection(context.Background(), design.SetSelectionRequest{DocumentID: result.DocumentID, ExpectedGeneration: result.Generation, ExpectedRevision: status.SelectionRevision, PageID: "page-1", NodeID: "frame-1"})
	if err != nil || selection.Selection.Revision != 2 {
		t.Fatalf("selection CAS: %#v %v", selection, err)
	}
	if _, err := service.Structure(context.Background(), design.StructureRequest{DocumentID: result.DocumentID, ExpectedRevision: status.SelectionRevision, PageID: "page-1"}); !errors.Is(err, design.ErrStaleSelection) {
		t.Fatalf("stale selection was accepted: %v", err)
	}
	structure, err := service.Structure(context.Background(), design.StructureRequest{DocumentID: result.DocumentID, ExpectedGeneration: result.Generation, ExpectedRevision: selection.Selection.Revision, PageID: "page-1", Limit: 10, Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(structure.Nodes) != 2 || structure.Nodes[0].ID != "frame-1" || structure.Nodes[1].ID != "text-1" {
		t.Fatalf("unexpected structure: %#v", structure)
	}
	text, err := service.Text(context.Background(), design.TextRequest{DocumentID: result.DocumentID, PageID: "page-1", Limit: 10})
	if err != nil || len(text.Entries) != 1 || text.Entries[0].Text != "Hello Design" {
		t.Fatalf("unexpected text: %#v %v", text, err)
	}
	preview, err := service.Preview(context.Background(), design.PreviewRequest{DocumentID: result.DocumentID, Kind: "cover"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.MIME != "image/png" || len(preview.PNG) == 0 || !bytes.Equal(preview.PNG[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		t.Fatalf("preview is not an actual PNG: %#v", preview)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/design/artifacts/"+result.DocumentID+"/thumbnail.png", nil)
	recorder := httptest.NewRecorder()
	service.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "image/png" || recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("artifact response: %d %#v %q", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	if _, err := png.Decode(bytes.NewReader(recorder.Body.Bytes())); err != nil {
		t.Fatalf("artifact did not decode as PNG: %v", err)
	}
}

func TestCASRemovalTombstoneAndRestartGeneration(t *testing.T) {
	root := t.TempDir()
	fixture := fixtureArchive(t)
	config := design.DefaultConfig(root)
	config.Parser = design.NewDeterministicParser()
	service, err := design.New(config)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Upload(context.Background(), design.UploadRequest{Name: "one.fig", Bytes: fixture, OperationID: "same"})
	if err != nil {
		t.Fatal(err)
	}
	retry, err := service.Upload(context.Background(), design.UploadRequest{Name: "one.fig", Bytes: fixture, OperationID: "same"})
	if err != nil || !retry.Idempotent || retry.DocumentID != first.DocumentID {
		t.Fatalf("idempotent retry: %#v %v", retry, err)
	}
	if _, err := service.Upload(context.Background(), design.UploadRequest{Name: "two.fig", Bytes: fixture}); !errors.Is(err, design.ErrConflict) {
		t.Fatalf("replacement did not conflict: %v", err)
	}
	if _, err := service.Remove(context.Background(), design.RemoveRequest{DocumentID: first.DocumentID, ExpectedGeneration: first.Generation + 1}); !errors.Is(err, design.ErrStaleDocument) {
		t.Fatalf("stale remove: %v", err)
	}
	removed, err := service.Remove(context.Background(), design.RemoveRequest{DocumentID: first.DocumentID, ExpectedGeneration: first.Generation, SelectionRevision: first.SelectionRevision})
	if err != nil || removed.CleanupPending {
		t.Fatalf("remove: %#v %v", removed, err)
	}
	if _, err := service.Structure(context.Background(), design.StructureRequest{DocumentID: first.DocumentID}); !errors.Is(err, design.ErrRemoved) {
		t.Fatalf("removed document remained readable: %v", err)
	}
	if err := service.Shutdown(); err != nil {
		t.Fatal(err)
	}
	restarted, err := design.New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Shutdown()
	status, err := restarted.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Availability != "removed" || status.Generation != first.Generation {
		t.Fatalf("tombstone was not recovered: %#v", status)
	}
	second, err := restarted.Upload(context.Background(), design.UploadRequest{Name: "two.fig", Bytes: fixture})
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation != first.Generation+1 || second.DocumentID == first.DocumentID {
		t.Fatalf("generation/id did not advance: %#v", second)
	}
}

func TestRestartRetainsCommittedActiveIndexWithoutWorker(t *testing.T) {
	root := t.TempDir()
	fixture := fixtureArchive(t)
	config := design.DefaultConfig(root)
	config.Parser = design.NewDeterministicParser()
	first, err := design.New(config)
	if err != nil {
		t.Fatal(err)
	}
	uploaded, err := first.Upload(context.Background(), design.UploadRequest{Name: "retained.fig", Bytes: fixture})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Shutdown(); err != nil {
		t.Fatal(err)
	}
	config.Parser = nil
	restarted, err := design.New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Shutdown()
	status, err := restarted.Status(context.Background(), design.StatusRequest{DocumentID: uploaded.DocumentID, ExpectedGeneration: uploaded.Generation})
	if err != nil || status.Availability != "ready" {
		t.Fatalf("retained status: %#v %v", status, err)
	}
	structure, err := restarted.Structure(context.Background(), design.StructureRequest{DocumentID: uploaded.DocumentID, PageID: "page-1", Depth: 2})
	if err != nil || len(structure.Nodes) != 2 {
		t.Fatalf("retained index query: %#v %v", structure, err)
	}
}

func TestPreflightRejectsArchiveTraversalCollisionAndExpansion(t *testing.T) {
	traversal := zipArchive(t, map[string][]byte{"canvas.fig": []byte("ok"), "../escape": []byte("bad")})
	if _, err := design.Preflight(traversal); !errors.Is(err, design.ErrInvalidRequest) {
		t.Fatalf("traversal accepted: %v", err)
	}
	collision := zipArchive(t, map[string][]byte{"canvas.fig": []byte("one"), "assets/canvas.fig": []byte("two")})
	if _, err := design.Preflight(collision); !errors.Is(err, design.ErrInvalidRequest) {
		t.Fatalf("basename collision accepted: %v", err)
	}
	tooMany := make(map[string][]byte, design.MaxArchiveEntries+1)
	tooMany["canvas.fig"] = []byte("required")
	for index := 0; index < design.MaxArchiveEntries; index++ {
		tooMany["n/entry-"+strconv.Itoa(index)] = []byte("x")
	}
	if _, err := design.Preflight(zipArchive(t, tooMany)); !errors.Is(err, design.ErrLimit) {
		t.Fatalf("entry bound accepted: %v", err)
	}
}

func TestQueryBoundsAndWorkerFailClosed(t *testing.T) {
	root := t.TempDir()
	service, fixture := newService(t)
	if _, err := service.Structure(context.Background(), design.StructureRequest{DocumentID: "missing-document"}); !errors.Is(err, design.ErrInvalidRequest) {
		t.Fatalf("missing document did not fail closed: %v", err)
	}
	if _, err := service.Upload(context.Background(), design.UploadRequest{Name: "too.fig", Bytes: fixture}); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Structure(context.Background(), design.StructureRequest{DocumentID: status.DocumentID, Limit: design.MaxQueryNodes + 1}); !errors.Is(err, design.ErrLimit) {
		t.Fatalf("query node bound accepted: %v", err)
	}
	if _, err := service.Structure(context.Background(), design.StructureRequest{DocumentID: status.DocumentID, PageID: "page-1", Depth: design.MaxGraphDepth + 1}); !errors.Is(err, design.ErrLimit) {
		t.Fatalf("depth bound accepted: %v", err)
	}
	if err := service.Shutdown(); err != nil {
		t.Fatal(err)
	}
	noWorker, err := design.New(design.Config{DataDir: root, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer noWorker.Shutdown()
	if _, err := noWorker.Upload(context.Background(), design.UploadRequest{Name: "none.fig", Bytes: fixture}); !errors.Is(err, design.ErrUnavailable) {
		t.Fatalf("missing worker did not fail closed: %v", err)
	}
}

func TestDisableCancelsInFlightParserWithoutPublishing(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	parser := design.ParserFunc(func(ctx context.Context, _ []byte) (design.NormalizedDocument, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return design.NormalizedDocument{}, ctx.Err()
	})
	config := design.DefaultConfig(t.TempDir())
	config.Parser = parser
	service, err := design.New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Shutdown()
	result := make(chan error, 1)
	go func() {
		_, uploadErr := service.Upload(context.Background(), design.UploadRequest{Name: "blocked.fig", Bytes: fixtureArchive(t)})
		result <- uploadErr
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("parser did not start")
	}
	service.Disable()
	select {
	case uploadErr := <-result:
		if !errors.Is(uploadErr, context.Canceled) {
			t.Fatalf("cancelled upload = %v, want context cancellation", uploadErr)
		}
	case <-time.After(time.Second):
		t.Fatal("parser did not cancel after disable")
	}
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Availability != "disabled" || status.DocumentID != "" {
		t.Fatalf("disabled status after cancelled upload = %#v", status)
	}
}

func newService(t *testing.T) (*design.Service, []byte) {
	t.Helper()
	service, err := design.New(design.Config{DataDir: t.TempDir(), Enabled: true, Parser: design.NewDeterministicParser()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown() })
	return service, fixtureArchive(t)
}

func fixtureArchive(t *testing.T) []byte {
	t.Helper()
	cover := pngBytes(t)
	designJSON, err := json.Marshal(map[string]any{
		"name":  "Offline fixture",
		"pages": []design.Page{{ID: "page-1", Name: "Page 1"}},
		"nodes": []design.Node{
			{ID: "frame-1", PageID: "page-1", Type: "FRAME", Name: "Frame", Visible: true, Width: 120, Height: 80},
			{ID: "text-1", PageID: "page-1", ParentID: "frame-1", Type: "TEXT", Name: "Greeting", Text: "Hello Design", Visible: true, Width: 80, Height: 20},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return zipArchive(t, map[string][]byte{"canvas.fig": []byte("fixture"), "design.json": designJSON, "thumbnail.png": cover})
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	picture := image.NewRGBA(image.Rect(0, 0, 2, 2))
	picture.Set(0, 0, color.RGBA{R: 255, A: 255})
	picture.Set(1, 1, color.RGBA{G: 255, A: 255})
	if err := png.Encode(&output, picture); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func zipArchive(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for name, content := range entries {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestFixtureParserRejectsOpaqueArchiveWithoutDesignJSON(t *testing.T) {
	service, _ := newService(t)
	opaque := zipArchive(t, map[string][]byte{"canvas.fig": []byte("not-a-fixture")})
	if _, err := service.Upload(context.Background(), design.UploadRequest{Name: "opaque.fig", Bytes: opaque}); !errors.Is(err, design.ErrInvalidRequest) {
		t.Fatalf("opaque archive error = %v, want ErrInvalidRequest", err)
	}
}
