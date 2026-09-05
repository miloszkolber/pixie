package controller

import (
	"strings"
	"unicode/utf8"

	piwire "github.com/miloszkolber/pixie/internal/piprotocol"
)

const (
	maxSessionPlanEntries      = 100
	maxSessionPlanScanEntries  = 200
	maxSessionPlanContentRunes = 4096
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
		state.Entries = append(state.Entries, SessionPlanEntry{Content: content, Priority: priority, Status: status})
	}
	return state
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
