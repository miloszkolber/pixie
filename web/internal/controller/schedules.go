package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
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
	ID        string `json:"id"`
	SessionID string `json:"sessionId,omitempty"`
	// CreationPending is durably installed before session.create. It remains
	// set when Pixie cannot durably link or settle the resulting native ID, so
	// the occurrence cannot be terminalized or dispatched again by guessing.
	CreationPending         bool       `json:"creationPending,omitempty"`
	StartedAt               time.Time  `json:"startedAt"`
	FinishedAt              *time.Time `json:"finishedAt,omitempty"`
	Status                  string     `json:"status"`
	Error                   string     `json:"error,omitempty"`
	CancellationRequestedAt *time.Time `json:"cancellationRequestedAt,omitempty"`
	CancellationReason      string     `json:"cancellationReason,omitempty"`
}
type Schedule struct {
	ID                string        `json:"id"`
	ProjectID         string        `json:"projectId"`
	Root              string        `json:"root"`
	Prompt            string        `json:"prompt"`
	Cron              string        `json:"cron"`
	Timezone          string        `json:"timezone"`
	Model             *WireModel    `json:"model,omitempty"`
	MaxRuntimeSeconds *int64        `json:"maxRuntimeSeconds"`
	Paused            bool          `json:"paused"`
	NextRun           time.Time     `json:"nextRun"`
	Runs              []ScheduleRun `json:"runs"`
}

type scheduleOperation struct {
	Key         string          `json:"key"`
	Fingerprint string          `json:"fingerprint"`
	Result      json.RawMessage `json:"result"`
}
type scheduleDisk struct {
	Version    int                 `json:"version"`
	Jobs       map[string]Schedule `json:"jobs"`
	Operations []scheduleOperation `json:"operations"`
}

const (
	scheduleRunRunning                 = "running"
	scheduleRunCancelling              = "cancelling"
	scheduleRunCancellationUnconfirmed = "cancellation_unconfirmed"
	scheduleRunCompleted               = "completed"
	scheduleRunFailed                  = "failed"
	scheduleRunInterrupted             = "interrupted"
	scheduleRunTimedOut                = "timed_out"
	scheduleCancellationDeadline       = "deadline"
	scheduleCancellationManual         = "manual"
	scheduleCancellationRestart        = "restart"
)

const (
	defaultScheduleMaxRuntime = 24 * time.Hour
	maxScheduleRuntimeSeconds = int64(^uint64(0)>>1) / int64(time.Second)
)

// ScheduleRuntimePolicy separates the persisted budget from watchdog
// enforcement. Turning deadlines off leaves every stored budget untouched.
type ScheduleRuntimePolicy struct {
	DefaultMaxRuntime time.Duration
	DeadlinesEnabled  bool
}

func DefaultScheduleRuntimePolicy() ScheduleRuntimePolicy {
	return ScheduleRuntimePolicy{DefaultMaxRuntime: defaultScheduleMaxRuntime, DeadlinesEnabled: true}
}

// ParseScheduleRuntimePolicy reads the two process settings once at startup.
// "off" makes new schedules unlimited; an omitted default remains 24 hours.
func ParseScheduleRuntimePolicy(getenv func(string) string) (ScheduleRuntimePolicy, error) {
	policy := DefaultScheduleRuntimePolicy()
	if getenv == nil {
		getenv = os.Getenv
	}
	if raw := strings.TrimSpace(getenv("PIXIE_SCHEDULE_DEFAULT_MAX_RUN")); raw != "" {
		if strings.EqualFold(raw, "off") {
			policy.DefaultMaxRuntime = 0
		} else {
			value, err := time.ParseDuration(raw)
			if err != nil || value < time.Second || value%time.Second != 0 {
				return ScheduleRuntimePolicy{}, fmt.Errorf("PIXIE_SCHEDULE_DEFAULT_MAX_RUN must be off or a whole-second duration of at least 1s")
			}
			policy.DefaultMaxRuntime = value
		}
	}
	if raw := strings.TrimSpace(getenv("PIXIE_SCHEDULE_DEADLINES")); raw != "" {
		switch strings.ToLower(raw) {
		case "on", "1", "true", "yes":
			policy.DeadlinesEnabled = true
		case "off", "0", "false", "no":
			policy.DeadlinesEnabled = false
		default:
			return ScheduleRuntimePolicy{}, fmt.Errorf("PIXIE_SCHEDULE_DEADLINES must be on or off")
		}
	}
	return policy, nil
}

type ScheduleCancellationOutcome struct {
	Confirmed bool
	Reason    string
}

// ScheduleCanceler confirms native cancellation. A returned nil outcome with
// Confirmed false is deliberately nonterminal: Pi may still be working.
type ScheduleCanceler func(context.Context, string) (ScheduleCancellationOutcome, error)

