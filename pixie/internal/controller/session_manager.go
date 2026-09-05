package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miloszkolber/pixie/internal/identifier"
	piwire "github.com/miloszkolber/pixie/internal/piprotocol"
	"github.com/miloszkolber/pixie/internal/workspace"
)

const (
	maxQueuedMessages          = 20
	maxQueueRecoveryWorkers    = 4
	inactiveProjectionMaxCount = 24
	inactiveProjectionMaxBytes = 8 * 1024 * 1024
	maxPendingCommandCatalogs  = 32
)

var errAgentIdentityChanged = errors.New("connected Pi agent identity changed")

type SessionPublisher func(channel string, data any)

type sessionEntry struct {
	capabilities map[string]int
	op           sessionOperationGate
	state        sync.Mutex
	// Protected by SessionManager.mu, including while waiting for op.
	refs      int
	ephemeral bool

	projectID          string
	cwd                string
	parentSessionID    string
	title              string
	model              *WireModel
	thinkingLevel      string
	configOptions      []any
	messages           []any
	streaming          bool
	promptActive       bool
	promptDone         chan struct{}
	toolChanged        chan struct{}
	settlement         *SessionSettlement
	stats              SessionStats
	messageUsage       map[string]messageUsage
	queue              sessionQueueState
	runID              string
	objectiveToken     string
	attached           uint64
	replay             *sessionEntry
	promptGeneration   uint64
	projectionID       string
	inactiveAt         time.Time
	inactiveBytes      int // Encoded size under state; zero means a mutation needs recounting.
	pendingEcho        *userEcho
	userResourceBytes  int
	promptAcknowledged bool
	pendingToolOutputs map[string]toolOutput
	appAttachments     map[string]appAttachmentState
	commands           []map[string]any
	planState          *SessionPlanState
	agentIdentity      string
	consumedQuestions  map[string]bool
	drainScheduled     bool
	drainFailures      uint8
	drainRetry         *time.Timer
}

type userEcho struct {
	text            string
	offset          int
	images          []map[string]any
	resources       []map[string]any
	matched         []bool
	resourceMatched []bool
}

type pendingQuestion struct {
	sessionID string
	args      map[string]any
	result    chan map[string]any
}

type sessionLease struct {
	ProjectID string `json:"projectId"`
	SessionID string `json:"sessionId"`
}

type clientSessionLeases struct {
	revision uint64
	sessions map[string]string // Session ID to project ID; owned by SessionManager.mu.
}

type SessionManager struct {
	mu           sync.Mutex
	work         sync.WaitGroup
	closed       bool
	client       *PiClient
	projects     *workspace.Projects
	policy       *workspace.PathPolicy
	records      *SessionRecords
	queues       *SessionQueues
	objectives   *Objectives
	deletions    *SessionDeletions
	objectiveURL string
	settings     *Settings
	sessions     map[string]*sessionEntry
	leases       map[string]*clientSessionLeases
	lifecycle    map[string]bool

	questions       map[questionKey]*pendingQuestion
	creating        int
	pendingCommands map[string]pendingCommandCatalog
	publish         SessionPublisher
	now             func() time.Time
	deviceCode      func(map[string]any)
	history         *HistoryIndex
}

type pendingCommandCatalog struct {
	generation uint64
	commands   []map[string]any
}

func NewSessionManager(projects *workspace.Projects, policy *workspace.PathPolicy, records *SessionRecords, queues *SessionQueues, objectives *Objectives, publish SessionPublisher) *SessionManager {
	var deletions *SessionDeletions
	if records != nil {
		deletions = NewSessionDeletions(records.store)
	}
	manager := &SessionManager{projects: projects, policy: policy, records: records, queues: queues, objectives: objectives, deletions: deletions, sessions: make(map[string]*sessionEntry), questions: make(map[questionKey]*pendingQuestion), publish: publish, now: time.Now}
	manager.history = newHistoryIndex(manager)
	return manager
}

func (m *SessionManager) SetClient(client *PiClient)     { m.client = client }
func (m *SessionManager) SetSettings(settings *Settings) { m.settings = settings }

func (m *SessionManager) SetObjectiveURL(url string) { m.objectiveURL = url }

func (m *SessionManager) RecordedCWD(projectID, sessionID string) (string, error) {
	records, err := m.records.List()
	if err != nil {
		return "", err
	}
	for _, record := range records {
		if record.ProjectID == projectID && record.SessionID == sessionID {
			return record.CWD, nil
		}
	}
	return "", fmt.Errorf("unknown session: %s", sessionID)
}

func (m *SessionManager) Create(ctx context.Context, projectID, cwd string, model *WireModel, thinking, clientKey string) (map[string]any, error) {
	result, after, err := m.create(ctx, projectID, cwd, model, thinking, clientKey)
	if after != nil {
		after()
	}
	return result, err
}

func (m *SessionManager) CreateDeferred(ctx context.Context, projectID, cwd string, model *WireModel, thinking, clientKey string) (any, error) {
	result, after, err := m.create(ctx, projectID, cwd, model, thinking, clientKey)
	if err != nil {
		return nil, err
	}
	return deferredResponse{result: result, after: after}, nil
}

