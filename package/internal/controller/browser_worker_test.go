package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/miloszkolber/pixie/internal/browser"
	"github.com/miloszkolber/pixie/internal/mcpserver"
	"github.com/miloszkolber/pixie/internal/workspace"
)

func workerBoundaryReport() browser.IsolationReport {
	return browser.IsolationReport{
		WorkerVersion: "test-worker",
		UID:           1414,
		GID:           1414,
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

func workerBoundaryServer(t *testing.T, report browser.IsolationReport) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/isolation" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(report)
	}))
	t.Cleanup(server.Close)
	return server
}

func newWorkerBoundaryRuntime(t *testing.T, values map[string]string) *Runtime {
	t.Helper()
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err := workspace.NewPathPolicy([]string{t.TempDir()}, false)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(RuntimeConfig{
		Host: "127.0.0.1", Port: port, DataDir: t.TempDir(), StaticDir: staticDir, Policy: policy,
		Getenv: func(key string) string { return values[key] },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	return runtime
}

func TestRuntimeVerifiesConformingBrowserWorkerBoundary(t *testing.T) {
	server := workerBoundaryServer(t, workerBoundaryReport())
	runtime := newWorkerBoundaryRuntime(t, map[string]string{"PIXIE_BROWSER_WORKER_URL": server.URL})
	// SetEnabled persists and reconciles; a nil result proves the startup
	// probe opened the boundary gate. The default Browser binary paths are
	// absent in this test, so local readiness is expected to remain false.
	if err := runtime.registry.SetEnabled("browser", true); err != nil {
		t.Fatalf("controller did not verify the conforming worker boundary: %v", err)
	}
}

func TestRuntimeRefusesUnprovenBrowserWorkerBoundary(t *testing.T) {
	withoutURL := newWorkerBoundaryRuntime(t, nil)
	if err := withoutURL.registry.SetEnabled("browser", true); !errors.Is(err, mcpserver.ErrUnverifiedWorkerBoundary) {
		t.Fatalf("Browser was enableable without a worker URL: %v", err)
	}
	if ready, _ := withoutURL.registry.Health("browser"); ready {
		t.Fatal("Browser became ready without a worker URL")
	}

	nonConforming := workerBoundaryReport()
	nonConforming.Network.EgressRestricted = false
	server := workerBoundaryServer(t, nonConforming)
	withBadReport := newWorkerBoundaryRuntime(t, map[string]string{"PIXIE_BROWSER_WORKER_URL": server.URL})
	if err := withBadReport.registry.SetEnabled("browser", true); !errors.Is(err, mcpserver.ErrUnverifiedWorkerBoundary) {
		t.Fatalf("Browser was enableable with a non-conforming report: %v", err)
	}
	if ready, _ := withBadReport.registry.Health("browser"); ready {
		t.Fatal("Browser became ready with a non-conforming report")
	}
}
