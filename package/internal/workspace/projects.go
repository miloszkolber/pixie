package workspace

import (
	"cmp"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/miloszkolber/pixie/internal/identifier"
	"github.com/miloszkolber/pixie/internal/persist"
)

var (
	slugCharacters = regexp.MustCompile(`[^a-z0-9]+`)
	projectIcons   = map[string]bool{"folder": true, "code": true, "book": true, "flask": true, "rocket": true, "sparkles": true}
)

type Projects struct {
	mu       sync.RWMutex
	store    persist.Store
	policy   *PathPolicy
	now      func() time.Time
	publish  func(Project)
	loaded   bool
	projects []Project
}

func NewProjects(store persist.Store, policy *PathPolicy) *Projects {
	return &Projects{store: store, policy: policy, now: time.Now}
}

func utf16Length(value string) int {
	count := 0
	for _, character := range value {
		count += utf16.RuneLen(character)
	}
	return count
}

func (p *Projects) SetPublisher(publish func(Project)) {
	p.mu.Lock()
	p.publish = publish
	p.mu.Unlock()
}

type persistedProject struct {
	Project
	Path string `json:"path,omitempty"`
}

type ProjectRootMigration struct {
	SourceProjectID string `json:"sourceProjectId"`
	Root            string `json:"root"`
	TargetProjectID string `json:"targetProjectId"`
}

func (p *Projects) load() ([]Project, error) {
	// This process is the sole owner of project state. Load and validate once,
	// then keep a private snapshot synchronized with each atomic write.
	if p.loaded {
		return cloneProjects(p.projects), nil
	}
	projects, _, migrated, err := p.readNormalized()
	if err != nil {
		return nil, err
	}
	if migrated {
		if err := p.save(projects); err != nil {
			return nil, err
		}
	} else {
		p.projects = cloneProjects(projects)
		p.loaded = true
	}
	return cloneProjects(projects), nil
}

// CoordinateLegacyMigration runs dependent state migration before a legacy
// multi-root project is replaced with its single-root projects.
func (p *Projects) CoordinateLegacyMigration(migrate func([]ProjectRootMigration) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.loaded {
		return nil
	}
	projects, mappings, migrated, err := p.readNormalized()
	if err != nil {
		return err
	}
	if len(mappings) > 0 && migrate != nil {
		if err := migrate(mappings); err != nil {
			return err
		}
	}
	if migrated {
		return p.save(projects)
	}
	p.projects = cloneProjects(projects)
	p.loaded = true
	return nil
}

func (p *Projects) readNormalized() ([]Project, []ProjectRootMigration, bool, error) {
	var persisted []persistedProject
	if _, err := persist.Read(p.store, "projects.json", &persisted, validateProjects); err != nil {
		return nil, nil, false, err
	}
	projects := make([]Project, 0, len(persisted))
	mappings := make([]ProjectRootMigration, 0)
	migrated := false
	claimedRoots := make(map[string]bool)
	takenIDs := make(map[string]bool)
	for _, entry := range persisted {
		if entry.ID != "" {
			takenIDs[entry.ID] = true
		}
	}
	var extras []struct {
		root      string
		source    persistedProject
		migrating bool
	}
	for _, entry := range persisted {
		roots := slices.Clone(entry.Roots)
		if len(roots) == 0 && entry.Path != "" {
			roots = []string{entry.Path}
			migrated = true
		}
		if entry.ID == "" || len(roots) == 0 {
			continue
		}
		canonicalRoots := make([]string, 0, len(roots))
		for _, root := range roots {
			canonical, resolveErr := p.policy.Directory(root, "Project")
			if resolveErr != nil {
				return nil, nil, false, resolveErr
			}
			if claimedRoots[canonical] {
				migrated = true
				continue
			}
			claimedRoots[canonical] = true
			canonicalRoots = append(canonicalRoots, canonical)
			if canonical != root {
				migrated = true
			}
		}
		if len(canonicalRoots) == 0 {
			continue
		}
		if len(roots) != 1 {
			migrated = true
		}
		root := canonicalRoots[0]
		projects = append(projects, Project{
			ID: entry.ID, Name: entry.Name, Roots: []string{root}, Slug: entry.Slug,
			LastOpened: entry.LastOpened, Icon: entry.Icon, Closed: entry.Closed,
		})
		isLegacySplit := len(canonicalRoots) > 1
		if isLegacySplit {
			mappings = append(mappings, ProjectRootMigration{SourceProjectID: entry.ID, Root: root, TargetProjectID: entry.ID})
		}
		for _, extra := range canonicalRoots[1:] {
			extras = append(extras, struct {
				root      string
				source    persistedProject
				migrating bool
			}{root: extra, source: entry, migrating: isLegacySplit})
		}
	}
	for _, extra := range extras {
		name := uniqueProjectName(filepath.Base(extra.root), projects)
		id := uniqueLegacyProjectID(extra.source.ID, extra.root, takenIDs)
		takenIDs[id] = true
		projects = append(projects, Project{
			ID: id, Name: name, Roots: []string{extra.root}, LastOpened: extra.source.LastOpened,
			Icon: extra.source.Icon, Closed: extra.source.Closed,
		})
		if extra.migrating {
			mappings = append(mappings, ProjectRootMigration{SourceProjectID: extra.source.ID, Root: extra.root, TargetProjectID: id})
		}
	}
	if ensureSlugs(projects) {
		migrated = true
	}
	return projects, mappings, migrated, nil
}

