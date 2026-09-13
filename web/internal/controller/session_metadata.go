package controller

import (
	"fmt"
	"sort"
	"strings"
)

// SessionMetadataHelperVersion versions these additive UI-03/FC08
// host/session-keyed catalog helpers. It never changes any persisted JSON
// schema or existing session transport.
const SessionMetadataHelperVersion = 1

// SessionCatalogRecentLimit mirrors the web catalog concise recent subset.
const SessionCatalogRecentLimit = 6

// UntitledSessionTitle is the explicit fallback for missing native titles.
// No model call is used to generate titles.
const UntitledSessionTitle = "Untitled chat"

// SessionMetadata carries host/session-keyed catalog metadata with a nullable
// host-side project key. A nil or empty ProjectID means the session is
// ungrouped. There is no hidden all-files project.
type SessionMetadata struct {
	HostID      string  `json:"hostId"`
	SessionID   string  `json:"sessionId"`
	ProjectID   *string `json:"projectId"`
	Title       string  `json:"title"`
	UpdatedAt   int64   `json:"updatedAt"`
	IsStreaming bool    `json:"isStreaming"`
	Archived    bool    `json:"archived"`
}

// SessionMetadataCatalog is the grouped/flat/ungrouped view built from one
// host/session-keyed catalog.
type SessionMetadataCatalog struct {
	Grouped   map[string][]SessionMetadata
	Ungrouped []SessionMetadata
	Flat      []SessionMetadata
}

// IsUngroupedProjectKey reports whether a nullable host-side project key
// means an ungrouped session. Only nil or the empty string is ungrouped.
func IsUngroupedProjectKey(key *string) bool {
	return key == nil || *key == ""
}

// ValidateOptionalProjectKey accepts a nil/empty (ungrouped) project key or a
// grouped project identifier. Grouped identifiers share the durable identity
// bound used by controller records: no NUL, no slashes, no surrounding
// whitespace, UTF-16 length within 256.
func ValidateOptionalProjectKey(key *string) error {
	if key == nil || *key == "" {
		return nil
	}
	return ValidateOptionalProjectID(*key)
}

// ValidateOptionalProjectID accepts an empty (ungrouped) project reference or
// a grouped project identifier without touching storage.
func ValidateOptionalProjectID(id string) error {
	if id == "" {
		return nil
	}
	if strings.TrimSpace(id) != id {
		return errorfProjectKey()
	}
	if containsNUL(id) || strings.ContainsAny(id, "/\\") {
		return errorfProjectKey()
	}
	if utf16Length(id) > 256 {
		return errorfProjectKey()
	}
	return nil
}

// SessionMetadataKey returns the stable host/session association key. Host and
// session IDs are paired opaquely; duplicate native session IDs across hosts
// never collapse by first match.
func SessionMetadataKey(hostID, sessionID string) string {
	return hostID + "\x00" + sessionID
}

// SessionMetadataSelectedKey returns the host/session key for one metadata row.
func SessionMetadataSelectedKey(metadata SessionMetadata) string {
	return SessionMetadataKey(metadata.HostID, metadata.SessionID)
}

// ValidateSessionMetadataKey validates the stable host/session association
// without touching storage. The host may be empty for the default host, but
// the native session ID is always required and never used as a path.
func ValidateSessionMetadataKey(hostID, sessionID string) error {
	if containsNUL(hostID) || containsNUL(sessionID) {
		return errorfSessionKey()
	}
	if strings.TrimSpace(sessionID) == "" {
		return errorfSessionKey()
	}
	return nil
}

// DisplaySessionMetadataTitle returns the native title as-is with an explicit
// unresolved fallback. No model call is used.
func DisplaySessionMetadataTitle(title string) string {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return UntitledSessionTitle
	}
	return trimmed
}

// ProjectIDOrEmpty returns the grouped project ID or the empty string for
// ungrouped metadata, preserving compatibility with project-required
// transports.
func (m SessionMetadata) ProjectIDOrEmpty() string {
	if m.ProjectID == nil {
		return ""
	}
	return *m.ProjectID
}

