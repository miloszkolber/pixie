package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var discoveryIgnored = map[string]bool{".git": true, ".cache": true, ".next": true, ".turbo": true, ".venv": true, "build": true, "coverage": true, "dist": true, "node_modules": true, "target": true, "vendor": true}

const discoveryReadBatch = 128

type GitHead struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	OID  string `json:"oid,omitempty"`
}

type GitFileChange struct {
	Path         string `json:"path"`
	OriginalPath string `json:"originalPath,omitempty"`
	Status       string `json:"status"`
	Added        *int   `json:"added,omitempty"`
	Removed      *int   `json:"removed,omitempty"`
}

type GitRepository struct {
	ID           string          `json:"id"`
	Root         string          `json:"root"`
	RelativePath string          `json:"relativePath"`
	Name         string          `json:"name"`
	Head         GitHead         `json:"head"`
	ComparisonID string          `json:"comparisonId,omitempty"`
	Clean        bool            `json:"clean"`
	Changes      []GitFileChange `json:"changes"`
}

type GitRepositoryList struct {
	Repositories []GitRepository `json:"repositories"`
	Complete     bool            `json:"complete"`
	Warnings     []string        `json:"warnings"`
}

type Git struct {
	projects      *Projects
	policy        *PathPolicy
	mu            sync.Mutex
	cache         map[string]*repositoryDiscovery
	statusFlights map[string]*gitStatusFlight
}

type repositoryDiscovery struct {
	root     string
	paths    []string
	complete bool
	done     chan struct{}
	err      error
}

type gitStatusFlight struct {
	projectID, repository string
	done                  chan struct{}
	dirty                 bool
	result                GitRepository
	err                   error
}

func NewGit(projects *Projects, policy *PathPolicy) *Git {
	return &Git{projects: projects, policy: policy, cache: make(map[string]*repositoryDiscovery), statusFlights: make(map[string]*gitStatusFlight)}
}

func (g *Git) Invalidate(projectID string) {
	g.mu.Lock()
	delete(g.cache, projectID)
	for _, flight := range g.statusFlights {
		if flight.projectID == projectID {
			flight.dirty = true
		}
	}
	g.mu.Unlock()
}

func (g *Git) ListRepositories(ctx context.Context, projectID string) (GitRepositoryList, error) {
	project, err := g.projects.Get(projectID)
	if err != nil {
		return GitRepositoryList{}, err
	}
	discovery, err := g.repositories(ctx, project)
	if err != nil {
		return GitRepositoryList{}, err
	}
	result := GitRepositoryList{Repositories: []GitRepository{}, Complete: discovery.complete, Warnings: []string{}}
	if !discovery.complete {
		result.Warnings = append(result.Warnings, "Repository discovery reached a filesystem or safety boundary.")
	}
	statuses := make([]GitRepository, len(discovery.paths))
	errors := make([]error, len(discovery.paths))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for worker := 0; worker < min(4, len(discovery.paths)); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				statuses[index], errors[index] = g.sharedStatus(ctx, project, discovery.paths[index], GitDiffScope{}, g.projectRepository)
			}
		}()
	}
	for index := range discovery.paths {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	for index, repository := range discovery.paths {
		if errors[index] != nil {
			result.Complete = false
			result.Warnings = append(result.Warnings, "Could not inspect "+projectRelativePath(project, repository)+".")
			continue
		}
		result.Repositories = append(result.Repositories, statuses[index])
	}
	return result, nil
}

func (g *Git) Status(ctx context.Context, projectID, requested string, scope GitDiffScope) (GitRepository, error) {
	project, repository, err := g.repositoryFor(ctx, projectID, requested)
	if err != nil {
		return GitRepository{}, err
	}
	return g.sharedStatus(ctx, project, repository, scope, g.projectRepository)
}

func (g *Git) MarkDirty(projectID, path string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, flight := range g.statusFlights {
		if flight.projectID == projectID && Within(flight.repository, path) {
			flight.dirty = true
		}
	}
}

func (g *Git) sharedStatus(ctx context.Context, project Project, repository string, scope GitDiffScope, inspect func(context.Context, Project, string, GitDiffScope) (GitRepository, error)) (GitRepository, error) {
	if err := ctx.Err(); err != nil {
		return GitRepository{}, err
	}
	if scope.Kind == "" {
		scope = GitDiffScope{Kind: "uncommitted"}
	}
	key := project.ID + "\x00" + repository + "\x00" + scope.Kind + "\x00" + scope.SHA + "\x00" + scope.BaseRef
	g.mu.Lock()
	flight := g.statusFlights[key]
	if flight == nil {
		flight = &gitStatusFlight{projectID: project.ID, repository: repository, done: make(chan struct{})}
		g.statusFlights[key] = flight
		go func() {
			bounded, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			result, err := inspect(bounded, project, repository, scope)
			g.mu.Lock()
			dirty := flight.dirty
			g.mu.Unlock()
			if err == nil && dirty && scope.Kind != "commit" {
				result, err = inspect(bounded, project, repository, scope)
			}
			g.mu.Lock()
			flight.result, flight.err = result, err
			delete(g.statusFlights, key)
			close(flight.done)
			g.mu.Unlock()
		}()
	}
	g.mu.Unlock()
	select {
	case <-flight.done:
		return flight.result, flight.err
	case <-ctx.Done():
		return GitRepository{}, ctx.Err()
	}
}

