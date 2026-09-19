package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/miloszkolber/pixie/internal/diagnostics"
	piwire "github.com/miloszkolber/pixie/piprotocol"
)

// nativeEventTextMaxBytes bounds one native Pi/SDK update text value before it
// enters controller session state or a browser projection. Native error text
// can embed credentials, URLs or absolute paths, so it is reduced through the
// shared diagnostics sanitizer instead of a second controller-local pattern
// set. The caller keeps the typed failure class (event type or stop reason).
const nativeEventTextMaxBytes = 2048

// nativeFailureSummary is the stable, secret-free fallback for a native
// failure whose message was empty or entirely redacted.
const nativeFailureSummary = "The Pi agent reported a failure."

// nativeErrorTextFields are the native update keys that carry free-form
// Pi/SDK failure text. Session, run, tool and entry identifiers are never
// sanitized, so required correlation values survive unchanged.
var nativeErrorTextFields = [...]string{"error", "errorMessage", "error_message", "finalError", "final_error"}

// normalizeNativeEventText reduces one native Pi/SDK update string to a
// bounded, secret-free summary using the shared diagnostics redaction rules.
func normalizeNativeEventText(text string) string {
	return diagnostics.SanitizeDiagnosticText(text, nativeEventTextMaxBytes)
}

// normalizeNativeFailureValue redacts the free-form text inside a native error
// value of an unknown shape. Maps and slices are traversed so a structured
// error cannot smuggle text past the boundary; scalar values are unchanged.
func normalizeNativeFailureValue(value any) any {
	switch typed := value.(type) {
	case string:
		return normalizeNativeEventText(typed)
	case map[string]any:
		if typed == nil {
			return typed
		}
		clone := make(map[string]any, len(typed))
		for key, item := range typed {
			clone[key] = normalizeNativeFailureValue(item)
		}
		return clone
	case []any:
		if typed == nil {
			return typed
		}
		clone := make([]any, len(typed))
		for index, item := range typed {
			clone[index] = normalizeNativeFailureValue(item)
		}
		return clone
	default:
		return value
	}
}

// normalizeNativeErrorFields redacts the known error-bearing fields of an
// event map. Unknown fields, including identifiers and forward-compatible
// future payloads, pass through untouched.
func normalizeNativeErrorFields(event map[string]any) map[string]any {
	for _, key := range nativeErrorTextFields {
		value, exists := event[key]
		if !exists {
			continue
		}
		event[key] = normalizeNativeFailureValue(value)
	}
	return event
}

type sessionUpdateOrigin uint8

const (
	agentPiUpdate sessionUpdateOrigin = iota
	piPiUpdate
	piExtensionUpdate
)

func (m *SessionManager) SessionUpdate(ctx context.Context, notification piwire.SessionNotification) error {
	raw := objectValue(notification)
	return m.applyUpdate(ctx, raw, false)
}

func (m *SessionManager) Extension(ctx context.Context, method string, params json.RawMessage) error {
	if method == "pi.session.update" {
		var raw map[string]any
		if err := json.Unmarshal(params, &raw); err != nil {
			return err
		}
		return m.applyUpdate(ctx, raw, true)
	}
	if method == "provider.login" {
		var value map[string]any
		if json.Unmarshal(params, &value) == nil && m.deviceCode != nil {
			m.deviceCode(value)
		}
	}
	return nil
}