func (m *SessionManager) create(ctx context.Context, projectID, cwd string, model *WireModel, thinking, clientKey string) (map[string]any, func(), error) {
	if m.client == nil {
		return nil, nil, fmt.Errorf("Pi agent client is not configured")
	}
	admitted, err := m.projects.AssertCWD(projectID, cwd)
	if err != nil {
		return nil, nil, err
	}
	token := identifier.New()
	generation, profile, err := m.client.Profile(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !profile.Compatible {
		return nil, nil, unsupportedAgentCapability(strings.Join(profile.MissingRequired, " and "))
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, nil, fmt.Errorf("session manager has been shut down")
	}
	m.creating++
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.creating--
		if m.creating == 0 {
			m.pendingCommands = nil
		}
		m.mu.Unlock()
	}()
	ctx = context.WithValue(ctx, connectionGenerationKey{}, generation)
	servers, err := m.sessionServers(profile, token)
	if err != nil {
		return nil, nil, err
	}
	response, err := m.client.NewSession(ctx, piwire.NewSessionRequest{Cwd: admitted, McpServers: servers, Meta: map[string]any{"projectId": projectID}})
	if err != nil {
		return nil, nil, err
	}
	sessionID := string(response.SessionId)
	if sessionID == "" {
		return nil, nil, fmt.Errorf("Pi agent response is missing sessionId")
	}
	_, err = m.client.Ready(ctx)
	if err != nil {
		return nil, nil, err
	}
	entry := newSessionEntry(sessionID, projectID, admitted, "", token)
	entry.capabilities = response.Capabilities
	entry.configOptions = jsonValues(response.ConfigOptions)
	entry.thinkingLevel = thinkingFromOptions(entry.configOptions)
	entry.model = modelFromSetup(entry.configOptions, response.Meta)
	entry.attached = generation
	entry.agentIdentity = agentProfileIdentity(profile, generation)
	if err := m.records.Record(ProjectSessionRecord{ProjectID: projectID, SessionID: sessionID, CWD: admitted, Title: entry.title}); err != nil {
		return nil, nil, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, nil, fmt.Errorf("session manager has been shut down")
	}
	m.sessions[sessionID] = entry
	if pending, ok := m.pendingCommands[sessionID]; ok && pending.generation == generation {
		entry.commands = pending.commands
		delete(m.pendingCommands, sessionID)
	}
	entry.refs++ // Configuration and the response must survive concurrent lease reconciliation.
	m.retainSessionLocked(clientKey, sessionID, projectID)
	m.mu.Unlock()
	releaseEntry := true
	defer func() {
		if releaseEntry {
			m.releaseEntry(entry)
		}
	}()
	m.emit("session.lifecycleChanged", map[string]any{"projectId": projectID, "sessionId": sessionID, "operation": "created"})
	if model != nil {
		if err := m.SetModel(ctx, sessionID, *model); err != nil {
			return nil, nil, err
		}
	}
	if thinking != "" {
		if err := m.SetThinking(ctx, sessionID, thinking); err != nil {
			return nil, nil, err
		}
	}
	entry.state.Lock()
	result := map[string]any{
		"sessionId":     sessionID,
		"model":         entry.model,
		"thinkingLevel": entry.thinkingLevel,
		"commands":      cloneSlashCommands(entry.commands),
	}
	var once sync.Once
	releaseEntry = false
	after := func() {
		once.Do(func() {
			entry.state.Unlock()
			m.releaseEntry(entry)
		})
	}
	return result, after, nil
}

func (m *SessionManager) EnsureAttached(ctx context.Context, sessionID, projectID, cwd string) (*sessionEntry, error) {
	if m.client == nil {
		return nil, fmt.Errorf("Pi agent client is not configured")
	}
	admitted, err := m.projects.AssertCWD(projectID, cwd)
	if err != nil {
		return nil, err
	}
	entry, _ := m.entry(sessionID)
	if entry == nil {
		records, err := m.records.List()
		if err != nil {
			return nil, err
		}
		var record *ProjectSessionRecord
		for index := range records {
			candidate := &records[index]
			if candidate.ProjectID == projectID && candidate.SessionID == sessionID && candidate.CWD == admitted {
				record = candidate
				break
			}
		}
		if record == nil {
			return nil, fmt.Errorf("unknown session: %s", sessionID)
		}
		entry = newSessionEntry(sessionID, projectID, admitted, record.ParentSessionID, identifier.New())
		if record.Title != "" {
			entry.title = record.Title
		}
		queue, queueErr := m.restoredQueue(projectID, sessionID)
		if queueErr != nil {
			return nil, queueErr
		}
		entry.queue = queue
		m.mu.Lock()
		if m.closed || m.lifecycle[sessionID] {
			m.mu.Unlock()
			return nil, fmt.Errorf("wait for the chat lifecycle operation to finish")
		}
		if existing := m.sessions[sessionID]; existing != nil {
			entry = existing
		} else {
			m.sessions[sessionID] = entry
		}
		entry.refs++
		m.mu.Unlock()
	}
	if entry.projectID != projectID || entry.cwd != admitted {
		m.releaseEntry(entry)
		return nil, fmt.Errorf("unknown session: %s", sessionID)
	}
	if err := m.lockEntryContext(ctx, sessionID, entry); err != nil {
		m.releaseEntry(entry)
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		entry.op.Unlock()
		m.releaseEntry(entry)
		return nil, err
	}
	err = m.attachLocked(ctx, sessionID, entry)
	entry.op.Unlock()
	if err != nil {
		m.releaseEntry(entry)
		return nil, err
	}
	return entry, nil
}

