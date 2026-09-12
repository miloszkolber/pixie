package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRawWorktreeSnapshotHonorsPerFileHashLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large"), make([]byte, gitRawHashFileLimit+1), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot := rawWorktreeSnapshotFor(context.Background(), root, "large", "100644", newRawHashBudget())
	if !snapshot.exists || !snapshot.indeterminate || !snapshot.limit {
		t.Fatalf("oversized raw snapshot: %#v", snapshot)
	}
}

func TestRawWorktreeSnapshotHonorsAggregateHashLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}

	budget := newRawHashBudget()
	budget.remaining = int64(len("content")) - 1
	snapshot := rawWorktreeSnapshotFor(context.Background(), root, "file", "100644", budget)
	if !snapshot.exists || !snapshot.indeterminate || !snapshot.limit {
		t.Fatalf("aggregate-limited raw snapshot: %#v", snapshot)
	}
}

func TestRawHashBudgetHonorsAggregateLimit(t *testing.T) {
	budget := newRawHashBudget()
	for index := int64(0); index < gitRawHashAggregateLimit/gitRawHashFileLimit; index++ {
		if !budget.reserve(gitRawHashFileLimit) {
			t.Fatalf("hash budget rejected file %d before aggregate limit", index)
		}
	}
	if budget.reserve(1) {
		t.Fatal("hash budget accepted bytes beyond aggregate limit")
	}
}

func TestRawPreviewBudgetHonorsAggregateLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}

	budget := newRawHashBudget()
	budget.remaining = int64(len("content"))
	preview, limited, err := readWorktreePreviewWithBudget(root, "file", budget)
	if err != nil || !limited || preview.issue != "tooLarge" {
		t.Fatalf("aggregate-limited raw preview: %#v, %v, limited=%v", preview, err, limited)
	}
}

func TestGitRepositoryIdentityRejectsPathReplacement(t *testing.T) {
	root := t.TempDir()
	identity, err := captureGitRepositoryIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	replaced := root + "-old"
	if err := os.Rename(root, replaced); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := revalidateGitRepositoryIdentity(root, identity); err == nil {
		t.Fatal("accepted a replaced repository path")
	}
}
