package controller

import (
	"context"
	"time"

	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/mcpserver"
	"github.com/miloszkolber/pixie/internal/workspace"
)

const runtimeStatusTimeout = 2 * time.Second

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
	Canvas      runtimeServiceStatus `json:"canvas"`
	Design      runtimeServiceStatus `json:"design"`
}

type runtimeStatusProvider struct {
	schedules *Schedules
	build     diagnostics.BuildInfo
	started   time.Time
	requests  *diagnostics.RequestCounter
	projects  *workspace.Projects
	settings  *Settings
	static    staticFiles
	agent     *PiClient
	auth      AuthConfig
	registry  *mcpserver.Registry
}

func newRuntimeStatusProvider(build diagnostics.BuildInfo, requests *diagnostics.RequestCounter, projects *workspace.Projects, settings *Settings, staticDir string, agent *PiClient, auth AuthConfig, registries ...*mcpserver.Registry) *runtimeStatusProvider {
	var registry *mcpserver.Registry
	if len(registries) > 0 {
		registry = registries[0]
	}
	return &runtimeStatusProvider{
		build: build, started: time.Now(), requests: requests, projects: projects, settings: settings,
		static: resolveStaticFiles(staticDir), agent: agent, auth: auth, registry: registry,
	}
}

func (s *runtimeStatusProvider) snapshot(ctx context.Context) runtimeStatusReport {
	bounded, cancel := context.WithTimeout(ctx, runtimeStatusTimeout)
	defer cancel()
	agentResult := make(chan runtimeAgentStatus, 1)
	go func() { agentResult <- projectAgentStatus(runtimePiStatus(bounded, s.agent)) }()
	report := runtimeStatusReport{
		Application: s.applicationStatus(),
		Agent:       runtimeAgentStatus{State: "unavailable", Detail: "Agent service is unavailable."},
		Canvas:      s.moduleStatus("canvas", "Canvas"),
		Design:      s.moduleStatus("design", "Design"),
	}
	select {
	case report.Agent = <-agentResult:
	case <-bounded.Done():
	}
	return report
}

func (s *runtimeStatusProvider) moduleStatus(id, displayName string) runtimeServiceStatus {
	if s.registry == nil {
		return runtimeServiceStatus{State: "unavailable", Detail: displayName + " service is unavailable."}
	}
	snapshot := s.registry.ModuleLifecycle(id)
	if snapshot.Ready {
		return runtimeServiceStatus{State: "ready"}
	}
	if snapshot.Detail == "" {
		snapshot.Detail = displayName + " service is unavailable."
	}
	return runtimeServiceStatus{State: "unavailable", Detail: snapshot.Detail}
}

func (s *runtimeStatusProvider) applicationStatus() runtimeServiceStatus {
	state, detail := "ready", ""
	if s.schedules != nil {
		if issue := s.schedules.Health(); issue != "" {
			state, detail = "degraded", issue
		}
	}
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
	if _, ok := s.static.stat("index.html"); !ok {
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
