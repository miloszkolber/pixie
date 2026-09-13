package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/miloszkolber/pixie/internal/browser"
)

const (
	workerBoundaryProbeTimeout = 4 * time.Second
	maxIsolationResponseBytes  = 64 * 1024
)

// VerifyWorkerBoundary probes a browser worker's /isolation endpoint and
// records the result on the registry. It calls SetWorkerBoundaryVerified(true)
// only when the returned report proves every required containment fact;
// otherwise it explicitly fails closed with false, so a probe error, a
// non-conforming report or a missing endpoint leaves the untrusted Browser
// module unavailable. The returned error names the failed requirement.
//
// controllerUID is the controller process's effective uid. The worker must
// report a different, non-root uid; the worker cannot know the controller's
// identity, so distinctness is established here rather than self-asserted.
func (r *Registry) VerifyWorkerBoundary(ctx context.Context, workerURL, token string, controllerUID int) error {
	if r == nil {
		return fmt.Errorf("registry is not configured")
	}
	parsed, err := url.Parse(strings.TrimSpace(workerURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		r.SetWorkerBoundaryVerified(false)
		return fmt.Errorf("browser worker URL must be one http(s) origin without credentials")
	}
	endpoint := strings.TrimRight(parsed.String(), "/") + "/isolation"
	probeContext, cancel := context.WithTimeout(ctx, workerBoundaryProbeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(probeContext, http.MethodGet, endpoint, nil)
	if err != nil {
		r.SetWorkerBoundaryVerified(false)
		return fmt.Errorf("build browser worker isolation probe: %w", err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{
		// A boundary report must come from the configured origin; a redirect
		// could silently move the probe to a different authority.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		r.SetWorkerBoundaryVerified(false)
		return fmt.Errorf("probe browser worker isolation: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		r.SetWorkerBoundaryVerified(false)
		return fmt.Errorf("browser worker isolation probe returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxIsolationResponseBytes+1))
	if err != nil {
		r.SetWorkerBoundaryVerified(false)
		return fmt.Errorf("read browser worker isolation report: %w", err)
	}
	if len(body) > maxIsolationResponseBytes {
		r.SetWorkerBoundaryVerified(false)
		return fmt.Errorf("browser worker isolation report exceeds %d bytes", maxIsolationResponseBytes)
	}
	var report browser.IsolationReport
	if err := json.Unmarshal(body, &report); err != nil {
		r.SetWorkerBoundaryVerified(false)
		return fmt.Errorf("decode browser worker isolation report: %w", err)
	}
	if err := browser.VerifyIsolationReport(report, controllerUID); err != nil {
		r.SetWorkerBoundaryVerified(false)
		return err
	}
	r.SetWorkerBoundaryVerified(true)
	return nil
}

// VerifyWorkerBoundaryFromEnvironment reads PIXIE_BROWSER_WORKER_URL and its
// optional PIXIE_BROWSER_WORKER_TOKEN and applies the probe. An unset URL is a
// no-op that leaves the boundary unverified, so Browser stays unavailable.
func (r *Registry) VerifyWorkerBoundaryFromEnvironment(ctx context.Context, getenv func(string) string, controllerUID int) error {
	if getenv == nil {
		return nil
	}
	workerURL := strings.TrimSpace(getenv("PIXIE_BROWSER_WORKER_URL"))
	if workerURL == "" {
		return nil
	}
	token := strings.TrimSpace(getenv("PIXIE_BROWSER_WORKER_TOKEN"))
	return r.VerifyWorkerBoundary(ctx, workerURL, token, controllerUID)
}
