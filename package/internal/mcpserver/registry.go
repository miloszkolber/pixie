package mcpserver

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miloszkolber/pixie/internal/browser"
	"github.com/miloszkolber/pixie/internal/canvas"
	"github.com/miloszkolber/pixie/internal/design"
	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/persist"
)

const (
	// BrowserRoute is the Browser module path on the controller listener; the
	// publisher now lives in the main Pixie process (Stage F merge).
	BrowserRoute = "/mcp/browser"
	// CanvasRoute is the Canvas module path on the controller listener.
	CanvasRoute = "/mcp/canvas"
	// DesignRoute is the Design module path on the controller listener.
	DesignRoute = "/mcp/design"
	// CanvasAPIPrefix owns Canvas human management/artifact routes.
	CanvasAPIPrefix = "/api/canvas"
	// DesignAPIPrefix owns Design human management/query/artifact routes.
	DesignAPIPrefix = "/api/design"
	// CatalogPath and StatusPath are the in-process publisher API. They mirror
	// the former separate host's /v1/mcp/modules and /v1/mcp/status shape with
	// the in-process route; exact paths remain an implementation detail.
	CatalogPath = "/api/mcp/modules"
	StatusPath  = "/api/mcp/status"

	storeFile  = "mcp-modules.json"
	browserID  = "browser"
	transports = "streamable_http"
)

// Chromium is the only Browser backend. The model-facing API (extension
// name, tools, and resources) never varies by backend because there is only
// one.

// Retired environment selection: PIXIE_MCP_MODULES and
// PIXIE_MCP_DISABLED_MODULES are ignored. Module enablement is owned by the
// Pixie persist store (mcp-modules.json) and toggled from the Tools UI; the
// Browser module defaults to enabled. A startup warning names the migration
// path when either variable is still set.
const (
	envModules  = "PIXIE_MCP_MODULES"
	envDisabled = "PIXIE_MCP_DISABLED_MODULES"
)

// Module is the registry record for one published Pixie MCP module. Registered
// modules stay compile-time and behind the same storage and trust boundary.
type Module struct {
	ID            string `json:"id"`
	ExtensionName string `json:"extensionName"`
	DisplayName   string `json:"displayName"`
	Description   string `json:"description"`
	Path          string `json:"path"`
	Transport     string `json:"transport"`
	Enabled       bool   `json:"enabled"`
	State         string `json:"state"`
	Detail        string `json:"detail,omitempty"`
	Endpoint      string `json:"endpoint,omitempty"`
}

// Catalog mirrors the former separate host catalog shape so Tools UI
// projections and Pi extension wiring stay identical across publisher engines.
type Catalog struct {
	SchemaVersion int            `json:"schemaVersion"`
	Revision      string         `json:"revision"`
	Gateway       GatewaySummary `json:"gateway"`
	Modules       []Module       `json:"modules"`
	Engine        string         `json:"engine"`
}

// GatewaySummary reports aggregate in-process publisher readiness.
type GatewaySummary struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// Health reports whether the named module can currently accept work.
func (r *Registry) Health(id string) (ready bool, detail string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.healthLocked(id)
}

func (r *Registry) healthLocked(id string) (bool, string) {
	definition, ok := r.definitionLocked(id)
	if !ok {
		return false, fmt.Sprintf("unknown in-process MCP module %q", id)
	}
	if !r.enabled[id] {
		return false, fmt.Sprintf("The %s module is disabled in Pixie MCP servers.", definition.DisplayName)
	}
	runtime := r.modules[id]
	if runtime == nil {
		if detail := r.failures[id]; detail != "" {
			return false, detail
		}
		return false, fmt.Sprintf("The %s module is not ready.", definition.DisplayName)
	}
	if runtime.ready != nil && !runtime.ready() {
		if runtime.readinessDetail != nil {
			if detail := runtime.readinessDetail(); detail != "" {
				return false, detail
			}
		}
		return false, fmt.Sprintf("The %s module is not ready.", definition.DisplayName)
	}
	return true, ""
}