// ScheduleRunner reports the durable session ID before submitting its prompt.
type ScheduleRunner func(context.Context, Schedule, func(string) error) error
type Schedules struct {
	mu         sync.Mutex
	store      persist.Store
	jobs       map[string]Schedule
	operations []scheduleOperation
	operation  *scheduleOperation
	method     string
	// operationResult lets schedule.stop atomically retain its accepted-versus-
	// settled receipt while save persists the cancelling run itself.
	operationResult any
	failures        map[string]time.Time
	lastError       string
	compiled        map[string]struct {
		key  string
		spec cron.Schedule
	}
	running         map[string]context.CancelFunc
	runStarted      map[string]time.Time
	cancelling      map[string]bool
	pending         map[string]ScheduleRun
	pendingCreation map[string]schedulePendingCreation
	run             ScheduleRunner
	cancelRun       ScheduleCanceler
	runtime         ScheduleRuntimePolicy
	validateRoot    func(string, string) (string, error)
	stop            chan struct{}
	started, closed bool
	work            sync.WaitGroup
	publishFaults   persist.PublishFaults
}

// schedulePendingCreation retains an ID received from Pi when writing its
// schedule link failed. It is process-local only; the already durable
// CreationPending claim remains fail-closed across a restart.
type schedulePendingCreation struct {
	runID     string
	sessionID string
}

// scheduleCreationUncertainError says session.create was sent or returned an
// ID, but the runner could not complete its durable local lifecycle. It is not
// a terminal execution failure because native work may still exist.
type scheduleCreationUncertainError struct{ cause error }

func (e *scheduleCreationUncertainError) Error() string {
	if e == nil || e.cause == nil {
		return "native session creation is unresolved"
	}
	return e.cause.Error()
}

func (e *scheduleCreationUncertainError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// SetPublishFaults injects deterministic post-rename faults into schedule
// publication for X04 coverage. Production code leaves it zero-valued.
func (s *Schedules) SetPublishFaults(faults persist.PublishFaults) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishFaults = faults
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
		if j.MaxRuntimeSeconds != nil && !validScheduleMaxRuntimeSeconds(*j.MaxRuntimeSeconds) {
			return fmt.Errorf("invalid schedule maximum runtime")
		}
		if _, err := scheduleNext(j, time.Now()); err != nil {
			return err
		}
		for _, run := range j.Runs {
			if run.ID == "" || run.StartedAt.IsZero() || !validScheduleRunStatus(run.Status) {
				return fmt.Errorf("invalid schedule run")
			}
			if run.CreationPending && run.FinishedAt != nil {
				return fmt.Errorf("pending schedule creation is terminal")
			}
			if run.CancellationReason != "" && !validScheduleCancellationReason(run.CancellationReason) {
				return fmt.Errorf("invalid schedule cancellation reason")
			}
		}
	}
	return nil
}
func validScheduleRunStatus(status string) bool {
	return contains([]string{scheduleRunRunning, scheduleRunCancelling, scheduleRunCancellationUnconfirmed, scheduleRunCompleted, scheduleRunFailed, scheduleRunInterrupted, scheduleRunTimedOut}, status)
}
func validScheduleCancellationReason(reason string) bool {
	return contains([]string{scheduleCancellationDeadline, scheduleCancellationManual, scheduleCancellationRestart}, reason)
}
func NewSchedules(store persist.Store, validateRoot func(string, string) (string, error), run ScheduleRunner) (*Schedules, error) {
	return NewSchedulesWithRuntime(store, validateRoot, run, DefaultScheduleRuntimePolicy())
}