func (m *SessionManager) attachLocked(ctx context.Context, sessionID string, entry *sessionEntry) error {
	m.mu.Lock()
	current := !m.closed && m.sessions[sessionID] == entry
	m.mu.Unlock()
	if !current {
		return fmt.Errorf("session changed while waiting for an operation")
	}
	cwd, err := m.RecordedCWD(entry.projectID, sessionID)
	if err != nil || cwd != entry.cwd {
		return fmt.Errorf("unknown session: %s", sessionID)
	}
	if _, err := m.projects.AssertCWD(entry.projectID, cwd); err != nil {
		return err
	}
	generation, profile, err := m.client.Profile(ctx)
	if err != nil {
		return err
	}
	if !profile.Compatible {
		return unsupportedAgentCapability(strings.Join(profile.MissingRequired, " and "))
	}
	identity := agentProfileIdentity(profile, generation)
	entry.state.Lock()
	if entry.agentIdentity != "" && entry.agentIdentity != identity {
		if entry.drainRetry != nil {
			entry.drainRetry.Stop()
			entry.drainRetry = nil
		}
		entry.state.Unlock()
		return fmt.Errorf("%w; reopen this chat only after restoring the original agent", errAgentIdentityChanged)
	}
	alreadyAttached := entry.attached == generation
	entry.state.Unlock()
	if alreadyAttached {
		m.scheduleFollowUp(sessionID, entry)
		return nil
	}
	m.cancelQuestions(sessionID)
	entry.state.Lock()
	replay := newSessionEntry(sessionID, entry.projectID, entry.cwd, entry.parentSessionID, entry.objectiveToken)
	replay.title = entry.title
	replay.commands = cloneSlashCommands(entry.commands)
	replay.agentIdentity = identity
	replay.attached = generation
	entry.replay = replay
	entry.state.Unlock()
	ctx = context.WithValue(ctx, connectionGenerationKey{}, generation)
	servers, serverErr := m.sessionServers(profile, entry.objectiveToken)
	var response piwire.LoadSessionResponse
	err = serverErr
	if err == nil {
		response, err = m.client.LoadSession(ctx, piwire.LoadSessionRequest{SessionId: piwire.SessionId(sessionID), Cwd: entry.cwd, McpServers: servers})
	}
	if err == nil {
		var currentGeneration uint64
		currentGeneration, err = m.client.Ready(ctx)
		if err == nil && currentGeneration != generation {
			err = fmt.Errorf("Pi connection changed while loading the session")
		}
	}
	if err != nil {
		entry.state.Lock()
		entry.replay = nil
		entry.state.Unlock()
		return err
	}
	entry.state.Lock()
	replay.capabilities = response.Capabilities
	replay.configOptions = jsonValues(response.ConfigOptions)
	replay.model = modelFromSetup(replay.configOptions, response.Meta)
	replay.thinkingLevel = thinkingFromOptions(replay.configOptions)
	// session.load replays a completed transcript. Message and tool chunks seen
	// during that RPC describe history, not a live prompt.
	replay.streaming = false
	replay.attached = generation
	replay.queue = entry.queue.clone()
	recoveredDispatch := replay.queue.Dispatch != nil
	if err := m.recoverQueuedDispatchLocked(sessionID, replay); err != nil {
		entry.replay = nil
		entry.state.Unlock()
		return err
	}
	replay.stats.TotalMessages = len(replay.messages)
	entry.title = replay.title
	entry.model = replay.model
	entry.thinkingLevel = replay.thinkingLevel
	entry.configOptions = replay.configOptions
	entry.messages = replay.messages
	entry.streaming = replay.streaming
	entry.settlement = replay.settlement
	entry.stats = replay.stats
	entry.messageUsage = replay.messageUsage
	entry.queue = replay.queue
	entry.runID = replay.runID
	entry.pendingEcho = replay.pendingEcho
	entry.userResourceBytes = replay.userResourceBytes
	entry.pendingToolOutputs = replay.pendingToolOutputs
	entry.consumedQuestions = replay.consumedQuestions
	entry.appAttachments = replay.appAttachments
	entry.commands = replay.commands
	entry.capabilities = replay.capabilities
	entry.planState = replay.planState
	entry.agentIdentity = replay.agentIdentity
	entry.attached = replay.attached
	entry.projectionID = replay.projectionID
	entry.replay = nil
	if recoveredDispatch {
		m.emitQueueProjection(sessionID, entry, !entry.promptActive)
	}
	commands := cloneSlashCommands(entry.commands)
	m.emit("agent.event", map[string]any{"sessionId": sessionID, "event": map[string]any{"type": "commands", "commands": commands}})
	entry.state.Unlock()
	m.scheduleFollowUp(sessionID, entry)
	return nil
}