// Config describes the in-process publisher. Browser storage roots are always
// derived from the controller data directory so Browser state never mixes
// with application data.
type Config struct {
	Host         string
	Port         int
	Token        string
	PublicOrigin string
	DataDir      string
	Getenv       func(string) (string, bool)
	// Binaries optionally overrides the Browser executable, config file, and
	// storage roots. Network and authentication stay publisher-owned.
	// Production leaves this nil; tests point it at fixtures.
	Binaries *BinaryConfig
	// CanvasConfig and DesignConfig are explicit optional worker/parser
	// compositions. The registry never discovers or substitutes a launcher or
	// parser from PATH; nil dependencies leave only their owning module
	// unavailable.
	CanvasConfig *canvas.Config
	DesignConfig *design.Config
}

// BinaryConfig overrides Browser process and storage paths for tests.
type BinaryConfig struct {
	AgentBrowser  string
	BrowserConfig string
	ArtifactRoot  string
	StateRoot     string
}

type persistedState struct {
	Modules map[string]persistedModule `json:"modules"`
}

type persistedModule struct {
	Enabled bool `json:"enabled"`
}

// Registry is the in-process Pixie MCP publisher. Browser, Canvas and Design
// share one persisted module map and one route/lifecycle boundary while each
// service keeps its own authority, storage and worker policy.
type Registry struct {
	config  Config
	build   diagnostics.BuildInfo
	logger  *slog.Logger
	store   persist.Store
	started time.Time

	mu          sync.RWMutex
	mutationMu  sync.Mutex
	browser     *browser.Service
	canvas      *canvas.Service
	design      *design.Service
	modules     map[string]*moduleRuntime
	failures    map[string]string
	definitions []ModuleDefinition
	persisted   persistedState
	enabled     map[string]bool
	nativeMCP   *NativeMCPRegistry
}

// moduleRuntime is the small lifecycle adapter shared by Browser, Canvas and
// Design. Slow construction happens before it is installed in Registry.modules;
// callers never hold Registry.mu while a service starts or stops.
type moduleRuntime struct {
	handler         http.Handler
	shutdown        func()
	enable          func()
	disable         func()
	ready           func() bool
	readinessDetail func() string
	browser         *browser.Service
	canvas          *canvas.Service
	design          *design.Service
}

// NewRegistry loads persisted module enablement and starts the registered
// services. Retired PIXIE_MCP_MODULES/DISABLED variables are ignored with a
// startup warning. Optional module construction failures stay local and leave
// desired enablement durable for a later explicit restart.
func NewRegistry(config Config, build diagnostics.BuildInfo, logger *slog.Logger) (*Registry, error) {
	if logger == nil {
		logger = diagnostics.NewLogger("mcpserver", build)
	}
	if config.DataDir == "" {
		return nil, fmt.Errorf("in-process MCP publisher requires a data directory")
	}
	if config.Port < 0 || config.Port > 65535 {
		return nil, fmt.Errorf("in-process MCP publisher port must be between 0 and 65535")
	}
	if config.Host == "" {
		config.Host = "127.0.0.1"
	}
	registry := &Registry{
		config:      config,
		build:       diagnostics.NormalizeBuild(build.Version, build.Revision),
		logger:      logger,
		store:       persist.Store{Dir: config.DataDir},
		started:     time.Now(),
		enabled:     make(map[string]bool),
		modules:     make(map[string]*moduleRuntime),
		failures:    make(map[string]string),
		definitions: DefaultModuleDefinitions(),
		nativeMCP:   NewNativeMCPRegistry(),
	}
	for _, definition := range registry.definitions {
		registry.enabled[definition.ID] = definition.DefaultEnabled
	}
	if err := registry.loadEnabled(); err != nil {
		return nil, err
	}
	// Remove the retired engine-preference file from deployments that
	// wrote it; enablement lives in mcp-modules.json and Chromium is the
	// only backend, so the file carries no information.
	_ = os.Remove(filepath.Join(config.DataDir, "browser.json"))
	registry.startInitial()
	return registry, nil
}

