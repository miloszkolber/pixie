package security_test

import (
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/security"
)

func TestScratchAndInodeBoundsFailClosed(t *testing.T) {
	valid := security.ScratchPolicy{
		MaxBytes: security.CanvasScratchMaxBytes, MaxInodes: security.ScratchMaxInodes,
		Private: true, ReserveBeforeExec: true,
	}
	if err := security.ValidateScratchPolicy(valid); err != nil {
		t.Fatalf("valid scratch policy rejected: %v", err)
	}
	mutations := map[string]func(*security.ScratchPolicy){
		"zero bytes":       func(policy *security.ScratchPolicy) { policy.MaxBytes = 0 },
		"negative bytes":   func(policy *security.ScratchPolicy) { policy.MaxBytes = -1 },
		"design overflow":  func(policy *security.ScratchPolicy) { policy.MaxBytes = security.DesignScratchMaxBytes + 1 },
		"zero inodes":      func(policy *security.ScratchPolicy) { policy.MaxInodes = 0 },
		"inode overflow":   func(policy *security.ScratchPolicy) { policy.MaxInodes = security.ScratchMaxInodes + 1 },
		"shared scratch":   func(policy *security.ScratchPolicy) { policy.Private = false },
		"late reservation": func(policy *security.ScratchPolicy) { policy.ReserveBeforeExec = false },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			policy := valid
			mutate(&policy)
			if err := security.ValidateScratchPolicy(policy); err == nil {
				t.Fatalf("unsafe scratch policy was accepted: %#v", policy)
			}
		})
	}
}

func TestWorkerSpecificScratchBounds(t *testing.T) {
	canvas := validWorkerPolicy(security.HostProfileLinuxBubblewrap, security.ArchitectureAMD64, security.WorkerCanvas)
	if err := security.ValidateWorkerPolicy(canvas); err != nil {
		t.Fatalf("valid Canvas policy rejected: %v", err)
	}
	canvas.Scratch.MaxBytes = security.CanvasScratchMaxBytes + 1
	if err := security.ValidateWorkerPolicy(canvas); err == nil {
		t.Fatal("Canvas policy accepted scratch above its worker bound")
	}
	design := validWorkerPolicy(security.HostProfileLinuxBubblewrap, security.ArchitectureARM64, security.WorkerDesign)
	if err := security.ValidateWorkerPolicy(design); err != nil {
		t.Fatalf("valid Design policy rejected: %v", err)
	}
}

func TestEgressPolicyRequiresExplicitDeclaredDestinations(t *testing.T) {
	if err := security.ValidateEgressPolicy(security.EgressPolicy{Mode: security.EgressDenied}); err != nil {
		t.Fatalf("egress denial rejected: %v", err)
	}
	if err := security.ValidateEgressPolicy(security.EgressPolicy{
		Mode: security.EgressBrowserDeclared, Destinations: []string{"https://example.test", "http://127.0.0.1:7312"},
	}); err != nil {
		t.Fatalf("declared browser egress rejected: %v", err)
	}
	unsafe := []security.EgressPolicy{
		{Mode: security.EgressUnrestricted},
		{Mode: security.EgressBrowserDeclared},
		{Mode: security.EgressBrowserDeclared, Destinations: []string{"*"}},
		{Mode: security.EgressBrowserDeclared, Destinations: []string{"https://example.test", "https://example.test"}},
		{Mode: security.EgressBrowserDeclared, Destinations: []string{"file:///etc/passwd"}},
		{Mode: security.EgressBrowserDeclared, Destinations: []string{"https://user:secret@example.test"}},
		{Mode: security.EgressDenied, Destinations: []string{"https://example.test"}},
	}
	for index, policy := range unsafe {
		if err := security.ValidateEgressPolicy(policy); err == nil {
			t.Fatalf("unsafe egress policy %d was accepted: %#v", index, policy)
		}
	}
}

