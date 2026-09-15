package controller

// This file owns the browser-MCP registration surface. Option B keeps the
// browser MCP server outside Pixie: the controller persists one lean setting,
// asks the host to read Pi's effective `mcpServers` layers, upserts or removes
// only Pixie's own instance-wide entry, and reports a bounded endpoint probe.
// It never proxies MCP traffic and fails closed when the assistant is
// unavailable.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const (
	browserMCPManagedByKey      = "_pixieManagedBy"
	browserMCPOwnershipTokenKey = "_pixieOwnershipToken"
	browserMCPManagedByValue    = "browser-mcp/v2"
	browserMCPHostConflict      = "MCP ownership conflict"
	browserMCPProbeToolLimit    = 128
)

// BrowserMCPStatus is the browser-safe projection of Pixie's persisted identity
// and a bounded endpoint probe. It intentionally excludes the raw endpoint,
// native definition and adapter diagnostics.
type BrowserMCPStatus struct {
	Name            string                `json:"name"`
	Enabled         bool                  `json:"enabled"`
	Registered      bool                  `json:"registered"`
	Layer           string                `json:"layer,omitempty"`
	Path            string                `json:"path,omitempty"`
	Disabled        bool                  `json:"disabled"`
	Reachable       bool                  `json:"reachable"`
	ServerInfo      *BrowserMCPServerInfo `json:"serverInfo,omitempty"`
	ProtocolVersion string                `json:"protocolVersion,omitempty"`
	Tools           []string              `json:"tools"`
	Error           string                `json:"error,omitempty"`
}

// BrowserMCPServerInfo is retained for wire compatibility. Browser MCP probe
// responses are endpoint-controlled, so BrowserMCPStatus never populates it.
type BrowserMCPServerInfo struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// BrowserMCPStatus reads Pi's effective configuration for the persisted server
// name, probes the endpoint and reports the composed state. A missing client
// or a failed host call is an error, never a success-shaped status.
func (a *PiAdmin) BrowserMCPStatus(ctx context.Context, projectDir string) (BrowserMCPStatus, error) {
	if a == nil || a.client == nil {
		return BrowserMCPStatus{}, fmt.Errorf("Pi administration is not configured")
	}
	if a.settings == nil {
		return BrowserMCPStatus{}, fmt.Errorf("settings are not configured")
	}
	config, err := a.settings.BrowserMCP()
	if err != nil {
		return BrowserMCPStatus{}, err
	}
	if err := validateBrowserMCPName(config.Name); err != nil {
		return BrowserMCPStatus{}, fmt.Errorf("saved browser MCP name is invalid")
	}
	if err := validateBrowserMCPURL(config.URL); err != nil {
		return BrowserMCPStatus{}, fmt.Errorf("saved browser MCP url is invalid")
	}
	status := BrowserMCPStatus{Name: config.Name, Enabled: config.Enabled, Tools: []string{}}
	readParams := map[string]any{"name": config.Name}
	if projectDir != "" {
		readParams["projectDir"] = projectDir
	}
	response, err := a.objectCall(ctx, "pi.mcp.servers.read", readParams)
	if err != nil {
		return BrowserMCPStatus{}, fmt.Errorf("couldn't read Pi MCP configuration: %w", err)
	}
	// The persisted name is the only server identity shown to the browser: it
	// is independently supplied and validated by Pixie. Native definitions and
	// endpoint probe metadata are untrusted for display and never select this
	// status probe, which also avoids sending another MCP entry's headers.
	probeDefinition := map[string]any{"url": config.URL}
	status.Registered = config.Enabled && browserMCPResponseOwned(response, config.OwnershipToken)
	probe, err := a.objectCall(ctx, "pi.mcp.servers.probe", map[string]any{"definition": probeDefinition})
	if err != nil {
		return BrowserMCPStatus{}, fmt.Errorf("couldn't probe Pi MCP endpoint: %w", err)
	}
	applyBrowserMCPProbe(&status, probe)
	return status, nil
}