func (g *Git) repositories(ctx context.Context, project Project) (repositoryDiscovery, error) {
	root, err := project.Root()
	if err != nil {
		return repositoryDiscovery{}, err
	}
	g.mu.Lock()
	cached := g.cache[project.ID]
	if cached == nil || cached.root != root {
		cached = &repositoryDiscovery{root: root, done: make(chan struct{})}
		g.cache[project.ID] = cached
		go func() {
			bounded, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			discovery, err := g.discover(bounded, project)
			g.mu.Lock()
			cached.paths, cached.complete, cached.err = discovery.paths, discovery.complete, err
			if err != nil && g.cache[project.ID] == cached {
				delete(g.cache, project.ID)
			}
			close(cached.done)
			g.mu.Unlock()
		}()
	}
	g.mu.Unlock()
	select {
	case <-cached.done:
	case <-ctx.Done():
		return repositoryDiscovery{}, ctx.Err()
	}
	if cached.err != nil {
		return repositoryDiscovery{}, cached.err
	}
	for _, repository := range cached.paths {
		if _, err := g.policy.Directory(repository, "Git repository"); err != nil {
			return repositoryDiscovery{}, err
		}
	}
	return *cached, nil
}

func (g *Git) discover(ctx context.Context, project Project) (repositoryDiscovery, error) {
	type queued struct {
		path  string
		depth int
	}
	result := repositoryDiscovery{complete: true}
	seen := make(map[string]bool)
	visited, scanned, queuedCount, probes := 0, 0, 0, 0
	configuredRoot, err := project.Root()
	if err != nil {
		return repositoryDiscovery{}, err
	}
	root, err := g.policy.Directory(configuredRoot, "Project root")
	if err != nil {
		return repositoryDiscovery{}, err
	}
	queue := []queued{{path: root}}
	queuedCount++
	for len(queue) > 0 && len(result.paths) < 64 {
		if ctx.Err() != nil {
			return repositoryDiscovery{}, ctx.Err()
		}
		if visited >= 20_000 || scanned >= 20_000 {
			result.complete = false
			break
		}
		current := queue[0]
		queue = queue[1:]
		visited++
		canonical, err := filepath.EvalSymlinks(current.path)
		if err != nil || seen[canonical] {
			continue
		}
		seen[canonical] = true
		if _, err := os.Lstat(filepath.Join(canonical, ".git")); err == nil {
			if probes >= 256 {
				result.complete = false
				break
			}
			probes++
			probe := runGit(ctx, canonical, []string{"rev-parse", "--show-toplevel"}, gitOutputLimit)
			if probe.ok {
				top, evalErr := filepath.EvalSymlinks(strings.TrimSpace(probe.out))
				if evalErr == nil && top == canonical {
					result.paths = append(result.paths, canonical)
				}
			}
		}
		if current.depth >= 5 {
			continue
		}
		directory, readErr := os.Open(canonical)
		if readErr != nil {
			continue
		}
	readEntries:
		for scanned < 20_000 && queuedCount < 4_000 {
			entries, readErr := directory.ReadDir(min(discoveryReadBatch, 20_000-scanned))
			if len(entries) == 0 {
				if readErr != nil && readErr != io.EOF {
					result.complete = false
				}
				break
			}
			for _, entry := range entries {
				scanned++
				if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || discoveryIgnored[entry.Name()] {
					continue
				}
				queue = append(queue, queued{path: filepath.Join(canonical, entry.Name()), depth: current.depth + 1})
				queuedCount++
				if queuedCount >= 4_000 {
					break readEntries
				}
			}
			if readErr == io.EOF {
				break
			}
		}
		_ = directory.Close()
		if scanned >= 20_000 || queuedCount >= 4_000 {
			result.complete = false
		}
	}
	if len(queue) > 0 || len(result.paths) >= 64 {
		result.complete = false
	}
	sort.Strings(result.paths)
	return result, nil
}

func (g *Git) repositoryFor(ctx context.Context, projectID, requested string) (Project, string, error) {
	project, err := g.projects.Get(projectID)
	if err != nil {
		return Project{}, "", err
	}
	wanted, err := g.policy.Directory(requested, "Git repository")
	if err != nil {
		return Project{}, "", err
	}
	discovery, err := g.repositories(ctx, project)
	if err != nil {
		return Project{}, "", err
	}
	for _, candidate := range discovery.paths {
		if candidate == wanted {
			return project, candidate, nil
		}
	}
	return Project{}, "", fmt.Errorf("directory is not a discovered repository in this project")
}

func (g *Git) projectRepository(ctx context.Context, project Project, repository string, scope GitDiffScope) (GitRepository, error) {
	changes, comparisonID, err := g.changes(ctx, repository, scope)
	if err != nil {
		return GitRepository{}, err
	}
	digest := sha256.Sum256([]byte(repository))
	return GitRepository{ID: hex.EncodeToString(digest[:])[:24], Root: repository, RelativePath: projectRelativePath(project, repository), Name: filepath.Base(repository), Head: gitHead(ctx, repository), ComparisonID: comparisonID, Clean: len(changes) == 0, Changes: changes}, nil
}

func gitHead(ctx context.Context, repository string) GitHead {
	oid := resolveCommit(ctx, repository, "HEAD")
	if oid == "" {
		return GitHead{Kind: "unborn"}
	}
	branch := runGit(ctx, repository, []string{"symbolic-ref", "--quiet", "HEAD"}, gitOutputLimit)
	ref := strings.TrimSpace(branch.out)
	if branch.ok && strings.HasPrefix(ref, "refs/heads/") && len(ref) > len("refs/heads/") {
		return GitHead{Kind: "branch", Name: strings.TrimPrefix(ref, "refs/heads/")}
	}
	return GitHead{Kind: "detached", OID: oid}
}

func projectRelativePath(project Project, repository string) string {
	root, err := project.Root()
	if err != nil || !Within(root, repository) {
		return filepath.Base(repository)
	}
	relative, _ := filepath.Rel(root, repository)
	if relative == "." {
		return filepath.Base(root)
	}
	return filepath.Join(filepath.Base(root), relative)
}
