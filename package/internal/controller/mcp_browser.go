package controller

// This file owns the browser-MCP registration surface. Option B keeps the
// browser MCP server outside Pixie: the controller persists one lean setting,
// asks the host to read Pi's effective `mcpServers` layers, upserts or removes
// only Pixie's own instance-wide entry, and reports a bounded endpoint probe.
// It never proxies MCP traffic and fails closed when the assistant is
// unavailable.

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// BrowserMCPStatus composes the persisted setting with the effective Pi
// configuration layer and the bounded endpoint probe.
type BrowserMCPStatus struct {
	Name            string         `json:"name"`
	URL             string         `json:"url"`
	Enabled         bool           `json:"enabled"`
	Registered      bool           `json:"registered"`
	Layer           string         `json:"layer,omitempty"`
	Path            string         `json:"path,omitempty"`
	Disabled        bool           `json:"disabled"`
	Definition      map[string]any `json:"definition,omitempty"`
	Reachable       bool           `json:"reachable"`
	ServerInfo      map[string]any `json:"serverInfo,omitempty"`
	ProtocolVersion string         `json:"protocolVersion,omitempty"`
	Tools           []string       `json:"tools"`
	Error           string         `json:"error,omitempty"`
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
	status := BrowserMCPStatus{Name: config.Name, URL: config.URL, Enabled: config.Enabled, Tools: []string{}}
	readParams := map[string]any{"name": config.Name}
	if projectDir != "" {
		readParams["projectDir"] = projectDir
	}
	response, err := a.objectCall(ctx, "pi.mcp.servers.read", readParams)
	if err != nil {
		return BrowserMCPStatus{}, fmt.Errorf("couldn't read Pi MCP configuration: %w", err)
	}
	if server, definition := browserMCPServer(response, config.Name); server != nil {
		status.Registered = true
		status.Layer = textValue(server["layer"])
		status.Path = textValue(server["path"])
		status.Disabled, _ = server["disabled"].(bool)
		status.Definition = definition
	}
	probeDefinition := map[string]any{"url": config.URL}
	if len(status.Definition) > 0 {
		probeDefinition = status.Definition
	}
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
	if next, err = a.settings.SetBrowserMCP(next); err != nil {
		return BrowserMCPStatus{}, err
	}
	projectDir := strings.TrimSpace(textValue(request["projectDir"]))
	if next.Enabled {
		definition := map[string]any{"url": next.URL}
		if headers := mapValue(request["headers"]); len(headers) > 0 {
			definition["headers"] = headers
		}
		params := map[string]any{"name": next.Name, "definition": definition}
		if err := a.call(ctx, "pi.mcp.servers.upsert", params, nil); err != nil {
			return BrowserMCPStatus{}, fmt.Errorf("couldn't register browser MCP in Pi: %w", err)
		}
	} else {
		if err := a.call(ctx, "pi.mcp.servers.remove", map[string]any{"name": next.Name}, nil); err != nil {
			return BrowserMCPStatus{}, fmt.Errorf("couldn't remove browser MCP from Pi: %w", err)
		}
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
	if err := a.call(ctx, "pi.mcp.servers.remove", map[string]any{"name": current.Name}, nil); err != nil {
		return BrowserMCPStatus{}, fmt.Errorf("couldn't remove browser MCP from Pi: %w", err)
	}
	current.Enabled = false
	if _, err := a.settings.SetBrowserMCP(current); err != nil {
		return BrowserMCPStatus{}, err
	}
	return a.BrowserMCPStatus(ctx, projectDir)
}

func browserMCPServer(response map[string]any, name string) (map[string]any, map[string]any) {
	for _, value := range arrayValue(response["servers"]) {
		server := mapValue(value)
		if textValue(server["name"]) != name {
			continue
		}
		return server, mapValue(server["definition"])
	}
	return nil, nil
}

func applyBrowserMCPProbe(status *BrowserMCPStatus, probe map[string]any) {
	status.Reachable, _ = probe["reachable"].(bool)
	status.ServerInfo = mapValue(probe["serverInfo"])
	if len(status.ServerInfo) == 0 {
		status.ServerInfo = nil
	}
	status.ProtocolVersion = textValue(probe["protocolVersion"])
	status.Tools = []string{}
	for _, value := range arrayValue(probe["tools"]) {
		if name := textValue(value); name != "" {
			status.Tools = append(status.Tools, name)
		}
	}
	status.Error = textValue(probe["error"])
}

func validateBrowserMCPName(name string) error {
	if name == "" || len(name) > 128 || containsNUL(name) {
		return fmt.Errorf("browser MCP name is invalid")
	}
	for _, character := range name {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			continue
		}
		switch character {
		case '-', '_', '.':
			continue
		default:
			return fmt.Errorf("browser MCP name must use letters, numbers, dots, underscores or hyphens")
		}
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
