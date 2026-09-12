// Package security contains declarative admission checks for untrusted worker
// processes. These checks do not create namespaces, delegate cgroups, or
// inspect the host. A policy that passes here is an input to a launcher; it is
// not evidence that the host has enforced the policy.
package security

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ErrInvalidPolicy identifies a malformed or incomplete worker policy.
var ErrInvalidPolicy = errors.New("invalid worker security policy")

// ErrUnsupportedHostProfile identifies a host topology for which this package
// has no containment contract.
var ErrUnsupportedHostProfile = errors.New("unsupported host profile")

// UnsupportedHostProfileError retains the profile and an actionable reason so
// callers can keep the affected module unavailable without falling back to a
// permissive worker.
type UnsupportedHostProfileError struct {
	Profile HostProfile
	Reason  string
}

func (e *UnsupportedHostProfileError) Error() string {
	return fmt.Sprintf("unsupported host profile %q: %s", e.Profile, e.Reason)
}

func (e *UnsupportedHostProfileError) Unwrap() error { return ErrUnsupportedHostProfile }

// Architecture is the explicitly tested target architecture. It is supplied
// by the caller rather than inferred from the current test or host process.
type Architecture string

const (
	ArchitectureAMD64 Architecture = "amd64"
	ArchitectureARM64 Architecture = "arm64"
)

// HostProfile names a complete worker topology, not merely a container label.
type HostProfile string

const (
	// HostProfileLinuxBubblewrap is the Linux namespace plus delegated-cgroup
	// candidate. The launcher still has to prove placement before execution.
	HostProfileLinuxBubblewrap HostProfile = "linux-bubblewrap"
	// HostProfileDockerRestrictedWorker is a separately delegated worker
	// service. A shared controller/worker UID is deliberately not this profile.
	HostProfileDockerRestrictedWorker HostProfile = "docker-restricted-worker"

	// HostProfileDirectHost has no worker enclosure and is unavailable for
	// untrusted processing.
	HostProfileDirectHost HostProfile = "direct-host"
	// HostProfileDockerSharedUID is the legacy same-UID container posture. A
	// container boundary and a shared UID do not establish worker isolation.
	HostProfileDockerSharedUID HostProfile = "docker-shared-uid"
)

// Compatibility spelling for callers that describe the Docker topology as a
// delegated worker rather than a restricted worker service.
const HostProfileDockerDelegatedWorker = HostProfileDockerRestrictedWorker

// SupportedHostProfiles returns the profiles with an explicit containment
// contract. It does not probe or assert that either profile is configured.
func SupportedHostProfiles() []HostProfile {
	return []HostProfile{HostProfileLinuxBubblewrap, HostProfileDockerRestrictedWorker}
}

// UnsupportedHostProfiles returns known profiles that must remain unavailable
// instead of silently inheriting the controller's authority.
func UnsupportedHostProfiles() []HostProfile {
	return []HostProfile{HostProfileDirectHost, HostProfileDockerSharedUID}
}

// ValidateHostProfile rejects unknown and explicitly unsupported topologies.
func ValidateHostProfile(profile HostProfile) error {
	switch profile {
	case HostProfileLinuxBubblewrap, HostProfileDockerRestrictedWorker:
		return nil
	case HostProfileDirectHost:
		return &UnsupportedHostProfileError{Profile: profile, Reason: "direct-host workers do not provide a containment boundary"}
	case HostProfileDockerSharedUID:
		return &UnsupportedHostProfileError{Profile: profile, Reason: "same-UID container execution is not worker isolation"}
	default:
		return &UnsupportedHostProfileError{Profile: profile, Reason: "no launcher or delegation contract is registered"}
	}
}

// ValidateArchitecture checks an explicit supported architecture name.
func ValidateArchitecture(architecture Architecture) error {
	switch architecture {
	case ArchitectureAMD64, ArchitectureARM64:
		return nil
	default:
		return invalidPolicy("unsupported worker architecture %q", architecture)
	}
}

// WorkerKind identifies the untrusted operation receiving the worker.
type WorkerKind string

const (
	WorkerBrowser WorkerKind = "browser"
	WorkerCanvas  WorkerKind = "canvas"
	WorkerDesign  WorkerKind = "design"
)

