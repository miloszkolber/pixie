package mcpserver_test

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/design"
	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/mcpserver"
	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/tests/internal/designfixture"
)

func TestLifecycleDesiredRemainsTrueWhenStartupFails(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ready, err := mcpserver.NewRegistry(mcpserver.Config{
		Host: "127.0.0.1", Port: 17881, DataDir: dataDir,
		DesignConfig: &design.Config{DataDir: dataDir, Parser: designfixture.NewParser()},
	}, diagnostics.NormalizeBuild("test", "test"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ready.Shutdown)
	if err := ready.SetEnabled("design", true); err != nil {
		t.Fatal(err)
	}
	if !ready.DesiredEnabled("design") {
		t.Fatal("ready registry lost desired enablement")
	}
	snapshot := ready.ModuleLifecycle("design")
	if !snapshot.Desired || !snapshot.Ready {
		t.Fatalf("ready lifecycle = %+v", snapshot)
	}

	failedDataDir := filepath.Join(root, "failed-data")
	if err := os.MkdirAll(failedDataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A negative parse timeout is a valid, local construction failure that
	// leaves no usable Design service.
	failed, err := mcpserver.NewRegistry(mcpserver.Config{
		Host: "127.0.0.1", Port: 17882, DataDir: failedDataDir,
		DesignConfig: &design.Config{DataDir: failedDataDir, Parser: designfixture.NewParser(), ParseTimeout: -time.Second},
	}, diagnostics.NormalizeBuild("test", "test"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("startup failure must degrade the module, not the publisher: %v", err)
	}
	t.Cleanup(failed.Shutdown)
	if err := failed.SetEnabled("design", true); err != nil {
		t.Fatal(err)
	}
	if !failed.DesiredEnabled("design") {
		t.Fatal("startup failure erased desired enablement")
	}
	startup := failed.ModuleLifecycle("design")
	if !startup.Desired || startup.Ready {
		t.Fatalf("failed-start lifecycle must keep desired with readiness failed: %+v", startup)
	}
	if ready, _ := failed.Health("design"); ready {
		t.Fatal("failed-start module reports ready")
	}
}

func TestLifecycleCompleteMapPreservesUnknownAndDecidesStart(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	raw := []byte("{\"modules\":{\"design\":{\"enabled\":true},\"unknown-module\":{\"enabled\":true}}}\n")
	if err := os.WriteFile(filepath.Join(store.Dir, "mcp-modules.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	complete, found, err := mcpserver.ReadCompleteState(store)
	if err != nil || !found {
		t.Fatalf("complete read = %v found=%v err=%v", complete, found, err)
	}
	if !complete.Modules["design"].Enabled {
		t.Fatalf("complete read lost design intent: %#v", complete.Modules)
	}
	if _, ok := complete.Modules["unknown-module"]; !ok {
		t.Fatalf("complete read dropped unknown entry: %#v", complete.Modules)
	}
	next := mcpserver.CompleteStateWithDesired(complete, "design", false)
	if next.Modules["design"].Enabled {
		t.Fatalf("desired fold did not apply: %#v", next.Modules)
	}
	if _, ok := next.Modules["unknown-module"]; !ok {
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
	if reconciled.Modules["design"].Enabled {
		t.Fatalf("reconciled primary lost disablement: %#v", reconciled.Modules)
	}
	if _, ok := reconciled.Modules["unknown-module"]; !ok {
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
