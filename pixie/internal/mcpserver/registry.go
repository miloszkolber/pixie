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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miloszkolber/pixie/internal/browser"
	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/persist"
)

const (
	// BrowserRoute is the Browser module path on the controller listener; the
	// publisher now lives in the main Pixie process (Stage F merge).
	BrowserRoute = "/mcp/browser"
	// CatalogPath and StatusPath are the in-process publisher API. They mirror
	// the former separate host's /v1/mcp/modules and /v1/mcp/status shape with
	// the in-process route; exact paths remain an implementation detail.
	CatalogPath = "/api/mcp/modules"
	StatusPath  = "/api/mcp/status"

	storeFile  = "mcp-modules.json"
	browserID  = "browser"
	transports = "streamable_http"
)

// Browser engine preference persisted in Pixie app state. Chromium is the
// default and only evaluated backend; obscura selects a CDP backend (an
// "obscura serve" endpoint via PIXIE_BROWSER_CDP) once it passes the
// compatibility suite in docs/roadmap.md. The engine switch never alters the
// model-facing API: extension name, tools, and resources stay identical.
const (
	engineStoreFile = "browser.json"
	engineChromium  = "chromium"
	engineObscura   = "obscura"
	defaultEngine   = engineChromium
)

// Deprecated-pending-parity: PIXIE_MCP_MODULES and PIXIE_MCP_DISABLED_MODULES
// remain the fallback default for publication until the Tools UI toggle
// reaches parity. Persisted enablement in mcp-modules.json wins once the
// operator toggles a module; afterwards the environment is ignored for
// that module.
const (
	envModules  = "PIXIE_MCP_MODULES"
	envDisabled = "PIXIE_MCP_DISABLED_MODULES"
)

// Module is the registry record for one published Pixie MCP module. Browser
// is currently the only module; additions stay compile-time and behind the
// same storage and trust boundary.
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
	if id != browserID || !r.enabled[browserID] {
		return false, "The Browser module is disabled in Pixie MCP servers."
	}
	if r.engine == engineObscura && r.browser == nil {
		return false, "The Browser module is not ready: the obscura engine needs PIXIE_BROWSER_CDP."
	}
	if r.browser == nil || !r.browser.Ready() {
		return false, "The Browser module is not ready."
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

// engineState is the persisted Browser engine preference. Unknown values
// read back as the default so a stale or hand-edited file degrades to the
// evaluated Chromium backend instead of failing the publisher.
type engineState struct {
	Engine string `json:"engine"`
}

// Registry is the in-process Pixie MCP publisher. It wraps one Browser module
// behind enable/disable state owned by the Pixie persist store, served on the
// controller listener by the main Pixie process.
type Registry struct {
	config  Config
	build   diagnostics.BuildInfo
	logger  *slog.Logger
	store   persist.Store
	started time.Time

	mu      sync.RWMutex
	browser *browser.Service
	enabled map[string]bool
	engine  string
}

// NewRegistry loads persisted module enablement, falling back to the
// deprecated-pending-parity PIXIE_MCP_MODULES/DISABLED environment default,
// and starts the enabled modules. A Browser module that cannot start (for
// example a missing agent-browser binary in the application image) degrades
// the catalog instead of failing the publisher.
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
		config:  config,
		build:   diagnostics.NormalizeBuild(build.Version, build.Revision),
		logger:  logger,
		store:   persist.Store{Dir: config.DataDir},
		started: time.Now(),
		enabled: map[string]bool{browserID: true},
	}
	if err := registry.loadEnabled(); err != nil {
		return nil, err
	}
	if err := registry.loadEngine(); err != nil {
		return nil, err
	}
	registry.startLocked()
	return registry, nil
}

func (r *Registry) loadEnabled() error {
	defaults := map[string]bool{browserID: true}
	if r.config.Getenv != nil {
		modulesEnv, _ := r.config.Getenv(envModules)
		disabledEnv, _ := r.config.Getenv(envDisabled)
		modules := parseList(modulesEnv, browserID)
		disabled := parseList(disabledEnv, "")
		active := make(map[string]bool, len(modules))
		for _, id := range modules {
			if id == browserID {
				active[id] = true
			}
		}
		for _, id := range disabled {
			delete(active, id)
		}
		defaults[browserID] = active[browserID]
	}
	var saved persistedState
	found, err := persist.Read(r.store, storeFile, &saved, validateState)
	if err != nil {
		return fmt.Errorf("read in-process MCP module state: %w", err)
	}
	if !found {
		r.enabled = defaults
		return nil
	}
	if state, ok := saved.Modules[browserID]; ok {
		r.enabled[browserID] = state.Enabled
	} else {
		r.enabled[browserID] = defaults[browserID]
	}
	return nil
}

