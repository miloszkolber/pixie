package controller

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	piwire "github.com/miloszkolber/pixie/internal/piprotocol"
)

const historyMaxSessions = 200

type historyMessage struct {
	role, text string
	index      int
}
type historyEntry struct {
	timestamp int64
	title     string
	messages  []historyMessage
	indexed   bool
	truncated bool
	attempts  int
	retryAt   time.Time
	used      uint64
}

type HistoryIndex struct {
	manager      *SessionManager
	search       chan struct{}
	mu           sync.Mutex
	entries      map[string]historyEntry
	epoch, clock uint64
}

func newHistoryIndex(manager *SessionManager) *HistoryIndex {
	return &HistoryIndex{manager: manager, search: make(chan struct{}, 1), entries: make(map[string]historyEntry)}
}

func (h *HistoryIndex) Forget(sessionID string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	delete(h.entries, sessionID)
	h.epoch++
	h.mu.Unlock()
}

func (h *HistoryIndex) Search(ctx context.Context, request map[string]any) (map[string]any, error) {
	query, ok := request["query"].(string)
	query = strings.TrimSpace(query)
	if !ok || utf16Length(query) > 200 {
		return nil, fmt.Errorf("history query must be text of at most 200 characters")
	}
	scope := mapValue(request["scope"])
	kind := textValue(scope["kind"])
	if kind != "all" && kind != "chat" && kind != "project" {
		return nil, fmt.Errorf("invalid history scope")
	}
	limit := 50
	if value, ok := numeric(request["limit"]); ok {
		limit = int(min(int64(200), max(int64(1), value)))
	}
	select {
	case h.search <- struct{}{}:
		defer func() { <-h.search }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	m := h.manager
	h.mu.Lock()
	epoch := h.epoch
	h.mu.Unlock()
	records, err := m.records.List()
	if err != nil {
		return nil, err
	}
	eligible := make(map[string]bool)
	for _, record := range records {
		if kind == "all" || kind == "chat" && record.SessionID == textValue(scope["sessionId"]) || kind == "project" && record.ProjectID == textValue(scope["projectId"]) {
			eligible[record.SessionID] = true
		}
	}
	var catalog []piwire.SessionInfo
	var cursor *string
	seen := make(map[string]bool)
	incomplete := false
	for page := 0; ; page++ {
		response, err := m.client.ListSessions(ctx, piwire.ListSessionsRequest{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		remaining := historyMaxSessions - len(catalog)
		matches := make([]piwire.SessionInfo, 0)
		for _, session := range response.Sessions {
			if eligible[string(session.SessionId)] {
				matches = append(matches, session)
			}
		}
		catalog = append(catalog, matches[:min(len(matches), remaining)]...)
		if len(catalog) == historyMaxSessions || response.NextCursor == nil || len(catalog) == len(eligible) {
			incomplete = incomplete || len(matches) > remaining || len(catalog) == historyMaxSessions && response.NextCursor != nil
			break
		}
		if seen[*response.NextCursor] || page == 19 {
			incomplete = true
			break
		}
		seen[*response.NextCursor] = true
		cursor = response.NextCursor
	}
	remote, order := make(map[string]remoteSession), make(map[string]int)
	for index, session := range catalog {
		if index >= historyMaxSessions {
			break
		}
		id := string(session.SessionId)
		if id == "" {
			return nil, fmt.Errorf("Pi session list is missing an identifier")
		}
		value := normalizeRemoteSession(session)
		stamp := textValue(session.Meta["createdAt"])
		if session.UpdatedAt != nil {
			stamp = *session.UpdatedAt
		}
		value.updatedAt, _ = parseTimestamp(stamp)
		remote[id], order[id] = value, index
	}
	selected := make([]ProjectSessionRecord, 0)
	for _, record := range records {
		if kind == "chat" && record.SessionID != textValue(scope["sessionId"]) || kind == "project" && record.ProjectID != textValue(scope["projectId"]) {
			continue
		}
		source, exists := remote[record.SessionID]
		if !exists || source.archived {
			continue
		}
		if _, err := m.projects.AssertCWD(record.ProjectID, record.CWD); err != nil {
			incomplete = true
			continue
		}
		selected = append(selected, record)
	}
	sort.SliceStable(selected, func(i, j int) bool {
		left, right := selected[i].SessionID, selected[j].SessionID
		if remote[left].updatedAt != remote[right].updatedAt {
			return remote[left].updatedAt > remote[right].updatedAt
		}
		return order[left] < order[right]
	})
	if len(selected) > historyMaxSessions {
		selected = selected[:historyMaxSessions]
	}
	h.mu.Lock()
	cached := make(map[string]historyEntry, len(selected))
	for _, record := range selected {
		cached[record.SessionID] = h.entries[record.SessionID]
	}
	h.mu.Unlock()
	// Index missing/stale chats before refreshing already indexed live chats.
	// This keeps large histories advancing even with many open tabs.
	var stale, live []ProjectSessionRecord
	for _, record := range selected {
		entry := cached[record.SessionID]
		if entry.timestamp != remote[record.SessionID].updatedAt {
			entry = historyEntry{}
		}
		if entry.attempts >= 3 || entry.retryAt.After(m.now()) {
			continue
		}
		if !entry.indexed {
			stale = append(stale, record)
			continue
		}
		m.mu.Lock()
		loaded := m.sessions[record.SessionID] != nil
		m.mu.Unlock()
		if loaded {
			live = append(live, record)
		}
	}
	jobs := append(stale, live...)
	if len(jobs) > 8 {
		jobs = jobs[:8]
	}
	results := make([]historyEntry, len(jobs))
	var workers sync.WaitGroup
	for index, record := range jobs {
		workers.Add(1)
		go func() {
			defer workers.Done()
			results[index] = h.index(ctx, record, remote[record.SessionID], cached[record.SessionID])
		}()
	}
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.epoch == epoch {
		for index, record := range jobs {
			h.clock++
			entry := results[index]
			entry.used = h.clock
			h.entries[record.SessionID] = entry
		}
	}
	for len(h.entries) > historyMaxSessions {
		oldest := ""
		for id, entry := range h.entries {
			if oldest == "" || entry.used < h.entries[oldest].used {
				oldest = id
			}
		}
		delete(h.entries, oldest)
	}
	prompts, messages := []map[string]any{}, []map[string]any{}
	promptTotal, messageTotal, indexing := 0, 0, false
	normalized := strings.ToLower(query)
	for _, record := range selected {
		entry := h.entries[record.SessionID]
		if entry.attempts >= 3 || entry.truncated {
			incomplete = true
		}
		if !entry.indexed || entry.timestamp != remote[record.SessionID].updatedAt {
			indexing = indexing || entry.attempts < 3
		}
		for index := len(entry.messages) - 1; index >= 0; index-- {
			message := entry.messages[index]
			if normalized != "" && !strings.Contains(strings.ToLower(message.text), normalized) {
				continue
			}
			shared := map[string]any{"text": message.text, "timestamp": entry.timestamp, "sessionId": record.SessionID, "sessionTitle": entry.title, "projectId": record.ProjectID, "cwd": record.CWD, "messageIndex": message.index, "anchorText": message.text}
			if message.role == "user" {
				promptTotal++
				if len(prompts) < limit {
					prompts = append(prompts, shared)
				}
			}
			messageTotal++
			if len(messages) < limit {
				copy := make(map[string]any, len(shared)+2)
				for key, value := range shared {
					copy[key] = value
				}
				copy["role"], copy["snippet"] = message.role, historySnippet(message.text, normalized)
				messages = append(messages, copy)
			}
		}
	}
	return map[string]any{"prompts": prompts, "messages": messages, "promptTotal": promptTotal, "messageTotal": messageTotal, "indexing": indexing, "incomplete": incomplete}, nil
}

func (h *HistoryIndex) index(ctx context.Context, record ProjectSessionRecord, source remoteSession, previous historyEntry) historyEntry {
	m := h.manager
	if previous.timestamp != source.updatedAt {
		previous = historyEntry{}
	}
	entry, err := m.EnsureAttached(ctx, record.SessionID, record.ProjectID, record.CWD)
	if err != nil {
		previous.timestamp = source.updatedAt
		previous.attempts++
		previous.retryAt = m.now().Add(time.Duration(300<<(previous.attempts-1)) * time.Millisecond)
		return previous
	}
	defer m.releaseEntry(entry)
	m.mu.Lock()
	entry.state.Lock()
	if !m.isLeasedLocked(record.SessionID) {
		entry.ephemeral = true
	}
	m.mu.Unlock()
	defer entry.state.Unlock()
	result := historyEntry{timestamp: source.updatedAt, title: source.title, indexed: true, truncated: len(entry.messages) > 500}
	remaining := 256 * 1024
	for index := len(entry.messages) - 1; index >= max(0, len(entry.messages)-500); index-- {
		if remaining <= 0 {
			result.truncated = true
			break
		}
		message := mapValue(entry.messages[index])
		role := textValue(message["role"])
		if role != "user" && role != "assistant" {
			continue
		}
		fullText := historyText(message)
		text := clipUTF16(fullText, min(16*1024, remaining))
		result.truncated = result.truncated || text != fullText
		if text == "" {
			continue
		}
		remaining -= utf16Length(text)
		result.messages = append(result.messages, historyMessage{role: role, text: text, index: index})
	}
	slices.Reverse(result.messages)
	return result
}

func historyText(message map[string]any) string {
	if text, ok := message["content"].(string); ok {
		return text
	}
	var parts []string
	for _, value := range arrayValue(message["content"]) {
		block := mapValue(value)
		if block["type"] == "text" {
			parts = append(parts, textValue(block["text"]))
		}
		if block["type"] == "thinking" && message["role"] == "assistant" {
			parts = append(parts, textValue(block["thinking"]))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func clipUTF16(text string, limit int) string {
	count := 0
	for index, value := range text {
		count++
		if value > 0xffff {
			count++
		}
		if count > limit {
			return text[:index]
		}
	}
	return text
}

func historySnippet(text, query string) string {
	lower := strings.ToLower(text)
	index := strings.Index(lower, query)
	if query == "" || index < 0 {
		return clipUTF16(text, 240)
	}
	// Use rune offsets when projecting a match back into the original text:
	// Unicode case conversion can change the number of UTF-8 bytes.
	runes := []rune(text)
	position := utf8.RuneCountInString(lower[:index])
	start, end := max(0, position-80), min(len(runes), position+utf8.RuneCountInString(query)+120)
	snippet := string(runes[start:end])
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(runes) {
		snippet += "…"
	}
	return snippet
}
