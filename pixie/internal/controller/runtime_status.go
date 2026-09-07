package controller

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/workspace"
)

const (
	runtimeStatusTimeout = 2 * time.Second
	maxRuntimeStatusBody = 64 * 1024
)

type runtimeServiceStatus struct {
	State    string                       `json:"state"`
	Build    *diagnostics.BuildInfo       `json:"build,omitempty"`
	Requests *diagnostics.RequestSnapshot `json:"requests,omitempty"`
	Process  *diagnostics.ProcessSnapshot `json:"process,omitempty"`
	Detail   string                       `json:"detail,omitempty"`
}

type runtimeAgentStatus struct {
	State   string `json:"state"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

type runtimeStatusReport struct {
	Application runtimeServiceStatus `json:"application"`
	Agent       runtimeAgentStatus   `json:"agent"`
	Browser     runtimeServiceStatus `json:"browser"`
}

type browserReadiness struct {
	Ready  bool `json:"ready"`
	Checks struct {
		Executable      bool `json:"executable"`
		Config          bool `json:"config"`
		ArtifactStorage bool `json:"artifactStorage"`
		StateStorage    bool `json:"stateStorage"`
	} `json:"checks"`
}

type browserRuntimeStatus struct {
	Build     diagnostics.BuildInfo       `json:"build"`
	Process   diagnostics.ProcessSnapshot `json:"process"`
	Readiness browserReadiness            `json:"readiness"`
	Requests  diagnostics.RequestSnapshot `json:"requests"`
}

type runtimeStatusProvider struct {
	build         diagnostics.BuildInfo
	started       time.Time
	requests      *diagnostics.RequestCounter
	projects      *workspace.Projects
	settings      *Settings
	staticDir     string
	agent         *PiClient
	auth          AuthConfig
	browserClient *http.Client
}

func newRuntimeStatusProvider(build diagnostics.BuildInfo, requests *diagnostics.RequestCounter, projects *workspace.Projects, settings *Settings, staticDir string, agent *PiClient, auth AuthConfig) *runtimeStatusProvider {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &runtimeStatusProvider{
		build: build, started: time.Now(), requests: requests, projects: projects, settings: settings,
		staticDir: staticDir, agent: agent, auth: auth,
		browserClient: &http.Client{
			Transport: transport,
			Timeout:   runtimeStatusTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (s *runtimeStatusProvider) snapshot(ctx context.Context) runtimeStatusReport {
	bounded, cancel := context.WithTimeout(ctx, runtimeStatusTimeout)
	defer cancel()
	agentResult := make(chan runtimeAgentStatus, 1)
	browserResult := make(chan runtimeServiceStatus, 1)
	go func() { agentResult <- projectAgentStatus(runtimePiStatus(bounded, s.agent)) }()
	go func() { browserResult <- s.browserStatus(bounded) }()
	report := runtimeStatusReport{
		Application: s.applicationStatus(),
		Agent:       runtimeAgentStatus{State: "unavailable", Detail: "Agent service is unavailable."},
		Browser:     unavailableBrowser("Browser service is unavailable."),
	}
	for pending := 2; pending > 0; {
		select {
		case report.Agent = <-agentResult:
			agentResult = nil
			pending--
		case report.Browser = <-browserResult:
			browserResult = nil
			pending--
		case <-bounded.Done():
			return report
		}
	}
	return report
}

func (s *runtimeStatusProvider) applicationStatus() runtimeServiceStatus {
	state, detail := "ready", ""
	if ready, reason := s.localReady(); !ready {
		state, detail = "degraded", reason
	}
	build, requests, process := s.build, s.requests.Snapshot(), diagnostics.Process(s.started)
	return runtimeServiceStatus{State: state, Build: &build, Requests: &requests, Process: &process, Detail: detail}
}

func (s *runtimeStatusProvider) localReady() (bool, string) {
	if s.projects == nil || s.settings == nil {
		return false, "Application state is unavailable."
	}
	if _, err := s.projects.List(true); err != nil {
		return false, "Application state is unavailable."
	}
	if _, err := s.settings.Get(); err != nil {
		return false, "Application state is unavailable."
	}
	if !regularFile(filepath.Join(s.staticDir, "index.html")) {
		return false, "Application interface is unavailable."
	}
	return true, ""
}

func projectAgentStatus(status map[string]any) runtimeAgentStatus {
	configured, _ := status["configured"].(bool)
	reachable, _ := status["reachable"].(bool)
	if !configured {
		return runtimeAgentStatus{State: "unavailable", Detail: "Agent connection is not configured."}
	}
	if !reachable {
		return runtimeAgentStatus{State: "unavailable", Detail: "Agent service is unavailable."}
	}
	profile, ok := status["agentProfile"].(AgentProfile)
	if !ok {
		return runtimeAgentStatus{State: "degraded", Detail: "Agent capabilities are unavailable."}
	}
	result := runtimeAgentStatus{State: "ready", Name: profile.Name, Version: profile.Version}
	if !profile.Compatible {
		result.State = "degraded"
		result.Detail = "Agent is missing required capabilities."
	}
	return result
}

func (s *runtimeStatusProvider) browserStatus(ctx context.Context) runtimeServiceStatus {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.auth.BrowserURL+"/status", nil)
	if err != nil {
		return unavailableBrowser("Browser status is unavailable.")
	}
	if browserAuth, browserToken := s.auth.BrowserServiceAuth(); browserAuth {
		request.Header.Set("Authorization", "Bearer "+browserToken)
	}
	response, err := s.browserClient.Do(request)
	if err != nil {
		return unavailableBrowser("Browser service is unavailable.")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return unavailableBrowser("Browser authentication failed.")
	}
	if response.StatusCode != http.StatusOK {
		return unavailableBrowser("Browser service is unavailable.")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxRuntimeStatusBody+1))
	if err != nil || len(body) > maxRuntimeStatusBody {
		return unavailableBrowser("Browser status is unavailable.")
	}
	var status browserRuntimeStatus
	if json.Unmarshal(body, &status) != nil || status.Build.Version == "" || status.Build.Revision == "" {
		return unavailableBrowser("Browser status is unavailable.")
	}
	build := diagnostics.NormalizeBuild(status.Build.Version, status.Build.Revision)
	state, detail := "ready", ""
	checks := status.Readiness.Checks
	if !status.Readiness.Ready || !checks.Executable || !checks.Config || !checks.ArtifactStorage || !checks.StateStorage {
		state, detail = "degraded", "Browser service is not ready."
	}
	return runtimeServiceStatus{State: state, Build: &build, Requests: &status.Requests, Process: &status.Process, Detail: detail}
}

func unavailableBrowser(detail string) runtimeServiceStatus {
	return runtimeServiceStatus{State: "unavailable", Detail: detail}
}

func (s *runtimeStatusProvider) close() {
	s.browserClient.CloseIdleConnections()
}