func (r *Registry) loadEnabled() error {
	if r.config.Getenv != nil {
		if modulesEnv, ok := r.config.Getenv(envModules); ok && modulesEnv != "" {
			r.logger.Warn("PIXIE_MCP_MODULES is retired; toggle modules in Tools instead")
		}
		if disabledEnv, ok := r.config.Getenv(envDisabled); ok && disabledEnv != "" {
			r.logger.Warn("PIXIE_MCP_DISABLED_MODULES is retired; toggle modules in Tools instead")
		}
	}
	var saved persistedState
	found, err := persist.Read(r.store, storeFile, &saved, validateState)
	if err != nil {
		return fmt.Errorf("read in-process MCP module state: %w", err)
	}
	if !found {
		r.persisted = persistedState{Modules: make(map[string]persistedModule)}
		return nil
	}
	r.persisted = clonePersistedState(saved)
	for _, definition := range r.definitions {
		if state, ok := saved.Modules[definition.ID]; ok {
			r.enabled[definition.ID] = state.Enabled
		}
	}
	return nil
}

// Register issues a native MCP credential from this publisher instance's
// ephemeral scope registry. The returned credential is bound to the exact
// module, server, session, and native generation supplied by the caller.
// Registry instances never share registrations.
func (r *Registry) Register(request NativeMCPRegistrationRequest) (NativeMCPRegistration, error) {
	if r == nil || r.nativeMCP == nil {
		return NativeMCPRegistration{}, fmt.Errorf("native MCP registry is not configured")
	}
	return r.nativeMCP.Register(request)
}

// Authorize validates a credential against this publisher instance's live
// scope registry. Caller-provided IDs do not grant access by themselves.
func (r *Registry) Authorize(request NativeMCPAuthorization) error {
	if r == nil || r.nativeMCP == nil {
		return fmt.Errorf("native MCP registry is not configured")
	}
	return r.nativeMCP.Authorize(request)
}

// Revoke removes one native MCP registration from this publisher instance.
// Repeated cleanup is intentionally a no-op.
func (r *Registry) Revoke(registrationID string) {
	if r == nil || r.nativeMCP == nil {
		return
	}
	r.nativeMCP.Revoke(registrationID)
}

// RevokeSession removes every native MCP registration bound to a session.
// It is used by controller session lifecycle transitions and is deliberately
// broader than one module/server so stale credentials cannot survive a delete
// or generation replacement.
func (r *Registry) RevokeSession(sessionID string) int {
	if r == nil {
		return 0
	}
	revoked := 0
	if r.nativeMCP != nil {
		revoked += r.nativeMCP.RevokeSession(sessionID)
	}
	r.mu.RLock()
	canvasService := r.canvas
	r.mu.RUnlock()
	if canvasService != nil {
		revoked += canvasService.RevokeSession(sessionID)
	}
	return revoked
}

// DeleteSession tombstones the Canvas document owned by a confirmed native
// session deletion. The Canvas service remains the owner of its storage and
// cleanup semantics; a registry without a composed Canvas is a no-op.
func (r *Registry) DeleteSession(sessionID string) error {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	canvasService := r.canvas
	r.mu.RUnlock()
	if canvasService == nil {
		return nil
	}
	return canvasService.DeleteSession(sessionID)
}

// RevokeModule removes every native MCP registration belonging to a module.
// Module disable/restart calls this before allowing the replacement lifecycle
// to continue.
func (r *Registry) RevokeModule(moduleID string) int {
	if r == nil || r.nativeMCP == nil {
		return 0
	}
	return r.nativeMCP.RevokeModule(moduleID)
}

// AttachCanvas issues the Canvas package's session-scoped capability through
// the registry-owned lifecycle. It is intentionally unavailable while Canvas
// has not been composed or is disabled; callers must not manufacture a token
// from a native session ID.
func (r *Registry) AttachCanvas(sessionID string, generation ...uint64) (canvas.Authority, error) {
	if r == nil {
		return canvas.Authority{}, fmt.Errorf("Canvas module is not configured")
	}
	r.mu.RLock()
	service := r.canvas
	enabled := r.enabled["canvas"]
	r.mu.RUnlock()
	if !enabled || service == nil {
		return canvas.Authority{}, fmt.Errorf("Canvas module is unavailable")
	}
	if ready, detail := r.Health("canvas"); !ready {
		if detail == "" {
			detail = "Canvas module is unavailable"
		}
		return canvas.Authority{}, fmt.Errorf("%s", detail)
	}
	return service.Attach(sessionID, generation...)
}

