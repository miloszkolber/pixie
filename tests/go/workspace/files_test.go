package workspace_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

func TestProjectsAndFilesPreserveAuthorityAcrossRestart(t *testing.T) {
	mount := t.TempDir()
	root := filepath.Join(mount, "project")
	outside := t.TempDir()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	policy, err := workspace.NewPathPolicy([]string{mount}, false)
	if err != nil {
		t.Fatal(err)
	}
	store := persist.Store{Dir: t.TempDir()}
	projects := workspace.NewProjects(store, policy)
	project, err := projects.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	files := workspace.NewFiles(projects, policy)
	listing, err := files.ReadDir(project.ID, ".")
	if err != nil || len(listing.Nodes) != 1 || listing.Nodes[0].Name != "visible.txt" {
		t.Fatalf("unsafe directory listing: %#v, %v", listing, err)
	}
	if err := os.Symlink(filepath.Join(root, "visible.txt"), filepath.Join(root, "inside")); err != nil {
		t.Fatal(err)
	}
	content, err := files.ReadFile(project.ID, "visible.txt")
	if err != nil || content != "hello" {
		t.Fatalf("read file: %q, %v", content, err)
	}
	content, err = files.ReadFile(project.ID, "inside")
	if err != nil || content != "hello" {
		t.Fatalf("read internal symlink: %q, %v", content, err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("visible.txt", filepath.Join(root, "swapped")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "swapped")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "swapped")); err != nil {
		t.Fatal(err)
	}
	if content, err := files.ReadFile(project.ID, "swapped"); err == nil || content == "secret" {
		t.Fatalf("path swap exposed an external file: %q, %v", content, err)
	}
	if err := os.Mkdir(filepath.Join(root, "internal-directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal-directory", "child.txt"), []byte("child"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "internal-directory"), filepath.Join(root, "directory-link")); err != nil {
		t.Fatal(err)
	}
	listing, err = files.ReadDir(project.ID, ".")
	if err != nil || len(listing.Nodes) != 4 || listing.Nodes[0].Name != "directory-link" || listing.Nodes[0].Kind != "dir" {
		t.Fatalf("parent listing did not classify internal directory symlink: %#v, %v", listing, err)
	}
	listing, err = files.ReadDir(project.ID, "directory-link")
	if err != nil || len(listing.Nodes) != 1 || listing.Nodes[0].Name != "child.txt" {
		t.Fatalf("internal directory symlink: %#v, %v", listing, err)
	}
	if err := os.Remove(filepath.Join(root, "directory-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "directory-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := files.ReadDir(project.ID, "directory-link"); err == nil {
		t.Fatal("path-swapped directory symlink escaped the project root")
	}
	for _, path := range []string{"../elsewhere.txt", "escape/secret.txt", "."} {
		if _, err := files.ReadFile(project.ID, path); err == nil {
			t.Fatalf("accepted unsafe file path %q", path)
		}
	}
	large := filepath.Join(root, "large.txt")
	file, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(4*1024*1024 + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := files.ReadFile(project.ID, "large.txt"); err == nil {
		t.Fatal("accepted an oversized preview")
	}

	firstName := "First name"
	if _, err := projects.Update(project.ID, &firstName, nil); err != nil {
		t.Fatal(err)
	}
	secondName := "Second name"
	if _, err := projects.Update(project.ID, &secondName, nil); err != nil {
		t.Fatal(err)
	}
	current, err := projects.Get(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.Roots[0] = outside
	again, err := projects.Get(project.ID)
	if err != nil || again.Roots[0] == outside || again.Name != secondName {
		t.Fatalf("caller mutated owned state: %#v, %v", again, err)
	}

	if err := os.WriteFile(filepath.Join(store.Dir, "projects.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted := workspace.NewProjects(store, policy)
	recovered, err := restarted.Get(project.ID)
	if err != nil || recovered.Name != firstName || len(recovered.Roots) != 1 {
		t.Fatalf("restart did not recover the last valid backup: %#v, %v", recovered, err)
	}
	// The running owner intentionally keeps its validated snapshot. Recovery is
	// a restart boundary, not a live external-file synchronization mechanism.
	current, err = projects.Get(project.ID)
	if err != nil || current.Name != secondName {
		t.Fatalf("external state mutation displaced the live snapshot: %#v, %v", current, err)
	}
}

func TestProjectsSplitLegacyRootsIntoIndependentProjects(t *testing.T) {
	mount := t.TempDir()
	first, second, third := filepath.Join(mount, "first"), filepath.Join(mount, "second"), filepath.Join(mount, "third")
	for _, path := range []string{first, second, third} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := workspace.NewPathPolicy([]string{mount}, false)
	if err != nil {
		t.Fatal(err)
	}
	store := persist.Store{Dir: t.TempDir()}
	legacy := []map[string]any{
		{"id": "primary", "name": "Primary", "roots": []string{first, second}, "slug": "primary", "lastOpened": 42, "icon": "rocket", "closed": true},
		{"id": "other", "name": "second", "roots": []string{third}, "slug": "second", "lastOpened": 7},
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Dir, "projects.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	projects := workspace.NewProjects(store, policy)
	listed, err := projects.List(true)
	if err != nil || len(listed) != 3 {
		t.Fatalf("legacy split: %#v, %v", listed, err)
	}
	primary, err := projects.Get("primary")
	if err != nil || primary.Roots[0] != first || primary.Name != "Primary" {
		t.Fatalf("primary project was not retained: %#v, %v", primary, err)
	}
	var extra workspace.Project
	for _, project := range listed {
		if len(project.Roots) != 1 {
			t.Fatalf("project keeps multiple roots: %#v", project)
		}
		if project.Roots[0] == second {
			extra = project
		}
	}
	if extra.ID == "" || extra.ID == "primary" || extra.Name != "second (2)" || extra.Slug == "second" || extra.Icon != "rocket" || !extra.Closed || extra.LastOpened != 42 {
		t.Fatalf("legacy extra did not become an independent preserved project: %#v", extra)
	}
	persisted, err := os.ReadFile(filepath.Join(store.Dir, "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	var normalized []struct {
		Roots []string `json:"roots"`
	}
	if err := json.Unmarshal(persisted, &normalized); err != nil {
		t.Fatal(err)
	}
	for _, project := range normalized {
		if len(project.Roots) != 1 || project.Roots[0] == "" {
			t.Fatalf("migration persisted invalid roots: %#v", project)
		}
	}
	if _, err := projects.Open(""); err == nil {
		t.Fatal("accepted an empty project root")
	}
}

func TestOpeningDirectoriesCreatesIndependentSingleRootProjects(t *testing.T) {
	mount := t.TempDir()
	first, second := filepath.Join(mount, "first"), filepath.Join(mount, "second")
	for _, path := range []string{first, second} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := workspace.NewPathPolicy([]string{mount}, false)
	if err != nil {
		t.Fatal(err)
	}
	projects := workspace.NewProjects(persist.Store{Dir: t.TempDir()}, policy)
	firstProject, err := projects.Open(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projects.Close(firstProject.ID); err != nil {
		t.Fatal(err)
	}
	secondProject, err := projects.Open(second)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := projects.Open(first)
	if err != nil || reopened.ID != firstProject.ID || reopened.Closed || len(reopened.Roots) != 1 || reopened.Roots[0] != first {
		t.Fatalf("first project did not reopen as its own single-root project: %#v, %v", reopened, err)
	}
	if secondProject.ID == firstProject.ID || len(secondProject.Roots) != 1 || secondProject.Roots[0] != second {
		t.Fatalf("second directory joined the existing project: %#v", secondProject)
	}
}

func TestDirectoryBrowserPaginatesAndFiltersUnsafeEntries(t *testing.T) {
	mount := t.TempDir()
	root := filepath.Join(mount, "root")
	outside := t.TempDir()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha", "beta", "gamma", ".hidden"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	policy, err := workspace.NewPathPolicy([]string{mount}, false)
	if err != nil {
		t.Fatal(err)
	}
	files := workspace.NewFiles(workspace.NewProjects(persist.Store{Dir: t.TempDir()}, policy), policy)

	path := root
	first, err := files.ListDirectories(workspace.DirectoryRequest{Path: &path, PageSize: 2})
	if err != nil || !first.HasMore || first.Cursor == nil || *first.Cursor != "1" {
		t.Fatalf("first page: %#v, %v", first, err)
	}
	second, err := files.ListDirectories(workspace.DirectoryRequest{Path: &path, Page: 1, PageSize: 2})
	if err != nil || second.HasMore || second.Cursor != nil {
		t.Fatalf("second page: %#v, %v", second, err)
	}
	names := make([]string, 0, len(first.Directories)+len(second.Directories))
	for _, entry := range append(first.Directories, second.Directories...) {
		names = append(names, entry.Name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "alpha,beta,gamma" {
		t.Fatalf("hidden or escaping entries leaked into pages: %#v", names)
	}
	visible, err := files.ListDirectories(workspace.DirectoryRequest{Path: &path, PageSize: 10, IncludeHidden: true})
	if err != nil || len(visible.Directories) != 4 {
		t.Fatalf("hidden directory was not opt-in: %#v, %v", visible, err)
	}
	if _, err := files.ListDirectories(workspace.DirectoryRequest{Path: &path, Page: 100}); err == nil {
		t.Fatal("accepted an unbounded page")
	}
	if err := os.Mkdir(filepath.Join(root, "alpha", "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "alpha"), filepath.Join(root, "safe-link")); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "safe-link")
	listing, err := files.ListDirectories(workspace.DirectoryRequest{Path: &linked, PageSize: 10})
	if err != nil || len(listing.Directories) != 1 || listing.Directories[0].Name != "child" {
		t.Fatalf("internal directory browser symlink: %#v, %v", listing, err)
	}
	if err := os.Remove(linked); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := files.ListDirectories(workspace.DirectoryRequest{Path: &linked, PageSize: 10}); err == nil {
		t.Fatal("path-swapped directory browser symlink escaped its mount")
	}
}

func TestBoundedPersistenceRejectsSpecialFilesWithoutBlocking(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "state")
	if err := os.WriteFile(path, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := persist.ReadBoundedFile(path, 3); err == nil {
		t.Fatal("accepted an oversized regular file")
	}
	pipe := filepath.Join(directory, "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := persist.ReadBoundedFile(pipe, 4)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("accepted a named pipe")
		}
	case <-time.After(time.Second):
		t.Fatal("bounded state read blocked on a named pipe")
	}
}
