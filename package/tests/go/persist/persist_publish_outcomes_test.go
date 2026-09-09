package persist_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

func TestPublishGuardsDistinguishOutcomes(t *testing.T) {
	installed := persist.PublishOutcome{Kind: persist.OutcomeInstalled, Stage: persist.StageAcknowledge, PrimaryVisible: true}
	uncertain := persist.PublishOutcome{Kind: persist.OutcomeDurabilityUncertain, Stage: persist.StageDirSync, PrimaryVisible: true, MustReconcile: true}
	uncommitted := persist.PublishOutcome{Kind: persist.OutcomeKnownUncommitted, Stage: persist.StagePrimary}

	for _, decide := range []func(persist.PublishOutcome) controller.LedgerPublishDecision{
		controller.DecideSchedulePublish,
		controller.DecideQueuePublish,
		controller.DecideDeletionPublish,
	} {
		got := decide(installed)
		if !got.MayDispatch || got.MustReconcile || got.MustRetainMutation {
			t.Fatalf("installed must allow guarded progress: %#v", got)
		}
		got = decide(uncertain)
		if got.MayDispatch || !got.MustReconcile || !got.MustRetainMutation {
			t.Fatalf("uncertain must block dispatch and retain mutation: %#v", got)
		}
		if got.Reason == "" {
			t.Fatal("uncertain decision requires a reason")
		}
		got = decide(uncommitted)
		if got.MayDispatch || got.MustReconcile {
			t.Fatalf("known-uncommitted must preserve prior commit without reconcile: %#v", got)
		}
	}
	scheduleUncertain := controller.DecideSchedulePublish(uncertain)
	if !strings.Contains(scheduleUncertain.Reason, "uncertain claim") {
		t.Fatalf("schedule guard must name uncertain claims: %q", scheduleUncertain.Reason)
	}
	queueUncertain := controller.DecideQueuePublish(uncertain)
	if !strings.Contains(queueUncertain.Reason, "uncertain claim") {
		t.Fatalf("queue guard must name uncertain claims: %q", queueUncertain.Reason)
	}
	deletionUncertain := controller.DecideDeletionPublish(uncertain)
	if !strings.Contains(deletionUncertain.Reason, "tombstone") {
		t.Fatalf("deletion guard must name tombstones: %q", deletionUncertain.Reason)
	}
}

