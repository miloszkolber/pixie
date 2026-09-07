package browser

import (
	"os"

	"github.com/miloszkolber/pixie/internal/diagnostics"
)

type readinessChecks struct {
	Executable      bool `json:"executable"`
	Config          bool `json:"config"`
	ArtifactStorage bool `json:"artifactStorage"`
	StateStorage    bool `json:"stateStorage"`
}

type readinessReport struct {
	Ready  bool            `json:"ready"`
	Checks readinessChecks `json:"checks"`
}

type statusReport struct {
	Build     diagnostics.BuildInfo       `json:"build"`
	Process   diagnostics.ProcessSnapshot `json:"process"`
	Readiness readinessReport             `json:"readiness"`
	Requests  diagnostics.RequestSnapshot `json:"requests"`
}

func (a *app) readiness() readinessReport {
	checks := readinessChecks{
		Executable:      readyFile(a.config.AgentBrowser, true),
		Config:          readyFile(a.config.BrowserConfig, false),
		ArtifactStorage: readyStorage(a.config.ArtifactRoot),
		StateStorage:    readyStorage(a.config.StateRoot),
	}
	return readinessReport{
		Ready:  checks.Executable && checks.Config && checks.ArtifactStorage && checks.StateStorage,
		Checks: checks,
	}
}

func (a *app) status() statusReport {
	return statusReport{
		Build:     a.build,
		Process:   diagnostics.Process(a.started),
		Readiness: a.readiness(),
		Requests:  a.requests.Snapshot(),
	}
}

func readyFile(path string, executable bool) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || executable && info.Mode().Perm()&0o111 == 0 {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	return file.Close() == nil
}

func readyStorage(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	temporary, err := os.CreateTemp(path, ".pixie-readiness-*")
	if err != nil {
		return false
	}
	name := temporary.Name()
	closeErr := temporary.Close()
	removeErr := os.Remove(name)
	return closeErr == nil && removeErr == nil
}
