package mcpserver_test

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/mcpserver"
	"github.com/miloszkolber/pixie/internal/persist"
)

func TestLifecycleDesiredRemainsTrueWhenStartupFails(t *testing.T) {
	ready := testRegistry(t, nil)
	enableBrowser(t, ready)
	if !ready.DesiredEnabled("browser") {
		t.Fatal("ready registry lost desired enablement")
	}
	snapshot := ready.ModuleLifecycle("browser")
	if !snapshot.Desired || !snapshot.Ready {
		t.Fatalf("ready lifecycle = %+v", snapshot)
	}

	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	failed, err := mcpserver.NewRegistry(mcpserver.Config{
		Host: "127.0.0.1", Port: 17881, DataDir: dataDir,
		Binaries: &mcpserver.BinaryConfig{
			AgentBrowser: filepath.Join(root, "missing-agent-browser"), BrowserConfig: configPath,
			ArtifactRoot: filepath.Join(root, "artifacts"), StateRoot: filepath.Join(root, "state"),
		},
	}, diagnostics.NormalizeBuild("test", "test"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("startup failure must degrade the module, not the publisher: %v", err)
	}
	t.Cleanup(failed.Shutdown)
	// The boundary is verified, so enablement is admitted and only the local
	// startup failure may keep readiness false.
	failed.SetWorkerBoundaryVerified(true)
	if err := failed.SetEnabled("browser", true); err != nil {
		t.Fatal(err)
	}
	if !failed.DesiredEnabled("browser") {
		t.Fatal("startup failure erased desired enablement")
	}
	startup := failed.ModuleLifecycle("browser")
	if !startup.Desired || startup.Ready {
		t.Fatalf("failed-start lifecycle must keep desired with readiness failed: %+v", startup)
	}
	catalog := failed.Catalog()
	if !catalog.Modules[0].Enabled || catalog.Modules[0].State != "unavailable" {
		t.Fatalf("failed-start catalog must preserve intent as unavailable: %#v", catalog.Modules[0])
	}
	if ready, _ := failed.Health("browser"); ready {
		t.Fatal("failed-start module reports ready")
	}
}

func TestLifecycleCompleteMapPreservesUnknownAndDecidesStart(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	raw := []byte("{\"modules\":{\"browser\":{\"enabled\":true},\"canvas\":{\"enabled\":true}}}\n")
	if err := os.WriteFile(filepath.Join(store.Dir, "mcp-modules.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	complete, found, err := mcpserver.ReadCompleteState(store)
	if err != nil || !found {
		t.Fatalf("complete read = %v found=%v err=%v", complete, found, err)
	}
	if !complete.Modules["browser"].Enabled {
		t.Fatalf("complete read lost browser intent: %#v", complete.Modules)
	}
	if _, ok := complete.Modules["canvas"]; !ok {
		t.Fatalf("complete read dropped unknown entry: %#v", complete.Modules)
	}
	next := mcpserver.CompleteStateWithDesired(complete, "browser", false)
	if next.Modules["browser"].Enabled {
		t.Fatalf("desired fold did not apply: %#v", next.Modules)
	}
	if _, ok := next.Modules["canvas"]; !ok {
		t.Fatalf("desired fold dropped unknown entry: %#v", next.Modules)
	}
	outcome, err := mcpserver.WriteCompleteStateWithOutcome(store, next, persist.PublishFaults{})
	if err != nil || outcome.Kind != persist.OutcomeInstalled {
		t.Fatalf("complete write outcome=%+v err=%v", outcome, err)
	}
	reconciled, err := mcpserver.ReconcileCompleteState(store)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Modules["browser"].Enabled {
		t.Fatalf("reconciled primary lost disablement: %#v", reconciled.Modules)
	}
	if _, ok := reconciled.Modules["canvas"]; !ok {
		t.Fatalf("reconciled primary dropped unknown entry: %#v", reconciled.Modules)
	}
	if may, _ := mcpserver.DecideModuleStart(outcome); !may {
		t.Fatal("installed outcome must allow worker start")
	}
	uncertain := persist.PublishOutcome{Kind: persist.OutcomeDurabilityUncertain, Stage: persist.StageDirSync, PrimaryVisible: true, MustReconcile: true}
	if may, _ := mcpserver.DecideModuleStart(uncertain); may {
		t.Fatal("uncertain outcome must never start a worker")
	}
	known := persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StageValidate}
	if may, _ := mcpserver.DecideModuleStart(known); may {
		t.Fatal("pre-publication failure must never start a worker")
	}
}

func TestLifecycleStrictConfigFailsLocallyWithoutFallback(t *testing.T) {
	bad := mcpserver.Config{Host: "evil.example", Port: 17882, DataDir: t.TempDir()}
	if _, err := mcpserver.StrictBrowserConfig(bad); err == nil || !strings.Contains(err.Error(), "invalid browser operator configuration") {
		t.Fatalf("restrictive host was not rejected locally: %v", err)
	}
	good := testRegistry(t, nil)
	enableBrowser(t, good)
	snapshotConfig, desired := good.SnapshotStartupConfig()
	if !desired["browser"] {
		t.Fatalf("startup snapshot lost desired state: %#v", desired)
	}
	if _, err := mcpserver.StrictBrowserConfig(snapshotConfig); err != nil {
		t.Fatalf("valid publisher config must stay usable: %v", err)
	}
}
