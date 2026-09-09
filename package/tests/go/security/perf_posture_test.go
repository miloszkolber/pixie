package security_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/security"
)

// PERF-01 is deliberately a fixture contract, not a claim about a deployed
// binary. The checker records repeated full-process runs on both architectures
// and keeps decoded image/UI memory separate from serialized buffer memory.
type performanceFixture struct {
	Architecture       security.Architecture
	Samples            int
	P50                time.Duration
	P95                time.Duration
	WorkerMemoryBytes  int64
	PIDs               int64
	CPUQuotaMillis     int64
	WallTime           time.Duration
	OutputBytes        int64
	DecodedMemoryBytes int64
	BufferMemoryBytes  int64
	FullProcess        bool
	ContentFilledUI    bool
	LiveDeployment     bool
}

const (
	minimumPerformanceSamples = 5
	maxPerformanceP95         = 2 * time.Second
	maxWorkerMemoryBytes      = 1 << 30
	maxWorkerPIDs             = 256
	maxWorkerCPUQuotaMillis   = 30_000
	maxWorkerWallTime         = 30 * time.Second
	maxWorkerOutputBytes      = 64 << 20
	maxDecodedMemoryBytes     = 256 << 20
	maxBufferMemoryBytes      = 64 << 20
)

func validatePerformanceFixture(fixture performanceFixture) error {
	if err := security.ValidateArchitecture(fixture.Architecture); err != nil {
		return err
	}
	if fixture.Samples < minimumPerformanceSamples {
		return fmt.Errorf("PERF-01 needs at least %d repeated samples", minimumPerformanceSamples)
	}
	if fixture.P50 <= 0 || fixture.P95 < fixture.P50 || fixture.P95 > maxPerformanceP95 {
		return fmt.Errorf("PERF-01 has an invalid p50/p95 budget")
	}
	if fixture.WorkerMemoryBytes <= 0 || fixture.WorkerMemoryBytes > maxWorkerMemoryBytes {
		return fmt.Errorf("PERF-01 worker memory budget is invalid")
	}
	if fixture.PIDs <= 0 || fixture.PIDs > maxWorkerPIDs || fixture.CPUQuotaMillis <= 0 || fixture.CPUQuotaMillis > maxWorkerCPUQuotaMillis {
		return fmt.Errorf("PERF-01 worker CPU/PID budget is invalid")
	}
	if fixture.WallTime <= 0 || fixture.WallTime > maxWorkerWallTime || fixture.OutputBytes <= 0 || fixture.OutputBytes > maxWorkerOutputBytes {
		return fmt.Errorf("PERF-01 worker wall/output budget is invalid")
	}
	if fixture.DecodedMemoryBytes <= 0 || fixture.DecodedMemoryBytes > maxDecodedMemoryBytes {
		return fmt.Errorf("PERF-01 decoded/buffer memory budget is invalid")
	}
	if fixture.BufferMemoryBytes <= 0 || fixture.BufferMemoryBytes > maxBufferMemoryBytes {
		return fmt.Errorf("PERF-01 serialized buffer memory budget is invalid")
	}
	if !fixture.FullProcess {
		return fmt.Errorf("PERF-01 must measure the complete process tree")
	}
	if !fixture.ContentFilledUI {
		return fmt.Errorf("PERF-01 must use content-filled UI fixtures")
	}
	if !fixture.LiveDeployment {
		return fmt.Errorf("PERF-01 live deployment evidence is missing")
	}
	return nil
}

// SEC-02/X12/X13: resource placement, scratch/inodes and descendant cleanup
// are exercised for every supported worker profile on both architectures.
func TestSecurityResourceAndDescendantFixturesCoverBothArchitectures(t *testing.T) {
	for _, architecture := range []security.Architecture{security.ArchitectureAMD64, security.ArchitectureARM64} {
		for _, profile := range security.SupportedHostProfiles() {
			for _, kind := range []security.WorkerKind{security.WorkerBrowser, security.WorkerCanvas, security.WorkerDesign} {
				policy := validWorkerPolicy(profile, architecture, kind)
				if err := security.ValidateWorkerPolicy(policy); err != nil {
					t.Fatalf("valid SEC-02 fixture %s/%s/%s rejected: %v", architecture, profile, kind, err)
				}
				if policy.Scratch.MaxInodes != security.ScratchMaxInodes || !policy.Scratch.ReserveBeforeExec {
					t.Fatalf("%s/%s scratch reservation is not bounded before exec: %#v", architecture, kind, policy.Scratch)
				}
				if policy.Descendants.DrainTimeout <= 0 || policy.Descendants.DrainTimeout > 30*time.Second {
					t.Fatalf("%s/%s descendant drain is not bounded: %#v", architecture, kind, policy.Descendants)
				}
			}
		}
	}
}

