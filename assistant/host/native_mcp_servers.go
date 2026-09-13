package host

// This file owns Pi's native MCP server configuration layers and the bounded
// MCP HTTP probe. It is not Pi RPC: the contract is the same `mcpServers`
// document the selected pi-mcp-adapter reads itself, merged across the
// documented precedence layers. Pixie writes only its own instance-wide layer
// (<agentDir>/mcp.json); every other layer, key and sibling server is preserved
// and never modified.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	mcpConfigMaxBytes       = 4 * 1024 * 1024
	mcpServerNameMaxBytes   = 128
	mcpProbeTimeout         = 10 * time.Second
	mcpProbeMaxBody         = 1 << 20
	mcpProbeProtocolVersion = "2025-06-18"
	mcpConfigLockName       = "mcp-servers.lock"
)

// mcpServerState is the effective projection of one named server across every
// configuration layer. Definition is the shallow per-field merge (higher layer
// wins per field) and Layer is the highest-precedence layer that defined the
// server. Disabled reflects the highest layer that carries an explicit
// `disabled` field.
type mcpServerState struct {
	Name       string         `json:"name"`
	Layer      string         `json:"layer"`
	Path       string         `json:"path"`
	Disabled   bool           `json:"disabled"`
	Definition map[string]any `json:"definition"`
}

type mcpConfigLayer struct {
	name string
	path string
}

// mcpConfigLayers returns the layers in ascending precedence order: each later
// layer overrides earlier ones per field. The project layers are only included
// for an absolute project directory.
func mcpConfigLayers(agentDir, projectDir string) []mcpConfigLayer {
	layers := make([]mcpConfigLayer, 0, 6)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		layers = append(layers,
			mcpConfigLayer{name: "user-config", path: filepath.Join(home, ".config", "mcp", "mcp.json")},
			mcpConfigLayer{name: "user-agents", path: filepath.Join(home, ".agents", "mcp.json")},
			mcpConfigLayer{name: "user-agents-dir", path: filepath.Join(home, ".agents", "mcp", "mcp.json")},
		)
	}
	if agentDir != "" {
		layers = append(layers, mcpConfigLayer{name: "agent-dir", path: filepath.Join(agentDir, "mcp.json")})
	}
	if projectDir != "" && filepath.IsAbs(projectDir) {
		layers = append(layers,
			mcpConfigLayer{name: "project", path: filepath.Join(projectDir, ".mcp.json")},
			mcpConfigLayer{name: "project-pi", path: filepath.Join(projectDir, ".pi", "mcp.json")},
		)
	}
	return layers
}

// readMCPServers merges every configuration layer by server name. Missing,
// unreadable or malformed layer files never fail the read; each problem is
// reported as a warning while the remaining layers still apply.
func readMCPServers(agentDir, projectDir string) ([]mcpServerState, []string, error) {
	if agentDir != "" && !filepath.IsAbs(agentDir) {
		return nil, nil, errors.New("Pi agent directory must be absolute")
	}
	merged := map[string]*mcpServerState{}
	warnings := make([]string, 0)
	for _, layer := range mcpConfigLayers(agentDir, projectDir) {
		servers, err := readMCPLayer(layer.path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("Cannot load MCP configuration %s: %v", layer.path, err))
			continue
		}
		for name, definition := range servers {
			state, known := merged[name]
			if !known {
				state = &mcpServerState{Name: name, Definition: map[string]any{}}
				merged[name] = state
			}
			for key, value := range definition {
				state.Definition[key] = value
			}
			if rawDisabled, present := definition["disabled"]; present {
				state.Disabled, _ = rawDisabled.(bool)
			}
			state.Layer = layer.name
			state.Path = layer.path
		}
	}
	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]mcpServerState, 0, len(names))
	for _, name := range names {
		result = append(result, *merged[name])
	}
	return result, warnings, nil
}

// readMCPLayer reads one `mcpServers` object. A missing file yields no servers
// and no error.
func readMCPLayer(path string) (map[string]map[string]any, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("MCP configuration must be a regular non-symlink file")
	}
	if info.Size() > mcpConfigMaxBytes {
		return nil, errors.New("MCP configuration exceeds the size bound")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	document := map[string]any{}
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, errors.New("MCP configuration is not valid JSON")
	}
	servers, _ := document["mcpServers"].(map[string]any)
	result := make(map[string]map[string]any, len(servers))
	for name, value := range servers {
		definition, ok := value.(map[string]any)
		if !ok {
			continue
		}
		result[name] = definition
	}
	return result, nil
}