// AttachCanvasManagement issues a controller-only capability for one verified
// project/session request. Unlike model-facing AttachCanvas it intentionally
// does not require Canvas to be enabled or its optional worker to be ready:
// retained documents must remain inspectable/removable during an outage.
func (r *Registry) AttachCanvasManagement(sessionID string, generation ...uint64) (canvas.Authority, error) {
	if r == nil {
		return canvas.Authority{}, fmt.Errorf("Canvas module is not configured")
	}
	r.mu.RLock()
	service := r.canvas
	r.mu.RUnlock()
	if service == nil {
		return canvas.Authority{}, fmt.Errorf("Canvas module is unavailable")
	}
	return service.AttachManagement(sessionID, generation...)
}

// AdvanceGeneration invalidates registrations from older generations for one
// exact module/server/session scope before a replacement native binding is
// admitted.
func (r *Registry) AdvanceGeneration(moduleID, serverID, sessionID string, generation uint64) (int, error) {
	if r == nil || r.nativeMCP == nil {
		return 0, fmt.Errorf("native MCP registry is not configured")
	}
	return r.nativeMCP.AdvanceGeneration(moduleID, serverID, sessionID, generation)
}

// RevokeAll removes every ephemeral native MCP registration. It is used when
// the publisher shuts down; persisted module enablement never persists these
// credentials.
func (r *Registry) revokeAll() int {
	if r == nil || r.nativeMCP == nil {
		return 0
	}
	return r.nativeMCP.RevokeAll()
}

func validateState(value persistedState) error {
	return validateCompleteState(value)
}

// SetEnabled persists module enablement in the Pixie app state and reconciles
// the owning service. Unknown modules fail closed. Persisted desired state is
// published before a service is enabled, while slow construction happens
// outside the registry request lock.
func (r *Registry) SetEnabled(id string, enabled bool) error {
	if _, ok := r.ModuleDefinition(id); !ok {
		return fmt.Errorf("unknown in-process MCP module %q", id)
	}
	r.mutationMu.Lock()
	defer r.mutationMu.Unlock()
	r.mu.RLock()
	unchanged := r.enabled[id] == enabled
	current := clonePersistedState(r.persisted)
	r.mu.RUnlock()
	if unchanged {
		if !enabled {
			// A repeated disable is still a lifecycle boundary. Do not leave a
			// registration issued before a previous cleanup usable.
			r.RevokeModule(id)
		}
		return nil
	}
	candidate := CompleteStateWithDesired(current, id, enabled)
	outcome, err := WriteCompleteStateWithOutcome(r.store, candidate, persist.PublishFaults{})
	if err != nil {
		if outcome.Kind == persist.OutcomeDurabilityUncertain {
			if reconciled, reconcileErr := ReconcileCompleteState(r.store); reconcileErr == nil {
				r.mu.Lock()
				r.persisted = clonePersistedState(reconciled)
				for _, definition := range r.definitions {
					if state, ok := reconciled.Modules[definition.ID]; ok {
						r.enabled[definition.ID] = state.Enabled
					}
				}
				r.mu.Unlock()
			}
		}
		return fmt.Errorf("persist in-process MCP module state: %w", err)
	}
	if !outcome.MayDispatch() {
		return fmt.Errorf("persist in-process MCP module state: mutation was not committed")
	}
	// Publish the desired state only after the complete map is durably
	// installed. A failed write leaves the prior catalog and runtime untouched.
	r.mu.Lock()
	r.persisted = clonePersistedState(candidate)
	r.enabled[id] = enabled
	r.mu.Unlock()
	if !enabled {
		// Revocation happens before stopping the module so no new privileged
		// continuation can use a credential after disablement is committed.
		r.RevokeModule(id)
	}
	return r.reconcileModule(id, enabled)
}

