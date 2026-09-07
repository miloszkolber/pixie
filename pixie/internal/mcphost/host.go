package mcphost

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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miloszkolber/pixie/internal/browser"
	"github.com/miloszkolber/pixie/internal/diagnostics"
)

const (
	CatalogPath       = "/v1/mcp/modules"
	GatewayStatusPath = "/v1/mcp/status"
	LivePath          = "/livez"
	ReadyPath         = "/readyz"
	StatusPath        = "/status" // legacy Browser status route
	maxBody           = 64 * 1024
)

type ModuleDescriptor struct {
	ID            string `json:"id"`
	ExtensionName string `json:"extensionName"`
	DisplayName   string `json:"displayName"`
	Description   string `json:"description"`
	Path          string `json:"path"`
	Transport     string `json:"transport"`
}

type ModuleSummary struct {
	ModuleDescriptor
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

type Catalog struct {
	SchemaVersion int             `json:"schemaVersion"`
	Revision      string          `json:"revision"`
	Gateway       GatewaySummary  `json:"gateway"`
	Modules       []ModuleSummary `json:"modules"`
}

type GatewaySummary struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

type module interface {
	Descriptor() ModuleDescriptor
	ServeHTTP(http.ResponseWriter, *http.Request)
	Ready() bool
	Shutdown()
}

type moduleFactory func(Config, diagnostics.BuildInfo, *slog.Logger) (module, error)

type browserModule struct {
	service *browser.Service
}

var moduleFactories = map[string]moduleFactory{
	// Keep modules explicit and compile-time registered. A module that needs a
	// different trust or storage boundary belongs behind a sidecar instead of
	// being added to this in-process registry.
	"browser": newBrowserModule,
}

func newBrowserModule(config Config, build diagnostics.BuildInfo, logger *slog.Logger) (module, error) {
	service, err := browser.NewService(config.BrowserConfig, build, logger)
	if err != nil {
		return nil, err
	}
	return &browserModule{service: service}, nil
}

func (m *browserModule) Descriptor() ModuleDescriptor {
	return ModuleDescriptor{
		ID: "browser", ExtensionName: "pixie-browser", DisplayName: "Pixie Browser",
		Description: "Bounded browser automation and browser guidance.", Path: "/browser", Transport: "streamable_http",
	}
}

func (m *browserModule) Ready() bool { return m.service.Ready() }
func (m *browserModule) Shutdown()   { m.service.Shutdown() }

func (m *browserModule) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	clone := request.Clone(request.Context())
	urlCopy := *request.URL
	switch {
	case request.URL.Path == "/browser":
		urlCopy.Path, urlCopy.RawPath = "/mcp", ""
		clone.RequestURI = requestURI("/mcp", urlCopy.RawQuery)
	case strings.HasPrefix(request.URL.Path, "/browser/"):
		path := strings.TrimPrefix(request.URL.Path, "/browser")
		urlCopy.Path, urlCopy.RawPath = path, ""
		clone.RequestURI = requestURI(path, urlCopy.RawQuery)
	default:
		// Legacy routes are already Browser routes. Keep their path intact so
		// /status, /v1/browser, and artifact/app-view compatibility endpoints
		// continue to work when the host is placed in front of Browser.
		clone.RequestURI = requestURI(urlCopy.Path, urlCopy.RawQuery)
	}
	clone.URL = &urlCopy
	m.service.ServeHTTP(response, clone)
}

func requestURI(path, rawQuery string) string {
	if rawQuery == "" {
		return path
	}
	return path + "?" + rawQuery
}

type Service struct {
	config   Config
	build    diagnostics.BuildInfo
	logger   *slog.Logger
	started  time.Time
	modules  map[string]module
	byPath   map[string]module
	revision string
	mu       sync.RWMutex
}