func (p *Projects) ensureLoaded() error {
	p.mu.RLock()
	if p.loaded {
		p.mu.RUnlock()
		return nil
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()
	_, err := p.load()
	return err
}

func (p *Projects) save(projects []Project) error {
	values := make([]persistedProject, len(projects))
	for index, project := range projects {
		values[index].Project = project
	}
	if err := persist.Write(p.store, "projects.json", values, validateCurrentProjects); err != nil {
		return err
	}
	p.projects = cloneProjects(projects)
	p.loaded = true
	return nil
}

func cloneProjects(projects []Project) []Project {
	result := make([]Project, len(projects))
	for index, project := range projects {
		result[index] = project
		result[index].Roots = slices.Clone(project.Roots)
	}
	return result
}

func validateProjects(values []persistedProject) error {
	if values == nil {
		return fmt.Errorf("projects must be an array")
	}
	for _, value := range values {
		if value.ID == "" || (len(value.Roots) == 0 && value.Path == "") || value.Icon != "" && !projectIcons[value.Icon] {
			return fmt.Errorf("invalid persisted project")
		}
		for _, root := range value.Roots {
			if strings.TrimSpace(root) == "" {
				return fmt.Errorf("invalid persisted project root")
			}
		}
	}
	return nil
}

func validateCurrentProjects(values []persistedProject) error {
	if err := validateProjects(values); err != nil {
		return err
	}
	for _, value := range values {
		if len(value.Roots) != 1 || value.Path != "" {
			return fmt.Errorf("project must have exactly one root")
		}
	}
	return nil
}

func (p *Projects) List(includeClosed bool) ([]Project, error) {
	if err := p.ensureLoaded(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	filtered := make([]Project, 0, len(p.projects))
	for _, project := range p.projects {
		if includeClosed || !project.Closed {
			project.Roots = slices.Clone(project.Roots)
			filtered = append(filtered, project)
		}
	}
	slices.SortStableFunc(filtered, func(a, b Project) int { return cmp.Compare(b.LastOpened, a.LastOpened) })
	return filtered, nil
}

func (p *Projects) Get(id string) (Project, error) {
	if err := p.ensureLoaded(); err != nil {
		return Project{}, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	var result Project
	found := false
	for _, project := range p.projects {
		if project.ID == id && (!found || project.LastOpened > result.LastOpened) {
			result, found = project, true
		}
	}
	if !found {
		return Project{}, fmt.Errorf("unknown project: %s", id)
	}
	result.Roots = slices.Clone(result.Roots)
	return result, nil
}

func (p *Projects) Open(path string) (result Project, err error) {
	p.mu.Lock()
	defer p.finishMutation(&result, &err)
	root, err := p.policy.Directory(path, "Project")
	if err != nil {
		return Project{}, err
	}
	projects, err := p.load()
	if err != nil {
		return Project{}, err
	}
	for index := range projects {
		known, rootErr := projects[index].Root()
		if rootErr != nil {
			return Project{}, rootErr
		}
		if known == root {
			projects[index].Closed = false
			projects[index].LastOpened = float64(p.now().UnixMilli())
			if err := p.save(projects); err != nil {
				return Project{}, err
			}
			return projects[index], nil
		}
	}
	name := filepath.Base(root)
	taken := make(map[string]bool, len(projects))
	for _, project := range projects {
		taken[project.Slug] = true
	}
	project := Project{ID: identifier.New(), Name: name, Roots: []string{root}, Slug: uniqueSlug(slugify(name), taken), LastOpened: float64(p.now().UnixMilli())}
	projects = append(projects, project)
	if err := p.save(projects); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (p *Projects) Update(id string, name, icon *string) (result Project, err error) {
	if name == nil && icon == nil {
		return Project{}, fmt.Errorf("project update requires a name or icon")
	}
	p.mu.Lock()
	defer p.finishMutation(&result, &err)
	projects, err := p.load()
	if err != nil {
		return Project{}, err
	}
	index := projectIndex(projects, id)
	if index < 0 {
		return Project{}, fmt.Errorf("unknown project: %s", id)
	}
	if name != nil {
		normalized := strings.TrimSpace(*name)
		if normalized == "" || strings.ContainsRune(normalized, 0) || utf16Length(normalized) > 100 {
			return Project{}, fmt.Errorf("invalid project name")
		}
		projects[index].Name = normalized
	}
	if icon != nil {
		if !projectIcons[*icon] {
			return Project{}, fmt.Errorf("unknown project icon")
		}
		projects[index].Icon = *icon
	}
	projects[index].LastOpened = float64(p.now().UnixMilli())
	if err := p.save(projects); err != nil {
		return Project{}, err
	}
	return projects[index], nil
}

func (p *Projects) Close(id string) (result Project, err error) {
	p.mu.Lock()
	defer p.finishMutation(&result, &err)
	projects, err := p.load()
	if err != nil {
		return Project{}, err
	}
	index := projectIndex(projects, id)
	if index < 0 {
		return Project{}, fmt.Errorf("unknown project: %s", id)
	}
	projects[index].Closed = true
	if err := p.save(projects); err != nil {
		return Project{}, err
	}
	return projects[index], nil
}

func (p *Projects) finishMutation(result *Project, err *error) {
	publish := p.publish
	p.mu.Unlock()
	if *err == nil && publish != nil {
		publish(*result)
	}
}

func (p *Projects) AssertCWD(projectID, cwd string) (string, error) {
	project, err := p.Get(projectID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(cwd) == "" {
		cwd, err = project.Root()
		if err != nil {
			return "", err
		}
	}
	candidate, err := p.policy.Directory(cwd, "Session directory")
	if err != nil {
		return "", err
	}
	root, err := project.Root()
	if err != nil {
		return "", err
	}
	if Within(root, candidate) {
		return candidate, nil
	}
	return "", fmt.Errorf("session directory is outside the project root")
}

func (p *Projects) AssertRoot(projectID, root string) (string, error) {
	project, err := p.Get(projectID)
	if err != nil {
		return "", err
	}
	admitted, err := project.Root()
	if err != nil {
		return "", err
	}
	// Get already resolves and authorizes the root against the current mount.
	if admitted == root {
		return root, nil
	}
	candidate, err := p.policy.Directory(root, "Project root")
	if err != nil {
		return "", err
	}
	if admitted != candidate {
		return "", fmt.Errorf("project root must exactly match the admitted project root")
	}
	return candidate, nil
}

func (p *Projects) Root(id string) (string, error) {
	project, err := p.Get(id)
	if err != nil {
		return "", err
	}
	return project.Root()
}

func projectIndex(projects []Project, id string) int {
	for index := range projects {
		if projects[index].ID == id {
			return index
		}
	}
	return -1
}

func slugify(name string) string {
	slug := strings.Trim(slugCharacters.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if slug == "" {
		return "project"
	}
	return slug
}

func uniqueSlug(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s-%d", base, suffix)
		if !taken[candidate] {
			return candidate
		}
	}
}

func ensureSlugs(projects []Project) bool {
	taken := make(map[string]bool, len(projects))
	changed := false
	for index := range projects {
		if projects[index].Slug != "" && !taken[projects[index].Slug] {
			taken[projects[index].Slug] = true
			continue
		}
		projects[index].Slug = uniqueSlug(slugify(projects[index].Name), taken)
		taken[projects[index].Slug] = true
		changed = true
	}
	return changed
}

func uniqueProjectName(base string, projects []Project) string {
	if base == "" || base == string(filepath.Separator) || base == "." {
		base = "Project"
	}
	taken := make(map[string]bool, len(projects))
	for _, project := range projects {
		taken[project.Name] = true
	}
	if !taken[base] {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s (%d)", base, suffix)
		if !taken[candidate] {
			return candidate
		}
	}
}

func uniqueLegacyProjectID(sourceID, root string, taken map[string]bool) string {
	for suffix := 0; ; suffix++ {
		digest := sha256.Sum256([]byte(fmt.Sprintf("pixie-project-root\x00%s\x00%s\x00%d", sourceID, root, suffix)))
		value := digest[:16]
		value[6] = value[6]&0x0f | 0x40
		value[8] = value[8]&0x3f | 0x80
		id := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[:4], value[4:6], value[6:8], value[8:10], value[10:16])
		if !taken[id] {
			return id
		}
	}
}