// validMCPServerName accepts the safe identifier charset used by Pi server
// names and rejects path separators, whitespace and NUL.
func validMCPServerName(name string) bool {
	if name == "" || len(name) > mcpServerNameMaxBytes {
		return false
	}
	for _, character := range name {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			continue
		}
		switch character {
		case '-', '_', '.':
			continue
		default:
			return false
		}
	}
	return true
}

// upsertMCPServer writes exactly one named entry into the Pixie-owned
// instance-wide layer (<agentDir>/mcp.json). Every other top-level key and
// sibling server is preserved. The mutation is serialized with the same
// installation lock discipline used by the native registry/source writers and
// published atomically (temp + fsync + rename).
func upsertMCPServer(agentDir, projectDir, name string, definition map[string]any) error {
	if agentDir == "" || !filepath.IsAbs(agentDir) {
		return errors.New("Pi agent directory must be absolute")
	}
	if !validMCPServerName(name) {
		return errors.New("invalid MCP server name")
	}
	if definition == nil {
		return errors.New("MCP server definition is required")
	}
	path := filepath.Join(agentDir, "mcp.json")
	return withMCPServerLock(agentDir, func() error {
		document, err := readMCPDocument(path)
		if err != nil {
			return err
		}
		servers, _ := document["mcpServers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
		}
		servers[name] = definition
		document["mcpServers"] = servers
		encoded, err := json.Marshal(document)
		if err != nil {
			return err
		}
		return atomicWriteAgentFile(path, encoded)
	})
}

// removeMCPServer removes exactly one named entry from the Pixie-owned layer
// and never touches another layer.
func removeMCPServer(agentDir, projectDir, name string) error {
	if agentDir == "" || !filepath.IsAbs(agentDir) {
		return errors.New("Pi agent directory must be absolute")
	}
	if !validMCPServerName(name) {
		return errors.New("invalid MCP server name")
	}
	path := filepath.Join(agentDir, "mcp.json")
	return withMCPServerLock(agentDir, func() error {
		document, err := readMCPDocument(path)
		if err != nil {
			return err
		}
		servers, _ := document["mcpServers"].(map[string]any)
		if _, present := servers[name]; !present {
			return nil
		}
		delete(servers, name)
		document["mcpServers"] = servers
		encoded, err := json.Marshal(document)
		if err != nil {
			return err
		}
		return atomicWriteAgentFile(path, encoded)
	})
}

// readMCPDocument loads the full document so an upsert or remove preserves
// unrelated top-level keys. A missing file is an empty document.
func readMCPDocument(path string) (map[string]any, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("MCP configuration must be a regular non-symlink file")
	}
	if info.Size() > mcpConfigMaxBytes {
		return nil, errors.New("MCP configuration exceeds the size bound")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	document := map[string]any{}
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, errors.New("MCP configuration is not valid JSON")
	}
	return document, nil
}