func NewService(config Config, build diagnostics.BuildInfo, logger *slog.Logger) (*Service, error) {
	if logger == nil {
		logger = diagnostics.NewLogger("mcp", build)
	}
	if config.Host == "" {
		config.Host = defaultHost
	}
	if config.Port == 0 {
		config.Port = defaultPort
	}
	if config.Port < 1 || config.Port > 65535 {
		return nil, fmt.Errorf("MCP host port must be between 1 and 65535")
	}
	if err := validateHost(config.Host, config.Authentication); err != nil {
		return nil, err
	}
	if config.Authentication && !strongToken(config.Token) {
		return nil, fmt.Errorf("PIXIE_MCP_TOKEN must be a strong printable random token")
	}
	if config.PublicOrigin != "" {
		normalized, err := normalizeOrigin(config.PublicOrigin)
		if err != nil || !config.Authentication {
			return nil, fmt.Errorf("PIXIE_MCP_PUBLIC_ORIGIN requires authentication and one absolute http(s) origin without a path")
		}
		config.PublicOrigin = normalized
	}
	// The host is the security boundary for every in-process module. Keep the
	// embedded Browser service on the same origin and credential even when a
	// caller constructs Config directly instead of using ConfigFromEnvironment.
	config.BrowserConfig.Host = config.Host
	config.BrowserConfig.Port = config.Port
	config.BrowserConfig.Authentication = config.Authentication
	config.BrowserConfig.Token = config.Token
	config.BrowserConfig.PublicOrigin = config.PublicOrigin
	if config.Modules == nil {
		config.Modules = []string{"browser"}
	}
	active := make(map[string]bool, len(config.Modules))
	for _, id := range config.Modules {
		if _, ok := moduleFactories[id]; !ok {
			return nil, fmt.Errorf("unknown Pixie MCP module %q", id)
		}
		if active[id] {
			return nil, fmt.Errorf("Pixie MCP module %q is configured more than once", id)
		}
		active[id] = true
	}
	disabled := make(map[string]bool, len(config.DisabledModules))
	for _, id := range config.DisabledModules {
		if _, ok := moduleFactories[id]; !ok {
			return nil, fmt.Errorf("unknown Pixie MCP module %q", id)
		}
		if disabled[id] {
			return nil, fmt.Errorf("Pixie MCP disabled module %q is configured more than once", id)
		}
		disabled[id] = true
		delete(active, id)
	}
	service := &Service{config: config, build: diagnostics.NormalizeBuild(build.Version, build.Revision), logger: logger, started: time.Now(), modules: map[string]module{}, byPath: map[string]module{}}
	for id := range active {
		created, err := moduleFactories[id](config, service.build, logger)
		if err != nil {
			service.shutdownModules()
			return nil, fmt.Errorf("initialize MCP module %q: %w", id, err)
		}
		descriptor := created.Descriptor()
		if !validModuleDescriptor(id, descriptor) {
			service.shutdownModules()
			return nil, fmt.Errorf("invalid MCP module descriptor: %s", id)
		}
		if reservedModulePath(descriptor.Path) {
			service.shutdownModules()
			return nil, fmt.Errorf("MCP module path is reserved: %s", descriptor.Path)
		}
		if _, exists := service.byPath[descriptor.Path]; exists {
			service.shutdownModules()
			return nil, fmt.Errorf("MCP module path is duplicated: %s", descriptor.Path)
		}
		service.modules[id], service.byPath[descriptor.Path] = created, created
	}
	service.revision = service.computeRevision()
	return service, nil
}

