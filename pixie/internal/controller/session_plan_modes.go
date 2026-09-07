package controller

import (
	"strings"
	"unicode/utf8"

	piwire "github.com/miloszkolber/pixie/internal/piprotocol"
)

const (
	maxSessionModes            = 64
	maxSessionModeScan         = 256
	maxSessionModeIDBytes      = 256
	maxSessionModeNameRunes    = 128
	maxModeDescriptionRunes    = 1024
	maxSessionPlanEntries      = 100
	maxSessionPlanScanEntries  = 200
	maxSessionPlanContentRunes = 4096
)

func projectSessionModes(source *piwire.SessionModeState) *SessionModeState {
	if source == nil || !validSessionModeID(string(source.CurrentModeId)) {
		return nil
	}
	modes := make([]SessionMode, 0, min(len(source.AvailableModes), maxSessionModes))
	seen := make(map[string]bool, maxSessionModes)
	var projectedCurrent *SessionMode
	for _, sourceMode := range source.AvailableModes[:min(len(source.AvailableModes), maxSessionModeScan)] {
		mode, ok := projectSessionMode(sourceMode)
		if !ok {
			continue
		}
		if mode.ID == string(source.CurrentModeId) {
			current := mode
			projectedCurrent = &current
		}
		if seen[mode.ID] || len(modes) == maxSessionModes {
			continue
		}
		seen[mode.ID] = true
		modes = append(modes, mode)
	}
	current := string(source.CurrentModeId)
	if !seen[current] {
		if projectedCurrent == nil {
			return nil
		}
		if len(modes) == maxSessionModes {
			modes[len(modes)-1] = *projectedCurrent
		} else {
			modes = append(modes, *projectedCurrent)
		}
	}
	return &SessionModeState{CurrentModeID: current, AvailableModes: modes}
}

func projectSessionMode(source piwire.SessionMode) (SessionMode, bool) {
	id := string(source.Id)
	name, ok, _ := boundedProjectionText(source.Name, maxSessionModeNameRunes, false)
	if !validSessionModeID(id) || !ok {
		return SessionMode{}, false
	}
	mode := SessionMode{ID: id, Name: name}
	if source.Description != nil {
		if description, valid, _ := boundedProjectionText(*source.Description, maxModeDescriptionRunes, true); valid && description != "" {
			mode.Description = &description
		}
	}
	return mode, true
}

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

func validSessionModeID(value string) bool {
	return value != "" && len(value) <= maxSessionModeIDBytes && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func validPlanPriority(value string) bool {
	return value == string(piwire.PlanEntryPriorityHigh) || value == string(piwire.PlanEntryPriorityMedium) || value == string(piwire.PlanEntryPriorityLow)
}

func validPlanStatus(value string) bool {
	return value == string(piwire.PlanEntryStatusPending) || value == string(piwire.PlanEntryStatusInProgress) || value == string(piwire.PlanEntryStatusCompleted)
}

func cloneSessionModes(state *SessionModeState) *SessionModeState {
	if state == nil {
		return nil
	}
	clone := &SessionModeState{CurrentModeID: state.CurrentModeID, AvailableModes: make([]SessionMode, len(state.AvailableModes))}
	copy(clone.AvailableModes, state.AvailableModes)
	for index, mode := range clone.AvailableModes {
		if mode.Description != nil {
			description := *mode.Description
			clone.AvailableModes[index].Description = &description
		}
	}
	return clone
}

func cloneSessionPlan(state *SessionPlanState) *SessionPlanState {
	if state == nil {
		return nil
	}
	clone := &SessionPlanState{Entries: make([]SessionPlanEntry, len(state.Entries)), Truncated: state.Truncated}
	copy(clone.Entries, state.Entries)
	return clone
}

func modeAdvertised(state *SessionModeState, modeID string) bool {
	if state == nil || !validSessionModeID(modeID) {
		return false
	}
	for _, mode := range state.AvailableModes {
		if mode.ID == modeID {
			return true
		}
	}
	return false
}
