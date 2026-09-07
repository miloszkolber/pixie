package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/miloszkolber/pixie/internal/identifier"
	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/robfig/cron/v3"
)

type ScheduleRun struct {
	ID         string     `json:"id"`
	SessionID  string     `json:"sessionId,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Status     string     `json:"status"`
	Error      string     `json:"error,omitempty"`
}
type Schedule struct {
	ID        string        `json:"id"`
	ProjectID string        `json:"projectId"`
	Root      string        `json:"root"`
	Prompt    string        `json:"prompt"`
	Cron      string        `json:"cron"`
	Timezone  string        `json:"timezone"`
	Model     *WireModel    `json:"model,omitempty"`
	Paused    bool          `json:"paused"`
	NextRun   time.Time     `json:"nextRun"`
	Runs      []ScheduleRun `json:"runs"`
}

// ScheduleRunner reports the durable session ID before submitting its prompt.
type ScheduleRunner func(context.Context, Schedule, func(string) error) error
type Schedules struct {
	mu              sync.Mutex
	store           persist.Store
	jobs            map[string]Schedule
	running         map[string]context.CancelFunc
	pending         map[string]ScheduleRun
	run             ScheduleRunner
	validateRoot    func(string, string) (string, error)
	stop            chan struct{}
	started, closed bool
	work            sync.WaitGroup
}

func scheduleNext(job Schedule, now time.Time) (time.Time, error) {
	if len(strings.Fields(job.Cron)) != 5 || len(job.Cron) > 512 {
		return time.Time{}, fmt.Errorf("schedule needs a five-field cron expression")
	}
	zone := job.Timezone
	if zone == "" {
		zone = "UTC"
	}
	if strings.ContainsAny(zone, " \t\r\n") {
		return time.Time{}, fmt.Errorf("invalid schedule timezone")
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return time.Time{}, fmt.Errorf("invalid schedule timezone")
	}
	spec, err := cron.ParseStandard("CRON_TZ=" + zone + " " + job.Cron)
	if err != nil {
		return time.Time{}, err
	}
	next := spec.Next(now)
	if next.IsZero() {
		return next, fmt.Errorf("schedule has no next occurrence")
	}
	return next, nil
}
func validateSchedules(jobs map[string]Schedule) error {
	if jobs == nil || len(jobs) > 1000 {
		return fmt.Errorf("invalid schedule store")
	}
	for id, j := range jobs {
		if id != j.ID || id == "" || j.ProjectID == "" || j.Root == "" || strings.TrimSpace(j.Prompt) == "" || len(j.Prompt) > 64*1024 || strings.ContainsRune(j.Prompt, 0) || len(j.Runs) > 100 {
			return fmt.Errorf("invalid schedule")
		}
		if _, err := scheduleNext(j, time.Now()); err != nil {
			return err
		}
	}
	return nil
}
func NewSchedules(store persist.Store, validateRoot func(string, string) (string, error), run ScheduleRunner) (*Schedules, error) {
	s := &Schedules{store: store, jobs: map[string]Schedule{}, running: map[string]context.CancelFunc{}, pending: map[string]ScheduleRun{}, run: run, validateRoot: validateRoot, stop: make(chan struct{})}
	raw, _, err := persist.ReadFile(filepath.Join(store.Dir, "schedules.json"))
	if err == nil {
		if err := persist.Decode(raw, &s.jobs, validateSchedules); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	} else if _, backupErr := os.Stat(filepath.Join(store.Dir, "schedules.json.bak")); !errors.Is(backupErr, os.ErrNotExist) {
		return nil, fmt.Errorf("schedule primary is missing; refusing an older execution ledger")
	}
	changed := false
	now := time.Now().UTC()
	for id, j := range s.jobs {
		for i := range j.Runs {
			if j.Runs[i].Status == "running" {
				j.Runs[i].Status = "interrupted"
				j.Runs[i].Error = "Pixie restarted; inspect the session before retrying"
				j.Runs[i].FinishedAt = &now
				j.Paused = true
				changed = true
			}
		}
		s.jobs[id] = j
	}
	if changed {
		if err := persist.Write(store, "schedules.json", s.jobs, validateSchedules); err != nil {
			return nil, err
		}
	}
	return s, nil
}
func (s *Schedules) save(job Schedule) error {
	previous, exists := s.jobs[job.ID]
	s.jobs[job.ID] = job
	if err := persist.Write(s.store, "schedules.json", s.jobs, validateSchedules); err != nil {
		if exists {
			s.jobs[job.ID] = previous
		} else {
			delete(s.jobs, job.ID)
		}
		return err
	}
	return nil
}
func cloneSchedule(j Schedule) Schedule {
	raw, _ := json.Marshal(j)
	var copy Schedule
	_ = json.Unmarshal(raw, &copy)
	return copy
}
func (s *Schedules) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.closed {
		return
	}
	s.started = true
	s.work.Add(1)
	go func() {
		defer s.work.Done()
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-s.stop:
				return
			case now := <-timer.C:
				s.tick(now)
			}
		}
	}()
}
func (s *Schedules) Close(ctx context.Context) {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.stop)
		for _, cancel := range s.running {
			cancel()
		}
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.work.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
func (s *Schedules) finishLocked(id string, run ScheduleRun) error {
	job := cloneSchedule(s.jobs[id])
	for i := range job.Runs {
		if job.Runs[i].ID == run.ID {
			job.Runs[i] = run
		}
	}
	if err := s.save(job); err != nil {
		return err
	}
	delete(s.running, id)
	delete(s.pending, id)
	return nil
}
func (s *Schedules) tick(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	for id, run := range s.pending {
		_ = s.finishLocked(id, run)
	}
	for id, j := range s.jobs {
		if !j.Paused && !j.NextRun.After(now) && s.running[id] == nil {
			_ = s.startLocked(id, now)
		}
	}
}
func (s *Schedules) startLocked(id string, now time.Time) error {
	if s.closed {
		return fmt.Errorf("scheduler is closed")
	}
	if s.running[id] != nil {
		return fmt.Errorf("schedule is already running")
	}
	j := cloneSchedule(s.jobs[id])
	if j.ID == "" {
		return fmt.Errorf("unknown schedule")
	}
	next, err := scheduleNext(j, now)
	if err != nil {
		return err
	}
	j.NextRun = next
	run := ScheduleRun{ID: identifier.New(), StartedAt: now.UTC(), Status: "running"}
	j.Runs = append([]ScheduleRun{run}, j.Runs...)
	if len(j.Runs) > 100 {
		j.Runs = j.Runs[:100]
	}
	// Claim the occurrence before dispatch. An ambiguous restart pauses it instead of repeating work.
	if err := s.save(j); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.running[id] = cancel
	s.work.Add(1)
	go func() {
		defer s.work.Done()
		defer cancel()
		err := func() error {
			if s.run == nil {
				return fmt.Errorf("schedule runner is unavailable")
			}
			if s.validateRoot != nil {
				if _, err := s.validateRoot(j.ProjectID, j.Root); err != nil {
					return err
				}
			}
			return s.run(ctx, j, func(sessionID string) error {
				s.mu.Lock()
				defer s.mu.Unlock()
				current := cloneSchedule(s.jobs[id])
				for i := range current.Runs {
					if current.Runs[i].ID == run.ID {
						current.Runs[i].SessionID = sessionID
					}
				}
				if err := s.save(current); err != nil {
					return err
				}
				run.SessionID = sessionID
				return nil
			})
		}()
		now := time.Now().UTC()
		run.FinishedAt = &now
		run.Status = "completed"
		if err != nil {
			run.Status = "failed"
			run.Error = err.Error()
		}
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			run.Status = "interrupted"
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.finishLocked(id, run) != nil {
			s.pending[id] = run
		}
	}()
	return nil
}
func (s *Schedules) Handle(ctx context.Context, method string, p map[string]any) (any, error) {
	projectID := textValue(p["projectId"])
	if projectID == "" {
		return nil, fmt.Errorf("project is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("scheduler is closed")
	}
	if method == "schedule.list" {
		jobs := []Schedule{}
		for _, j := range s.jobs {
			if j.ProjectID == projectID {
				jobs = append(jobs, cloneSchedule(j))
			}
		}
		sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
		return jobs, nil
	}
	id := textValue(p["scheduleId"])
	j := cloneSchedule(s.jobs[id])
	if method == "schedule.create" {
		root := textValue(p["root"])
		if s.validateRoot != nil {
			var err error
			root, err = s.validateRoot(projectID, root)
			if err != nil {
				return nil, err
			}
		}
		j = Schedule{ID: identifier.New(), ProjectID: projectID, Root: root, Prompt: textValue(p["prompt"]), Cron: textValue(p["cron"]), Timezone: textValue(p["timezone"]), Runs: []ScheduleRun{}}
		if model, ok := p["model"]; ok {
			raw, _ := json.Marshal(model)
			if json.Unmarshal(raw, &j.Model) != nil || j.Model == nil || j.Model.ID == "" || j.Model.Provider == "" {
				return nil, fmt.Errorf("invalid schedule model")
			}
		}
	} else if j.ID == "" || j.ProjectID != projectID {
		return nil, fmt.Errorf("unknown project schedule")
	}
	switch method {
	case "schedule.create", "schedule.update":
		if method == "schedule.update" {
			if s.running[id] != nil {
				for _, key := range []string{"cron", "timezone", "prompt"} {
					if _, exists := p[key]; exists {
						return nil, fmt.Errorf("stop the running schedule before editing it")
					}
				}
			}
			for key, target := range map[string]*string{"cron": &j.Cron, "timezone": &j.Timezone, "prompt": &j.Prompt} {
				if v, ok := p[key]; ok {
					value, ok := v.(string)
					if !ok {
						return nil, fmt.Errorf("invalid %s", key)
					}
					*target = value
				}
			}
			if v, ok := p["paused"]; ok {
				paused, ok := v.(bool)
				if !ok {
					return nil, fmt.Errorf("invalid paused state")
				}
				j.Paused = paused
			}
		}
		if j.Timezone == "" {
			j.Timezone = "UTC"
		}
		next, err := scheduleNext(j, time.Now())
		if err != nil {
			return nil, err
		}
		j.NextRun = next
		if err := s.save(j); err != nil {
			return nil, err
		}
		return cloneSchedule(j), nil
	case "schedule.delete":
		if s.running[id] != nil {
			return nil, fmt.Errorf("stop the running schedule before deleting it")
		}
		delete(s.jobs, id)
		if err := persist.Write(s.store, "schedules.json", s.jobs, validateSchedules); err != nil {
			s.jobs[id] = j
			return nil, err
		}
		return ack(nil)
	case "schedule.runNow":
		return ack(s.startLocked(id, time.Now()))
	case "schedule.stop":
		if cancel := s.running[id]; cancel != nil {
			cancel()
		}
		return ack(nil)
	default:
		return nil, fmt.Errorf("unknown schedule operation")
	}
}
func (m *SessionManager) runSchedule(ctx context.Context, j Schedule, admitted func(string) error) error {
	result, err := m.Create(ctx, j.ProjectID, j.Root, j.Model, "", "")
	if err != nil {
		return err
	}
	id := textValue(result["sessionId"])
	if id == "" {
		return fmt.Errorf("scheduled session has no ID")
	}
	if err := admitted(id); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.Prompt(ctx, id, j.Prompt, nil, nil); err != nil {
		return err
	}
	entry, err := m.entry(id)
	if err != nil {
		return err
	}
	defer m.releaseEntry(entry)
	entry.state.Lock()
	done := entry.promptDone
	entry.state.Unlock()
	select {
	case <-ctx.Done():
		cancelCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = m.Abort(cancelCtx, id)
		return ctx.Err()
	case <-done:
	}
	entry.state.Lock()
	defer entry.state.Unlock()
	if entry.settlement != nil && contains([]string{"aborted", "cancelled", "canceled"}, entry.settlement.StopReason) {
		return context.Canceled
	}
	if entry.settlement != nil && entry.settlement.StopReason == "error" {
		return fmt.Errorf("%s", entry.settlement.ErrorMessage)
	}
	return nil
}