func (m *SessionManager) applyUpdate(ctx context.Context, notification map[string]any, piOnly bool) error {
	sessionID := textValue(notification["sessionId"])
	if sessionID == "" {
		return fmt.Errorf("Pi update is missing sessionId")
	}
	update := mapValue(notification["update"])
	kind := textValue(update["sessionUpdate"])
	// Generic extension dialogs are manager-level (session-bound, single-use)
	// and never touch the transcript projection. Ephemeral status, widget,
	// title, and working-message projections fan out the same way.
	if kind == "ui_request" || kind == "ui_notify" || kind == "ui_cancel" ||
		kind == "ui_status" || kind == "ui_widget" || kind == "ui_title" || kind == "ui_working" {
		if !m.acceptUiUpdate(ctx, sessionID) {
			return nil
		}
		return m.applyUiUpdate(ctx, sessionID, kind, update)
	}
	origin := agentPiUpdate
	if piOnly {
		origin = piExtensionUpdate
	} else if recognized, _ := ctx.Value(recognizedPiConnectionKey{}).(bool); recognized {
		origin = piPiUpdate
	}
	m.mu.Lock()
	entry := m.sessions[sessionID]
	if m.closed {
		entry = nil
	}
	if entry == nil && !m.closed && m.creating > 0 && kind == "available_commands_update" && origin != piExtensionUpdate {
		generation, tagged := ctx.Value(connectionGenerationKey{}).(uint64)
		_, exists := m.pendingCommands[sessionID]
		if tagged && (exists || len(m.pendingCommands) < maxPendingCommandCatalogs) {
			if m.pendingCommands == nil {
				m.pendingCommands = make(map[string]pendingCommandCatalog)
			}
			m.pendingCommands[sessionID] = pendingCommandCatalog{
				generation: generation,
				commands:   projectAgentSlashCommands(update["availableCommands"], origin == piPiUpdate),
			}
		}
	}
	retainUntilScheduled := entry != nil && kind == "status_message" && terminalStatusKind(textValue(mapValue(update["status"])["type"]))
	if retainUntilScheduled {
		// A terminal notification wakes durable queued work after releasing the
		// projection lock. Keep the projection alive across that handoff so lease
		// reconciliation cannot evict it before scheduler admission.
		entry.refs++
	}
	m.mu.Unlock()
	if entry == nil {
		return nil
	}
	if retainUntilScheduled {
		defer m.releaseEntry(entry)
	}
	entry.state.Lock()
	target := entry
	publish := true
	if entry.replay != nil {
		target = entry.replay
		publish = false
	}
	// The SDK drains already-received notifications after disconnect. They must
	// not enter a transcript being replayed over a replacement connection.
	if generation, tagged := ctx.Value(connectionGenerationKey{}).(uint64); tagged && generation != target.attached {
		entry.state.Unlock()
		return nil
	}
	previousTitle := target.title
	// Settled semantics: one prompt can emit several agent_end events
	// (retry, compaction, extension-injected or queued turns). The browser
	// keeps its stream open until prompt settlement, so these lifecycle
	// annotations travel verbatim and never touch the transcript. A stale
	// agent_start arriving after settlement must not resurrect streaming
	// state; anything replayed into history is not a live run annotation.
	if lifecycleKind(kind) {
		if !publish {
			entry.state.Unlock()
			return nil
		}
		if kind == "agent_start" && !target.promptActive && !target.streaming {
			entry.state.Unlock()
			return nil
		}
		// AUX-04: compaction suspends delivery of the controller-owned follow-up
		// queue. The queue is drained only when compaction ends, so a prompt
		// submitted while Pi compacts is delivered afterwards instead of being
		// dropped or raced against the compaction.
		if kind == "compaction_start" {
			target.compactionActive = true
		}
		if kind == "compaction_end" {
			target.compactionActive = false
		}
		wakeQueue := kind == "compaction_end"
		if kind == "agent_settled" && !target.promptActive && (target.streaming || target.runID != "") {
			// A reload can reattach to a session whose native run was still
			// active in the snapshot. The Go host reports settlement with
			// agent_settled, so this is the authoritative signal that the
			// replayed run ended. Clear it to admit durable follow-ups and idle
			// release. An in-flight controller prompt keeps its RPC result
			// authoritative and is excluded by the promptActive guard.
			target.streaming = false
			target.runID = ""
			target.settlement = &SessionSettlement{StopReason: "complete"}
			wakeQueue = true
		}
		event := make(map[string]any, len(update))
		for key, value := range update {
			if key != "sessionUpdate" {
				event[key] = value
			}
		}
		// AUX-32: the native agent_end payload can carry the full message array
		// with a hostile tool result. The host projection already replaces it
		// with a bounded summary; drop it here as well so no sender can relay
		// raw native transcript text to the browser or the published stream.
		if kind == "agent_end" {
			delete(event, "messages")
		}
		event["type"] = kind
		// Native lifecycle events travel as run annotations, but a retry or
		// compaction failure can still carry a raw provider error. Redact the
		// known error fields before they reach the browser; identifiers and
		// unknown fields are preserved.
		event = normalizeNativeErrorFields(event)
		m.emit("agent.event", map[string]any{"sessionId": sessionID, "event": event})
		if kind == "agent_settled" && wakeQueue {
			m.emit("agent.event", map[string]any{"sessionId": sessionID, "event": map[string]any{"type": "complete", "status": "complete"}})
		}
		entry.state.Unlock()
		if wakeQueue {
			m.scheduleFollowUp(sessionID, entry)
		}
		return nil
	}
	// Late message events from an older run must not resurrect a completed
	// stream. Live chunks only extend a run that is still open; replayed
	// history (publish == false) still rebuilds the transcript.
	if publish && (kind == "agent_message_chunk" || kind == "agent_thought_chunk") &&
		!target.promptActive && !target.streaming {
		entry.state.Unlock()
		return nil
	}
	events := applySessionUpdate(target, kind, update, origin)
	var persistedTitle string
	if target.title != "" && target.title != previousTitle {
		persistedTitle = target.title
	}
	wakeQueue := false
	if publish {
		for _, event := range events {
			if event["type"] == "message_start" {
				event["message"] = cloneJSON(event["message"])
			}
			wakeQueue = wakeQueue || event["type"] == "complete" || event["type"] == "error" || event["type"] == "compaction_end"
		}
		for _, event := range events {
			m.emit("agent.event", map[string]any{"sessionId": sessionID, "event": event})
		}
	}
	entry.state.Unlock()
	if persistedTitle != "" && m.records != nil {
		// A title update is useful even when Pi does not return it from a
		// later session.load. The live projection remains authoritative if the
		// small local persistence write fails; the next update can retry it.
		_ = m.records.SetTitle(entry.projectID, sessionID, persistedTitle)
	}
	if wakeQueue {
		m.scheduleFollowUp(sessionID, entry)
	}
	if !wakeQueue {
		// A late status/tool notification can grow an otherwise idle, unleased
		// projection without passing through an operation release. Re-apply the
		// inactive budget here so the 8 MiB cap remains a cap, not a later hint.
		m.mu.Lock()
		if !m.closed && m.sessions[sessionID] == entry && entry.refs == 0 && !m.isLeasedLocked(sessionID) {
			m.evictLocked()
		}
		m.mu.Unlock()
	}
	return nil
}

