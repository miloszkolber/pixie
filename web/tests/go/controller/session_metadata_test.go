package controller_test

import (
	"testing"

	"github.com/miloszkolber/pixie/internal/controller"
)

func ungroupedProjectKey(value string) *string {
	if value == "" {
		empty := ""
		// Empty string is an explicit ungrouped key; nil is the canonical form.
		// Tests cover both spellings.
		return &empty
	}
	return &value
}

func metadata(hostID, sessionID string, projectKey *string, updatedAt int64) controller.SessionMetadata {
	return controller.SessionMetadata{
		HostID:    hostID,
		SessionID: sessionID,
		ProjectID: projectKey,
		Title:     sessionID,
		UpdatedAt: updatedAt,
	}
}

func TestSessionMetadataProjectKeyNullable(t *testing.T) {
	if !controller.IsUngroupedProjectKey(nil) {
		t.Fatal("nil project key must mean ungrouped")
	}
	empty := ""
	if !controller.IsUngroupedProjectKey(&empty) {
		t.Fatal("empty project key must mean ungrouped")
	}
	grouped := "alpha"
	if controller.IsUngroupedProjectKey(&grouped) {
		t.Fatal("grouped project key reported as ungrouped")
	}
	if err := controller.ValidateOptionalProjectKey(nil); err != nil {
		t.Fatalf("nil project key rejected: %v", err)
	}
	if err := controller.ValidateOptionalProjectKey(&empty); err != nil {
		t.Fatalf("empty project key rejected: %v", err)
	}
	if err := controller.ValidateOptionalProjectKey(&grouped); err != nil {
		t.Fatalf("grouped project key rejected: %v", err)
	}
	for _, id := range []string{"a/b", "a\\b", " padded ", "a\x00b"} {
		value := id
		if err := controller.ValidateOptionalProjectKey(&value); err == nil {
			t.Fatalf("invalid project key accepted: %q", id)
		}
	}
}

func TestSessionMetadataUngroupedPartition(t *testing.T) {
	grouped := "a"
	sessions := []controller.SessionMetadata{
		metadata("", "grouped", &grouped, 30),
		metadata("", "null-key", nil, 40),
		metadata("", "empty-key", ungroupedProjectKey(""), 20),
		metadata("", "stray", ungroupedProjectKey("elsewhere"), 25),
	}
	catalog := controller.PartitionSessionMetadata(sessions, []string{"a"})
	if len(catalog.Grouped["a"]) != 1 || catalog.Grouped["a"][0].SessionID != "grouped" {
		t.Fatalf("grouped partition is wrong: %#v", catalog.Grouped)
	}
	got := []string{}
	for _, session := range catalog.Ungrouped {
		got = append(got, session.SessionID)
	}
	want := []string{"null-key", "stray", "empty-key"}
	if len(got) != len(want) {
		t.Fatalf("ungrouped partition length is wrong: %#v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("ungrouped partition order is wrong: %#v, want %#v", got, want)
		}
	}
	flat := []string{}
	for _, session := range catalog.Flat {
		flat = append(flat, session.SessionID)
	}
	wantFlat := []string{"null-key", "grouped", "stray", "empty-key"}
	if len(flat) != len(wantFlat) {
		t.Fatalf("flat order length is wrong: %#v", flat)
	}
	for index := range wantFlat {
		if flat[index] != wantFlat[index] {
			t.Fatalf("flat order is wrong: %#v, want %#v", flat, wantFlat)
		}
	}
	for _, session := range append(append([]controller.SessionMetadata{}, catalog.Ungrouped...), catalog.Flat...) {
		if session.ProjectIDOrEmpty() == "all-files" {
			t.Fatal("catalog fabricated a hidden all-files project")
		}
	}
}

func TestSessionMetadataHostSessionKey(t *testing.T) {
	grouped := "a"
	sessions := []controller.SessionMetadata{
		metadata("host-a", "dup", &grouped, 10),
		metadata("host-b", "dup", &grouped, 20),
		metadata("host-a", "dup", &grouped, 5),
	}
	catalog := controller.PartitionSessionMetadata(sessions, []string{"a"})
	if len(catalog.Flat) != 2 {
		t.Fatalf("duplicate native IDs across hosts collapsed: %#v", catalog.Flat)
	}
	if catalog.Flat[0].HostID != "host-b" || catalog.Flat[1].HostID != "host-a" {
		t.Fatalf("host/session order is wrong: %#v", catalog.Flat)
	}
	if key := controller.SessionMetadataKey("host-a", "dup"); key == controller.SessionMetadataKey("host-b", "dup") {
		t.Fatal("host/session keys collide across hosts")
	}
	if err := controller.ValidateSessionMetadataKey("host-a", "dup"); err != nil {
		t.Fatalf("valid session key rejected: %v", err)
	}
	if err := controller.ValidateSessionMetadataKey("host-a", ""); err == nil {
		t.Fatal("empty session key accepted")
	}
	if err := controller.ValidateSessionMetadataKey("host-a", "a\x00b"); err == nil {
		t.Fatal("NUL session key accepted")
	}
}

