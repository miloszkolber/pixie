package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miloszkolber/pixie/internal/canvas"
	"github.com/miloszkolber/pixie/internal/design"
	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/mcpserver"
	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
	piwire "github.com/miloszkolber/pixie/shared/piprotocol"
)

const (
	DefaultControllerPort = 7312
	// DefaultDataDir is the last-resort state directory when neither
	// PIXIE_DATA_DIR nor a home directory is available. Containers pin
	// PIXIE_DATA_DIR explicitly; see defaultDataDir.
	DefaultDataDir = "/var/lib/pixie"
	// DefaultStaticDir is the last-resort web asset directory, matching the
	// container layout. Release binaries serve embedded assets instead; see
	// resolveStaticFiles.
	DefaultStaticDir = "/app/web"
)

const (
	// healthTransitionMaxEntries bounds the retained health history. The ring
	// keeps state tokens only; a raw error, endpoint or path never enters it.
	healthTransitionMaxEntries = 32
)

// HealthTransition is one bounded, secret-free component state change. Only
// stable state tokens cross this boundary, so operator diagnostics can show a
// transition history without exporting raw host errors or filesystem paths.
type HealthTransition struct {
	At        string `json:"at"`
	Component string `json:"component"`
	From      string `json:"from,omitempty"`
	To        string `json:"to"`
}

// RuntimeDiagnosticsHealth is the diagnostics projection of the health ring.
type RuntimeDiagnosticsHealth struct {
	Transitions []HealthTransition `json:"transitions"`
}

// healthStates is the complete vocabulary a transition may record. Observe
// drops anything else, which keeps host-supplied free text out by construction.
var healthStates = map[string]bool{
	"unknown":      true,
	"unconfigured": true,
	"unreachable":  true,
	"incompatible": true,
	"degraded":     true,
	"ready":        true,
	"healthy":      true,
}

// healthTransitionRing records deduplicated health transitions per component.
// It is safe for concurrent use. now is wall time on purpose: the history is
// an operator-facing timeline, not an elapsed-time calculation.
type healthTransitionRing struct {
	mu      sync.Mutex
	now     func() time.Time
	last    map[string]string
	entries []HealthTransition
}

func newHealthTransitionRing() *healthTransitionRing {
	return &healthTransitionRing{now: time.Now, last: make(map[string]string), entries: make([]HealthTransition, 0, healthTransitionMaxEntries)}
}

// Observe records a state change for a known component. Unknown components and
// states are ignored rather than stored, so no arbitrary text is retained.
func (r *healthTransitionRing) Observe(component, state string) {
	if r == nil || !healthComponents[component] || !healthStates[state] {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if previous, ok := r.last[component]; ok && previous == state {
		return
	}
	event := HealthTransition{At: r.now().UTC().Format(time.RFC3339), Component: component, From: r.last[component], To: state}
	r.last[component] = state
	if len(r.entries) == healthTransitionMaxEntries {
		copy(r.entries, r.entries[1:])
		r.entries[len(r.entries)-1] = event
		return
	}
	r.entries = append(r.entries, event)
}

func (r *healthTransitionRing) Snapshot() []HealthTransition {
	if r == nil {
		return []HealthTransition{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]HealthTransition(nil), r.entries...)
}

// healthComponents is the fixed set of observed components.
var healthComponents = map[string]bool{
	"agent":       true,
	"application": true,
	"schedule":    true,
}

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
	// ProtocolMode is the raw PIXIE_PI_PROTOCOL value (v1, auto or v2,
	// case-insensitive). Empty selects the byte-identical v1 default. An
	// invalid value fails NewRuntime.
	ProtocolMode string
	// AgentDir is the selected full-host native agent directory. When set, the
	// deletion pairing storage key is derived from it. Controller-only runs
	// leave it empty and read PIXIE_PI_STORAGE_KEY instead.
	AgentDir string
	// Optional worker/parser composition is explicit. Runtime never discovers
	// Canvas/Design helpers from PATH or substitutes an unrestricted fallback.
	CanvasConfig *canvas.Config
	DesignConfig *design.Config
	// ScheduleRuntime is parsed by the entrypoint when supplied. Nil keeps
	// embedded/test callers on the process environment policy.
	ScheduleRuntime *ScheduleRuntimePolicy
}

