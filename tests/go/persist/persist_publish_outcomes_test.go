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

func TestQueueLedgerPublishPostRenameUncertainViaSave(t *testing.T) {
	for name, faults := range map[string]persist.PublishFaults{
		"DurabilityDirSync": {FailDirSync: errors.New("injected dir-sync failure")},
		"DurabilityReply":   {FailReply: errors.New("injected reply loss")},
	} {
		t.Run(name, func(t *testing.T) {
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
			queues := controller.NewSessionQueues(store)
			state, found, err := queues.Get("project", opaque)
			if err != nil || !found {
				t.Fatalf("queue get prior: found=%v err=%v", found, err)
			}
			state.Revision = "second"
			queues.SetPublishFaults(faults)
			if err := queues.Save("project", opaque, state); err == nil {
				t.Fatal("post-rename fault must block the queue publish")
			} else if !strings.Contains(err.Error(), "dir-sync") && !strings.Contains(err.Error(), "reply") {
				t.Fatalf("uncertain queue publish must report durability: %v", err)
			}
			uncertain := persist.PublishOutcome{Kind: persist.OutcomeDurabilityUncertain, Stage: persist.StageDirSync, PrimaryVisible: true, MustReconcile: true}
			decision := controller.DecideQueuePublish(uncertain)
			if decision.MayDispatch || !decision.MustReconcile || !decision.MustRetainMutation {
				t.Fatalf("uncertain queue publish must block dispatch and retain mutation: %#v", decision)
			}
			reconciled, found, err := queues.Get("project", opaque)
			if err != nil || !found || reconciled.Revision != "second" {
				t.Fatalf("uncertain queue candidate must stay visible for reconciliation: found=%v rev=%q err=%v", found, reconciled.Revision, err)
			}
			summary, err := controller.ReconcileQueueAfterPublish(store, uncertain)
			if err != nil {
				t.Fatal(err)
			}
			if summary.RecordCount != 1 || summary.FollowUpCount != 1 {
				t.Fatalf("reconcile must read the validated candidate: %#v", summary)
			}
			primary, err := os.ReadFile(filepath.Join(dir, "pi-session-queues.json"))
			if err != nil || !strings.Contains(string(primary), `"second"`) {
				t.Fatal("reconcile must not overwrite the visible candidate with the older backup")
			}
			queues.SetPublishFaults(persist.PublishFaults{})
			reconciled.Revision = "installed"
			if err := queues.Save("project", opaque, reconciled); err != nil {
				t.Fatalf("installed queue publish must succeed after faults clear: %v", err)
			}
		})
	}
}

func TestQueueLedgerPublishPreRenameKnownUncommittedPreservesPrior(t *testing.T) {
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
	queues := controller.NewSessionQueues(store)
	state, found, err := queues.Get("project", opaque)
	if err != nil || !found {
		t.Fatal("queue prior missing")
	}
	state.Revision = "candidate"
	queues.SetPublishFaults(persist.PublishFaults{FailPrimary: errors.New("injected primary failure")})
	if err := queues.Save("project", opaque, state); err == nil {
		t.Fatal("pre-rename fault must fail")
	}
	kept, found, err := queues.Get("project", opaque)
	if err != nil || !found || kept.Revision != "first" {
		t.Fatalf("known-uncommitted queue failure must preserve prior commit: %#v %v", kept, err)
	}
	queues.SetPublishFaults(persist.PublishFaults{})
	kept.Revision = "candidate"
	if err := queues.Save("project", opaque, kept); err != nil {
		t.Fatalf("retry after known-uncommitted must re-attempt: %v", err)
	}
}

