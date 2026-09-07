package browser_test

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
	"time"

	"github.com/miloszkolber/pixie/internal/browser"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOfficialMCPClientUsesTheStatelessBrowserService(t *testing.T) {
	listener, port := listenLoopback(t)
	runtime := newTestRuntime(t, false, func(config *browser.Config) {
		config.Port = port
		config.MaxTotalArtifactBytes = 1 << 20
	})
	server := &http.Server{Handler: runtime.service}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		<-serveDone
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "pixie-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: "http://" + listener.Addr().String() + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil || len(listed.Tools) != 2 {
		t.Fatalf("SDK tools/list = %#v, %v", listed, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "browser_command", Arguments: map[string]any{"session": "standard-client", "command": "snapshot"}})
	if err != nil || result.IsError || len(result.Content) != 1 {
		t.Fatalf("SDK tools/call = %#v, %v", result, err)
	}
	if text, ok := result.Content[0].(*mcp.TextContent); !ok || !containsAll(text.Text, `"stdout":"stdout"`, `"stderr":"stderr"`) {
		t.Fatalf("SDK result = %#v", result)
	}
}

func TestMCPDiscoveryDocumentsCommandsAndSharesTheLegacyExecutor(t *testing.T) {
	runtime := newTestRuntime(t, true, nil)
	var initialized struct {
		ProtocolVersion, Instructions string
		ServerInfo                    struct{ Name, Version string }
	}
	mcpRoundTrip(t, runtime.service, "initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "test", "version": "1"}}, &initialized)
	if initialized.ProtocolVersion != "2025-11-25" || initialized.ServerInfo.Name != "pixie-browser" || initialized.ServerInfo.Version != "2.3.4" || !strings.Contains(initialized.Instructions, "pixie://browser/guide") {
		t.Fatalf("initialize = %#v", initialized)
	}

	var listed struct {
		Tools []struct {
			Name        string
			InputSchema struct {
				Properties struct {
					Command struct{ Enum []string }
				}
			}
		}
	}
	mcpRoundTrip(t, runtime.service, "tools/list", map[string]any{}, &listed)
	if len(listed.Tools) != 2 {
		t.Fatalf("tools = %#v", listed)
	}
	var commands []string
	for _, tool := range listed.Tools {
		if tool.Name == "browser_command" {
			commands = tool.InputSchema.Properties.Command.Enum
		}
	}
	for _, required := range []string{"open", "snapshot", "screenshot", "close"} {
		if !contains(commands, required) {
			t.Fatalf("browser command schema omits %q: %#v", required, commands)
		}
	}
	if contains(commands, "eval") {
		t.Fatalf("browser command schema exposed eval: %#v", commands)
	}

	var resource struct {
		Contents []struct{ URI, MIMEType, Text string }
	}
	mcpRoundTrip(t, runtime.service, "resources/read", map[string]any{"uri": "pixie://browser/guide"}, &resource)
	if len(resource.Contents) != 1 || resource.Contents[0].MIMEType != "text/markdown" || !containsAll(resource.Contents[0].Text, "`open`", "`snapshot`", "`close`") {
		t.Fatalf("guide resource = %#v", resource)
	}
	var guidance browserToolResult
	mcpRoundTrip(t, runtime.service, "tools/call", map[string]any{"name": "browser_guidance", "arguments": map[string]any{}}, &guidance)
	if guidance.IsError || len(guidance.Content) != 1 || guidance.Content[0].Text != resource.Contents[0].Text {
		t.Fatalf("guide tool = %#v", guidance)
	}

	var result browserToolResult
	mcpRoundTrip(t, runtime.service, "tools/call", map[string]any{"name": "browser_command", "arguments": map[string]any{"command": "snapshot", "session": "shared"}}, &result)
	var textResult map[string]any
	if result.IsError || result.StructuredContent["stdout"] != "stdout" || result.StructuredContent["session"] != "shared" || len(result.Content) != 1 || json.Unmarshal([]byte(result.Content[0].Text), &textResult) != nil || !reflect.DeepEqual(textResult, result.StructuredContent) {
		t.Fatalf("browser tool result = %#v", result)
	}
	legacy := postBrowserRequest(t, runtime.service, testToken, "snapshot", "shared", nil)
	var legacyResult map[string]any
	if err := json.Unmarshal(legacy.Body.Bytes(), &legacyResult); err != nil {
		t.Fatal(err)
	}
	delete(textResult, "session")
	if legacy.Code != http.StatusOK || !reflect.DeepEqual(textResult, legacyResult) {
		t.Fatalf("MCP and legacy results differ: %#v, %#v", textResult, legacyResult)
	}
}

func TestMCPAuthenticationOriginAndBodyBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, host, origin, token, publicOrigin, fetchSite string
		status                                             int
	}{
		{name: "native", token: testToken, status: http.StatusOK},
		{name: "same origin", token: testToken, origin: "http://127.0.0.1:8787", status: http.StatusOK},
		{name: "missing token", status: http.StatusUnauthorized},
		{name: "wrong token", token: "wrong", status: http.StatusUnauthorized},
		{name: "cross origin", token: testToken, origin: "https://evil.example", status: http.StatusForbidden},
		{name: "rebound host", token: testToken, host: "evil.example:8787", status: http.StatusForbidden},
		{name: "cross site", token: testToken, fetchSite: "cross-site", status: http.StatusForbidden},
		{name: "trusted proxy", token: testToken, host: "browser.example:443", origin: "https://browser.example", publicOrigin: "https://browser.example", status: http.StatusOK},
		{name: "proxy foreign origin", token: testToken, origin: "https://evil.example", publicOrigin: "https://browser.example", status: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := newTestRuntime(t, true, func(config *browser.Config) { config.PublicOrigin = test.publicOrigin })
			request := newMCPRequest(t, "tools/list", map[string]any{})
			request.Header.Set("Authorization", "Bearer "+test.token)
			if test.host != "" {
				request.Host = test.host
			}
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			request.Header.Set("Sec-Fetch-Site", test.fetchSite)
			response := httptest.NewRecorder()
			runtime.service.ServeHTTP(response, request)
			if response.Code != test.status || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}

	runtime := newTestRuntime(t, false, nil)
	request := newMCPRequest(t, "tools/list", map[string]any{})
	request.Header.Del("Authorization")
	request.Header.Set("Origin", "http://127.0.0.1:8787")
	request.Header.Add("Origin", "http://127.0.0.1:8787")
	response := httptest.NewRecorder()
	runtime.service.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("multiple origins accepted: %d", response.Code)
	}

	oversized := newMCPRequest(t, "tools/call", map[string]any{"name": "browser_command", "arguments": strings.Repeat("x", 64*1024+1)})
	response = httptest.NewRecorder()
	runtime.service.ServeHTTP(response, oversized)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("MCP accepted oversized body: %d %s", response.Code, response.Body.String())
	}
}