// Restart reconstructs an enabled module without changing its persisted
// desired state. It is intentionally separate from SetEnabled so a retry of an
// unchanged enablement cannot interrupt a healthy Browser service.
func (r *Registry) Restart(id string) error {
	definition, ok := r.ModuleDefinition(id)
	if !ok {
		return fmt.Errorf("unknown in-process MCP module %q", id)
	}
	r.mutationMu.Lock()
	defer r.mutationMu.Unlock()
	r.mu.RLock()
	enabled := r.enabled[id]
	r.mu.RUnlock()
	if !enabled {
		return fmt.Errorf("cannot restart disabled in-process MCP module %q", id)
	}
	runtime, err := r.constructModule(definition.ID, true)
	if err != nil {
		r.recordFailure(definition.ID, err)
		r.logger.Error("in-process MCP module restart failed", "module", definition.ID, "error", err)
		return fmt.Errorf("restart in-process MCP module %q: %w", id, err)
	}
	// Restart creates a new module runtime generation. Credentials issued to
	// the previous runtime must not cross that boundary. Revoke before exposing
	// the replacement so there is no interval where new routing observes a new
	// handler while old credentials remain accepted.
	r.RevokeModule(id)
	previous := r.installRuntime(definition.ID, runtime)
	if previous != nil && previous.shutdown != nil {
		previous.shutdown()
	}
	return nil
}

// Catalog returns the in-process publisher catalog with Pixie-owned
// enablement. Registered modules remain visible while disabled or unavailable
// so the UI can explain and explicitly retry each independent lifecycle.
func (r *Registry) Catalog() Catalog {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.catalogLocked()
}

func (r *Registry) catalogLocked() Catalog {
	modules := make([]Module, 0, len(r.definitions))
	degraded := false
	for _, definition := range r.definitions {
		ready, detail := r.healthLocked(definition.ID)
		state := "ready"
		if !ready {
			state = "unavailable"
			// Disabled optional modules do not make the gateway degraded. The
			// legacy Browser disabled state remains degraded for compatibility
			// with the existing Tools UI and readiness semantics. Flag-driven,
			// never a module-name comparison.
			if r.enabled[definition.ID] || definition.DegradesGatewayWhenDisabled {
				degraded = true
			}
		}
		module := Module{
			ID: definition.ID, ExtensionName: definition.ExtensionName,
			DisplayName: definition.DisplayName, Description: definition.Description,
			Path: definition.Path, Transport: definition.Transport,
			Enabled: r.enabled[definition.ID], State: state,
			Endpoint: r.endpointForLocked(definition.Path),
		}
		if detail != "" {
			module.Detail = detail
		}
		modules = append(modules, module)
	}
	gateway := GatewaySummary{State: "ready"}
	if degraded {
		gateway.State, gateway.Detail = "degraded", "One or more published modules are unavailable."
	}
	return Catalog{
		SchemaVersion: 1, Revision: r.revisionLocked(), Gateway: gateway,
		Modules: modules, Engine: "in-process",
	}
}

func (r *Registry) revisionLocked() string {
	// json.Marshal sorts map keys, making unknown persisted entries part of a
	// deterministic committed-state revision without routing or executing them.
	value := struct {
		Definitions []ModuleDefinition `json:"definitions"`
		Enabled     map[string]bool    `json:"enabled"`
		Persisted   persistedState     `json:"persisted"`
	}{Definitions: r.definitions, Enabled: r.enabled, Persisted: r.persisted}
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])[:16]
}

func (r *Registry) endpointLocked() string {
	return r.endpointForLocked(BrowserRoute)
}

func (r *Registry) endpointForLocked(route string) string {
	if r.config.Port <= 0 {
		return route
	}
	host := r.config.Host
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(r.config.Port)) + route
}

// Endpoint returns the controller-local URL Pi clients use for the Browser
// module through this publisher.
func (r *Registry) Endpoint() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.endpointLocked()
}

