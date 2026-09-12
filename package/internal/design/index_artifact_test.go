package design

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteIndexUsesDedicatedBoundedImmutablePath(t *testing.T) {
	root := t.TempDir()
	store, err := NewIndexStore(root)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"version":1,"nodes":[]}`
	artifact, err := store.WriteIndex(context.Background(), "document-1", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(root, "documents", "document-1", "index.json")
	if artifact.Path != wantPath || artifact.Bytes != int64(len(body)) || artifact.SHA256 == "" {
		t.Fatalf("unexpected artifact: %#v", artifact)
	}
	if _, err := os.Stat(filepath.Join(root, "staging")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("index is writable: %v", info.Mode().Perm())
	}
	file, err := store.OpenIndex("document-1")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := os.ReadFile(artifact.Path)
	if err != nil || !bytes.Equal(data, []byte(body)) {
		t.Fatalf("published index mismatch: %v", err)
	}
	if _, err := store.WriteIndex(context.Background(), "document-1", strings.NewReader(body)); err == nil {
		t.Fatal("immutable index was replaced")
	}
}

func TestWriteIndexRejectsOversizeAndMalformedOutput(t *testing.T) {
	store, err := NewIndexStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteIndex(context.Background(), "too-large", &ioLimitReader{remaining: MaxIndexArtifactBytes + 1}); err == nil {
		t.Fatal("oversize index was admitted")
	}
	if _, err := store.WriteIndex(context.Background(), "bad", strings.NewReader(`{"nodes":`)); err == nil {
		t.Fatal("malformed index was admitted")
	}
}

type ioLimitReader struct{ remaining int }

func (r *ioLimitReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, os.ErrClosed
	}
	read := len(p)
	if read > r.remaining {
		read = r.remaining
	}
	for i := 0; i < read; i++ {
		p[i] = 'x'
	}
	r.remaining -= read
	return read, nil
}
