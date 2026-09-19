package persist_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/miloszkolber/pixie/internal/persist"
)

type outcomeDoc struct {
	Note string `json:"note"`
}

func mustWriteOutcome(t *testing.T, store persist.Store, name string, doc outcomeDoc) {
	t.Helper()
	if _, err := persist.WriteWithOutcome(store, name, doc, nil, persist.PublishFaults{}); err != nil {
		t.Fatal(err)
	}
}

func TestWriteWithOutcomeSuccessIsInstalled(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	outcome, err := persist.WriteWithOutcome(store, "doc.json", outcomeDoc{Note: "first"}, nil, persist.PublishFaults{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != persist.OutcomeInstalled || !outcome.PrimaryVisible || outcome.MustReconcileLedger() {
		t.Fatalf("success must be installed without reconcile: %#v", outcome)
	}
	if !outcome.MayDispatch() {
		t.Fatal("installed outcome must allow dependent work")
	}
	reconciled, err := persist.ReconcilePrimary[outcomeDoc](store, "doc.json", nil)
	if err != nil || reconciled.Note != "first" {
		t.Fatalf("reconcile validated primary: %#v %v", reconciled, err)
	}
}

func TestWriteWithOutcomePreRenameStaysKnownUncommitted(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	mustWriteOutcome(t, store, "doc.json", outcomeDoc{Note: "prior"})
	before, err := os.ReadFile(filepath.Join(store.Dir, "doc.json"))
	if err != nil {
		t.Fatal(err)
	}
	for stage, faults := range map[persist.PublishStage]persist.PublishFaults{
		persist.StageReserve: {FailReserve: errors.New("reserve unavailable")},
		persist.StageBackup:  {FailBackup: errors.New("backup unavailable")},
		persist.StagePrimary: {FailPrimary: errors.New("primary unavailable")},
	} {
		outcome, err := persist.WriteWithOutcome(store, "doc.json", outcomeDoc{Note: "candidate"}, nil, faults)
		if err == nil {
			t.Fatalf("stage %s must fail", stage)
		}
		if outcome.Kind != persist.OutcomeKnownUncommitted || outcome.PrimaryVisible || outcome.MustReconcileLedger() {
			t.Fatalf("stage %s must be known-uncommitted: %#v", stage, outcome)
		}
		if outcome.Stage != stage {
			t.Fatalf("stage %s reported as %s", stage, outcome.Stage)
		}
		if outcome.MayDispatch() {
			t.Fatalf("stage %s must not dispatch", stage)
		}
		after, err := os.ReadFile(filepath.Join(store.Dir, "doc.json"))
		if err != nil || string(after) != string(before) {
			t.Fatalf("stage %s changed the primary", stage)
		}
		reconciled, err := persist.ReconcilePrimary[outcomeDoc](store, "doc.json", nil)
		if err != nil || reconciled.Note != "prior" {
			t.Fatalf("stage %s must preserve prior commit: %#v %v", stage, reconciled, err)
		}
	}
}

func TestWriteWithOutcomePostRenameIsDurabilityUncertain(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	mustWriteOutcome(t, store, "doc.json", outcomeDoc{Note: "prior"})
	for stage, faults := range map[persist.PublishStage]persist.PublishFaults{
		persist.StageDirSync:     {FailDirSync: errors.New("dir sync lost")},
		persist.StageAcknowledge: {FailReply: errors.New("reply lost")},
	} {
		outcome, err := persist.WriteWithOutcome(store, "doc.json", outcomeDoc{Note: "candidate"}, nil, faults)
		if err == nil {
			t.Fatalf("stage %s must report an error", stage)
		}
		if outcome.Kind != persist.OutcomeDurabilityUncertain || !outcome.PrimaryVisible || !outcome.MustReconcileLedger() {
			t.Fatalf("stage %s must be durability-uncertain: %#v", stage, outcome)
		}
		if outcome.Stage != stage {
			t.Fatalf("stage %s reported as %s", stage, outcome.Stage)
		}
		if outcome.MayDispatch() {
			t.Fatalf("stage %s must not dispatch before reconcile", stage)
		}
		reconciled, err := persist.ReconcilePrimary[outcomeDoc](store, "doc.json", nil)
		if err != nil || reconciled.Note != "candidate" {
			t.Fatalf("stage %s must reconcile the visible candidate: %#v %v", stage, reconciled, err)
		}
		// Restore the prior value so the next fault starts from a known state.
		mustWriteOutcome(t, store, "doc.json", outcomeDoc{Note: "prior"})
	}
}

func TestReconcilePrimaryNeverFallsBackToBackup(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	mustWriteOutcome(t, store, "doc.json", outcomeDoc{Note: "one"})
	mustWriteOutcome(t, store, "doc.json", outcomeDoc{Note: "two"})
	if err := os.WriteFile(filepath.Join(store.Dir, "doc.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := persist.ReconcilePrimary[outcomeDoc](store, "doc.json", nil); err == nil {
		t.Fatal("corrupt primary must stay unresolved instead of replaying its older backup")
	}
	if _, err := os.Stat(filepath.Join(store.Dir, "doc.json")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(store.Dir, "doc.json"))
	if err != nil || string(raw) != "broken" {
		t.Fatal("reconcile must never write or restore a backup over the visible primary")
	}
}
