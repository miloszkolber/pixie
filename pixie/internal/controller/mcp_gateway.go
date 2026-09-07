package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

const (
	mcpGatewayCatalogPath = "/v1/mcp/modules"
	mcpGatewayTimeout     = 3 * time.Second
	mcpGatewayBodyLimit   = 64 * 1024
)

// MCPGateway is the controller-side client for the Pixie MCP
// host. It owns discovery and endpoint construction; the browser never sees
// the host token or raw Pi extension configuration.
type MCPGateway struct {
	baseURL     string
	token       string
	client      *http.Client
	mu          sync.Mutex
	lastModules []mcpGatewayWireModule
}

type mcpGatewayWire struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Revision      string                 `json:"revision"`
	Gateway       mcpGatewayWireState    `json:"gateway"`
	Modules       []mcpGatewayWireModule `json:"modules"`
}

type mcpGatewayWireState struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
}

type mcpGatewayWireModule struct {
	ID            string `json:"id"`
	ExtensionName string `json:"extensionName"`
	DisplayName   string `json:"displayName"`
	Description   string `json:"description"`
	Path          string `json:"path"`
	Transport     string `json:"transport"`
	State         string `json:"state"`
	Detail        string `json:"detail"`
}

func NewMCPGateway(config AuthConfig) *MCPGateway {
	if config.MCPURL == "" {
		return &MCPGateway{}
	}
	transport := http.DefaultTransport
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaultTransport.Clone()
	}
	return &MCPGateway{
		baseURL: strings.TrimSuffix(config.MCPURL, "/"), token: config.MCPToken,
		client: &http.Client{Transport: transport, Timeout: mcpGatewayTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func (g *MCPGateway) Close() {
	if g != nil && g.client != nil {
		g.client.CloseIdleConnections()
	}
}

func (g *MCPGateway) Catalog(ctx context.Context, admin *PiAdmin) (map[string]any, error) {
	wire := g.fetch(ctx)
	configured := []piExtension(nil)
	configurationDetail := ""
	if g != nil && g.baseURL != "" {
		if admin == nil {
			configurationDetail = "Pi administration is unavailable."
		} else {
			value, _, err := admin.extensions(ctx, "pi.config.extensions.list", nil, true)
			if err != nil {
				configurationDetail = "Pi extension state is unavailable."
			} else {
				configured = value
			}
		}
	}
	modules := make([]map[string]any, 0, len(wire.Modules))
	for _, module := range wire.Modules {
		item := map[string]any{
			"id": module.ID, "extensionName": module.ExtensionName, "displayName": module.DisplayName,
			"description": module.Description, "path": module.Path, "transport": module.Transport,
			"state": module.State,
		}
		if module.Detail != "" {
			item["detail"] = module.Detail
		}
		binding, detail := gatewayBinding(module, configured, g.endpoint(module), g.token)
		if configurationDetail != "" {
			binding, detail = "unavailable", configurationDetail
		}
		item["binding"] = binding
		if detail != "" {
			item["bindingDetail"] = detail
		}
		modules = append(modules, item)
	}
	gateway := map[string]any{"state": wire.Gateway.State}
	if wire.Gateway.Detail != "" {
		gateway["detail"] = wire.Gateway.Detail
	}
	if wire.Revision != "" {
		gateway["revision"] = wire.Revision
	}
	return map[string]any{"schemaVersion": 1, "gateway": gateway, "modules": modules}, nil
}

func (g *MCPGateway) SetPiEnabled(ctx context.Context, admin *PiAdmin, moduleID string, enabled bool, revision string) (map[string]any, error) {
	if g == nil || g.baseURL == "" {
		return nil, fmt.Errorf("Pixie MCP host is not configured")
	}
	if admin == nil {
		return nil, fmt.Errorf("Pi administration is not configured")
	}
	wire := g.fetch(ctx)
	var module mcpGatewayWireModule
	for _, candidate := range wire.Modules {
		if candidate.ID == moduleID {
			module = candidate
			break
		}
	}
	if enabled {
		if wire.Gateway.State != "ready" && wire.Gateway.State != "degraded" {
			return nil, fmt.Errorf("Pixie MCP host is unavailable")
		}
		if revision != "" && revision != wire.Revision {
			return nil, fmt.Errorf("Pixie MCP catalog changed; refresh and try again")
		}
		if module.ID == "" {
			return nil, fmt.Errorf("Pixie MCP module is not published: %s", moduleID)
		}
		if module.State != "ready" {
			return nil, fmt.Errorf("Pixie MCP module is unavailable: %s", moduleID)
		}
	} else if module.ID == "" {
		// A host outage should not prevent disabling the first Browser module,
		// which can also have been configured through the standalone route. For
		// dynamic modules, disabling remains available while the catalog still
		// publishes their identity.
		identity, ok := gatewayModuleIdentity(moduleID)
		if !ok {
			return nil, fmt.Errorf("Pixie MCP module is not published: %s", moduleID)
		}
		module = identity
	}
	if err := admin.setGatewayModule(ctx, module, g.endpoint(module), g.token, enabled); err != nil {
		return nil, err
	}
	return g.Catalog(ctx, admin)
}

func (g *MCPGateway) fetch(ctx context.Context) mcpGatewayWire {
	wire := g.fetchLive(ctx)
	if g == nil || g.baseURL == "" {
		return wire
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if wire.Gateway.State == "ready" || wire.Gateway.State == "degraded" {
		g.lastModules = append([]mcpGatewayWireModule(nil), wire.Modules...)
	} else {
		wire.Modules = append([]mcpGatewayWireModule(nil), g.lastModules...)
		if len(wire.Modules) == 0 {
			if browser, ok := gatewayModuleIdentity("browser"); ok {
				wire.Modules = []mcpGatewayWireModule{browser}
			}
		}
		for index := range wire.Modules {
			wire.Modules[index].State = "unavailable"
			wire.Modules[index].Detail = "Publication and readiness are unknown while the host is unavailable. Global Pi configuration can still be disabled."
		}
	}
	return wire
}

func (g *MCPGateway) fetchLive(ctx context.Context) mcpGatewayWire {
	if g == nil || g.baseURL == "" {
		return mcpGatewayWire{SchemaVersion: 1, Gateway: mcpGatewayWireState{State: "not-configured", Detail: "MCP host is not configured."}, Modules: []mcpGatewayWireModule{}}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, g.baseURL+mcpGatewayCatalogPath, nil)
	if err != nil {
		return mcpGatewayWire{SchemaVersion: 1, Gateway: mcpGatewayWireState{State: "incompatible", Detail: "MCP host address is invalid."}, Modules: []mcpGatewayWireModule{}}
	}
	if g.token != "" {
		request.Header.Set("Authorization", "Bearer "+g.token)
	}
	response, err := g.client.Do(request)
	if err != nil {
		return mcpGatewayWire{SchemaVersion: 1, Gateway: mcpGatewayWireState{State: "unreachable", Detail: "MCP host is unavailable."}, Modules: []mcpGatewayWireModule{}}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return mcpGatewayWire{SchemaVersion: 1, Gateway: mcpGatewayWireState{State: "incompatible", Detail: "MCP host did not return a catalog."}, Modules: []mcpGatewayWireModule{}}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, mcpGatewayBodyLimit+1))
	if err != nil || len(body) > mcpGatewayBodyLimit {
		return mcpGatewayWire{SchemaVersion: 1, Gateway: mcpGatewayWireState{State: "incompatible", Detail: "MCP host catalog is invalid."}, Modules: []mcpGatewayWireModule{}}
	}
	var decoded mcpGatewayWire
	if err := json.Unmarshal(body, &decoded); err != nil || decoded.SchemaVersion != 1 || !validCatalogRevision(decoded.Revision) || !validGatewayState(decoded.Gateway.State) {
		return mcpGatewayWire{SchemaVersion: 1, Gateway: mcpGatewayWireState{State: "incompatible", Detail: "MCP host catalog is incompatible."}, Modules: []mcpGatewayWireModule{}}
	}
	seenIDs := make(map[string]bool, len(decoded.Modules))
	seenPaths := make(map[string]bool, len(decoded.Modules))
	for _, module := range decoded.Modules {
		if !validModuleID(module.ID) || seenIDs[module.ID] || module.Path != "/"+module.ID || !validModuleText(module.ExtensionName, 128, false) || !validModuleText(module.DisplayName, 256, false) || !validModuleText(module.Description, 2048, true) || !validModuleText(module.Detail, 2048, true) || module.Transport != "streamable_http" || !validModuleState(module.State) || !validGatewayPath(module.Path) || reservedGatewayPath(module.Path) || seenPaths[module.Path] {
			return mcpGatewayWire{SchemaVersion: 1, Gateway: mcpGatewayWireState{State: "incompatible", Detail: "MCP host catalog contains an invalid module."}, Modules: []mcpGatewayWireModule{}}
		}
		seenIDs[module.ID], seenPaths[module.Path] = true, true
	}
	return decoded
}

func (g *MCPGateway) endpoint(module mcpGatewayWireModule) string {
	if g == nil || g.baseURL == "" || !validGatewayPath(module.Path) {
		return ""
	}
	return g.baseURL + module.Path
}

func gatewayBinding(module mcpGatewayWireModule, configured []piExtension, endpoint, token string) (string, string) {
	for _, extension := range configured {
		if extension.summary["name"] != module.ExtensionName {
			continue
		}
		configuredEndpoint := mcpExtensionURL(extension.raw)
		if !sameGatewayEndpoint(configuredEndpoint, endpoint) {
			return "conflict", "A different MCP endpoint is already configured with this name."
		}
		if !mcpExtensionCredentialMatches(extension.raw, token) {
			return "conflict", "This MCP extension uses a different credential; remove it and enable the module here."
		}
		enabled, _ := extension.summary["enabled"].(bool)
		if enabled {
			return "enabled", ""
		}
		return "disabled", ""
	}
	return "not-configured", ""
}

func gatewayModuleIdentity(moduleID string) (mcpGatewayWireModule, bool) {
	if moduleID != "browser" {
		return mcpGatewayWireModule{}, false
	}
	return mcpGatewayWireModule{ID: "browser", ExtensionName: "pixie-browser", DisplayName: "Pixie Browser", Description: "Bounded browser automation and browser guidance.", Path: "/browser", Transport: "streamable_http", State: "ready"}, true
}

func validGatewayState(value string) bool {
	return value == "ready" || value == "degraded" || value == "not-configured" || value == "unreachable" || value == "incompatible"
}

func validModuleState(value string) bool { return value == "ready" || value == "unavailable" }

func validCatalogRevision(value string) bool { return validModuleText(value, 128, false) }

func validModuleText(value string, limit int, empty bool) bool {
	if (!empty && value == "") || len(value) > limit {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
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

func validGatewayPath(value string) bool {
	if value == "" || len(value) > 128 || !strings.HasPrefix(value, "/") || strings.Contains(value, "//") || strings.ContainsAny(value, "?#%\\") || path.Clean(value) != value {
		return false
	}
	if !validModuleText(value, 128, false) {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && !parsed.IsAbs() && parsed.Host == "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func reservedGatewayPath(value string) bool {
	for _, reserved := range []string{mcpGatewayCatalogPath, "/v1/mcp/status", "/livez", "/readyz", "/status", "/health", "/mcp", "/v1", "/v1/browser", "/v1/artifacts", "/v1/app-views"} {
		if value == reserved || strings.HasPrefix(value, reserved+"/") {
			return true
		}
	}
	return false
}

func mcpExtensionURL(raw map[string]any) string {
	server := mapValue(raw["server"])
	if value := textValue(server["url"]); value != "" {
		return value
	}
	return textValue(server["uri"])
}

func sameGatewayEndpoint(configured, canonical string) bool {
	return configured != "" && configured == canonical
}

func mcpExtensionCredentialMatches(raw map[string]any, token string) bool {
	server := mapValue(raw["server"])
	authorizationSeen := false
	authorizationMatched := false
	for _, header := range arrayValue(server["headers"]) {
		entry := mapValue(header)
		if !strings.EqualFold(textValue(entry["name"]), "Authorization") {
			continue
		}
		authorizationSeen = true
		value := textValue(entry["value"])
		if token != "" && (value == "Bearer ${PIXIE_MCP_TOKEN}" || value == "Bearer "+token) {
			authorizationMatched = true
			continue
		}
		return false
	}
	if token == "" {
		return !authorizationSeen
	}
	return authorizationSeen && authorizationMatched
}

func (a *PiAdmin) setGatewayModule(ctx context.Context, module mcpGatewayWireModule, endpoint, token string, enabled bool) error {
	if a == nil || a.client == nil {
		return fmt.Errorf("Pi administration is not configured")
	}
	if !a.extensionMu.TryLock() {
		return fmt.Errorf("wait for the Pi extension update to finish")
	}
	defer a.extensionMu.Unlock()
	configured, _, err := a.extensions(ctx, "pi.config.extensions.list", nil, true)
	if err != nil {
		return err
	}
	existing := findExtension(configured, "name", module.ExtensionName)
	if existing != nil {
		if !sameGatewayEndpoint(mcpExtensionURL(existing.raw), endpoint) {
			return fmt.Errorf("MCP extension name is already configured for another endpoint: %s", module.ExtensionName)
		}
		if !mcpExtensionCredentialMatches(existing.raw, token) {
			return fmt.Errorf("MCP extension uses a different credential; remove it before enabling the module: %s", module.ExtensionName)
		}
		key, ok := existing.summary["configKey"].(string)
		if !ok || key == "" {
			return fmt.Errorf("configured MCP extension is missing its config key: %s", module.ExtensionName)
		}
		return a.call(ctx, "pi.config.extensions.set-enabled", map[string]any{"configKey": key, "enabled": enabled}, nil)
	}
	if !enabled {
		return nil
	}
	_, profile, err := a.client.Profile(ctx)
	if err != nil {
		return err
	}
	if !profile.Operations.HTTPMCP {
		return unsupportedAgentCapability("HTTP MCP extensions")
	}
	if !profile.Operations.Administration {
		return unsupportedAgentCapability("Pi administration")
	}
	headers := []any{}
	envKeys := []string{}
	if token != "" {
		headers = append(headers, map[string]any{"name": "Authorization", "value": "Bearer ${PIXIE_MCP_TOKEN}"})
		envKeys = append(envKeys, "PIXIE_MCP_TOKEN")
	}
	raw := map[string]any{
		"type":        "mcp",
		"server":      map[string]any{"type": "http", "name": module.ExtensionName, "url": endpoint, "headers": headers},
		"description": module.Description,
		"timeout":     130,
	}
	if len(envKeys) > 0 {
		raw["envKeys"] = envKeys
	}
	return a.call(ctx, "pi.config.extensions.add", map[string]any{"extension": raw, "enabled": true}, nil)
}
