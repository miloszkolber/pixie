package diagnostics

import "fmt"

// Recovery status values are stable codes an operator surface can print and a
// support export can bound.
const (
	RecoveryOK       = "ok"
	RecoveryDegraded = "degraded"
	RecoveryFailed   = "failed"
	RecoveryUnknown  = "unknown"

	// RestartLoopThreshold is the number of recent supervisor starts that is
	// treated as a restart loop rather than a normal service restart.
	RestartLoopThreshold = 4
)

// RecoveryCheck is one bounded, secret-free recovery fact plus remediation.
// Code and Remediation are package constants; no host, path or error text is
// ever copied into them.
type RecoveryCheck struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Code        string `json:"code,omitempty"`
	Remediation string `json:"remediation,omitempty"`
}

// RecoveryReport is the operator recovery projection. It never carries raw
// errors, paths, endpoints or credentials.
type RecoveryReport struct {
	Summary string          `json:"summary"`
	Checks  []RecoveryCheck `json:"checks"`
}

// RecoveryFacts collects the already-validated facts a caller can establish.
// Nil means unknown and is distinct from a healthy false value.
type RecoveryFacts struct {
	ConfigReadable   *bool
	HostConfigured   *bool
	HostReachable    *bool
	HostReady        *bool
	PortMismatch     *bool
	PiPackagePresent *bool
	PiPackageValid   *bool
	AgentDirWritable *bool
	OwnerLockHeld    *bool
	RestartCount     int
}

const (
	recoveryConfigUnreadable = "Point --config at a readable absolute JSON file, or omit it to use environment defaults."
	recoveryHostUnconfigured = "Configure PIXIE_PI_SECRET_KEY so the controller can dial the assistant host over loopback."
	recoveryHostUnreachable  = "Start the assistant host and confirm its configured port matches PIXIE_PI_PORT; Pixie does not attach to an active TUI."
	recoveryHostNotReady     = "Wait for the assistant host readiness probe to pass, then retry. Check the retained child stderr for the first failure."
	recoveryPortMismatch     = "Set PIXIE_PI_PORT to the assistant host port and remove the conflicting PIXIE_PI_URL, then restart the service."
	recoveryPackageMissing   = "Install the operator Pi package required by the selected product, then retry."
	recoveryPackageInvalid   = "Replace the operator Pi package with a complete, non-symlinked installation for this architecture."
	recoveryAgentDir         = "Grant the service user write and execute access to the configured agent directory, or select a writable directory."
	recoveryOwnerLock        = "Pi is already owned by another Pixie session; stop that owner before retrying, or use a different PI_CODING_AGENT_DIR. Pixie does not attach to an active TUI."
	recoveryRestartLoop      = "The service restarted too often in a short window. Inspect the retained child stderr, fix the first crash, then restart."
)

// AssessRecovery turns validated facts into a bounded, ordered report. The
// check set and remediation text are fixed; callers supply only typed facts.
func AssessRecovery(facts RecoveryFacts) RecoveryReport {
	checks := []RecoveryCheck{
		configRecoveryCheck(facts.ConfigReadable),
		hostRecoveryCheck(facts),
		portRecoveryCheck(facts.PortMismatch),
		piPackageRecoveryCheck(facts.PiPackagePresent, facts.PiPackageValid),
		agentDirRecoveryCheck(facts.AgentDirWritable),
		ownerLockRecoveryCheck(facts.OwnerLockHeld),
		restartRecoveryCheck(facts.RestartCount),
	}
	summary := RecoveryOK
	for _, check := range checks {
		switch check.Status {
		case RecoveryFailed:
			summary = "attention"
		case RecoveryDegraded, RecoveryUnknown:
			if summary != "attention" {
				summary = "incomplete"
			}
		}
	}
	return RecoveryReport{Summary: summary, Checks: checks}
}

func configRecoveryCheck(readable *bool) RecoveryCheck {
	if readable == nil {
		return RecoveryCheck{ID: "config.readable", Status: RecoveryUnknown, Remediation: recoveryConfigUnreadable}
	}
	if !*readable {
		return RecoveryCheck{ID: "config.readable", Status: RecoveryFailed, Code: "config.unreadable", Remediation: recoveryConfigUnreadable}
	}
	return RecoveryCheck{ID: "config.readable", Status: RecoveryOK}
}

