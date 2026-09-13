package browser_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/browser"
)

func newTestWorker(t *testing.T, runtime *testRuntime) *browser.Worker {
	t.Helper()
	worker, err := browser.NewWorker(runtime.service, "2.3.4", testToken)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func TestWorkerIsolationReportsTheWorkerFacts(t *testing.T) {
	runtime := newTestRuntime(t, true, nil)
	worker := newTestWorker(t, runtime)
	request := httptest.NewRequest(http.MethodGet, "/isolation", nil)
	request.Header.Set("Authorization", "Bearer "+testToken)
	response := httptest.NewRecorder()
	worker.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("isolation status = %d body = %s", response.Code, response.Body.String())
	}
	var report browser.IsolationReport
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.WorkerVersion != "2.3.4" {
		t.Fatalf("worker version = %q", report.WorkerVersion)
	}
	if report.UID != os.Geteuid() || report.GID != os.Getegid() {
		t.Fatalf("worker uid/gid = %d/%d, want %d/%d", report.UID, report.GID, os.Geteuid(), os.Getegid())
	}
	if !report.Cleanup.BoundedStop || !report.Cleanup.SessionCleanup || !report.Cleanup.StaleTempSweep {
		t.Fatalf("worker cleanup facts = %#v", report.Cleanup)
	}
}

func TestWorkerIsolationRequiresToken(t *testing.T) {
	runtime := newTestRuntime(t, true, nil)
	worker := newTestWorker(t, runtime)
	response := httptest.NewRecorder()
	worker.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/isolation", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated isolation status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestWorkerRunDelegatesOnlyBoundedBrowserOperations(t *testing.T) {
	runtime := newTestRuntime(t, true, nil)
	worker := newTestWorker(t, runtime)
	run := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/run", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+testToken)
		response := httptest.NewRecorder()
		worker.ServeHTTP(response, request)
		return response
	}
	response := run(`{"session":"worker-1","command":"snapshot","args":[]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("run snapshot status = %d body = %s", response.Code, response.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["outcome"] != "completed" || result["command"] != "snapshot" {
		t.Fatalf("run snapshot result = %#v", result)
	}
	// The worker surface has no arbitrary command executor: an unknown
	// operation is rejected by the service's bounded command policy.
	rejected := run(`{"session":"worker-1","command":"exec","args":["/bin/sh"]}`)
	if rejected.Code != http.StatusBadRequest {
		t.Fatalf("arbitrary command status = %d body = %s", rejected.Code, rejected.Body.String())
	}
	// GET /run is not a read surface.
	get := httptest.NewRecorder()
	worker.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/run", nil))
	if get.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /run status = %d body = %s", get.Code, get.Body.String())
	}
}