func NewSchedulesWithRuntime(store persist.Store, validateRoot func(string, string) (string, error), run ScheduleRunner, runtime ScheduleRuntimePolicy) (*Schedules, error) {
	if runtime.DefaultMaxRuntime < 0 || runtime.DefaultMaxRuntime%time.Second != 0 {
		return nil, fmt.Errorf("invalid schedule runtime policy")
	}
	s := &Schedules{store: store, jobs: map[string]Schedule{}, running: map[string]context.CancelFunc{}, runStarted: map[string]time.Time{}, cancelling: map[string]bool{}, pending: map[string]ScheduleRun{}, pendingCreation: map[string]schedulePendingCreation{}, run: run, runtime: runtime, validateRoot: validateRoot, stop: make(chan struct{}), failures: map[string]time.Time{}, compiled: map[string]struct {
		key  string
		spec cron.Schedule
	}{}}
	// Isolated runners have no native session boundary. Runtime installs a
	// SessionManager-backed cancellation handler before schedules dispatch.
	s.cancelRun = func(context.Context, string) (ScheduleCancellationOutcome, error) {
		return ScheduleCancellationOutcome{Confirmed: true}, nil
	}
	raw, _, err := persist.ReadFile(filepath.Join(store.Dir, "schedules.json"))
	migrated := false
	if err == nil {
		s.jobs, s.operations, migrated, err = decodeScheduleLedger(raw)
		if err != nil {
			return nil, err
		}
		for _, op := range s.operations {
			if op.Key == "" || op.Fingerprint == "" || !json.Valid(op.Result) {
				return nil, fmt.Errorf("invalid schedule operation")
			}
		}
		if err := validateSchedules(s.jobs); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	} else if _, backupErr := os.Stat(filepath.Join(store.Dir, "schedules.json.bak")); !errors.Is(backupErr, os.ErrNotExist) {
		return nil, fmt.Errorf("schedule primary is missing; refusing an older execution ledger")
	}
	changed := migrated
	now := time.Now().UTC()
	for id, j := range s.jobs {
		for i := range j.Runs {
			if j.Runs[i].CreationPending || j.Runs[i].Status == scheduleRunRunning || j.Runs[i].Status == scheduleRunCancelling {
				// A restart has no proof that Pi stopped. Keep the run nonterminal
				// and paused until an operator retries schedule.stop. A pending
				// creation is equally uncertain: session.create may have committed
				// before the controller lost its reply or durable link.
				j.Runs[i].Status = scheduleRunCancellationUnconfirmed
				j.Runs[i].Error = "Pixie restarted before native session creation or cancellation could be confirmed"
				j.Runs[i].FinishedAt = nil
				j.Runs[i].CancellationReason = scheduleCancellationRestart
				j.Runs[i].CancellationRequestedAt = &now
				j.Paused = true
				changed = true
			}
		}
		s.jobs[id] = j
	}
	if changed {
		if _, err := s.write(nil); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// SetCancellationHandler installs the native confirmation boundary. It is
// separate from ScheduleRunner so schedule.stop can retry an unconfirmed
// cancellation after the original runner has already returned.
func (s *Schedules) SetCancellationHandler(cancel ScheduleCanceler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel != nil {
		s.cancelRun = cancel
	}
}
func (s *Schedules) save(job Schedule) error {
	previous, exists := s.jobs[job.ID]
	s.jobs[job.ID] = job
	outcome, err := s.write(job)
	if err != nil {
		// An uncertain primary stays visible for reconciliation, so the
		// candidate is retained with its mutation identity. Only a
		// known-uncommitted failure restores the prior commit.
		if outcome.Kind != persist.OutcomeDurabilityUncertain {
			if exists {
				s.jobs[job.ID] = previous
			} else {
				delete(s.jobs, job.ID)
			}
		}
		return err
	}
	return nil
}
func (s *Schedules) write(result any) (persist.PublishOutcome, error) {
	if s.jobs == nil || len(s.jobs) > 1000 {
		return persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StageValidate}, fmt.Errorf("too many schedules")
	}
	operations := slices.Clone(s.operations)
	if s.operation != nil {
		op := *s.operation
		if s.method != "schedule.create" && s.method != "schedule.update" {
			result = map[string]any{"ok": true}
			if s.method == "schedule.stop" && s.operationResult != nil {
				result = s.operationResult
			}
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StageValidate}, err
		}
		op.Result = raw
		operations = append(operations, op)
		if len(operations) > 512 {
			operations = operations[len(operations)-512:]
		}
	}
	outcome, err := persist.WriteWithOutcome(s.store, "schedules.json", scheduleDisk{scheduleLedgerVersion, s.jobs, operations}, nil, s.publishFaults)
	decision := DecideSchedulePublish(outcome)
	if decision.MayDispatch {
		s.operations = operations
		return outcome, nil
	}
	if outcome.Kind == persist.OutcomeDurabilityUncertain {
		// Retain the mutation identity so an identical retry reconciles the
		// original operation instead of duplicating work, then reconcile the
		// validated primary without ever restoring the older backup.
		s.operations = operations
		if _, reconcileErr := ReconcileScheduleAfterPublish(s.store, outcome); reconcileErr != nil {
			return outcome, fmt.Errorf("uncertain schedule publish remains unresolved: %w", errors.Join(err, reconcileErr))
		}
		return outcome, err
	}
	return outcome, err
}
func (s *Schedules) next(job Schedule, now time.Time) (time.Time, error) {
	if job.Timezone == "" {
		job.Timezone = "UTC"
	}
	key := job.Timezone + " " + job.Cron
	cached := s.compiled[job.ID]
	if cached.key != key {
		if _, err := scheduleNext(job, now); err != nil {
			return time.Time{}, err
		}
		spec, err := cron.ParseStandard("CRON_TZ=" + job.Timezone + " " + job.Cron)
		if err != nil {
			return time.Time{}, err
		}
		cached.key, cached.spec = key, spec
		s.compiled[job.ID] = cached
	}
	return cached.spec.Next(now), nil
}
func (s *Schedules) failed(id string, now time.Time, err error) {
	if err == nil {
		delete(s.failures, id)
		if len(s.failures) == 0 {
			s.lastError = ""
		}
		return
	}
	s.failures[id] = now.Add(30 * time.Second)
	s.lastError = "A schedule could not be saved or started. Check application storage and schedule configuration."
	slog.Error("schedule operation failed", "schedule", id, "error", err)
}
func (s *Schedules) Health() string { s.mu.Lock(); defer s.mu.Unlock(); return s.lastError }

