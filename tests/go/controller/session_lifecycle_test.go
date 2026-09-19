package controller_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

// AUX-26 decision: archive/unarchive stay explicitly unavailable. They write
// no durable archive marker and no host dispatch, so the reconciled list must
// keep the session present and unarchived across a restart.
func TestArchiveUnavailableKeepsListReconciledWithoutMarker(t *testing.T) {
	manager, _, project, store := newSessionManagerWithInitializeAndPublisher(t, nil, nil, bunHostInitializeResponse(), nil)
	ctx := t.Context()
	if err := manager.Archive(ctx, project.ID, "chat", project.Roots[0]); err == nil || !strings.Contains(err.Error(), "controller-owned") {
		t.Fatalf("archive did not fail closed: %v", err)
	}
	if err := manager.Unarchive(ctx, project.ID, "chat"); err == nil || !strings.Contains(err.Error(), "controller-owned") {
		t.Fatalf("unarchive did not fail closed: %v", err)
	}
	entries, err := os.ReadDir(store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(strings.ToLower(entry.Name()), "archive") {
			t.Fatalf("unavailable archive wrote a durable marker: %s", entry.Name())
		}
	}
	summaries, err := manager.List(ctx, project.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, summary := range summaries {
		if summary.SessionID != "chat" {
			continue
		}
		found = true
		if summary.Archived {
			t.Fatal("list reported an archived session without a durable marker")
		}
	}
	if !found {
		t.Fatal("unavailable archive removed the session from the reconciled list")
	}
}

// AUX-27 admission ordering: every rejection before durable admission must
// leave the session's native MCP authority intact. A Canvas capability attached
// for "chat" is the observable native authority; a pre-admission failure must
// leave it revocable, while the legacy path revoked it up front.
func TestDeleteUnsupportedCapabilityLeavesNativeAuthorityIntact(t *testing.T) {
	registry := readyCanvasRegistry(t, true)
	initialize := bunHostInitializeResponse()
	initialize["operationSet"].(map[string]bool)["session.delete"] = false
	recorder := &deletionMethodRecorder{}
	manager, _, project, _ := newSessionManagerWithInitializeAndPublisher(t, nil, nil, initialize, nil, recorder.observe)
	manager.SetMCPRegistry(registry)
	if _, err := registry.AttachCanvas("chat", 1); err != nil {
		t.Fatal(err)
	}
	err := manager.Delete(t.Context(), project.ID, "chat", project.Roots[0])
	if err == nil || !strings.Contains(err.Error(), "does not support session.delete") {
		t.Fatalf("unsupported deletion = %v", err)
	}
	if recorder.saw("session.delete") {
		t.Fatal("unsupported capability dispatched a native delete")
	}
	if revoked := registry.RevokeSession("chat"); revoked == 0 {
		t.Fatal("unsupported capability revoked native MCP authority")
	}
}

// A profile that never establishes the deletion capability is a pre-admission
// failure too: the controller must not touch existing native authority.
func TestDeleteProfileFailureLeavesNativeAuthorityIntact(t *testing.T) {
	registry := readyCanvasRegistry(t, true)
	// runtime.hello without a runtimeId is incompatible, so Profile fails
	// before capability, binding or journal admission.
	initialize := map[string]any{"protocolVersion": 1}
	recorder := &deletionMethodRecorder{}
	manager, _, project, _ := newSessionManagerWithInitializeAndPublisher(t, nil, nil, initialize, nil, recorder.observe)
	manager.SetMCPRegistry(registry)
	if _, err := registry.AttachCanvas("chat", 1); err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(t.Context(), project.ID, "chat", project.Roots[0]); err == nil {
		t.Fatal("deletion with an incompatible profile succeeded")
	}
	if recorder.saw("session.delete") {
		t.Fatal("incompatible profile dispatched a native delete")
	}
	if revoked := registry.RevokeSession("chat"); revoked == 0 {
		t.Fatal("incompatible profile revoked native MCP authority")
	}
}

// A deletion-journal write failure is also pre-admission: the intent is not
// durable, so native authority must survive and no delete may be dispatched.
func TestDeleteJournalFailureLeavesNativeAuthorityIntact(t *testing.T) {
	registry := readyCanvasRegistry(t, true)
	recorder := &deletionMethodRecorder{}
	manager, _, project, _ := newSessionManagerWithInitializeAndPublisher(t, nil, nil, deletionAuthorityHostResponse(), nil, recorder.observe)
	manager.SetMCPRegistry(registry)
	manager.SetDeletionPublishFaults(persist.PublishFaults{FailPrimary: errors.New("injected journal failure")})
	if _, err := registry.AttachCanvas("chat", 1); err != nil {
		t.Fatal(err)
	}
	err := manager.Delete(t.Context(), project.ID, "chat", project.Roots[0])
	if err == nil || !strings.Contains(err.Error(), "restart Pixie to reconcile") {
		t.Fatalf("journal failure = %v", err)
	}
	if recorder.saw("session.delete") {
		t.Fatal("journal failure dispatched a native delete")
	}
	if revoked := registry.RevokeSession("chat"); revoked == 0 {
		t.Fatal("journal failure revoked native MCP authority")
	}
}

// Once the intent is durable, deletion still revokes native authority and
// dispatches the host delete; the admitted record is the justification.
func TestDeleteAfterDurableAdmissionRevokesAndDispatches(t *testing.T) {
	registry := readyCanvasRegistry(t, true)
	recorder := &deletionMethodRecorder{}
	manager, _, project, _ := newSessionManagerWithInitializeAndPublisher(t, nil, nil, deletionAuthorityHostResponse(), nil, recorder.observe)
	manager.SetMCPRegistry(registry)
	if _, err := registry.AttachCanvas("chat", 1); err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(t.Context(), project.ID, "chat", project.Roots[0]); err != nil {
		t.Fatalf("admitted deletion failed: %v", err)
	}
	if !recorder.saw("session.delete") {
		t.Fatal("admitted deletion did not dispatch a native delete")
	}
	if revoked := registry.RevokeSession("chat"); revoked != 0 {
		t.Fatal("admitted deletion left native MCP authority live")
	}
}

// AUX-05: a duplicate registration for one native session path is an invariant
// conflict. The host fixture returns a fixed session ID, so a second create
// must fail closed with the typed code instead of overwriting the resident.
func TestCreateDuplicateRegistrationIsTypedConflict(t *testing.T) {
	manager, _, project, _ := newSessionManager(t, nil, nil)
	ctx := t.Context()
	if _, err := manager.Create(ctx, project.ID, project.Roots[0], nil, "", "client-a"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := manager.Create(ctx, project.ID, project.Roots[0], nil, "", "client-a")
	if err == nil {
		t.Fatal("duplicate create registration succeeded")
	}
	var coded interface{ ErrorCode() string }
	if !errors.As(err, &coded) || coded.ErrorCode() != "SESSION_REGISTRATION_CONFLICT" {
		t.Fatalf("duplicate create error = %v, want SESSION_REGISTRATION_CONFLICT", err)
	}
}

// AUX-14: the browser session.fork request carries an entryId for "edit from
// here". The handler must forward that intent to the host as an in-file branch
// request instead of dropping it and creating a new-file fork.
func TestSessionForkHandlerForwardsEntryIdToHost(t *testing.T) {
	var mu sync.Mutex
	var forkParams map[string]any
	manager, _, project, _ := newSessionManagerWithInitializeAndPublisher(t, nil, nil, bunHostInitializeResponse(), nil, func(method string, params map[string]any) {
		if method != "session.fork" {
			return
		}
		mu.Lock()
		forkParams = params
		mu.Unlock()
	})
	ctx := t.Context()
	handler := controller.CoreHandler{Sessions: manager}
	params, err := json.Marshal(map[string]string{"projectId": project.ID, "sessionId": "chat", "entryId": "entry-42"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.Handle(ctx, "session.fork", params, "client-a"); err != nil {
		t.Fatalf("branch request: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if forkParams == nil {
		t.Fatal("branch request never reached the host session.fork route")
	}
	if got, _ := forkParams["entryId"].(string); got != "entry-42" {
		t.Fatalf("host session.fork entryId = %q, want entry-42", got)
	}
}

// AUX-14: a new-file fork is a root in the controller's ancestry. The native
// session header owns the parent link, so the controller records no fabricated
// parent relation and only subagent sessions nest.
func TestForkRecordsARootWithoutControllerParentLink(t *testing.T) {
	manager, _, project, store := newSessionManager(t, nil, nil)
	ctx := t.Context()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	summary, err := manager.Fork(ctx, project.ID, "chat", project.Roots[0])
	if err != nil {
		t.Fatal(err)
	}
	if summary.ParentSessionID != "" {
		t.Fatalf("fork recorded a controller parent link: %q", summary.ParentSessionID)
	}
	records, err := controller.NewSessionRecords(store).List()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.SessionID == summary.SessionID && record.ParentSessionID != "" {
			t.Fatalf("fork persisted a controller parent link: %#v", record)
		}
	}
}

// AUX-14: an edit request must fail closed when the host cannot serve a fork
// route at all, rather than silently producing an independent session.
func TestBranchFailsClosedWhenHostLacksForkRoute(t *testing.T) {
	initialize := bunHostInitializeResponse()
	initialize["operationSet"].(map[string]bool)["session.fork"] = false
	manager, _, project, _ := newSessionManagerWithInitialize(t, nil, nil, initialize)
	ctx := t.Context()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	_, err := manager.Branch(ctx, project.ID, "chat", project.Roots[0], "entry-1")
	if err == nil {
		t.Fatal("branch succeeded on a host without a fork route")
	}
	var coded interface{ ErrorCode() string }
	if !errors.As(err, &coded) || coded.ErrorCode() != "UNSUPPORTED_AGENT_CAPABILITY" {
		t.Fatalf("branch failure = %v, want a typed unsupported-capability error", err)
	}
}

// The Bun host negotiates no archive route, so unarchive must fail closed
// without dispatching a host method or emitting an unarchived event.
func TestUnarchiveFailsClosedWithoutHostDispatch(t *testing.T) {
	recorder := &deletionMethodRecorder{}
	events := make(chan publishedEvent, 8)
	manager, _, project, _ := newSessionManagerWithInitializeAndPublisher(t, nil, nil, bunHostInitializeResponse(), func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	}, recorder.observe)
	if err := manager.Unarchive(t.Context(), project.ID, "chat"); err == nil || !strings.Contains(err.Error(), "controller-owned") {
		t.Fatalf("unarchive did not fail closed: %v", err)
	}
	if recorder.saw("pi.session.unarchive") || recorder.saw("session.unarchive") {
		t.Fatal("failed unarchive dispatched a host archive route")
	}
	select {
	case published := <-events:
		t.Fatalf("failed unarchive emitted a lifecycle event: %#v", published)
	case <-time.After(50 * time.Millisecond):
	}
}

// Uncertain journal tombstones must surface for reconciliation even before
// restart recovery quarantines them, and reading them must never clear them.
func TestDeletionReconciliationSurfacesUncertainJournal(t *testing.T) {
	manager, _, project, store := newSessionManager(t, nil, nil)
	deletions := controller.NewSessionDeletions(store)
	if err := deletions.Request(project.ID, "chat", fixtureDeletionAgentBinding(t)); err != nil {
		t.Fatal(err)
	}
	first := manager.DeletionRecoveryStatus()
	if len(first) != 1 || first[0].SessionID != "chat" {
		t.Fatalf("uncertain journal tombstone was not projected: %#v", first)
	}
	reconciled := manager.DeletionReconciliationStatus()
	if len(reconciled) != 1 || reconciled[0].SessionID != "chat" || reconciled[0].Remediation == "" {
		t.Fatalf("reconciliation lacks remediation: %#v", reconciled)
	}
	second := manager.DeletionRecoveryStatus()
	if len(second) != 1 {
		t.Fatalf("reconciliation read cleared the tombstone: %#v", second)
	}
	pending, err := controller.NewSessionDeletions(store).List()
	if err != nil || len(pending) != 1 {
		t.Fatalf("tombstone was not retained: %#v %v", pending, err)
	}
}

// Retain is an explicit no-op: it keeps the tombstone in both the quarantine
// and the journal, while confirm finishes it through the existing path.
func TestDeletionRetainKeepsTombstoneThenConfirmFinishes(t *testing.T) {
	manager, _, project, store := newSessionManager(t, nil, nil)
	if err := controller.NewSessionDeletions(store).Request(project.ID, "chat", "sha256:"+strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecoverDeletions(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(manager.DeletionRecoveryStatus()) != 1 {
		t.Fatal("quarantine was not projected")
	}
	if err := manager.RetainExternalDeletion(project.ID, "chat"); err != nil {
		t.Fatalf("retain rejected a retained tombstone: %v", err)
	}
	if len(manager.DeletionRecoveryStatus()) != 1 {
		t.Fatal("retain cleared the tombstone")
	}
	pending, err := controller.NewSessionDeletions(store).List()
	if err != nil || len(pending) != 1 {
		t.Fatalf("retain did not keep the journal tombstone: %#v %v", pending, err)
	}
	if err := manager.ConfirmExternalDeletion(project.ID, "chat"); err != nil {
		t.Fatalf("confirm failed: %v", err)
	}
	if len(manager.DeletionRecoveryStatus()) != 0 {
		t.Fatal("confirmed deletion stayed projected")
	}
	if err := manager.RetainExternalDeletion(project.ID, "chat"); err == nil {
		t.Fatal("retain accepted a missing tombstone")
	}
}

// Retain is a controller operation, not a browser-local no-op. Its successful
// acknowledgement leaves the durable tombstone available for later recovery.
func TestRetainExternalDeletionAcknowledgesWithoutRemovingTombstone(t *testing.T) {
	manager, _, project, store := newSessionManager(t, nil, nil)
	if err := controller.NewSessionDeletions(store).Request(project.ID, "chat", fixtureDeletionAgentBinding(t)); err != nil {
		t.Fatal(err)
	}
	handler := controller.CoreHandler{Sessions: manager}
	result, err := handler.Handle(t.Context(), "session.retainExternalDeletion", json.RawMessage(`{"projectId":"`+project.ID+`","sessionId":"chat"}`), "test-client")
	if err != nil {
		t.Fatalf("retain handler: %v", err)
	}
	ack, ok := result.(map[string]bool)
	if !ok || !ack["ok"] {
		t.Fatalf("retain result = %#v", result)
	}
	if projected := manager.DeletionRecoveryStatus(); len(projected) != 1 || projected[0].SessionID != "chat" {
		t.Fatalf("retain acknowledgement removed the record: %#v", projected)
	}
	pending, err := controller.NewSessionDeletions(store).List()
	if err != nil || len(pending) != 1 {
		t.Fatalf("retain acknowledgement removed the durable tombstone: %#v %v", pending, err)
	}
}

// Requested recovery has no cwd in its journal record. It must therefore find
// exactly the persisted association and re-admit its unchanged, non-symlink
// directory before it even asks the fake host for an authority profile.
func TestRequestedDeletionRecoveryQuarantinesInvalidSessionAssociationBeforeHostCall(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*controller.SessionRecords, string, string) error
		wantReason string
	}{
		{
			name: "missing association",
			mutate: func(records *controller.SessionRecords, projectID, _ string) error {
				return records.Forget(projectID, "chat")
			},
			wantReason: "association is missing",
		},
		{
			name: "project mismatch",
			mutate: func(records *controller.SessionRecords, _, cwd string) error {
				return records.Record(controller.ProjectSessionRecord{ProjectID: "other-project", SessionID: "chat", CWD: cwd})
			},
			wantReason: "does not match the deletion project",
		},
		{
			name: "missing cwd",
			mutate: func(records *controller.SessionRecords, projectID, cwd string) error {
				return records.Record(controller.ProjectSessionRecord{ProjectID: projectID, SessionID: "chat", CWD: filepath.Join(cwd, "gone")})
			},
			wantReason: "cwd is unavailable",
		},
		{
			name: "symlink cwd",
			mutate: func(records *controller.SessionRecords, projectID, cwd string) error {
				target := filepath.Join(cwd, "target")
				link := filepath.Join(cwd, "link")
				if err := os.Mkdir(target, 0o700); err != nil {
					return err
				}
				if err := os.Symlink(target, link); err != nil {
					return err
				}
				return records.Record(controller.ProjectSessionRecord{ProjectID: projectID, SessionID: "chat", CWD: link})
			},
			wantReason: "non-symlink directory",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &deletionMethodRecorder{}
			manager, _, project, store := newSessionManagerWithInitializeAndPublisher(t, nil, nil, deletionAuthorityHostResponse(), nil, recorder.observe)
			records := controller.NewSessionRecords(store)
			if err := test.mutate(records, project.ID, project.Roots[0]); err != nil {
				t.Fatal(err)
			}
			if err := controller.NewSessionDeletions(store).Request(project.ID, "chat", fixtureDeletionAgentBinding(t)); err != nil {
				t.Fatal(err)
			}
			if err := manager.RecoverDeletions(t.Context()); err != nil {
				t.Fatal(err)
			}
			if recorder.saw("runtime.hello") || recorder.saw("session.delete") {
				t.Fatal("invalid persisted association reached host authority or delete dispatch")
			}
			status := manager.DeletionRecoveryStatus()
			if len(status) != 1 || !strings.Contains(status[0].Reason, test.wantReason) {
				t.Fatalf("recovery status = %#v, want reason containing %q", status, test.wantReason)
			}
			pending, err := controller.NewSessionDeletions(store).List()
			if err != nil || len(pending) != 1 || pending[0].Phase != "requested" {
				t.Fatalf("invalid association did not retain requested tombstone: %#v %v", pending, err)
			}
		})
	}
}

// The fake host models only the controller protocol. It establishes that an
// exact admitted association remains replayable; it is not evidence that a
// live native Pi currently supports session.delete.
func TestRequestedDeletionRecoveryDispatchesOnlyAfterExactCWDReadmission(t *testing.T) {
	recorder := &deletionMethodRecorder{}
	manager, _, project, store := newSessionManagerWithInitializeAndPublisher(t, nil, nil, deletionAuthorityHostResponse(), nil, recorder.observe)
	if err := controller.NewSessionDeletions(store).Request(project.ID, "chat", fixtureDeletionAgentBinding(t)); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecoverDeletions(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !recorder.saw("session.delete") {
		t.Fatal("valid admitted association did not reach the fake delete endpoint")
	}
	pending, err := controller.NewSessionDeletions(store).List()
	if err != nil || len(pending) != 0 {
		t.Fatalf("successful requested recovery left a tombstone: %#v %v", pending, err)
	}
}

func TestRequestedDeletionRecoveryReplaysValidUngroupedAssociation(t *testing.T) {
	recorder := &deletionMethodRecorder{}
	manager, _, project, store := newSessionManagerWithInitializeAndPublisher(t, nil, nil, deletionAuthorityHostResponse(), nil, recorder.observe)
	if err := controller.NewSessionRecords(store).Record(controller.ProjectSessionRecord{ProjectID: "", SessionID: "ungrouped", CWD: project.Roots[0]}); err != nil {
		t.Fatal(err)
	}
	if err := controller.NewSessionDeletions(store).Request("", "ungrouped", fixtureDeletionAgentBinding(t)); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecoverDeletions(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !recorder.saw("session.delete") {
		t.Fatal("valid ungrouped association did not reach the fake delete endpoint")
	}
	if pending, err := controller.NewSessionDeletions(store).List(); err != nil || len(pending) != 0 {
		t.Fatalf("successful ungrouped recovery left a tombstone: %#v %v", pending, err)
	}
}

func TestConfirmedExternalDeletionCompletesLocalCleanupWithoutCWDAdmission(t *testing.T) {
	recorder := &deletionMethodRecorder{}
	manager, _, project, store := newSessionManagerWithInitializeAndPublisher(t, nil, nil, bunHostInitializeResponse(), nil, recorder.observe)
	if err := controller.NewSessionRecords(store).Record(controller.ProjectSessionRecord{
		ProjectID: project.ID,
		SessionID: "chat",
		CWD:       filepath.Join(project.Roots[0], "unmounted"),
	}); err != nil {
		t.Fatal(err)
	}
	goal := "clean this local state"
	if _, err := controller.NewObjectives(store).Update(project.ID, "chat", &goal, nil); err != nil {
		t.Fatal(err)
	}
	if err := controller.NewSessionDeletions(store).Request(project.ID, "chat", fixtureDeletionAgentBinding(t)); err != nil {
		t.Fatal(err)
	}
	if err := manager.ConfirmExternalDeletion(project.ID, "chat"); err != nil {
		t.Fatalf("confirm must not require cwd admission: %v", err)
	}
	if recorder.saw("runtime.hello") || recorder.saw("session.delete") {
		t.Fatal("external confirmation contacted the fake host")
	}
	if records, err := controller.NewSessionRecords(store).List(); err != nil || len(records) != 0 {
		t.Fatalf("confirmed deletion left a session association: %#v %v", records, err)
	}
	if objective, err := controller.NewObjectives(store).Get(project.ID, "chat"); err != nil || objective.Goal != nil {
		t.Fatalf("confirmed deletion left an objective: %#v %v", objective, err)
	}
	if pending, err := controller.NewSessionDeletions(store).List(); err != nil || len(pending) != 0 {
		t.Fatalf("confirmed deletion left a tombstone: %#v %v", pending, err)
	}
}

func TestRetainExternalDeletionAcknowledgesUngroupedTombstoneWithoutMutation(t *testing.T) {
	manager, _, project, store := newSessionManager(t, nil, nil)
	if err := controller.NewSessionRecords(store).Record(controller.ProjectSessionRecord{ProjectID: "", SessionID: "ungrouped", CWD: project.Roots[0]}); err != nil {
		t.Fatal(err)
	}
	deletions := controller.NewSessionDeletions(store)
	if err := deletions.Request("", "ungrouped", fixtureDeletionAgentBinding(t)); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(store.Dir, "pi-session-deletions.json")
	before, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	handler := controller.CoreHandler{Sessions: manager}
	result, err := handler.Handle(t.Context(), "session.retainExternalDeletion", json.RawMessage(`{"projectId":"","sessionId":"ungrouped"}`), "test-client")
	if err != nil {
		t.Fatalf("retain ungrouped tombstone: %v", err)
	}
	if ack, ok := result.(map[string]bool); !ok || !ack["ok"] {
		t.Fatalf("retain result = %#v", result)
	}
	after, err := os.ReadFile(journal)
	if err != nil || string(after) != string(before) {
		t.Fatalf("retain changed the ungrouped tombstone: %q %v", after, err)
	}
	if _, err := handler.Handle(t.Context(), "session.retainExternalDeletion", json.RawMessage(`{"sessionId":"ungrouped"}`), "test-client"); err == nil {
		t.Fatal("retain accepted a missing projectId as an ungrouped target")
	}
}

// Confirm is destructive local reconciliation, so the caller must select a
// project scope explicitly. Missing and null are not the ungrouped sentinel.
func TestConfirmExternalDeletionRejectsMissingProjectIDWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name   string
		params json.RawMessage
	}{
		{name: "omitted", params: json.RawMessage(`{"sessionId":"chat"}`)},
		{name: "null", params: json.RawMessage(`{"projectId":null,"sessionId":"chat"}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, _, project, store := newSessionManager(t, nil, nil)
			goal := "keep this local state"
			if _, err := controller.NewObjectives(store).Update(project.ID, "chat", &goal, nil); err != nil {
				t.Fatal(err)
			}
			if err := controller.NewSessionDeletions(store).Request(project.ID, "chat", fixtureDeletionAgentBinding(t)); err != nil {
				t.Fatal(err)
			}
			journal := filepath.Join(store.Dir, "pi-session-deletions.json")
			before, err := os.ReadFile(journal)
			if err != nil {
				t.Fatal(err)
			}

			handler := controller.CoreHandler{Sessions: manager}
			if _, err := handler.Handle(t.Context(), "session.confirmExternalDeletion", test.params, "test-client"); err == nil {
				t.Fatal("confirmation accepted an unspecified project scope")
			}

			after, err := os.ReadFile(journal)
			if err != nil || string(after) != string(before) {
				t.Fatalf("invalid confirmation changed tombstone: %q %v", after, err)
			}
			if cwd, err := manager.RecordedCWD(project.ID, "chat"); err != nil || cwd != project.Roots[0] {
				t.Fatalf("invalid confirmation changed session association: %q %v", cwd, err)
			}
			objective, err := controller.NewObjectives(store).Get(project.ID, "chat")
			if err != nil || objective.Goal == nil || *objective.Goal != goal {
				t.Fatalf("invalid confirmation changed objective: %#v %v", objective, err)
			}
		})
	}
}

func TestConfirmExternalDeletionAcceptsExplicitProjectScope(t *testing.T) {
	for _, test := range []struct {
		name      string
		projectID func(string) string
		sessionID string
		addRecord bool
	}{
		{name: "grouped", projectID: func(id string) string { return id }, sessionID: "chat"},
		{name: "ungrouped", projectID: func(string) string { return "" }, sessionID: "ungrouped", addRecord: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, _, project, store := newSessionManager(t, nil, nil)
			projectID := test.projectID(project.ID)
			if test.addRecord {
				if err := controller.NewSessionRecords(store).Record(controller.ProjectSessionRecord{ProjectID: projectID, SessionID: test.sessionID, CWD: project.Roots[0]}); err != nil {
					t.Fatal(err)
				}
			}
			if err := controller.NewSessionDeletions(store).Request(projectID, test.sessionID, fixtureDeletionAgentBinding(t)); err != nil {
				t.Fatal(err)
			}
			params, err := json.Marshal(map[string]string{"projectId": projectID, "sessionId": test.sessionID})
			if err != nil {
				t.Fatal(err)
			}

			handler := controller.CoreHandler{Sessions: manager}
			result, err := handler.Handle(t.Context(), "session.confirmExternalDeletion", params, "test-client")
			if err != nil {
				t.Fatalf("confirm explicit project scope: %v", err)
			}
			if ack, ok := result.(map[string]bool); !ok || !ack["ok"] {
				t.Fatalf("confirm result = %#v", result)
			}
			if pending, err := controller.NewSessionDeletions(store).List(); err != nil || len(pending) != 0 {
				t.Fatalf("explicit confirmation left tombstone: %#v %v", pending, err)
			}
		})
	}
}

func TestAuthenticatedDiagnosticsWebSocketReturnsTypedData(t *testing.T) {
	activeCount, deletionCount := 0, 1
	handler := controller.CoreHandler{RuntimeDiagnostics: func(context.Context) controller.RuntimeDiagnosticsReport {
		return controller.RuntimeDiagnosticsReport{
			Capabilities: controller.RuntimeDiagnosticsCapabilities{
				Compatible:   boolPointer(true),
				OperationSet: map[string]bool{"session.delete": true},
			},
			Host: controller.RuntimeDiagnosticsHost{Configured: boolPointer(true), Reachable: boolPointer(true)},
			Runs: controller.RuntimeDiagnosticsRuns{ActiveCount: &activeCount},
			DeletionReconciliation: controller.RuntimeDiagnosticsDeletionReconciliation{
				Count: &deletionCount,
				Records: []controller.DeletionReconciliation{{
					DeletionRecovery: controller.DeletionRecovery{ProjectID: "project", SessionID: "chat", Phase: "requested", Reason: "uncertain"},
					Remediation:      "Verify the native session before confirming.",
					Uncertain:        true,
				}},
			},
			Schedule:    controller.RuntimeDiagnosticsSchedule{State: "healthy"},
			Remediation: []string{"Verify the retained deletion record."},
		}
	}}
	const token = "controller-token-0123456789abcdef0123456789"
	server, err := controller.NewWebSocketServer(handler, nil, controller.AuthConfig{Enabled: true, ControllerToken: token})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	host := httptest.NewServer(server)
	defer host.Close()
	setWebSocketListenerPort(t, server, host)
	auth, err := controller.NewAuth(token)
	if err != nil {
		t.Fatal(err)
	}
	cookie, ok := auth.Login(token)
	if !ok {
		t.Fatal("fixture login was rejected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(host.URL, "http")+"/?client=diagnostics", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {host.URL}, "Cookie": {controller.AuthCookieName + "=" + cookie}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"id":"diagnostics","method":"runtime.diagnostics","params":{}}`)); err != nil {
		t.Fatal(err)
	}
	_, raw, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		ID     string                              `json:"id"`
		OK     bool                                `json:"ok"`
		Result controller.RuntimeDiagnosticsReport `json:"result"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != "diagnostics" || !response.OK || response.Result.Runs.ActiveCount == nil || *response.Result.Runs.ActiveCount != 0 || response.Result.DeletionReconciliation.Count == nil || *response.Result.DeletionReconciliation.Count != 1 || len(response.Result.DeletionReconciliation.Records) != 1 || !response.Result.DeletionReconciliation.Records[0].Uncertain {
		t.Fatalf("diagnostics response = %#v", response)
	}
}

// Readiness is intentionally public for service managers, so it must never
// disclose the retained project/session tombstone it found during startup.
func TestPublicReadyzExcludesDeletionRecoveryDetails(t *testing.T) {
	dataDir, root := t.TempDir(), t.TempDir()
	policy, err := workspace.NewPathPolicy([]string{root}, false)
	if err != nil {
		t.Fatal(err)
	}
	store := persist.Store{Dir: dataDir}
	projects := workspace.NewProjects(store, policy)
	project, err := projects.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.NewSessionDeletions(store).Request(project.ID, "chat", fixtureDeletionAgentBinding(t)); err != nil {
		t.Fatal(err)
	}
	runtime := newMigrationRuntime(t, dataDir, policy)
	host, err := runtime.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(context.Background())
	response, err := http.Get(host + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("readyz status = %d", response.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 || body["ready"] != false {
		t.Fatalf("readyz exposed more than the readiness bit: %#v", body)
	}
	encoded, _ := json.Marshal(body)
	for _, forbidden := range []string{project.ID, "chat", "deletionRecovery", "diagnostics"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("readyz exposed deletion-recovery detail %q: %s", forbidden, encoded)
		}
	}
}

func boolPointer(value bool) *bool { return &value }

// Diagnostics remediation must stay actionable and secret-free while the
// pending deletion count is reflected.
func TestDeletionReconciliationRemediationMentionsConfirmRetain(t *testing.T) {
	manager, _, project, store := newSessionManager(t, nil, nil)
	if err := controller.NewSessionDeletions(store).Request(project.ID, "chat", "sha256:"+strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecoverDeletions(t.Context()); err != nil {
		t.Fatal(err)
	}
	reconciled := manager.DeletionReconciliationStatus()
	if len(reconciled) != 1 {
		t.Fatalf("reconciliation = %#v", reconciled)
	}
	hint := strings.ToLower(reconciled[0].Remediation)
	if !strings.Contains(hint, "confirm") || !strings.Contains(hint, "retain") {
		t.Fatalf("remediation does not guide confirm/retain: %q", reconciled[0].Remediation)
	}
}
