package security_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/security"
)

func TestWorkerPolicyValidatesExplicitProfilesOnBothArchitectures(t *testing.T) {
	for _, architecture := range []security.Architecture{security.ArchitectureAMD64, security.ArchitectureARM64} {
		for _, profile := range security.SupportedHostProfiles() {
			for _, kind := range []security.WorkerKind{security.WorkerBrowser, security.WorkerCanvas, security.WorkerDesign} {
				policy := validWorkerPolicy(profile, architecture, kind)
				if err := security.ValidateWorkerPolicy(policy); err != nil {
					t.Fatalf("%s/%s/%s policy rejected: %v", profile, architecture, kind, err)
				}
			}
		}
	}
}

func TestUnsupportedHostProfilesFailClosed(t *testing.T) {
	profiles := append(security.UnsupportedHostProfiles(), security.HostProfile("unknown-profile"), security.HostProfile(""))
	for _, profile := range profiles {
		t.Run(string(profile), func(t *testing.T) {
			err := security.ValidateHostProfile(profile)
			if err == nil {
				t.Fatalf("profile %q was accepted", profile)
			}
			if !errors.Is(err, security.ErrUnsupportedHostProfile) {
				t.Fatalf("profile %q error = %v, want unsupported-profile error", profile, err)
			}
			var unsupported *security.UnsupportedHostProfileError
			if !errors.As(err, &unsupported) || unsupported.Profile != profile || unsupported.Reason == "" {
				t.Fatalf("profile %q lost explicit reason: %v", profile, err)
			}
			if profile == security.HostProfileDockerSharedUID && !strings.Contains(err.Error(), "same-UID") {
				t.Fatalf("shared-UID refusal does not explain the boundary: %v", err)
			}
		})
	}
}

func TestLauncherPlacementCannotFallBackToUncontainedExecution(t *testing.T) {
	valid := validWorkerPolicy(security.HostProfileLinuxBubblewrap, security.ArchitectureAMD64, security.WorkerCanvas)
	mutations := map[string]func(*security.WorkerPolicy){
		"unknown launcher": func(policy *security.WorkerPolicy) { policy.Placement.Launcher = "sh -c worker" },
		"wrong launcher for profile": func(policy *security.WorkerPolicy) {
			policy.Placement.Launcher = security.LauncherRestrictedWorkerService
		},
		"unverified pre-exec placement": func(policy *security.WorkerPolicy) {
			policy.Placement.VerifiedBeforeExec = false
		},
		"delegation absent": func(policy *security.WorkerPolicy) {
			policy.Placement.CgroupDelegated = false
		},
		"limits applied after exec": func(policy *security.WorkerPolicy) {
			policy.Placement.LimitsActiveBeforeExec = false
		},
		"mount namespace absent": func(policy *security.WorkerPolicy) {
			policy.Placement.Namespaces.Mount = false
		},
		"pid namespace absent": func(policy *security.WorkerPolicy) {
			policy.Placement.Namespaces.PID = false
		},
		"user namespace absent": func(policy *security.WorkerPolicy) {
			policy.Placement.Namespaces.User = false
		},
		"shared uid": func(policy *security.WorkerPolicy) {
			policy.Placement.DistinctUID = false
		},
		"writable input": func(policy *security.WorkerPolicy) {
			policy.Placement.ReadOnlyInputs = false
		},
		"shared scratch": func(policy *security.WorkerPolicy) {
			policy.Placement.PrivateScratch = false
		},
		"controller mount": func(policy *security.WorkerPolicy) {
			policy.Placement.NoControllerMounts = false
		},
		"Pi mount": func(policy *security.WorkerPolicy) {
			policy.Placement.NoPiMounts = false
		},
		"project mount": func(policy *security.WorkerPolicy) {
			policy.Placement.NoProjectMounts = false
		},
		"user runtime socket": func(policy *security.WorkerPolicy) {
			policy.Placement.NoUserRuntimeSockets = false
		},
		"Docker socket": func(policy *security.WorkerPolicy) {
			policy.Placement.NoDockerSocket = false
		},
		"writable cgroup tree": func(policy *security.WorkerPolicy) {
			policy.Placement.NoWritableCgroupTree = false
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			policy := valid
			mutate(&policy)
			if err := security.ValidateWorkerPolicy(policy); err == nil {
				t.Fatalf("unsafe launcher placement was accepted: %#v", policy.Placement)
			}
		})
	}
}

func TestUnsupportedProfilesNeverReachWorkerAdmission(t *testing.T) {
	for _, profile := range security.UnsupportedHostProfiles() {
		policy := validWorkerPolicy(security.HostProfileLinuxBubblewrap, security.ArchitectureAMD64, security.WorkerCanvas)
		policy.Profile = profile
		if err := security.ValidateWorkerPolicy(policy); !errors.Is(err, security.ErrUnsupportedHostProfile) {
			t.Fatalf("profile %q admission error = %v, want explicit unsupported profile", profile, err)
		}
	}
}

func validWorkerPolicy(profile security.HostProfile, architecture security.Architecture, kind security.WorkerKind) security.WorkerPolicy {
	launcher := security.LauncherBubblewrap
	if profile == security.HostProfileDockerRestrictedWorker {
		launcher = security.LauncherRestrictedWorkerService
	}
	egress := security.EgressPolicy{Mode: security.EgressDenied}
	if kind == security.WorkerBrowser {
		egress = security.EgressPolicy{Mode: security.EgressBrowserDeclared, Destinations: []string{"https://example.test"}}
	}
	scratchBytes := security.CanvasScratchMaxBytes
	if kind == security.WorkerDesign {
		scratchBytes = security.DesignScratchMaxBytes
	}
	return security.WorkerPolicy{
		Profile:      profile,
		Architecture: architecture,
		Kind:         kind,
		Placement: security.ResourcePlacement{
			Launcher: launcher, VerifiedBeforeExec: true,
			Namespaces:      security.NamespacePolicy{Mount: true, PID: true, User: true},
			CgroupDelegated: true, LimitsActiveBeforeExec: true, DistinctUID: true,
			ReadOnlyInputs: true, PrivateScratch: true,
			NoControllerMounts: true, NoPiMounts: true, NoProjectMounts: true,
			NoUserRuntimeSockets: true, NoDockerSocket: true, NoWritableCgroupTree: true,
		},
		Scratch: security.ScratchPolicy{
			MaxBytes: scratchBytes, MaxInodes: security.ScratchMaxInodes,
			Private: true, ReserveBeforeExec: true,
		},
		Egress: egress,
		Descriptors: security.DescriptorPolicy{
			Allowed: []int{3}, CloseUnlisted: true, VerifiedBeforeExec: true,
		},
		Descendants: security.DescendantPolicy{
			ProcessGroup: true, KillProcessGroup: true, ReapDescendants: true,
			BoundedDrain: true, CleanupVerified: true, DrainTimeout: 2 * time.Second,
		},
	}
}