func validateState(value persistedState) error {
	if value.Modules == nil {
		return fmt.Errorf("modules must be an object")
	}
	for id := range value.Modules {
		if id != browserID {
			return fmt.Errorf("unknown in-process MCP module %q", id)
		}
	}
	return nil
}

// normalizeEngine maps any persisted engine value to a known backend,
// defaulting to the evaluated Chromium backend.
func normalizeEngine(value string) string {
	if value == engineObscura {
		return engineObscura
	}
	return engineChromium
}

func validateEngineState(value engineState) error {
	if value.Engine != engineChromium && value.Engine != engineObscura {
		return fmt.Errorf("unknown browser engine %q", value.Engine)
	}
	return nil
}

func (r *Registry) loadEngine() error {
	var saved engineState
	found, err := persist.Read(r.store, engineStoreFile, &saved, validateEngineState)
	if err != nil {
		return fmt.Errorf("read browser engine state: %w", err)
	}
	if !found {
		r.engine = defaultEngine
		return nil
	}
	r.engine = normalizeEngine(saved.Engine)
	return nil
}

// Engine reports the persisted Browser engine preference. It defaults to
// chromium; obscura stays unevaluated until the compatibility suite passes.
func (r *Registry) Engine() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.engine
}

// SetEngine persists the Browser engine preference in Pixie app state and
// restarts the module on the selected backend. Unknown engines fail closed.
// Switching engines does not alter the model-facing API.
func (r *Registry) SetEngine(engine string) error {
	if engine != engineChromium && engine != engineObscura {
		return fmt.Errorf("unknown browser engine %q", engine)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.engine = engine
	if err := persist.Write(r.store, engineStoreFile, engineState{Engine: engine}, validateEngineState); err != nil {
		return fmt.Errorf("persist browser engine state: %w", err)
	}
	r.startLocked()
	return nil
}

func parseList(value, fallback string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		if fallback == "" {
			return nil
		}
		return []string{fallback}
	}
	var result []string
	for _, item := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// SetEnabled persists module enablement in the Pixie app state and starts or
// stops the module. Unknown modules fail closed; Browser is the only module.
func (r *Registry) SetEnabled(id string, enabled bool) error {
	if id != browserID {
		return fmt.Errorf("unknown in-process MCP module %q", id)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled[id] = enabled
	state := persistedState{Modules: map[string]persistedModule{id: {Enabled: enabled}}}
	if err := persist.Write(r.store, storeFile, state, validateState); err != nil {
		return fmt.Errorf("persist in-process MCP module state: %w", err)
	}
	r.startLocked()
	return nil
}

// Catalog returns the in-process publisher catalog with Pixie-owned
// enablement. Switching publisher engines does not alter the model-facing
// API: the Browser extension name, tools, and resource surface are identical.
func (r *Registry) Catalog() Catalog {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.catalogLocked()
}

func (r *Registry) catalogLocked() Catalog {
	enabled := r.enabled[browserID]
	detail := ""
	state := "ready"
	if !enabled {
		state, detail = "unavailable", "The Browser module is disabled in Pixie MCP servers."
	} else if r.browser == nil || !r.browser.Ready() {
		state, detail = "unavailable", "The Browser module is not ready."
		if r.engine == engineObscura {
			detail = "The Browser module is not ready: the obscura engine needs PIXIE_BROWSER_CDP."
		}
	}
	module := Module{
		ID: browserID, ExtensionName: "pixie-browser", DisplayName: "Pixie Browser",
		Description: "Bounded browser automation and browser guidance.",
		Path:        BrowserRoute, Transport: transports,
		Enabled: enabled, State: state, Endpoint: r.endpointLocked(),
	}
	if detail != "" {
		module.Detail = detail
	}
	gateway := GatewaySummary{State: "ready"}
	if state != "ready" {
		gateway.State, gateway.Detail = "degraded", "One or more published modules are unavailable."
	}
	return Catalog{
		SchemaVersion: 1, Revision: r.revisionLocked(enabled), Gateway: gateway,
		Modules: []Module{module}, Engine: "in-process",
	}
}

func (r *Registry) revisionLocked(enabled bool) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		browserID, "pixie-browser", "Pixie Browser",
		"Bounded browser automation and browser guidance.",
		BrowserRoute, transports, strconv.FormatBool(enabled),
	}, "\x00")))
	return hex.EncodeToString(digest[:])[:16]
}

func (r *Registry) endpointLocked() string {
	if r.config.Port <= 0 {
		return BrowserRoute
	}
	host := r.config.Host
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(r.config.Port)) + BrowserRoute
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
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.browser != nil {
		r.browser.Shutdown()
		r.browser = nil
	}
}

