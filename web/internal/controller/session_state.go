package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/miloszkolber/pixie/internal/persist"
)

type ProjectSessionRecord struct {
	ProjectID       string `json:"projectId"`
	SessionID       string `json:"sessionId"`
	CWD             string `json:"cwd"`
	ParentSessionID string `json:"parentSessionId,omitempty"`
	Title           string `json:"title,omitempty"`
}

type SessionRecords struct {
	mu    sync.Mutex
	store persist.Store
}

type storedSessions struct {
	Version int                    `json:"version"`
	Engine  string                 `json:"engine"`
	Records []ProjectSessionRecord `json:"records"`
}

func validateRecords(value storedSessions) error {
	if value.Version != 2 || value.Engine != "pi" || value.Records == nil {
		return fmt.Errorf("invalid project-session store")
	}
	for _, record := range value.Records {
		if err := validateSessionRecord(record); err != nil {
			return err
		}
	}
	return nil
}

func NewSessionRecords(store persist.Store) *SessionRecords { return &SessionRecords{store: store} }

func (s *SessionRecords) List() ([]ProjectSessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *SessionRecords) load() ([]ProjectSessionRecord, error) {
	var value storedSessions
	ok, err := persist.Read(s.store, "pi-project-sessions.json", &value, validateRecords)
	if err != nil || !ok {
		return []ProjectSessionRecord{}, err
	}
	return value.Records, nil
}

func (s *SessionRecords) save(records []ProjectSessionRecord) error {
	return persist.Write(s.store, "pi-project-sessions.json", storedSessions{Version: 2, Engine: "pi", Records: records}, validateRecords)
}

func (s *SessionRecords) Record(record ProjectSessionRecord) error {
	if err := validateSessionRecord(record); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.load()
	if err != nil {
		return err
	}
	for index := range records {
		if records[index].SessionID == record.SessionID {
			if record.Title == "" {
				record.Title = records[index].Title
			}
			records[index] = record
			return s.save(records)
		}
	}
	return s.save(append(records, record))
}

// SetTitle persists the last title observed for a session without requiring
// callers to reconstruct the rest of its durable association. Pi can emit
// generated titles as session updates, but those updates are not guaranteed
// to be returned by a later session.load, so the controller keeps this small
// local projection as a restart fallback.
func (s *SessionRecords) SetTitle(projectID, sessionID, title string) error {
	title = strings.TrimSpace(title)
	if title == "" || containsNUL(title) || utf16Length(title) > 200 {
		return fmt.Errorf("session title is invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.load()
	if err != nil {
		return err
	}
	for index := range records {
		if records[index].ProjectID == projectID && records[index].SessionID == sessionID {
			records[index].Title = title
			return s.save(records)
		}
	}
	return fmt.Errorf("unknown session: %s", sessionID)
}

func (s *SessionRecords) Forget(projectID, sessionID string) error {
	return s.filter(func(record ProjectSessionRecord) bool {
		return record.ProjectID != projectID || record.SessionID != sessionID
	})
}

func (s *SessionRecords) ForgetProject(projectID string) error {
	return s.filter(func(record ProjectSessionRecord) bool { return record.ProjectID != projectID })
}

func (s *SessionRecords) filter(keep func(ProjectSessionRecord) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.load()
	if err != nil {
		return err
	}
	filtered := records[:0]
	for _, record := range records {
		if keep(record) {
			filtered = append(filtered, record)
		}
	}
	if err := s.save(filtered); err != nil {
		return err
	}
	// A deletion is not complete while the rollback generation can restore it.
	return s.save(filtered)
}

func validateSessionRecord(record ProjectSessionRecord) error {
	if record.ProjectID == "" || validatePiSessionID(record.SessionID) != nil || record.CWD == "" || containsNUL(record.ProjectID) || containsNUL(record.CWD) {
		return fmt.Errorf("invalid project session record")
	}
	if record.ParentSessionID != "" && (validatePiSessionID(record.ParentSessionID) != nil || record.ParentSessionID == record.SessionID) {
		return fmt.Errorf("invalid project session record")
	}
	if record.Title != "" && (containsNUL(record.Title) || utf16Length(record.Title) > 200) {
		return fmt.Errorf("invalid project session record")
	}
	return nil
}

type SessionTask struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Status string `json:"status"`
}

type SessionGoal struct {
	ProjectID string        `json:"projectId"`
	SessionID string        `json:"sessionId"`
	Goal      *string       `json:"goal"`
	Tasks     []SessionTask `json:"tasks"`
	UpdatedAt *float64      `json:"updatedAt"`
}

type storedObjective struct {
	Version   int           `json:"version"`
	ProjectID string        `json:"projectId"`
	SessionID string        `json:"sessionId"`
	Goal      *string       `json:"goal"`
	Tasks     []SessionTask `json:"tasks"`
	UpdatedAt float64       `json:"updatedAt"`
}

func (s *storedObjective) UnmarshalJSON(data []byte) error {
	type objective storedObjective
	var value objective
	raw := struct {
		*objective
		Goal      json.RawMessage `json:"goal"`
		UpdatedAt *float64        `json:"updatedAt"`
	}{objective: &value}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw.Goal) == 0 || raw.UpdatedAt == nil {
		return fmt.Errorf("objective requires goal and updatedAt")
	}
	if err := json.Unmarshal(raw.Goal, &value.Goal); err != nil {
		return err
	}
	value.UpdatedAt = *raw.UpdatedAt
	*s = storedObjective(value)
	return nil
}