func applySessionUpdate(entry *sessionEntry, kind string, update map[string]any, origin sessionUpdateOrigin) []map[string]any {
	// Notifications can change a closed chat without holding an operation ref.
	entry.inactiveBytes = 0
	if origin == piExtensionUpdate {
		return applyPiOnlyUpdate(entry, kind, update)
	}
	trustedPi := origin == piPiUpdate
	switch kind {
	case "agent_message_chunk", "user_message_chunk":
		role := "assistant"
		if kind == "user_message_chunk" {
			role = "user"
		}
		content := mapValue(update["content"])
		if role == "assistant" && textValue(content["type"]) == "resource" {
			resource := mapValue(content["resource"])
			text := textValue(resource["text"])
			if text == "" {
				text = "[Resource content: " + textValue(resource["uri"]) + "]"
			}
			content = map[string]any{"type": "text", "text": text}
		}
		if textValue(content["type"]) == "resource" {
			marker, byteLength, valid := replayTextResourceMarker(content)
			if !valid {
				return nil
			}
			if role == "user" {
				if matched, enriched := consumeUserEcho(entry, "resource", textValue(update["entryId"]), marker); matched {
					if enriched != nil {
						return []map[string]any{{"type": "message_start", "message": enriched}}
					}
					return nil
				}
			}
			if role != "user" || !appendUserResourceMarker(entry, marker, byteLength) {
				return nil
			}
			entry.stats.TotalMessages = len(entry.messages)
			return []map[string]any{{"type": "message_start", "message": entry.messages[len(entry.messages)-1]}}
		}
		if textValue(content["type"]) == "image" {
			image := map[string]any{"type": "image", "data": textValue(content["data"]), "mimeType": textValue(content["mimeType"])}
			if role == "user" {
				if matched, enriched := consumeUserEcho(entry, "image", textValue(update["entryId"]), image); matched {
					if enriched != nil {
						return []map[string]any{{"type": "message_start", "message": enriched}}
					}
					return nil
				}
			}
			appendMessageBlock(entry, role, image, textValue(update["messageId"]), textValue(update["entryId"]))
			entry.stats.TotalMessages = len(entry.messages)
			if role == "user" {
				return []map[string]any{{"type": "message_start", "message": entry.messages[len(entry.messages)-1]}}
			}
			entry.streaming = true
			return []map[string]any{{"type": "image", "messageId": optionalText(update["messageId"]), "image": image}}
		}
		if content["type"] == "resource_link" {
			content = map[string]any{"type": "text", "text": "Resource: " + textValue(content["name"]) + " (" + textValue(content["uri"]) + ")"}
		}
		if content["type"] != "text" && content["text"] == nil {
			content = map[string]any{"type": "text", "text": "[Unsupported content: " + textValue(content["type"]) + "]"}
		}
		text := textValue(content["text"])
		if role == "user" {
			if matched, enriched := consumeUserEcho(entry, "text", textValue(update["entryId"]), map[string]any{"text": text}); matched {
				if enriched != nil {
					return []map[string]any{{"type": "message_start", "message": enriched}}
				}
				return nil
			}
		}
		appendMessageBlock(entry, role, map[string]any{"type": "text", "text": text}, textValue(update["messageId"]), textValue(update["entryId"]))
		entry.streaming = true
		entry.stats.TotalMessages = len(entry.messages)
		if role == "user" {
			return []map[string]any{{"type": "message_start", "message": entry.messages[len(entry.messages)-1]}}
		}
		return []map[string]any{{"type": "text", "messageId": optionalText(update["messageId"]), "text": text}}
	case "available_commands_update":
		commands := projectAgentSlashCommands(update["availableCommands"], trustedPi)
		entry.commands = commands
		return []map[string]any{{"type": "commands", "commands": cloneSlashCommands(commands)}}
	case "plan":
		plan := projectSessionPlan(update["entries"])
		entry.planState = plan
		return []map[string]any{{"type": "plan", "planState": cloneSessionPlan(plan)}}

	case "agent_thought_chunk":
		text := textValue(mapValue(update["content"])["text"])
		appendMessageBlock(entry, "assistant", map[string]any{"type": "thinking", "thinking": text}, textValue(update["messageId"]))
		entry.streaming = true
		return []map[string]any{{"type": "thinking", "messageId": optionalText(update["messageId"]), "text": text}}
	case "tool_call":
		trustedTool := map[string]any{}
		if trustedPi {
			trustedTool = mapValue(mapValue(mapValue(update["_meta"])["pi"])["toolCall"])
		}
		toolName := textValue(trustedTool["toolName"])
		activityTool := textValue(trustedTool["extensionName"]) == "summon" && (toolName == "delegate" || toolName == "load")
		if toolName == "" {
			if textValue(update["title"]) == "ask_user_question" {
				toolName = "ask_user_question"
			} else {
				toolName = "tool"
			}
		}
		if strings.HasSuffix(toolName, "__ask_user_question") ||
			(textValue(trustedTool["extensionName"]) == "mcp" && strings.HasSuffix(toolName, "_ask_user_question")) {
			toolName = "ask_user_question"
		}
		toolID := textValue(update["toolCallId"])
		if entry.pendingToolOutputs == nil {
			entry.pendingToolOutputs = make(map[string]toolOutput)
		}
		// Keep an empty entry for every active call. Besides restoring running
		// tools after reconnect, this tombstones a completed older invocation if
		// an upstream reuses its call ID.
		entry.pendingToolOutputs[toolID] = toolOutput{SubagentActivityTool: activityTool}
		if entry.toolChanged != nil {
			close(entry.toolChanged)
			entry.toolChanged = nil
		}
		input := update["rawInput"]
		if input == nil {
			input = map[string]any{}
		}
		appendMessageBlock(entry, "assistant", map[string]any{"type": "toolCall", "id": toolID, "toolName": toolName, "name": toolName, "title": textValue(update["title"]), "arguments": input})
		entry.stats.TotalMessages = len(entry.messages)
		events := []map[string]any{{"type": "tool-start", "toolCallId": toolID, "toolName": toolName, "title": textValue(update["title"]), "tool": input}}
		return append(events, applySessionUpdate(entry, "tool_call_update", update, origin)...)

	case "tool_call_update":
		toolID := textValue(update["toolCallId"])
		if failure, exists := update["error"]; exists && failure != nil {
			// A native tool failure becomes the persisted transcript result and is
			// copied into tool details. Redact it before either use; the failed
			// status still carries the failure class.
			update = maps.Clone(update)
			update["error"] = normalizeNativeFailureValue(failure)
		}
		var projectedCall map[string]any
		for index := len(entry.messages) - 1; index >= 0 && projectedCall == nil; index-- {
			message := mapValue(entry.messages[index])
			for _, value := range contentBlocks(message["content"]) {
				block := mapValue(value)
				if block["type"] != "toolCall" || textValue(block["id"]) != toolID {
					continue
				}
				for source, destination := range map[string]string{"title": "title", "kind": "kind", "locations": "locations", "rawInput": "arguments", "status": "status"} {
					if value, present := update[source]; present {
						block[destination] = value
					}
				}
				projectedCall = block
			}
		}
		if entry.toolChanged != nil {
			close(entry.toolChanged)
			entry.toolChanged = nil
		}
		status := textValue(update["status"])
		finished := status == "completed" || status == "error" || status == "failed"
		result, activity := projectToolOutputAndActivity(entry, toolID, update, finished, trustedPi)
		if finished {
			message := map[string]any{"role": "toolResult", "toolCallId": toolID, "content": result, "details": toolDetailsForAgent(update, trustedPi)}
			if activity != nil {
				message["subagentActivity"] = cloneJSON(activity)
			}
			if status == "error" || status == "failed" {
				message["isError"] = true
			}
			entry.messages = append(entry.messages, message)
		}
		eventType := "tool-update"
		if finished {
			eventType = "tool-end"
		}
		event := map[string]any{"type": eventType, "toolCallId": toolID, "status": status, "tool": result}
		if projectedCall != nil {
			event["toolCall"] = cloneJSON(projectedCall)
		}
		if activity != nil {
			event["subagentActivity"] = activity
		}
		return []map[string]any{event}
	case "config_option_update":
		entry.configOptions = arrayValue(update["configOptions"])
		entry.thinkingLevel = thinkingFromOptions(entry.configOptions)
		entry.model = modelFromSetup(entry.configOptions, nil)
		return []map[string]any{{"type": "config", "configOptions": projectConfigOptions(entry.configOptions), "model": entry.model}}
	case "session_info_update":
		if title := textValue(update["title"]); title != "" {
			// Pi commonly emits its placeholder title while replaying a
			// session. Do not let that placeholder erase a title remembered by
			// the controller; an explicit rename is persisted separately.
			if title != "Chat" || entry.title == "Chat" {
				entry.title = title
			}
		}
		if trustedPi {
			piMeta := mapValue(mapValue(update["_meta"])["pi"])
			if value, exists := piMeta["activeRunId"]; exists {
				entry.runID = textValue(value)
			}
		} else {
			entry.runID = ""
		}
		return []map[string]any{{"type": "session-info", "title": entry.title}}
	case "usage_update":
		return applyStandardUsageUpdate(entry, update)
	}
	return nil
}