func TestQueueLedgerForgetPostRenameUncertainStaysFiltered(t *testing.T) {
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
	seed := queueStore{Version: 1, Engine: "pi", Records: []queueRecord{
		{ProjectID: "project", SessionID: "agent/session/keep", Revision: "keep", FollowUp: []queuedItem{{ID: "one", Text: "keep"}}, Handled: []any{}},
		{ProjectID: "project", SessionID: "agent/session/drop", Revision: "drop", FollowUp: []queuedItem{{ID: "two", Text: "drop"}}, Handled: []any{}},
	}}
	if _, err := persist.WriteWithOutcome(store, "pi-session-queues.json", seed, nil, persist.PublishFaults{}); err != nil {
		t.Fatal(err)
	}
	queues := controller.NewSessionQueues(store)
	queues.SetPublishFaults(persist.PublishFaults{FailDirSync: errors.New("injected dir-sync failure")})
	if err := queues.Forget("project", "agent/session/drop"); err == nil {
		t.Fatal("uncertain queue forget must stay blocked")
	}
	uncertain := persist.PublishOutcome{Kind: persist.OutcomeDurabilityUncertain, Stage: persist.StageDirSync, PrimaryVisible: true, MustReconcile: true}
	summary, err := controller.ReconcileQueueAfterPublish(store, uncertain)
	if err != nil {
		t.Fatal(err)
	}
	if summary.RecordCount != 1 || len(summary.Sessions) != 1 || summary.Sessions[0] != "agent/session/keep" {
		t.Fatalf("uncertain forget must stay filtered on the visible primary: %#v", summary)
	}
	if _, found, err := queues.Get("project", "agent/session/drop"); err != nil || found {
		t.Fatalf("dropped queue must not resurrect: found=%v err=%v", found, err)
	}
}

func TestDeletionLedgerPublishPostRenameUncertainViaJournal(t *testing.T) {
	for name, faults := range map[string]persist.PublishFaults{
		"DurabilityDirSync": {FailDirSync: errors.New("injected dir-sync failure")},
		"DurabilityReply":   {FailReply: errors.New("injected reply loss")},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			store := persist.Store{Dir: dir}
			deletions := controller.NewSessionDeletions(store)
			confirmedOpaque := "agent/session/" + strings.Repeat("c", 32)
			confirmedBinding := "sha256:" + strings.Repeat("c", 64)
			if err := deletions.Request("project", confirmedOpaque, confirmedBinding); err != nil {
				t.Fatal(err)
			}
			if err := deletions.Confirm("project", confirmedOpaque); err != nil {
				t.Fatal(err)
			}
			candidateOpaque := "agent/session/" + strings.Repeat("d", 32)
			candidateBinding := "sha256:" + strings.Repeat("d", 64)
			deletions.SetPublishFaults(faults)
			if err := deletions.Request("project", candidateOpaque, candidateBinding); err == nil {
				t.Fatal("post-rename deletion fault must block the publish")
			} else if !strings.Contains(err.Error(), "dir-sync") && !strings.Contains(err.Error(), "reply") {
				t.Fatalf("uncertain deletion publish must report durability: %v", err)
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
			if summary.Confirmed != 1 || summary.Requested != 1 {
				t.Fatalf("reconcile must preserve confirmed tombstone and visible candidate: %#v", summary)
			}
			foundCandidate := false
			for _, tombstone := range summary.Tombstones {
				if tombstone.SessionID == candidateOpaque && tombstone.Phase == "requested" {
					foundCandidate = true
				}
			}
			if !foundCandidate {
				t.Fatalf("uncertain deletion candidate must stay visible: %#v", summary)
			}
			// An identical retry reconciles the existing journal entry instead
			// of duplicating it, even while the fault is still armed.
			deletions.SetPublishFaults(persist.PublishFaults{})
			if err := deletions.Request("project", candidateOpaque, candidateBinding); err != nil {
				t.Fatalf("identical deletion retry must reconcile: %v", err)
			}
			after, err := controller.ReconcileDeletionAfterPublish(store, uncertain)
			if err != nil || len(after.Tombstones) != 2 {
				t.Fatalf("tombstones must survive reconciliation: %#v %v", after, err)
			}
		})
	}
}

func TestDeletionLedgerPublishPreRenameKnownUncommittedPreservesTombstones(t *testing.T) {
	dir := t.TempDir()
	store := persist.Store{Dir: dir}
	deletions := controller.NewSessionDeletions(store)
	opaque := "agent/session/" + strings.Repeat("e", 32)
	binding := "sha256:" + strings.Repeat("e", 64)
	if err := deletions.Request("project", opaque, binding); err != nil {
		t.Fatal(err)
	}
	candidateOpaque := "agent/session/" + strings.Repeat("f", 32)
	candidateBinding := "sha256:" + strings.Repeat("f", 64)
	deletions.SetPublishFaults(persist.PublishFaults{FailPrimary: errors.New("injected primary failure")})
	if err := deletions.Request("project", candidateOpaque, candidateBinding); err == nil {
		t.Fatal("pre-rename deletion fault must fail")
	}
	summary, err := controller.InspectDeletionLedger(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Tombstones) != 1 || summary.Tombstones[0].SessionID != opaque {
		t.Fatalf("known-uncommitted deletion failure must preserve prior tombstones: %#v", summary)
	}
}
