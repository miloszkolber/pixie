package controller_test

import (
	"encoding/json"
	"github.com/miloszkolber/pixie/internal/controller"
	"testing"
)

func TestSetLeasesRestoresRememberedTitle(t *testing.T) {
	manager, _, project, store := newSessionManager(t, nil, nil)
	records := controller.NewSessionRecords(store)
	if err := records.Record(controller.ProjectSessionRecord{ProjectID: project.ID, SessionID: "chat", CWD: project.Roots[0], Title: "Remembered title"}); err != nil {
		t.Fatal(err)
	}
	handler := controller.CoreHandler{Sessions: manager}
	raw, _ := json.Marshal(map[string]any{"revision": 1, "sessions": []any{map[string]any{"projectId": project.ID, "sessionId": "chat"}}})
	if _, err := handler.Handle(t.Context(), "session.setLeases", raw, "client"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Messages(t.Context(), "chat", project.ID, project.Roots[0], "client")
	if err != nil {
		t.Fatal(err)
	}
	if summary := snapshot["summary"].(controller.SessionSummary); summary.Title != "Remembered title" {
		t.Fatalf("restored title: %#v", summary)
	}
}
