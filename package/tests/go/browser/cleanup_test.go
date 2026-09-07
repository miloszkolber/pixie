package browser_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/browser"
)

func TestClosingAbsentBrowserSessionDoesNotConsumeCapacityOrLaunchProcess(t *testing.T) {
	runtime := newTestRuntime(t, true, func(config *browser.Config) { config.MaxSessions = 1 })
	if response := postBrowserRequest(t, runtime.service, testToken, "snapshot", "external", nil); response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	// Every attempted process start would fail, proving absent close is local.
	if err := os.Remove(runtime.config.AgentBrowser); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		response := postBrowserRequest(t, runtime.service, testToken, "close", "absent", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("idempotent close failed at capacity: %s", response.Body.String())
		}
	}
	for _, root := range []string{runtime.config.ArtifactRoot, runtime.config.StateRoot} {
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 1 || entries[0].Name() != "external" {
			t.Fatalf("close changed unrelated session storage: %v, %v", entries, err)
		}
	}
}

func TestFailedEmergencyCloseRetainsRuntimeAddressForRetry(t *testing.T) {
	runtime := newTestRuntime(t, false, nil)
	original, err := os.ReadFile(runtime.config.AgentBrowser)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(original), "close) exit 0", "close) exit 9", 1)
	if err := os.WriteFile(runtime.config.AgentBrowser, []byte(broken), 0700); err != nil {
		t.Fatal(err)
	}
	// A signalled command triggers the service's emergency close path.
	response := postBrowserRequest(t, runtime.service, "", "back", "retry-close", nil)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("expected failed command: %s", response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(runtime.config.StateRoot, "retry-close", "run")); err != nil {
		t.Fatalf("failed close discarded the daemon's socket location: %v", err)
	}
	if err := os.WriteFile(runtime.config.AgentBrowser, original, 0700); err != nil {
		t.Fatal(err)
	}
	response = postBrowserRequest(t, runtime.service, "", "close", "retry-close", nil)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	for _, root := range []string{runtime.config.ArtifactRoot, runtime.config.StateRoot} {
		if _, err := os.Stat(filepath.Join(root, "retry-close")); !os.IsNotExist(err) {
			t.Fatalf("successful close retained session storage: %v", err)
		}
	}
}