type Runtime struct {
	config    RuntimeConfig
	auth      AuthConfig
	server    *http.Server
	listener  net.Listener
	client    *PiClient
	sessions  *SessionManager
	schedules *Schedules
	socket    *WebSocketServer
	logins    *ProviderLogins
	watches   *workspace.ProjectWatches
	status    *runtimeStatusProvider
	registry  *mcpserver.Registry
	health    *healthTransitionRing
	errors    chan error
}

// defaultDataDir resolves user-writable state storage: an explicit
// XDG_DATA_HOME wins, then ~/.local/share/pixie, with the container path as
// the last resort when no home directory is available.
func defaultDataDir(getenv func(string) string) string {
	if xdg := strings.TrimSpace(getenv("XDG_DATA_HOME")); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "pixie")
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".local", "share", "pixie")
	}
	return DefaultDataDir
}

// resolvePairingStorageKey selects the canonical key naming the native storage
// the controller may pair with. Full-host derives it from the selected agent
// directory so the pairing never depends on a raw path or an environment
// value; controller-only uses the explicit PIXIE_PI_STORAGE_KEY. Paired mode
// requires a key, while auto treats a missing key as pairing unavailable.
func resolvePairingStorageKey(mode DeletionAuthorityMode, agentDir string, getenv func(string) string) (string, error) {
	if strings.TrimSpace(agentDir) != "" {
		key, err := persist.DerivePairingStorageKey(agentDir)
		if err != nil {
			return "", err
		}
		return key, nil
	}
	key := strings.TrimSpace(getenv("PIXIE_PI_STORAGE_KEY"))
	if mode == DeletionAuthorityPaired && key == "" {
		return "", fmt.Errorf("PIXIE_DELETION_AUTHORITY=paired requires a pairing storage key: select a full-host agent directory or set PIXIE_PI_STORAGE_KEY")
	}
	return key, nil
}