func cloneSchedule(j Schedule) Schedule {
	j.Runs = slices.Clone(j.Runs)
	if j.MaxRuntimeSeconds != nil {
		maxRuntime := *j.MaxRuntimeSeconds
		j.MaxRuntimeSeconds = &maxRuntime
	}
	for i := range j.Runs {
		if j.Runs[i].FinishedAt != nil {
			finished := *j.Runs[i].FinishedAt
			j.Runs[i].FinishedAt = &finished
		}
		if j.Runs[i].CancellationRequestedAt != nil {
			requested := *j.Runs[i].CancellationRequestedAt
			j.Runs[i].CancellationRequestedAt = &requested
		}
	}
	if j.Model != nil {
		model := *j.Model
		j.Model = &model
	}
	return j
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
	run.CreationPending = false
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
	delete(s.runStarted, id)
	delete(s.pending, id)
	return nil
}

func unsettledScheduleRun(job Schedule) *ScheduleRun {
	for i := range job.Runs {
		if contains([]string{scheduleRunRunning, scheduleRunCancelling, scheduleRunCancellationUnconfirmed}, job.Runs[i].Status) {
			return &job.Runs[i]
		}
	}
	return nil
}

func scheduleRunIndex(job Schedule, runID string) int {
	for i := range job.Runs {
		if job.Runs[i].ID == runID {
			return i
		}
	}
	return -1
}

type scheduleStopResult struct {
	OK       bool   `json:"ok"`
	Status   string `json:"status"`
	Accepted bool   `json:"accepted"`
	Settled  bool   `json:"settled"`
}

func (s *Schedules) requestCancellationLocked(id, reason string) (scheduleStopResult, error) {
	job := cloneSchedule(s.jobs[id])
	run := unsettledScheduleRun(job)
	if run == nil {
		result := scheduleStopResult{OK: true, Status: "settled", Settled: true}
		s.operationResult = result
		if s.operation != nil {
			if _, err := s.write(nil); err != nil {
				return scheduleStopResult{}, err
			}
		}
		return result, nil
	}
	if s.cancelling[id] {
		result := scheduleStopResult{OK: true, Status: scheduleRunCancelling, Accepted: true}
		s.operationResult = result
		if s.operation != nil {
			if _, err := s.write(nil); err != nil {
				return scheduleStopResult{}, err
			}
		}
		return result, nil
	}
	now := time.Now().UTC()
	// A deadline keeps its reason across retries so a later confirmed outcome
	// remains timed_out. A restart record has not sent cancellation yet, so a
	// user's retry becomes a manual interruption.
	if run.CancellationReason != scheduleCancellationDeadline {
		run.CancellationReason = reason
	}
	run.CancellationRequestedAt = &now
	run.Status = scheduleRunCancelling
	run.FinishedAt = nil
	run.Error = ""
	job.Paused = true
	result := scheduleStopResult{OK: true, Status: scheduleRunCancelling, Accepted: true}
	s.operationResult = result
	if err := s.save(job); err != nil {
		return scheduleStopResult{}, err
	}
	s.signalCancellationLocked(id, run.ID)
	return result, nil
}

// signalCancellationLocked starts the effect only after the cancelling state
// is installed. It also resumes an identical schedule.stop after a
// post-rename persistence error: the first request never signals Pi, while
// the retry observes the reconciled durable intent and safely continues it.
func (s *Schedules) signalCancellationLocked(id, runID string) {
	if s.cancelling[id] {
		return
	}
	s.cancelling[id] = true
	if cancel := s.running[id]; cancel != nil {
		// The durable cancelling/paused state is committed before the runner is
		// told to leave its wait. The runner invokes the native confirmation
		// boundary after it has recorded any newly-created session ID.
		cancel()
		return
	}
	s.work.Add(1)
	go func() {
		defer s.work.Done()
		s.reconcileCancellation(id, runID)
	}()
}

