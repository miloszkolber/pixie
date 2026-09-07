package controller

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

const (
	DefaultControllerPort = 7312
	DefaultDataDir        = "/var/lib/pixie"
	DefaultStaticDir      = "/app/web"
)

type RuntimeConfig struct {
	Host        string
	Port        int
	DataDir     string
	StaticDir   string
	AppVersion  string
	AppRevision string
	// Optional embedding/test endpoint; the production entrypoint uses pinned host Pi.
	PiURL  string
	Policy *workspace.PathPolicy
	Getenv func(string) string
}

type Runtime struct {
	config    RuntimeConfig
	auth      AuthConfig
	server    *http.Server
	listener  net.Listener
	client    *PiClient
	sessions  *SessionManager
	schedules *Schedules
	apps      *AppViews
	socket    *WebSocketServer
	logins    *ProviderLogins
	watches   *workspace.ProjectWatches
	status    *runtimeStatusProvider
	browser   *BrowserPanels
	mcp       *MCPGateway
	errors    chan error
}

func NewRuntime(config RuntimeConfig) (*Runtime, error) {
	if config.Getenv == nil {
		config.Getenv = os.Getenv
	}
	authConfig, err := ReadAuthConfig(config.Getenv)
	if err != nil {
		return nil, err
	}
	if config.Host == "" {
		config.Host = authConfig.ControllerHost
	}
	if config.Port == 0 {
		config.Port = DefaultControllerPort
	}
	if config.DataDir == "" {
		config.DataDir = DefaultDataDir
	}
	if config.StaticDir == "" {
		config.StaticDir = DefaultStaticDir
	}
	build := diagnostics.NormalizeBuild(config.AppVersion, config.AppRevision)
	if config.Policy == nil {
		config.Policy, err = workspace.DiscoverPathPolicy()
		if err != nil {
			return nil, fmt.Errorf("discover project mounts: %w", err)
		}
	}
	store := persist.Store{Dir: config.DataDir}
	browserPanels, err := NewPersistentBrowserPanels(authConfig, nil, store)
	if err != nil {
		return nil, err
	}
	projects := workspace.NewProjects(store, config.Policy)
	files := workspace.NewFiles(projects, config.Policy)
	records := NewSessionRecords(store)
	queues := NewSessionQueues(store)
	objectives := NewObjectives(store)
	deletions := NewSessionDeletions(store)
	if err := migrateProjectRoots(projects, config.Policy, records, queues, objectives, deletions, store); err != nil {
		return nil, fmt.Errorf("migrate project roots: %w", err)
	}
	var socket *WebSocketServer
	publish := func(channel string, data any) {
		if socket != nil {
			_ = socket.Publish(context.Background(), channel, data)
		}
	}
	projects.SetPublisher(func(project workspace.Project) { publish("project.updated", project) })
	settings := NewSettings(store, func(value AppConfig) { publish("settings.changed", value) })
	sessions := NewSessionManager(projects, config.Policy, records, queues, objectives, publish)
	if config.PiURL == "" {
		config.PiURL = strings.TrimSpace(config.Getenv("PIXIE_PI_URL"))
	}
	client := NewPiClient(config.PiURL, strings.TrimSpace(config.Getenv("PIXIE_PI_SECRET_KEY")), config.AppVersion, sessions)
	client.profileChanged = func(profile AgentProfile) { publish("agent.profileChanged", profile) }
	sessions.SetClient(client)
	sessions.SetSettings(settings)
	sessions.SetObjectiveURL("http://127.0.0.1:" + strconv.Itoa(config.Port) + "/mcp/objective")
	schedules, err := NewSchedules(store, projects.AssertRoot, sessions.runSchedule)
	if err != nil {
		return nil, fmt.Errorf("load schedules: %w", err)
	}
	admin := NewPiAdmin(client, settings)
	admin.sessions = sessions
	admin.publish = publish
	admin.logins.publish = func(clientKey string, data any) {
		if socket != nil {
			_ = socket.PublishToClient(context.Background(), clientKey, "provider.login", data)
		}
	}
	sessions.deviceCode = admin.logins.DeviceCode
	git := workspace.NewGit(projects, config.Policy)
	watches := workspace.NewProjectWatches(projects, git, publish)
	apps := NewAppViews(sessions, authConfig, config.Port)
	requests := &diagnostics.RequestCounter{}
	mcpGateway := NewMCPGateway(authConfig)
	statusProvider := newRuntimeStatusProvider(build, requests, projects, settings, config.StaticDir, client, authConfig)
	handler := CoreHandler{Schedules: schedules, Projects: projects, Files: files, Sessions: sessions, Apps: apps, Settings: settings, Admin: admin, Git: git, Watches: watches, Requests: requests, RuntimeStatus: statusProvider.snapshot, BrowserPanels: browserPanels, MCPGateway: mcpGateway}
	welcome := func(ctx context.Context) (any, error) {
		recent, err := projects.List(true)
		if err != nil {
			return nil, err
		}
		open := make([]workspace.Project, 0, len(recent))
		for _, project := range recent {
			if !project.Closed {
				open = append(open, project)
			}
		}
		appConfig, err := settings.Get()
		if err != nil {
			return nil, err
		}
		status := runtimePiStatus(ctx, client)
		health := make(map[string]any, len(status))
		for key, value := range status {
			if key != "agentProfile" {
				health[key] = value
			}
		}
		result := map[string]any{"protocolVersion": BrowserProtocolVersion, "projects": open, "recentProjects": recent, "config": appConfig, "piStatus": health}
		if profile, ok := status["agentProfile"]; ok {
			result["agentProfile"] = profile
		}
		if config.AppVersion != "" {
			result["appVersion"] = config.AppVersion
		}
		return result, nil
	}
	socket, err = NewWebSocketServer(handler, welcome, authConfig)
	if err != nil {
		return nil, err
	}
	socket.LoginSnapshot = admin.logins.Snapshot
	socket.ClientReaped = func(clientKey string) {
		apps.ReleaseClient(clientKey)
		sessions.ReleaseClient(clientKey)
		browserPanels.ReleaseClient(clientKey)
	}
	ready := func(response http.ResponseWriter, request *http.Request) {
		status := runtimePiStatus(request.Context(), client)
		localReady, localDetail := statusProvider.localReady()
		status["applicationReady"] = localReady
		if !localReady {
			status["applicationError"] = localDetail
		}
		code := http.StatusOK
		profile, _ := status["agentProfile"].(AgentProfile)
		if !localReady || status["configured"] != true || status["reachable"] != true || !profile.Compatible {
			code = http.StatusServiceUnavailable
		}
		writeAuthJSON(response, code, status)
	}
	httpHandler, err := NewHTTPHandler(socket, ObjectiveHandler{Sessions: sessions, Schedules: schedules}, projects, files, authConfig, config.StaticDir, ready)
	if err != nil {
		return nil, err
	}
	return &Runtime{schedules: schedules, config: config, auth: authConfig, server: &http.Server{Handler: httpHandler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}, client: client, sessions: sessions, apps: apps, socket: socket, logins: admin.logins, watches: watches, status: statusProvider, browser: browserPanels, mcp: mcpGateway}, nil
}

