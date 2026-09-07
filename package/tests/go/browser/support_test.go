package browser_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/browser"
	"github.com/miloszkolber/pixie/internal/diagnostics"
)

const testToken = "server-test-token-0123456789abcdef0123456789"

type testRuntime struct {
	service *browser.Service
	config  browser.Config
	root    string
}

func newTestRuntime(t *testing.T, authentication bool, configure func(*browser.Config)) *testRuntime {
	t.Helper()
	root := t.TempDir()
	agentBrowser := filepath.Join(root, "fake-browser")
	configFile := filepath.Join(root, "config.json")
	largeOutput := filepath.Join(root, "large-output")
	if err := os.WriteFile(largeOutput, bytes.Repeat([]byte("x"), 300_000), 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
agent_session="$4"
shift 4
command="$1"
shift
case "$command" in
  click) printf 'failed action'; exit 7 ;;
  open) /bin/sleep 30 ;;
  back) kill -TERM $$ ;;
  forward) i=0; while [ "$i" -lt 2048 ]; do printf x; i=$((i + 1)); done > "$HOME/blob"; /bin/sleep 30 ;;
  reload) i=0; while [ "$i" -lt 2048 ]; do printf x; i=$((i + 1)); done > "$HOME/blob" ;;
  press) /bin/sleep 30 & exit 0 ;;
  vitals) /bin/cat %q; /bin/cat %q >&2 ;;
  get) printf '%%s\n' "$agent_session" "$PATH" "$HOME" "$TMPDIR" "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_STATE_HOME" "$AGENT_BROWSER_SOCKET_DIR" "$AGENT_BROWSER_CONTENT_BOUNDARIES" "$AGENT_BROWSER_MAX_OUTPUT" "${PIXIE_PRIVATE_TEST-unset}" ;;
  screenshot) printf 'fake-image' > "$1" ;;
  snapshot) printf 'stdout'; printf 'stderr' >&2 ;;
  close) exit 0 ;;
  *) printf '%%s' "$command" ;;
esac
`, largeOutput, largeOutput)
	if err := os.WriteFile(agentBrowser, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configFile, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration := browser.Config{
		Host:                  "127.0.0.1",
		Port:                  8787,
		Authentication:        authentication,
		Token:                 testToken,
		ArtifactRoot:          filepath.Join(root, "artifacts"),
		StateRoot:             filepath.Join(root, "state"),
		AgentBrowser:          agentBrowser,
		BrowserConfig:         configFile,
		CommandTimeout:        2 * time.Second,
		RequestTimeout:        4 * time.Second,
		MaxArtifactBytes:      64,
		MaxTotalArtifactBytes: 10,
		MaxStateBytes:         1024,
		MaxSessions:           4,
		MaxStateEntries:       100,
	}
	if !authentication {
		configuration.Token = ""
	}
	if configure != nil {
		configure(&configuration)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service, err := browser.NewService(configuration, diagnostics.NormalizeBuild("2.3.4", "abcdef123456"), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Shutdown)
	return &testRuntime{service: service, config: configuration, root: root}
}

func browserRequest(t *testing.T, ctx context.Context, token, command, session string, args []string) *http.Request {
	t.Helper()
	body, err := json.Marshal(map[string]any{"command": command, "session": session, "args": args})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/browser", bytes.NewReader(body)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return request
}

func postBrowserRequest(t *testing.T, handler http.Handler, token, command, session string, args []string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, browserRequest(t, context.Background(), token, command, session, args))
	return response
}

func responseCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	code, _ := body["code"].(string)
	return code
}

func newMCPRequest(t *testing.T, method string, params any) *http.Request {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	request.Host = "127.0.0.1:8787"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2025-11-25")
	request.Header.Set("Authorization", "Bearer "+testToken)
	return request
}

func mcpRoundTrip(t *testing.T, handler http.Handler, method string, params, target any) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, newMCPRequest(t, method, params))
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || response.Code != http.StatusOK || len(envelope.Error) > 0 {
		t.Fatalf("MCP response = %d %s (%v)", response.Code, response.Body.String(), err)
	}
	if err := json.Unmarshal(envelope.Result, target); err != nil {
		t.Fatal(err)
	}
}

type browserToolResult struct {
	StructuredContent map[string]any `json:"structuredContent"`
	Content           []struct{ Type, Text string }
	IsError           bool
}

func waitForPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func listenLoopback(t *testing.T) (net.Listener, int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return listener, listener.Addr().(*net.TCPAddr).Port
}

func containsAll(value string, expected ...string) bool {
	for _, item := range expected {
		if !strings.Contains(value, item) {
			return false
		}
	}
	return true
}