// ConfigureBrowserMCP persists the setting and registers or removes Pixie's
// instance-wide Pi entry. It returns the same composed status the settings
// surface reports.
func (a *PiAdmin) ConfigureBrowserMCP(ctx context.Context, request map[string]any) (BrowserMCPStatus, error) {
	if a == nil || a.client == nil {
		return BrowserMCPStatus{}, fmt.Errorf("Pi administration is not configured")
	}
	if a.settings == nil {
		return BrowserMCPStatus{}, fmt.Errorf("settings are not configured")
	}
	current, err := a.settings.BrowserMCP()
	if err != nil {
		return BrowserMCPStatus{}, err
	}
	if err := validateBrowserMCPName(current.Name); err != nil {
		return BrowserMCPStatus{}, fmt.Errorf("saved browser MCP name is invalid")
	}
	next := current
	if value := strings.TrimSpace(textValue(request["name"])); value != "" {
		next.Name = value
	}
	if value, present := request["url"]; present {
		text := strings.TrimSpace(textValue(value))
		if text == "" {
			return BrowserMCPStatus{}, fmt.Errorf("browser MCP url is required")
		}
		next.URL = text
	}
	if value, ok := request["enabled"].(bool); ok {
		next.Enabled = value
	}
	if err := validateBrowserMCPName(next.Name); err != nil {
		return BrowserMCPStatus{}, err
	}
	if err := validateBrowserMCPURL(next.URL); err != nil {
		return BrowserMCPStatus{}, err
	}
	if next.Name != current.Name && (current.Enabled || next.Enabled) {
		return BrowserMCPStatus{}, fmt.Errorf("to change the Browser MCP name, remove the enabled entry first, then save the new name while disabled before registering it")
	}
	projectDir := strings.TrimSpace(textValue(request["projectDir"]))
	if next.Enabled {
		if next.OwnershipToken == "" {
			next.OwnershipToken, err = newBrowserMCPOwnershipToken()
			if err != nil {
				return BrowserMCPStatus{}, err
			}
		}
		definition := map[string]any{"url": next.URL}
		if headers := mapValue(request["headers"]); len(headers) > 0 {
			definition["headers"] = headers
		}
		definition[browserMCPManagedByKey] = browserMCPManagedByValue
		definition[browserMCPOwnershipTokenKey] = next.OwnershipToken
		params := browserMCPConditionalParams(next.Name, next.OwnershipToken)
		params["definition"] = definition
		if projectDir != "" {
			params["projectDir"] = projectDir
		}
		if err := a.call(ctx, "pi.mcp.servers.upsert", params, nil); err != nil {
			return BrowserMCPStatus{}, browserMCPMutationError(next.Name, "register", err)
		}
	} else {
		// Keep the currently persisted name until Pi confirms removal. In
		// particular, a disabled rename removes only the old Pixie-owned entry;
		// it never guesses at another native server to clean up.
		exists, owned, err := a.browserMCPOwnership(ctx, projectDir, current.Name, current.OwnershipToken)
		if err != nil {
			return BrowserMCPStatus{}, err
		}
		if exists && !owned {
			// A disabled rename does not need to touch the old native entry. This
			// lets an operator choose a new Pixie name without deleting an
			// unrelated entry that happens to use Pixie's previous default.
			if next.Name == current.Name {
				return BrowserMCPStatus{}, browserMCPOwnershipConflict(current.Name)
			}
		} else if exists {
			params := browserMCPConditionalParams(current.Name, current.OwnershipToken)
			if projectDir != "" {
				params["projectDir"] = projectDir
			}
			if err := a.call(ctx, "pi.mcp.servers.remove", params, nil); err != nil {
				return BrowserMCPStatus{}, browserMCPMutationError(current.Name, "remove", err)
			}
		}
	}
	if next, err = a.settings.SetBrowserMCP(next); err != nil {
		return BrowserMCPStatus{}, fmt.Errorf("Pi may have applied the browser MCP change, but Pixie could not save it; retry the same change before making another: %w", err)
	}
	return a.BrowserMCPStatus(ctx, projectDir)
}

