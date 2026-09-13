package browser

import (
	"crypto/subtle"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Worker is the loopback HTTP boundary around the untrusted browser service.
// It exposes only a fact report and the bounded browser operation surface; it
// never accepts an arbitrary command. The worker is expected to run under a
// dedicated service identity, filesystem and network policy (see
// package/systemd/pixie-browser-worker.service); the report is how a
// controller checks those facts instead of trusting configuration.
type Worker struct {
	service *Service
	version string
	token   string
	cleanup CleanupFacts
}

// NewWorker wraps an initialized browser service. token is the optional
// bearer required for every worker API call; when empty the loopback endpoint
// is unauthenticated and must not be exposed beyond the host.
func NewWorker(service *Service, version, token string) (*Worker, error) {
	if service == nil {
		return nil, fmt.Errorf("browser worker requires an initialized service")
	}
	return &Worker{
		service: service,
		version: strings.TrimSpace(version),
		token:   token,
		// The wrapped service performs a stale temp/lock sweep during
		// construction and owns bounded per-session cleanup; a constructed
		// worker therefore reports those facts rather than asserting them
		// from configuration.
		cleanup: CleanupFacts{BoundedStop: true, SessionCleanup: true, StaleTempSweep: true},
	}, nil
}

// ServeHTTP serves GET /isolation and POST /run on the worker listener.
func (w *Worker) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	parsed, err := parseRequestURL(request)
	if err != nil {
		respondError(response, err)
		return
	}
	switch parsed.EscapedPath() {
	case "/isolation":
		if request.Method != http.MethodGet {
			rejectMethod(response, request, http.MethodGet)
			return
		}
		if !w.authorized(request) {
			_, _ = io.Copy(io.Discard, request.Body)
			writeJSON(response, http.StatusUnauthorized, map[string]any{"outcome": "rejected", "code": "unauthorized"}, nil)
			return
		}
		report := ProbeIsolation(w.version)
		report.Cleanup = w.cleanup
		writeJSON(response, http.StatusOK, report, nil)
	case "/run":
		if request.Method != http.MethodPost {
			rejectMethod(response, request, http.MethodPost)
			return
		}
		if !w.authorized(request) {
			_, _ = io.Copy(io.Discard, request.Body)
			writeJSON(response, http.StatusUnauthorized, map[string]any{"outcome": "rejected", "code": "unauthorized"}, nil)
			return
		}
		w.forwardRun(response, request)
	default:
		// Every other path is the existing bounded Browser service surface
		// (/mcp, /v1/browser, /status, artifacts). The worker exists so that
		// surface runs under the containment boundary instead of the
		// controller's own identity; it never widens the service contract.
		w.service.ServeHTTP(response, request)
	}
}

// forwardRun maps the worker operation surface onto the service's existing
// /v1/browser endpoint. Validation, quotas and cancellation stay owned by the
// service, so the worker cannot become a general command executor.
func (w *Worker) forwardRun(response http.ResponseWriter, request *http.Request) {
	clone := request.Clone(request.Context())
	urlCopy := *request.URL
	urlCopy.Path, urlCopy.RawPath = "/v1/browser", ""
	clone.URL = &urlCopy
	clone.RequestURI = "/v1/browser"
	if request.URL.RawQuery != "" {
		clone.RequestURI += "?" + request.URL.RawQuery
	}
	w.service.ServeHTTP(response, clone)
}

func (w *Worker) authorized(request *http.Request) bool {
	if w.token == "" {
		return true
	}
	values := request.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return false
	}
	expected := []byte(w.token)
	supplied := []byte(strings.TrimPrefix(values[0], "Bearer "))
	return len(expected) > 0 && len(expected) == len(supplied) && subtle.ConstantTimeCompare(expected, supplied) == 1
}
