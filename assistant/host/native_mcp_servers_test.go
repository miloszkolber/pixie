package host

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readJSONObject(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value := map[string]any{}
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return value
}

func serverByName(t *testing.T, servers []mcpServerState, name string) mcpServerState {
	t.Helper()
	for _, server := range servers {
		if server.Name == name {
			return server
		}
	}
	t.Fatalf("server %q is absent from %#v", name, servers)
	return mcpServerState{}
}

// TestReadMCPServersMergesPerFieldAndAppliesDisabledOverride proves the
// documented precedence order, per-field higher-layer wins, and the
// higher-layer disabled override while retaining lower-layer fields.
func TestReadMCPServersMergesPerFieldAndAppliesDisabledOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	agentDir := t.TempDir()
	projectDir := t.TempDir()

	writeJSONFile(t, filepath.Join(home, ".config", "mcp", "mcp.json"), map[string]any{
		"mcpServers": map[string]any{"alpha": map[string]any{"url": "http://user-config", "headers": map[string]any{"X-Low": "1"}}},
	})
	writeJSONFile(t, filepath.Join(home, ".agents", "mcp.json"), map[string]any{
		"mcpServers": map[string]any{
			"alpha": map[string]any{"url": "http://user-agents"},
			"beta":  map[string]any{"url": "http://beta"},
		},
	})
	writeJSONFile(t, filepath.Join(agentDir, "mcp.json"), map[string]any{
		"mcpServers": map[string]any{
			"alpha": map[string]any{"url": "http://agent-dir"},
			"gamma": map[string]any{"command": "run"},
		},
	})
	writeJSONFile(t, filepath.Join(projectDir, ".mcp.json"), map[string]any{
		"mcpServers": map[string]any{"alpha": map[string]any{"url": "http://project"}},
	})
	writeJSONFile(t, filepath.Join(projectDir, ".pi", "mcp.json"), map[string]any{
		"mcpServers": map[string]any{"alpha": map[string]any{"disabled": true}},
	})

	servers, warnings, err := readMCPServers(agentDir, projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	alpha := serverByName(t, servers, "alpha")
	if alpha.Layer != "project-pi" || !alpha.Disabled {
		t.Fatalf("alpha = %#v", alpha)
	}
	if alpha.Definition["url"] != "http://project" {
		t.Fatalf("alpha url = %#v", alpha.Definition["url"])
	}
	headers, _ := alpha.Definition["headers"].(map[string]any)
	if headers["X-Low"] != "1" {
		t.Fatalf("lower-layer field was not retained: %#v", alpha.Definition)
	}
	if alpha.Path != filepath.Join(projectDir, ".pi", "mcp.json") {
		t.Fatalf("alpha path = %q", alpha.Path)
	}
	if beta := serverByName(t, servers, "beta"); beta.Layer != "user-agents" {
		t.Fatalf("beta = %#v", beta)
	}
	if gamma := serverByName(t, servers, "gamma"); gamma.Layer != "agent-dir" || gamma.Disabled {
		t.Fatalf("gamma = %#v", gamma)
	}
}

// TestReadMCPServersWarnsOnMalformedLayerWithoutFailing proves a broken layer
// degrades to a warning instead of failing the whole read.
func TestReadMCPServersWarnsOnMalformedLayerWithoutFailing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "mcp.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(home, ".agents", "mcp.json"), map[string]any{
		"mcpServers": map[string]any{"survivor": map[string]any{"url": "http://kept"}},
	})
	servers, warnings, err := readMCPServers(agentDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if server := serverByName(t, servers, "survivor"); server.Definition["url"] != "http://kept" {
		t.Fatalf("survivor = %#v", server)
	}
}

// TestUpsertRemoveMCPServerScopesWritesToAgentLayer proves the Pixie-owned
// layer is the only file written and that unrelated keys and sibling servers
// are preserved.
func TestUpsertRemoveMCPServerScopesWritesToAgentLayer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	agentDir := t.TempDir()
	projectDir := t.TempDir()
	agentPath := filepath.Join(agentDir, "mcp.json")
	userAgentsPath := filepath.Join(home, ".agents", "mcp.json")

	writeJSONFile(t, agentPath, map[string]any{
		"theme": "dark",
		"mcpServers": map[string]any{
			"other":         map[string]any{"command": "keep"},
			"pixie-browser": map[string]any{"url": "http://old"},
		},
	})
	writeJSONFile(t, userAgentsPath, map[string]any{
		"mcpServers": map[string]any{"lower": map[string]any{"url": "http://lower"}},
	})

	if err := upsertMCPServer(agentDir, projectDir, "pixie-browser", map[string]any{"url": "http://new"}); err != nil {
		t.Fatal(err)
	}
	document := readJSONObject(t, agentPath)
	if document["theme"] != "dark" {
		t.Fatalf("unrelated top-level key lost: %#v", document)
	}
	servers, _ := document["mcpServers"].(map[string]any)
	if _, present := servers["other"]; !present {
		t.Fatalf("sibling server lost: %#v", servers)
	}
	updated, _ := servers["pixie-browser"].(map[string]any)
	if updated["url"] != "http://new" {
		t.Fatalf("target server = %#v", updated)
	}

	if err := removeMCPServer(agentDir, projectDir, "pixie-browser"); err != nil {
		t.Fatal(err)
	}
	document = readJSONObject(t, agentPath)
	if document["theme"] != "dark" {
		t.Fatalf("unrelated top-level key lost after remove: %#v", document)
	}
	servers, _ = document["mcpServers"].(map[string]any)
	if _, present := servers["pixie-browser"]; present {
		t.Fatalf("target server survived remove: %#v", servers)
	}
	if _, present := servers["other"]; !present {
		t.Fatalf("sibling server lost after remove: %#v", servers)
	}
	// A missing entry is a no-op, and other layers are never touched.
	if err := removeMCPServer(agentDir, projectDir, "absent"); err != nil {
		t.Fatal(err)
	}
	lower := readJSONObject(t, userAgentsPath)
	lowerServers, _ := lower["mcpServers"].(map[string]any)
	if _, present := lowerServers["lower"]; !present {
		t.Fatalf("lower layer was modified: %#v", lower)
	}
	if err := upsertMCPServer(agentDir, projectDir, "bad/name", map[string]any{"url": "http://x"}); err == nil {
		t.Fatal("unsafe server name was accepted")
	}
}