func (s *Service) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	path := request.URL.Path
	switch path {
	case LivePath, "/health":
		if request.Method != http.MethodGet {
			methodNotAllowed(response, http.MethodGet)
			return
		}
		writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
		return
	case ReadyPath:
		if request.Method != http.MethodGet {
			methodNotAllowed(response, http.MethodGet)
			return
		}
		if !s.authorized(request) {
			writeJSON(response, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		catalog := s.catalog()
		status := http.StatusOK
		if catalog.Gateway.State != "ready" {
			status = http.StatusServiceUnavailable
		}
		writeJSONStatus(response, status, catalog)
		return
	case CatalogPath:
		if request.Method != http.MethodGet {
			methodNotAllowed(response, http.MethodGet)
			return
		}
		if !s.authorized(request) {
			writeJSON(response, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		writeJSON(response, http.StatusOK, s.catalog())
		return
	case GatewayStatusPath:
		if request.Method != http.MethodGet {
			methodNotAllowed(response, http.MethodGet)
			return
		}
		if !s.authorized(request) {
			writeJSON(response, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"build": s.build, "startedAt": s.started.UTC().Format(time.RFC3339), "catalog": s.catalog()})
		return
	}
	if module := s.moduleForPath(path); module != nil {
		if !s.authorized(request) {
			writeJSON(response, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		module.ServeHTTP(response, request)
		return
	}
	if path == "/mcp" || path == StatusPath || path == "/v1/browser" || path == "/v1/browser/leases" || strings.HasPrefix(path, "/v1/artifacts/") || path == "/v1/app-views" || strings.HasPrefix(path, "/v1/app-views/") {
		if module := s.legacyBrowser(); module != nil {
			module.ServeHTTP(response, request)
			return
		}
	}
	writeJSON(response, http.StatusNotFound, map[string]string{"code": "not_found"})
}

func (s *Service) HTTPServer() *http.Server {
	return &http.Server{
		Addr:              net.JoinHostPort(s.config.Host, strconv.Itoa(s.config.Port)),
		Handler:           s,
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       130 * time.Second,
		WriteTimeout:      130 * time.Second,
		IdleTimeout:       5 * time.Second,
		MaxHeaderBytes:    maxBody,
	}
}

func (s *Service) Shutdown() { s.shutdownModules() }

func (s *Service) Catalog() Catalog { return s.catalog() }

func (s *Service) moduleForPath(requestPath string) module {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if module, ok := s.byPath[requestPath]; ok {
		return module
	}
	bestPath := ""
	var selected module
	for modulePath, candidate := range s.byPath {
		if strings.HasPrefix(requestPath, modulePath+"/") && len(modulePath) > len(bestPath) {
			bestPath, selected = modulePath, candidate
		}
	}
	return selected
}

func (s *Service) legacyBrowser() module {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.modules["browser"]
}

func reservedModulePath(value string) bool {
	for _, reserved := range []string{CatalogPath, GatewayStatusPath, LivePath, ReadyPath, StatusPath, "/health", "/mcp", "/v1", "/v1/browser", "/v1/artifacts", "/v1/app-views"} {
		if value == reserved || strings.HasPrefix(value, reserved+"/") {
			return true
		}
	}
	return false
}

func validModuleDescriptor(id string, descriptor ModuleDescriptor) bool {
	return descriptor.ID == id && validModuleID(descriptor.ID) &&
		validModuleText(descriptor.ExtensionName, 128, false) &&
		validModuleText(descriptor.DisplayName, 256, false) &&
		validModuleText(descriptor.Description, 2048, true) &&
		descriptor.Path == "/"+descriptor.ID && descriptor.Transport == "streamable_http"
}

func validModuleID(value string) bool {
	if value == "" || len(value) > 64 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}

func validModuleText(value string, limit int, empty bool) bool {
	if !empty && value == "" || len(value) > limit {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func (s *Service) shutdownModules() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, module := range s.modules {
		module.Shutdown()
		delete(s.modules, id)
	}
	s.byPath = map[string]module{}
}

func (s *Service) catalog() Catalog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.modules))
	for id := range s.modules {
		ids = append(ids, id)
	}
	// There are currently only compile-time modules; sorting by descriptor ID
	// keeps the wire response deterministic as the registry grows.
	slicesSort(ids)
	modules := make([]ModuleSummary, 0, len(ids))
	ready := true
	for _, id := range ids {
		module := s.modules[id]
		descriptor := module.Descriptor()
		state := "ready"
		detail := ""
		if !module.Ready() {
			state, detail, ready = "unavailable", "Module is not ready.", false
		}
		modules = append(modules, ModuleSummary{ModuleDescriptor: descriptor, State: state, Detail: detail})
	}
	gateway := GatewaySummary{State: "ready"}
	if !ready {
		gateway.State, gateway.Detail = "degraded", "One or more published modules are unavailable."
	}
	return Catalog{SchemaVersion: 1, Revision: s.revision, Gateway: gateway, Modules: modules}
}

func (s *Service) computeRevision() string {
	ids := make([]string, 0, len(s.modules))
	for id, module := range s.modules {
		descriptor := module.Descriptor()
		ids = append(ids, strings.Join([]string{id, descriptor.ID, descriptor.ExtensionName, descriptor.DisplayName, descriptor.Description, descriptor.Path, descriptor.Transport}, "\x00"))
	}
	slicesSort(ids)
	digest := sha256.Sum256([]byte(strings.Join(ids, "\x00")))
	return hex.EncodeToString(digest[:])[:16]
}

func (s *Service) authorized(request *http.Request) bool {
	if s.config.Authentication {
		values := request.Header.Values("Authorization")
		if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || !constantTimeEqual(strings.TrimPrefix(values[0], "Bearer "), s.config.Token) {
			return false
		}
	} else if site := request.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	if request.Header.Get("Origin") != "" && !s.expectedOrigin(request) {
		return false
	}
	return s.expectedHost(request)
}

func (s *Service) expectedOrigin(request *http.Request) bool {
	origin, err := normalizeOrigin(request.Header.Get("Origin"))
	if err != nil {
		return false
	}
	expected := s.config.PublicOrigin
	if expected == "" {
		expected = requestScheme(request) + "://" + request.Host
		expected, err = normalizeOrigin(expected)
		if err != nil {
			return false
		}
	}
	return origin == expected
}

func (s *Service) expectedHost(request *http.Request) bool {
	if s.config.PublicOrigin != "" {
		expected, err := url.Parse(s.config.PublicOrigin)
		if err != nil {
			return false
		}
		name, port, splitErr := net.SplitHostPort(requestHost(request))
		if splitErr != nil {
			name, port = requestHost(request), ""
		}
		if !strings.EqualFold(strings.Trim(name, "[]"), expected.Hostname()) {
			return false
		}
		if expected.Port() != "" {
			return port == expected.Port()
		}
		if port == "" {
			return true
		}
		return port == defaultOriginPort(expected.Scheme)
	}
	name, port, err := net.SplitHostPort(request.Host)
	if err != nil {
		name, port = request.Host, ""
	}
	if port == "" {
		port = strconv.Itoa(s.config.Port)
	}
	if port != strconv.Itoa(s.config.Port) {
		return false
	}
	name = strings.Trim(name, "[]")
	if s.config.Host == "localhost" {
		return strings.EqualFold(name, "localhost") || net.ParseIP(name).IsLoopback()
	}
	bind, candidate := net.ParseIP(s.config.Host), net.ParseIP(name)
	return candidate != nil && (bind.IsUnspecified() || bind.Equal(candidate) || bind.IsLoopback() && candidate.IsLoopback())
}

func defaultOriginPort(scheme string) string {
	if strings.EqualFold(scheme, "https") {
		return "443"
	}
	return "80"
}

func requestHost(request *http.Request) string {
	if request.Host != "" {
		return request.Host
	}
	return request.URL.Host
}

func requestScheme(request *http.Request) string {
	if request.TLS != nil {
		return "https"
	}
	return "http"
}

func constantTimeEqual(left, right string) bool {
	leftDigest := sha256.Sum256([]byte(left))
	rightDigest := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftDigest[:], rightDigest[:]) == 1
}

func writeJSON(response http.ResponseWriter, status int, body any) {
	writeJSONStatus(response, status, body)
}

func writeJSONStatus(response http.ResponseWriter, status int, body any) {
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

func slicesSort(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