func (m *SessionManager) List(ctx context.Context, projectID string, archived any) ([]SessionSummary, error) {
	_, profile, err := m.client.Profile(ctx)
	if err != nil {
		return nil, err
	}
	if !profile.Compatible {
		return nil, unsupportedAgentCapability(strings.Join(profile.MissingRequired, " and "))
	}
	records, err := m.records.List()
	if err != nil {
		return nil, err
	}
	filtered := records[:0]
	for _, record := range records {
		if record.ProjectID == projectID {
			filtered = append(filtered, record)
		}
	}
	queues := make(map[string]sessionQueueState)
	if m.queues != nil {
		stored, queueErr := m.queues.List()
		if queueErr != nil {
			return nil, queueErr
		}
		for _, record := range stored {
			if record.ProjectID == projectID {
				queues[record.SessionID] = queueStateFromRecord(record)
			}
		}
	}
	remote := make(map[string]remoteSession)
	var cursor *string
	seen := make(map[string]bool)
	for page := 0; page < 20; page++ {
		response, err := m.client.ListSessions(ctx, piwire.ListSessionsRequest{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, session := range response.Sessions {
			remote[string(session.SessionId)] = normalizeRemoteSession(session)
		}
		if response.NextCursor == nil {
			break
		}
		if seen[*response.NextCursor] {
			return nil, fmt.Errorf("Pi agent session list was truncated because it repeated a cursor")
		}
		seen[*response.NextCursor] = true
		cursor = response.NextCursor
		if page == 19 {
			return nil, fmt.Errorf("Pi agent session list was truncated after 20 pages")
		}
	}
	missing := make([]ProjectSessionRecord, 0)
	for _, record := range filtered {
		if _, found := remote[record.SessionID]; !found {
			missing = append(missing, record)
		}
	}
	if profile.Pi && len(missing) > 200 {
		return nil, fmt.Errorf("Pi session list requires more than 200 per-session lookups")
	}
	if profile.Pi {
		for _, record := range missing {
			info, err := m.info(ctx, record.SessionID)
			if err != nil {
				var requestError *piwire.RequestError
				if errors.As(err, &requestError) && requestError.Code == -32002 {
					continue
				}
				return nil, err
			}
			remote[record.SessionID] = info
		}
	}
	result := make([]SessionSummary, 0, len(filtered))
	for _, record := range filtered {
		source, found := remote[record.SessionID]
		if !found {
			continue
		}
		wantArchived := archived == true || archived == "all"
		wantActive := archived != true
		if source.archived && !wantArchived || !source.archived && !wantActive {
			continue
		}
		m.mu.Lock()
		live := m.sessions[record.SessionID]
		m.mu.Unlock()
		if live != nil && !source.archived {
			result = append(result, m.summary(record.SessionID, live))
			continue
		}
		queue := SessionQueue{Revision: "", Steering: []string{}, FollowUp: []string{}}
		if stored, ok := queues[record.SessionID]; ok {
			queue = stored.wire(true)
		}
		title := source.title
		if record.Title != "" && (title == "" || title == "Chat") {
			title = record.Title
		}
		result = append(result, SessionSummary{SessionID: record.SessionID, ProjectID: projectID, CWD: record.CWD, ParentSessionID: record.ParentSessionID, Title: title, ThinkingLevel: "off", MessageCount: source.messageCount, UpdatedAt: source.updatedAt, Live: false, Archived: source.archived, Queue: &queue})
	}
	return result, nil
}

func (m *SessionManager) SetModel(ctx context.Context, sessionID string, model WireModel) error {
	entry, err := m.entry(sessionID)
	if err != nil {
		return err
	}
	defer m.releaseEntry(entry)
	if err := m.lockEntry(sessionID, entry); err != nil {
		return err
	}
	defer entry.op.Unlock()
	if err := m.attachLocked(ctx, sessionID, entry); err != nil {
		return err
	}
	ctx = entry.context(ctx)
	entry.state.Lock()
	var current *WireModel
	if entry.model != nil {
		copied := *entry.model
		current = &copied
	}
	running := entry.streaming || entry.promptActive || entry.runID != ""
	entry.state.Unlock()
	if running {
		return fmt.Errorf("stop the running chat before changing its model")
	}
	if current != nil && current.Provider == model.Provider && current.ID == model.ID {
		return nil
	}
	providerChanged := current == nil || current.Provider != model.Provider
	if providerChanged {
		_, err := m.setConfig(ctx, sessionID, "provider", model.Provider)
		if err != nil {
			failure := fmt.Errorf("set provider %q while changing session model: %w", model.Provider, err)
			return m.reconcileModelSwitchFailure(ctx, sessionID, entry, current, true, failure)
		}
	}
	options, err := m.setConfig(ctx, sessionID, "model", model.ID)
	if err != nil {
		failure := fmt.Errorf("set model %q for provider %q: %w", model.ID, model.Provider, err)
		return m.reconcileModelSwitchFailure(ctx, sessionID, entry, current, providerChanged, failure)
	}
	configured := modelFromSetup(options, nil)
	if configured == nil || configured.Provider != model.Provider || configured.ID != model.ID {
		failure := fmt.Errorf("set model returned %q/%q, want %q/%q", restoredProvider(configured), restoredID(configured), model.Provider, model.ID)
		return m.reconcileModelSwitchFailure(ctx, sessionID, entry, current, providerChanged, failure)
	}
	entry.state.Lock()
	entry.configOptions = options
	entry.model = configured
	entry.state.Unlock()
	m.emitSessionConfig(sessionID, configured, options)
	return nil
}

// reconcileModelSwitchFailure keeps the local projection aligned with Pi
// after a model change did not complete. Pi has no transaction for these
// updates, so restore the previous model and provider when it changed. A
// failed restore invalidates the projection and reloads it instead of
// retaining an old model beside options from the new provider.
func (m *SessionManager) reconcileModelSwitchFailure(ctx context.Context, sessionID string, entry *sessionEntry, previous *WireModel, providerChanged bool, failure error) error {
	var options []any
	var rollbackErr error
	if previous != nil {
		options, rollbackErr = m.restoreModelConfig(ctx, sessionID, previous, providerChanged)
	}
	if rollbackErr == nil {
		if previous != nil {
			entry.state.Lock()
			entry.configOptions = options
			entry.model = previous
			entry.state.Unlock()
			m.emitSessionConfig(sessionID, previous, options)
			return failure
		}
		// No complete prior pair is available to restore. Reload rather than
		// assuming a failed request left Pi unchanged.
	}

	refreshCtx := context.WithoutCancel(ctx)
	if err := m.reloadSessionConfig(refreshCtx, sessionID, entry); err != nil {
		return errors.Join(failure, rollbackErr, fmt.Errorf("reload session configuration after failed model switch: %w", err))
	}
	entry.state.Lock()
	model, options := entry.model, entry.configOptions
	entry.state.Unlock()
	m.emitSessionConfig(sessionID, model, options)
	return errors.Join(failure, rollbackErr)
}

func (m *SessionManager) emitSessionConfig(sessionID string, model *WireModel, options []any) {
	m.emit("agent.event", map[string]any{"sessionId": sessionID, "event": map[string]any{"type": "config", "configOptions": projectConfigOptions(options), "model": model}})
}

func (m *SessionManager) restoreModelConfig(ctx context.Context, sessionID string, previous *WireModel, providerChanged bool) ([]any, error) {
	if providerChanged {
		if _, err := m.setConfig(ctx, sessionID, "provider", previous.Provider); err != nil {
			return nil, fmt.Errorf("restore previous provider %q: %w", previous.Provider, err)
		}
	}
	options, err := m.setConfig(ctx, sessionID, "model", previous.ID)
	if err != nil {
		return nil, fmt.Errorf("restore previous model %q for provider %q: %w", previous.ID, previous.Provider, err)
	}
	restored := modelFromSetup(options, nil)
	if restored == nil || restored.Provider != previous.Provider || restored.ID != previous.ID {
		return nil, fmt.Errorf("restore previous provider/model returned %q/%q, want %q/%q", restoredProvider(restored), restoredID(restored), previous.Provider, previous.ID)
	}
	return options, nil
}

func (m *SessionManager) reloadSessionConfig(ctx context.Context, sessionID string, entry *sessionEntry) error {
	// Do not expose the pre-switch model if loading the authoritative state also
	// fails. A later operation retries attachment from Pi.
	entry.state.Lock()
	entry.attached = 0
	entry.configOptions = nil
	entry.model = nil
	entry.state.Unlock()
	return m.attachLocked(ctx, sessionID, entry)
}

func restoredProvider(model *WireModel) string {
	if model == nil {
		return ""
	}
	return model.Provider
}

func restoredID(model *WireModel) string {
	if model == nil {
		return ""
	}
	return model.ID
}

func (m *SessionManager) SetThinking(ctx context.Context, sessionID, level string) error {
	entry, err := m.entry(sessionID)
	if err != nil {
		return err
	}
	defer m.releaseEntry(entry)
	if err := m.lockEntry(sessionID, entry); err != nil {
		return err
	}
	defer entry.op.Unlock()
	if err := m.attachLocked(ctx, sessionID, entry); err != nil {
		return err
	}
	entry.state.Lock()
	running := entry.streaming || entry.promptActive || entry.runID != ""
	entry.state.Unlock()
	if running {
		return fmt.Errorf("stop the running chat before changing its thinking level")
	}
	options, err := m.setConfig(entry.context(ctx), sessionID, "thinking", level)
	if err != nil {
		return err
	}
	entry.state.Lock()
	entry.configOptions = options
	entry.thinkingLevel = level
	var model *WireModel
	if entry.model != nil {
		copied := *entry.model
		model = &copied
	}
	entry.state.Unlock()
	m.emitSessionConfig(sessionID, model, options)
	return nil
}

func (m *SessionManager) setConfig(ctx context.Context, sessionID, configID, value string) ([]any, error) {
	response, err := m.client.SetConfig(ctx, piwire.SetSessionConfigOptionRequest{ValueId: &piwire.SetSessionConfigOptionValueId{SessionId: piwire.SessionId(sessionID), ConfigId: piwire.SessionConfigId(configID), Value: piwire.SessionConfigValueId(value)}})
	if err != nil {
		return nil, err
	}
	return jsonValues(response.ConfigOptions), nil
}

func (m *SessionManager) entry(sessionID string) (*sessionEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.lifecycle[sessionID] {
		return nil, fmt.Errorf("wait for the chat lifecycle operation to finish")
	}
	entry := m.sessions[sessionID]
	if entry == nil {
		return nil, fmt.Errorf("unknown session: %s", sessionID)
	}
	entry.refs++
	return entry, nil
}

func (m *SessionManager) queueEntry(sessionID string) (*sessionEntry, error) {
	entry, err := m.entry(sessionID)
	if err == nil || m.records == nil {
		return entry, err
	}
	records, loadErr := m.records.List()
	if loadErr != nil {
		return nil, loadErr
	}
	var record *ProjectSessionRecord
	for index := range records {
		if records[index].SessionID == sessionID {
			record = &records[index]
			break
		}
	}
	if record == nil {
		return nil, err
	}
	queue, loadErr := m.restoredQueue(record.ProjectID, sessionID)
	if loadErr != nil {
		return nil, loadErr
	}
	candidate := newSessionEntry(sessionID, record.ProjectID, record.CWD, record.ParentSessionID, identifier.New())
	if record.Title != "" {
		candidate.title = record.Title
	}
	candidate.queue = queue
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.lifecycle[sessionID] {
		return nil, fmt.Errorf("wait for the chat lifecycle operation to finish")
	}
	if existing := m.sessions[sessionID]; existing != nil {
		entry = existing
	} else {
		entry = candidate
		m.sessions[sessionID] = entry
	}
	entry.refs++
	return entry, nil
}

func (m *SessionManager) releaseEntry(entry *sessionEntry) {
	m.mu.Lock()
	entry.state.Lock()
	entry.inactiveBytes = 0
	entry.state.Unlock()
	entry.refs--
	m.evictLocked()
	m.mu.Unlock()
}

// Background prompt and queue workers register before shutdown can begin.
// shutdown can therefore close the Pi connection and wait for every worker
// that was admitted while the manager was live.
func (m *SessionManager) retainWork(sessionID string, entry *sessionEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.sessions[sessionID] != entry || m.lifecycle[sessionID] {
		return fmt.Errorf("session changed while starting background work")
	}
	entry.refs++
	m.work.Add(1)
	return nil
}

func (m *SessionManager) releaseWork(entry *sessionEntry) {
	m.releaseEntry(entry)
	m.work.Done()
}

func (m *SessionManager) retainSessionLocked(clientKey, sessionID, projectID string) {
	if m.leases == nil {
		m.leases = make(map[string]*clientSessionLeases)
	}
	leases := m.leases[clientKey]
	if leases == nil {
		leases = &clientSessionLeases{sessions: make(map[string]string)}
		m.leases[clientKey] = leases
	}
	// Older browsers acquire implicitly. Once a browser declares its open tabs,
	// a late create/load response must not resurrect a tab it already closed.
	if leases.revision == 0 {
		leases.sessions[sessionID] = projectID
	}
}

func (m *SessionManager) SetLeases(clientKey string, revision uint64, requested []sessionLease) error {
	if revision == 0 || revision > 1<<53-1 || len(requested) > 512 {
		return fmt.Errorf("invalid session lease snapshot")
	}
	m.mu.Lock()
	resume := make([]queueResume, 0)
	defer func() {
		m.mu.Unlock()
		for _, target := range resume {
			m.scheduleFollowUp(target.sessionID, target.entry)
		}
	}()
	if m.closed {
		return fmt.Errorf("session manager has been shut down")
	}
	if previous := m.leases[clientKey]; previous != nil && revision <= previous.revision {
		return nil
	}
	// Read after acquiring mu: a completed delete must not be resurrected from
	// an association captured before its lifecycle operation finished.
	records, err := m.records.List()
	if err != nil {
		return err
	}
	byID := make(map[string]ProjectSessionRecord, len(records))
	for _, record := range records {
		byID[record.SessionID] = record
	}
	durableQueues := make(map[string]sessionQueueState)
	if m.queues != nil {
		stored, queueErr := m.queues.List()
		if queueErr != nil {
			return queueErr
		}
		for _, record := range stored {
			durableQueues[record.SessionID] = queueStateFromRecord(record)
		}
	}
	next := &clientSessionLeases{revision: revision, sessions: make(map[string]string, len(requested))}
	projects := make(map[string]workspace.Project)
	directories := make(map[string]string)
	for _, lease := range requested {
		if lease.ProjectID == "" || lease.SessionID == "" {
			return fmt.Errorf("invalid session lease snapshot")
		}
		record, ok := byID[lease.SessionID]
		if !ok {
			// Another browser may have deleted this tab while we were offline.
			// Ignore it without preventing the remaining tabs from reacquiring.
			continue
		}
		if record.ProjectID != lease.ProjectID {
			return fmt.Errorf("unknown session: %s", lease.SessionID)
		}
		project, found := projects[lease.ProjectID]
		if !found {
			project, err = m.projects.Get(lease.ProjectID)
			if err != nil {
				return err
			}
			projects[lease.ProjectID] = project
		}
		// Closing a project is global. Check under the same lock as ReleaseProject
		// so an older in-flight snapshot cannot restore its abandoned leases.
		if project.Closed {
			continue
		}
		cwd, checked := directories[record.CWD]
		if !checked {
			cwd, err = m.policy.Directory(record.CWD, "Session directory")
			if err != nil {
				return err
			}
			directories[record.CWD] = cwd
		}
		root, rootErr := project.Root()
		if rootErr != nil {
			return rootErr
		}
		if !workspace.Within(root, cwd) {
			return fmt.Errorf("session directory is outside the project root")
		}
		next.sessions[lease.SessionID] = lease.ProjectID
	}
	if m.leases == nil {
		m.leases = make(map[string]*clientSessionLeases)
	}
	m.leases[clientKey] = next
	for sessionID, projectID := range next.sessions {
		if m.sessions[sessionID] == nil && !m.lifecycle[sessionID] {
			record := byID[sessionID]
			// A long disconnect may outlive eviction. Restore only the association;
			// the next read/prompt loads the authoritative transcript from Pi.
			entry := newSessionEntry(sessionID, projectID, record.CWD, record.ParentSessionID, identifier.New())
			if record.Title != "" {
				entry.title = record.Title
			}
			if queue, ok := durableQueues[sessionID]; ok {
				entry.queue = queue
			}
			m.sessions[sessionID] = entry
		}
	}
	m.evictLocked()
	for sessionID := range next.sessions {
		if entry := m.sessions[sessionID]; entry != nil {
			entry.state.Lock()
			if runnableFollowUpLocked(entry) {
				resume = append(resume, queueResume{sessionID: sessionID, entry: entry})
			}
			entry.state.Unlock()
		}
	}
	return nil
}

func (m *SessionManager) Release(sessionID, projectID, cwd, clientKey string) {
	m.mu.Lock()
	entry := m.sessions[sessionID]
	leases := m.leases[clientKey]
	if leases != nil && leases.sessions[sessionID] == projectID && (entry == nil || entry.projectID == projectID && entry.cwd == cwd) {
		delete(leases.sessions, sessionID)
	}
	m.evictLocked()
	m.mu.Unlock()
}

func (m *SessionManager) isLeasedLocked(sessionID string) bool {
	for _, client := range m.leases {
		if _, found := client.sessions[sessionID]; found {
			return true
		}
	}
	return false
}

func (m *SessionManager) ReleaseClient(clientKey string) {
	m.mu.Lock()
	delete(m.leases, clientKey)
	m.evictLocked()
	m.mu.Unlock()
}

func (m *SessionManager) ReleaseProject(projectID string) {
	m.mu.Lock()
	// Close publishes before this cleanup runs. Another browser can already have
	// reopened the project and acquired a newer snapshot in that interval.
	if project, err := m.projects.Get(projectID); err == nil && !project.Closed {
		m.mu.Unlock()
		return
	}
	for _, leases := range m.leases {
		for sessionID, owner := range leases.sessions {
			if owner == projectID {
				delete(leases.sessions, sessionID)
			}
		}
	}
	m.evictLocked()
	m.mu.Unlock()
}

func (m *SessionManager) summary(sessionID string, entry *sessionEntry) SessionSummary {
	entry.state.Lock()
	defer entry.state.Unlock()
	return m.summaryLocked(sessionID, entry)
}

func (m *SessionManager) summaryLocked(sessionID string, entry *sessionEntry) SessionSummary {
	queue := entry.queue.wire(!entry.promptActive)
	return SessionSummary{Capabilities: entry.capabilities, SessionID: sessionID, ProjectID: entry.projectID, CWD: entry.cwd, ParentSessionID: entry.parentSessionID, Title: entry.title, Model: entry.model, ThinkingLevel: entry.thinkingLevel, IsStreaming: entry.streaming || entry.promptActive, MessageCount: len(entry.messages), UpdatedAt: m.now().UnixMilli(), Live: true, Archived: false, LastSettlement: entry.settlement, Queue: &queue, ConfigOptions: projectConfigOptions(entry.configOptions)}
}

func (m *SessionManager) evictLocked() {
	type candidate struct {
		id    string
		at    time.Time
		bytes int
	}
	var candidates []candidate
	total := 0
	leased := make(map[string]bool)
	for _, client := range m.leases {
		for sessionID := range client.sessions {
			leased[sessionID] = true
		}
	}
	for id, entry := range m.sessions {
		if entry.refs > 0 {
			continue
		}
		entry.state.Lock()
		memoryOnlyQueue := m.queues == nil && queuedFollowUpCount(entry.queue) > 0
		if leased[id] || entry.streaming || entry.promptActive || entry.runID != "" || len(entry.queue.Steering) > 0 || memoryOnlyQueue || entry.drainScheduled || entry.drainRetry != nil || entry.replay != nil {
			entry.inactiveAt = time.Time{}
			entry.state.Unlock()
			continue
		}
		if entry.ephemeral {
			delete(m.sessions, id)
			entry.state.Unlock()
			continue
		}
		if entry.inactiveAt.IsZero() {
			entry.inactiveAt = m.now()
		}
		if entry.inactiveBytes == 0 {
			attachments := make(map[string]AppAttachment, len(entry.appAttachments))
			for toolCallID, state := range entry.appAttachments {
				attachments[toolCallID] = state.attachment
			}
			encoded, err := json.Marshal([]any{entry.messages, entry.pendingToolOutputs, attachments, entry.planState})
			if err != nil {
				// A projection that cannot be measured must not bypass the budget.
				delete(m.sessions, id)
				entry.state.Unlock()
				continue
			}
			entry.inactiveBytes = len(encoded)
		}
		candidates = append(candidates, candidate{id: id, at: entry.inactiveAt, bytes: entry.inactiveBytes})
		total += entry.inactiveBytes
		entry.state.Unlock()
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].at.Equal(candidates[j].at) {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].at.Before(candidates[j].at)
	})
	for len(candidates) > inactiveProjectionMaxCount || total > inactiveProjectionMaxBytes {
		item := candidates[0]
		delete(m.sessions, item.id)
		total -= item.bytes
		candidates = candidates[1:]
	}
}