func validateWorkerKind(kind WorkerKind) error {
	switch kind {
	case WorkerBrowser, WorkerCanvas, WorkerDesign:
		return nil
	default:
		return invalidPolicy("unsupported worker kind %q", kind)
	}
}

// Launcher names are intentionally narrow. A caller cannot turn an arbitrary
// shell command into a worker launcher by placing it in a policy.
const (
	LauncherBubblewrap              = "bubblewrap"
	LauncherRestrictedWorkerService = "restricted-worker-service"
)

// NamespacePolicy describes the private namespaces that must be active before
// untrusted input is handed to a worker.
type NamespacePolicy struct {
	Mount bool
	PID   bool
	User  bool
}

// ResourcePlacement is the pre-exec placement attestation consumed by worker
// admission. All negative mount/socket controls are explicit to prevent a
// zero-value policy from being mistaken for a safe default.
type ResourcePlacement struct {
	Launcher               string
	VerifiedBeforeExec     bool
	Namespaces             NamespacePolicy
	CgroupDelegated        bool
	LimitsActiveBeforeExec bool
	DistinctUID            bool
	ReadOnlyInputs         bool
	PrivateScratch         bool
	NoControllerMounts     bool
	NoPiMounts             bool
	NoProjectMounts        bool
	NoUserRuntimeSockets   bool
	NoDockerSocket         bool
	NoWritableCgroupTree   bool
}

// ValidatePreExecPlacement checks that isolation and resource placement are
// declared before execution. It performs no host mutation or capability
// probing.
func ValidatePreExecPlacement(placement ResourcePlacement) error {
	if placement.Launcher == "" || strings.TrimSpace(placement.Launcher) != placement.Launcher || strings.ContainsAny(placement.Launcher, "\r\n\t ") {
		return invalidPolicy("worker launcher must be an explicit registered name")
	}
	if placement.Launcher != LauncherBubblewrap && placement.Launcher != LauncherRestrictedWorkerService {
		return invalidPolicy("worker launcher %q is not registered", placement.Launcher)
	}
	checks := []struct {
		name  string
		value bool
	}{
		{"pre-exec placement verification", placement.VerifiedBeforeExec},
		{"private mount namespace", placement.Namespaces.Mount},
		{"private PID namespace", placement.Namespaces.PID},
		{"private user namespace", placement.Namespaces.User},
		{"delegated cgroup", placement.CgroupDelegated},
		{"limits active before exec", placement.LimitsActiveBeforeExec},
		{"distinct worker UID", placement.DistinctUID},
		{"read-only input mounts", placement.ReadOnlyInputs},
		{"private scratch area", placement.PrivateScratch},
		{"controller mounts absent", placement.NoControllerMounts},
		{"Pi mounts absent", placement.NoPiMounts},
		{"project mounts absent", placement.NoProjectMounts},
		{"user runtime sockets absent", placement.NoUserRuntimeSockets},
		{"Docker socket absent", placement.NoDockerSocket},
		{"writable cgroup tree absent", placement.NoWritableCgroupTree},
	}
	for _, check := range checks {
		if !check.value {
			if check.name == "distinct worker UID" {
				return invalidPolicy("same-UID worker execution is not isolation")
			}
			return invalidPolicy("%s is required before worker exec", check.name)
		}
	}
	return nil
}

// ScratchPolicy bounds ordinary filesystem scratch separately from memory and
// cgroup limits.
type ScratchPolicy struct {
	MaxBytes          int64
	MaxInodes         int64
	Private           bool
	ReserveBeforeExec bool
}

const (
	CanvasScratchMaxBytes int64 = 128 << 20
	DesignScratchMaxBytes int64 = 384 << 20
	ScratchMaxInodes      int64 = 8_192
)

// ValidateScratchPolicy checks positive, private, pre-reserved scratch bounds.
func ValidateScratchPolicy(policy ScratchPolicy) error {
	if policy.MaxBytes <= 0 || policy.MaxBytes > DesignScratchMaxBytes {
		return invalidPolicy("scratch byte limit must be between 1 and %d", DesignScratchMaxBytes)
	}
	if policy.MaxInodes <= 0 || policy.MaxInodes > ScratchMaxInodes {
		return invalidPolicy("scratch inode limit must be between 1 and %d", ScratchMaxInodes)
	}
	if !policy.Private {
		return invalidPolicy("scratch must be private to the worker")
	}
	if !policy.ReserveBeforeExec {
		return invalidPolicy("scratch capacity must be reserved before worker exec")
	}
	return nil
}