// RemoveBrowserMCP unregisters Pixie's instance-wide Pi entry and disables the
// persisted setting.
func (a *PiAdmin) RemoveBrowserMCP(ctx context.Context, projectDir string) (BrowserMCPStatus, error) {
	if a == nil || a.client == nil {
		return BrowserMCPStatus{}, fmt.Errorf("Pi administration is not configured")
	}
	if a.settings == nil {
		return BrowserMCPStatus{}, fmt.Errorf("settings are not configured")
	}
	current, err := a.settings.BrowserMCP()
	if err != nil {
		return BrowserMCPStatus{}, err
	}
	if err := validateBrowserMCPName(current.Name); err != nil {
		return BrowserMCPStatus{}, fmt.Errorf("saved browser MCP name is invalid")
	}
	if current.OwnershipToken == "" {
		return BrowserMCPStatus{}, browserMCPOwnershipConflict(current.Name)
	}
	params := browserMCPConditionalParams(current.Name, current.OwnershipToken)
	if projectDir != "" {
		params["projectDir"] = projectDir
	}
	if err := a.call(ctx, "pi.mcp.servers.remove", params, nil); err != nil {
		return BrowserMCPStatus{}, browserMCPMutationError(current.Name, "remove", err)
	}
	current.Enabled = false
	if _, err := a.settings.SetBrowserMCP(current); err != nil {
		return BrowserMCPStatus{}, fmt.Errorf("Pi may have removed the browser MCP entry, but Pixie could not save it; retry the same removal before making another change: %w", err)
	}
	return a.BrowserMCPStatus(ctx, projectDir)
}

// browserMCPCandidate is intentionally the complete named-read surface from
// the host: source identity plus ownership markers, never native definitions.
func browserMCPCandidate(value any) map[string]any {
	return mapValue(value)
}

func browserMCPResponseOwned(response map[string]any, token string) bool {
	candidates := arrayValue(response["servers"])
	return len(candidates) == 1 && browserMCPEntryOwned(browserMCPCandidate(candidates[0]), token)
}

func (a *PiAdmin) browserMCPOwnership(ctx context.Context, projectDir, name, token string) (bool, bool, error) {
	params := map[string]any{"name": name}
	if projectDir != "" {
		params["projectDir"] = projectDir
	}
	response, err := a.objectCall(ctx, "pi.mcp.servers.read", params)
	if err != nil {
		return false, false, fmt.Errorf("couldn't read Pi MCP configuration: %w", err)
	}
	candidates := arrayValue(response["servers"])
	return len(candidates) > 0, browserMCPResponseOwned(response, token), nil
}

func browserMCPOwnershipConflict(name string) error {
	return fmt.Errorf("Browser MCP ownership for %q needs recovery; refresh its status and resolve the conflicting Pi entry before trying again", name)
}

func browserMCPEntryOwned(candidate map[string]any, token string) bool {
	markers := mapValue(candidate["markers"])
	return token != "" &&
		textValue(candidate["layer"]) == "agent-dir" &&
		candidate["disabled"] != true &&
		textValue(markers[browserMCPManagedByKey]) == browserMCPManagedByValue &&
		textValue(markers[browserMCPOwnershipTokenKey]) == token
}

func browserMCPConditionalParams(name, token string) map[string]any {
	return map[string]any{
		"name":                          name,
		"expectedAgentOwnershipToken":   token,
		"requireNoOtherLayerCollisions": true,
	}
}

func browserMCPMutationError(name, action string, err error) error {
	for current := err; current != nil; current = errors.Unwrap(current) {
		if current.Error() == browserMCPHostConflict {
			return browserMCPOwnershipConflict(name)
		}
	}
	return fmt.Errorf("couldn't %s browser MCP in Pi: %w", action, err)
}

func applyBrowserMCPProbe(status *BrowserMCPStatus, probe map[string]any) {
	status.Reachable, _ = probe["reachable"].(bool)
	status.Tools = []string{}
	// Tool names, protocol versions, and serverInfo come from the endpoint and
	// can reflect URLs or authorization values. Keep the existing wire field as
	// a bounded list of Pixie-generated labels so clients retain its count
	// without receiving endpoint-controlled strings.
	for range arrayValue(probe["tools"]) {
		if len(status.Tools) == browserMCPProbeToolLimit {
			break
		}
		status.Tools = append(status.Tools, fmt.Sprintf("Tool %d", len(status.Tools)+1))
	}
	// Endpoint errors are adapter-controlled text and can echo credentials from
	// an Authorization header or URL. The browser gets a fixed recovery message,
	// never that raw diagnostic.
	if !status.Reachable || textValue(probe["error"]) != "" {
		status.Error = "The endpoint probe failed. Check the endpoint and its deployment configuration."
	}
}

func validateBrowserMCPName(name string) error {
	if name == "" || len(name) > 128 || containsNUL(name) {
		return fmt.Errorf("browser MCP name is invalid")
	}
	for _, character := range []byte(name) {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return fmt.Errorf("browser MCP name must use ASCII letters, numbers, dots, underscores or hyphens")
	}
	return nil
}

func validateBrowserMCPURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("browser MCP url must be an http(s) URL without credentials")
	}
	return nil
}