func hostRecoveryCheck(facts RecoveryFacts) RecoveryCheck {
	check := RecoveryCheck{ID: "host.ready"}
	if facts.HostConfigured == nil {
		check.Status = RecoveryUnknown
		check.Remediation = recoveryHostUnconfigured
		return check
	}
	if !*facts.HostConfigured {
		check.Status = RecoveryFailed
		check.Code = "host.not_configured"
		check.Remediation = recoveryHostUnconfigured
		return check
	}
	if facts.HostReachable == nil {
		check.Status = RecoveryUnknown
		check.Remediation = recoveryHostUnreachable
		return check
	}
	if !*facts.HostReachable {
		check.Status = RecoveryFailed
		check.Code = "host.unreachable"
		check.Remediation = recoveryHostUnreachable
		return check
	}
	if facts.HostReady != nil && !*facts.HostReady {
		check.Status = RecoveryDegraded
		check.Code = "host.not_ready"
		check.Remediation = recoveryHostNotReady
		return check
	}
	check.Status = RecoveryOK
	return check
}

func portRecoveryCheck(mismatch *bool) RecoveryCheck {
	check := RecoveryCheck{ID: "host.port"}
	if mismatch == nil {
		check.Status = RecoveryUnknown
		return check
	}
	if *mismatch {
		check.Status = RecoveryFailed
		check.Code = "host.port_mismatch"
		check.Remediation = recoveryPortMismatch
		return check
	}
	check.Status = RecoveryOK
	return check
}

func piPackageRecoveryCheck(present, valid *bool) RecoveryCheck {
	check := RecoveryCheck{ID: "pi_package"}
	switch {
	case present == nil || valid == nil:
		check.Status = RecoveryUnknown
	case !*present:
		check.Status = RecoveryFailed
		check.Code = "pi_package.missing"
		check.Remediation = recoveryPackageMissing
	case !*valid:
		check.Status = RecoveryFailed
		check.Code = "pi_package.invalid"
		check.Remediation = recoveryPackageInvalid
	default:
		check.Status = RecoveryOK
	}
	return check
}

func agentDirRecoveryCheck(writable *bool) RecoveryCheck {
	check := RecoveryCheck{ID: "agent_dir.writable"}
	if writable == nil {
		check.Status = RecoveryUnknown
		return check
	}
	if !*writable {
		check.Status = RecoveryFailed
		check.Code = "agent_dir.not_writable"
		check.Remediation = recoveryAgentDir
		return check
	}
	check.Status = RecoveryOK
	return check
}

func ownerLockRecoveryCheck(held *bool) RecoveryCheck {
	check := RecoveryCheck{ID: "owner_lock"}
	if held == nil {
		check.Status = RecoveryUnknown
		return check
	}
	if *held {
		check.Status = RecoveryDegraded
		check.Code = "owner_lock.held"
		check.Remediation = recoveryOwnerLock
		return check
	}
	check.Status = RecoveryOK
	return check
}

func restartRecoveryCheck(count int) RecoveryCheck {
	check := RecoveryCheck{ID: "supervisor.restart_loop"}
	if count < 0 {
		check.Status = RecoveryUnknown
		return check
	}
	if count >= RestartLoopThreshold {
		check.Status = RecoveryFailed
		check.Code = "supervisor.restart_loop"
		check.Remediation = recoveryRestartLoop
		return check
	}
	check.Status = RecoveryOK
	return check
}

// FormatRecoveryCheck renders one check as a single bounded line. It emits no
// memory addresses, paths or arbitrary text; values are package constants.
func FormatRecoveryCheck(check RecoveryCheck) string {
	switch {
	case check.Code != "" && check.Remediation != "":
		return fmt.Sprintf("%s=%s code=%s remediation=%q", check.ID, check.Status, check.Code, check.Remediation)
	case check.Remediation != "":
		return fmt.Sprintf("%s=%s remediation=%q", check.ID, check.Status, check.Remediation)
	case check.Code != "":
		return fmt.Sprintf("%s=%s code=%s", check.ID, check.Status, check.Code)
	default:
		return fmt.Sprintf("%s=%s", check.ID, check.Status)
	}
}
