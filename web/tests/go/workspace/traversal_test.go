package workspace_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

// A canary file outside the admitted mount must never be reachable through a
// symlink placed inside the mount, and the denial must be the typed traversal
// error so an HTTP surface can map it to 403.
func TestResolveRealPathDeniesCanarySymlinkOutsideMount(t *testing.T) {
	mount := t.TempDir()
	root := filepath.Join(mount, "project")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	canaryDir := t.TempDir()
	canary := filepath.Join(canaryDir, "canary.txt")
	if err := os.WriteFile(canary, []byte("canary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(canaryDir, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	policy, err := workspace.NewPathPolicy([]string{mount}, false)
	if err != nil {
		t.Fatal(err)
	}

	// The strict project-root walk-up denies the link.
	if _, err := policy.ResolveUnder(root, filepath.Join(root, "escape", "canary.txt"), false, false, "Project file"); !workspace.IsTraversal(err) {
		t.Fatalf("ResolveUnder escape = %v, want typed traversal", err)
	}
	// The mount-level admission denies the linked directory.
	if _, err := policy.Directory(filepath.Join(root, "escape"), "Directory"); !workspace.IsTraversal(err) {
		t.Fatalf("Directory escape = %v, want typed traversal", err)
	}

	// A descriptor walk through Files reports the same typed denial.
	projects := workspace.NewProjects(persist.Store{Dir: t.TempDir()}, policy)
	project, err := projects.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	files := workspace.NewFiles(projects, policy)
	if _, err := files.ReadFile(project.ID, "escape/canary.txt"); !workspace.IsTraversal(err) {
		t.Fatalf("Files.ReadFile escape = %v, want typed traversal", err)
	}
	if content, err := files.ReadFile(project.ID, "escape/canary.txt"); err == nil || strings.Contains(content, "canary") {
		t.Fatalf("canary escaped the mount: %q, %v", content, err)
	}
}

func TestResolveRealPathAllowsInternalSymlinksAndRejectsLexicalEscape(t *testing.T) {
	root := t.TempDir()
	internal := filepath.Join(root, "internal")
	if err := os.MkdirAll(internal, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(internal, "child.txt"), []byte("child"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(internal, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	resolved, err := workspace.ResolveRealPath(root, filepath.Join(root, "link", "child.txt"), false)
	if err != nil || resolved != filepath.Join(internal, "child.txt") {
		t.Fatalf("internal symlink resolve = %q, %v", resolved, err)
	}
	if _, err := workspace.ResolveRealPath(root, filepath.Join(root, "..", "outside"), false); !errors.Is(err, workspace.ErrTraversal) {
		t.Fatalf("lexical escape = %v, want traversal", err)
	}
	if _, err := workspace.ResolveRealPath(root, "/etc/hostname", false); !errors.Is(err, workspace.ErrTraversal) {
		t.Fatalf("absolute escape = %v, want traversal", err)
	}
	if err := workspace.ErrTraversal.(interface{ ErrorCode() string }).ErrorCode(); err != "PATH_ESCAPES_PROJECT_ROOT" {
		t.Fatalf("traversal code = %q", err)
	}
}

func TestResolveRealPathAllowsMissingLeafOnlyAtTheEnd(t *testing.T) {
	root := t.TempDir()
	missing, err := workspace.ResolveRealPath(root, filepath.Join(root, "new.txt"), true)
	if err != nil || missing != filepath.Join(root, "new.txt") {
		t.Fatalf("missing leaf resolve = %q, %v", missing, err)
	}
	if _, err := workspace.ResolveRealPath(root, filepath.Join(root, "missing", "new.txt"), true); err == nil {
		t.Fatal("missing intermediate directory was accepted")
	}
	if _, err := workspace.ResolveRealPath(root, filepath.Join(root, "new.txt"), false); err == nil {
		t.Fatal("missing leaf was accepted without allowMissingLeaf")
	}
}