// EgressMode describes the only network postures a worker may declare.
type EgressMode string

const (
	EgressDenied          EgressMode = "denied"
	EgressBrowserDeclared EgressMode = "browser-declared"
	EgressUnrestricted    EgressMode = "unrestricted"
)

// EgressPolicy is declarative. Destinations are checked as URLs, but no
// connection is made and URL reachability is not inferred from validation.
type EgressPolicy struct {
	Mode         EgressMode
	Destinations []string
}

// ValidateEgressPolicy rejects unrestricted or implicit network access.
func ValidateEgressPolicy(policy EgressPolicy) error {
	switch policy.Mode {
	case EgressDenied:
		if len(policy.Destinations) != 0 {
			return invalidPolicy("egress-denied workers cannot declare destinations")
		}
		return nil
	case EgressBrowserDeclared:
		if len(policy.Destinations) == 0 {
			return invalidPolicy("browser egress requires an explicit destination policy")
		}
		seen := make(map[string]struct{}, len(policy.Destinations))
		for _, destination := range policy.Destinations {
			if err := validateEgressDestination(destination); err != nil {
				return err
			}
			if _, exists := seen[destination]; exists {
				return invalidPolicy("duplicate egress destination %q", destination)
			}
			seen[destination] = struct{}{}
		}
		return nil
	case EgressUnrestricted:
		return invalidPolicy("unrestricted worker egress is unavailable")
	default:
		return invalidPolicy("unknown worker egress mode %q", policy.Mode)
	}
}

func validateEgressDestination(destination string) error {
	if destination == "" || strings.TrimSpace(destination) != destination || strings.ContainsAny(destination, "\r\n\t") || strings.Contains(destination, "*") {
		return invalidPolicy("egress destination %q is not an explicit URL", destination)
	}
	parsed, err := url.Parse(destination)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return invalidPolicy("egress destination %q must be an explicit http(s) URL", destination)
	}
	if parsed.User != nil {
		return invalidPolicy("egress destination %q must not contain credentials", destination)
	}
	return nil
}

// DescriptorPolicy describes the descriptors deliberately inherited by a
// worker. An allowlist is not enough unless unlisted descriptors are closed
// and the allowlist was checked before exec.
type DescriptorPolicy struct {
	Allowed            []int
	CloseUnlisted      bool
	VerifiedBeforeExec bool
}

// ValidateDescriptorPolicy checks a finite descriptor allowlist.
func ValidateDescriptorPolicy(policy DescriptorPolicy) error {
	if !policy.CloseUnlisted {
		return invalidPolicy("unlisted inherited descriptors must be closed")
	}
	if !policy.VerifiedBeforeExec {
		return invalidPolicy("inherited descriptors must be verified before exec")
	}
	if len(policy.Allowed) > 16 {
		return invalidPolicy("too many inherited descriptors are allowlisted")
	}
	seen := make(map[int]struct{}, len(policy.Allowed))
	for _, descriptor := range policy.Allowed {
		if descriptor < 0 {
			return invalidPolicy("inherited descriptor %d is invalid", descriptor)
		}
		if _, exists := seen[descriptor]; exists {
			return invalidPolicy("inherited descriptor %d is duplicated", descriptor)
		}
		seen[descriptor] = struct{}{}
	}
	return nil
}

// ValidateInheritedDescriptors validates an observed descriptor list against
// a previously validated policy without opening or closing descriptors.
func ValidateInheritedDescriptors(policy DescriptorPolicy, observed []int) error {
	if err := ValidateDescriptorPolicy(policy); err != nil {
		return err
	}
	allowed := make(map[int]struct{}, len(policy.Allowed))
	for _, descriptor := range policy.Allowed {
		allowed[descriptor] = struct{}{}
	}
	seen := make(map[int]struct{}, len(observed))
	for _, descriptor := range observed {
		if descriptor < 0 {
			return invalidPolicy("observed inherited descriptor %d is invalid", descriptor)
		}
		if _, duplicate := seen[descriptor]; duplicate {
			return invalidPolicy("observed inherited descriptor %d is duplicated", descriptor)
		}
		seen[descriptor] = struct{}{}
		if _, ok := allowed[descriptor]; !ok {
			return invalidPolicy("unexpected inherited descriptor %d", descriptor)
		}
	}
	return nil
}

