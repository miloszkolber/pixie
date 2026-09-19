package controller

import (
	"context"
	"encoding/json"
	"fmt"
	piwire "github.com/miloszkolber/pixie/piprotocol"
	"strings"
)

// projectPiEvent translates native SDK events into the controller's durable UI
// projection. It never changes Pi's tool selection or execution behavior.
func projectPiEvent(ctx context.Context, sink PiEvents, raw json.RawMessage) error {
	if sink == nil {
		return nil
	}
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	id := textValue(envelope["sessionId"])
	if id == "" {
		return fmt.Errorf("Pi event has no session ID")
	}
	event := mapValue(envelope["event"])
	emit := func(kind string, update map[string]any) error {
		update["sessionUpdate"] = kind
		return sink.SessionUpdate(ctx, piwire.SessionNotification{SessionId: id, Update: update})
	}
	extension := func(kind string, update map[string]any) error {
		update["sessionUpdate"] = kind
		b, _ := json.Marshal(map[string]any{"sessionId": id, "update": update})
		return sink.Extension(ctx, "pi.session.update", b)
	}
	message := mapValue(event["message"])
	messageID := textValue(message["messageId"])
	if messageID == "" && message["timestamp"] != nil {
		messageID = fmt.Sprintf("%s:%v", textValue(message["role"]), message["timestamp"])
	}
	toolStart := func(tool map[string]any) error {
		name := textValue(tool["name"])
		if name == "" {
			name = textValue(tool["toolName"])
		}
		toolID := textValue(tool["id"])
		if toolID == "" {
			toolID = textValue(tool["toolCallId"])
		}
		source := "builtin"
		actual := name
		input := tool["arguments"]
		if parts := strings.SplitN(name, "__", 2); len(parts) == 2 {
			source, actual = parts[0], parts[1]
		}
		if proxy := mapValue(input); name == "mcp" && textValue(proxy["tool"]) != "" {
			actual, source = textValue(proxy["tool"]), textValue(proxy["server"])
			if source == "" {
				source = "mcp"
			}
			input = proxy["args"]
			if encoded, ok := input.(string); ok {
				var arguments map[string]any
				if json.Unmarshal([]byte(encoded), &arguments) == nil {
					input = arguments
				}
			}
		}
		return emit("tool_call", map[string]any{"toolCallId": toolID, "title": actual, "status": "in_progress", "rawInput": input, "_meta": map[string]any{"pi": map[string]any{"toolCall": map[string]any{"toolName": actual, "extensionName": source}}}})
	}
	toolEnd := func(toolID string, result map[string]any, finished bool, isError bool) error {
		status := "in_progress"
		if finished {
			status = "completed"
		}
		if isError || mapValue(mapValue(result["details"])["mcp"])["isError"] == true {
			status = "failed"
		}
		details := mapValue(result["details"])
		meta := map[string]any{"pi": map[string]any{"subagentActivity": details["subagent"]}}
		if err := emit("tool_call_update", map[string]any{"toolCallId": toolID, "status": status, "rawOutput": result, "_meta": meta}); err != nil {
			return err
		}
		// Plan state updates only on successful, finished results: a failed,
		// partial, or unrelated task-shaped payload must never replace the
		// displayed plan, including on replay.
		if !isError && finished {
			if plan := mapValue(details["plan"]); plan["entries"] != nil {
				return emit("plan", plan)
			}
			// Dual-read migration: the legacy update_plan envelope above is
			// read back from persisted transcripts only, while upstream
			// `todo` results (details.tasks/nextId) project onto the same
			// plan display.
			if entries := projectTodoPlanEntries(details); entries != nil {
				return emit("plan", map[string]any{"entries": entries})
			}
		}
		return nil
	}
	usage := func() error {
		if message["role"] != "assistant" {
			return nil
		}
		u := mapValue(message["usage"])
		if len(u) == 0 {
			return nil
		}
		return extension("message_usage", map[string]any{"messageId": messageID, "usage": map[string]any{"inputTokens": u["input"], "outputTokens": u["output"], "cacheReadTokens": u["cacheRead"], "cacheWriteTokens": u["cacheWrite"], "totalTokens": u["totalTokens"], "cost": mapValue(u["cost"])["total"]}})
	}
	switch textValue(event["type"]) {
	case "replay_message", "message_start":
		replay := event["type"] == "replay_message"
		role := textValue(message["role"])
		if role == "summary" {
			return extension("native_summary", message)
		}
		if role == "plan" {
			return emit("plan", map[string]any{"entries": message["entries"]})
		}
		if role == "custom" && message["display"] != true {
			return nil
		}
		if role == "custom" {
			message["summaryKind"] = "custom"
			return extension("native_summary", message)
		}
		if role == "toolResult" {
			if replay {
				return toolEnd(textValue(message["toolCallId"]), message, message["partial"] != true, message["isError"] == true)
			}
			return nil
		}
		if !replay && role != "user" {
			return nil
		}
		content := message["content"]
		if display := message["displayContent"]; display != nil {
			content = display
		}
		if value, ok := content.(string); ok {
			content = []any{map[string]any{"type": "text", "text": value}}
		}
		// AUX-14: the host projects the native session-entry id onto user
		// messages. Carry it so the controller transcript keeps the exact entry
		// "Edit from here" branches from; a message without one stays entry-less.
		entryID := ""
		if role == "user" {
			entryID = textValue(message["entryId"])
		}
		for _, value := range contentBlocks(content) {
			block := mapValue(value)
			kind := "agent_message_chunk"
			if role == "user" {
				kind = "user_message_chunk"
			}
			switch textValue(block["type"]) {
			case "toolCall":
				if err := toolStart(block); err != nil {
					return err
				}
				continue
			case "thinking":
				kind = "agent_thought_chunk"
				block = map[string]any{"type": "text", "text": block["thinking"]}
			case "image":
				if source := mapValue(block["source"]); source["type"] == "base64" {
					block = map[string]any{"type": "image", "data": source["data"], "mimeType": source["mediaType"]}
				}
			}
			update := map[string]any{"messageId": messageID, "content": block}
			if entryID != "" {
				update["entryId"] = entryID
			}
			if err := emit(kind, update); err != nil {
				return err
			}
		}
		if replay {
			return usage()
		}
	case "message_update":
		delta := mapValue(event["assistantMessageEvent"])
		kind := ""
		switch delta["type"] {
		case "text_delta":
			kind = "agent_message_chunk"
		case "thinking_delta":
			kind = "agent_thought_chunk"
		}
		if kind != "" {
			return emit(kind, map[string]any{"messageId": messageID, "content": map[string]any{"type": "text", "text": delta["delta"]}})
		}
	case "message_end":
		return usage()
	case "tool_execution_start":
		return toolStart(map[string]any{"toolCallId": event["toolCallId"], "toolName": event["toolName"], "arguments": event["args"]})
	case "tool_execution_update":
		return toolEnd(textValue(event["toolCallId"]), mapValue(event["partialResult"]), false, false)
	case "tool_execution_end":
		return toolEnd(textValue(event["toolCallId"]), mapValue(event["result"]), true, event["isError"] == true)
	case "run_start", "run_end":
		if err := emit("session_info_update", map[string]any{"_meta": map[string]any{"pi": map[string]any{"activeRunId": textValue(event["runId"])}}}); err != nil {
			return err
		}
		if event["type"] == "run_end" {
			status := "complete"
			if event["stopReason"] == "error" {
				status = "error"
			}
			return extension("status_message", map[string]any{"status": map[string]any{"type": status}})
		}
	case "configuration_changed":
		return emit("config_option_update", map[string]any{"configOptions": event["configOptions"]})
	case "session_info_changed":
		return emit("session_info_update", map[string]any{"title": event["name"]})
	case "plan":
		return emit("plan", map[string]any{"entries": event["entries"]})
	case "extension_error":
		return extension("status_message", map[string]any{"status": map[string]any{"type": "notice", "message": event["error"]}})
	case "agent_end":
		return emit("agent_end", projectAgentEnd(event))
	case "agent_start", "agent_settled",
		"compaction_start", "compaction_end",
		"auto_retry_start", "auto_retry_end",
		"summarization_retry_scheduled", "summarization_retry_finished",
		"thinking_level_changed":
		// One prompt can emit several agent_end events (retry, compaction,
		// extension-injected or queued turns). The browser keeps its stream
		// open until prompt settlement; these events only annotate it, so
		// they travel verbatim without touching the transcript projection.
		// Stale late events are dropped by the controller's run guard.
		update := map[string]any{}
		for key, value := range event {
			if key != "type" {
				update[key] = value
			}
		}
		return emit(textValue(event["type"]), update)
	case piwire.UiRequestEvent, piwire.UiNotifyEvent, piwire.UiCancelEvent,
		piwire.UiStatusEvent, piwire.UiWidgetEvent, piwire.UiTitleEvent, piwire.UiWorkingEvent:
		return projectUiEvent(event, emit, extension)
	case nativeUiRequestEvent:
		// Pi 0.85.1 RPC emits raw extension_ui_request frames rather than the
		// legacy host's pixie:ui:* events. Map the method-specific fields onto
		// the same projection so the browser contract is unchanged.
		return projectNativeUiEvent(event, emit, extension)
	}
	return nil
}