func newSessionEntry(sessionID, projectID, cwd, parent, token string) *sessionEntry {
	return &sessionEntry{projectID: projectID, cwd: cwd, parentSessionID: parent, title: "Chat", thinkingLevel: "off", messages: []any{}, commands: []map[string]any{}, stats: SessionStats{SessionID: sessionID, Reported: map[string]bool{}}, queue: newSessionQueueState(), objectiveToken: token, consumedQuestions: make(map[string]bool), projectionID: identifier.New()}
}

func agentProfileIdentity(profile AgentProfile, generation uint64) string {
	if profile.Pi {
		return profile.identity
	}
	if profile.identity != "" {
		return "agent:" + profile.identity
	}
	if profile.Name == "" {
		return fmt.Sprintf("anonymous-agent:%d", generation)
	}
	operations, _ := json.Marshal(profile.Operations)
	return "agent:" + profile.Name + "\x00" + profile.Version + "\x00" + string(operations)
}

func cloneSlashCommands(commands []map[string]any) []map[string]any {
	if commands == nil {
		return []map[string]any{}
	}
	cloned := make([]map[string]any, len(commands))
	for index, command := range commands {
		cloned[index] = cloneJSON(command).(map[string]any)
	}
	return cloned
}

func (m *SessionManager) restoredQueue(projectID, sessionID string) (sessionQueueState, error) {
	if m.queues == nil {
		return newSessionQueueState(), nil
	}
	queue, found, err := m.queues.Get(projectID, sessionID)
	if err != nil {
		return sessionQueueState{}, err
	}
	if !found {
		return newSessionQueueState(), nil
	}
	return queue, nil
}

