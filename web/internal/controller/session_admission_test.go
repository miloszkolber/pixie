package controller

import (
	"context"
	"testing"
	"time"

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

// AUX-19 internal: a durable follow-up that would become runnable after a drain
// must stay queued instead of dispatching through a settlement, attach or
// lifecycle path that never passed the browser admission boundary.
func TestFollowUpAdmissionRefusedAfterDrain(t *testing.T) {
	entry := newSessionEntry("session", "project", "/tmp/project", "", "token")
	entry.queue.FollowUp = []queuedFollowUp{{ID: "queued", Text: "continue"}}
	gate := NewAdmissionGate(0)
	manager := &SessionManager{sessions: map[string]*sessionEntry{"session": entry}, gate: gate}

	gate.BeginDrain()
	if manager.admitFollowUp("session", entry) {
		t.Fatal("a follow-up was admitted after the drain began")
	}
	if len(entry.queue.FollowUp) != 1 || entry.queue.FollowUp[0].ID != "queued" {
		t.Fatalf("refused follow-up changed the durable queue: %#v", entry.queue.FollowUp)
	}
	if manager.activeWork != 0 || entry.refs != 0 || entry.drainScheduled {
		t.Fatalf("refused follow-up left admission state: active=%d refs=%d scheduled=%v", manager.activeWork, entry.refs, entry.drainScheduled)
	}
	// Stop and read-only inspection stay available during a drain.
	if !gate.TryAdmit("session.abort") {
		t.Fatal("stop was refused while draining")
	}
}

// AUX-19 internal: an admitted follow-up run registers with the shared gate and
// keeps WaitForDrain waiting until runFollowUp settles and releases it.
func TestAdmittedFollowUpRunHoldsDrainUntilItSettles(t *testing.T) {
	entry := newSessionEntry("session", "project", "/tmp/project", "", "token")
	entry.queue.FollowUp = []queuedFollowUp{{ID: "queued", Text: "continue"}}
	gate := NewAdmissionGate(0)
	manager := &SessionManager{sessions: map[string]*sessionEntry{"session": entry}, gate: gate}

	if !manager.admitFollowUp("session", entry) {
		t.Fatal("follow-up was not admitted before the drain")
	}
	gate.BeginDrain()
	drained := make(chan struct{})
	go func() {
		gate.WaitForDrain(context.Background())
		close(drained)
	}()
	select {
	case <-drained:
		t.Fatal("WaitForDrain settled while an admitted follow-up run was still registered")
	case <-time.After(20 * time.Millisecond):
	}
	// Remove the projection so runFollowUp settles through its error path; the
	// admission must still be released.
	manager.mu.Lock()
	delete(manager.sessions, "session")
	manager.mu.Unlock()
	manager.runFollowUp("session", entry)
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForDrain did not observe the settled follow-up run")
	}
	if entry.drainScheduled {
		t.Fatal("settled follow-up left its admission flag set")
	}
	if manager.activeWork != 0 {
		t.Fatalf("settled follow-up left active work at %d", manager.activeWork)
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