// DescendantPolicy makes cancellation and teardown cover the worker's whole
// process group, including descendants retaining output pipes.
type DescendantPolicy struct {
	ProcessGroup     bool
	KillProcessGroup bool
	ReapDescendants  bool
	BoundedDrain     bool
	CleanupVerified  bool
	DrainTimeout     time.Duration
}

const maxDescendantDrainTimeout = 30 * time.Second

// ValidateDescendantPolicy checks bounded group cleanup without signalling any
// process.
func ValidateDescendantPolicy(policy DescendantPolicy) error {
	checks := []struct {
		name  string
		value bool
	}{
		{"managed process group", policy.ProcessGroup},
		{"process-group cancellation", policy.KillProcessGroup},
		{"descendant reaping", policy.ReapDescendants},
		{"bounded pipe drain", policy.BoundedDrain},
		{"cleanup verification", policy.CleanupVerified},
	}
	for _, check := range checks {
		if !check.value {
			return invalidPolicy("%s is required", check.name)
		}
	}
	if policy.DrainTimeout <= 0 || policy.DrainTimeout > maxDescendantDrainTimeout {
		return invalidPolicy("descendant drain timeout must be between 1ns and %s", maxDescendantDrainTimeout)
	}
	return nil
}

// WorkerPolicy composes the host, pre-exec, resource and cleanup contracts for
// one untrusted worker admission.
type WorkerPolicy struct {
	Profile      HostProfile
	Architecture Architecture
	Kind         WorkerKind
	Placement    ResourcePlacement
	Scratch      ScratchPolicy
	Egress       EgressPolicy
	Descriptors  DescriptorPolicy
	Descendants  DescendantPolicy
}

// ValidateWorkerPolicy performs all pure fail-closed checks for one worker.
func ValidateWorkerPolicy(policy WorkerPolicy) error {
	if err := ValidateHostProfile(policy.Profile); err != nil {
		return err
	}
	if err := ValidateArchitecture(policy.Architecture); err != nil {
		return err
	}
	if err := validateWorkerKind(policy.Kind); err != nil {
		return err
	}
	if err := ValidatePreExecPlacement(policy.Placement); err != nil {
		return err
	}
	if err := validateProfileLauncher(policy.Profile, policy.Placement.Launcher); err != nil {
		return err
	}
	if err := ValidateScratchPolicy(policy.Scratch); err != nil {
		return err
	}
	if policy.Kind == WorkerCanvas && policy.Scratch.MaxBytes > CanvasScratchMaxBytes {
		return invalidPolicy("Canvas scratch exceeds the %d-byte worker bound", CanvasScratchMaxBytes)
	}
	if err := ValidateEgressPolicy(policy.Egress); err != nil {
		return err
	}
	switch policy.Kind {
	case WorkerBrowser:
		if policy.Egress.Mode != EgressBrowserDeclared {
			return invalidPolicy("Browser workers require declared browsing egress")
		}
	case WorkerCanvas, WorkerDesign:
		if policy.Egress.Mode != EgressDenied {
			return invalidPolicy("%s workers require egress denial", policy.Kind)
		}
	}
	if err := ValidateDescriptorPolicy(policy.Descriptors); err != nil {
		return err
	}
	return ValidateDescendantPolicy(policy.Descendants)
}

func validateProfileLauncher(profile HostProfile, launcher string) error {
	want := LauncherBubblewrap
	if profile == HostProfileDockerRestrictedWorker {
		want = LauncherRestrictedWorkerService
	}
	if launcher != want {
		return invalidPolicy("host profile %q requires launcher %q", profile, want)
	}
	return nil
}

func invalidPolicy(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPolicy, fmt.Sprintf(format, args...))
}
