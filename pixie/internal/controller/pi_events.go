package controller

import (
	"context"
	"encoding/json"
	"fmt"
	piwire "github.com/miloszkolber/pixie/internal/piprotocol"
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
		if parts := strings.SplitN(name, "__", 2); len(parts) == 2 {
			source, actual = parts[0], parts[1]
		}
		if name == "delegate" {
			source = "summon"
		}
		return emit("tool_call", map[string]any{"toolCallId": toolID, "title": actual, "status": "in_progress", "rawInput": tool["arguments"], "_meta": map[string]any{"pi": map[string]any{"toolCall": map[string]any{"toolName": actual, "extensionName": source}}}})
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
		meta := map[string]any{"pi": map[string]any{"mcpApp": mapValue(details["mcp"])["app"], "subagentActivity": details["subagent"]}}
		if err := emit("tool_call_update", map[string]any{"toolCallId": toolID, "status": status, "rawOutput": result, "_meta": meta}); err != nil {
			return err
		}
		if plan := mapValue(details["plan"]); plan["entries"] != nil {
			return emit("plan", plan)
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
			if err := emit(kind, map[string]any{"messageId": messageID, "content": block}); err != nil {
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
	case "plan":
		return emit("plan", map[string]any{"entries": event["entries"]})
	case "extension_error":
		return extension("status_message", map[string]any{"status": map[string]any{"type": "notice", "message": event["error"]}})
	}
	return nil
}