// withMCPServerLock serializes writers through a dedicated installation lock
// file. A second flock on the registry lock is impossible because the running
// supervisor already holds it for its lifetime, so this lock is separate.
func withMCPServerLock(agentDir string, change func() error) error {
	directory := filepath.Join(agentDir, "pixie")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(directory, mcpConfigLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := lockNativeFile(file); err != nil {
		return fmt.Errorf("lock MCP server configuration: %w", err)
	}
	defer unlockNativeFile(file)
	return change()
}

// mcpProbeResult is the bounded projection returned to the controller. A
// transport or protocol failure is a reachable:false result rather than an
// operation error, so status reporting stays available.
type mcpProbeResult struct {
	Reachable       bool           `json:"reachable"`
	ServerInfo      map[string]any `json:"serverInfo,omitempty"`
	ProtocolVersion string         `json:"protocolVersion,omitempty"`
	Tools           []string       `json:"tools"`
	Error           string         `json:"error,omitempty"`
}

// probeMCPServer performs the MCP Streamable HTTP handshake: initialize,
// notifications/initialized, then tools/list. Only a missing or unsafe url is
// an operation error; connection and protocol failures are reported in the
// bounded result.
func probeMCPServer(ctx context.Context, definition map[string]any) (mcpProbeResult, error) {
	result := mcpProbeResult{Tools: []string{}}
	urlText, _ := definition["url"].(string)
	if urlText == "" {
		urlText, _ = definition["uri"].(string)
	}
	if urlText == "" {
		return result, errors.New("MCP server definition is missing a url")
	}
	parsed, err := url.Parse(urlText)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return result, errors.New("MCP server url must be an http(s) URL without credentials")
	}
	headers := mcpHeaderMap(definition["headers"])
	probeCtx, cancel := context.WithTimeout(ctx, mcpProbeTimeout)
	defer cancel()
	client := &http.Client{
		Timeout:       mcpProbeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	initPayload, err := mcpPost(probeCtx, client, parsed.String(), headers, "", mcpInitializeRequest(), 1)
	if err != nil {
		return mcpProbeFailed(result, err), nil
	}
	sessionID := initPayload.sessionID
	initResult, err := mcpDecodeResult(initPayload.body)
	if err != nil {
		return mcpProbeFailed(result, err), nil
	}
	if len(initResult) > 0 {
		var info struct {
			ProtocolVersion string         `json:"protocolVersion"`
			ServerInfo      map[string]any `json:"serverInfo"`
		}
		if json.Unmarshal(initResult, &info) == nil {
			result.ProtocolVersion = info.ProtocolVersion
			result.ServerInfo = info.ServerInfo
		}
	}
	if err := mcpPostNotification(probeCtx, client, parsed.String(), headers, sessionID, "notifications/initialized"); err != nil {
		return mcpProbeFailed(result, err), nil
	}
	toolsPayload, err := mcpPost(probeCtx, client, parsed.String(), headers, sessionID, mcpToolsListRequest(), 2)
	if err != nil {
		return mcpProbeFailed(result, err), nil
	}
	toolsResult, err := mcpDecodeResult(toolsPayload.body)
	if err != nil {
		return mcpProbeFailed(result, err), nil
	}
	if len(toolsResult) > 0 {
		var tools struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if json.Unmarshal(toolsResult, &tools) == nil {
			for _, tool := range tools.Tools {
				if tool.Name != "" {
					result.Tools = append(result.Tools, tool.Name)
				}
			}
		}
	}
	result.Reachable = true
	return result, nil
}

func mcpProbeFailed(result mcpProbeResult, err error) mcpProbeResult {
	result.Reachable = false
	if err != nil {
		result.Error = err.Error()
	}
	return result
}

func mcpInitializeRequest() map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcpProbeProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "pixie", "version": "1"},
		},
	}
}

func mcpToolsListRequest() map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}}
}

type mcpPostResult struct {
	body      json.RawMessage
	sessionID string
}

func mcpPostNotification(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, sessionID, method string) error {
	_, err := mcpPost(ctx, client, endpoint, headers, sessionID, map[string]any{"jsonrpc": "2.0", "method": method}, 0)
	return err
}

// mcpPost sends one JSON-RPC message and returns the bounded JSON-RPC response
// (nil for an empty acknowledgement). wantID selects the matching message from
// an event stream; zero means no response is expected.
func mcpPost(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, sessionID string, body map[string]any, wantID int) (mcpPostResult, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return mcpPostResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return mcpPostResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		request.Header.Set("Mcp-Session-Id", sessionID)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return mcpPostResult{}, err
	}
	defer response.Body.Close()
	result := mcpPostResult{sessionID: response.Header.Get("Mcp-Session-Id")}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, mcpProbeMaxBody))
		return result, fmt.Errorf("MCP endpoint returned HTTP %d", response.StatusCode)
	}
	raw, err := mcpReadBody(response)
	if err != nil {
		return result, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return result, nil
	}
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		payload, err := mcpParseSSE(raw, wantID)
		if err != nil {
			return result, err
		}
		result.body = payload
		return result, nil
	}
	result.body = json.RawMessage(raw)
	return result, nil
}

// mcpReadBody bounds a JSON response and reads a single complete event from an
// event stream without waiting for the stream to close.
func mcpReadBody(response *http.Response) ([]byte, error) {
	if !strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		raw, err := io.ReadAll(io.LimitReader(response.Body, mcpProbeMaxBody+1))
		if err != nil {
			return nil, err
		}
		if len(raw) > mcpProbeMaxBody {
			return nil, errors.New("MCP response exceeds the size bound")
		}
		return raw, nil
	}
	reader := bufio.NewReaderSize(io.LimitReader(response.Body, mcpProbeMaxBody+1), 64*1024)
	buffer := bytes.Buffer{}
	sawData := false
	for {
		line, err := reader.ReadBytes('\n')
		if buffer.Len()+len(line) > mcpProbeMaxBody {
			return nil, errors.New("MCP response exceeds the size bound")
		}
		buffer.Write(line)
		trimmed := bytes.TrimRight(line, "\r\n")
		if len(bytes.TrimSpace(trimmed)) == 0 {
			if sawData {
				return buffer.Bytes(), nil
			}
			continue
		}
		if bytes.HasPrefix(trimmed, []byte("data:")) {
			sawData = true
		}
		if err != nil {
			if sawData {
				return buffer.Bytes(), nil
			}
			return nil, err
		}
	}
}