func TestQueuePostRenameUncertainReconcilesPrimary(t *testing.T) {
	dir := t.TempDir()
	store := persist.Store{Dir: dir}
	type queuedItem struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	type queueRecord struct {
		ProjectID string       `json:"projectId"`
		SessionID string       `json:"sessionId"`
		Revision  string       `json:"revision"`
		FollowUp  []queuedItem `json:"followUp"`
		Handled   []any        `json:"handled"`
	}
	type queueStore struct {
		Version int           `json:"version"`
		Engine  string        `json:"engine"`
		Records []queueRecord `json:"records"`
	}
	opaque := "agent/session/opaque"
	prior := queueStore{Version: 1, Engine: "pi", Records: []queueRecord{{ProjectID: "project", SessionID: opaque, Revision: "first", FollowUp: []queuedItem{{ID: "one", Text: "first"}}, Handled: []any{}}}}
	if _, err := persist.WriteWithOutcome(store, "pi-session-queues.json", prior, nil, persist.PublishFaults{}); err != nil {
		t.Fatal(err)
	}
	candidate := queueStore{Version: 1, Engine: "pi", Records: []queueRecord{{ProjectID: "project", SessionID: opaque, Revision: "second", FollowUp: []queuedItem{{ID: "one", Text: "first"}, {ID: "two", Text: "second"}}, Handled: []any{}}}}
	outcome, err := persist.WriteWithOutcome(store, "pi-session-queues.json", candidate, nil, persist.PublishFaults{FailDirSync: errors.New("injected dir-sync failure")})
	if err == nil || outcome.Kind != persist.OutcomeDurabilityUncertain || !outcome.PrimaryVisible {
		t.Fatalf("post-rename dir-sync must be durability-uncertain: %#v %v", outcome, err)
	}
	decision := controller.DecideQueuePublish(outcome)
	if decision.MayDispatch || !decision.MustReconcile || !decision.MustRetainMutation {
		t.Fatalf("uncertain queue publish must block dispatch: %#v", decision)
	}
	summary, err := controller.ReconcileQueueAfterPublish(store, outcome)
	if err != nil {
		t.Fatal(err)
	}
	if summary.RecordCount != 1 || summary.FollowUpCount != 2 {
		t.Fatalf("reconcile must read the visible candidate: %#v", summary)
	}
	primary, err := os.ReadFile(filepath.Join(dir, "pi-session-queues.json"))
	if err != nil || !strings.Contains(string(primary), `"second"`) {
		t.Fatal("reconcile must not overwrite the visible candidate with the older backup")
	}
	if err := os.WriteFile(filepath.Join(dir, "pi-session-queues.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.ReconcileQueueAfterPublish(store, outcome); err == nil {
		t.Fatal("corrupt queue primary must keep an uncertain outcome unresolved")
	}
}

func TestSchedulePostReplyUncertainBlocksDispatch(t *testing.T) {
	dir := t.TempDir()
	store := persist.Store{Dir: dir}
	job := controller.Schedule{ID: "job", ProjectID: "project", Root: "/project", Prompt: "Review", Cron: "0 9 * * *", Timezone: "Europe/Warsaw", NextRun: time.Now().Add(time.Hour), Runs: []controller.ScheduleRun{}}
	outcome, err := persist.WriteWithOutcome(store, "schedules.json", map[string]controller.Schedule{"job": job}, nil, persist.PublishFaults{FailReply: errors.New("injected reply loss")})
	if err == nil || outcome.Kind != persist.OutcomeDurabilityUncertain {
		t.Fatalf("reply loss must stay durability-uncertain: %#v %v", outcome, err)
	}
	decision := controller.DecideSchedulePublish(outcome)
	if decision.MayDispatch || !decision.MustReconcile {
		t.Fatalf("uncertain schedule publish must not dispatch: %#v", decision)
	}
	summary, err := controller.ReconcileScheduleAfterPublish(store, outcome)
	if err != nil {
		t.Fatal(err)
	}
	if summary.JobCount != 1 || len(summary.IDs) != 1 || summary.IDs[0] != "job" {
		t.Fatalf("reconcile must preserve schedule identity: %#v", summary)
	}
	plan, err := controller.PlanScheduleMigration(summary)
	if err != nil || !plan.DispatchBlocked {
		t.Fatalf("reconciled schedule must never dispatch missed jobs: %#v %v", plan, err)
	}
}

func TestDeletionUncertainKeepsTombstoneFailClosed(t *testing.T) {
	dir := t.TempDir()
	store := persist.Store{Dir: dir}
	deletions := controller.NewSessionDeletions(store)
	opaque := "agent/session/" + strings.Repeat("opaque", 32)
	binding := "sha256:" + strings.Repeat("b", 64)
	if err := deletions.Request("project", opaque, binding); err != nil {
		t.Fatal(err)
	}
	if err := deletions.Confirm("project", opaque); err != nil {
		t.Fatal(err)
	}
	uncertain := persist.PublishOutcome{Kind: persist.OutcomeDurabilityUncertain, Stage: persist.StageDirSync, PrimaryVisible: true, MustReconcile: true}
	decision := controller.DecideDeletionPublish(uncertain)
	if decision.MayDispatch || !decision.MustReconcile || !decision.MustRetainMutation {
		t.Fatalf("uncertain deletion publish must stay blocked: %#v", decision)
	}
	summary, err := controller.ReconcileDeletionAfterPublish(store, uncertain)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Confirmed != 1 || len(summary.Tombstones) != 1 || summary.Tombstones[0].Phase != "confirmed" {
		t.Fatalf("reconcile must preserve the confirmed tombstone: %#v", summary)
	}
	if err := os.WriteFile(filepath.Join(dir, "pi-session-deletions.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.ReconcileDeletionAfterPublish(store, uncertain); err == nil {
		t.Fatal("corrupt deletion primary must keep an uncertain outcome unresolved instead of resurrecting backup")
	}
}
