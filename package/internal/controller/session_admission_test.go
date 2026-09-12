package controller

import (
	"testing"

	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

func TestFollowUpAdmissionTracksActiveWork(t *testing.T) {
	entry := newSessionEntry("session", "project", "/tmp/project", "", "token")
	entry.queue.FollowUp = []queuedFollowUp{{ID: "queued", Text: "continue"}}
	manager := &SessionManager{sessions: map[string]*sessionEntry{"session": entry}}

	if !manager.admitFollowUp("session", entry) {
		t.Fatal("follow-up was not admitted")
	}
	if manager.activeWork != 1 {
		t.Fatalf("active work after admission = %d, want 1", manager.activeWork)
	}
	manager.releaseWork(entry)
	if manager.activeWork != 0 {
		t.Fatalf("active work after release = %d, want 0", manager.activeWork)
	}
}

func TestSetLeasesRejectsResidentOverflow(t *testing.T) {
	root := t.TempDir()
	policy, err := workspace.NewPathPolicy([]string{root}, false)
	if err != nil {
		t.Fatal(err)
	}
	store := persist.Store{Dir: t.TempDir()}
	projects := workspace.NewProjects(store, policy)
	project, err := projects.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	records := NewSessionRecords(store)
	requested := make([]sessionLease, maxManagedResidents+1)
	for index := range requested {
		sessionID := "session-" + string(rune('a'+index))
		if err := records.Record(ProjectSessionRecord{ProjectID: project.ID, SessionID: sessionID, CWD: project.Roots[0]}); err != nil {
			t.Fatal(err)
		}
		requested[index] = sessionLease{ProjectID: project.ID, SessionID: sessionID}
	}
	manager := NewSessionManager(projects, policy, records, nil, nil, nil)
	if err := manager.SetLeases("client", 1, requested); err == nil {
		t.Fatal("lease rehydration exceeded resident capacity")
	}
	if len(manager.sessions) != 0 {
		t.Fatalf("lease rehydration created %d residents after rejection", len(manager.sessions))
	}
}
