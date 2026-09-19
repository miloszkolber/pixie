package security_test

import (
	"errors"
	"testing"

	"github.com/miloszkolber/pixie/internal/security"
)

// This is an admission fixture, not a host probe.  A real launcher must supply
// the same attestation before it starts untrusted input; this test makes the
// fail-closed decision explicit for both declared launcher topologies.
func TestLauncherDelegationAdmissionHasNoPermissiveFallback(t *testing.T) {
	for _, architecture := range []security.Architecture{security.ArchitectureAMD64, security.ArchitectureARM64} {
		for _, profile := range security.SupportedHostProfiles() {
			t.Run(string(architecture)+"/"+string(profile), func(t *testing.T) {
				policy := validWorkerPolicy(profile, architecture, security.WorkerCanvas)
				if err := security.ValidateWorkerPolicy(policy); err != nil {
					t.Fatalf("valid launcher/delegation fixture rejected: %v", err)
				}

				mutations := []struct {
					name   string
					mutate func(*security.WorkerPolicy)
				}{
					{name: "delegation denied", mutate: func(value *security.WorkerPolicy) { value.Placement.CgroupDelegated = false }},
					{name: "placement not verified", mutate: func(value *security.WorkerPolicy) { value.Placement.VerifiedBeforeExec = false }},
					{name: "limits applied after exec", mutate: func(value *security.WorkerPolicy) { value.Placement.LimitsActiveBeforeExec = false }},
					{name: "launcher changed", mutate: func(value *security.WorkerPolicy) {
						value.Placement.Launcher = security.LauncherBubblewrap + " --unshare-all"
					}},
				}
				for _, mutation := range mutations {
					t.Run(mutation.name, func(t *testing.T) {
						candidate := policy
						mutation.mutate(&candidate)
						err := security.ValidateWorkerPolicy(candidate)
						if err == nil {
							t.Fatal("worker would be admitted without verified launcher/delegation placement")
						}
						if !errors.Is(err, security.ErrInvalidPolicy) {
							t.Fatalf("admission error = %v, want invalid policy", err)
						}
					})
				}
			})
		}
	}
}

func TestLauncherProfileCannotBorrowAnotherProfileDelegation(t *testing.T) {
	for _, architecture := range []security.Architecture{security.ArchitectureAMD64, security.ArchitectureARM64} {
		bubblewrap := validWorkerPolicy(security.HostProfileLinuxBubblewrap, architecture, security.WorkerDesign)
		bubblewrap.Profile = security.HostProfileDockerRestrictedWorker
		if err := security.ValidateWorkerPolicy(bubblewrap); err == nil {
			t.Fatal("Docker worker accepted a bubblewrap launcher")
		}

		docker := validWorkerPolicy(security.HostProfileDockerRestrictedWorker, architecture, security.WorkerDesign)
		docker.Profile = security.HostProfileLinuxBubblewrap
		if err := security.ValidateWorkerPolicy(docker); err == nil {
			t.Fatal("Linux worker accepted a restricted-service launcher")
		}
	}
}
