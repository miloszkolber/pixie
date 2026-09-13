package browser_test

import (
	"os"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/browser"
)

func conformingIsolationReport() browser.IsolationReport {
	return browser.IsolationReport{
		WorkerVersion: "2.3.4",
		UID:           1414,
		GID:           1414,
		DistinctUser:  true,
		Filesystem: browser.FilesystemFacts{
			Mediated: true, PrivateTmp: true, SystemReadOnly: true, HomeProtected: true,
		},
		Network: browser.NetworkFacts{EgressRestricted: true},
		Limits: browser.LimitFacts{
			NoNewPrivileges: true, MemoryMaxBytes: 1 << 30, CPUQuota: true, TasksMax: 256,
		},
		Cleanup: browser.CleanupFacts{BoundedStop: true, SessionCleanup: true, StaleTempSweep: true},
	}
}

func TestVerifyIsolationReportAcceptsOnlyProvenContainment(t *testing.T) {
	if err := browser.VerifyIsolationReport(conformingIsolationReport(), 1000); err != nil {
		t.Fatalf("conforming report was rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*browser.IsolationReport)
		want   string
	}{
		{"missing version", func(r *browser.IsolationReport) { r.WorkerVersion = "" }, "version"},
		{"root uid", func(r *browser.IsolationReport) { r.UID = 0 }, "must not run as root"},
		{"same uid as controller", func(r *browser.IsolationReport) { r.UID = 1000 }, "distinct uid"},
		{"root gid", func(r *browser.IsolationReport) { r.GID = 0 }, "root group"},
		{"not a distinct user", func(r *browser.IsolationReport) { r.DistinctUser = false }, "distinct service user"},
		{"filesystem not mediated", func(r *browser.IsolationReport) { r.Filesystem.Mediated = false }, "filesystem mediation"},
		{"no private tmp", func(r *browser.IsolationReport) { r.Filesystem.PrivateTmp = false }, "private tmp"},
		{"system writable", func(r *browser.IsolationReport) { r.Filesystem.SystemReadOnly = false }, "read-only"},
		{"home unprotected", func(r *browser.IsolationReport) { r.Filesystem.HomeProtected = false }, "home directories"},
		{"egress unrestricted", func(r *browser.IsolationReport) { r.Network.EgressRestricted = false }, "egress restriction"},
		{"no new privileges absent", func(r *browser.IsolationReport) { r.Limits.NoNewPrivileges = false }, "NoNewPrivileges"},
		{"memory unbounded", func(r *browser.IsolationReport) { r.Limits.MemoryMaxBytes = 0 }, "memory cgroup limit"},
		{"cpu unbounded", func(r *browser.IsolationReport) { r.Limits.CPUQuota = false }, "CPU cgroup quota"},
		{"tasks unbounded", func(r *browser.IsolationReport) { r.Limits.TasksMax = 0 }, "task (pids) cgroup limit"},
		{"unbounded stop", func(r *browser.IsolationReport) { r.Cleanup.BoundedStop = false }, "bounded stop"},
		{"no session cleanup", func(r *browser.IsolationReport) { r.Cleanup.SessionCleanup = false }, "session cleanup"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			report := conformingIsolationReport()
			test.mutate(&report)
			err := browser.VerifyIsolationReport(report, 1000)
			if err == nil {
				t.Fatal("non-conforming report was accepted")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %q does not name %q", err, test.want)
			}
		})
	}
}

func TestProbeIsolationReportsObservedSelfFacts(t *testing.T) {
	report := browser.ProbeIsolation("2.3.4")
	if report.WorkerVersion != "2.3.4" {
		t.Fatalf("worker version = %q", report.WorkerVersion)
	}
	if report.UID != os.Geteuid() || report.GID != os.Getegid() {
		t.Fatalf("probed uid/gid = %d/%d, want %d/%d", report.UID, report.GID, os.Geteuid(), os.Getegid())
	}
	if report.DistinctUser != (os.Geteuid() != 0) {
		t.Fatalf("distinct user = %v for uid %d", report.DistinctUser, report.UID)
	}
	// The egress probe must always record what it attempted, even when the
	// result is a denial; an unobserved value must stay false.
	if len(report.Network.Probes) == 0 {
		t.Fatal("egress probe did not record its outcome")
	}
}
