package controller

import (
	"fmt"
	"path/filepath"
	"reflect"
	"slices"

	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

const projectRootMigrationName = "project-root-migration.json"

type projectRootMigrationJournal struct {
	Version          int                              `json:"version"`
	Phase            string                           `json:"phase"`
	Roots            []workspace.ProjectRootMigration `json:"roots"`
	Moves            []projectRootSessionMove         `json:"moves"`
	Unresolved       []projectRootMigrationIssue      `json:"unresolved"`
	ConfirmedOrphans []projectRootConfirmedOrphan     `json:"confirmedOrphans"`
}

type projectRootSessionMove struct {
	SourceProjectID string `json:"sourceProjectId"`
	TargetProjectID string `json:"targetProjectId"`
	SessionID       string `json:"sessionId"`
	CWD             string `json:"cwd"`
}

type projectRootMigrationIssue struct {
	ProjectID string `json:"projectId"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	Reason    string `json:"reason"`
}

type projectRootConfirmedOrphan struct {
	ProjectID string `json:"projectId"`
	SessionID string `json:"sessionId"`
}

func migrateProjectRoots(projects *workspace.Projects, policy *workspace.PathPolicy, records *SessionRecords, queues *SessionQueues, objectives *Objectives, deletions *SessionDeletions, store persist.Store) error {
	journal, exists, err := readProjectRootMigration(store)
	if err != nil {
		return err
	}
	if !exists {
		err := projects.CoordinateLegacyMigration(func(roots []workspace.ProjectRootMigration) error {
			journal = projectRootMigrationJournal{Version: 1, Phase: "prepared", Roots: roots, Moves: []projectRootSessionMove{}, Unresolved: []projectRootMigrationIssue{}, ConfirmedOrphans: []projectRootConfirmedOrphan{}}
			if err := writeProjectRootMigration(store, journal); err != nil {
				return err
			}
			return applyProjectRootMigration(&journal, policy, records, queues, objectives, deletions, store)
		})
		if err != nil {
			return err
		}
		return removeStoredGenerations(storePath(store, projectRootMigrationName))
	}
	if err := applyProjectRootMigration(&journal, policy, records, queues, objectives, deletions, store); err != nil {
		return err
	}
	if err := projects.CoordinateLegacyMigration(func(roots []workspace.ProjectRootMigration) error {
		if !slices.EqualFunc(roots, journal.Roots, equalProjectRootMigration) {
			return fmt.Errorf("legacy project roots changed while session migration was incomplete")
		}
		return nil
	}); err != nil {
		return err
	}
	return removeStoredGenerations(storePath(store, projectRootMigrationName))
}

func applyProjectRootMigration(journal *projectRootMigrationJournal, policy *workspace.PathPolicy, records *SessionRecords, queues *SessionQueues, objectives *Objectives, deletions *SessionDeletions, store persist.Store) error {
	if journal.Phase == "prepared" {
		moves, unresolved, err := projectRootSessionMoves(journal.Roots, policy, records)
		if err != nil {
			return err
		}
		orphans, deletionIssues, err := projectRootDeletionPlan(journal.Roots, moves, deletions)
		if err != nil {
			return err
		}
		journal.Moves, journal.Unresolved, journal.ConfirmedOrphans = moves, append(unresolved, deletionIssues...), orphans
		if len(journal.Unresolved) > 0 {
			if err := writeProjectRootMigration(store, *journal); err != nil {
				return err
			}
			return fmt.Errorf("legacy project root migration has %d unresolved session association(s)", len(journal.Unresolved))
		}
		journal.Phase = "migrating"
		if err := writeProjectRootMigration(store, *journal); err != nil {
			return err
		}
	}
	if journal.Phase == "migrating" && journal.ConfirmedOrphans == nil {
		orphans, issues, err := projectRootDeletionPlan(journal.Roots, journal.Moves, deletions)
		if err != nil {
			return err
		}
		journal.ConfirmedOrphans, journal.Unresolved = orphans, appendProjectRootMigrationIssues(journal.Unresolved, issues)
		if len(issues) > 0 {
			if err := writeProjectRootMigration(store, *journal); err != nil {
				return err
			}
			return fmt.Errorf("legacy project root migration has %d unresolved session association(s)", len(journal.Unresolved))
		}
		if err := writeProjectRootMigration(store, *journal); err != nil {
			return err
		}
	}
	if journal.ConfirmedOrphans == nil {
		journal.ConfirmedOrphans = []projectRootConfirmedOrphan{}
	}
	if len(journal.Unresolved) > 0 {
		return fmt.Errorf("legacy project root migration has %d unresolved session association(s)", len(journal.Unresolved))
	}
	if journal.Phase != "migrating" {
		return fmt.Errorf("invalid legacy project root migration state")
	}
	if err := migrateSessionRecordProjects(records, journal.Moves); err != nil {
		return err
	}
	if err := migrateSessionQueueProjects(queues, journal.Moves); err != nil {
		return err
	}
	if err := migrateSessionObjectiveProjects(objectives, journal.Moves); err != nil {
		return err
	}
	if err := migrateSessionDeletionProjects(deletions, journal.Moves); err != nil {
		return err
	}
	return cleanupConfirmedOrphanDeletions(queues, objectives, deletions, journal.ConfirmedOrphans)
}

func appendProjectRootMigrationIssues(current, additions []projectRootMigrationIssue) []projectRootMigrationIssue {
	for _, addition := range additions {
		duplicate := false
		for _, existing := range current {
			if existing.ProjectID == addition.ProjectID && existing.SessionID == addition.SessionID && existing.CWD == addition.CWD && existing.Reason == addition.Reason {
				duplicate = true
				break
			}
		}
		if !duplicate {
			current = append(current, addition)
		}
	}
	return current
}

func projectRootSessionMoves(roots []workspace.ProjectRootMigration, policy *workspace.PathPolicy, records *SessionRecords) ([]projectRootSessionMove, []projectRootMigrationIssue, error) {
	stored, err := records.List()
	if err != nil {
		return nil, nil, err
	}
	moves := make([]projectRootSessionMove, 0, len(stored))
	unresolved := make([]projectRootMigrationIssue, 0)
	for _, record := range stored {
		var candidates []workspace.ProjectRootMigration
		for _, root := range roots {
			if root.SourceProjectID == record.ProjectID {
				candidates = append(candidates, root)
			}
		}
		if len(candidates) == 0 {
			continue
		}
		cwd, resolveErr := policy.Directory(record.CWD, "Session directory")
		if resolveErr != nil {
			unresolved = append(unresolved, projectRootMigrationIssue{ProjectID: record.ProjectID, SessionID: record.SessionID, CWD: record.CWD, Reason: "session directory is unavailable"})
			continue
		}
		var selected *workspace.ProjectRootMigration
		for index := range candidates {
			candidate := candidates[index]
			if !workspace.Within(candidate.Root, cwd) {
				continue
			}
			if selected == nil || len(candidate.Root) > len(selected.Root) {
				selected = &candidate
				continue
			}
			if len(candidate.Root) == len(selected.Root) && candidate.TargetProjectID != selected.TargetProjectID {
				selected = nil
				break
			}
		}
		if selected == nil {
			unresolved = append(unresolved, projectRootMigrationIssue{ProjectID: record.ProjectID, SessionID: record.SessionID, CWD: record.CWD, Reason: "session directory is outside or ambiguously matches legacy project roots"})
			continue
		}
		moves = append(moves, projectRootSessionMove{SourceProjectID: record.ProjectID, TargetProjectID: selected.TargetProjectID, SessionID: record.SessionID, CWD: cwd})
	}
	return moves, unresolved, nil
}

func projectRootDeletionPlan(roots []workspace.ProjectRootMigration, moves []projectRootSessionMove, deletions *SessionDeletions) ([]projectRootConfirmedOrphan, []projectRootMigrationIssue, error) {
	if deletions == nil {
		return []projectRootConfirmedOrphan{}, []projectRootMigrationIssue{}, nil
	}
	stored, err := deletions.List()
	if err != nil {
		return nil, nil, err
	}
	legacyProjects := make(map[string]bool, len(roots))
	for _, root := range roots {
		legacyProjects[root.SourceProjectID] = true
	}
	associated := make(map[string]bool, len(moves))
	for _, move := range moves {
		associated[queueRecordKey(move.SourceProjectID, move.SessionID)] = true
	}
	orphans := make([]projectRootConfirmedOrphan, 0)
	issues := make([]projectRootMigrationIssue, 0)
	for _, deletion := range stored {
		if !legacyProjects[deletion.ProjectID] || associated[queueRecordKey(deletion.ProjectID, deletion.SessionID)] {
			continue
		}
		if deletion.Phase == deletionConfirmed {
			orphans = append(orphans, projectRootConfirmedOrphan{ProjectID: deletion.ProjectID, SessionID: deletion.SessionID})
			continue
		}
		issues = append(issues, projectRootMigrationIssue{ProjectID: deletion.ProjectID, SessionID: deletion.SessionID, Reason: "requested deletion has no session association"})
	}
	return orphans, issues, nil
}

func migrateSessionRecordProjects(records *SessionRecords, moves []projectRootSessionMove) error {
	records.mu.Lock()
	defer records.mu.Unlock()
	stored, err := records.load()
	if err != nil {
		return err
	}
	targets := make(map[string]string, len(moves))
	for _, move := range moves {
		targets[queueRecordKey(move.SourceProjectID, move.SessionID)] = move.TargetProjectID
	}
	changed := false
	for index := range stored {
		if target, ok := targets[queueRecordKey(stored[index].ProjectID, stored[index].SessionID)]; ok && target != stored[index].ProjectID {
			stored[index].ProjectID = target
			changed = true
		}
	}
	seen := make(map[string]ProjectSessionRecord, len(stored))
	for _, record := range stored {
		if prior, exists := seen[record.SessionID]; exists && prior != record {
			return fmt.Errorf("session migration would create conflicting associations for %s", record.SessionID)
		}
		seen[record.SessionID] = record
	}
	if changed {
		return records.save(stored)
	}
	return nil
}

func migrateSessionQueueProjects(queues *SessionQueues, moves []projectRootSessionMove) error {
	queues.mu.Lock()
	defer queues.mu.Unlock()
	stored, err := queues.load()
	if err != nil {
		return err
	}
	for _, move := range moves {
		if move.SourceProjectID == move.TargetProjectID {
			continue
		}
		source, target := -1, -1
		for index := range stored {
			if stored[index].SessionID != move.SessionID {
				continue
			}
			if stored[index].ProjectID == move.SourceProjectID {
				source = index
			}
			if stored[index].ProjectID == move.TargetProjectID {
				target = index
			}
		}
		if source < 0 {
			continue
		}
		if target >= 0 {
			if !equalQueueExceptProject(stored[source], stored[target]) {
				return fmt.Errorf("session migration found conflicting queues for %s", move.SessionID)
			}
			stored = append(stored[:source], stored[source+1:]...)
			continue
		}
		stored[source].ProjectID = move.TargetProjectID
	}
	return queues.save(stored)
}

func migrateSessionObjectiveProjects(objectives *Objectives, moves []projectRootSessionMove) error {
	objectives.mu.Lock()
	defer objectives.mu.Unlock()
	for _, move := range moves {
		if move.SourceProjectID == move.TargetProjectID {
			continue
		}
		source, err := objectives.read(move.SourceProjectID, move.SessionID)
		if err != nil || source == nil {
			if err != nil {
				return err
			}
			continue
		}
		target, err := objectives.read(move.TargetProjectID, move.SessionID)
		if err != nil {
			return err
		}
		if target != nil && !equalObjectiveExceptProject(*source, *target) {
			return fmt.Errorf("session migration found conflicting objectives for %s", move.SessionID)
		}
		if target == nil {
			copied := *source
			copied.ProjectID = move.TargetProjectID
			if err := objectives.write(copied); err != nil {
				return err
			}
		}
		if err := removeStoredGenerations(storePath(objectives.store, objectiveName(move.SourceProjectID, move.SessionID))); err != nil {
			return err
		}
		if err := removeStoredGenerations(storePath(objectives.store, "extensions/session-goals/"+objectiveKey(move.SourceProjectID, move.SessionID)+".json")); err != nil {
			return err
		}
	}
	return nil
}

func migrateSessionDeletionProjects(deletions *SessionDeletions, moves []projectRootSessionMove) error {
	if deletions == nil {
		return nil
	}
	deletions.mu.Lock()
	defer deletions.mu.Unlock()
	stored, err := deletions.load()
	if err != nil {
		return err
	}
	targets := make(map[string]string, len(moves))
	for _, move := range moves {
		targets[queueRecordKey(move.SourceProjectID, move.SessionID)] = move.TargetProjectID
	}
	changed := false
	for index := range stored {
		if target, ok := targets[queueRecordKey(stored[index].ProjectID, stored[index].SessionID)]; ok && target != stored[index].ProjectID {
			stored[index].ProjectID = target
			changed = true
		}
	}
	if changed {
		return deletions.save(stored)
	}
	return nil
}

func cleanupConfirmedOrphanDeletions(queues *SessionQueues, objectives *Objectives, deletions *SessionDeletions, orphans []projectRootConfirmedOrphan) error {
	for _, orphan := range orphans {
		if queues != nil {
			if err := queues.Forget(orphan.ProjectID, orphan.SessionID); err != nil {
				return fmt.Errorf("clean confirmed orphan deletion queue: %w", err)
			}
		}
		if objectives != nil {
			if err := objectives.Forget(orphan.ProjectID, orphan.SessionID); err != nil {
				return fmt.Errorf("clean confirmed orphan deletion objective: %w", err)
			}
		}
		if deletions != nil {
			if err := deletions.Forget(orphan.ProjectID, orphan.SessionID); err != nil {
				return fmt.Errorf("finish confirmed orphan deletion: %w", err)
			}
		}
	}
	return nil
}

func readProjectRootMigration(store persist.Store) (projectRootMigrationJournal, bool, error) {
	var journal projectRootMigrationJournal
	ok, err := persist.Read(store, projectRootMigrationName, &journal, validateProjectRootMigration)
	return journal, ok, err
}

func writeProjectRootMigration(store persist.Store, journal projectRootMigrationJournal) error {
	return persist.Write(store, projectRootMigrationName, journal, validateProjectRootMigration)
}

func validateProjectRootMigration(journal projectRootMigrationJournal) error {
	if journal.Version != 1 || (journal.Phase != "prepared" && journal.Phase != "migrating") || journal.Roots == nil || journal.Moves == nil || journal.Unresolved == nil {
		return fmt.Errorf("invalid project root migration")
	}
	for _, root := range journal.Roots {
		if root.SourceProjectID == "" || root.TargetProjectID == "" || root.Root == "" {
			return fmt.Errorf("invalid project root migration")
		}
	}
	for _, move := range journal.Moves {
		if move.SourceProjectID == "" || move.TargetProjectID == "" || move.SessionID == "" || move.CWD == "" {
			return fmt.Errorf("invalid project root migration")
		}
	}
	for _, orphan := range journal.ConfirmedOrphans {
		if orphan.ProjectID == "" || orphan.SessionID == "" {
			return fmt.Errorf("invalid project root migration")
		}
	}
	return nil
}

func equalProjectRootMigration(a, b workspace.ProjectRootMigration) bool { return a == b }

func equalQueueExceptProject(a, b storedSessionQueue) bool {
	a.ProjectID, b.ProjectID = "", ""
	return reflect.DeepEqual(a, b)
}

func equalObjectiveExceptProject(a, b storedObjective) bool {
	a.ProjectID, b.ProjectID = "", ""
	return a.Version == b.Version && a.SessionID == b.SessionID && a.UpdatedAt == b.UpdatedAt && equalGoal(a.Goal, b.Goal) && slices.EqualFunc(a.Tasks, b.Tasks, func(left, right SessionTask) bool { return left == right })
}

func equalGoal(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func storePath(store persist.Store, name string) string { return filepath.Join(store.Dir, name) }
