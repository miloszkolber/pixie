package controller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/canvas"
)

func TestCanvasMCPAttachRejectedFailsClosedExceptKnownObjectivePartialFailure(t *testing.T) {
	tests := []struct {
		name              string
		result            string
		objectiveExpected bool
		wantRejected      bool
	}{
		{name: "empty", result: "", wantRejected: true},
		{name: "missing outcome", result: `{}`, wantRejected: true},
		{name: "success", result: `{"ok":true}`, wantRejected: false},
		{name: "inconsistent success", result: `{"ok":true,"unavailable":["MCP connection unavailable: pixie-canvas"]}`, wantRejected: true},
		{name: "false without detail", result: `{"ok":false}`, wantRejected: true},
		{name: "Canvas unavailable", result: `{"ok":false,"unavailable":["MCP connection unavailable: pixie-canvas"]}`, wantRejected: true, objectiveExpected: true},
		{name: "known objective partial failure", result: `{"ok":false,"unavailable":["MCP connection unavailable: pixie_objectives"]}`, objectiveExpected: true, wantRejected: false},
		{name: "unexpected partial failure", result: `{"ok":false,"unavailable":["MCP connection unavailable: unknown"]}`, objectiveExpected: true, wantRejected: true},
		{name: "objective not requested", result: `{"ok":false,"unavailable":["MCP connection unavailable: pixie_objectives"]}`, wantRejected: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := canvasMCPAttachRejected(json.RawMessage(test.result), test.objectiveExpected); got != test.wantRejected {
				t.Fatalf("canvasMCPAttachRejected() = %t, want %t", got, test.wantRejected)
			}
		})
	}
}

func TestCanvasMCPEndpointRejectsCredentialBearingOrNonBrowserURLs(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{name: "canonical", endpoint: "http://127.0.0.1:7312/mcp/browser", want: "http://127.0.0.1:7312/mcp/canvas"},
		{name: "https", endpoint: "https://localhost/mcp/browser", want: "https://localhost/mcp/canvas"},
		{name: "relative", endpoint: "/mcp/browser"},
		{name: "wrong path", endpoint: "http://127.0.0.1:7312/mcp/canvas"},
		{name: "query", endpoint: "http://127.0.0.1:7312/mcp/browser?token=secret"},
		{name: "fragment", endpoint: "http://127.0.0.1:7312/mcp/browser#secret"},
		{name: "user info", endpoint: "http://user:password@127.0.0.1:7312/mcp/browser"},
		{name: "unsupported scheme", endpoint: "ws://127.0.0.1:7312/mcp/browser"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := canvasMCPEndpoint(test.endpoint); got != test.want {
				t.Fatalf("canvasMCPEndpoint(%q) = %q, want %q", test.endpoint, got, test.want)
			}
		})
	}
}

func TestCanvasPublishOutcomeDistinguishesKnownInstalledUncertain(t *testing.T) {
	installed := canvas.PublishOutcome{Kind: canvas.OutcomeInstalled, Stage: canvas.StageAcknowledge, PrimaryVisible: true}
	uncertain := canvas.PublishOutcome{Kind: canvas.OutcomeDurabilityUncertain, Stage: canvas.StageDirSync, PrimaryVisible: true, MustReconcile: true}
	uncommitted := canvas.PublishOutcome{Kind: canvas.OutcomeKnownUncommitted, Stage: canvas.StagePrimaryRename}
	got := canvas.DecideCanvasPublish(installed)
	if !got.MayDispatch || got.MustReconcile || got.MustRetainMutation {
		t.Fatalf("installed must allow guarded progress: %#v", got)
	}
	got = canvas.DecideCanvasPublish(uncertain)
	if got.MayDispatch || !got.MustReconcile || !got.MustRetainMutation {
		t.Fatalf("uncertain must block dispatch and retain mutation: %#v", got)
	}
	if got.Reason == "" || !strings.Contains(got.Reason, "reconcile") {
		t.Fatalf("uncertain decision must name reconciliation: %q", got.Reason)
	}
	got = canvas.DecideCanvasPublish(uncommitted)
	if got.MayDispatch || got.MustReconcile {
		t.Fatalf("known-uncommitted must preserve prior without reconcile: %#v", got)
	}
}

func TestCanvasUncertainReconcilesValidatedPrimaryWithoutBackupRestore(t *testing.T) {
	root := t.TempDir()
	config := canvas.DefaultConfig(root)
	config.WorkerLauncher = canvas.NewDeterministicWorkerLauncher()
	service, err := canvas.New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown() })
	authority, err := service.Attach("native-session-canvas-controller", 1)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(context.Background(), authority)
	if err != nil {
		t.Fatal(err)
	}
	service.SetPublishFaults(canvas.PublishFaults{FailDirSync: errors.New("injected dir-sync failure")})
	_, err = service.Write(context.Background(), authority, canvas.WriteRequest{CanvasID: created.Canvas.ID, ExpectedVersion: 1, HTML: "<html><body><p>candidate</p></body></html>", MutationID: "controller-reconcile-1"})
	if !errors.Is(err, canvas.ErrPersistenceUncertain) {
		t.Fatalf("uncertain publish must stay persistence_uncertain, got %v", err)
	}
	outcome := canvas.PublishOutcome{Kind: canvas.OutcomeDurabilityUncertain, Stage: canvas.StageDirSync, PrimaryVisible: true, MustReconcile: true}
	decision := canvas.DecideCanvasPublish(outcome)
	if decision.MayDispatch || !decision.MustReconcile || !decision.MustRetainMutation {
		t.Fatalf("uncertain must retain mutation and reconcile: %#v", decision)
	}
	reconciled, err := service.ReconcileCanvasAfterPublish(created.Canvas.ID, outcome)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Version != 2 {
		t.Fatalf("reconcile must validate candidate v2, got %d", reconciled.Version)
	}
	primary, err := os.ReadFile(filepath.Join(root, "mcp-canvas", authority.SessionKey, created.Canvas.ID, "meta.json"))
	if err != nil || !strings.Contains(string(primary), `"currentVersion": 2`) {
		t.Fatal("reconcile must not overwrite the visible candidate with an older backup")
	}
	retry, err := service.Write(context.Background(), authority, canvas.WriteRequest{CanvasID: created.Canvas.ID, ExpectedVersion: 1, HTML: "<html><body><p>candidate</p></body></html>", MutationID: "controller-reconcile-1"})
	if err != nil || !retry.Idempotent || retry.Version != 2 {
		t.Fatalf("identical retry must reconcile original: %#v %v", retry, err)
	}
}