// nativeUiRequestEvent is the raw Pi RPC extension UI request frame type.
const nativeUiRequestEvent = "extension_ui_request"

// AUX-32: Pi's agent_end payload carries the full native message array, which
// can embed a hostile tool result. The browser-visible event never forwards
// that array; it carries only a bounded, sanitized stop/retry summary. The
// failure/stop class stays visible through willRetry, stopReason and the
// redacted errorMessage while arbitrary native text does not cross the boundary.
func projectAgentEnd(event map[string]any) map[string]any {
	update := map[string]any{"willRetry": event["willRetry"] == true}
	messages := arrayValue(event["messages"])
	if len(messages) == 0 {
		return update
	}
	summary := map[string]any{"messageCount": len(messages)}
	for index := len(messages) - 1; index >= 0; index-- {
		message := mapValue(messages[index])
		if textValue(message["role"]) != "assistant" {
			continue
		}
		if stop := textValue(message["stopReason"]); stop != "" {
			summary["stopReason"] = stop
		}
		if failure := textValue(message["errorMessage"]); failure != "" {
			summary["errorMessage"] = normalizeNativeEventText(failure)
		}
		break
	}
	update["summary"] = summary
	return update
}

// projectNativeUiEvent translates one raw Pi extension_ui_request frame into
// the controller's UI update shapes. Blocking dialogs become ui_request;
// passive notify/status/widget/title/working methods fan out without pending
// state. Unknown methods and the terminal-only set_editor_text are dropped.
func projectNativeUiEvent(
	event map[string]any,
	emit func(kind string, update map[string]any) error,
	extension func(kind string, update map[string]any) error,
) error {
	requestID := textValue(event["id"])
	if requestID == "" {
		return nil
	}
	switch textValue(event["method"]) {
	case piwire.UiPrimitiveSelect, piwire.UiPrimitiveConfirm, piwire.UiPrimitiveInput, piwire.UiPrimitiveEditor:
		mapped := map[string]any{
			"type":      piwire.UiRequestEvent,
			"requestId": requestID,
			"primitive": textValue(event["method"]),
			"title":     event["title"],
		}
		for _, key := range []string{"message", "options", "placeholder", "prefill", "timeout"} {
			if value, exists := event[key]; exists {
				mapped[key] = value
			}
		}
		return projectUiEvent(mapped, emit, extension)
	case "notify":
		return projectUiEvent(map[string]any{
			"type":    piwire.UiNotifyEvent,
			"message": event["message"],
			"level":   event["notifyType"],
		}, emit, extension)
	case "setStatus":
		return projectUiEvent(map[string]any{
			"type": piwire.UiStatusEvent,
			"key":  event["statusKey"],
			"text": event["statusText"],
		}, emit, extension)
	case "setWidget":
		mapped := map[string]any{"type": piwire.UiWidgetEvent, "key": event["widgetKey"]}
		if lines, exists := event["widgetLines"]; exists {
			mapped["lines"] = lines
		}
		if placement := textValue(event["widgetPlacement"]); placement != "" {
			mapped["placement"] = placement
		}
		return projectUiEvent(mapped, emit, extension)
	case "setTitle":
		return projectUiEvent(map[string]any{"type": piwire.UiTitleEvent, "title": event["title"]}, emit, extension)
	case "setWorkingMessage":
		return projectUiEvent(map[string]any{"type": piwire.UiWorkingEvent, "message": event["message"]}, emit, extension)
	}
	return nil
}