func TestWorkerEgressRulesDoNotReuseBrowserNetworkForCanvasOrDesign(t *testing.T) {
	for _, kind := range []security.WorkerKind{security.WorkerCanvas, security.WorkerDesign} {
		policy := validWorkerPolicy(security.HostProfileLinuxBubblewrap, security.ArchitectureAMD64, kind)
		policy.Egress = security.EgressPolicy{Mode: security.EgressBrowserDeclared, Destinations: []string{"https://example.test"}}
		if err := security.ValidateWorkerPolicy(policy); err == nil {
			t.Fatalf("%s inherited Browser egress policy", kind)
		}
	}
	browser := validWorkerPolicy(security.HostProfileLinuxBubblewrap, security.ArchitectureAMD64, security.WorkerBrowser)
	browser.Egress = security.EgressPolicy{Mode: security.EgressDenied}
	if err := security.ValidateWorkerPolicy(browser); err == nil {
		t.Fatal("Browser policy without declared browsing egress was accepted")
	}
}

func TestInheritedDescriptorPolicyRejectsUnexpectedDescriptors(t *testing.T) {
	valid := security.DescriptorPolicy{Allowed: []int{3, 4}, CloseUnlisted: true, VerifiedBeforeExec: true}
	if err := security.ValidateDescriptorPolicy(valid); err != nil {
		t.Fatalf("valid descriptor policy rejected: %v", err)
	}
	if err := security.ValidateInheritedDescriptors(valid, []int{3, 4}); err != nil {
		t.Fatalf("allowlisted descriptors rejected: %v", err)
	}
	for _, observed := range [][]int{{3, 5}, {3, 3}, {-1}} {
		if err := security.ValidateInheritedDescriptors(valid, observed); err == nil {
			t.Fatalf("observed descriptor set was accepted: %v", observed)
		}
	}
	unsafe := []security.DescriptorPolicy{
		{Allowed: []int{3}, VerifiedBeforeExec: true},
		{Allowed: []int{3}, CloseUnlisted: true},
		{Allowed: []int{3, 3}, CloseUnlisted: true, VerifiedBeforeExec: true},
		{Allowed: []int{-1}, CloseUnlisted: true, VerifiedBeforeExec: true},
	}
	for index, policy := range unsafe {
		if err := security.ValidateDescriptorPolicy(policy); err == nil {
			t.Fatalf("unsafe descriptor policy %d was accepted: %#v", index, policy)
		}
	}
}

func TestDescendantPolicyRequiresBoundedGroupCleanup(t *testing.T) {
	valid := security.DescendantPolicy{
		ProcessGroup: true, KillProcessGroup: true, ReapDescendants: true,
		BoundedDrain: true, CleanupVerified: true, DrainTimeout: 2 * time.Second,
	}
	if err := security.ValidateDescendantPolicy(valid); err != nil {
		t.Fatalf("valid descendant policy rejected: %v", err)
	}
	mutations := map[string]func(*security.DescendantPolicy){
		"no process group":   func(policy *security.DescendantPolicy) { policy.ProcessGroup = false },
		"no group kill":      func(policy *security.DescendantPolicy) { policy.KillProcessGroup = false },
		"no reaping":         func(policy *security.DescendantPolicy) { policy.ReapDescendants = false },
		"unbounded drain":    func(policy *security.DescendantPolicy) { policy.BoundedDrain = false },
		"unverified cleanup": func(policy *security.DescendantPolicy) { policy.CleanupVerified = false },
		"zero timeout":       func(policy *security.DescendantPolicy) { policy.DrainTimeout = 0 },
		"unbounded timeout":  func(policy *security.DescendantPolicy) { policy.DrainTimeout = 31 * time.Second },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			policy := valid
			mutate(&policy)
			if err := security.ValidateDescendantPolicy(policy); err == nil {
				t.Fatalf("unsafe descendant policy was accepted: %#v", policy)
			}
		})
	}
}
