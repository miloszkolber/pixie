package mcpserver_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/mcpserver"
)

func engineRegistry(t *testing.T, root string, getenv func(string) (string, bool)) *mcpserver.Registry {
	t.Helper()
	agentBrowser := filepath.Join(root, "fake-browser")
	if err := os.WriteFile(agentBrowser, []byte("#!/bin/sh\nprintf '%s' \"${AGENT_BROWSER_CDP-unset}\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	registry, err := mcpserver.NewRegistry(mcpserver.Config{
		Host: "127.0.0.1", Port: 17874, DataDir: dataDir, Getenv: getenv,
		Binaries: &mcpserver.BinaryConfig{
			AgentBrowser: agentBrowser, BrowserConfig: configPath,
			ArtifactRoot: filepath.Join(root, "artifacts"), StateRoot: filepath.Join(root, "state"),
		},
	}, diagnostics.NormalizeBuild("test", "test"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Shutdown)
	return registry
}

func reopenEngineRegistry(t *testing.T, root string, getenv func(string) (string, bool)) *mcpserver.Registry {
	t.Helper()
	registry := engineRegistry(t, root, getenv)
	return registry
}

func engineStatus(t *testing.T, registry *mcpserver.Registry) map[string]any {
	t.Helper()
	response := serve(registry, http.MethodGet, mcpserver.StatusPath, "", "127.0.0.1:17874", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	var status map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	return status
}

type engineToolResult struct {
	StructuredContent map[string]any `json:"structuredContent"`
	Content           []struct{ Type, Text string }
	IsError           bool
}

func engineBrowserCommand(t *testing.T, registry *mcpserver.Registry, session string) engineToolResult {
	t.Helper()
	params := map[string]any{
		"name":      "browser_command",
		"arguments": map[string]any{"session": session, "command": "snapshot"},
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params})
	if err != nil {
		t.Fatal(err)
	}
	response := serve(registry, http.MethodPost, "/mcp/browser", string(body), "127.0.0.1:17874", map[string]string{
		"Content-Type": "application/json", "Accept": "application/json, text/event-stream",
		"MCP-Protocol-Version": "2025-11-25",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("browser command status = %d %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || len(envelope.Error) > 0 {
		t.Fatalf("browser command envelope = %d %s (%v)", response.Code, response.Body.String(), err)
	}
	var result engineToolResult
	if err := json.Unmarshal(envelope.Result, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestBrowserEngineDefaultsToChromium(t *testing.T) {
	root := t.TempDir()
	registry := engineRegistry(t, root, nil)
	if registry.Engine() != "chromium" {
		t.Fatalf("default engine = %q", registry.Engine())
	}
	if status := engineStatus(t, registry); status["browserEngine"] != "chromium" {
		t.Fatalf("status engine = %#v", status["browserEngine"])
	}
	if ready, _ := registry.Health("browser"); !ready {
		t.Fatal("chromium engine is not ready with fixture binaries")
	}
}

func TestBrowserEngineRejectsUnknownBackends(t *testing.T) {
	root := t.TempDir()
	registry := engineRegistry(t, root, nil)
	if err := registry.SetEngine("firefox"); err == nil {
		t.Fatal("unknown engine accepted")
	}
	if registry.Engine() != "chromium" {
		t.Fatalf("engine after rejection = %q", registry.Engine())
	}
}

func TestObscuraWithoutEndpointDegradesInsteadOfFallingBack(t *testing.T) {
	root := t.TempDir()
	registry := engineRegistry(t, root, nil)
	if err := registry.SetEngine("obscura"); err != nil {
		t.Fatal(err)
	}
	if ready, detail := registry.Health("browser"); ready || !strings.Contains(detail, "PIXIE_BROWSER_CDP") {
		t.Fatalf("obscura health = %v %q", ready, detail)
	}
	catalog := registry.Catalog()
	module := catalog.Modules[0]
	// The engine switch never alters the model-facing API: identity, tools,
	// and resources stay identical; only readiness degrades.
	if module.ID != "browser" || module.ExtensionName != "pixie-browser" || module.Path != "/mcp/browser" ||
		module.Transport != "streamable_http" {
		t.Fatalf("obscura module identity changed = %#v", module)
	}
	if module.State != "unavailable" || !strings.Contains(module.Detail, "PIXIE_BROWSER_CDP") {
		t.Fatalf("obscura module = %#v", module)
	}
	params := map[string]any{
		"name":      "browser_command",
		"arguments": map[string]any{"session": "obscura-degraded", "command": "snapshot"},
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params})
	if err != nil {
		t.Fatal(err)
	}
	degraded := serve(registry, http.MethodPost, "/mcp/browser", string(body), "127.0.0.1:17874", map[string]string{
		"Content-Type": "application/json", "Accept": "application/json, text/event-stream",
		"MCP-Protocol-Version": "2025-11-25",
	})
	if degraded.Code != http.StatusNotFound {
		t.Fatalf("degraded obscura status = %d %s", degraded.Code, degraded.Body.String())
	}
	if err := registry.SetEngine("chromium"); err != nil {
		t.Fatal(err)
	}
	if ready, _ := registry.Health("browser"); !ready {
		t.Fatal("chromium engine did not recover")
	}
}

func TestObscuraWithEndpointStartsAndChromiumIgnoresCDP(t *testing.T) {
	root := t.TempDir()
	getenv := func(key string) (string, bool) {
		if key == "PIXIE_BROWSER_CDP" {
			return "127.0.0.1:9222", true
		}
		return "", false
	}
	registry := engineRegistry(t, root, getenv)
	// The persisted chromium engine wins over the operator CDP override: the
	// child never sees a CDP backend while chromium is selected.
	result := engineBrowserCommand(t, registry, "chromium-ignores-cdp")
	if result.IsError || result.StructuredContent["stdout"] != "unset" {
		t.Fatalf("chromium child saw CDP: %#v", result)
	}
	if err := registry.SetEngine("obscura"); err != nil {
		t.Fatal(err)
	}
	if ready, detail := registry.Health("browser"); !ready {
		t.Fatalf("obscura with endpoint not ready: %q", detail)
	}
	result = engineBrowserCommand(t, registry, "obscura-uses-cdp")
	if result.IsError || result.StructuredContent["stdout"] != "127.0.0.1:9222" {
		t.Fatalf("obscura child missed CDP: %#v", result)
	}
}

func TestBrowserEnginePersistsAcrossRestarts(t *testing.T) {
	root := t.TempDir()
	first := engineRegistry(t, root, nil)
	if err := first.SetEngine("obscura"); err != nil {
		t.Fatal(err)
	}
	first.Shutdown()
	second := reopenEngineRegistry(t, root, nil)
	if second.Engine() != "obscura" {
		t.Fatalf("reopened engine = %q", second.Engine())
	}
	raw, err := os.ReadFile(filepath.Join(root, "data", "browser.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"engine"`) {
		t.Fatalf("browser.json = %s", raw)
	}
}
