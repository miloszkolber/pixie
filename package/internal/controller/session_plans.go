package controller

import (
	"strings"
	"unicode/utf8"

	piwire "github.com/miloszkolber/pixie/contracts/piprotocol"
)

const (
	maxSessionPlanEntries      = 100
	maxSessionPlanScanEntries  = 200
	maxSessionPlanContentRunes = 4096
	maxSessionPlanTaskID       = 1 << 30
	maxSessionPlanBlockedBy    = 32
)

func projectSessionPlan(entries any) *SessionPlanState {
	source := arrayValue(entries)
	state := &SessionPlanState{Entries: make([]SessionPlanEntry, 0, min(len(source), maxSessionPlanEntries))}
	inspect := min(len(source), maxSessionPlanScanEntries)
	state.Truncated = len(source) > inspect
	for _, raw := range source[:inspect] {
		if len(state.Entries) == maxSessionPlanEntries {
			state.Truncated = true
			break
		}
		entry := mapValue(raw)
		content, valid, truncated := boundedProjectionText(textValue(entry["content"]), maxSessionPlanContentRunes, true)
		priority := textValue(entry["priority"])
		status := textValue(entry["status"])
		if !valid || content == "" || !validPlanPriority(priority) || !validPlanStatus(status) {
			state.Truncated = true
			continue
		}
		if truncated {
			state.Truncated = true
		}
		state.Entries = append(state.Entries, SessionPlanEntry{Content: content, Priority: priority, Status: status, ID: planEntryID(entry["id"]), BlockedBy: planEntryBlockedBy(entry["blockedBy"])})
	}
	return state
}

// projectTodoPlanEntries maps an upstream `todo` tool result envelope
// (details.tasks/details.nextId) onto plan entries. It returns nil unless the
// envelope carries a numeric nextId, which distinguishes a real todo result
// from unrelated payloads that happen to contain a "tasks" field. A valid but
// empty task list returns a non-nil empty slice so clearing todos clears the
// displayed plan. Tombstoned (deleted) tasks are skipped; a missing or invalid
// priority defaults to medium because Pixie's plan contract requires one.
func projectTodoPlanEntries(details map[string]any) []any {
	tasks, ok := details["tasks"].([]any)
	if !ok {
		return nil
	}
	if _, ok := details["nextId"].(float64); !ok {
		return nil
	}
	entries := make([]any, 0, min(len(tasks), maxSessionPlanEntries))
	for _, raw := range tasks {
		task := mapValue(raw)
		status, ok := todoPlanStatus(textValue(task["status"]))
		if !ok {
			continue
		}
		content, valid, _ := boundedProjectionText(textValue(task["subject"]), maxSessionPlanContentRunes, true)
		if !valid || content == "" {
			continue
		}
		priority := textValue(mapValue(task["metadata"])["priority"])
		if !validPlanPriority(priority) {
			priority = string(piwire.PlanEntryPriorityMedium)
		}
		entry := map[string]any{"content": content, "priority": priority, "status": status}
		if id, ok := todoPlanTaskID(task["id"]); ok {
			entry["id"] = id
		}
		if blockedBy := todoPlanBlockedBy(task["blockedBy"]); len(blockedBy) > 0 {
			entry["blockedBy"] = blockedBy
		}
		entries = append(entries, entry)
		if len(entries) == maxSessionPlanEntries {
			break
		}
	}
	return entries
}

func todoPlanStatus(value string) (string, bool) {
	switch value {
	case string(piwire.PlanEntryStatusPending):
		return value, true
	case string(piwire.PlanEntryStatusInProgress):
		return value, true
	case string(piwire.PlanEntryStatusCompleted):
		return value, true
	}
	return "", false
}

func todoPlanTaskID(value any) (float64, bool) {
	id, ok := value.(float64)
	if !ok || id != float64(int(id)) || id <= 0 || id > maxSessionPlanTaskID {
		return 0, false
	}
	return id, true
}

func todoPlanBlockedBy(value any) []any {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	blockedBy := make([]any, 0, min(len(raw), maxSessionPlanBlockedBy))
	for _, item := range raw {
		if id, ok := todoPlanTaskID(item); ok {
			blockedBy = append(blockedBy, id)
		}
		if len(blockedBy) == maxSessionPlanBlockedBy {
			break
		}
	}
	return blockedBy
}

func planEntryID(value any) *int {
	id, ok := value.(float64)
	if !ok || id != float64(int(id)) || id <= 0 || id > maxSessionPlanTaskID {
		return nil
	}
	result := int(id)
	return &result
}

func planEntryBlockedBy(value any) []int {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	blockedBy := make([]int, 0, min(len(raw), maxSessionPlanBlockedBy))
	for _, item := range raw {
		id, ok := item.(float64)
		if !ok || id != float64(int(id)) || id <= 0 || id > maxSessionPlanTaskID {
			continue
		}
		blockedBy = append(blockedBy, int(id))
		if len(blockedBy) == maxSessionPlanBlockedBy {
			break
		}
	}
	if len(blockedBy) == 0 {
		return nil
	}
	return blockedBy
}

func boundedProjectionText(value string, limit int, allowNewlines bool) (string, bool, bool) {
	var projected strings.Builder
	projected.Grow(min(len(value), limit*utf8.UTFMax))
	offset := 0
	for count := 0; offset < len(value) && count < limit; count++ {
		character, size := utf8.DecodeRuneInString(value[offset:])
		if character == utf8.RuneError && size == 1 || character == 0 {
			return "", false, offset < len(value)
		}
		if !allowNewlines && (character == '\n' || character == '\r' || character == '\t') {
			character = ' '
		}
		projected.WriteRune(character)
		offset += size
	}
	result := strings.TrimSpace(projected.String())
	return result, result != "", offset < len(value)
}

func validPlanPriority(value string) bool {
	return value == string(piwire.PlanEntryPriorityHigh) || value == string(piwire.PlanEntryPriorityMedium) || value == string(piwire.PlanEntryPriorityLow)
}

func validPlanStatus(value string) bool {
	return value == string(piwire.PlanEntryStatusPending) || value == string(piwire.PlanEntryStatusInProgress) || value == string(piwire.PlanEntryStatusCompleted)
}

func cloneSessionPlan(state *SessionPlanState) *SessionPlanState {
	if state == nil {
		return nil
	}
	clone := &SessionPlanState{Entries: make([]SessionPlanEntry, len(state.Entries)), Truncated: state.Truncated}
	copy(clone.Entries, state.Entries)
	return clone
}