func (s *Schedules) reconcileCancellation(id, runID string) {
	s.mu.Lock()
	job := cloneSchedule(s.jobs[id])
	index := scheduleRunIndex(job, runID)
	if index < 0 || job.Runs[index].Status != scheduleRunCancelling {
		delete(s.cancelling, id)
		s.mu.Unlock()
		return
	}
	runSnapshot := job.Runs[index]
	cancel := s.cancelRun
	s.mu.Unlock()

	result := ScheduleCancellationOutcome{Confirmed: false, Reason: "Native session creation is unresolved; wait for its ID before confirming cancellation."}
	var cancelErr error
	if runSnapshot.SessionID != "" && cancel != nil {
		ctx, release := context.WithTimeout(context.Background(), 30*time.Second)
		result, cancelErr = cancel(ctx, runSnapshot.SessionID)
		release()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	job = cloneSchedule(s.jobs[id])
	index = scheduleRunIndex(job, runID)
	if index < 0 || job.Runs[index].Status != scheduleRunCancelling {
		delete(s.cancelling, id)
		return
	}
	run := &job.Runs[index]
	if cancelErr == nil && result.Confirmed {
		now := time.Now().UTC()
		run.FinishedAt = &now
		run.CreationPending = false
		if run.CancellationReason == scheduleCancellationDeadline {
			run.Status = scheduleRunTimedOut
		} else {
			run.Status = scheduleRunInterrupted
		}
		run.Error = ""
		if err := s.save(job); err != nil {
			delete(s.cancelling, id)
			// The durable ledger remains cancelling after this known failure. Do
			// not retain a dead local runner entry that would prevent an explicit
			// schedule.stop retry from reconciling it.
			delete(s.running, id)
			delete(s.runStarted, id)
			delete(s.pending, id)
			s.failed(id, time.Now(), err)
			return
		}
		delete(s.running, id)
		delete(s.runStarted, id)
		delete(s.pending, id)
		delete(s.cancelling, id)
		return
	}
	// Do not infer a native stop from an accepted abort, a deadline, a context
	// timeout, or a transport reset. This state is deliberately nonterminal.
	run.Status = scheduleRunCancellationUnconfirmed
	run.FinishedAt = nil
	run.Error = "Native cancellation could not be confirmed. Retry stop after checking Pi."
	if result.Reason != "" && cancelErr == nil {
		run.Error = result.Reason
	}
	job.Paused = true
	if err := s.save(job); err != nil {
		s.failed(id, time.Now(), err)
	}
	delete(s.running, id)
	delete(s.runStarted, id)
	delete(s.pending, id)
	delete(s.cancelling, id)
}

// reconcilePendingCreationLocked retries only the durable schedule link for a
// native ID that was already returned. It never calls session.create again.
// The runner has stopped short of prompting, so even a successfully linked ID
// stays explicitly uncertain until it is released or stopped.
func (s *Schedules) reconcilePendingCreationLocked(id string, pending schedulePendingCreation) error {
	job := cloneSchedule(s.jobs[id])
	index := scheduleRunIndex(job, pending.runID)
	if index < 0 {
		delete(s.pendingCreation, id)
		return nil
	}
	run := &job.Runs[index]
	if run.SessionID != "" && run.SessionID != pending.sessionID {
		return fmt.Errorf("schedule native session link conflicts with the pending creation")
	}
	run.SessionID = pending.sessionID
	run.CreationPending = true
	resumeCancellation := run.Status == scheduleRunCancelling || (run.Status == scheduleRunCancellationUnconfirmed && run.CancellationRequestedAt != nil && run.CancellationReason != "")
	if resumeCancellation {
		run.Status = scheduleRunCancelling
		run.FinishedAt = nil
		run.Error = ""
	} else {
		run.Status = scheduleRunCancellationUnconfirmed
		run.FinishedAt = nil
		run.Error = "Native session creation is unresolved. Retry stop only after its schedule link is durable."
		job.Paused = true
	}
	if err := s.save(job); err != nil {
		return err
	}
	delete(s.pendingCreation, id)
	if resumeCancellation {
		// A deadline/manual cancellation that became unconfirmed solely because
		// the ID was unavailable resumes automatically once the exact link is
		// durable. Do not require a new schedule.runNow or another create.
		delete(s.cancelling, id)
		s.signalCancellationLocked(id, run.ID)
	}
	return nil
}

// retainCreationUncertainLocked turns an ambiguous session.create outcome into
// a durable, nonterminal claim. A storage error may prevent that richer state
// from being published immediately; the original CreationPending claim was
// committed before session.create and continues to fence new work.
func (s *Schedules) retainCreationUncertainLocked(id, runID string, cause error) error {
	job := cloneSchedule(s.jobs[id])
	index := scheduleRunIndex(job, runID)
	if index < 0 {
		delete(s.running, id)
		delete(s.runStarted, id)
		return nil
	}
	run := &job.Runs[index]
	if pending, ok := s.pendingCreation[id]; ok && pending.runID == runID {
		if run.SessionID != "" && run.SessionID != pending.sessionID {
			delete(s.running, id)
			delete(s.runStarted, id)
			return fmt.Errorf("schedule native session link conflicts with the pending creation")
		}
		run.SessionID = pending.sessionID
	}
	run.CreationPending = true
	run.FinishedAt = nil
	if run.Status != scheduleRunCancelling {
		run.Status = scheduleRunCancellationUnconfirmed
		job.Paused = true
	}
	run.Error = "Native session creation is unresolved; Pixie will not start another run until it is reconciled, released, or stopped."
	if cause != nil {
		run.Error += " " + cause.Error()
	}
	err := s.save(job)
	delete(s.running, id)
	delete(s.runStarted, id)
	delete(s.pending, id)
	return err
}

func (s *Schedules) tick(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	for id, pending := range s.pendingCreation {
		if now.Before(s.failures[id]) {
			continue
		}
		err := s.reconcilePendingCreationLocked(id, pending)
		s.failed(id, now, err)
	}
	for id, run := range s.pending {
		if !now.Before(s.failures[id]) {
			s.failed(id, now, s.finishLocked(id, run))
		}
	}
	if s.runtime.DeadlinesEnabled {
		for id, job := range s.jobs {
			run := unsettledScheduleRun(job)
			if run == nil || run.Status != scheduleRunRunning || job.MaxRuntimeSeconds == nil {
				continue
			}
			if !scheduleDeadlineExceeded(now, s.runStarted[id], run.StartedAt, *job.MaxRuntimeSeconds) {
				continue
			}
			s.failed(id, now, func() error {
				_, err := s.requestCancellationLocked(id, scheduleCancellationDeadline)
				return err
			}())
		}
	}
	for id, j := range s.jobs {
		if len(s.running) >= 8 {
			break
		}
		if !j.Paused && unsettledScheduleRun(j) == nil && !j.NextRun.After(now) && s.running[id] == nil && !now.Before(s.failures[id]) {
			s.failed(id, now, s.startLocked(id, now))
		}
	}
}

// Watchdog evaluates due deadlines without modifying policy configuration. It
// exists for focused tests and uses the same ticker path as the live runner.
func (s *Schedules) Watchdog(now time.Time) { s.tick(now) }

// scheduleDeadlineExceeded reports whether maxSeconds of elapsed runtime have
// passed. A live run has a process-local start that carries its monotonic
// reading, so a wall-clock jump cannot shorten or extend its deadline. A run
// recovered from disk has no monotonic reading; its persisted wall timestamp
// is the only available source and the comparison is wall-based by necessity.
func scheduleDeadlineExceeded(now, localStart, persistedStart time.Time, maxSeconds int64) bool {
	if localStart.IsZero() {
		localStart = persistedStart
	}
	return !now.Before(localStart.Add(time.Duration(maxSeconds) * time.Second))
}

func (s *Schedules) startLocked(id string, now time.Time) error {
	if s.closed {
		return fmt.Errorf("scheduler is closed")
	}
	if len(s.running) >= 8 {
		return fmt.Errorf("schedule concurrency limit reached; retry after a run finishes")
	}
	if s.running[id] != nil || unsettledScheduleRun(s.jobs[id]) != nil {
		return fmt.Errorf("schedule is already running")
	}
	j := cloneSchedule(s.jobs[id])
	if j.ID == "" {
		return fmt.Errorf("unknown schedule")
	}
	next, err := s.next(j, now)
	if err != nil {
		return err
	}
	j.NextRun = next
	// Persist the creation claim before crossing into Pi. A missing native ID is
	// an uncertain external effect, never evidence that no native session exists.
	run := ScheduleRun{ID: identifier.New(), StartedAt: now.UTC(), Status: scheduleRunRunning, CreationPending: true}
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
	// Keep a process-local start reading with its monotonic component so the
	// deadline watchdog measures elapsed runtime instead of trusting the wall
	// clock. The durable StartedAt remains a calendar timestamp for the ledger.
	s.runStarted[id] = now
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
				index := scheduleRunIndex(current, run.ID)
				if index < 0 {
					return &scheduleCreationUncertainError{cause: fmt.Errorf("schedule run disappeared before native session link could be recorded")}
				}
				if current.Runs[index].SessionID != "" && current.Runs[index].SessionID != sessionID {
					return &scheduleCreationUncertainError{cause: fmt.Errorf("schedule native session link conflicts with the recorded run")}
				}
				current.Runs[index].SessionID = sessionID
				current.Runs[index].CreationPending = false
				if current.Runs[index].Status == scheduleRunRunning {
					current.Runs[index].Error = ""
				}
				if err := s.save(current); err != nil {
					s.pendingCreation[id] = schedulePendingCreation{runID: run.ID, sessionID: sessionID}
					return &scheduleCreationUncertainError{cause: err}
				}
				run.SessionID = sessionID
				run.CreationPending = false
				return nil
			})
		}()
		s.mu.Lock()
		current := cloneSchedule(s.jobs[id])
		index := scheduleRunIndex(current, run.ID)
		cancelling := index >= 0 && current.Runs[index].Status == scheduleRunCancelling
		closed := s.closed
		s.mu.Unlock()
		var creationUncertain *scheduleCreationUncertainError
		if errors.As(err, &creationUncertain) {
			s.mu.Lock()
			retainErr := s.retainCreationUncertainLocked(id, run.ID, creationUncertain)
			cancelling = false
			if current = cloneSchedule(s.jobs[id]); scheduleRunIndex(current, run.ID) >= 0 && current.Runs[scheduleRunIndex(current, run.ID)].Status == scheduleRunCancelling {
				if _, pending := s.pendingCreation[id]; pending {
					// The original cancellation goroutine was deliberately held
					// behind the runner. It has now returned, so let the durable
					// link retry start a fresh confirmation once the ID is saved.
					delete(s.cancelling, id)
				} else {
					cancelling = true
				}
			}
			s.failed(id, time.Now(), errors.Join(err, retainErr))
			s.mu.Unlock()
			if cancelling {
				s.reconcileCancellation(id, run.ID)
			}
			return
		}
		if cancelling {
			s.reconcileCancellation(id, run.ID)
			return
		}
		if closed && index >= 0 && current.Runs[index].Status == scheduleRunRunning {
			s.mu.Lock()
			current = cloneSchedule(s.jobs[id])
			index = scheduleRunIndex(current, run.ID)
			if index >= 0 && current.Runs[index].Status == scheduleRunRunning {
				current.Runs[index].Status = scheduleRunCancellationUnconfirmed
				current.Runs[index].CancellationReason = scheduleCancellationRestart
				now := time.Now().UTC()
				current.Runs[index].CancellationRequestedAt = &now
				current.Runs[index].Error = "Pixie stopped before native cancellation could be confirmed"
				current.Paused = true
				if saveErr := s.save(current); saveErr != nil {
					s.failed(id, time.Now(), saveErr)
				}
				delete(s.running, id)
				delete(s.runStarted, id)
			}
			s.mu.Unlock()
			return
		}
		now := time.Now().UTC()
		run.FinishedAt = &now
		run.CreationPending = false
		run.Status = scheduleRunCompleted
		if err != nil {
			run.Status = scheduleRunFailed
			run.Error = err.Error()
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.finishLocked(id, run); err != nil {
			s.pending[id] = run
			s.failed(id, time.Now(), err)
		}
	}()
	return nil
}