// Shutdown stops the published modules. The persist store keeps enablement
// for the next start.
func (r *Registry) Shutdown() {
	r.mutationMu.Lock()
	defer r.mutationMu.Unlock()
	r.revokeAll()
	r.mu.Lock()
	runtimes := make([]*moduleRuntime, 0, len(r.modules))
	for id, runtime := range r.modules {
		runtimes = append(runtimes, runtime)
		delete(r.modules, id)
	}
	r.browser = nil
	r.canvas = nil
	r.design = nil
	r.mu.Unlock()
	for _, runtime := range runtimes {
		if runtime != nil && runtime.shutdown != nil {
			runtime.shutdown()
		}
	}
}

// BrowserLegacyHandler serves the Browser service's own REST surface
// (/v1/browser, /v1/browser/leases, /v1/artifacts/*) from the in-process
// module, preserving the former separate host's panel and artifact
// compatibility routes. The handler re-checks module enablement on every call
// and returns nil while the module is disabled or degraded; callers fall back
// to their external BrowserURL proxy in that case.
func (r *Registry) BrowserLegacyHandler() func() http.Handler {
	return func() http.Handler {
		r.mu.RLock()
		service := r.browser
		enabled := r.enabled[browserID]
		r.mu.RUnlock()
		if !enabled || service == nil {
			return nil
		}
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			// The Browser service keeps its own bearer, host, and origin checks;
			// it stays the trust boundary for panel and artifact traffic.
			clone := request.Clone(request.Context())
			service.ServeHTTP(response, clone)
		})
	}
}

func portOrDefault(port int) int {
	if port > 0 {
		return port
	}
	return 7312
}