type queueResume struct {
	sessionID string
	entry     *sessionEntry
}

// prepareQueueResume recreates only sessions with work that can advance
// safely. Blocked or temporarily unavailable sessions stay on disk.
func (m *SessionManager) prepareQueueResume() ([]queueResume, error) {
	if m.queues == nil || m.records == nil {
		return nil, nil
	}
	stored, err := m.queues.List()
	if err != nil {
		return nil, err
	}
	records, err := m.records.List()
	if err != nil {
		return nil, err
	}
	associations := make(map[string]ProjectSessionRecord, len(records))
	for _, record := range records {
		key := queueRecordKey(record.ProjectID, record.SessionID)
		associations[key] = record
	}
	candidates := make([]queueResume, 0, len(stored))
	for _, record := range stored {
		state := queueStateFromRecord(record)
		if state.Blocked != nil || len(state.FollowUp) == 0 && state.Dispatch == nil {
			continue
		}
		association, found := associations[queueRecordKey(record.ProjectID, record.SessionID)]
		if !found {
			continue
		}
		project, err := m.projects.Get(record.ProjectID)
		if err != nil {
			return nil, err
		}
		if project.Closed {
			continue
		}
		admitted, err := m.projects.AssertCWD(record.ProjectID, association.CWD)
		if err != nil {
			continue
		}
		candidate := newSessionEntry(record.SessionID, record.ProjectID, admitted, association.ParentSessionID, identifier.New())
		if association.Title != "" {
			candidate.title = association.Title
		}
		candidate.queue = state
		candidates = append(candidates, queueResume{sessionID: record.SessionID, entry: candidate})
	}
	targets := make([]queueResume, 0, len(candidates))
	for _, candidate := range candidates {
		m.mu.Lock()
		entry := m.sessions[candidate.sessionID]
		if entry == nil {
			entry = candidate.entry
			m.sessions[candidate.sessionID] = entry
		}
		if m.closed || m.lifecycle[candidate.sessionID] || entry.projectID != candidate.entry.projectID || entry.cwd != candidate.entry.cwd {
			m.mu.Unlock()
			continue
		}
		entry.refs++
		m.mu.Unlock()
		targets = append(targets, queueResume{sessionID: candidate.sessionID, entry: entry})
	}
	return targets, nil
}