type Objectives struct {
	mu    sync.Mutex
	store persist.Store
	now   func() time.Time
}

func NewObjectives(store persist.Store) *Objectives { return &Objectives{store: store, now: time.Now} }

func (o *Objectives) Get(projectID, sessionID string) (SessionGoal, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	stored, err := o.read(projectID, sessionID)
	if err != nil {
		return SessionGoal{}, err
	}
	if stored == nil {
		return SessionGoal{ProjectID: projectID, SessionID: sessionID, Tasks: []SessionTask{}}, nil
	}
	updated := stored.UpdatedAt
	return SessionGoal{ProjectID: projectID, SessionID: sessionID, Goal: stored.Goal, Tasks: stored.Tasks, UpdatedAt: &updated}, nil
}

func (o *Objectives) Update(projectID, sessionID string, goal *string, tasks *[]SessionTask) (SessionGoal, error) {
	if goal == nil && tasks == nil {
		return SessionGoal{}, fmt.Errorf("an objective update requires goal or tasks")
	}
	if err := validateDurableSessionTarget(projectID, sessionID); err != nil {
		return SessionGoal{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	current, err := o.read(projectID, sessionID)
	if err != nil {
		return SessionGoal{}, err
	}
	var nextGoal *string
	var nextTasks []SessionTask
	if current != nil {
		nextGoal, nextTasks = current.Goal, current.Tasks
	}
	if goal != nil {
		normalized, normalizeErr := normalizeGoal(*goal)
		if normalizeErr != nil {
			return SessionGoal{}, normalizeErr
		}
		nextGoal = &normalized
	}
	if tasks != nil {
		if err := validateTasks(*tasks); err != nil {
			return SessionGoal{}, err
		}
		nextTasks = append([]SessionTask(nil), (*tasks)...)
	}
	stored := storedObjective{Version: 2, ProjectID: projectID, SessionID: sessionID, Goal: nextGoal, Tasks: nonNilTasks(nextTasks), UpdatedAt: float64(o.now().UnixMilli())}
	if err := o.write(stored); err != nil {
		return SessionGoal{}, err
	}
	updated := stored.UpdatedAt
	return SessionGoal{ProjectID: projectID, SessionID: sessionID, Goal: stored.Goal, Tasks: stored.Tasks, UpdatedAt: &updated}, nil
}

func (o *Objectives) ClearGoal(projectID, sessionID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	current, err := o.read(projectID, sessionID)
	if err != nil || current == nil {
		return err
	}
	if len(current.Tasks) > 0 {
		current.Goal = nil
		current.UpdatedAt = float64(o.now().UnixMilli())
		return o.write(*current)
	}
	path := filepath.Join(o.store.Dir, objectiveName(projectID, sessionID))
	return removeStoredGenerations(path)
}

// Forget removes every objective field and both persisted schema generations.
// ClearGoal remains separate because clearing a user goal intentionally keeps
// agent-managed tasks.
func (o *Objectives) Forget(projectID, sessionID string) error {
	// Session IDs are opaque Pi values. Unlike user-entered objective IDs,
	// deletion cleanup can safely accept the broader persisted-record contract
	// because objective filenames are SHA-256 keys rather than raw IDs.
	if projectID == "" || sessionID == "" || containsNUL(projectID) || containsNUL(sessionID) {
		return fmt.Errorf("invalid session objective target")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	current := filepath.Join(o.store.Dir, objectiveName(projectID, sessionID))
	legacy := filepath.Join(o.store.Dir, "extensions", "session-goals", objectiveKey(projectID, sessionID)+".json")
	return errors.Join(removeStoredGenerations(current), removeStoredGenerations(legacy))
}

func (o *Objectives) ClearProject(projectID string) error {
	if err := validateIdentity(projectID, "Project id"); err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	directory := filepath.Join(o.store.Dir, "extensions", "session-objectives")
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	validName := regexp.MustCompile(`^[a-f0-9]{64}\.json(?:\.bak)?$`)
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !validName.MatchString(entry.Name()) {
			continue
		}
		content, _, readErr := persist.ReadFile(filepath.Join(directory, entry.Name()))
		if readErr != nil {
			continue
		}
		var identity struct {
			ProjectID string `json:"projectId"`
		}
		if json.Unmarshal(content, &identity) == nil && identity.ProjectID == projectID {
			_ = os.Remove(filepath.Join(directory, entry.Name()))
		}
	}
	return nil
}

func (o *Objectives) read(projectID, sessionID string) (*storedObjective, error) {
	if err := validateDurableSessionTarget(projectID, sessionID); err != nil {
		return nil, err
	}
	var stored storedObjective
	ok, err := persist.Read(o.store, objectiveName(projectID, sessionID), &stored, func(value storedObjective) error { return validateObjective(value, projectID, sessionID) })
	if err != nil {
		return nil, err
	}
	if ok {
		return &stored, nil
	}
	legacyPath := filepath.Join(o.store.Dir, "extensions", "session-goals", objectiveKey(projectID, sessionID)+".json")
	content, _, err := persist.ReadFile(legacyPath)
	if err != nil {
		return nil, nil
	}
	var legacy struct {
		Version     int     `json:"version"`
		WorkspaceID string  `json:"workspaceId"`
		SessionID   string  `json:"sessionId"`
		Goal        string  `json:"goal"`
		UpdatedAt   float64 `json:"updatedAt"`
	}
	if json.Unmarshal(content, &legacy) != nil || legacy.Version != 1 || legacy.WorkspaceID != projectID || legacy.SessionID != sessionID {
		return nil, nil
	}
	goal, err := normalizeGoal(legacy.Goal)
	if err != nil {
		return nil, nil
	}
	updated := legacy.UpdatedAt
	if updated == 0 {
		updated = float64(o.now().UnixMilli())
	}
	stored = storedObjective{Version: 2, ProjectID: projectID, SessionID: sessionID, Goal: &goal, Tasks: []SessionTask{}, UpdatedAt: updated}
	if err := o.write(stored); err != nil {
		return nil, err
	}
	return &stored, nil
}

func (o *Objectives) write(stored storedObjective) error {
	return persist.Write(o.store, objectiveName(stored.ProjectID, stored.SessionID), stored, func(value storedObjective) error { return validateObjective(value, stored.ProjectID, stored.SessionID) })
}

func validateObjective(value storedObjective, projectID, sessionID string) error {
	if validateDurableSessionTarget(projectID, sessionID) != nil || value.Version != 2 || value.ProjectID != projectID || value.SessionID != sessionID || value.Tasks == nil {
		return fmt.Errorf("invalid objective")
	}
	if value.Goal != nil {
		goal, err := normalizeGoal(*value.Goal)
		if err != nil {
			return err
		}
		*value.Goal = goal
	}
	return validateTasks(value.Tasks)
}

func validateTasks(tasks []SessionTask) error {
	if len(tasks) > 200 {
		return fmt.Errorf("task list is invalid")
	}
	seen := make(map[string]bool, len(tasks))
	for index := range tasks {
		task := &tasks[index]
		task.Text = strings.TrimSpace(task.Text)
		if task.ID == "" || utf16Length(task.ID) > 256 || task.Text == "" || utf16Length(task.Text) > 2_000 || containsNUL(task.Text) || !map[string]bool{"pending": true, "active": true, "done": true}[task.Status] || seen[task.ID] {
			return fmt.Errorf("task is invalid")
		}
		seen[task.ID] = true
	}
	return nil
}

func normalizeGoal(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf16Length(value) > 2_000 || containsNUL(value) {
		return "", fmt.Errorf("session goal is invalid")
	}
	return value, nil
}

func validateIdentity(value, label string) error {
	if value == "" || utf16Length(value) > 256 || strings.ContainsAny(value, "\x00/\\") {
		return fmt.Errorf("%s is invalid", label)
	}
	return nil
}

func validatePiSessionID(value string) error {
	// Pi session identifiers are opaque. They are stored as data, never used
	// directly as path components; objective filenames use a digest instead.
	if value == "" || containsNUL(value) {
		return fmt.Errorf("Session id is invalid")
	}
	return nil
}

func validateDurableSessionTarget(projectID, sessionID string) error {
	if err := validateIdentity(projectID, "Project id"); err != nil {
		return err
	}
	return validatePiSessionID(sessionID)
}

func objectiveKey(projectID, sessionID string) string {
	digest := sha256.Sum256([]byte(projectID + "\x00" + sessionID))
	return hex.EncodeToString(digest[:])
}

func objectiveName(projectID, sessionID string) string {
	return filepath.Join("extensions", "session-objectives", objectiveKey(projectID, sessionID)+".json")
}

func removeStoredGenerations(path string) error {
	var removeErrors []error
	for _, candidate := range []string{path, path + ".bak"} {
		if err := os.Remove(candidate); err != nil && !os.IsNotExist(err) {
			removeErrors = append(removeErrors, err)
		}
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		if !os.IsNotExist(err) {
			removeErrors = append(removeErrors, err)
		}
	} else {
		if err := directory.Sync(); err != nil {
			removeErrors = append(removeErrors, err)
		}
		if err := directory.Close(); err != nil {
			removeErrors = append(removeErrors, err)
		}
	}
	return errors.Join(removeErrors...)
}

func utf16Length(value string) int {
	count := 0
	for _, character := range value {
		count += utf16.RuneLen(character)
	}
	return count
}

func nonNilTasks(tasks []SessionTask) []SessionTask {
	if tasks == nil {
		return []SessionTask{}
	}
	return tasks
}