func (r *Registry) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	switch {
	case request.URL.Path == CatalogPath:
		if request.Method != http.MethodGet {
			methodNotAllowed(response, http.MethodGet)
			return
		}
		if !r.authorized(request) {
			writeJSON(response, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		writeJSON(response, http.StatusOK, r.Catalog())
	case request.URL.Path == StatusPath:
		if request.Method != http.MethodGet {
			methodNotAllowed(response, http.MethodGet)
			return
		}
		if !r.authorized(request) {
			writeJSON(response, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		catalog := r.Catalog()
		writeJSON(response, http.StatusOK, map[string]any{
			"build": r.build, "startedAt": r.started.UTC().Format(time.RFC3339), "catalog": catalog,
		})
	case r.IsMCPRoute(request.URL.Path) || r.IsManagementRoute(request.URL.Path):
		id, runtime, enabled := r.serviceForRoute(request.URL.Path)
		definition, haveDefinition := r.ModuleDefinition(id)
		scoped := haveDefinition && definition.SessionScoped
		isMCP := r.IsMCPRoute(request.URL.Path)
		isManagement := r.IsManagementRoute(request.URL.Path)
		// Session-scoped modules authenticate each MCP request with their own
		// per-session capability in the module handler; requiring the
		// publisher bearer here would replace that session credential and
		// make scoped MCP calls impossible. Every other module MCP surface
		// uses the publisher bearer at this boundary. Flag-driven, never a
		// module-name comparison.
		if isMCP && !scoped && !r.authorized(request) {
			writeJSON(response, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		if runtime == nil {
			if !enabled && isMCP {
				writeJSON(response, http.StatusNotFound, map[string]string{"code": "not_found"})
				return
			}
			writeJSON(response, http.StatusServiceUnavailable, map[string]string{"code": "unavailable"})
			return
		}
		if isMCP && !enabled {
			writeJSON(response, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		// Route ownership is registered independently of readiness so retained
		// management/removal data remains reachable, but model-facing MCP work
		// must fail closed when a mandatory worker or other dependency is absent.
		guideRoute := strings.HasSuffix(request.URL.Path, "/guide")
		if isMCP && !guideRoute && runtime.ready != nil && !runtime.ready() {
			detail := "module is unavailable"
			if runtime.readinessDetail != nil {
				if candidate := runtime.readinessDetail(); candidate != "" {
					detail = candidate
				}
			}
			writeJSON(response, http.StatusServiceUnavailable, map[string]string{"code": "unavailable", "detail": detail})
			return
		}
		if runtime.handler == nil {
			writeJSON(response, http.StatusServiceUnavailable, map[string]string{"code": "unavailable"})
			return
		}
		delegated := r.moduleRequest(id, request.URL.Path, request)
		if isManagement && scoped {
			scope, ok := ManagementScopeFromContext(request.Context())
			if ok {
				authority, err := r.AttachCanvasManagement(scope.SessionID, scope.Generation)
				if err != nil {
					writeJSON(response, http.StatusServiceUnavailable, map[string]string{"code": "unavailable"})
					return
				}
				service := runtime.canvas
				if service == nil {
					writeJSON(response, http.StatusServiceUnavailable, map[string]string{"code": "unavailable"})
					return
				}
				delegated.Header.Del("Cookie")
				delegated.Header.Del("Authorization")
				delegated = delegated.WithContext(canvas.ContextWithManagementAuthority(delegated.Context(), authority))
				defer service.Revoke(authority)
			} else if unscopedHealthAlias(definition, request.URL.Path) == "" {
				// Direct registry callers must explicitly provide the controller's
				// verified scope for the scoped management surface. Keep the legacy
				// unscoped status compatibility route for health/catalog tests.
				writeJSON(response, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
				return
			}
		}
		runtime.handler.ServeHTTP(response, delegated)
	default:
		writeJSON(response, http.StatusNotFound, map[string]string{"code": "not_found"})
	}
}

// authorized guards the publisher metadata endpoints with the same credential
// as module traffic: the shared PIXIE_MCP_TOKEN bearer when configured,
// otherwise same-origin fetch metadata like the former separate host.
func (r *Registry) authorized(request *http.Request) bool {
	r.mu.RLock()
	token := r.config.Token
	publicOrigin := r.config.PublicOrigin
	r.mu.RUnlock()
	if token != "" {
		values := request.Header.Values("Authorization")
		if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") ||
			!constantTimeEqual(strings.TrimPrefix(values[0], "Bearer "), token) {
			return false
		}
	} else if site := request.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	if origin := request.Header.Get("Origin"); origin != "" {
		normalized, err := normalizeOrigin(origin)
		if err != nil {
			return false
		}
		expected := publicOrigin
		if expected == "" {
			scheme := "http"
			if request.TLS != nil {
				scheme = "https"
			}
			expected, err = normalizeOrigin(scheme + "://" + request.Host)
			if err != nil {
				return false
			}
		}
		if normalized != expected {
			return false
		}
	}
	return true
}

func normalizeOrigin(value string) (string, error) {
	if value == "" || len(value) > 512 || strings.TrimSpace(value) != value {
		return "", fmt.Errorf("invalid origin")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
		parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid origin")
	}
	host := strings.ToLower(parsed.Hostname())
	if port := parsed.Port(); port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return strings.ToLower(parsed.Scheme) + "://" + host, nil
}

func constantTimeEqual(left, right string) bool {
	leftDigest := sha256.Sum256([]byte(left))
	rightDigest := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftDigest[:], rightDigest[:]) == 1
}

func writeJSON(response http.ResponseWriter, status int, body any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	encoded, err := json.Marshal(body)
	if err == nil {
		_, _ = response.Write(encoded)
	}
}

func methodNotAllowed(response http.ResponseWriter, allowed string) {
	response.Header().Set("Allow", allowed)
	writeJSON(response, http.StatusMethodNotAllowed, map[string]string{"code": "method_not_allowed"})
}

// unscopedHealthAlias reports the legacy unscoped status compatibility route
// for a session-scoped definition. Direct callers without a controller-verified
// scope may still reach the module health probe there; every other scoped
// management route requires an explicit scope.
func unscopedHealthAlias(definition ModuleDefinition, route string) string {
	if len(definition.ManagementPaths) == 0 {
		return ""
	}
	if route == definition.ManagementPaths[0]+"/status" {
		return "/health"
	}
	return ""
}