func NewRuntime(config RuntimeConfig) (*Runtime, error) {
	if config.Getenv == nil {
		config.Getenv = os.Getenv
	}
	scheduleRuntime := config.ScheduleRuntime
	if scheduleRuntime == nil {
		parsed, err := ParseScheduleRuntimePolicy(config.Getenv)
		if err != nil {
			return nil, err
		}
		scheduleRuntime = &parsed
	}
	// Destructive-recovery authority is resolved before any listener, store or
	// client is created so an invalid selection fails startup.
	deletionAuthority, err := ParseDeletionAuthorityMode(config.Getenv("PIXIE_DELETION_AUTHORITY"))
	if err != nil {
		return nil, err
	}
	// Host protocol negotiation is resolved before any listener or client so
	// an invalid PIXIE_PI_PROTOCOL fails startup rather than dialing a
	// downgraded transport. An explicit RuntimeConfig value wins; otherwise
	// the controller resolves the process environment.
	protocolRaw := strings.TrimSpace(config.ProtocolMode)
	if protocolRaw == "" {
		protocolRaw = config.Getenv(piwire.HostProtocolEnvVar)
	}
	protocolMode, err := piwire.ParseHostProtocolMode(protocolRaw)
	if err != nil {
		return nil, err
	}
	pairingStorageKey, err := resolvePairingStorageKey(deletionAuthority, config.AgentDir, config.Getenv)
	if err != nil {
		return nil, err
	}
	authConfig, err := ReadAuthConfig(config.Getenv)
	if err != nil {
		return nil, err
	}
	if config.DataDir == "" {
		config.DataDir = defaultDataDir(config.Getenv)
	}
	// Carry the resolved state directory into the auth construction so the
	// signing secret and stored password hash are loaded from the same place
	// the rest of the controller state lives.
	authConfig.DataDir = config.DataDir
	// Record the configured credentials before any listener, store or client can
	// log them. Every diagnostic sink (structured logs, child-stderr rings and
	// support exports) shares this one process-wide redaction boundary.
	diagnostics.ConfigureSanitizerSecrets(
		config.Getenv("PIXIE_PI_SECRET_KEY"),
		authConfig.ControllerToken,
		authConfig.MCPToken,
	)
	if config.Host == "" {
		config.Host = authConfig.ControllerHost
	}
	if config.Port == 0 {
		config.Port = DefaultControllerPort
	}
	if err := validateControllerRuntime(config.Host, config.Port, authConfig); err != nil {
		return nil, err
	}
	// Authority checks need the same effective listener host/port as the
	// listener itself. Keep this alongside the runtime defaults so HTTP and
	// WebSocket handlers cannot drift to a request-derived authority.
	authConfig.ControllerHost = config.Host
	authConfig.ControllerPort = config.Port
	// An empty StaticDir is intentional: asset resolution prefers the
	// embedded web bundle and falls back to DefaultStaticDir.
	build := diagnostics.NormalizeBuild(config.AppVersion, config.AppRevision)
	if config.Policy == nil {
		config.Policy, err = workspace.DiscoverPathPolicy()
		if err != nil {
			return nil, fmt.Errorf("discover project mounts: %w", err)
		}
	}
	store := persist.Store{Dir: config.DataDir}
	// In-process Pixie MCP publisher: Canvas and Design publish on the
	// controller listener. A module that cannot start here degrades the
	// catalog instead of failing startup.
	mcpRegistry, err := mcpserver.NewRegistry(mcpserver.Config{
		Host:         config.Host,
		Port:         config.Port,
		Token:        authConfig.MCPToken,
		PublicOrigin: authConfig.PublicOrigin,
		DataDir:      store.Dir,
		CanvasConfig: config.CanvasConfig,
		DesignConfig: config.DesignConfig,
		Getenv: func(key string) (string, bool) {
			if config.Getenv == nil {
				return "", false
			}
			value := config.Getenv(key)
			return value, value != ""
		},
	}, build, nil)
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
	settings := NewSettings(store, func(value AppConfig) { publish("settings.changed", browserAppConfig(value)) })
	sessions := NewSessionManager(projects, config.Policy, records, queues, objectives, publish)
	sessions.SetDeletionAuthority(deletionAuthority, pairingStorageKey)
	sessions.SetMCPRegistry(mcpRegistry)
	if config.PiURL == "" {
		resolved, err := resolvePiURL(config.Getenv)
		if err != nil {
			return nil, err
		}
		config.PiURL = resolved
	}
	client := NewPiClientWithProtocol(config.PiURL, strings.TrimSpace(config.Getenv("PIXIE_PI_SECRET_KEY")), config.AppVersion, sessions, protocolMode)
	client.profileChanged = func(profile AgentProfile) { publish("agent.profileChanged", profile) }
	sessions.SetClient(client)
	sessions.SetSettings(settings)
	sessions.SetObjectiveURL("http://127.0.0.1:" + strconv.Itoa(config.Port) + "/mcp/objective")
	schedules, err := NewSchedulesWithRuntime(store, projects.AssertRoot, sessions.runSchedule, *scheduleRuntime)
	if err != nil {
		return nil, fmt.Errorf("load schedules: %w", err)
	}
	schedules.SetCancellationHandler(sessions.cancelScheduleRun)
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
	requests := &diagnostics.RequestCounter{}
	events := diagnostics.NewControllerEventRing()
	healthRing := newHealthTransitionRing()
	statusProvider := newRuntimeStatusProvider(build, requests, projects, settings, config.StaticDir, client, authConfig, mcpRegistry)
	statusProvider.schedules = schedules
	handler := CoreHandler{
		Schedules: schedules, Projects: projects, Files: files, Sessions: sessions, Settings: settings,
		Admin: admin, Git: git, Watches: watches, Requests: requests, RuntimeStatus: statusProvider.snapshot,
		RuntimeDiagnostics: func(ctx context.Context) RuntimeDiagnosticsReport {
			return runtimeDiagnosticsSnapshot(healthRing, sessions, statusProvider, runtimePiStatus(ctx, client))
		},
		SupportSnapshotAuthEnabled: authConfig.Enabled,
		SupportSnapshot: func(ctx context.Context) (json.RawMessage, error) {
			report := runtimeDiagnosticsSnapshot(healthRing, sessions, statusProvider, runtimePiStatus(ctx, client))
			return diagnostics.MarshalSupportSnapshot(supportSnapshotRuntime(build, report, sessions.SessionSchemaSummary()), events.Snapshot())
		},
		ControllerEvents: events,
		MCPRegistry:      mcpRegistry,
	}
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
		result := map[string]any{"protocolVersion": BrowserProtocolVersion, "projects": open, "recentProjects": recent, "config": browserAppConfig(appConfig), "piStatus": health}
		if profile, ok := status["agentProfile"]; ok {
			result["agentProfile"] = profile
		}
		if recoveries := sessions.DeletionRecoveryStatus(); len(recoveries) > 0 {
			result["deletionRecovery"] = recoveries
		}
		result["diagnostics"] = runtimeDiagnosticsSnapshot(healthRing, sessions, statusProvider, status)
		if config.AppVersion != "" {
			result["appVersion"] = config.AppVersion
		}
		return result, nil
	}
	socket, err = NewWebSocketServer(handler, welcome, authConfig)
	if err != nil {
		return nil, err
	}
	// AUX-19: internal prompt settlement, attach/resume and lifecycle wakeups
	// dispatch follow-ups through the session manager rather than the browser
	// boundary. Share the server's gate so those runs are refused during a drain
	// and counted by WaitForDrain until they settle.
	sessions.SetAdmissionGate(socket.gate)
	socket.LoginSnapshot = admin.logins.Snapshot
	socket.ClientReaped = func(clientKey string) {
		sessions.ReleaseClient(clientKey)
	}
	ready := func(response http.ResponseWriter, request *http.Request) {
		status := runtimePiStatus(request.Context(), client)
		localReady, _ := statusProvider.localReady()
		code := http.StatusOK
		profile, _ := status["agentProfile"].(AgentProfile)
		if !localReady || status["configured"] != true || status["reachable"] != true || !profile.Compatible {
			code = http.StatusServiceUnavailable
		}
		// /readyz remains usable by unauthenticated service managers. It is a
		// minimal readiness bit, never an operator diagnostics or project/session
		// recovery endpoint.
		writeAuthJSON(response, code, map[string]bool{"ready": code == http.StatusOK})
	}
	httpHandler, err := NewHTTPHandler(socket, ObjectiveHandler{Sessions: sessions, Schedules: schedules}, projects, files, authConfig, config.StaticDir, ready)
	if err != nil {
		mcpRegistry.Shutdown()
		return nil, err
	}
	httpHandler.MCPRegistry = mcpRegistry
	httpHandler.SessionRecords = records
	return &Runtime{schedules: schedules, config: config, auth: authConfig, server: &http.Server{Handler: httpHandler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}, client: client, sessions: sessions, socket: socket, logins: admin.logins, watches: watches, status: statusProvider, registry: mcpRegistry, health: healthRing}, nil
}

