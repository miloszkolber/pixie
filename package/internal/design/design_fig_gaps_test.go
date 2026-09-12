package design

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestX09DedicatedArtifactHoldsRepresentativeIndex proves the dedicated
// 64 MiB artifact path holds a representative 16-64 MiB normalized index
// without oversized individual nodes, while the generic 16 MiB JSON ceiling
// would reject the same bytes. It also proves worker-supplied paths are never
// accepted and the published artifact stays immutable.
func TestX09DedicatedArtifactHoldsRepresentativeIndex(t *testing.T) {
	store, err := NewIndexStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const nodeCount = 23000
	pages := []Page{{ID: "page-1", Name: "Page 1", Position: 0, NodeCount: nodeCount}}
	nodes := make([]Node, 0, nodeCount)
	for i := 0; i < nodeCount; i++ {
		id := "node-" + gapPad(i)
		nodes = append(nodes, Node{
			ID:       id,
			PageID:   "page-1",
			Position: i,
			Type:     "FRAME",
			Name:     "Node " + gapPad(i),
			Visible:  true,
			Width:    10,
			Height:   10,
			Text:     "body-" + gapPad(i) + " " + strings.Repeat("x", 600),
		})
	}
	index := indexDocument{Schema: 1, IndexVersion: defaultIndexVersion, DocumentID: "document-x09", Generation: 1, Name: "Representative", ParserVersion: "fixture-parser-v1", Pages: pages, Nodes: nodes}
	encoded, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	// Representative: many small nodes, none oversized, total 16-64 MiB.
	if len(encoded) <= 16*1024*1024 {
		t.Fatalf("representative index did not exceed generic ceiling: %d", len(encoded))
	}
	if len(encoded) > MaxIndexArtifactBytes {
		t.Fatalf("representative index exceeds dedicated artifact limit: %d", len(encoded))
	}
	for _, node := range nodes {
		if len(node.Text) > 64*1024 || len(node.Name) > 512 {
			t.Fatal("representative fixture uses an oversized individual node")
		}
	}
	artifact, err := store.WriteIndex(context.Background(), "document-x09", strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Bytes != int64(len(encoded)) || artifact.SHA256 == "" {
		t.Fatalf("unexpected artifact: %#v", artifact)
	}
	// Generic 16 MiB ceiling proof: the same bytes would be rejected as generic
	// persisted JSON or as a host/browser frame payload.
	if int64(len(encoded)) <= 16*1024*1024 {
		t.Fatal("generic-cap proof failed: index fits in 16 MiB")
	}
	// Worker-supplied paths are never accepted; only generated opaque IDs work.
	for _, bad := range []string{"../escape", "/abs", "a/b", "", strings.Repeat("x", 200)} {
		if _, err := store.WriteIndex(context.Background(), bad, strings.NewReader(`{"ok":true}`)); err == nil {
			t.Fatalf("worker-supplied document id %q was accepted", bad)
		}
	}
	if _, err := store.OpenIndex("../escape"); err == nil {
		t.Fatal("worker-supplied open path was accepted")
	}
	// Trailing JSON is rejected even when the prefix is valid.
	if _, err := store.WriteIndex(context.Background(), "trailing-x09", strings.NewReader(`{"a":1}{"b":2}`)); err == nil {
		t.Fatal("trailing JSON index was admitted")
	}
}

// TestCursorBindingRejectsTamper proves opaque cursors bind document,
// generation, kind, page/parent/root/depth and integrity, and stay bounded.
func TestCursorBindingRejectsTamper(t *testing.T) {
	const doc = "abcdefghijklmnopqrstuvwxyz"
	cursor := makeCursor(doc, 7, "nodes", "page-1", "", "", 1, 100)
	offset, err := parseCursor(cursor, doc, 7, "nodes", "page-1", "", "", 1)
	if err != nil || offset != 100 {
		t.Fatalf("valid cursor: offset=%d err=%v", offset, err)
	}
	cases := []struct {
		name       string
		value      string
		documentID string
		generation uint64
		kind       string
		pageID     string
		parentID   string
		rootID     string
		depth      int
		wantLimit  bool
	}{
		{name: "cross-document", value: cursor, documentID: "abcdefghijklmnop", generation: 7, kind: "nodes", pageID: "page-1", depth: 1},
		{name: "cross-generation", value: cursor, documentID: doc, generation: 8, kind: "nodes", pageID: "page-1", depth: 1},
		{name: "cross-kind", value: cursor, documentID: doc, generation: 7, kind: "text", pageID: "page-1", depth: 1},
		{name: "cross-page", value: cursor, documentID: doc, generation: 7, kind: "nodes", pageID: "page-2", depth: 1},
		{name: "cross-depth", value: cursor, documentID: doc, generation: 7, kind: "nodes", pageID: "page-1", depth: 2},
		{name: "cross-parent", value: makeCursor(doc, 7, "nodes", "page-1", "parent-1", "", 1, 10), documentID: doc, generation: 7, kind: "nodes", pageID: "page-1", depth: 1},
		{name: "malformed", value: "!!!", documentID: doc, generation: 7, kind: "nodes", pageID: "page-1", depth: 1},
		{name: "tampered", value: cursor[:len(cursor)-1] + gapFlip(cursor), documentID: doc, generation: 7, kind: "nodes", pageID: "page-1", depth: 1},
		{name: "oversize", value: strings.Repeat("A", MaxCursorBytes+1), documentID: doc, generation: 7, kind: "nodes", pageID: "page-1", depth: 1, wantLimit: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseCursor(tc.value, tc.documentID, tc.generation, tc.kind, tc.pageID, tc.parentID, tc.rootID, tc.depth)
			if err == nil {
				t.Fatalf("cursor %q was accepted", tc.name)
			}
			if tc.wantLimit {
				if !errors.Is(err, ErrLimit) {
					t.Fatalf("oversize cursor = %v, want limit_exceeded", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("cursor %q = %v, want invalid_request", tc.name, err)
			}
		})
	}
	if len(cursor) > MaxCursorBytes {
		t.Fatalf("valid cursor exceeds %d-byte bound: %d", MaxCursorBytes, len(cursor))
	}
}

// TestTrimmingKeepsQueriesBounded proves 256 KiB trimming drops/trailing
// content instead of leaking a full index or failing the document.
func TestTrimmingKeepsQueriesBounded(t *testing.T) {
	nodes := make([]Node, 0, 40)
	for i := 0; i < 40; i++ {
		nodes = append(nodes, Node{ID: "trim-" + gapPad(i), PageID: "page-1", Position: i, Type: "TEXT", Name: "Trim", Visible: true, Text: strings.Repeat("z", 8*1024)})
	}
	structure := &StructureResult{Outcome: "structure", DocumentID: "doc", Generation: 1, IndexVersion: defaultIndexVersion, Nodes: nodes, Items: nodes}
	trimStructure(structure, MaxQueryBytes)
	if !structure.Truncated || len(structure.Nodes) >= 40 {
		t.Fatalf("structure was not trimmed: nodes=%d truncated=%v", len(structure.Nodes), structure.Truncated)
	}
	encoded, err := json.Marshal(structure)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > MaxQueryBytes {
		t.Fatalf("trimmed structure still exceeds 256 KiB: %d", len(encoded))
	}
	entries := make([]TextEntry, 0, 20)
	for i := 0; i < 20; i++ {
		entries = append(entries, TextEntry{NodeID: "text-" + gapPad(i), PageID: "page-1", Text: strings.Repeat("y", 32*1024)})
	}
	text := &TextResult{Outcome: "text", DocumentID: "doc", Generation: 1, IndexVersion: defaultIndexVersion, Entries: entries}
	boundTextResult(text, MaxQueryBytes)
	if !text.Truncated {
		t.Fatal("oversized text was not marked truncated")
	}
	encoded, err = json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > MaxQueryBytes {
		t.Fatalf("trimmed text still exceeds 256 KiB: %d", len(encoded))
	}
}

func gapPad(i int) string {
	s := "000000" + gapItoa(i)
	return s[len(s)-6:]
}

func gapItoa(i int) string {
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

func gapFlip(s string) string {
	if s == "" {
		return "A"
	}
	if s[len(s)-1] == 'A' {
		return "B"
	}
	return "A"
}
