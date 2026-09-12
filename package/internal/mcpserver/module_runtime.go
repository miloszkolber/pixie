package mcpserver

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/miloszkolber/pixie/internal/browser"
	"github.com/miloszkolber/pixie/internal/canvas"
	"github.com/miloszkolber/pixie/internal/design"
)

func clonePersistedState(value persistedState) persistedState {
	clone := persistedState{Modules: make(map[string]persistedModule, len(value.Modules))}
	for key, module := range value.Modules {
		clone.Modules[key] = module
	}
	return clone
}

func (r *Registry) definitionLocked(id string) (ModuleDefinition, bool) {
	for _, definition := range r.definitions {
		if definition.ID == id {
			return definition, true
		}
	}
	return ModuleDefinition{}, false
}

// ModuleDefinition returns a copy of the trusted route/lifecycle declaration.
func (r *Registry) ModuleDefinition(id string) (ModuleDefinition, bool) {
	if r == nil {
		return ModuleDefinition{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.definitionLocked(id)
}

func (r *Registry) startInitial() {
	r.mu.RLock()
	definitions := append([]ModuleDefinition(nil), r.definitions...)
	desired := make(map[string]bool, len(r.enabled))
	for id, enabled := range r.enabled {
		desired[id] = enabled
	}
	r.mu.RUnlock()
	for _, definition := range definitions {
		// Modules without durable retained data skip construction while
		// disabled. Modules with RetainWhileDisabled still restore their
		// stores so status/removal remains available. The decision consumes
		// only the declarative flag, never a module-name switch.
		if !definition.RetainWhileDisabled && !desired[definition.ID] {
			continue
		}
		runtime, err := r.constructModule(definition.ID, desired[definition.ID])
		if err != nil {
			r.recordFailure(definition.ID, err)
			r.logger.Error("in-process MCP module unavailable", "module", definition.ID, "error", err)
			continue
		}
		r.installRuntime(definition.ID, runtime)
	}
}

func (r *Registry) recordFailure(id string, err error) {
	detail := "module startup failed"
	if err != nil {
		detail = err.Error()
	}
	r.mu.Lock()
	if r.failures == nil {
		r.failures = make(map[string]string)
	}
	r.failures[id] = detail
	r.mu.Unlock()
}

func (r *Registry) clearFailure(id string) {
	r.mu.Lock()
	delete(r.failures, id)
	r.mu.Unlock()
}

func (r *Registry) installRuntime(id string, runtime *moduleRuntime) *moduleRuntime {
	r.mu.Lock()
	previous := r.modules[id]
	r.modules[id] = runtime
	delete(r.failures, id)
	switch id {
	case browserID:
		if runtime == nil {
			r.browser = nil
		} else {
			r.browser = runtime.browser
		}
	case "canvas":
		if runtime == nil {
			r.canvas = nil
		} else {
			r.canvas = runtime.canvas
		}
	case "design":
		if runtime == nil {
			r.design = nil
		} else {
			r.design = runtime.design
		}
	}
	r.mu.Unlock()
	return previous
}

func (r *Registry) removeRuntime(id string) *moduleRuntime {
	r.mu.Lock()
	previous := r.modules[id]
	delete(r.modules, id)
	switch id {
	case browserID:
		r.browser = nil
	case "canvas":
		r.canvas = nil
	case "design":
		r.design = nil
	}
	r.mu.Unlock()
	return previous
}

func (r *Registry) reconcileModule(id string, enabled bool) error {
	if !enabled {
		runtime := r.removeOrDisable(id)
		// Only ephemeral modules drop their runtime on disable and need a
		// shutdown; retained modules keep their store for status/removal.
		// The distinction is the declarative RetainWhileDisabled flag.
		if definition, ok := r.ModuleDefinition(id); !ok || !definition.RetainWhileDisabled {
			if runtime != nil && runtime.shutdown != nil {
				runtime.shutdown()
			}
		}
		return nil
	}

	r.mu.RLock()
	runtime := r.modules[id]
	r.mu.RUnlock()
	if runtime == nil {
		newRuntime, err := r.constructModule(id, true)
		if err != nil {
			// Desired state remains true and the catalog reports unavailable;
			// startup failure is intentionally local to this module.
			r.recordFailure(id, err)
			r.logger.Error("in-process MCP module unavailable", "module", id, "error", err)
			return nil
		}
		previous := r.installRuntime(id, newRuntime)
		if previous != nil && previous.shutdown != nil {
			previous.shutdown()
		}
		return nil
	}
	if runtime.enable != nil {
		runtime.enable()
	}
	return nil
}

func (r *Registry) removeOrDisable(id string) *moduleRuntime {
	r.mu.RLock()
	runtime := r.modules[id]
	r.mu.RUnlock()
	if runtime == nil {
		return nil
	}
	// Ephemeral modules drop their runtime on disable; retained modules keep
	// the installed store and only change admission. Flag-driven, not
	// name-driven.
	if definition, ok := r.ModuleDefinition(id); !ok || !definition.RetainWhileDisabled {
		return r.removeRuntime(id)
	}
	if runtime.disable != nil {
		runtime.disable()
	}
	return runtime
}

func (r *Registry) constructModule(id string, enabled bool) (*moduleRuntime, error) {
	r.mu.RLock()
	config := r.config
	build := r.build
	logger := r.logger
	definition, haveDefinition := r.definitionLocked(id)
	boundaryAllowed := !haveDefinition || r.workerBoundaryAllowsLocked(definition)
	r.mu.RUnlock()
	// An untrusted module never starts merely because it was enabled in
	// persisted state; the verified boundary must exist at construction time.
	// Existing desired state stays true and the failure is recorded locally so
	// readiness reports the missing boundary.
	if enabled && !boundaryAllowed {
		return nil, ErrUnverifiedWorkerBoundary
	}
	switch id {
	case browserID:
		browserConfig, err := StrictBrowserConfig(config)
		if err != nil {
			return nil, err
		}
		service, err := browser.NewService(browserConfig, build, logger)
		if err != nil {
			return nil, err
		}
		return &moduleRuntime{
			handler:  service,
			browser:  service,
			shutdown: service.Shutdown,
			ready:    service.Ready,
			readinessDetail: func() string {
				return "The Browser module is not ready."
			},
		}, nil
	case "canvas":
		canvasConfig := canvas.Config{DataDir: config.DataDir, Enabled: enabled, Logger: logger}
		if config.CanvasConfig != nil {
			canvasConfig = *config.CanvasConfig
			if canvasConfig.DataDir == "" {
				canvasConfig.DataDir = config.DataDir
			}
			canvasConfig.Enabled = enabled
			if canvasConfig.Logger == nil {
				canvasConfig.Logger = logger
			}
		}
		service, err := canvas.New(canvasConfig)
		if err != nil {
			return nil, err
		}
		workerConfigured := canvasConfig.WorkerLauncher != nil
		return &moduleRuntime{
			handler: service, canvas: service, shutdown: func() { _ = service.Shutdown() },
			enable: service.Enable, disable: service.Disable, ready: func() bool {
				return service.Ready() && workerConfigured
			}, readinessDetail: func() string {
				if !workerConfigured {
					return "The Canvas contained worker is not configured."
				}
				return "The Canvas module is not ready."
			},
		}, nil
	case "design":
		designConfig := design.Config{DataDir: config.DataDir, Enabled: enabled, Logger: logger}
		if config.DesignConfig != nil {
			designConfig = *config.DesignConfig
			if designConfig.DataDir == "" {
				designConfig.DataDir = config.DataDir
			}
			designConfig.Enabled = enabled
			if designConfig.Logger == nil {
				designConfig.Logger = logger
			}
		}
		if designConfig.Parser == nil && designConfig.Worker != nil {
			designConfig.Parser = designConfig.Worker
		}
		service, err := design.New(designConfig)
		if err != nil {
			return nil, err
		}
		parserConfigured := designConfig.Parser != nil
		return &moduleRuntime{
			handler: service, design: service, shutdown: func() { _ = service.Shutdown() },
			enable: service.Enable, disable: service.Disable, ready: func() bool {
				return service.Ready() && parserConfigured
			}, readinessDetail: func() string {
				if !parserConfigured {
					return "The Design parser worker is not configured."
				}
				return "The Design module is not ready."
			},
		}, nil
	default:
		return nil, fmt.Errorf("unknown in-process MCP module %q", id)
	}
}

func (r *Registry) serviceForRoute(route string) (string, *moduleRuntime, bool) {
	r.mu.RLock()
	definitions := append([]ModuleDefinition(nil), r.definitions...)
	r.mu.RUnlock()
	// One ownership rule for every module: exact-or-child match over the
	// registry's own definitions, never the compiled defaults and never a
	// per-module switch. Test-registered fixtures resolve through the same
	// path as Browser/Canvas/Design.
	if definition, ok := DefinitionForMCPRoute(definitions, route); ok {
		r.mu.RLock()
		runtime := r.modules[definition.ID]
		enabled := r.enabled[definition.ID]
		r.mu.RUnlock()
		return definition.ID, runtime, enabled
	}
	if definition, ok := DefinitionForManagementRoute(definitions, route); ok {
		r.mu.RLock()
		runtime := r.modules[definition.ID]
		enabled := r.enabled[definition.ID]
		r.mu.RUnlock()
		return definition.ID, runtime, enabled
	}
	return "", nil, false
}

// DefinitionForRoute resolves the owning definition for any registered MCP or
// management route using the registry's own definitions. It powers the
// top-level controller's scope decision without a module-name branch.
func (r *Registry) DefinitionForRoute(route string) (ModuleDefinition, bool) {
	if r == nil {
		return ModuleDefinition{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if definition, ok := DefinitionForMCPRoute(r.definitions, route); ok {
		return definition, true
	}
	return DefinitionForManagementRoute(r.definitions, route)
}

// DefinitionForManagementRoute resolves the owning management definition from
// the registry's own definitions, including test-registered fixtures.
func (r *Registry) DefinitionForManagementRoute(route string) (ModuleDefinition, bool) {
	if r == nil {
		return ModuleDefinition{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return DefinitionForManagementRoute(r.definitions, route)
}

// IsMCPRoute reports ownership using the registry's own definitions,
// including test fixtures. The package-level IsRegisteredMCPRoute covers only
// the compiled defaults.
func (r *Registry) IsMCPRoute(route string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := DefinitionForMCPRoute(r.definitions, route)
	return ok
}

// IsManagementRoute reports management ownership using the registry's own
// definitions, including test fixtures.
func (r *Registry) IsManagementRoute(route string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := DefinitionForManagementRoute(r.definitions, route)
	return ok
}

// RegisterTestModule installs a generic stub module for focused routing tests.
// It validates duplicate/overlap/core-takeover through the same gate as the
// compiled defaults, appends the definition in stable order and installs a
// retained runtime. Production code never calls this; tests shut the registry
// down normally. The stub handler serves MCP, management and artifact paths;
// readiness stays test-controlled through the supplied callbacks.
func (r *Registry) RegisterTestModule(definition ModuleDefinition, handler http.Handler, ready func() bool, readinessDetail func() string) error {
	if r == nil {
		return fmt.Errorf("registry is not configured")
	}
	if handler == nil {
		return fmt.Errorf("test module handler is required")
	}
	if err := ValidateDescriptor(definition.Frontend); err != nil {
		return fmt.Errorf("invalid test module descriptor: %w", err)
	}
	if definition.Frontend.ModuleID != definition.ID {
		return fmt.Errorf("test module descriptor %q must equal definition id %q", definition.Frontend.ModuleID, definition.ID)
	}
	r.mu.RLock()
	candidate := append(append([]ModuleDefinition(nil), r.definitions...), definition)
	r.mu.RUnlock()
	if err := ValidateModuleDefinitions(candidate); err != nil {
		return err
	}
	readyFunc := ready
	if readyFunc == nil {
		readyFunc = func() bool { return true }
	}
	detailFunc := readinessDetail
	if detailFunc == nil {
		detailFunc = func() string { return "" }
	}
	r.mu.Lock()
	r.definitions = candidate
	if _, known := r.enabled[definition.ID]; !known {
		r.enabled[definition.ID] = definition.DefaultEnabled
	}
	if _, known := r.persisted.Modules[definition.ID]; !known {
		if r.persisted.Modules == nil {
			r.persisted.Modules = make(map[string]persistedModule)
		}
		r.persisted.Modules[definition.ID] = persistedModule{Enabled: r.enabled[definition.ID]}
	}
	r.mu.Unlock()
	r.installRuntime(definition.ID, &moduleRuntime{
		handler:         handler,
		ready:           readyFunc,
		readinessDetail: detailFunc,
	})
	return nil
}

func moduleIsMCPRoute(route string) bool {
	return IsRegisteredMCPRoute(route)
}

func moduleIsManagementRoute(route string) bool {
	return IsRegisteredManagementRoute(route)
}

func (r *Registry) moduleRequest(id, route string, request *http.Request) *http.Request {
	clone := request.Clone(request.Context())
	urlCopy := *request.URL
	pathValue := route
	// Generic per-definition translation, never a module-name switch. Strip
	// modules map their external MCP prefix to a service-internal root;
	// session-scoped modules map scoped management routes to canonical
	// operation paths; every other module (Design, fixtures) passes the route
	// through unchanged. Unknown IDs also pass through; validation gates
	// registration so an unknown ID here is a fail-closed programming error,
	// not a routing decision.
	if definition, ok := r.ModuleDefinition(id); ok {
		switch {
		case definition.StripMCPrefix && (route == definition.Path || strings.HasPrefix(route, definition.Path+"/")):
			pathValue = strings.TrimPrefix(route, definition.Path)
			if pathValue == "" {
				pathValue = "/mcp"
			}
		case definition.SessionScoped && isManagementRouteFor(definition, route):
			pathValue = translateSessionScopedManagementRoute(definition, route, request)
		}
	}
	urlCopy.Path, urlCopy.RawPath = pathValue, ""
	clone.URL = &urlCopy
	clone.RequestURI = pathValue
	if request.URL.RawQuery != "" {
		clone.RequestURI += "?" + request.URL.RawQuery
	}
	return clone
}

// isManagementRouteFor reports whether route is owned by definition's
// management surface.
func isManagementRouteFor(definition ModuleDefinition, route string) bool {
	for _, prefix := range definition.ManagementPaths {
		if prefix == "" {
			continue
		}
		if route == prefix || strings.HasPrefix(route, prefix+"/") {
			return true
		}
	}
	return false
}

// translateSessionScopedManagementRoute maps scoped management routes to the
// owning service's canonical operation paths. The controller has already
// verified the project/session scope; the service only needs the operation
// path after authority is attached. The mapping is shared by every
// session-scoped module so adding another scoped module needs no new branch.
func translateSessionScopedManagementRoute(definition ModuleDefinition, route string, request *http.Request) string {
	if len(definition.ManagementPaths) == 0 {
		return route
	}
	prefix := definition.ManagementPaths[0]
	if route != prefix && !strings.HasPrefix(route, prefix+"/") {
		return route
	}
	suffix := strings.TrimPrefix(route, prefix)
	switch {
	case suffix == "" || suffix == "/" || suffix == "/health" || suffix == "/readyz":
		return "/health"
	case suffix == "/status":
		// Preserve the historical health alias for direct callers that have
		// not supplied a controller scope.
		if _, scoped := ManagementScopeFromContext(request.Context()); scoped {
			return prefix + "/status"
		}
		return "/health"
	case strings.HasPrefix(suffix, "/status/"):
		return prefix + "/status"
	case suffix == "/remove":
		return prefix + "/remove"
	case strings.HasPrefix(suffix, "/remove/"):
		return prefix + "/remove"
	case strings.HasPrefix(suffix, "/artifact/"):
		parts := strings.Split(strings.TrimPrefix(suffix, "/artifact/"), "/")
		if len(parts) == 3 {
			return prefix + "/artifact/" + parts[1] + "/" + parts[2]
		} else if len(parts) == 4 {
			return prefix + "/artifact/" + parts[2] + "/" + parts[3]
		}
	case strings.HasPrefix(suffix, "/artifacts/"):
		parts := strings.Split(strings.TrimPrefix(suffix, "/artifacts/"), "/")
		if len(parts) == 3 {
			return prefix + "/artifacts/" + parts[1] + "/" + parts[2]
		} else if len(parts) == 4 {
			return prefix + "/artifacts/" + parts[2] + "/" + parts[3]
		}
	default:
		// Also accept target-first management routes. The controller has
		// already verified the first two segments and the service only needs
		// the canonical operation path after authority is attached.
		parts := strings.Split(strings.Trim(suffix, "/"), "/")
		if len(parts) == 2 && (parts[1] == "status" || parts[1] == "remove") {
			return prefix + "/" + parts[1]
		} else if len(parts) == 4 && (parts[1] == "artifact" || parts[1] == "artifacts") {
			return prefix + "/" + parts[1] + "/" + parts[2] + "/" + parts[3]
		} else if len(parts) == 3 && (parts[2] == "status" || parts[2] == "remove") {
			return prefix + "/" + parts[2]
		} else if len(parts) == 5 && (parts[2] == "artifact" || parts[2] == "artifacts") {
			return prefix + "/" + parts[2] + "/" + parts[3] + "/" + parts[4]
		}
	}
	return route
}