func (m *SessionManager) resumeQueues(targets []queueResume) {
	// Admission retains every durable target in shutdown accounting while the
	// fixed worker pool bounds only this startup sweep.
	jobs := make(chan queueResume, len(targets))
	for _, target := range targets {
		if m.admitFollowUp(target.sessionID, target.entry) {
			jobs <- target
		}
		m.releaseEntry(target.entry)
	}
	close(jobs)
	workers := min(maxQueueRecoveryWorkers, len(jobs))
	for range workers {
		go func() {
			for target := range jobs {
				m.runFollowUp(target.sessionID, target.entry)
			}
		}()
	}
}

func (m *SessionManager) sessionServers(profile AgentProfile, token string) ([]piwire.McpServer, error) {
	servers := m.objectiveServers(profile, token)
	if m.settings == nil {
		return servers, nil
	}
	config, err := m.settings.Get()
	if err != nil {
		return nil, err
	}
	if config.Signet.Enabled {
		endpoint := "http://" + net.JoinHostPort(strings.Trim(config.Signet.Address, "[]"), strconv.Itoa(config.Signet.Port)) + "/mcp"
		servers = append(servers, piwire.McpServer{Http: &piwire.McpServerHttpInline{Type: "http", Name: "signet", Url: endpoint, Headers: []piwire.HttpHeader{}}})
	}
	return servers, nil
}

