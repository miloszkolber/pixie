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

	piwire "github.com/miloszkolber/pixie/internal/piprotocol"
)

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
	events := applySessionUpdate(target, kind, update, origin)
	if !publish && kind == "tool_call" {
		// Replay is historical evidence, never authority for a new HTTP question.
		target.consumedQuestions[textValue(update["toolCallId"])] = true
	}
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
			wakeQueue = wakeQueue || event["type"] == "complete" || event["type"] == "error"
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
			if role == "user" && consumeEchoResource(entry, marker) {
				return nil
			}
			if role != "user" || !appendUserResourceMarker(entry, marker, byteLength) {
				return nil
			}
			entry.stats.TotalMessages = len(entry.messages)
			return []map[string]any{{"type": "message_start", "message": entry.messages[len(entry.messages)-1]}}
		}
		if textValue(content["type"]) == "image" {
			image := map[string]any{"type": "image", "data": textValue(content["data"]), "mimeType": textValue(content["mimeType"])}
			if role == "user" && consumeEchoImage(entry, image) {
				return nil
			}
			appendMessageBlock(entry, role, image, textValue(update["messageId"]))
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
		if role == "user" && consumeEchoText(entry, text) {
			return nil
		}
		appendMessageBlock(entry, role, map[string]any{"type": "text", "text": text}, textValue(update["messageId"]))
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
		if strings.HasSuffix(toolName, "__ask_user_question") {
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
		delete(entry.consumedQuestions, toolID)
		if entry.toolChanged != nil {
			close(entry.toolChanged)
			entry.toolChanged = nil
		}
		delete(entry.appAttachments, toolID)
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
		var attachment map[string]any
		if trustedPi {
			attachment = projectAppAttachment(entry, toolID, update)
		} else {
			delete(entry.appAttachments, toolID)
		}
		if finished {
			message := map[string]any{"role": "toolResult", "toolCallId": toolID, "content": result, "details": toolDetailsForAgent(update, trustedPi)}
			if activity != nil {
				message["subagentActivity"] = cloneJSON(activity)
			}
			if attachment != nil {
				message["app"] = attachment
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
		if attachment != nil {
			event["app"] = attachment
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
	entry.stats.Cost = amount
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

func projectToolOutput(entry *sessionEntry, id string, update map[string]any, finished bool) any {
	result, _ := projectToolOutputAndActivity(entry, id, update, finished, true)
	return result
}

const (
	maxSubagentActivityEvents          = 32
	maxSubagentActivityIdentifierBytes = 256
)

type subagentActivityEvent struct {
	ChildSessionID string `json:"childSessionId"`
	ToolName       string `json:"toolName"`
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
		if state, ok := entry.appAttachments[id]; ok {
			preview["app"] = appAttachmentValue(state.attachment)
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
		event := mapValue(update["event"])
		switch textValue(event["type"]) {
		case "compaction_start", "compaction_end", "auto_retry_start", "auto_retry_end", "summarization_retry_scheduled", "summarization_retry_finished", "thinking_level_changed":
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
		entry.stats.Tokens.Input = input
		entry.stats.Tokens.Output = output
		entry.stats.Tokens.Total = input + output + entry.stats.Tokens.CacheRead + entry.stats.Tokens.CacheWrite
		reported := maps.Clone(entry.stats.Reported)
		if reported == nil {
			reported = make(map[string]bool)
		}
		reported["input"], reported["output"], reported["total"] = true, true, true
		if cost, ok := update["accumulatedCost"].(float64); ok && !math.IsNaN(cost) && !math.IsInf(cost, 0) && cost >= 0 {
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
		message := textValue(status["message"])
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
	if len(identity) > 0 {
		messageID = identity[0]
	}
	differentMessage := false
	if len(entry.messages) > 0 && messageID != "" {
		differentMessage = textValue(mapValue(entry.messages[len(entry.messages)-1])["messageId"]) != messageID
	}
	if len(entry.messages) == 0 || differentMessage || textValue(mapValue(entry.messages[len(entry.messages)-1])["role"]) != role {
		if role == "user" {
			entry.userResourceBytes = 0
		}
		entry.messages = append(entry.messages, map[string]any{"role": role, "content": []any{block}, "messageId": messageID})
		return
	}
	message := mapValue(entry.messages[len(entry.messages)-1])
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

func consumeEchoText(entry *sessionEntry, text string) bool {
	echo := entry.pendingEcho
	if echo == nil || echo.offset > len(echo.text) || !strings.HasPrefix(echo.text[echo.offset:], text) {
		entry.pendingEcho = nil
		return false
	}
	echo.offset += len(text)
	if echoComplete(echo) {
		entry.promptAcknowledged = true
		entry.pendingEcho = nil
	}
	return true
}

func consumeEchoImage(entry *sessionEntry, image map[string]any) bool {
	echo := entry.pendingEcho
	if echo == nil {
		return false
	}
	for index, expected := range echo.images {
		if !echo.matched[index] && expected["data"] == image["data"] && expected["mimeType"] == image["mimeType"] {
			echo.matched[index] = true
			if echoComplete(echo) {
				entry.promptAcknowledged = true
				entry.pendingEcho = nil
			}
			return true
		}
	}
	entry.pendingEcho = nil
	return false
}

func consumeEchoResource(entry *sessionEntry, marker map[string]any) bool {
	echo := entry.pendingEcho
	if echo == nil {
		return false
	}
	for index, expected := range echo.resources {
		if !echo.resourceMatched[index] && expected["name"] == marker["name"] && expected["mimeType"] == marker["mimeType"] {
			echo.resourceMatched[index] = true
			if echoComplete(echo) {
				entry.promptAcknowledged = true
				entry.pendingEcho = nil
			}
			return true
		}
	}
	entry.pendingEcho = nil
	return false
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