func applyStandardUsageUpdate(entry *sessionEntry, update map[string]any) []map[string]any {
	size := integerValue(update["size"])
	used := integerValue(update["used"])
	if size < 0 {
		size = 0
	}
	if used < 0 {
		used = 0
	}
	var percent any
	if size > 0 {
		percent = float64(used) / float64(size) * 100
	}
	entry.stats.ContextUsage = map[string]any{"tokens": used, "contextWindow": size, "percent": percent}
	events := []map[string]any{{"type": "context", "contextUsage": entry.stats.ContextUsage}}
	cost := mapValue(update["cost"])
	amount, amountOK := cost["amount"].(float64)
	currency := textValue(cost["currency"])
	if !amountOK || math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 || !validCurrencyCode(currency) {
		return events
	}
	// AUX-15: cost is monotonic within one currency. A compaction can re-emit a
	// smaller authoritative amount, but the displayed total must not regress.
	if entry.stats.CostCurrency != "" && entry.stats.CostCurrency != currency {
		entry.stats.Cost = amount
	} else if amount > entry.stats.Cost {
		entry.stats.Cost = amount
	}
	entry.stats.CostCurrency = currency
	reported := maps.Clone(entry.stats.Reported)
	if reported == nil {
		reported = make(map[string]bool)
	}
	reported["cost"] = true
	entry.stats.Reported = reported
	return append([]map[string]any{{
		"type":         "usage",
		"usage":        map[string]any{"input": entry.stats.Tokens.Input, "output": entry.stats.Tokens.Output, "cacheRead": entry.stats.Tokens.CacheRead, "cacheWrite": entry.stats.Tokens.CacheWrite, "total": entry.stats.Tokens.Total, "cost": entry.stats.Cost},
		"reported":     entry.stats.Reported,
		"costCurrency": currency,
	}}, events...)
}