func (m *SessionManager) objectiveServers(profile AgentProfile, token string) []piwire.McpServer {
	if m.objectiveURL == "" {
		return []piwire.McpServer{}
	}
	return []piwire.McpServer{{Http: &piwire.McpServerHttpInline{Type: "http", Name: "pixie_objectives", Url: m.objectiveURL, Headers: []piwire.HttpHeader{{Name: "Authorization", Value: "Bearer " + token}}}}}
}

type remoteSession struct {
	title        string
	updatedAt    int64
	messageCount int
	archived     bool
}

func normalizeRemoteSession(session piwire.SessionInfo) remoteSession {
	title := "Chat"
	if session.Title != nil && *session.Title != "" {
		title = *session.Title
	}
	updated := time.Now().UnixMilli()
	if session.UpdatedAt != nil {
		if parsed, err := time.Parse(time.RFC3339Nano, *session.UpdatedAt); err == nil {
			updated = parsed.UnixMilli()
		}
	}
	archived := false
	messageCount := 0
	if value, ok := session.Meta["archivedAt"]; ok && value != nil {
		archived = true
	}
	if value, ok := numeric(session.Meta["messageCount"]); ok {
		messageCount = int(value)
	}
	return remoteSession{title: title, updatedAt: updated, messageCount: messageCount, archived: archived}
}

func jsonValues[T any](values []T) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		encoded, _ := json.Marshal(value)
		var decoded any
		_ = json.Unmarshal(encoded, &decoded)
		result = append(result, decoded)
	}
	return result
}

func thinkingFromOptions(options []any) string {
	for _, value := range options {
		option, _ := value.(map[string]any)
		if option["id"] == "thinking" || option["category"] == "thought_level" {
			if current, ok := option["currentValue"].(string); ok {
				return current
			}
		}
	}
	return "off"
}

func modelFromSetup(options []any, meta map[string]any) *WireModel {
	pi, _ := meta["pi"].(map[string]any)
	provider, _ := firstString(meta["providerId"], pi["providerId"])
	model, _ := firstString(meta["modelId"], pi["modelId"])
	for _, raw := range options {
		option := mapValue(raw)
		value, ok := option["currentValue"].(string)
		if !ok {
			continue
		}
		switch option["id"] {
		case "provider":
			provider = value
		case "model":
			model = value
		}
	}
	if provider == "" || model == "" {
		return nil
	}
	return &WireModel{ID: model, Name: model, Provider: provider, Available: true}
}

func firstString(values ...any) (string, bool) {
	for _, value := range values {
		if text, ok := value.(string); ok && text != "" {
			return text, true
		}
	}
	return "", false
}

func numeric(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed < math.MinInt64 || typed >= math.MaxInt64 {
			return 0, false
		}
		return int64(typed), true
	case int:
		return int64(typed), true
	case json.Number:
		parsed, err := strconv.ParseInt(string(typed), 10, 64)
		return parsed, err == nil
	}
	return 0, false
}

func nonempty(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("value cannot be empty")
	}
	return value, nil
}