func validateControllerRuntime(host string, port int, auth AuthConfig) error {
	if err := validateControllerHost(host); err != nil {
		return fmt.Errorf("invalid effective controller bind: %w", err)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("PIXIE_CONTROLLER_PORT must be a port 1-65535, got %d", port)
	}
	effectiveAuth := auth
	effectiveAuth.ControllerHost = host
	if !mcpPublisherAuthConfigured(effectiveAuth) {
		return fmt.Errorf("PIXIE_MCP_TOKEN must be a strong printable random token for the enabled MCP publisher")
	}
	if err := validateTrustedProxyAuth(auth.Enabled, auth.TrustedProxyCIDRs); err != nil {
		return err
	}
	if isLoopbackControllerHost(host) {
		return nil
	}
	if auth.PublicOrigin == "" {
		return fmt.Errorf("a non-loopback effective controller bind requires PIXIE_PUBLIC_ORIGIN")
	}
	if !auth.Enabled && !auth.AllowRemoteWithout {
		return fmt.Errorf("a non-loopback effective controller bind requires controller authentication or explicit PIXIE_ALLOW_UNAUTHENTICATED_REMOTE=true")
	}
	return nil
}

func (r *Runtime) Start() (string, error) {
	if err := r.sessions.RecoverDeletions(context.Background()); err != nil {
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
	r.sessions.resumeQueues(queued)
	return "http://" + net.JoinHostPort(r.config.Host, strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)), nil
}

func (r *Runtime) Errors() <-chan error { return r.errors }