// projectUiEvent translates generic extension UI bridge events into
// controller session updates. Dialog state itself is registered in
// applyUpdate (session-bound, single-use); the projection only carries the
// request to browsers. Unknown shapes are dropped: a malformed host event
// must never break the Pi connection.
func projectUiEvent(
	event map[string]any,
	emit func(kind string, update map[string]any) error,
	extension func(kind string, update map[string]any) error,
) error {
	switch textValue(event["type"]) {
	case piwire.UiRequestEvent:
		update := map[string]any{
			"requestId": textValue(event["requestId"]),
			"sessionId": textValue(event["sessionId"]),
			"primitive": textValue(event["primitive"]),
			"title":     textValue(event["title"]),
		}
		for _, key := range []string{"message", "options", "placeholder", "prefill", "timeout"} {
			if value, exists := event[key]; exists {
				update[key] = value
			}
		}
		return emit("ui_request", update)
	case piwire.UiNotifyEvent:
		return extension("ui_notify", map[string]any{
			"message": textValue(event["message"]),
			"level":   textValue(event["level"]),
		})
	case piwire.UiCancelEvent:
		return emit("ui_cancel", map[string]any{"requestId": textValue(event["requestId"])})
	case piwire.UiStatusEvent:
		return emit("ui_status", map[string]any{
			"key":  textValue(event["key"]),
			"text": textValue(event["text"]),
		})
	case piwire.UiWidgetEvent:
		update := map[string]any{"key": textValue(event["key"])}
		if lines, ok := event["lines"].([]any); ok {
			update["lines"] = lines
		} else if lines, ok := event["lines"].([]string); ok {
			converted := make([]any, 0, len(lines))
			for _, line := range lines {
				converted = append(converted, line)
			}
			update["lines"] = converted
		}
		if placement := textValue(event["placement"]); placement != "" {
			update["placement"] = placement
		}
		return emit("ui_widget", update)
	case piwire.UiTitleEvent:
		return emit("ui_title", map[string]any{"title": textValue(event["title"])})
	case piwire.UiWorkingEvent:
		return emit("ui_working", map[string]any{"message": textValue(event["message"])})
	}
	return nil
}
