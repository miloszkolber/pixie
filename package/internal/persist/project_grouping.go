package persist

import (
	"fmt"
	"strings"
	"unicode/utf16"
)

// ProjectGroupingHelperVersion versions these additive STATE-01 nullable
// grouping helpers. It never changes any persisted JSON schema.
const ProjectGroupingHelperVersion = 1

// IsUngroupedProjectID reports whether id means an ungrouped session. Only
// the empty string is ungrouped; whitespace or any other value is a grouped
// (or invalid) project reference. There is no hidden all-files project.
func IsUngroupedProjectID(id string) bool {
	return id == ""
}

// ValidateOptionalProjectID accepts an empty (ungrouped) project reference or
// a grouped project identifier. Grouped identifiers must not contain NUL,
// slashes or surrounding whitespace and stay within the durable identity
// bound shared with controller records.
func ValidateOptionalProjectID(id string) error {
	if id == "" {
		return nil
	}
	if strings.TrimSpace(id) != id || id == "" {
		return fmt.Errorf("project id is invalid")
	}
	if strings.ContainsRune(id, 0) || strings.ContainsAny(id, "/\\") {
		return fmt.Errorf("project id is invalid")
	}
	if projectIDUTF16Length(id) > 256 {
		return fmt.Errorf("project id is invalid")
	}
	return nil
}

// ValidateSessionKey validates the stable key used for drafts and layouts.
// Drafts and layouts migrate by session key so removing a project grouping
// never deletes conversations or their unsent text.
func ValidateSessionKey(sessionID string) error {
	if strings.TrimSpace(sessionID) == "" || strings.ContainsRune(sessionID, 0) {
		return fmt.Errorf("session id is invalid")
	}
	return nil
}

// DraftKeyForSession returns the stable draft/layout key for one session. The
// key depends only on the session, never on the project grouping, so entries
// survive project removal as ungrouped state.
func DraftKeyForSession(sessionID string) (string, error) {
	if err := ValidateSessionKey(sessionID); err != nil {
		return "", err
	}
	return sessionID, nil
}

// MigrateDraftsBySessionKey re-keys legacy project-scoped drafts and layouts
// by session key. Legacy keys have the form "projectID\x00sessionID"; an empty
// project section means the entry was already ungrouped. Values are preserved
// verbatim. A session appearing under two projects with different text
// conflicts instead of merging opportunistically; identical duplicates fold.
func MigrateDraftsBySessionKey(legacy map[string]string) (map[string]string, error) {
	migrated := make(map[string]string, len(legacy))
	for rawKey, text := range legacy {
		projectID, sessionID, ok := splitProjectScopedKey(rawKey)
		if !ok {
			return nil, fmt.Errorf("invalid project-scoped draft key")
		}
		if err := ValidateOptionalProjectID(projectID); err != nil {
			return nil, fmt.Errorf("invalid project-scoped draft key")
		}
		key, err := DraftKeyForSession(sessionID)
		if err != nil {
			return nil, fmt.Errorf("invalid project-scoped draft key")
		}
		if prior, exists := migrated[key]; exists {
			if prior != text {
				return nil, fmt.Errorf("draft migration found conflicting entries for one session")
			}
			continue
		}
		migrated[key] = text
	}
	return migrated, nil
}

// ProjectScopedDraftKey builds the legacy "projectID\x00sessionID" form used
// only as a migration input. New state must already be keyed by session.
func ProjectScopedDraftKey(projectID, sessionID string) (string, error) {
	if err := ValidateOptionalProjectID(projectID); err != nil {
		return "", err
	}
	if err := ValidateSessionKey(sessionID); err != nil {
		return "", err
	}
	return projectID + "\x00" + sessionID, nil
}

func splitProjectScopedKey(key string) (string, string, bool) {
	parts := strings.SplitN(key, "\x00", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	if strings.ContainsRune(parts[1], 0) {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func projectIDUTF16Length(value string) int {
	count := 0
	for _, character := range value {
		count += utf16.RuneLen(character)
	}
	return count
}