// BrowserLegacyHandler serves the Browser service's own REST surface
// (/v1/browser, /v1/browser/leases, /v1/artifacts/*, /v1/app-views*) from the
// in-process module, preserving the former separate host's panel and artifact
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

func (r *Registry) startLocked() {
	if !r.enabled[browserID] {
		if r.browser != nil {
			r.browser.Shutdown()
			r.browser = nil
		}
		return
	}
	config := r.browserConfig()
	if r.engine == engineObscura && config.CDPEndpoint == "" {
		// Never silently fall back to Chromium while obscura is selected:
		// degrade the module until the operator configures the CDP endpoint.
		r.logger.Error("obscura browser engine needs PIXIE_BROWSER_CDP")
		if r.browser != nil {
			r.browser.Shutdown()
			r.browser = nil
		}
		return
	}
	service, err := browser.NewService(config, r.build, r.logger)
	if err != nil {
		r.logger.Error("in-process Browser module unavailable", "error", err)
		if r.browser != nil {
			r.browser.Shutdown()
			r.browser = nil
		}
		return
	}
	if r.browser != nil {
		r.browser.Shutdown()
	}
	r.browser = service
}

func (r *Registry) browserConfig() browser.Config {
	lookup := r.config.Getenv
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}
	config, err := browser.ConfigFromEnvironment(func(key string) (string, bool) {
		switch key {
		case "PIXIE_BROWSER_HOST":
			return r.config.Host, true
		case "PIXIE_BROWSER_PORT":
			return strconv.Itoa(portOrDefault(r.config.Port)), true
		case "PIXIE_BROWSER_AUTH":
			return strconv.FormatBool(r.config.Token != ""), true
		case "PIXIE_BROWSER_TOKEN":
			return r.config.Token, r.config.Token != ""
		case "PIXIE_BROWSER_PUBLIC_ORIGIN":
			return r.config.PublicOrigin, r.config.PublicOrigin != ""
		}
		// PIXIE_BROWSER_* operator settings (binary path, config file,
		// timeouts) still apply; network settings stay publisher-owned.
		return lookup(key)
	})
	if err != nil {
		// ConfigFromEnvironment only fails on malformed operator overrides;
		// fall back to publisher-owned settings so one bad variable degrades
		// the module instead of the whole publisher.
		r.logger.Error("in-process Browser operator config invalid, using publisher defaults", "error", err)
		config = browser.Config{}
	}
	if config.Host == "" {
		config.Host = r.config.Host
	}
	if config.Port == 0 {
		config.Port = portOrDefault(r.config.Port)
	}
	config.Authentication = r.config.Token != ""
	config.Token = r.config.Token
	config.PublicOrigin = r.config.PublicOrigin
	// The persisted engine preference wins over the operator CDP override: a
	// chromium engine never connects to a CDP backend, and obscura never
	// launches Chromium silently (startLocked degrades it without an endpoint).
	if r.engine != engineObscura {
		config.CDPEndpoint = ""
	}
	// Storage isolation: this publisher stores Browser state under the
	// controller data directory instead of the image-level browser roots.
	config.ArtifactRoot = filepath.Join(r.config.DataDir, "mcp-browser", "artifacts")
	config.StateRoot = filepath.Join(r.config.DataDir, "mcp-browser", "state")
	if binaries := r.config.Binaries; binaries != nil {
		if binaries.AgentBrowser != "" {
			config.AgentBrowser = binaries.AgentBrowser
		}
		if binaries.BrowserConfig != "" {
			config.BrowserConfig = binaries.BrowserConfig
		}
		if binaries.ArtifactRoot != "" {
			config.ArtifactRoot = binaries.ArtifactRoot
		}
		if binaries.StateRoot != "" {
			config.StateRoot = binaries.StateRoot
		}
	}
	return config
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
			"browserEngine": r.Engine(),
		})
	case request.URL.Path == BrowserRoute || strings.HasPrefix(request.URL.Path, BrowserRoute+"/"):
		r.mu.RLock()
		service := r.browser
		enabled := r.enabled[browserID]
		r.mu.RUnlock()
		if !enabled || service == nil {
			writeJSON(response, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		// The Browser service keeps its own bearer, host, and origin checks
		// against the controller-facing configuration; it stays the trust
		// boundary for module traffic.
		clone := request.Clone(request.Context())
		urlCopy := *request.URL
		trimmed := strings.TrimPrefix(request.URL.Path, BrowserRoute)
		if trimmed == "" {
			trimmed = "/mcp"
		}
		urlCopy.Path, urlCopy.RawPath = trimmed, ""
		clone.URL = &urlCopy
		clone.RequestURI = trimmed
		if request.URL.RawQuery != "" {
			clone.RequestURI += "?" + request.URL.RawQuery
		}
		service.ServeHTTP(response, clone)
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
