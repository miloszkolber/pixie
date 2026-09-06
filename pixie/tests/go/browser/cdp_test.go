package browser_test

import (
	"bytes"
	"context"
	"encoding/json"
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

func cdpLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func TestCDPEndpointDefaultsToChromiumLaunch(t *testing.T) {
	config, err := browser.ConfigFromEnvironment(cdpLookup(map[string]string{}))
	if err != nil {
		t.Fatal(err)
	}
	if config.CDPEndpoint != "" {
		t.Fatalf("default CDP endpoint = %q", config.CDPEndpoint)
	}
}

func TestCDPEndpointAcceptsPortAndHostPort(t *testing.T) {
	for _, endpoint := range []string{"9222", "127.0.0.1:9222", "obscura:9222"} {
		config, err := browser.ConfigFromEnvironment(cdpLookup(map[string]string{"PIXIE_BROWSER_CDP": endpoint}))
		if err != nil {
			t.Fatalf("CDP endpoint %q: %v", endpoint, err)
		}
		if config.CDPEndpoint != endpoint {
			t.Fatalf("CDP endpoint %q kept %q", endpoint, config.CDPEndpoint)
		}
	}
}

func TestCDPEndpointRejectsInvalidValues(t *testing.T) {
	for _, endpoint := range []string{"0", "99999", "9222x", "ws://127.0.0.1:9222", "http://x:9222", "127.0.0.1:", ":9222:", "a/b:9222", "has space:9222", "[::1]:9222"} {
		if _, err := browser.ConfigFromEnvironment(cdpLookup(map[string]string{"PIXIE_BROWSER_CDP": endpoint})); err == nil {
			t.Fatalf("CDP endpoint %q accepted", endpoint)
		}
	}
}

// The shared fake binary only implements the policy-visible command surface,
// so CDP passthrough uses a local probe that reports the child environment
// for any allowed command.
func newCDPProbe(t *testing.T, cdpEndpoint string) (http.Handler, string) {
	t.Helper()
	root := t.TempDir()
	probe := filepath.Join(root, "cdp-probe")
	script := "#!/bin/sh\nprintf '%s' \"${AGENT_BROWSER_CDP-unset}\"\n"
	if err := os.WriteFile(probe, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(root, "config.json")
	if err := os.WriteFile(configFile, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := browser.NewService(browser.Config{
		Host: "127.0.0.1", Port: 8787,
		ArtifactRoot: filepath.Join(root, "artifacts"), StateRoot: filepath.Join(root, "state"),
		AgentBrowser: probe, BrowserConfig: configFile, CDPEndpoint: cdpEndpoint,
		CommandTimeout: 5 * time.Second, RequestTimeout: 10 * time.Second,
		MaxArtifactBytes: 1 << 20, MaxTotalArtifactBytes: 1 << 20, MaxStateBytes: 1 << 20,
		MaxSessions: 4, MaxStateEntries: 100,
	}, diagnostics.NormalizeBuild("test", "test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Shutdown)
	return service, root
}

func probeCDP(t *testing.T, handler http.Handler, session string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"command": "snapshot", "session": session})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/browser", bytes.NewReader(body)).WithContext(context.Background())
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("probe status = %d %s", response.Code, response.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	stdout, _ := result["stdout"].(string)
	return stdout
}

func TestChildEnvironmentOmitsCDPByDefault(t *testing.T) {
	handler, _ := newCDPProbe(t, "")
	if stdout := probeCDP(t, handler, "chromium-default"); stdout != "unset" {
		t.Fatalf("default child AGENT_BROWSER_CDP = %q", stdout)
	}
}

func TestChildEnvironmentPassesCDPEndpoint(t *testing.T) {
	handler, _ := newCDPProbe(t, "127.0.0.1:9222")
	if stdout := probeCDP(t, handler, "obscura-selected"); stdout != "127.0.0.1:9222" {
		t.Fatalf("child AGENT_BROWSER_CDP = %q", stdout)
	}
}

// The merged deployment shares one container UID between the controller and
// the Browser subprocess, so the child environment must not inherit any
// controller secrets: a compromised renderer sees only scoped directories.
func TestChildEnvironmentCarriesNoControllerSecrets(t *testing.T) {
	t.Setenv("PIXIE_TOKEN", "controller-secret-must-not-leak")
	t.Setenv("PIXIE_MCP_TOKEN", "mcp-secret-must-not-leak")
	t.Setenv("PIXIE_BROWSER_TOKEN", "browser-secret-must-not-leak")
	t.Setenv("PIXIE_PI_SECRET_KEY", "pi-secret-must-not-leak")
	root := t.TempDir()
	probe := filepath.Join(root, "env-probe")
	script := "#!/bin/sh\nfor name in PIXIE_TOKEN PIXIE_MCP_TOKEN PIXIE_BROWSER_TOKEN PIXIE_PI_SECRET_KEY HOME TMPDIR; do printf '%s=%s\\n' \"$name\" \"$(eval echo \\$$name)\"; done\n"
	if err := os.WriteFile(probe, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(root, "config.json")
	if err := os.WriteFile(configFile, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	stateRoot := filepath.Join(root, "state")
	service, err := browser.NewService(browser.Config{
		Host: "127.0.0.1", Port: 8787,
		ArtifactRoot: filepath.Join(root, "artifacts"), StateRoot: stateRoot,
		AgentBrowser: probe, BrowserConfig: configFile,
		CommandTimeout: 5 * time.Second, RequestTimeout: 10 * time.Second,
		MaxArtifactBytes: 1 << 20, MaxTotalArtifactBytes: 1 << 20, MaxStateBytes: 1 << 20,
		MaxSessions: 4, MaxStateEntries: 100,
	}, diagnostics.NormalizeBuild("test", "test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Shutdown)
	stdout := probeCDP(t, service, "env-secrecy")
	for _, leaked := range []string{"controller-secret-must-not-leak", "mcp-secret-must-not-leak", "browser-secret-must-not-leak", "pi-secret-must-not-leak"} {
		if strings.Contains(stdout, leaked) {
			t.Fatalf("child environment leaked a controller secret: %q", stdout)
		}
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "HOME=") && !strings.HasPrefix(strings.TrimPrefix(line, "HOME="), stateRoot) {
			t.Fatalf("child HOME escaped browser state: %q", stdout)
		}
		if strings.HasPrefix(line, "TMPDIR=") && !strings.HasPrefix(strings.TrimPrefix(line, "TMPDIR="), stateRoot) {
			t.Fatalf("child TMPDIR escaped browser state: %q", stdout)
		}
	}
}