func (r *Runtime) Start() (string, error) {
	if err := r.sessions.recoverDeletions(context.Background()); err != nil {
		return "", fmt.Errorf("resume session deletions: %w", err)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(r.config.Host, strconv.Itoa(r.config.Port)))
	if err != nil {
		return "", err
	}
	r.listener = listener
	r.errors = make(chan error, 1)
	queued, err := r.sessions.prepareQueueResume()
	if err != nil {
		_ = listener.Close()
		return "", fmt.Errorf("resume queued follow-ups: %w", err)
	}
	go func() { r.errors <- r.server.Serve(listener) }()
	r.schedules.Start()
	r.browser.ResumeCleanup()
	r.sessions.resumeQueues(queued)
	return "http://" + net.JoinHostPort(r.config.Host, strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)), nil
}

func (r *Runtime) Errors() <-chan error { return r.errors }

func (r *Runtime) Shutdown(ctx context.Context) error {
	r.schedules.Close(ctx)
	r.status.close()
	r.mcp.Close()
	r.browser.CloseAll(ctx)
	r.logins.Close()
	r.watches.Close()
	r.socket.Close(ctx)
	r.apps.CloseAll(ctx)
	r.sessions.shutdown(ctx)
	return r.server.Shutdown(ctx)
}

func runtimePiStatus(ctx context.Context, client *PiClient) map[string]any {
	if client.scope.requireSecret && client.scope.secret == "" {
		return map[string]any{"configured": false, "reachable": false, "error": "PIXIE_PI_SECRET_KEY is not configured"}
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, profile, err := client.Profile(bounded)
	if err != nil {
		return map[string]any{"configured": true, "reachable": false, "error": err.Error()}
	}
	status := map[string]any{"configured": true, "reachable": true, "agentProfile": profile}
	if !profile.Compatible {
		status["error"] = "Connected agent is missing required capabilities: " + strings.Join(profile.MissingRequired, ", ")
	}
	return status
}

func (m *SessionManager) shutdown(ctx context.Context) {
	m.mu.Lock()
	m.closed = true
	active := make(map[string]uint64)
	ids := make([]string, 0, len(m.sessions))
	for id, entry := range m.sessions {
		ids = append(ids, id)
		entry.state.Lock()
		if entry.streaming || entry.promptActive || entry.runID != "" {
			active[id] = entry.attached
		}
		if entry.drainRetry != nil {
			entry.drainRetry.Stop()
			entry.drainRetry = nil
		}
		entry.promptGeneration++
		entry.state.Unlock()
	}
	questions := m.questions
	m.questions = make(map[questionKey]*pendingQuestion)
	m.mu.Unlock()

	for _, pending := range questions {
		select {
		case pending.result <- map[string]any{"answers": []any{}, "cancelled": true}:
		default:
		}
	}
	var pending sync.WaitGroup
	if m.client != nil {
		for id, generation := range active {
			pending.Add(1)
			go func(sessionID string) {
				defer pending.Done()
				_ = m.client.Cancel(context.WithValue(ctx, connectionGenerationKey{}, generation), sessionID)
			}(id)
		}
	}
	pending.Wait()
	if m.client != nil {
		m.client.Close()
	}
	m.work.Wait()
}