func TestPerformanceFixtureRequiresRepeatedFullProcessAndMemoryEvidence(t *testing.T) {
	fixtures := []performanceFixture{
		{Architecture: security.ArchitectureAMD64, Samples: 9, P50: 120 * time.Millisecond, P95: 420 * time.Millisecond, WorkerMemoryBytes: 512 << 20, PIDs: 128, CPUQuotaMillis: 20_000, WallTime: 30 * time.Second, OutputBytes: 32 << 20, DecodedMemoryBytes: 96 << 20, BufferMemoryBytes: 12 << 20, FullProcess: true, ContentFilledUI: true},
		{Architecture: security.ArchitectureARM64, Samples: 9, P50: 150 * time.Millisecond, P95: 500 * time.Millisecond, WorkerMemoryBytes: 512 << 20, PIDs: 128, CPUQuotaMillis: 20_000, WallTime: 30 * time.Second, OutputBytes: 32 << 20, DecodedMemoryBytes: 96 << 20, BufferMemoryBytes: 12 << 20, FullProcess: true, ContentFilledUI: true},
	}
	for _, fixture := range fixtures {
		if err := validatePerformanceFixture(fixture); err == nil {
			t.Fatalf("%s fixture without live deployment evidence was accepted", fixture.Architecture)
		}
		fixture.LiveDeployment = true
		if err := validatePerformanceFixture(fixture); err != nil {
			t.Fatalf("complete %s performance fixture rejected: %v", fixture.Architecture, err)
		}
	}
}

func TestPerformanceFixtureRejectsUnboundedOrPartialMeasurements(t *testing.T) {
	valid := performanceFixture{
		Architecture: security.ArchitectureAMD64, Samples: 8,
		P50: 100 * time.Millisecond, P95: 300 * time.Millisecond,
		WorkerMemoryBytes: 512 << 20, PIDs: 128, CPUQuotaMillis: 20_000,
		WallTime: 30 * time.Second, OutputBytes: 32 << 20,
		DecodedMemoryBytes: 64 << 20, BufferMemoryBytes: 8 << 20,
		FullProcess: true, ContentFilledUI: true, LiveDeployment: true,
	}
	mutations := map[string]func(*performanceFixture){
		"single sample":             func(fixture *performanceFixture) { fixture.Samples = 1 },
		"p95 over budget":           func(fixture *performanceFixture) { fixture.P95 = 3 * time.Second },
		"worker memory over budget": func(fixture *performanceFixture) { fixture.WorkerMemoryBytes = (1 << 30) + 1 },
		"PID budget omitted":        func(fixture *performanceFixture) { fixture.PIDs = 0 },
		"CPU budget omitted":        func(fixture *performanceFixture) { fixture.CPUQuotaMillis = 0 },
		"wall budget omitted":       func(fixture *performanceFixture) { fixture.WallTime = 0 },
		"output budget omitted":     func(fixture *performanceFixture) { fixture.OutputBytes = 0 },
		"decoded memory omitted":    func(fixture *performanceFixture) { fixture.DecodedMemoryBytes = 0 },
		"buffer memory omitted":     func(fixture *performanceFixture) { fixture.BufferMemoryBytes = 0 },
		"main pid only":             func(fixture *performanceFixture) { fixture.FullProcess = false },
		"empty UI":                  func(fixture *performanceFixture) { fixture.ContentFilledUI = false },
		"arm64 missing":             func(fixture *performanceFixture) { fixture.Architecture = security.Architecture("arm64e") },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			fixture := valid
			mutate(&fixture)
			if err := validatePerformanceFixture(fixture); err == nil {
				t.Fatalf("unsafe PERF-01 fixture was accepted: %#v", fixture)
			}
		})
	}
}
