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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/design"
)

// TestX09LargeIndexDedicatedPath stages a representative 16-64 MiB normalized
// index through the dedicated artifact path. It proves the generic 16 MiB
// JSON ceiling would reject the same bytes, slot metadata stays small, and
// queries remain paged/bounded with no full-index frame leak. Saved cover is
// not asserted as frame rendering (FIG-06 stays open).
func TestX09LargeIndexDedicatedPath(t *testing.T) {
	root := t.TempDir()
	service, err := design.New(design.Config{DataDir: root, Enabled: true, Parser: design.NewDeterministicParser()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown() })

	const nodeCount = 30000
	const textBody = 500
	pages := []design.Page{{ID: "page-1", Name: "Page 1"}}
	nodes := make([]design.Node, 0, nodeCount)
	for i := 0; i < nodeCount; i++ {
		id := "node-" + padSix(i)
		text := "body-" + padSix(i) + " " + strings.Repeat("x", textBody)
		nodes = append(nodes, design.Node{
			ID:       id,
			PageID:   "page-1",
			Position: i,
			Type:     "FRAME",
			Name:     "Node " + padSix(i),
			Visible:  true,
			Width:    10,
			Height:   10,
			Text:     text,
		})
	}
	designJSON, err := json.Marshal(map[string]any{"name": "Large representative", "pages": pages, "nodes": nodes})
	if err != nil {
		t.Fatal(err)
	}
	if len(designJSON) <= 16*1024*1024 {
		t.Fatalf("fixture setup did not exceed generic 16 MiB ceiling: %d bytes", len(designJSON))
	}
	source := zipentries(t, map[string][]byte{"canvas.fig": []byte("large-fixture"), "design.json": designJSON})
	if int64(len(source)) > design.MaxUploadBytes {
		t.Fatalf("synthetic source exceeds upload bound: %d", len(source))
	}
	result, err := service.Upload(context.Background(), design.UploadRequest{Name: "large.fig", Bytes: source, OperationID: "x09-large"})
	if err != nil {
		t.Fatal(err)
	}
	if result.DocumentID == "" || result.Generation == 0 {
		t.Fatalf("unexpected upload result: %#v", result)
	}

	storeRoot := filepath.Join(root, "mcp-design")
	slotPath := filepath.Join(storeRoot, "slot.json")
	indexPath := filepath.Join(storeRoot, "documents", result.DocumentID, "index.json")
	slotBytes, err := os.ReadFile(slotPath)
	if err != nil {
		t.Fatal(err)
	}
	indexInfo, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	indexBytes, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	// Dedicated artifact path accepts 16-64 MiB; generic 16 MiB ceiling would reject it.
	if len(indexBytes) <= 16*1024*1024 {
		t.Fatalf("normalized index did not exceed 16 MiB: %d", len(indexBytes))
	}
	if len(indexBytes) > 64*1024*1024 {
		t.Fatalf("normalized index exceeded dedicated 64 MiB artifact limit: %d", len(indexBytes))
	}
	if indexInfo.Size() != int64(len(indexBytes)) {
		t.Fatalf("index size mismatch: stat %d read %d", indexInfo.Size(), len(indexBytes))
	}
	if len(slotBytes) > 16*1024*1024 {
		t.Fatalf("slot metadata exceeded generic 16 MiB ceiling: %d", len(slotBytes))
	}
	if len(slotBytes) > 256*1024 {
		t.Fatalf("slot metadata unexpectedly large for commit-point proof: %d", len(slotBytes))
	}
	if bytes.Contains(slotBytes, []byte("node-000001")) {
		t.Fatal("slot metadata inlines full index content instead of referencing the dedicated artifact")
	}
	var slot map[string]any
	if err := json.Unmarshal(slotBytes, &slot); err != nil {
		t.Fatal(err)
	}
	indexSizeField, _ := slot["indexBytes"].(float64)
	if int64(indexSizeField) != int64(len(indexBytes)) {
		t.Fatalf("slot indexBytes %v does not match artifact %d", slot["indexBytes"], len(indexBytes))
	}
	if sha, _ := slot["indexSha256"].(string); sha == "" {
		t.Fatal("slot metadata does not reference the immutable index artifact hash")
	}
	// Generated path check: no worker-supplied path escapes the document directory.
	if !strings.HasPrefix(filepath.Clean(indexPath), filepath.Clean(storeRoot)) {
		t.Fatalf("index artifact path escapes store root: %q", indexPath)
	}
	info, err := os.Lstat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		t.Fatalf("index artifact is not a regular file: %v", info.Mode())
	}
	if info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("index artifact is writable: %v", info.Mode().Perm())
	}

	// No full-index frame leak: every read stays paged/bounded.
	status, err := service.Status(context.Background(), design.StatusRequest{DocumentID: result.DocumentID})
	if err != nil {
		t.Fatal(err)
	}
	statusJSON, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if len(statusJSON) > 256*1024 {
		t.Fatalf("status leaks unbounded content: %d bytes", len(statusJSON))
	}
	if strings.Contains(string(statusJSON), "node-000001") {
		t.Fatal("status response inlines full index nodes")
	}
	structure, err := service.Structure(context.Background(), design.StructureRequest{DocumentID: result.DocumentID, PageID: "page-1", Limit: 100, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(structure.Nodes) != 100 || !structure.Truncated || structure.NextCursor == "" {
		t.Fatalf("large-index structure not paged: nodes=%d truncated=%v cursor=%q", len(structure.Nodes), structure.Truncated, structure.NextCursor)
	}
	structureJSON, err := json.Marshal(structure)
	if err != nil {
		t.Fatal(err)
	}
	if len(structureJSON) > design.MaxQueryBytes {
		t.Fatalf("structure response exceeds 256 KiB query bound: %d", len(structureJSON))
	}
	follow, err := service.Structure(context.Background(), design.StructureRequest{DocumentID: result.DocumentID, PageID: "page-1", Limit: 100, Depth: 1, Cursor: structure.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(follow.Nodes) == 0 || follow.Nodes[0].ID == structure.Nodes[0].ID {
		t.Fatalf("cursor pagination did not advance: %#v", follow.Nodes)
	}
	text, err := service.Text(context.Background(), design.TextRequest{DocumentID: result.DocumentID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(text.Entries) == 0 || len(text.Entries) > 100 {
		t.Fatalf("large-index text not bounded: %d", len(text.Entries))
	}
	textJSON, err := json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(textJSON) > design.MaxQueryBytes {
		t.Fatalf("text response exceeds 256 KiB query bound: %d", len(textJSON))
	}
	// No cover was staged, so preview must fail closed without returning index bytes.
	if _, err := service.Preview(context.Background(), design.PreviewRequest{DocumentID: result.DocumentID, Kind: "cover"}); !errors.Is(err, design.ErrNotFound) {
		t.Fatalf("cover preview on coverless large index = %v, want not_found", err)
	}
}

// TestCursorPaginationTamperBinding proves stable cursors reject tampering,
// oversize values, kind/depth/page mismatches, and stay bounded.
func TestCursorPaginationTamperBinding(t *testing.T) {
	service, fixture := newCursorService(t, 150)
	uploaded, err := service.Upload(context.Background(), design.UploadRequest{Name: "cursor.fig", Bytes: fixture, OperationID: "cursor-1"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Structure(context.Background(), design.StructureRequest{DocumentID: uploaded.DocumentID, PageID: "page-1", Limit: 100, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Nodes) != 100 || first.NextCursor == "" || !first.Truncated {
		t.Fatalf("expected paged structure: %#v", first)
	}
	second, err := service.Structure(context.Background(), design.StructureRequest{DocumentID: uploaded.DocumentID, PageID: "page-1", Limit: 100, Depth: 1, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Nodes) != 50 || second.Truncated || second.NextCursor != "" {
		t.Fatalf("expected final page of 50: %#v", second)
	}
	if second.Nodes[0].ID == first.Nodes[0].ID {
		t.Fatal("cursor did not paginate")
	}
	// Tampered cursor must fail closed.
	tampered := first.NextCursor[:len(first.NextCursor)-1] + flipLast(first.NextCursor)
	if _, err := service.Structure(context.Background(), design.StructureRequest{DocumentID: uploaded.DocumentID, PageID: "page-1", Limit: 100, Depth: 1, Cursor: tampered}); !errors.Is(err, design.ErrInvalidRequest) {
		t.Fatalf("tampered cursor = %v, want invalid_request", err)
	}
	// Oversize cursor exceeds the 512-byte bound.
	if _, err := service.Structure(context.Background(), design.StructureRequest{DocumentID: uploaded.DocumentID, PageID: "page-1", Limit: 10, Depth: 1, Cursor: strings.Repeat("A", design.MaxCursorBytes+1)}); !errors.Is(err, design.ErrLimit) {
		t.Fatalf("oversize cursor = %v, want limit_exceeded", err)
	}
	// Malformed base64 fails closed.
	if _, err := service.Structure(context.Background(), design.StructureRequest{DocumentID: uploaded.DocumentID, PageID: "page-1", Limit: 10, Depth: 1, Cursor: "!!!not-base64!!!"}); !errors.Is(err, design.ErrInvalidRequest) {
		t.Fatalf("malformed cursor = %v, want invalid_request", err)
	}
	// Kind binding: a nodes cursor is not a text cursor.
	if _, err := service.Text(context.Background(), design.TextRequest{DocumentID: uploaded.DocumentID, Limit: 10, Cursor: first.NextCursor}); !errors.Is(err, design.ErrInvalidRequest) {
		t.Fatalf("cross-kind cursor = %v, want invalid_request", err)
	}
	// Depth binding: same cursor with a different depth fails.
	if _, err := service.Structure(context.Background(), design.StructureRequest{DocumentID: uploaded.DocumentID, PageID: "page-1", Limit: 10, Depth: 2, Cursor: first.NextCursor}); !errors.Is(err, design.ErrInvalidRequest) {
		t.Fatalf("depth-mismatched cursor = %v, want invalid_request", err)
	}
	// Text pagination also binds cursors.
	textFirst, err := service.Text(context.Background(), design.TextRequest{DocumentID: uploaded.DocumentID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(textFirst.Entries) != 100 || textFirst.NextCursor == "" {
		t.Fatalf("expected paged text: %#v", textFirst)
	}
	textSecond, err := service.Text(context.Background(), design.TextRequest{DocumentID: uploaded.DocumentID, Limit: 100, Cursor: textFirst.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(textSecond.Entries) == 0 {
		t.Fatalf("text cursor did not paginate: %#v", textSecond)
	}
}

// TestCursorCrossDocumentGeneration proves cursors never silently read a new
// upload: cross-document and new-generation cursors are rejected.
func TestCursorCrossDocumentGeneration(t *testing.T) {
	root := t.TempDir()
	config := design.DefaultConfig(root)
	config.Parser = design.NewDeterministicParser()
	service, err := design.New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown() })
	_, firstFixture := newCursorService(t, 120)
	first, err := service.Upload(context.Background(), design.UploadRequest{Name: "first.fig", Bytes: firstFixture, OperationID: "cross-1"})
	if err != nil {
		t.Fatal(err)
	}
	paged, err := service.Structure(context.Background(), design.StructureRequest{DocumentID: first.DocumentID, PageID: "page-1", Limit: 100, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if paged.NextCursor == "" {
		t.Fatal("expected a cursor from the first document")
	}
	oldCursor := paged.NextCursor
	oldID := first.DocumentID
	oldGeneration := first.Generation
	if _, err := service.Remove(context.Background(), design.RemoveRequest{DocumentID: first.DocumentID, ExpectedGeneration: first.Generation}); err != nil {
		t.Fatal(err)
	}
	_, secondFixture := newCursorService(t, 5)
	second, err := service.Upload(context.Background(), design.UploadRequest{Name: "second.fig", Bytes: secondFixture, OperationID: "cross-2"})
	if err != nil {
		t.Fatal(err)
	}
	if second.DocumentID == oldID || second.Generation == oldGeneration {
		t.Fatalf("replacement did not advance identity: %#v vs %s/%d", second, oldID, oldGeneration)
	}
	// Old document identity is stale after replacement.
	if _, err := service.Structure(context.Background(), design.StructureRequest{DocumentID: oldID, PageID: "page-1", Limit: 10}); !errors.Is(err, design.ErrStaleDocument) {
		t.Fatalf("old document read = %v, want stale_document", err)
	}
	// Old cursor bound to the previous document/generation cannot read the new one.
	if _, err := service.Structure(context.Background(), design.StructureRequest{DocumentID: second.DocumentID, PageID: "page-1", Limit: 10, Depth: 1, Cursor: oldCursor}); !errors.Is(err, design.ErrInvalidRequest) {
		t.Fatalf("cross-document cursor = %v, want invalid_request", err)
	}
	if _, err := service.Text(context.Background(), design.TextRequest{DocumentID: second.DocumentID, Limit: 10, Cursor: oldCursor}); !errors.Is(err, design.ErrInvalidRequest) {
		t.Fatalf("cross-document text cursor = %v, want invalid_request", err)
	}
}

// TestQueryTrimming256KiB proves oversized structure/text results trim to the
// 256 KiB query bound instead of leaking a full index or failing the document.
func TestQueryTrimming256KiB(t *testing.T) {
	service, err := design.New(design.Config{DataDir: t.TempDir(), Enabled: true, Parser: design.NewDeterministicParser()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown() })
	pages := []design.Page{{ID: "page-1", Name: "Page 1"}}
	nodes := make([]design.Node, 0, 80)
	for i := 0; i < 80; i++ {
		nodes = append(nodes, design.Node{
			ID:       "big-" + padSix(i),
			PageID:   "page-1",
			Position: i,
			Type:     "TEXT",
			Name:     "Big " + padSix(i),
			Visible:  true,
			Width:    100,
			Height:   20,
			Text:     "t-" + padSix(i) + "-" + strings.Repeat("y", 8*1024),
		})
	}
	source := zipentries(t, map[string][]byte{"canvas.fig": []byte("trim"), "design.json": mustJSON(t, map[string]any{"name": "Trim", "pages": pages, "nodes": nodes})})
	uploaded, err := service.Upload(context.Background(), design.UploadRequest{Name: "trim.fig", Bytes: source})
	if err != nil {
		t.Fatal(err)
	}
	structure, err := service.Structure(context.Background(), design.StructureRequest{DocumentID: uploaded.DocumentID, PageID: "page-1", Limit: 80, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !structure.Truncated {
		t.Fatalf("oversized structure was not marked truncated: nodes=%d", len(structure.Nodes))
	}
	if len(structure.Nodes) >= 80 {
		t.Fatalf("oversized structure was not trimmed: %d nodes", len(structure.Nodes))
	}
	encoded, err := json.Marshal(structure)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > design.MaxQueryBytes {
		t.Fatalf("trimmed structure still exceeds 256 KiB: %d", len(encoded))
	}
	text, err := service.Text(context.Background(), design.TextRequest{DocumentID: uploaded.DocumentID, Limit: 80})
	if err != nil {
		t.Fatal(err)
	}
	if !text.Truncated {
		t.Fatalf("oversized text was not marked truncated: entries=%d", len(text.Entries))
	}
	encoded, err = json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > design.MaxQueryBytes {
		t.Fatalf("trimmed text still exceeds 256 KiB: %d", len(encoded))
	}
}

// TestHugeTextImageHandling covers per-node text bounds and cover-image
// validation without building a renderer (FIG-06 stays open).
func TestHugeTextImageHandling(t *testing.T) {
	service, err := design.New(design.Config{DataDir: t.TempDir(), Enabled: true, Parser: design.NewDeterministicParser()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown() })

	// One 64 KiB direct-text node is the per-node maximum and must remain readable.
	maxText := strings.Repeat("h", 64*1024)
	maxSource := zipentries(t, map[string][]byte{"canvas.fig": []byte("max"), "design.json": mustJSON(t, map[string]any{
		"name":  "Max text",
		"pages": []design.Page{{ID: "page-1", Name: "Page 1"}},
		"nodes": []design.Node{{ID: "max-text", PageID: "page-1", Type: "TEXT", Name: "Max", Visible: true, Width: 10, Height: 10, Text: maxText}},
	})})
	maxed, err := service.Upload(context.Background(), design.UploadRequest{Name: "max.fig", Bytes: maxSource, OperationID: "huge-max"})
	if err != nil {
		t.Fatal(err)
	}
	node, err := service.Node(context.Background(), design.NodeRequest{DocumentID: maxed.DocumentID, NodeID: "max-text"})
	if err != nil {
		t.Fatal(err)
	}
	if node.Node.Text != maxText {
		t.Fatalf("max text node mismatch: %d vs %d", len(node.Node.Text), len(maxText))
	}
	if _, err := service.Remove(context.Background(), design.RemoveRequest{DocumentID: maxed.DocumentID, ExpectedGeneration: maxed.Generation}); err != nil {
		t.Fatal(err)
	}

	// Text above 64 KiB per node is rejected before the slot commits.
	tooBig := strings.Repeat("h", 64*1024+1)
	badSource := zipentries(t, map[string][]byte{"canvas.fig": []byte("bad"), "design.json": mustJSON(t, map[string]any{
		"name":  "Too big",
		"pages": []design.Page{{ID: "page-1", Name: "Page 1"}},
		"nodes": []design.Node{{ID: "too-big", PageID: "page-1", Type: "TEXT", Name: "TooBig", Visible: true, Text: tooBig}},
	})})
	if _, err := service.Upload(context.Background(), design.UploadRequest{Name: "toolarge.fig", Bytes: badSource, OperationID: "huge-too-big"}); !errors.Is(err, design.ErrInvalidWorkerResponse) && !errors.Is(err, design.ErrLimit) {
		t.Fatalf("oversized text upload = %v, want invalid_worker_response or limit", err)
	}
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Availability == "ready" && status.DocumentID != "" {
		t.Fatalf("failed huge-text upload published a document: %#v", status)
	}

	// An invalid saved cover is a warning/absent cover, not a failed document.
	invalidSource := zipentries(t, map[string][]byte{
		"canvas.fig":    []byte("cover"),
		"design.json":   mustJSON(t, map[string]any{"name": "Bad cover", "pages": []design.Page{{ID: "page-1", Name: "Page 1"}}, "nodes": []design.Node{{ID: "n1", PageID: "page-1", Type: "FRAME", Name: "N", Visible: true}}}),
		"thumbnail.png": []byte("not a png"),
	})
	invalid, err := service.Upload(context.Background(), design.UploadRequest{Name: "badcover.fig", Bytes: invalidSource, OperationID: "huge-bad-cover"})
	if err != nil {
		t.Fatal(err)
	}
	if invalid.CoverAvailable {
		t.Fatalf("invalid cover was advertised: %#v", invalid)
	}
	if _, err := service.Preview(context.Background(), design.PreviewRequest{DocumentID: invalid.DocumentID, Kind: "cover"}); !errors.Is(err, design.ErrNotFound) {
		t.Fatalf("invalid cover preview = %v, want not_found", err)
	}
	if _, err := service.Remove(context.Background(), design.RemoveRequest{DocumentID: invalid.DocumentID, ExpectedGeneration: invalid.Generation}); err != nil {
		t.Fatal(err)
	}

	// A cover exceeding the 2048 dimension bound is omitted the same way.
	wide := widePNG(t, 3000, 10)
	wideSource := zipentries(t, map[string][]byte{
		"canvas.fig":    []byte("wide"),
		"design.json":   mustJSON(t, map[string]any{"name": "Wide cover", "pages": []design.Page{{ID: "page-1", Name: "Page 1"}}, "nodes": []design.Node{{ID: "n1", PageID: "page-1", Type: "FRAME", Name: "N", Visible: true}}}),
		"thumbnail.png": wide,
	})
	wideResult, err := service.Upload(context.Background(), design.UploadRequest{Name: "widecover.fig", Bytes: wideSource, OperationID: "huge-wide-cover"})
	if err != nil {
		t.Fatal(err)
	}
	if wideResult.CoverAvailable {
		t.Fatalf("oversize-dimension cover was advertised: %#v", wideResult)
	}
}

// TestParserMissingReadiness is the Design counterpart to Canvas unavailable:
// no parser means uploads fail closed while health/status stay available.
func TestParserMissingReadiness(t *testing.T) {
	service, err := design.New(design.Config{DataDir: t.TempDir(), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown() })
	if !service.Ready() {
		t.Fatal("parser-missing service should be healthy-but-unavailable, not corrupt")
	}
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || status.Availability != "empty" || status.DocumentID != "" {
		t.Fatalf("unexpected parser-missing status: %#v", status)
	}
	_, fixture := newCursorService(t, 2)
	if _, err := service.Upload(context.Background(), design.UploadRequest{Name: "noparser.fig", Bytes: fixture}); !errors.Is(err, design.ErrUnavailable) {
		t.Fatalf("parser-missing upload = %v, want unavailable", err)
	}
	if !service.Ready() {
		t.Fatal("failed upload marked a healthy service corrupt")
	}
	after, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.DocumentID != "" || after.Availability != "empty" {
		t.Fatalf("failed parser-missing upload changed state: %#v", after)
	}
}

// TestRemovalWhileDisabledWithoutParser proves removal works while disabled
// and without invoking the parser, using a retained source after restart.
func TestRemovalWhileDisabledWithoutParser(t *testing.T) {
	root := t.TempDir()
	firstConfig := design.DefaultConfig(root)
	firstConfig.Parser = design.NewDeterministicParser()
	first, err := design.New(firstConfig)
	if err != nil {
		t.Fatal(err)
	}
	_, fixture := newCursorService(t, 2)
	uploaded, err := first.Upload(context.Background(), design.UploadRequest{Name: "retained.fig", Bytes: fixture})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Shutdown(); err != nil {
		t.Fatal(err)
	}
	// Restart without a parser: retained index stays readable, parser stays absent.
	restarted, err := design.New(design.Config{DataDir: root, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Shutdown() })
	status, err := restarted.Status(context.Background(), design.StatusRequest{DocumentID: uploaded.DocumentID})
	if err != nil || status.Availability != "ready" {
		t.Fatalf("retained status without parser: %#v %v", status, err)
	}
	if _, err := restarted.Structure(context.Background(), design.StructureRequest{DocumentID: uploaded.DocumentID, PageID: "page-1"}); err != nil {
		t.Fatalf("retained query without parser: %v", err)
	}
	restarted.Disable()
	removed, err := restarted.Remove(context.Background(), design.RemoveRequest{DocumentID: uploaded.DocumentID, ExpectedGeneration: uploaded.Generation})
	if err != nil {
		t.Fatalf("disabled removal without parser: %v", err)
	}
	if removed.DocumentID != uploaded.DocumentID || removed.CleanupPending {
		t.Fatalf("unexpected disabled removal: %#v", removed)
	}
	after, err := restarted.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.Availability != "removed" || after.DocumentID != uploaded.DocumentID {
		t.Fatalf("tombstone after disabled removal: %#v", after)
	}
	// While disabled, reads fail disabled (admission closed) rather than
	// leaking the tombstoned content; after re-enable they fail removed.
	if _, err := restarted.Structure(context.Background(), design.StructureRequest{DocumentID: uploaded.DocumentID}); !errors.Is(err, design.ErrDisabled) {
		t.Fatalf("disabled read after removal = %v, want disabled", err)
	}
	restarted.Enable()
	if _, err := restarted.Structure(context.Background(), design.StructureRequest{DocumentID: uploaded.DocumentID}); !errors.Is(err, design.ErrRemoved) {
		t.Fatalf("removed document readable after disabled removal: %v", err)
	}
}

// TestDisabledManagementBehavior checks service-level disabled semantics:
// uploads/reads/focus fail disabled, status reports disabled, removal still works.
func TestDisabledManagementBehavior(t *testing.T) {
	t.Run("default disabled", func(t *testing.T) {
		service, err := design.New(design.Config{DataDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = service.Shutdown() })
		if !service.Ready() {
			t.Fatal("default-disabled service should be healthy")
		}
		_, fixture := newCursorService(t, 2)
		if _, err := service.Upload(context.Background(), design.UploadRequest{Name: "default.fig", Bytes: fixture}); !errors.Is(err, design.ErrDisabled) {
			t.Fatalf("default-disabled upload = %v, want disabled", err)
		}
		status, err := service.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if status.Enabled || status.Availability != "disabled" {
			t.Fatalf("default-disabled status: %#v", status)
		}
	})
	t.Run("disable retains source and revokes reads", func(t *testing.T) {
		service, err := design.New(design.Config{DataDir: t.TempDir(), Enabled: true, Parser: design.NewDeterministicParser()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = service.Shutdown() })
		_, fixture := newCursorService(t, 2)
		uploaded, err := service.Upload(context.Background(), design.UploadRequest{Name: "managed.fig", Bytes: fixture})
		if err != nil {
			t.Fatal(err)
		}
		service.Disable()
		status, err := service.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if status.Enabled || status.Availability != "disabled" || status.DocumentID != uploaded.DocumentID {
			t.Fatalf("disabled status with retained source: %#v", status)
		}
		if _, err := service.Upload(context.Background(), design.UploadRequest{Name: "other.fig", Bytes: fixture}); !errors.Is(err, design.ErrDisabled) {
			t.Fatalf("disabled upload = %v, want disabled", err)
		}
		if _, err := service.Structure(context.Background(), design.StructureRequest{DocumentID: uploaded.DocumentID, PageID: "page-1"}); !errors.Is(err, design.ErrDisabled) {
			t.Fatalf("disabled structure = %v, want disabled", err)
		}
		if _, err := service.Text(context.Background(), design.TextRequest{DocumentID: uploaded.DocumentID}); !errors.Is(err, design.ErrDisabled) {
			t.Fatalf("disabled text = %v, want disabled", err)
		}
		if _, err := service.Node(context.Background(), design.NodeRequest{DocumentID: uploaded.DocumentID, NodeID: "frame-1"}); !errors.Is(err, design.ErrDisabled) {
			t.Fatalf("disabled node = %v, want disabled", err)
		}
		if _, err := service.Preview(context.Background(), design.PreviewRequest{DocumentID: uploaded.DocumentID, Kind: "cover"}); !errors.Is(err, design.ErrDisabled) {
			t.Fatalf("disabled preview = %v, want disabled", err)
		}
		if _, err := service.SetSelection(context.Background(), design.SetSelectionRequest{DocumentID: uploaded.DocumentID, ExpectedGeneration: uploaded.Generation}); !errors.Is(err, design.ErrDisabled) {
			t.Fatalf("disabled focus = %v, want disabled", err)
		}
		// Management removal remains available while disabled.
		removed, err := service.Remove(context.Background(), design.RemoveRequest{DocumentID: uploaded.DocumentID, ExpectedGeneration: uploaded.Generation})
		if err != nil {
			t.Fatalf("disabled removal = %v", err)
		}
		if removed.DocumentID != uploaded.DocumentID {
			t.Fatalf("disabled removal identity: %#v", removed)
		}
		service.Enable()
		enabledStatus, err := service.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !enabledStatus.Enabled || enabledStatus.Availability != "removed" {
			t.Fatalf("status after re-enable following removal: %#v", enabledStatus)
		}
	})
}

func newCursorService(t *testing.T, count int) (*design.Service, []byte) {
	t.Helper()
	service, err := design.New(design.Config{DataDir: t.TempDir(), Enabled: true, Parser: design.NewDeterministicParser()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown() })
	pages := []design.Page{{ID: "page-1", Name: "Page 1"}}
	nodes := make([]design.Node, 0, count)
	for i := 0; i < count; i++ {
		id := "node-" + padSix(i)
		if count <= 10 {
			id = []string{"frame-1", "text-1", "node-3", "node-4", "node-5", "node-6", "node-7", "node-8", "node-9", "node-10"}[i]
		}
		text := "text " + padSix(i)
		if count <= 10 && i == 0 {
			text = ""
		}
		parent := ""
		if i > 0 && count <= 10 && i == 1 {
			parent = "frame-1"
		}
		nodes = append(nodes, design.Node{ID: id, PageID: "page-1", ParentID: parent, Position: i, Type: "FRAME", Name: "Node " + padSix(i), Visible: true, Width: 10, Height: 10, Text: text})
	}
	// Keep the shared small fixture shape stable for tests that expect frame-1/text-1.
	if count == 2 {
		nodes = []design.Node{
			{ID: "frame-1", PageID: "page-1", Position: 0, Type: "FRAME", Name: "Frame", Visible: true, Width: 120, Height: 80},
			{ID: "text-1", PageID: "page-1", ParentID: "frame-1", Position: 0, Type: "TEXT", Name: "Greeting", Text: "Hello Design", Visible: true, Width: 80, Height: 20},
		}
	}
	source := zipentries(t, map[string][]byte{"canvas.fig": []byte("cursor"), "design.json": mustJSON(t, map[string]any{"name": "Cursor", "pages": pages, "nodes": nodes})})
	return service, source
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func zipentries(t *testing.T, entries map[string][]byte) []byte {
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

func padSix(i int) string {
	digits := "000000" + itoa(i)
	return digits[len(digits)-6:]
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var out [20]byte
	pos := len(out)
	for i > 0 {
		pos--
		out[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(out[pos:])
}

func flipLast(s string) string {
	if s == "" {
		return "A"
	}
	last := s[len(s)-1]
	if last == 'A' {
		return "B"
	}
	return "A"
}

func widePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			picture.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, picture); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
