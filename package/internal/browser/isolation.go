package browser

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	egressProbeAddress = "1.1.1.1:443"
	egressProbeTimeout = 750 * time.Millisecond
	procStatusPath     = "/proc/self/status"
	cgroupV2Base       = "/sys/fs/cgroup"
)

// IsolationReport is the fact-only self-description a browser worker returns
// from GET /isolation. Every field is an observation the worker could make
// about its own process; a fact it could not establish is left false or zero
// instead of being inferred from configuration. The report is evidence for the
// controller to check, never an authorization token and never a substitute for
// the enforcement it describes.
type IsolationReport struct {
	WorkerVersion string          `json:"workerVersion"`
	UID           int             `json:"uid"`
	GID           int             `json:"gid"`
	DistinctUser  bool            `json:"distinctUser"`
	Filesystem    FilesystemFacts `json:"filesystem"`
	Network       NetworkFacts    `json:"network"`
	Limits        LimitFacts      `json:"limits"`
	Cleanup       CleanupFacts    `json:"cleanup"`
}

// FilesystemFacts records what the worker could observe about its filesystem
// scope. Mediated is the conjunction of the required controls, not a separate
// claim: it is only true when every component below is true.
type FilesystemFacts struct {
	Mediated       bool     `json:"mediated"`
	PrivateTmp     bool     `json:"privateTmp"`
	SystemReadOnly bool     `json:"systemReadOnly"`
	HomeProtected  bool     `json:"homeProtected"`
	ReadOnlyMounts []string `json:"readOnlyMounts,omitempty"`
}

// NetworkFacts records the observed egress posture. EgressRestricted is true
// only when a bounded outbound probe to an arbitrary address was denied; the
// probe outcome is evidence, not proof of the enforcing mechanism.
type NetworkFacts struct {
	EgressRestricted bool     `json:"egressRestricted"`
	Probes           []string `json:"probes,omitempty"`
}

// LimitFacts records the pre-exec resource controls the worker observed for
// itself. Zero or false means the worker could not establish the control.
type LimitFacts struct {
	NoNewPrivileges   bool  `json:"noNewPrivileges"`
	MemoryMaxBytes    int64 `json:"memoryMaxBytes"`
	CPUQuota          bool  `json:"cpuQuota"`
	TasksMax          int   `json:"tasksMax"`
	AddressSpaceBytes int64 `json:"addressSpaceBytes,omitempty"`
}

// CleanupFacts records the worker's bounded teardown behavior. Cleanup is
// implementation behavior rather than a kernel-enforced control, so it is
// reported separately and never used to imply process containment.
type CleanupFacts struct {
	BoundedStop    bool `json:"boundedStop"`
	SessionCleanup bool `json:"sessionCleanup"`
	StaleTempSweep bool `json:"staleTempSweep"`
}

// VerifyIsolationReport checks a worker's fact report against the containment
// the controller requires before it unlocks an untrusted Browser module. A
// missing, false or unobservable fact fails closed; the returned error names
// every failed requirement so the operator can see why Browser stays
// unavailable.
func VerifyIsolationReport(report IsolationReport, controllerUID int) error {
	var failures []string
	require := func(ok bool, reason string) {
		if !ok {
			failures = append(failures, reason)
		}
	}
	require(report.WorkerVersion != "", "worker version is not reported")
	require(report.UID != 0, "worker must not run as root")
	require(report.UID != controllerUID, "worker must run as a distinct uid from the controller")
	require(report.GID != 0, "worker must not use the root group")
	require(report.DistinctUser, "worker did not establish a distinct service user")
	require(report.Filesystem.Mediated, "filesystem mediation is not established")
	require(report.Filesystem.PrivateTmp, "private tmp is not established")
	require(report.Filesystem.SystemReadOnly, "system paths are not read-only")
	require(report.Filesystem.HomeProtected, "home directories are not protected")
	require(report.Network.EgressRestricted, "egress restriction is not established")
	require(report.Limits.NoNewPrivileges, "NoNewPrivileges is not established")
	require(report.Limits.MemoryMaxBytes > 0, "a memory cgroup limit is not established")
	require(report.Limits.CPUQuota, "a CPU cgroup quota is not established")
	require(report.Limits.TasksMax > 0, "a task (pids) cgroup limit is not established")
	require(report.Cleanup.BoundedStop, "a bounded stop is not established")
	require(report.Cleanup.SessionCleanup, "session cleanup is not established")
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("browser worker isolation is not proven: %s", strings.Join(failures, "; "))
}

