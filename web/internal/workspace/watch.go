package workspace

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const maxWatchDirectories = 20_000

type ProjectFsChange struct {
	Root string `json:"root"`
	Path string `json:"path"`
}
type ProjectFsChanged struct {
	ProjectID string            `json:"projectId"`
	Changes   []ProjectFsChange `json:"changes"`
	Truncated bool              `json:"truncated"`
}

type ProjectWatches struct {
	mu       sync.Mutex
	projects *Projects
	git      *Git
	publish  func(string, any)
	active   map[string]*projectWatch
}

type projectWatch struct {
	owner      *ProjectWatches
	id         string
	root       string
	native     *fsnotify.Watcher
	dirs       map[string]bool
	stop, done chan struct{}
	ready      chan struct{}
	truncated  bool
}

func NewProjectWatches(projects *Projects, git *Git, publish func(string, any)) *ProjectWatches {
	return &ProjectWatches{projects: projects, git: git, publish: publish, active: make(map[string]*projectWatch)}
}

func (w *ProjectWatches) Ensure(projectID string) (bool, error) { return w.ensure(projectID, false) }
func (w *ProjectWatches) Reconcile(projectID string) error {
	_, err := w.ensure(projectID, true)
	return err
}

func (w *ProjectWatches) ensure(projectID string, onlyActive bool) (bool, error) {
	w.mu.Lock()
	state, created, err := func() (*projectWatch, bool, error) {
		defer w.mu.Unlock()
		current := w.active[projectID]
		if onlyActive && current == nil {
			return nil, false, nil
		}
		project, err := w.projects.Get(projectID)
		if err != nil {
			return nil, false, err
		}
		root, err := project.Root()
		if err != nil {
			return nil, false, err
		}
		if current != nil && current.root == root {
			return current, false, nil
		}
		if current != nil {
			current.close()
			delete(w.active, projectID)
		}
		native, err := fsnotify.NewBufferedWatcher(1024)
		if err != nil {
			return nil, false, fmt.Errorf("could not start project filesystem notifications")
		}
		state := &projectWatch{owner: w, id: projectID, root: root, native: native, dirs: make(map[string]bool), stop: make(chan struct{}), done: make(chan struct{}), ready: make(chan struct{})}
		if err := native.Add(root); err != nil {
			native.Close()
			return nil, false, fmt.Errorf("could not watch the admitted project root")
		}
		state.dirs[root] = true
		w.active[projectID] = state
		go state.run()
		return state, true, nil
	}()
	if state != nil {
		<-state.ready
	}
	return created, err
}

func (w *ProjectWatches) Stop(projectID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if state := w.active[projectID]; state != nil {
		state.close()
		delete(w.active, projectID)
	}
}

func (w *ProjectWatches) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, state := range w.active {
		state.close()
		delete(w.active, id)
	}
}

func (s *projectWatch) close() {
	close(s.stop)
	_ = s.native.Close()
	<-s.done
}

// Read directory entries in small batches and never follow symlinks. Reaching
// a filesystem/descriptor/entry limit produces an explicit incomplete refresh.
func (s *projectWatch) addTree(start string) {
	queue, scanned := []string{start}, 0
	for len(queue) > 0 {
		select {
		case <-s.stop:
			return
		default:
		}
		path := queue[0]
		queue = queue[1:]
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil || canonical != path {
			s.truncated = true
			continue
		}
		if !s.dirs[path] {
			if len(s.dirs) >= maxWatchDirectories {
				s.truncated = true
				return
			}
			if err := s.native.Add(path); err != nil {
				s.truncated = true
				continue
			}
			s.dirs[path] = true
		}
		file, err := os.Open(path)
		if err != nil {
			s.truncated = true
			continue
		}
		for {
			entries, readErr := file.ReadDir(256)
			scanned += len(entries)
			if scanned > 200_000 {
				s.truncated = true
				file.Close()
				return
			}
			for _, entry := range entries {
				if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				if len(queue) >= maxWatchDirectories {
					s.truncated = true
					continue
				}
				queue = append(queue, filepath.Join(path, entry.Name()))
			}
			if readErr != nil {
				if readErr != io.EOF {
					s.truncated = true
				}
				break
			}
		}
		file.Close()
	}
}

func (s *projectWatch) run() {
	defer close(s.done)
	// Admission holds only the cheap root watch; traversal belongs to this worker.
	s.addTree(s.root)
	close(s.ready)
	// Refresh files that may have changed while descendant watches were armed.
	s.truncated = true
	changes := make(map[ProjectFsChange]bool)
	var timer *time.Timer
	var tick <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	schedule := func() {
		if tick == nil {
			timer = time.NewTimer(100 * time.Millisecond)
			tick = timer.C
		}
	}
	if s.truncated {
		schedule()
	}
	for {
		select {
		case <-s.stop:
			return
		case event, open := <-s.native.Events:
			if !open {
				return
			}
			path := filepath.Clean(event.Name)
			if s.owner.git != nil {
				s.owner.git.MarkDirty(s.id, path)
			}
			wasDirectory := s.dirs[path]
			if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
				for watched := range s.dirs {
					if Within(path, watched) {
						_ = s.native.Remove(watched)
						delete(s.dirs, watched)
					}
				}
			}
			newDirectory := false
			if event.Has(fsnotify.Create) {
				if info, err := os.Lstat(path); err == nil && info.IsDir() {
					newDirectory = true
					s.addTree(path)
					// A populated directory can arrive before its watches are armed.
					s.truncated = true
				}
			}
			if Within(s.root, path) {
				relative, err := filepath.Rel(s.root, path)
				if err != nil {
					s.truncated = true
					continue
				}
				relative = filepath.ToSlash(relative)
				change := ProjectFsChange{Root: s.root, Path: relative}
				if len(changes) < 500 || changes[change] {
					changes[change] = true
				} else {
					s.truncated = true
				}
				if s.owner.git != nil && (slices.Contains(strings.Split(relative, "/"), ".git") || newDirectory || wasDirectory && (event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename))) {
					s.owner.git.Invalidate(s.id)
				}
			}
			schedule()
		case _, open := <-s.native.Errors:
			if !open {
				return
			}
			s.truncated = true
			if s.owner.git != nil {
				s.owner.git.Invalidate(s.id)
			}
			s.addTree(s.root)
			schedule()
		case <-tick:
			tick = nil
			payload := ProjectFsChanged{ProjectID: s.id, Changes: make([]ProjectFsChange, 0, len(changes)), Truncated: s.truncated}
			for change := range changes {
				payload.Changes = append(payload.Changes, change)
			}
			sort.Slice(payload.Changes, func(i, j int) bool {
				a, b := payload.Changes[i], payload.Changes[j]
				if a.Root != b.Root {
					return a.Root < b.Root
				}
				return a.Path < b.Path
			})
			clear(changes)
			s.truncated = false
			if (len(payload.Changes) > 0 || payload.Truncated) && s.owner.publish != nil {
				s.owner.publish("project.fsChanged", payload)
			}
		}
	}
}
