package mcpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/miloszkolber/pixie/internal/browser"
	"github.com/miloszkolber/pixie/internal/mcpserver"
)

const workerControllerUID = 4242

func conformingWorkerReport() browser.IsolationReport {
	return browser.IsolationReport{
		WorkerVersion: "test-worker",
		UID:           workerControllerUID + 1,
		GID:           workerControllerUID + 1,
		DistinctUser:  true,
		Filesystem: browser.FilesystemFacts{
			Mediated: true, PrivateTmp: true, SystemReadOnly: true, HomeProtected: true,
		},
		Network: browser.NetworkFacts{EgressRestricted: true},
		Limits: browser.LimitFacts{
			NoNewPrivileges: true, MemoryMaxBytes: 1 << 30, CPUQuota: true, TasksMax: 256,
		},
		Cleanup: browser.CleanupFacts{BoundedStop: true, SessionCleanup: true, StaleTempSweep: true},
	}
}

// isolationServer serves GET /isolation with the supplied report. When token
// is non-empty, the bearer is required.
func isolationServer(t *testing.T, report any, token string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/isolation" {
			http.NotFound(response, request)
			return
		}
		if token != "" && request.Header.Get("Authorization") != "Bearer "+token {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(report)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestRegistryVerifiesConformingWorkerBoundary(t *testing.T) {
	registry := testRegistry(t, nil)
	server := isolationServer(t, conformingWorkerReport(), "")
	if err := registry.VerifyWorkerBoundary(context.Background(), server.URL, "", workerControllerUID); err != nil {
		t.Fatalf("conforming worker boundary was rejected: %v", err)
	}
	if err := registry.SetEnabled("browser", true); err != nil {
		t.Fatalf("verified worker boundary did not open Browser enablement: %v", err)
	}
	if ready, detail := registry.Health("browser"); !ready {
		t.Fatalf("Browser is not ready behind a verified boundary: %s", detail)
	}
}

func TestRegistryRefusesUnprovenWorkerBoundaries(t *testing.T) {
	missingEndpoint := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.NotFound(response, request)
	}))
	t.Cleanup(missingEndpoint.Close)
	unreachable := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	unreachableURL := unreachable.URL
	unreachable.Close()

	nonConforming := conformingWorkerReport()
	nonConforming.Network.EgressRestricted = false
	sameUser := conformingWorkerReport()
	sameUser.UID = workerControllerUID

	cases := []struct {
		name   string
		url    string
		token  string
		report any
	}{
		{name: "non-conforming report", report: nonConforming},
		{name: "worker shares the controller uid", report: sameUser},
		{name: "missing endpoint", url: missingEndpoint.URL},
		{name: "unreachable worker", url: unreachableURL},
		{name: "not a url", url: "ftp://worker.example/isolation"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			registry := testRegistry(t, nil)
			workerURL := test.url
			if workerURL == "" {
				server := isolationServer(t, test.report, test.token)
				workerURL = server.URL
			}
			if err := registry.VerifyWorkerBoundary(context.Background(), workerURL, test.token, workerControllerUID); err == nil {
				t.Fatal("unproven worker boundary was accepted")
			}
			if err := registry.SetEnabled("browser", true); !errors.Is(err, mcpserver.ErrUnverifiedWorkerBoundary) {
				t.Fatalf("Browser was enableable after an unproven boundary: %v", err)
			}
			if ready, _ := registry.Health("browser"); ready {
				t.Fatal("Browser became ready after an unproven boundary")
			}
		})
	}
}

func TestRegistryWorkerBoundaryRequiresConfiguredBearer(t *testing.T) {
	registry := testRegistry(t, nil)
	server := isolationServer(t, conformingWorkerReport(), "worker-secret-token-0123456789abcdef")
	if err := registry.VerifyWorkerBoundary(context.Background(), server.URL, "wrong-token", workerControllerUID); err == nil {
		t.Fatal("worker boundary accepted a wrong bearer")
	}
	if err := registry.VerifyWorkerBoundary(context.Background(), server.URL, "worker-secret-token-0123456789abcdef", workerControllerUID); err != nil {
		t.Fatalf("worker boundary rejected the configured bearer: %v", err)
	}
}

func TestRegistryBoundaryFromEnvironmentLeavesBrowserUnavailableWithoutURL(t *testing.T) {
	registry := testRegistry(t, nil)
	if err := registry.VerifyWorkerBoundaryFromEnvironment(context.Background(), func(string) string { return "" }, workerControllerUID); err != nil {
		t.Fatalf("unset worker URL is a no-op: %v", err)
	}
	if err := registry.SetEnabled("browser", true); !errors.Is(err, mcpserver.ErrUnverifiedWorkerBoundary) {
		t.Fatalf("Browser was enableable without a worker URL: %v", err)
	}
	if ready, _ := registry.Health("browser"); ready {
		t.Fatal("Browser became ready without a worker URL")
	}
}

func TestRegistryBoundaryFromEnvironmentVerifiesConformingURL(t *testing.T) {
	registry := testRegistry(t, nil)
	server := isolationServer(t, conformingWorkerReport(), "worker-secret-token-0123456789abcdef")
	values := map[string]string{
		"PIXIE_BROWSER_WORKER_URL":   server.URL,
		"PIXIE_BROWSER_WORKER_TOKEN": "worker-secret-token-0123456789abcdef",
	}
	if err := registry.VerifyWorkerBoundaryFromEnvironment(context.Background(), func(key string) string { return values[key] }, workerControllerUID); err != nil {
		t.Fatalf("conforming environment boundary was rejected: %v", err)
	}
	if err := registry.SetEnabled("browser", true); err != nil {
		t.Fatalf("environment-verified boundary did not open Browser enablement: %v", err)
	}
}