func TestSessionMetadataRecentFirstOrder(t *testing.T) {
	sessions := []controller.SessionMetadata{
		metadata("", "b", nil, 2),
		metadata("", "a", nil, 3),
		metadata("", "c", nil, 1),
	}
	sorted := controller.SortSessionMetadataRecentFirst(sessions)
	if sorted[0].SessionID != "a" || sorted[1].SessionID != "b" || sorted[2].SessionID != "c" {
		t.Fatalf("recent-first order is wrong: %#v", sorted)
	}
	if sessions[0].SessionID != "b" {
		t.Fatal("sort mutated its input")
	}
	tied := []controller.SessionMetadata{
		metadata("host-b", "s", nil, 5),
		metadata("host-a", "s", nil, 5),
	}
	ordered := controller.SortSessionMetadataRecentFirst(tied)
	if ordered[0].HostID != "host-a" {
		t.Fatalf("host tie-break is wrong: %#v", ordered)
	}
}

func TestSessionMetadataSelectedRunningPinning(t *testing.T) {
	sessions := make([]controller.SessionMetadata, 0, 8)
	for index := 0; index < 8; index++ {
		session := metadata("", string(rune('a'+index)), nil, int64(100-index))
		session.SessionID = string([]byte{byte('a' + index)})
		if index == 7 {
			session.IsStreaming = true
		}
		sessions = append(sessions, session)
	}
	sessions = controller.SortSessionMetadataRecentFirst(sessions)
	selectedKey := controller.SessionMetadataKey("", "h")
	visible, hidden := controller.SelectSessionMetadataSubset(sessions, selectedKey, 6, false)
	if len(visible) != 7 || hidden != 1 {
		t.Fatalf("running session was hidden: visible=%d hidden=%d", len(visible), hidden)
	}
	found := false
	for _, session := range visible {
		if session.SessionID == "h" {
			found = true
		}
	}
	if !found {
		t.Fatal("running session is missing from the visible subset")
	}
	expanded, expandedHidden := controller.SelectSessionMetadataSubset(sessions, selectedKey, 6, true)
	if len(expanded) != 8 || expandedHidden != 0 {
		t.Fatalf("expanded subset is wrong: visible=%d hidden=%d", len(expanded), expandedHidden)
	}
}

func TestSessionMetadataNativeTitles(t *testing.T) {
	if got := controller.DisplaySessionMetadataTitle("  Hello  "); got != "Hello" {
		t.Fatalf("native title was modified: %q", got)
	}
	if got := controller.DisplaySessionMetadataTitle("Duplicate"); got != "Duplicate" {
		t.Fatalf("duplicate native title was modified: %q", got)
	}
	for _, title := range []string{"", "   "} {
		if got := controller.DisplaySessionMetadataTitle(title); got != "Untitled chat" {
			t.Fatalf("missing title fallback is wrong: %q", got)
		}
	}
}

func TestSessionMetadataFromProjectRequiredSummary(t *testing.T) {
	grouped := controller.SessionSummary{SessionID: "s1", ProjectID: "a", Title: "Hello", UpdatedAt: 10}
	converted := controller.SessionMetadataFromSummary("host-a", grouped)
	if converted.HostID != "host-a" || converted.SessionID != "s1" || converted.ProjectIDOrEmpty() != "a" {
		t.Fatalf("grouped summary bridge is wrong: %#v", converted)
	}
	empty := controller.SessionSummary{SessionID: "s2", ProjectID: "", Title: "Hello", UpdatedAt: 10}
	ungrouped := controller.SessionMetadataFromSummary("", empty)
	if !controller.IsUngroupedProjectKey(ungrouped.ProjectID) {
		t.Fatalf("empty project summary did not become ungrouped: %#v", ungrouped)
	}
	archived := metadata("", "old", nil, 40)
	archived.Archived = true
	active := metadata("", "new", nil, 10)
	catalog := controller.PartitionSessionMetadata([]controller.SessionMetadata{archived, active}, []string{})
	if len(catalog.Flat) != 1 || catalog.Flat[0].SessionID != "new" {
		t.Fatalf("archived sessions leaked into the chats catalog: %#v", catalog.Flat)
	}
}