func runtimeSecondsFromDuration(value time.Duration) *int64 {
	if value <= 0 {
		return nil
	}
	seconds := int64(value / time.Second)
	return &seconds
}

func validScheduleMaxRuntimeSeconds(seconds int64) bool {
	return seconds >= 1 && seconds <= maxScheduleRuntimeSeconds
}

func parseScheduleMaxRuntime(value any) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	if text, ok := value.(string); ok && strings.EqualFold(strings.TrimSpace(text), "off") {
		return nil, nil
	}
	seconds := integerValue(value)
	if !validScheduleMaxRuntimeSeconds(seconds) {
		return nil, fmt.Errorf("maximum runtime must be a whole number of seconds or off")
	}
	// integerValue returns zero for fractional floats, so reject values that it
	// cannot represent exactly instead of silently shortening a budget.
	if numeric, ok := value.(float64); ok && numeric != float64(seconds) {
		return nil, fmt.Errorf("maximum runtime must be a whole number of seconds or off")
	}
	return &seconds, nil
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
	if method == "schedule.health" {
		return map[string]string{"error": s.lastError}, nil
	}
	if method == "schedule.preview" {
		if s.validateRoot != nil {
			if _, err := s.validateRoot(projectID, textValue(p["root"])); err != nil {
				return nil, err
			}
		}
		job := Schedule{Cron: textValue(p["cron"]), Timezone: textValue(p["timezone"])}
		if job.Timezone == "" {
			job.Timezone = "UTC"
		}
		next, err := scheduleNext(job, time.Now())
		if err != nil {
			return nil, err
		}
		return map[string]any{"timezone": job.Timezone, "nextRun": next}, nil
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
	identity := queueIdentity(ctx)
	if key := textValue(p["mutationId"]); key != "" {
		if len(key) > 128 || containsNUL(key) {
			return nil, fmt.Errorf("invalid mutation identity")
		}
		digest := sha256.Sum256([]byte(projectID + "\x00" + key))
		payload, _ := json.Marshal(p)
		fingerprint := sha256.Sum256(append([]byte(method+"\x00"), payload...))
		identity = queueRequestIdentity{hex.EncodeToString(digest[:]), hex.EncodeToString(fingerprint[:])}
	}
	if identity.Key != "" {
		for _, op := range s.operations {
			if op.Key != identity.Key {
				continue
			}
			if op.Fingerprint != identity.Fingerprint {
				return nil, fmt.Errorf("schedule mutation identity reused with different input")
			}
			if method == "schedule.stop" {
				id := textValue(p["scheduleId"])
				if job := s.jobs[id]; job.ID != "" && job.ProjectID == projectID {
					if run := unsettledScheduleRun(job); run != nil && run.Status == scheduleRunCancelling {
						s.signalCancellationLocked(id, run.ID)
					}
				}
			}
			if method == "schedule.create" || method == "schedule.update" {
				var job Schedule
				err := json.Unmarshal(op.Result, &job)
				return job, err
			}
			var result any
			err := json.Unmarshal(op.Result, &result)
			return result, err
		}
		s.operation = &scheduleOperation{Key: identity.Key, Fingerprint: identity.Fingerprint}
		s.method = method
		defer func() { s.operation = nil; s.method = ""; s.operationResult = nil }()
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
		j = Schedule{ID: identifier.New(), ProjectID: projectID, Root: root, Prompt: textValue(p["prompt"]), Cron: textValue(p["cron"]), Timezone: textValue(p["timezone"]), MaxRuntimeSeconds: runtimeSecondsFromDuration(s.runtime.DefaultMaxRuntime), Runs: []ScheduleRun{}}
		if value, ok := p["maxRuntimeSeconds"]; ok {
			maxRuntime, err := parseScheduleMaxRuntime(value)
			if err != nil {
				return nil, err
			}
			j.MaxRuntimeSeconds = maxRuntime
		}
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
			if run := unsettledScheduleRun(j); run != nil && contains([]string{scheduleRunCancelling, scheduleRunCancellationUnconfirmed}, run.Status) {
				return nil, fmt.Errorf("reconcile the unconfirmed cancellation before editing this schedule")
			}
			if s.running[id] != nil {
				for _, key := range []string{"cron", "timezone", "prompt", "maxRuntimeSeconds"} {
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
			if value, ok := p["maxRuntimeSeconds"]; ok {
				maxRuntime, err := parseScheduleMaxRuntime(value)
				if err != nil {
					return nil, err
				}
				j.MaxRuntimeSeconds = maxRuntime
			}
		}
		if j.Timezone == "" {
			j.Timezone = "UTC"
		}
		if strings.TrimSpace(j.Prompt) == "" || len(j.Prompt) > 64*1024 || containsNUL(j.Prompt) {
			return nil, fmt.Errorf("invalid schedule prompt")
		}
		next, err := s.next(j, time.Now())
		if err != nil {
			return nil, err
		}
		j.NextRun = next
		if err := s.save(j); err != nil {
			return nil, err
		}
		return cloneSchedule(j), nil
	case "schedule.delete":
		if unsettledScheduleRun(j) != nil || s.running[id] != nil {
			return nil, fmt.Errorf("stop the running schedule before deleting it")
		}
		delete(s.jobs, id)
		if outcome, err := s.write(nil); err != nil {
			// An uncertain delete stays applied after reconciling the
			// validated primary; only a known-uncommitted failure restores
			// the prior entry.
			if outcome.Kind != persist.OutcomeDurabilityUncertain {
				s.jobs[id] = j
			}
			return nil, err
		}
		delete(s.compiled, id)
		delete(s.failures, id)
		if len(s.failures) == 0 {
			s.lastError = ""
		}
		return ack(nil)
	case "schedule.runNow":
		// Pause gates automatic dispatch only: a paused schedule still
		// accepts an explicit manual run, which records one execution like
		// any other. Stopping settles the active execution without clearing
		// the paused flag, so future automatic dispatch stays held.
		return ack(s.startLocked(id, time.Now()))
	case "schedule.stop":
		return s.requestCancellationLocked(id, scheduleCancellationManual)
	default:
		return nil, fmt.Errorf("unknown schedule operation")
	}
}
func (m *SessionManager) runSchedule(ctx context.Context, j Schedule, admitted func(string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Once session.create has begun, cancellation must not discard its reply.
	// The scheduler's original context still gates prompting below, while this
	// detached creation context lets the authoritative native ID reach the
	// durable schedule callback first.
	creationCtx := context.WithoutCancel(ctx)
	nativeCreated := false
	result, after, err := m.create(creationCtx, j.ProjectID, j.Root, j.Model, "", "", func(sessionID string) error {
		nativeCreated = true
		m.mu.Lock()
		if m.scheduleCreationRoots == nil {
			m.scheduleCreationRoots = make(map[string]string)
		}
		m.scheduleCreationRoots[sessionID] = j.Root
		m.mu.Unlock()
		return admitted(sessionID)
	})
	if after != nil {
		after()
	}
	if err != nil {
		var uncertain *sessionCreateUncertainError
		if nativeCreated || errors.As(err, &uncertain) {
			return &scheduleCreationUncertainError{cause: err}
		}
		return err
	}
	id := textValue(result["sessionId"])
	if id == "" {
		return &scheduleCreationUncertainError{cause: fmt.Errorf("scheduled session has no ID")}
	}
	m.mu.Lock()
	delete(m.scheduleCreationRoots, id)
	m.mu.Unlock()
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
		// Schedules persist cancelling before ending this wait. The scheduler,
		// not this runner, owns the native Stop confirmation and records either
		// a terminal outcome or cancellation_unconfirmed.
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