// TestMCPServerOperationsDispatchThroughCallHost exercises the registered
// operation names end to end with the supervisor's own agent directory.
func TestMCPServerOperationsDispatchThroughCallHost(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	supervisor, agentDir, cwd := startAgentSupervisor(t)

	upserted := callAgent(t, supervisor, "pi.mcp.servers.upsert", map[string]any{
		"name": "pixie-browser", "definition": map[string]any{"url": "http://127.0.0.1:3000/mcp"},
	})
	servers, _ := upserted["servers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("upsert response = %#v", upserted)
	}
	entry, _ := servers[0].(map[string]any)
	if entry["name"] != "pixie-browser" || entry["layer"] != "agent-dir" {
		t.Fatalf("upsert entry = %#v", entry)
	}

	read := callAgent(t, supervisor, "pi.mcp.servers.read", map[string]any{"projectDir": cwd, "name": "pixie-browser"})
	if len(read["servers"].([]any)) != 1 {
		t.Fatalf("read response = %#v", read)
	}
	if _, err := supervisor.callHost(context.Background(), "pi.mcp.servers.upsert", map[string]any{"name": "pixie-browser"}); err == nil {
		t.Fatal("upsert without a definition was accepted")
	}
	if removed := callAgent(t, supervisor, "pi.mcp.servers.remove", map[string]any{"name": "pixie-browser"}); removed["ok"] != true {
		t.Fatalf("remove response = %#v", removed)
	}
	after := callAgent(t, supervisor, "pi.mcp.servers.read", map[string]any{"name": "pixie-browser"})
	if len(after["servers"].([]any)) != 0 {
		t.Fatalf("read after remove = %#v", after)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "pixie", mcpConfigLockName)); err != nil {
		t.Fatalf("lock file was not created: %v", err)
	}
}

// TestProbeMCPServerHandshakesJSONAndSSE proves the bounded probe performs
// initialize, notifications/initialized and tools/list over HTTP JSON and MCP
// event streams and forwards definition headers.
func TestProbeMCPServerHandshakesJSONAndSSE(t *testing.T) {
	calls := make(chan string, 8)
	headerSeen := make(chan string, 8)
	handler := func(response http.ResponseWriter, request *http.Request) {
		if value := request.Header.Get("X-Test-Header"); value != "" {
			headerSeen <- value
		}
		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			http.Error(response, "bad request", http.StatusBadRequest)
			return
		}
		calls <- rpc.Method
		switch rpc.Method {
		case "initialize":
			if request.URL.Query().Get("sse") == "1" {
				response.Header().Set("Content-Type", "text/event-stream")
				_, _ = response.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":\"2025-06-18\",\"serverInfo\":{\"name\":\"stub-sse\",\"version\":\"1\"}}}\n\n"))
				return
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"stub","version":"1"}}}`))
		case "notifications/initialized":
			response.WriteHeader(http.StatusAccepted)
		case "tools/list":
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"browse"},{"name":"snapshot"}]}}`))
		default:
			http.Error(response, "unexpected method", http.StatusBadRequest)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()

	result, err := probeMCPServer(context.Background(), map[string]any{
		"url":     server.URL,
		"headers": map[string]any{"X-Test-Header": "present"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reachable || result.ServerInfo["name"] != "stub" || result.ProtocolVersion != "2025-06-18" {
		t.Fatalf("probe result = %#v", result)
	}
	if !reflect.DeepEqual(result.Tools, []string{"browse", "snapshot"}) {
		t.Fatalf("probe tools = %#v", result.Tools)
	}
	got := []string{<-calls, <-calls, <-calls}
	if !reflect.DeepEqual(got, []string{"initialize", "notifications/initialized", "tools/list"}) {
		t.Fatalf("probe call order = %#v", got)
	}
	if header := <-headerSeen; header != "present" {
		t.Fatalf("probe header = %q", header)
	}

	sseResult, err := probeMCPServer(context.Background(), map[string]any{"url": server.URL + "?sse=1"})
	if err != nil {
		t.Fatal(err)
	}
	if !sseResult.Reachable || sseResult.ServerInfo["name"] != "stub-sse" {
		t.Fatalf("sse probe result = %#v", sseResult)
	}
}

// TestProbeMCPServerReportsUnreachableWithoutFailing proves a transport failure
// is a bounded result, while an unsafe definition is rejected.
func TestProbeMCPServerReportsUnreachableWithoutFailing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()
	result, err := probeMCPServer(context.Background(), map[string]any{"url": url})
	if err != nil {
		t.Fatal(err)
	}
	if result.Reachable || result.Error == "" {
		t.Fatalf("unreachable probe = %#v", result)
	}
	if _, err := probeMCPServer(context.Background(), map[string]any{"url": "file:///etc/passwd"}); err == nil {
		t.Fatal("non-http MCP url was accepted")
	}
	if _, err := probeMCPServer(context.Background(), map[string]any{}); err == nil || !strings.Contains(err.Error(), "url") {
		t.Fatalf("missing url error = %v", err)
	}
}