func validCurrencyCode(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

// mergeMonotonic keeps a running total from regressing when a later
// authoritative update reports a smaller accumulated value (for example after
// compaction). A genuinely newer baseline still wins once it exceeds the total.
func mergeMonotonic(current, next int64) int64 {
	if next > current {
		return next
	}
	return current
}

// Pi updates replace only the supplied fields. Keep unfinished output by call
// ID so interleaved tools and status-only completion cannot erase it.
type toolOutput struct {
	Raw                       any
	Content                   any
	LiveText                  string
	Sequence                  float64
	Truncated                 bool
	SubagentActivityTool      bool
	SubagentActivityEvents    []subagentActivityEvent
	SubagentActivityTruncated bool
}

const (
	maxSubagentActivityEvents          = 32
	maxSubagentActivityIdentifierBytes = 256
)

type subagentActivityEvent struct {
	ChildSessionID string `json:"childSessionId"`
	ToolName       string `json:"toolName"`
}

// Trusted Pi details pass through unchanged. Untrusted metadata stays out
// of agent-visible tool details.
func toolDetailsForAgent(update map[string]any, trustedPi bool) map[string]any {
	if trustedPi {
		return update
	}
	meta := mapValue(update["_meta"])
	if len(meta) == 0 {
		return update
	}
	details := maps.Clone(update)
	cleanMeta := maps.Clone(meta)
	delete(cleanMeta, "pi")
	delete(cleanMeta, "toolNotification")
	if len(cleanMeta) == 0 {
		delete(details, "_meta")
	} else {
		details["_meta"] = cleanMeta
	}
	return details
}

func projectToolOutputAndActivity(entry *sessionEntry, id string, update map[string]any, finished, trustedPi bool) (any, map[string]any) {
	output := entry.pendingToolOutputs[id]
	if raw := update["rawOutput"]; raw != nil {
		output.Raw = raw
	} else if failure := update["error"]; failure != nil {
		output.Raw = failure
	}
	if content := update["content"]; content != nil {
		output.Content = content
	}
	// Pi shell output arrives as ordered deltas, not a final result snapshot.
	notification := map[string]any{}
	if trustedPi {
		notification = mapValue(mapValue(update["_meta"])["toolNotification"])
		projectSubagentActivity(&output, notification)
		native := mapValue(mapValue(mapValue(update["_meta"])["pi"])["subagentActivity"])
		child := textValue(native["sessionId"])
		if output.SubagentActivityTool && validSubagentActivityIdentifier(child) {
			output.SubagentActivityEvents = nil
			events := arrayValue(native["events"])
			start := max(0, len(events)-maxSubagentActivityEvents)
			for _, raw := range events[start:] {
				name := textValue(mapValue(raw)["name"])
				if validSubagentActivityIdentifier(name) {
					output.SubagentActivityEvents = append(output.SubagentActivityEvents, subagentActivityEvent{ChildSessionID: child, ToolName: name})
				}
			}
			output.SubagentActivityTruncated = start > 0
		}
	} else {
		output.LiveText = ""
		output.Sequence = 0
		output.Truncated = false
		output.SubagentActivityTool = false
		output.SubagentActivityEvents = nil
		output.SubagentActivityTruncated = false
	}
	if notification["type"] == "live_output" {
		params := mapValue(notification["params"])
		sequence, _ := params["sequence"].(float64)
		if sequence > output.Sequence {
			output.Sequence = sequence
			output.Truncated = output.Truncated || params["truncated"] == true
			const maxLiveOutput = 256 * 1024
			for _, chunk := range arrayValue(params["chunks"]) {
				text := textValue(mapValue(chunk)["output"])
				remaining := maxLiveOutput - len(output.LiveText)
				if len(text) > remaining {
					output.Truncated = true
					for remaining > 0 && !utf8.RuneStart(text[remaining]) {
						remaining--
					}
					text = text[:remaining]
				}
				output.LiveText += text
			}
		}
	}
	if finished {
		delete(entry.pendingToolOutputs, id)
	} else {
		if entry.pendingToolOutputs == nil {
			entry.pendingToolOutputs = make(map[string]toolOutput)
		}
		entry.pendingToolOutputs[id] = output
	}
	activity := subagentActivityValue(output)
	return toolOutputValue(output), activity
}

func toolOutputValue(output toolOutput) any {
	if output.Raw != nil && len(arrayValue(output.Content)) > 0 {
		return map[string]any{"structuredContent": output.Raw, "content": output.Content}
	}
	if output.Raw != nil {
		return output.Raw
	}
	if output.Content != nil {
		return output.Content
	}
	if output.Truncated {
		return output.LiveText + "\n[Live output truncated]"
	}
	if output.LiveText != "" {
		return output.LiveText
	}
	return nil
}

func toolOutputHasValue(output toolOutput) bool {
	return output.Raw != nil || output.Content != nil || output.Truncated || output.LiveText != ""
}

// The caller holds entry.state. Output maps and slices are detached so the
// response encoder cannot observe later live updates.
func pendingToolPreviewsLocked(entry *sessionEntry) []any {
	ids := make([]string, 0, len(entry.pendingToolOutputs))
	for id := range entry.pendingToolOutputs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	previews := make([]any, 0, len(ids))
	for _, id := range ids {
		output := entry.pendingToolOutputs[id]
		preview := map[string]any{"toolCallId": id}
		if toolOutputHasValue(output) {
			preview["output"] = cloneJSON(toolOutputValue(output))
		}
		if activity := subagentActivityValue(output); activity != nil {
			preview["subagentActivity"] = activity
		}
		previews = append(previews, preview)
	}
	return previews
}

func projectSubagentActivity(output *toolOutput, notification map[string]any) {
	if !output.SubagentActivityTool || notification["type"] != "message" {
		return
	}
	params := mapValue(notification["params"])
	if params["level"] != "info" {
		return
	}
	data := mapValue(params["data"])
	if data["type"] != "subagent_tool_request" {
		return
	}
	childSessionID := textValue(data["subagent_id"])
	toolName := textValue(mapValue(data["tool_call"])["name"])
	if !validSubagentActivityIdentifier(childSessionID) || !validSubagentActivityIdentifier(toolName) || textValue(params["logger"]) != "subagent:"+childSessionID {
		return
	}
	event := subagentActivityEvent{ChildSessionID: childSessionID, ToolName: toolName}
	if len(output.SubagentActivityEvents) == maxSubagentActivityEvents {
		copy(output.SubagentActivityEvents, output.SubagentActivityEvents[1:])
		output.SubagentActivityEvents[len(output.SubagentActivityEvents)-1] = event
		output.SubagentActivityTruncated = true
		return
	}
	output.SubagentActivityEvents = append(output.SubagentActivityEvents, event)
}

func validSubagentActivityIdentifier(value string) bool {
	return value != "" && len(value) <= maxSubagentActivityIdentifierBytes && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func subagentActivityValue(output toolOutput) map[string]any {
	if len(output.SubagentActivityEvents) == 0 {
		return nil
	}
	events := make([]any, len(output.SubagentActivityEvents))
	for index, event := range output.SubagentActivityEvents {
		events[index] = map[string]any{"childSessionId": event.ChildSessionID, "toolName": event.ToolName}
	}
	activity := map[string]any{"events": events}
	if output.SubagentActivityTruncated {
		activity["truncated"] = true
	}
	return activity
}

type messageUsage struct {
	input, output, cacheRead, cacheWrite, total int64
	cost                                        float64
}

func applyPiOnlyUpdate(entry *sessionEntry, kind string, update map[string]any) []map[string]any {
	switch kind {
	case "native_lifecycle":
		event := normalizeNativeErrorFields(maps.Clone(mapValue(update["event"])))
		switch textValue(event["type"]) {
		case "compaction_start":
			entry.compactionActive = true
			return []map[string]any{event}
		case "compaction_end":
			entry.compactionActive = false
			return []map[string]any{event}
		case "auto_retry_start", "auto_retry_end", "summarization_retry_scheduled", "summarization_retry_finished", "thinking_level_changed":
			return []map[string]any{event}
		}
	case "native_summary":
		id := textValue(update["messageId"])
		for _, message := range entry.messages {
			if id != "" && mapValue(message)["messageId"] == id {
				return nil
			}
		}
		summary := textValue(update["summary"])
		content := update["content"]
		if summary != "" {
			content = []any{map[string]any{"type": "text", "text": summary}}
		}
		if value, ok := content.(string); ok {
			content = []any{map[string]any{"type": "text", "text": value}}
		}
		message := map[string]any{"role": "assistant", "messageId": id, "content": content, "presentation": map[string]any{"kind": update["summaryKind"], "summary": summary, "tokensBefore": update["tokensBefore"]}}
		entry.messages = append(entry.messages, message)
		entry.stats.TotalMessages = len(entry.messages)
		return []map[string]any{{"type": "message_start", "message": message}}

	case "message_usage":
		usage := mapValue(update["usage"])
		input := integerValue(usage["inputTokens"])
		output := integerValue(usage["outputTokens"])
		cacheRead := integerValue(usage["cacheReadTokens"])
		cacheWrite := integerValue(usage["cacheWriteTokens"])
		total, totalReported := numeric(usage["totalTokens"])
		if !totalReported {
			total = input + output + cacheRead + cacheWrite
		}
		cost := floatValue(usage["cost"])
		if id := textValue(update["messageId"]); id != "" {
			if entry.messageUsage == nil {
				entry.messageUsage = make(map[string]messageUsage)
			}
			previous := entry.messageUsage[id]
			entry.messageUsage[id] = messageUsage{input, output, cacheRead, cacheWrite, total, cost}
			input -= previous.input
			output -= previous.output
			cacheRead -= previous.cacheRead
			cacheWrite -= previous.cacheWrite
			total -= previous.total
			cost -= previous.cost
		}
		entry.stats.Tokens.Input += input
		entry.stats.Tokens.Output += output
		entry.stats.Tokens.CacheRead += cacheRead
		entry.stats.Tokens.CacheWrite += cacheWrite
		entry.stats.Tokens.Total += total
		entry.stats.Cost += cost
		reported := maps.Clone(entry.stats.Reported)
		if reported == nil {
			reported = make(map[string]bool)
		}
		for field, key := range map[string]string{"inputTokens": "input", "outputTokens": "output", "cacheReadTokens": "cacheRead", "cacheWriteTokens": "cacheWrite", "cost": "cost"} {
			if _, ok := usage[field].(float64); ok {
				reported[key] = true
			}
		}
		if totalReported || total != 0 {
			reported["total"] = true
		}
		entry.stats.Reported = reported
		return []map[string]any{{"type": "usage", "usage": map[string]any{"input": entry.stats.Tokens.Input, "output": entry.stats.Tokens.Output, "cacheRead": entry.stats.Tokens.CacheRead, "cacheWrite": entry.stats.Tokens.CacheWrite, "total": entry.stats.Tokens.Total, "cost": entry.stats.Cost}, "reported": entry.stats.Reported}}
	case "usage_update":
		input := integerValue(update["accumulatedInputTokens"])
		output := integerValue(update["accumulatedOutputTokens"])
		// AUX-15: every entry, including compaction and branch summaries, adds
		// to the aggregate. The SDK's accumulated counters can reset at
		// compaction, so merge monotonically instead of overwriting; live
		// context usage below still comes straight from the SDK.
		entry.stats.Tokens.Input = mergeMonotonic(entry.stats.Tokens.Input, input)
		entry.stats.Tokens.Output = mergeMonotonic(entry.stats.Tokens.Output, output)
		entry.stats.Tokens.Total = entry.stats.Tokens.Input + entry.stats.Tokens.Output + entry.stats.Tokens.CacheRead + entry.stats.Tokens.CacheWrite
		reported := maps.Clone(entry.stats.Reported)
		if reported == nil {
			reported = make(map[string]bool)
		}
		reported["input"], reported["output"], reported["total"] = true, true, true
		if cost, ok := update["accumulatedCost"].(float64); ok && !math.IsNaN(cost) && !math.IsInf(cost, 0) && cost >= 0 && cost > entry.stats.Cost {
			entry.stats.Cost = cost
			reported["cost"] = true
		}
		if currency := textValue(update["costCurrency"]); validCurrencyCode(currency) {
			entry.stats.CostCurrency = currency
		}
		entry.stats.Reported = reported
		limit := integerValue(update["contextLimit"])
		used := integerValue(update["used"])
		var percent any
		if limit > 0 {
			percent = float64(used) / float64(limit) * 100
		}
		entry.stats.ContextUsage = map[string]any{"tokens": used, "contextWindow": limit, "percent": percent}
		return []map[string]any{
			{"type": "usage", "usage": map[string]any{"input": entry.stats.Tokens.Input, "output": entry.stats.Tokens.Output, "cacheRead": entry.stats.Tokens.CacheRead, "cacheWrite": entry.stats.Tokens.CacheWrite, "total": entry.stats.Tokens.Total, "cost": entry.stats.Cost}, "reported": entry.stats.Reported, "costCurrency": entry.stats.CostCurrency},
			{"type": "context", "contextUsage": entry.stats.ContextUsage},
		}
	case "status_message":
		status := mapValue(update["status"])
		kind := textValue(status["type"])
		// Native status text is free-form and can embed credentials, URLs or
		// absolute paths. Reduce it at this boundary before it is persisted in
		// the settlement or projected as an activity/error event.
		message := normalizeNativeEventText(textValue(status["message"]))
		if kind == "notice" || kind == "progress" {
			return []map[string]any{{"type": "activity", "status": kind, "text": clipUTF16(message, 4000)}}
		}
		// Pi may publish a terminal status before session.prompt returns.
		// Keep the browser busy until that RPC supplies the authoritative result;
		// otherwise a second optimistic prompt can be admitted and then rejected.
		if entry.promptActive {
			return nil
		}
		lowerKind := strings.ToLower(kind)
		if strings.Contains(lowerKind, "error") || strings.Contains(lowerKind, "fail") {
			// Keep the stable failure class even when redaction removed every
			// native detail; the browser must not turn an error into success.
			if message == "" {
				message = nativeFailureSummary
			}
			entry.streaming = false
			entry.settlement = &SessionSettlement{StopReason: "error", ErrorMessage: message}
			return []map[string]any{{"type": "error", "error": message}}
		}
		if terminalStatusKind(lowerKind) {
			entry.streaming = false
			entry.settlement = &SessionSettlement{StopReason: kind}
			return []map[string]any{{"type": "complete", "status": kind}}
		}
	}
	return nil
}

func terminalStatusKind(kind string) bool {
	kind = strings.ToLower(kind)
	return strings.Contains(kind, "error") || strings.Contains(kind, "fail") || strings.Contains(kind, "complete") || strings.Contains(kind, "idle") || strings.Contains(kind, "done") || strings.Contains(kind, "cancel")
}

func appendMessageBlock(entry *sessionEntry, role string, block map[string]any, identity ...string) {
	messageID := ""
	entryID := ""
	if len(identity) > 0 {
		messageID = identity[0]
	}
	if len(identity) > 1 {
		entryID = identity[1]
	}
	differentMessage := false
	if len(entry.messages) > 0 && messageID != "" {
		differentMessage = textValue(mapValue(entry.messages[len(entry.messages)-1])["messageId"]) != messageID
	}
	if len(entry.messages) == 0 || differentMessage || textValue(mapValue(entry.messages[len(entry.messages)-1])["role"]) != role {
		if role == "user" {
			entry.userResourceBytes = 0
		}
		message := map[string]any{"role": role, "content": []any{block}, "messageId": messageID}
		// AUX-14: keep the native session-entry id on the projected user message
		// so "Edit from here" branches from the exact native entry. Other roles
		// and entry-less messages carry no field.
		if entryID != "" {
			message["entryId"] = entryID
		}
		entry.messages = append(entry.messages, message)
		return
	}
	message := mapValue(entry.messages[len(entry.messages)-1])
	if entryID != "" && textValue(message["entryId"]) == "" {
		// A later fragment of the same message repeats the id; a distinct
		// message without enough identity keeps the first entry rather than
		// silently retargeting the branch.
		message["entryId"] = entryID
	}
	content, ok := message["content"].([]any)
	if !ok {
		if text, textOK := message["content"].(string); textOK {
			content = []any{map[string]any{"type": "text", "text": text}}
		}
	}
	if len(content) > 0 {
		last := mapValue(content[len(content)-1])
		if last["type"] == block["type"] && (block["type"] == "text" || block["type"] == "thinking") {
			key := "text"
			if block["type"] == "thinking" {
				key = "thinking"
			}
			last[key] = textValue(last[key]) + textValue(block[key])
			content[len(content)-1] = last
			message["content"] = content
			entry.messages[len(entry.messages)-1] = message
			return
		}
	}
	message["content"] = append(content, block)
	entry.messages[len(entry.messages)-1] = message
}

func appendUserResourceMarker(entry *sessionEntry, marker map[string]any, byteLength int) bool {
	if len(entry.messages) == 0 || textValue(mapValue(entry.messages[len(entry.messages)-1])["role"]) != "user" {
		entry.userResourceBytes = 0
	}
	if entry.userResourceBytes+byteLength > maxTextAttachmentTotalBytes {
		return false
	}
	if len(entry.messages) > 0 {
		last := mapValue(entry.messages[len(entry.messages)-1])
		if textValue(last["role"]) == "user" {
			count := 0
			for _, block := range contentBlocks(last["content"]) {
				if textValue(mapValue(block)["type"]) == "resource" {
					count++
				}
			}
			if count >= maxTextAttachmentCount {
				return false
			}
		}
	}
	appendMessageBlock(entry, "user", marker)
	entry.userResourceBytes += byteLength
	return true
}

func contentBlocks(content any) []any {
	blocks, _ := content.([]any)
	return blocks
}

// Detach mutable transcript objects before publishing them outside the state lock.
// Strings (including images) remain shared rather than being re-encoded and copied.
func cloneJSON(value any) any {
	switch value := value.(type) {
	case map[string]any:
		if value == nil {
			return value
		}
		clone := make(map[string]any, len(value))
		for key, item := range value {
			clone[key] = cloneJSON(item)
		}
		return clone
	case []any:
		if value == nil {
			return value
		}
		clone := make([]any, len(value))
		for index, item := range value {
			clone[index] = cloneJSON(item)
		}
		return clone
	default:
		return value
	}
}

func consumeEchoText(entry *sessionEntry, text, entryID string) (matched, attached bool) {
	echo := entry.pendingEcho
	if echo == nil || echo.offset > len(echo.text) || !strings.HasPrefix(echo.text[echo.offset:], text) {
		entry.pendingEcho = nil
		return false, false
	}
	if attachEchoEntryID(entry, echo, entryID) {
		attached = true
	}
	echo.offset += len(text)
	if echoComplete(echo) {
		entry.promptAcknowledged = true
		entry.pendingEcho = nil
	}
	return true, attached
}

func consumeEchoImage(entry *sessionEntry, image map[string]any, entryID string) (matched, attached bool) {
	echo := entry.pendingEcho
	if echo == nil {
		return false, false
	}
	for index, expected := range echo.images {
		if !echo.matched[index] && expected["data"] == image["data"] && expected["mimeType"] == image["mimeType"] {
			echo.matched[index] = true
			if attachEchoEntryID(entry, echo, entryID) {
				attached = true
			}
			if echoComplete(echo) {
				entry.promptAcknowledged = true
				entry.pendingEcho = nil
			}
			return true, attached
		}
	}
	entry.pendingEcho = nil
	return false, false
}

func consumeEchoResource(entry *sessionEntry, marker map[string]any, entryID string) (matched, attached bool) {
	echo := entry.pendingEcho
	if echo == nil {
		return false, false
	}
	for index, expected := range echo.resources {
		if !echo.resourceMatched[index] && expected["name"] == marker["name"] && expected["mimeType"] == marker["mimeType"] {
			echo.resourceMatched[index] = true
			if attachEchoEntryID(entry, echo, entryID) {
				attached = true
			}
			if echoComplete(echo) {
				entry.promptAcknowledged = true
				entry.pendingEcho = nil
			}
			return true, attached
		}
	}
	entry.pendingEcho = nil
	return false, false
}

// consumeUserEcho routes one native user fragment to the pending optimistic
// message. It reports whether the fragment belonged to that message and, when
// the host supplied a previously unknown entry id, returns the enriched
// projected message so the caller can publish it without waiting for a replay.
func consumeUserEcho(entry *sessionEntry, kind, entryID string, payload map[string]any) (matched bool, enriched map[string]any) {
	echoIndex := -1
	if entry.pendingEcho != nil {
		echoIndex = entry.pendingEcho.messageIndex
	}
	var attached bool
	switch kind {
	case "text":
		matched, attached = consumeEchoText(entry, textValue(payload["text"]), entryID)
	case "image":
		matched, attached = consumeEchoImage(entry, payload, entryID)
	case "resource":
		matched, attached = consumeEchoResource(entry, payload, entryID)
	}
	if matched && attached && echoIndex >= 0 && echoIndex < len(entry.messages) {
		if message, ok := cloneJSON(entry.messages[echoIndex]).(map[string]any); ok {
			return true, message
		}
	}
	return matched, nil
}

// attachEchoEntryID records the native session-entry id on the optimistic user
// message being consumed. It only fills an absent id, so a later fragment or
// replay cannot retarget an earlier entry and no id is ever fabricated. The
// pending echo's rollback snapshot is updated in step so a proven rejection can
// still recognize the exact optimistic message.
func attachEchoEntryID(entry *sessionEntry, echo *userEcho, entryID string) bool {
	if echo == nil || entryID == "" || echo.messageIndex < 0 || echo.messageIndex >= len(entry.messages) {
		return false
	}
	message, ok := entry.messages[echo.messageIndex].(map[string]any)
	if !ok || textValue(message["entryId"]) != "" {
		return false
	}
	message["entryId"] = entryID
	entry.messages[echo.messageIndex] = message
	if optimistic, ok := echo.optimistic.(map[string]any); ok {
		optimistic["entryId"] = entryID
	}
	return true
}

func echoComplete(echo *userEcho) bool {
	if echo.offset < len(echo.text) {
		return false
	}
	for _, matched := range echo.matched {
		if !matched {
			return false
		}
	}
	for _, matched := range echo.resourceMatched {
		if !matched {
			return false
		}
	}
	return true
}

func (m *SessionManager) emit(channel string, data any) {
	if m.publish != nil {
		m.publish(channel, stripNilFields(data))
	}
}

// lifecycleKind reports native run annotations that travel verbatim to
// browsers without touching the transcript projection. Prompt settlement
// stays authoritative in the prompt RPC path; the first agent_end is never
// treated as final.
func lifecycleKind(kind string) bool {
	switch kind {
	case "agent_start", "agent_end", "agent_settled",
		"compaction_start", "compaction_end",
		"auto_retry_start", "auto_retry_end",
		"summarization_retry_scheduled", "summarization_retry_finished",
		"thinking_level_changed":
		return true
	}
	return false
}

func objectValue(value any) map[string]any {
	encoded, _ := json.Marshal(value)
	var result map[string]any
	_ = json.Unmarshal(encoded, &result)
	if result == nil {
		return map[string]any{}
	}
	return result
}

func mapValue(value any) map[string]any {
	result, _ := value.(map[string]any)
	if result == nil {
		return map[string]any{}
	}
	return result
}

func arrayValue(value any) []any {
	result, _ := value.([]any)
	if result == nil {
		return []any{}
	}
	return result
}

func textValue(value any) string { text, _ := value.(string); return text }
func optionalText(value any) any {
	if text := textValue(value); text != "" {
		return text
	}
	return nil
}
func integerValue(value any) int64 { number, _ := numeric(value); return number }
func floatValue(value any) float64 { number, _ := value.(float64); return number }

func stripNilFields(value any) any {
	if object, ok := value.(map[string]any); ok {
		for key, entry := range object {
			if entry == nil {
				delete(object, key)
			}
		}
	}
	return value
}
