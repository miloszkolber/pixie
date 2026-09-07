package controller_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

func TestCorruptSettingsCannotBeReplacedWithDefaults(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	for _, name := range []string{"config.json", "config.json.bak"} {
		if err := os.WriteFile(filepath.Join(store.Dir, name), []byte("broken"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	settings := controller.NewSettings(store, nil)
	if _, err := settings.Get(); err == nil {
		t.Fatal("corruption was treated as first run")
	}
	if _, err := settings.SetModelVisibility("provider", "model", true); err == nil {
		t.Fatal("mutation overwrote damaged state")
	}
	for _, name := range []string{"config.json", "config.json.bak"} {
		data, err := os.ReadFile(filepath.Join(store.Dir, name))
		if err != nil || string(data) != "broken" {
			t.Fatalf("damaged file was changed: %s, %v", name, err)
		}
	}
}

func TestIndependentModelVisibilityChangesSurviveConcurrentClients(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	settings := controller.NewSettings(store, nil)
	var group sync.WaitGroup
	for _, model := range []string{"one", "two", "three", "four"} {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := settings.SetModelVisibility("provider", model, true); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	recovered, err := controller.NewSettings(store, nil).Get()
	if err != nil || len(recovered.HiddenModels) != 4 {
		t.Fatalf("independent updates lost: %#v, %v", recovered, err)
	}
}

func TestDurableSessionStateRecoversSafeDataAcrossRestart(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	opaqueSessionID := "agent/session/" + strings.Repeat("opaque", 32)
	records := controller.NewSessionRecords(store)
	if err := records.Record(controller.ProjectSessionRecord{ProjectID: "project", SessionID: opaqueSessionID, CWD: "/project"}); err != nil {
		t.Fatal(err)
	}
	if err := records.Record(controller.ProjectSessionRecord{ProjectID: "project", SessionID: "second", CWD: "/project", ParentSessionID: opaqueSessionID}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Dir, "pi-project-sessions.json"), []byte(`{"version":99,"engine":"pi","records":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := controller.NewSessionRecords(store).List()
	if err != nil || len(recovered) != 1 || recovered[0].SessionID != opaqueSessionID {
		t.Fatalf("session association recovery: %#v, %v", recovered, err)
	}

	objectives := controller.NewObjectives(store)
	goal := "Saved goal"
	if _, err := objectives.Update("project", opaqueSessionID, &goal, nil); err != nil {
		t.Fatal(err)
	}
	tasks := []controller.SessionTask{{ID: "one", Text: "  Keep parity  ", Status: "active"}}
	state, err := objectives.Update("project", opaqueSessionID, nil, &tasks)
	if err != nil || state.Goal == nil || *state.Goal != goal || len(state.Tasks) != 1 || state.Tasks[0].Text != "Keep parity" {
		t.Fatalf("partial objective update lost state: %#v, %v", state, err)
	}
	newer := "Newer goal"
	if _, err := objectives.Update("project", opaqueSessionID, &newer, nil); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(store.Dir, "extensions", "session-objectives", "*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("objective state path: %#v, %v", paths, err)
	}
	if err := os.WriteFile(paths[0], []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err = controller.NewObjectives(store).Get("project", opaqueSessionID)
	if err != nil || state.Goal == nil || *state.Goal != goal || len(state.Tasks) != 1 {
		t.Fatalf("objective backup recovery: %#v, %v", state, err)
	}
}

func TestSessionTitleProjectionIsDurable(t *testing.T) {
	store := persist.Store{Dir: t.TempDir()}
	records := controller.NewSessionRecords(store)
	if err := records.Record(controller.ProjectSessionRecord{ProjectID: "project", SessionID: "chat", CWD: "/project"}); err != nil {
		t.Fatal(err)
	}
	if err := records.SetTitle("project", "chat", "Remembered title"); err != nil {
		t.Fatal(err)
	}
	recovered, err := controller.NewSessionRecords(store).List()
	if err != nil || len(recovered) != 1 || recovered[0].Title != "Remembered title" {
		t.Fatalf("session title persistence: %#v, %v", recovered, err)
	}
}

func TestExecutionJournalsFailClosedInsteadOfReplayingBackups(t *testing.T) {
	t.Run("follow-up queue", func(t *testing.T) {
		store := persist.Store{Dir: t.TempDir()}
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
		first := queueStore{Version: 1, Engine: "pi", Records: []queueRecord{{
			ProjectID: "project", SessionID: opaque, Revision: "first", FollowUp: []queuedItem{{ID: "one", Text: "first"}}, Handled: []any{},
		}}}
		second := queueStore{Version: 1, Engine: "pi", Records: []queueRecord{{
			ProjectID: "project", SessionID: opaque, Revision: "second", FollowUp: []queuedItem{{ID: "one", Text: "first"}, {ID: "two", Text: "second"}}, Handled: []any{},
		}}}
		if err := persist.Write(store, "pi-session-queues.json", first, nil); err != nil {
			t.Fatal(err)
		}
		if err := persist.Write(store, "pi-session-queues.json", second, nil); err != nil {
			t.Fatal(err)
		}
		queues := controller.NewSessionQueues(store)
		state, found, err := queues.Get("project", opaque)
		if err != nil || !found || state.Revision != "second" || len(state.FollowUp) != 2 || state.FollowUp[1].Text != "second" {
			t.Fatalf("queue restore: found=%v state=%#v err=%v", found, state, err)
		}
		if err := os.WriteFile(filepath.Join(store.Dir, "pi-session-queues.json"), []byte("broken"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := controller.NewSessionQueues(store).List(); err == nil {
			t.Fatal("corrupt queue primary replayed its older backup")
		}
	})

	t.Run("deletion journal", func(t *testing.T) {
		store := persist.Store{Dir: t.TempDir()}
		deletions := controller.NewSessionDeletions(store)
		opaque := "agent/session/" + strings.Repeat("opaque", 32)
		binding := "sha256:" + strings.Repeat("a", 64)
		if err := deletions.Request("project", opaque, "pi"); err == nil {
			t.Fatal("deletion accepted an unbound logical agent identity")
		}
		if err := deletions.Request("project", opaque, binding); err != nil {
			t.Fatal(err)
		}
		if err := deletions.Request("other", opaque, binding); err == nil {
			t.Fatal("deletion request crossed project ownership")
		}
		if err := deletions.Confirm("project", opaque); err != nil {
			t.Fatal(err)
		}
		pending, err := controller.NewSessionDeletions(store).List()
		if err != nil || len(pending) != 1 || pending[0].SessionID != opaque || pending[0].Phase != "confirmed" {
			t.Fatalf("deletion restart recovery: %#v, %v", pending, err)
		}
		if err := os.Remove(filepath.Join(store.Dir, "pi-session-deletions.json")); err != nil {
			t.Fatal(err)
		}
		if _, err := controller.NewSessionDeletions(store).List(); err == nil {
			t.Fatal("missing deletion primary replayed an older backup")
		}
	})
}