// BeginDrain starts the AUX-19 quiesce: new prompts, forks and resume-style
// session creation are refused immediately while already-admitted work,
// including asynchronous prompt runs, keeps the admission it was granted.
// WaitForDrain is bounded by the caller's context (controllerQuiesceTimeout in
// production); once that expires, Shutdown cancels any still-active runs.
// It is safe to call before or during Shutdown.
func (r *Runtime) BeginDrain() {
	if r != nil && r.socket != nil {
		r.socket.BeginDrain()
	}
}

// WaitForDrain blocks until admitted runnable work has settled or ctx ends.
func (r *Runtime) WaitForDrain(ctx context.Context) {
	if r != nil && r.socket != nil {
		r.socket.WaitForDrain(ctx)
	}
}

// Quiescing reports whether the controller is refusing new runnable work.
func (r *Runtime) Quiescing() bool {
	return r != nil && r.socket != nil && r.socket.Quiescing()
}

func (r *Runtime) Shutdown(ctx context.Context) error {
	r.schedules.Close(ctx)
	if r.registry != nil {
		r.registry.Shutdown()
	}
	r.logins.Close()
	r.watches.Close()
	r.socket.Close(ctx)
	r.sessions.shutdown(ctx)
	return r.server.Shutdown(ctx)
}