// ProbeIsolation observes the current process's containment facts. It is the
// worker's own best-effort measurement; callers that cannot establish a fact
// receive false/zero and must not treat the report as consent.
func ProbeIsolation(version string) IsolationReport {
	uid, gid := os.Geteuid(), os.Getegid()
	return IsolationReport{
		WorkerVersion: strings.TrimSpace(version),
		UID:           uid,
		GID:           gid,
		DistinctUser:  uid != 0,
		Filesystem:    probeFilesystem(),
		Network:       probeNetwork(),
		Limits:        probeLimits(),
	}
}

func probeFilesystem() FilesystemFacts {
	facts := FilesystemFacts{
		PrivateTmp:     distinctMount("/", "/tmp"),
		SystemReadOnly: readOnlyMount("/usr") && readOnlyMount("/etc"),
		HomeProtected:  protectedHome("/home") && protectedHome("/root"),
	}
	for _, path := range []string{"/usr", "/etc", "/boot"} {
		if readOnlyMount(path) {
			facts.ReadOnlyMounts = append(facts.ReadOnlyMounts, path)
		}
	}
	facts.Mediated = facts.PrivateTmp && facts.SystemReadOnly && facts.HomeProtected
	return facts
}

// distinctMount reports whether target is a separate mount from root. Under a
// private /tmp the target has a different device than /.
func distinctMount(root, target string) bool {
	var rootStat, targetStat syscall.Stat_t
	if err := syscall.Stat(root, &rootStat); err != nil {
		return false
	}
	if err := syscall.Stat(target, &targetStat); err != nil {
		return false
	}
	return rootStat.Dev != targetStat.Dev
}

func readOnlyMount(path string) bool {
	var status unix.Statfs_t
	if err := unix.Statfs(path, &status); err != nil {
		return false
	}
	return status.Flags&unix.ST_RDONLY != 0
}

// protectedHome treats an absent, inaccessible, read-only or separately
// mounted home as protected. A home directory the worker can still read and
// write on the root filesystem is not protected.
func protectedHome(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return true
	}
	return readOnlyMount(path) || distinctMount("/", path)
}

func probeNetwork() NetworkFacts {
	facts := NetworkFacts{}
	connection, err := net.DialTimeout("tcp", egressProbeAddress, egressProbeTimeout)
	if err != nil {
		facts.EgressRestricted = true
		facts.Probes = []string{"tcp " + egressProbeAddress + " denied"}
		return facts
	}
	_ = connection.Close()
	facts.Probes = []string{"tcp " + egressProbeAddress + " reachable"}
	return facts
}

func probeLimits() LimitFacts {
	facts := LimitFacts{NoNewPrivileges: processNoNewPrivileges()}
	if value, ok := cgroupValue("memory.max"); ok {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed > 0 {
			facts.MemoryMaxBytes = parsed
		}
	}
	if value, ok := cgroupValue("cpu.max"); ok {
		fields := strings.Fields(value)
		facts.CPUQuota = len(fields) > 0 && fields[0] != "max"
	}
	if value, ok := cgroupValue("pids.max"); ok {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			facts.TasksMax = parsed
		}
	}
	facts.AddressSpaceBytes = addressSpaceLimit()
	return facts
}

func processNoNewPrivileges() bool {
	data, err := os.ReadFile(procStatusPath)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "NoNewPrivs:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "NoNewPrivs:")) == "1"
		}
	}
	return false
}

func cgroupValue(name string) (string, bool) {
	path, ok := currentCgroupV2Path()
	if !ok {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(cgroupV2Base, path, name))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}

func currentCgroupV2Path() (string, bool) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "0::") {
			path := strings.TrimSpace(strings.TrimPrefix(line, "0::"))
			if path == "" {
				path = "/"
			}
			return path, true
		}
	}
	return "", false
}

func addressSpaceLimit() int64 {
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_AS, &limit); err != nil {
		return 0
	}
	if limit.Cur == unix.RLIM_INFINITY || limit.Cur > uint64(^uint64(0)>>1) {
		return 0
	}
	return int64(limit.Cur)
}
