package diagnostics_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/diagnostics"
)

func TestSupportSnapshotOmitsHostileDetailsAndBoundsEvents(t *testing.T) {
	active, retained := 3, 2
	hostileValues := []string{
		"Authorization: Basic YWxpY2U6YmFzaWMtcGFzcw==",
		"Cookie: session=hostile-cookie-value",
		"X-Opaque-Credential: opaque-credential-value",
		"Bearer bearer-token-value",
		"sk_live_hostile-api-token-value",
		"https://url-user:url-password@example.invalid/support?access_token=query-token-value",
		"/home/alice/.pi/agent/config.json",
		`C:\Users\alice\.pi\config.json`,
		"projectId=project-hostile-id",
		"sessionId=session-hostile-id",
		"requestId=request-hostile-id",
	}
	raw := strings.Join(hostileValues, " | ")
	ring := diagnostics.NewControllerEventRing()
	for index := 0; index < diagnostics.SupportSnapshotMaxEvents+4; index++ {
		ring.RecordRequest("session.delete", fmt.Errorf("request %d failed: %s %s", index, raw, strings.Repeat("x", 400)))
	}

	payload, err := diagnostics.MarshalSupportSnapshot(diagnostics.SupportSnapshotRuntime{
		Build: diagnostics.BuildInfo{Version: raw, Revision: raw},
		Host: diagnostics.SupportSnapshotHost{
			Configured:        boolPointer(true),
			Reachable:         boolPointer(false),
			ApplicationReady:  boolPointer(false),
			Reason:            raw,
			ApplicationReason: raw,
		},
		ActiveRunCount:        &active,
		RetainedDeletionCount: &retained,
		Schedule: diagnostics.SupportSnapshotSchedule{
			State:  "degraded",
			Reason: raw,
		},
	}, ring.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > diagnostics.SupportSnapshotMaxBytes {
		t.Fatalf("snapshot has %d bytes, limit is %d", len(payload), diagnostics.SupportSnapshotMaxBytes)
	}
	text := string(payload)
	for _, forbidden := range append(hostileValues, "YWxpY2U6YmFzaWMtcGFzcw==", "hostile-cookie-value", "opaque-credential-value", "bearer-token-value", "hostile-api-token-value", "url-user", "url-password", "example.invalid", "query-token-value", "alice", "project-hostile-id", "session-hostile-id", "request-hostile-id") {
		if strings.Contains(text, forbidden) {
			t.Fatalf("support snapshot leaked %q: %s", forbidden, text)
		}
	}

	var snapshot diagnostics.SupportSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.SchemaVersion != diagnostics.SupportSnapshotSchemaVersion || len(snapshot.Events) != diagnostics.SupportSnapshotMaxEvents {
		t.Fatalf("snapshot bounds = %#v", snapshot)
	}
	if snapshot.Runtime.Build.Version != "0.0.0-dev" || snapshot.Runtime.Build.Revision != "unknown" || snapshot.Runtime.Host.Reason != "host.unreachable" || snapshot.Runtime.Host.ApplicationReason != "application.unavailable" || snapshot.Runtime.Schedule.Reason != "schedule.degraded" {
		t.Fatalf("support snapshot diagnostics = %#v", snapshot.Runtime)
	}
	if snapshot.Runtime.ActiveRunCount == nil || *snapshot.Runtime.ActiveRunCount != active || snapshot.Runtime.RetainedDeletionCount == nil || *snapshot.Runtime.RetainedDeletionCount != retained {
		t.Fatalf("snapshot runtime counts = %#v", snapshot.Runtime)
	}
	for _, event := range snapshot.Events {
		if event.Kind != "request" || event.Operation != "session.delete" || event.Outcome != "failed" {
			t.Fatalf("unexpected event = %#v", event)
		}
		if event.Detail != "request.failed" {
			t.Fatalf("event detail = %q, want bounded failure code", event.Detail)
		}
		if _, err := time.Parse(time.RFC3339, event.At); err != nil {
			t.Fatalf("event timestamp = %q: %v", event.At, err)
		}
	}
}

func TestSupportSnapshotNormalizesDirectlySuppliedEventFacts(t *testing.T) {
	rawID := "project-hostile-id"
	rawError := "https://url-user:url-password@example.invalid/?token=query-token-value " + rawID
	payload, err := diagnostics.MarshalSupportSnapshot(diagnostics.SupportSnapshotRuntime{
		Build: diagnostics.BuildInfo{Version: "v1.2.3", Revision: strings.Repeat("a", 40)},
	}, []diagnostics.ControllerEvent{{
		At:        "2026-09-15T12:00:00Z",
		Kind:      "request",
		Operation: "project-" + rawID,
		Outcome:   "failed",
		Detail:    rawError,
	}, {
		At:        "2026-09-15T12:00:00Z",
		Kind:      "request",
		Operation: "session.delete",
		Outcome:   "succeeded",
		Detail:    rawError,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > diagnostics.SupportSnapshotMaxBytes {
		t.Fatalf("snapshot has %d bytes, limit is %d", len(payload), diagnostics.SupportSnapshotMaxBytes)
	}
	for _, forbidden := range []string{rawID, "url-user", "url-password", "example.invalid", "query-token-value"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("direct event leaked %q: %s", forbidden, payload)
		}
	}
	var snapshot diagnostics.SupportSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Runtime.Build.Version != "v1.2.3" || snapshot.Runtime.Build.Revision != strings.Repeat("a", 40) || len(snapshot.Events) != 2 || snapshot.Events[0].Operation != "unknown" || snapshot.Events[0].Detail != "request.failed" || snapshot.Events[1].Operation != "session.delete" || snapshot.Events[1].Detail != "" {
		t.Fatalf("normalized events = %#v", snapshot.Events)
	}
}

func TestSupportSnapshotDropsInvalidFactsRatherThanClaimingThem(t *testing.T) {
	negative, excessive := -1, 1_000_001
	payload, err := diagnostics.MarshalSupportSnapshot(diagnostics.SupportSnapshotRuntime{
		Build:                 diagnostics.BuildInfo{},
		ActiveRunCount:        &negative,
		RetainedDeletionCount: &excessive,
		Schedule:              diagnostics.SupportSnapshotSchedule{State: "untrusted-value", Reason: "not exported"},
	}, []diagnostics.ControllerEvent{{
		At:        "not-a-time",
		Kind:      "request",
		Operation: "session.delete",
		Outcome:   "failed",
		Detail:    "ignored",
	}})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot diagnostics.SupportSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Runtime.ActiveRunCount != nil || snapshot.Runtime.RetainedDeletionCount != nil || snapshot.Runtime.Schedule.State != "unknown" || snapshot.Runtime.Schedule.Reason != "" || len(snapshot.Events) != 0 {
		t.Fatalf("invalid snapshot facts were retained: %#v", snapshot)
	}
}

func boolPointer(value bool) *bool { return &value }

func TestSupportSnapshotSanitizesChildStderrAndEnvironmentIdentity(t *testing.T) {
	t.Setenv(diagnostics.BootIdentityEnvironment, "Bearer bearer-token-value")
	t.Setenv(diagnostics.RunIdentityEnvironment, "/home/alice/.pi")
	ringShell := map[string]string{
		"child stderr": "https://url-user:url-password@example.invalid/?token=query-token-value sk_live_hostile-api-token-value /home/alice/.pi/agent",
	}
	var entries []diagnostics.StderrEntry
	for _, text := range ringShell {
		entries = append(entries, diagnostics.StderrEntry{At: "2026-09-15T12:00:00Z", Text: text})
	}
	for index := 0; index < diagnostics.DefaultStderrMaxLines+8; index++ {
		entries = append(entries, diagnostics.StderrEntry{At: "2026-09-15T12:00:00Z", Text: fmt.Sprintf("safe line %d", index)})
	}
	payload, err := diagnostics.MarshalSupportSnapshot(diagnostics.SupportSnapshotRuntime{
		ChildStderr: &diagnostics.StderrSummary{Entries: entries, Dropped: 1},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, forbidden := range []string{"bearer-token-value", "url-user", "url-password", "example.invalid", "query-token-value", "hostile-api-token-value", "alice"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("support snapshot leaked %q: %s", forbidden, text)
		}
	}
	var snapshot diagnostics.SupportSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Identity != nil {
		t.Fatalf("hostile environment identity was exported: %#v", snapshot.Identity)
	}
	if snapshot.ChildStderr == nil || snapshot.ChildStderr.Retained > diagnostics.DefaultStderrMaxLines || snapshot.ChildStderr.Bytes > diagnostics.DefaultStderrMaxBytes {
		t.Fatalf("child stderr summary = %#v", snapshot.ChildStderr)
	}
}