// reloadHost asks the Pi host service to end itself so the service manager
// brings a fresh process up. Configured native extensions then apply to every
// session at once, with the documented restart semantics: in-flight runs
// interrupt and session transcripts stay durable on disk.
func (a *PiAdmin) reloadHost(ctx context.Context) (map[string]any, error) {
	var response struct {
		Ok bool `json:"ok"`
	}
	if err := a.call(ctx, "runtime.restart", nil, &response); err != nil {
		return nil, err
	}
	if !response.Ok {
		return nil, fmt.Errorf("Pi host did not accept the reload request")
	}
	return map[string]any{"ok": true}, nil
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

// runtimeDiagnosticsSnapshot projects the authenticated operator diagnostics
// surface from already-collected status. It never includes secrets, endpoints,
// or filesystem roots, and it never dispatches or clears tombstones. It also
// feeds the bounded health-transition ring with stable state tokens only.
func runtimeDiagnosticsSnapshot(health *healthTransitionRing, sessions *SessionManager, status *runtimeStatusProvider, piStatus map[string]any) RuntimeDiagnosticsReport {
	report := RuntimeDiagnosticsReport{
		Capabilities:           RuntimeDiagnosticsCapabilities{},
		Host:                   RuntimeDiagnosticsHost{},
		Runs:                   RuntimeDiagnosticsRuns{},
		DeletionReconciliation: RuntimeDiagnosticsDeletionReconciliation{},
		Schedule:               RuntimeDiagnosticsSchedule{State: "unknown"},
		Health:                 RuntimeDiagnosticsHealth{Transitions: []HealthTransition{}},
		Remediation:            []string{},
	}
	if configured, ok := piStatus["configured"].(bool); ok {
		report.Host.Configured = boolPointer(configured)
	}
	if reachable, ok := piStatus["reachable"].(bool); ok {
		report.Host.Reachable = boolPointer(reachable)
	}
	if reason, ok := piStatus["error"].(string); ok && reason != "" {
		report.Host.Reason = diagnostics.SanitizeDiagnosticDetail(reason)
	}
	if profile, ok := piStatus["agentProfile"].(AgentProfile); ok {
		compatible := profile.Compatible
		operations := profile.Operations
		report.Capabilities.Compatible = &compatible
		report.Capabilities.MissingRequired = append([]string{}, profile.MissingRequired...)
		report.Capabilities.Operations = &operations
		report.Capabilities.Capabilities = maps.Clone(profile.Capabilities)
		report.Capabilities.OperationSet = cloneBoolMap(profile.OperationSet)
	}
	if status != nil {
		localReady, localDetail := status.localReady()
		report.Host.ApplicationReady = boolPointer(localReady)
		if !localReady {
			report.Host.ApplicationReason = diagnostics.SanitizeDiagnosticDetail(localDetail)
		}
		if status.schedules != nil {
			report.Schedule.State = "healthy"
			if issue := status.schedules.Health(); issue != "" {
				report.Schedule.State = "degraded"
				report.Schedule.Reason = diagnostics.SanitizeDiagnosticDetail(issue)
			}
		}
	}
	if sessions != nil {
		activeCount := countActiveSessionRuns(sessions)
		reconciled := sessions.DeletionReconciliationStatus()
		reconciliationCount := len(reconciled)
		report.Runs.ActiveCount = &activeCount
		report.DeletionReconciliation.Count = &reconciliationCount
		report.DeletionReconciliation.Records = reconciled
	}
	if health != nil {
		health.Observe("agent", agentHealthState(report))
		health.Observe("application", applicationHealthState(report))
		health.Observe("schedule", report.Schedule.State)
		report.Health.Transitions = health.Snapshot()
	}
	report.Remediation = runtimeRemediationHints(report)
	return report
}

// agentHealthState reduces the projected host facts to one stable token. An
// unknown fact stays "unknown" rather than being treated as healthy.
func agentHealthState(report RuntimeDiagnosticsReport) string {
	if report.Host.Configured == nil {
		return "unknown"
	}
	if !*report.Host.Configured {
		return "unconfigured"
	}
	if report.Host.Reachable == nil {
		return "unknown"
	}
	if !*report.Host.Reachable {
		return "unreachable"
	}
	if report.Capabilities.Compatible == nil {
		return "unknown"
	}
	if !*report.Capabilities.Compatible {
		return "incompatible"
	}
	return "ready"
}

func applicationHealthState(report RuntimeDiagnosticsReport) string {
	if report.Host.ApplicationReady == nil {
		return "unknown"
	}
	if !*report.Host.ApplicationReady {
		return "degraded"
	}
	return "ready"
}

// supportSnapshotRuntime maps the broader authenticated diagnostics report to
// the narrow support-export allowlist. In particular, retained deletion
// records and agent capability payloads never cross this boundary because they
// can carry identifiers or host-provided free text. The AUX-12 session-schema
// degradation summary is sampled from the session manager so the snapshot
// accessor is actually observed; nil means no resident session degraded.
func supportSnapshotRuntime(build diagnostics.BuildInfo, report RuntimeDiagnosticsReport, sessionSchema *diagnostics.SessionSchemaSummary) diagnostics.SupportSnapshotRuntime {
	return diagnostics.SupportSnapshotRuntime{
		Build: build,
		Host: diagnostics.SupportSnapshotHost{
			Configured:        report.Host.Configured,
			Reachable:         report.Host.Reachable,
			ApplicationReady:  report.Host.ApplicationReady,
			Reason:            report.Host.Reason,
			ApplicationReason: report.Host.ApplicationReason,
		},
		ActiveRunCount:        report.Runs.ActiveCount,
		RetainedDeletionCount: report.DeletionReconciliation.Count,
		Schedule: diagnostics.SupportSnapshotSchedule{
			State:  report.Schedule.State,
			Reason: report.Schedule.Reason,
		},
		SessionSchema:     sessionSchema,
		HealthTransitions: supportHealthTransitions(report.Health.Transitions),
	}
}

// supportHealthTransitions maps the controller-owned ring into the diagnostics
// transport shape. It copies only the already-validated component/state tokens.
func supportHealthTransitions(transitions []HealthTransition) []diagnostics.HealthTransition {
	if len(transitions) == 0 {
		return nil
	}
	result := make([]diagnostics.HealthTransition, 0, len(transitions))
	for _, transition := range transitions {
		result = append(result, diagnostics.HealthTransition{
			At:        transition.At,
			Component: transition.Component,
			From:      transition.From,
			To:        transition.To,
		})
	}
	return result
}

func boolPointer(value bool) *bool { return &value }

// countActiveSessionRuns counts residents with an accepted run identity. View
// lifetime never owns execution, so this is a read-only projection for the
// diagnostics current-run slot.
func countActiveSessionRuns(sessions *SessionManager) int {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	count := 0
	for _, entry := range sessions.sessions {
		entry.state.Lock()
		if entry.streaming || entry.promptActive || entry.runID != "" {
			count++
		}
		entry.state.Unlock()
	}
	return count
}

// runtimeRemediationHints turns typed host health and pending uncertainty into
// actionable operator guidance. Hints never contain secrets or paths, and a
// healthy "No action needed" hint appears only when every prerequisite is known.
func runtimeRemediationHints(report RuntimeDiagnosticsReport) []string {
	hints := []string{}
	configured, reachable := report.Host.Configured, report.Host.Reachable
	if configured == nil {
		hints = append(hints, "Assistant-host configuration is unknown: refresh diagnostics before changing host settings.")
	} else if !*configured {
		hints = append(hints, "Configure PIXIE_PI_SECRET_KEY so the controller can dial the assistant host over loopback.")
	} else if reachable == nil {
		hints = append(hints, "Assistant-host reachability is unknown: refresh diagnostics and verify the host service is running.")
	} else if !*reachable {
		hints = append(hints, "Assistant host is unreachable: verify pixie_cli is serving on its configured loopback port and that config port and PIXIE_PI_PORT match.")
	}
	if configured != nil && *configured && reachable != nil && *reachable && report.Capabilities.Compatible == nil {
		hints = append(hints, "Assistant capabilities are unknown: reconnect to a compatible host before using optional operations.")
	}
	if configured != nil && *configured && reachable != nil && *reachable && report.Capabilities.Compatible != nil && !*report.Capabilities.Compatible {
		missing := strings.Join(report.Capabilities.MissingRequired, ", ")
		if missing == "" {
			missing = "required capabilities"
		}
		hints = append(hints, "Connected agent is missing "+missing+": restore a compatible Pi host before creating or resuming chats.")
	}
	if configured != nil && *configured && reachable != nil && *reachable && report.Capabilities.Compatible != nil && *report.Capabilities.Compatible && report.Capabilities.Operations != nil && !report.Capabilities.Operations.DeleteSession {
		hints = append(hints, "Connected agent has no session.delete support: retain deletion records instead of confirming new deletions.")
	}
	if report.Host.ApplicationReady == nil {
		hints = append(hints, "Controller readiness is unknown: refresh diagnostics before relying on local state.")
	} else if !*report.Host.ApplicationReady {
		detail := report.Host.ApplicationReason
		if detail == "" {
			detail = "application state is unavailable"
		}
		hints = append(hints, "Controller application is not ready ("+detail+"): check project storage and the embedded web bundle, then retry.")
	}
	if report.Schedule.State == "unknown" {
		hints = append(hints, "Schedule health is unknown: refresh diagnostics before relying on scheduled work.")
	} else if report.Schedule.State == "degraded" {
		detail := report.Schedule.Reason
		if detail == "" {
			detail = "a degraded state"
		}
		hints = append(hints, "Schedules report "+detail+"; paused or failing schedules do not block chats but need operator review.")
	}
	if report.DeletionReconciliation.Count == nil {
		hints = append(hints, "Deletion reconciliation is unknown: refresh diagnostics before confirming or retaining a tombstone.")
	} else if *report.DeletionReconciliation.Count > 0 {
		hints = append(hints, "There are retained deletion tombstones: confirm each one only after verifying the native session is gone, or retain to keep the tombstone in place. Tombstones are never cleared automatically.")
	}
	if len(hints) == 0 && configured != nil && *configured && reachable != nil && *reachable && report.Capabilities.Compatible != nil && *report.Capabilities.Compatible && report.Host.ApplicationReady != nil && *report.Host.ApplicationReady && report.Schedule.State == "healthy" && report.DeletionReconciliation.Count != nil && *report.DeletionReconciliation.Count == 0 {
		hints = append(hints, "No action needed: host is reachable, capabilities are negotiated and no uncertain work is pending.")
	}
	return hints
}

func (m *SessionManager) shutdown(ctx context.Context) {
	m.mu.Lock()
	m.closed = true
	// Process-local partial-create scratch is not durable state; drop it so a
	// stopped controller cannot retain a stale native session claim.
	m.scheduleCreationRoots = map[string]string{}
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
	m.mu.Unlock()
	// Revoke registrations for active and settled residents alike. The native
	// registry is instance-owned, so this is local cleanup rather than durable
	// module state.
	for _, id := range ids {
		m.revokeNativeMCPSession(id)
	}

	// Shutdown unwinds blocked UI on every session: dismiss browser modals.
	// The host closes its own bridges on session close.
	m.cancelDialogs("")
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
