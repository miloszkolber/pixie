package persist_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

func TestMigrationBothTopologySwitchesDryRun(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	mappings := []string{"/var/lib/pixie:/data/pixie", "xdg-state:/data/state"}
	for _, direction := range []struct {
		from controller.MigrationTopology
		to   controller.MigrationTopology
	}{
		{controller.TopologyDockerPlusAssistant, controller.TopologyCombinedHost},
		{controller.TopologyCombinedHost, controller.TopologyDockerPlusAssistant},
	} {
		beforeSource := migrationListNames(t, source)
		beforeTarget := migrationListNames(t, target)
		plan, err := controller.PlanTopologySwitch(direction.from, direction.to, source, target, mappings)
		if err != nil {
			t.Fatalf("switch %s->%s: %v", direction.from, direction.to, err)
		}
		if !strings.Contains(plan.RedactedSource, "[redacted]") || !strings.Contains(plan.RedactedTarget, "[redacted]") {
			t.Fatalf("switch %s->%s must redact roots: %#v", direction.from, direction.to, plan)
		}
		if strings.Contains(plan.RedactedSource, source) || strings.Contains(plan.RedactedTarget, target) {
			t.Fatalf("switch %s->%s leaked absolute roots: %#v", direction.from, direction.to, plan)
		}
		if !plan.DispatchPausedRequired || !plan.OwnershipReleaseRequired || !plan.DispatchBlocked {
			t.Fatalf("switch %s->%s must require paused dispatch and verified ownership release: %#v", direction.from, direction.to, plan)
		}
		if len(plan.PathMappings) != len(mappings) {
			t.Fatalf("switch %s->%s lost explicit path mappings: %#v", direction.from, direction.to, plan)
		}
		if len(plan.Warnings) == 0 {
			t.Fatalf("switch %s->%s must warn about credentials and explicit resume: %#v", direction.from, direction.to, plan)
		}
		afterSource := migrationListNames(t, source)
		afterTarget := migrationListNames(t, target)
		if len(beforeSource) != len(afterSource) || len(beforeTarget) != len(afterTarget) {
			t.Fatalf("switch %s->%s dry-run must perform no writes", direction.from, direction.to)
		}
		if err := controller.ValidateTopologySwitchPreconditions(true, true); err != nil {
			t.Fatalf("switch %s->%s preconditions: %v", direction.from, direction.to, err)
		}
	}
	if _, err := controller.PlanTopologySwitch(controller.TopologyCombinedHost, controller.TopologyCombinedHost, source, target, mappings); err == nil {
		t.Fatal("same-topology switch must be rejected")
	}
	if err := controller.ValidateTopologySwitchPreconditions(false, true); err == nil {
		t.Fatal("unpaused dispatch must block switching")
	}
	if err := controller.ValidateTopologySwitchPreconditions(true, false); err == nil {
		t.Fatal("unverified ownership must block switching")
	}
	if _, err := controller.PlanTopologySwitch(controller.TopologyCombinedHost, controller.TopologyCombinedHost, source, target, nil); err == nil {
		t.Fatal("same-topology plan must be rejected even without mappings")
	}
	emptyMappings, err := controller.PlanTopologySwitch(controller.TopologyDockerPlusAssistant, controller.TopologyCombinedHost, source, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	foundMappingWarning := false
	for _, warning := range emptyMappings.Warnings {
		if strings.Contains(warning, "explicit container-volume") {
			foundMappingWarning = true
		}
	}
	if !foundMappingWarning {
		t.Fatalf("missing path mapping must warn instead of equating volumes: %#v", emptyMappings.Warnings)
	}
	// Topology helpers must never move native state on their own.
	if _, err := os.Stat(filepath.Join(source, "native-sessions")); !os.IsNotExist(err) {
		t.Fatal("topology dry-run must not create native state")
	}
	_ = persist.MigrationApplyVersion
}

func TestMigrationArchiveAssociationsStayVisible(t *testing.T) {
	before := []controller.ArchiveAssociation{
		{ProjectID: "project", SessionID: "agent/session/one", ParentSessionID: "", CWD: "/project", Title: "kept", Archived: false},
		{ProjectID: "project", SessionID: "agent/session/two", ParentSessionID: "agent/session/one", CWD: "/project", Title: "archived", Archived: true},
		{ProjectID: "", SessionID: "agent/session/ungrouped", ParentSessionID: "", CWD: "/elsewhere", Title: "ungrouped", Archived: false},
	}
	converted, warnings, err := controller.ConvertLegacyArchiveMetadata(before)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.ValidateArchivePreservation(before, converted); err != nil {
		t.Fatalf("identical archive conversion must preserve: %v", err)
	}
	foundUngrouped := false
	for _, warning := range warnings {
		if strings.Contains(warning, "ungrouped") {
			foundUngrouped = true
		}
	}
	if !foundUngrouped {
		t.Fatalf("ungrouped archive must stay ungrouped without a hidden project: %#v", warnings)
	}
	dangling := []controller.ArchiveAssociation{
		{ProjectID: "project", SessionID: "agent/session/child", ParentSessionID: "agent/session/missing", CWD: "/project", Archived: false},
	}
	kept, danglingWarnings, err := controller.ConvertLegacyArchiveMetadata(dangling)
	if err != nil || len(kept) != 1 || kept[0].ParentSessionID != "agent/session/missing" {
		t.Fatalf("dangling parent must be preserved without inventing a branch: %#v %v", kept, err)
	}
	foundDangling := false
	for _, warning := range danglingWarnings {
		if strings.Contains(warning, "dangling parent") {
			foundDangling = true
		}
	}
	if !foundDangling {
		t.Fatalf("dangling parent must warn: %#v", danglingWarnings)
	}
	synthesized := append(append([]controller.ArchiveAssociation(nil), before...), controller.ArchiveAssociation{ProjectID: "project", SessionID: "agent/session/invented", CWD: "/project"})
	if err := controller.ValidateArchivePreservation(before, synthesized); err == nil {
		t.Fatal("synthesized transcripts must conflict")
	}
	dropped := before[:2]
	if err := controller.ValidateArchivePreservation(before, dropped); err == nil {
		t.Fatal("dropped catalog associations must conflict")
	}
	flipped := append([]controller.ArchiveAssociation(nil), before...)
	flipped[1].Archived = false
	if err := controller.ValidateArchivePreservation(before, flipped); err == nil {
		t.Fatal("flipped archive flags must conflict")
	}
	rebranched := append([]controller.ArchiveAssociation(nil), before...)
	rebranched[1].ParentSessionID = "agent/session/other"
	if err := controller.ValidateArchivePreservation(before, rebranched); err == nil {
		t.Fatal("invented branches must conflict")
	}
	duplicate := append(append([]controller.ArchiveAssociation(nil), before...), controller.ArchiveAssociation{ProjectID: "other", SessionID: before[0].SessionID, CWD: "/project"})
	if err := controller.ValidateArchivePreservation(duplicate, duplicate); err == nil {
		t.Fatal("duplicate native IDs must block ambiguous actions")
	}
	conflicting := []controller.ArchiveAssociation{
		{ProjectID: "project", SessionID: "agent/session/one", CWD: "/project"},
		{ProjectID: "other", SessionID: "agent/session/one", CWD: "/project"},
	}
	if _, _, err := controller.ConvertLegacyArchiveMetadata(conflicting); err == nil {
		t.Fatal("conflicting legacy entries must conflict instead of merging")
	}
}

func TestMigrationUngroupedQueuesAndDeletionsPreserved(t *testing.T) {
	queues := []controller.QueueAssociation{
		{ProjectID: "project", SessionID: "s1"},
		{ProjectID: "", SessionID: "agent/session/ungrouped"},
	}
	grouped, ungrouped := controller.ClassifyUngroupedQueues(queues)
	if len(grouped) != 1 || len(ungrouped) != 1 {
		t.Fatalf("queue grouping split: grouped=%#v ungrouped=%#v", grouped, ungrouped)
	}
	if err := controller.ValidateUngroupedQueuePreservation(queues, queues); err != nil {
		t.Fatal(err)
	}
	lost := []controller.QueueAssociation{{ProjectID: "project", SessionID: "s1"}}
	if err := controller.ValidateUngroupedQueuePreservation(queues, lost); err == nil {
		t.Fatal("lost ungrouped queue must conflict")
	}
	fabricated := []controller.QueueAssociation{
		{ProjectID: "project", SessionID: "s1"},
		{ProjectID: "project", SessionID: "agent/session/ungrouped"},
	}
	if err := controller.ValidateUngroupedQueuePreservation(queues, fabricated); err == nil {
		t.Fatal("fabricated hidden project for ungrouped queue must conflict")
	}
	tombstones := []controller.DeletionTombstone{
		{ProjectID: "project", SessionID: "s1", Binding: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Phase: "confirmed"},
		{ProjectID: "", SessionID: "agent/session/ungrouped", Binding: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Phase: "requested"},
	}
	groupedDel, ungroupedDel := controller.ClassifyUngroupedDeletions(tombstones)
	if len(groupedDel) != 1 || len(ungroupedDel) != 1 {
		t.Fatalf("deletion grouping split: %#v %#v", groupedDel, ungroupedDel)
	}
	if err := controller.ValidateUngroupedDeletionPreservation(tombstones, tombstones); err != nil {
		t.Fatal(err)
	}
	droppedDel := tombstones[:1]
	if err := controller.ValidateUngroupedDeletionPreservation(tombstones, droppedDel); err == nil {
		t.Fatal("lost ungrouped tombstone must conflict")
	}
	flippedDel := append([]controller.DeletionTombstone(nil), tombstones...)
	flippedDel[1].Phase = "confirmed"
	if err := controller.ValidateUngroupedDeletionPreservation(tombstones, flippedDel); err == nil {
		t.Fatal("flipped ungrouped phase must conflict")
	}
}