// mcpParseSSE returns the JSON-RPC payload from the first matching event. A
// non-zero wantID filters out unrelated notifications.
func mcpParseSSE(raw []byte, wantID int) (json.RawMessage, error) {
	normalized := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, event := range strings.Split(normalized, "\n\n") {
		data := make([]string, 0)
		for _, line := range strings.Split(event, "\n") {
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			value := strings.TrimPrefix(line, "data:")
			value = strings.TrimPrefix(value, " ")
			data = append(data, value)
		}
		if len(data) == 0 {
			continue
		}
		payload := []byte(strings.Join(data, "\n"))
		var envelope struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(payload, &envelope) != nil {
			continue
		}
		if wantID != 0 && len(envelope.ID) > 0 && strings.Trim(string(envelope.ID), `"`) != strconv.Itoa(wantID) {
			continue
		}
		return payload, nil
	}
	return nil, errors.New("MCP event stream did not contain a JSON-RPC response")
}

func mcpDecodeResult(payload json.RawMessage) (json.RawMessage, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, errors.New("MCP response is not valid JSON-RPC")
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", envelope.Error.Code, envelope.Error.Message)
	}
	return envelope.Result, nil
}

func mcpHeaderMap(value any) map[string]string {
	result := map[string]string{}
	switch typed := value.(type) {
	case map[string]any:
		for key, raw := range typed {
			if text, ok := raw.(string); ok {
				result[key] = text
			}
		}
	case map[string]string:
		for key, text := range typed {
			result[key] = text
		}
	}
	return result
}

// mcpProjectDir selects the optional project scope for read/upsert/remove.
func mcpProjectDir(params map[string]any) string {
	projectDir, _ := params["projectDir"].(string)
	return projectDir
}

// readMCPServersOperation implements pi.mcp.servers.read. An optional name
// filters the effective list; warnings describe skipped layers.
func (s *nativeSupervisor) readMCPServersOperation(params map[string]any) (json.RawMessage, error) {
	servers, warnings, err := readMCPServers(s.config.AgentDir, mcpProjectDir(params))
	if err != nil {
		return nil, err
	}
	if name, _ := params["name"].(string); name != "" {
		filtered := make([]mcpServerState, 0, 1)
		for _, server := range servers {
			if server.Name == name {
				filtered = append(filtered, server)
			}
		}
		servers = filtered
	}
	return json.Marshal(map[string]any{"servers": servers, "warnings": warnings})
}

// upsertMCPServerOperation implements pi.mcp.servers.upsert and returns the
// effective state after the write.
func (s *nativeSupervisor) upsertMCPServerOperation(params map[string]any) (json.RawMessage, error) {
	name, _ := params["name"].(string)
	definition, ok := params["definition"].(map[string]any)
	if !ok || len(definition) == 0 {
		return nil, errors.New("MCP server definition is required")
	}
	if err := upsertMCPServer(s.config.AgentDir, mcpProjectDir(params), name, definition); err != nil {
		return nil, err
	}
	return s.readMCPServersOperation(map[string]any{"name": name, "projectDir": mcpProjectDir(params)})
}

// removeMCPServerOperation implements pi.mcp.servers.remove.
func (s *nativeSupervisor) removeMCPServerOperation(params map[string]any) (json.RawMessage, error) {
	name, _ := params["name"].(string)
	if err := removeMCPServer(s.config.AgentDir, mcpProjectDir(params), name); err != nil {
		return nil, err
	}
	return json.RawMessage(`{"ok":true}`), nil
}

// probeMCPServerOperation implements pi.mcp.servers.probe.
func (s *nativeSupervisor) probeMCPServerOperation(ctx context.Context, params map[string]any) (json.RawMessage, error) {
	definition, ok := params["definition"].(map[string]any)
	if !ok || len(definition) == 0 {
		return nil, errors.New("MCP server definition is required")
	}
	result, err := probeMCPServer(ctx, definition)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