func TestMCPAndLegacyRoutesShareArtifactsQuotasAndShutdown(t *testing.T) {
	runtime := newTestRuntime(t, true, nil)
	var result browserToolResult
	mcpRoundTrip(t, runtime.service, "tools/call", map[string]any{"name": "browser_command", "arguments": map[string]any{"command": "screenshot", "session": "shared", "args": []string{"screen.png"}}}, &result)
	artifact, _ := result.StructuredContent["artifact"].(map[string]any)
	if result.IsError || artifact["url"] != "/v1/artifacts/shared/screen.png" {
		t.Fatalf("screenshot = %#v", result)
	}
	duplicate := postBrowserRequest(t, runtime.service, testToken, "screenshot", "shared", []string{"screen.png"})
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("legacy overwrote MCP artifact: %d", duplicate.Code)
	}
	mcpRoundTrip(t, runtime.service, "tools/call", map[string]any{"name": "browser_command", "arguments": map[string]any{"command": "screenshot", "session": "second", "args": []string{"two.png"}}}, &result)
	if !result.IsError || result.StructuredContent["code"] != "quota_exceeded" {
		t.Fatalf("MCP ignored shared artifact quota: %#v", result)
	}
	if response := postBrowserRequest(t, runtime.service, testToken, "close", "shared", nil); response.Code != http.StatusOK {
		t.Fatalf("legacy close failed: %d", response.Code)
	}
	if _, err := os.Stat(filepath.Join(runtime.config.ArtifactRoot, "shared")); !os.IsNotExist(err) {
		t.Fatalf("legacy close retained MCP artifacts: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := newMCPRequest(t, "tools/call", map[string]any{"name": "browser_command", "arguments": map[string]any{"command": "open", "session": "shutdown", "args": []string{"https://example.com"}}}).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		runtime.service.ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	waitForPath(t, filepath.Join(runtime.config.StateRoot, "shutdown", ".lock"))
	runtime.service.Shutdown()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not cancel the MCP command")
	}
	if _, err := os.Stat(filepath.Join(runtime.config.StateRoot, "shutdown")); !os.IsNotExist(err) {
		t.Fatalf("shutdown retained MCP state: %v", err)
	}
	rejected := postBrowserRequest(t, runtime.service, testToken, "snapshot", "late", nil)
	if rejected.Code != http.StatusServiceUnavailable || responseCode(t, rejected) != "shutting_down" {
		t.Fatalf("shutdown accepted new work: %d %s", rejected.Code, rejected.Body.String())
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