// SessionMetadataFromSummary bridges an existing project-required summary to
// nullable host/session-keyed metadata. An empty project ID becomes an
// explicit ungrouped (nil) key; grouped IDs are copied verbatim.
func SessionMetadataFromSummary(hostID string, summary SessionSummary) SessionMetadata {
	var projectKey *string
	if summary.ProjectID != "" {
		value := summary.ProjectID
		projectKey = &value
	}
	return SessionMetadata{
		HostID:      hostID,
		SessionID:   summary.SessionID,
		ProjectID:   projectKey,
		Title:       summary.Title,
		UpdatedAt:   summary.UpdatedAt,
		IsStreaming: summary.IsStreaming,
		Archived:    summary.Archived,
	}
}

// SortSessionMetadataRecentFirst returns a newest-first copy with a stable
// host/session-key tie-break. The input order is not mutated.
func SortSessionMetadataRecentFirst(sessions []SessionMetadata) []SessionMetadata {
	sorted := append([]SessionMetadata(nil), sessions...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].UpdatedAt != sorted[j].UpdatedAt {
			return sorted[i].UpdatedAt > sorted[j].UpdatedAt
		}
		if sorted[i].HostID != sorted[j].HostID {
			return sorted[i].HostID < sorted[j].HostID
		}
		return sorted[i].SessionID < sorted[j].SessionID
	})
	return sorted
}

// SelectSessionMetadataSubset returns the concise recent subset that never
// hides the selected or running session behind Show more/Expand. Order is
// preserved; hiddenCount is the number of rows behind the subset.
func SelectSessionMetadataSubset(sessions []SessionMetadata, selectedKey string, limit int, expanded bool) (visible []SessionMetadata, hiddenCount int) {
	if expanded || len(sessions) <= limit || limit < 0 {
		return append([]SessionMetadata(nil), sessions...), 0
	}
	// Index-aware pinning preserves recent-first order: the first limit rows
	// stay visible plus the selected key and any running session.
	visible = make([]SessionMetadata, 0, len(sessions))
	for index, session := range sessions {
		if index < limit || SessionMetadataSelectedKey(session) == selectedKey || session.IsStreaming {
			visible = append(visible, session)
		}
	}
	return visible, len(sessions) - len(visible)
}

// PartitionSessionMetadata builds grouped/flat/ungrouped views from one
// host/session-keyed catalog. Inputs may mix grouped records and explicit
// ungrouped metadata. Sessions whose project key is ungrouped or has no open
// project land in ungrouped rather than hiding. Archived sessions stay out of
// the Chats catalog. Duplicate native IDs across hosts are kept by their
// host/session key. No hidden all-files project is fabricated.
func PartitionSessionMetadata(sessions []SessionMetadata, knownProjectIDs []string) SessionMetadataCatalog {
	known := make(map[string]bool, len(knownProjectIDs))
	for _, id := range knownProjectIDs {
		known[id] = true
	}
	seen := make(map[string]bool, len(sessions))
	grouped := make(map[string][]SessionMetadata, len(knownProjectIDs))
	ungroupedByKey := make(map[string]SessionMetadata, len(sessions))
	for _, session := range sessions {
		key := SessionMetadataSelectedKey(session)
		if session.SessionID == "" || seen[key] {
			continue
		}
		seen[key] = true
		if session.Archived {
			continue
		}
		if session.ProjectID != nil && *session.ProjectID != "" && known[*session.ProjectID] {
			grouped[*session.ProjectID] = append(grouped[*session.ProjectID], session)
			continue
		}
		ungroupedByKey[key] = session
	}
	for project := range grouped {
		grouped[project] = SortSessionMetadataRecentFirst(grouped[project])
	}
	ungrouped := make([]SessionMetadata, 0, len(ungroupedByKey))
	for _, session := range ungroupedByKey {
		ungrouped = append(ungrouped, session)
	}
	ungrouped = SortSessionMetadataRecentFirst(ungrouped)
	flatInput := make([]SessionMetadata, 0, len(sessions))
	for _, list := range grouped {
		flatInput = append(flatInput, list...)
	}
	flatInput = append(flatInput, ungrouped...)
	flat := SortSessionMetadataRecentFirst(flatInput)
	if grouped == nil {
		grouped = make(map[string][]SessionMetadata)
	}
	if ungrouped == nil {
		ungrouped = []SessionMetadata{}
	}
	if flat == nil {
		flat = []SessionMetadata{}
	}
	return SessionMetadataCatalog{Grouped: grouped, Ungrouped: ungrouped, Flat: flat}
}

func errorfProjectKey() error {
	return fmt.Errorf("project id is invalid")
}

func errorfSessionKey() error {
	return fmt.Errorf("session key is invalid")
}
