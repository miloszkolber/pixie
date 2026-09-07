package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

const objectiveBodyLimit = 1024 * 1024

type ObjectiveHandler struct {
	Sessions  *SessionManager
	Schedules *Schedules
}

func (h ObjectiveHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || (mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json")) {
		http.Error(response, "content type must be JSON", http.StatusUnsupportedMediaType)
		return
	}
	authorization := request.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") {
		http.Error(response, "unauthorized", http.StatusUnauthorized)
		return
	}
	projectID, sessionID, ok := h.Sessions.ObjectiveOwner(strings.TrimPrefix(authorization, "Bearer "))
	if !ok {
		http.Error(response, "unauthorized", http.StatusUnauthorized)
		return
	}
	if request.ContentLength > objectiveBodyLimit {
		http.Error(response, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	reader := io.LimitReader(request.Body, objectiveBodyLimit+1)
	payload, err := io.ReadAll(reader)
	if err != nil || len(payload) > objectiveBodyLimit {
		http.Error(response, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	var rpc struct {
		ID     any            `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if json.Unmarshal(payload, &rpc) != nil {
		http.Error(response, "invalid JSON", http.StatusBadRequest)
		return
	}
	switch rpc.Method {
	case "initialize":
		writeRPCResult(response, rpc.ID, map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "pixie-objectives", "version": "1"}})
	case "notifications/initialized":
		response.WriteHeader(http.StatusAccepted)
	case "tools/list":
		writeRPCResult(response, rpc.ID, map[string]any{"tools": h.tools()})
	case "tools/call":
		name := textValue(rpc.Params["name"])
		arguments := mapValue(rpc.Params["arguments"])
		if name == "schedule_manage" && h.Schedules != nil {
			action := textValue(arguments["action"])
			if !contains([]string{"list", "create", "update", "delete", "runNow", "stop"}, action) {
				writeRPCError(response, rpc.ID, "Unknown schedule action")
				return
			}
			arguments["projectId"] = projectID
			if action == "create" {
				root, err := h.Sessions.RecordedCWD(projectID, sessionID)
				if err != nil {
					writeRPCError(response, rpc.ID, err.Error())
					return
				}
				arguments["root"] = root
			}
			result, err := h.Schedules.Handle(request.Context(), "schedule."+action, arguments)
			if err != nil {
				writeRPCError(response, rpc.ID, err.Error())
				return
			}
			writeToolResult(response, rpc.ID, result)
			return
		}
		if name == "ask_user_question" {
			result, err := h.Sessions.AskQuestion(request.Context(), sessionID, arguments)
			if err != nil {
				writeRPCError(response, rpc.ID, err.Error())
				return
			}
			writeToolResult(response, rpc.ID, result)
			return
		}
		var state SessionGoal
		var operationErr error
		switch name {
		case "objective_get":
			state, operationErr = h.Sessions.Objective(request.Context(), projectID, sessionID)
		case "objective_update":
			if len(arguments) == 0 || hasUnknownKeys(arguments, "goal", "tasks") {
				writeRPCError(response, rpc.ID, "Invalid objective update arguments")
				return
			}
			var goal *string
			if raw, exists := arguments["goal"]; exists {
				value, ok := raw.(string)
				if !ok {
					writeRPCError(response, rpc.ID, "Invalid objective update arguments")
					return
				}
				goal = &value
			}
			var tasks *[]SessionTask
			if raw, exists := arguments["tasks"]; exists {
				if _, ok := raw.([]any); !ok {
					writeRPCError(response, rpc.ID, "Invalid objective update arguments")
					return
				}
				encoded, _ := json.Marshal(raw)
				var value []SessionTask
				if json.Unmarshal(encoded, &value) != nil {
					writeRPCError(response, rpc.ID, "Invalid objective update arguments")
					return
				}
				tasks = &value
			}
			state, operationErr = h.Sessions.UpdateObjectiveFromAgent(request.Context(), projectID, sessionID, goal, tasks)
		default:
			writeRPCError(response, rpc.ID, "Unknown objective tool or invalid arguments")
			return
		}
		if operationErr != nil {
			writeRPCError(response, rpc.ID, operationErr.Error())
			return
		}
		writeToolResult(response, rpc.ID, state)
	default:
		writeRPCError(response, rpc.ID, "Unknown MCP method")
	}
}

func objectiveTools() []map[string]any {
	text := map[string]any{"type": "string", "minLength": 1, "maxLength": 2_000, "pattern": `^[^\u0000]*[^\s\u0000][^\u0000]*$`}
	option := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"label":             map[string]any{"type": "string", "minLength": 1, "maxLength": 500},
			"description":       map[string]any{"type": "string", "maxLength": 2_000},
			"preview":           map[string]any{"type": "string", "maxLength": 8_000},
			"recommendedReason": map[string]any{"type": "string", "maxLength": 2_000},
		},
		"required":             []string{"label", "description"},
		"additionalProperties": false,
	}
	question := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question":    text,
			"header":      map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
			"options":     map[string]any{"type": "array", "minItems": 1, "maxItems": 12, "items": option},
			"multiSelect": map[string]any{"type": "boolean"},
		},
		"required":             []string{"question", "header", "options"},
		"additionalProperties": false,
	}
	return []map[string]any{
		{
			"name": "objective_get", "description": "Get this session's objective and tasks.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		},
		{
			"name": "objective_update", "description": "Atomically update this session's objective and/or tasks.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"goal": text,
					"tasks": map[string]any{
						"type": "array", "maxItems": 200,
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"id":     map[string]any{"type": "string", "minLength": 1, "maxLength": 256},
								"text":   text,
								"status": map[string]any{"enum": []string{"pending", "active", "done"}},
							},
							"required": []string{"id", "text", "status"},
						},
					},
				},
				"minProperties": 1, "additionalProperties": false,
			},
		},
		{
			"name": "ask_user_question", "description": "Pause and ask the user one or more supporting questions before continuing.",
			"inputSchema": map[string]any{
				"type": "object", "properties": map[string]any{"questions": map[string]any{"type": "array", "minItems": 1, "maxItems": 8, "items": question}},
				"required": []string{"questions"}, "additionalProperties": false,
			},
		},
	}
}

func writeRPCResult(response http.ResponseWriter, id, result any) {
	writeJSONResponse(response, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func writeRPCError(response http.ResponseWriter, id any, message string) {
	writeJSONResponse(response, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32602, "message": message}})
}

func writeToolResult(response http.ResponseWriter, id, result any) {
	encoded, _ := json.Marshal(result)
	writeRPCResult(response, id, map[string]any{"content": []map[string]any{{"type": "text", "text": string(encoded)}}, "structuredContent": result})
}

func writeJSONResponse(response http.ResponseWriter, value any) {
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(value); err != nil {
		http.Error(response, fmt.Sprintf("encode response: %v", err), http.StatusInternalServerError)
	}
}

func hasUnknownKeys(value map[string]any, allowed ...string) bool {
	known := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		known[key] = true
	}
	for key := range value {
		if !known[key] {
			return true
		}
	}
	return false
}

func (h ObjectiveHandler) tools() []map[string]any {
	tools := objectiveTools()
	if h.Schedules == nil {
		return tools
	}
	return append(tools, map[string]any{"name": "schedule_manage", "description": "Manage Pixie-owned schedules in this project. Each run starts a separate Pi session. Cron has five fields; timezone defaults to UTC. Actions: list, create, update, delete, runNow, stop. Update paused to pause or resume.", "inputSchema": map[string]any{"type": "object", "required": []string{"action"}, "properties": map[string]any{"action": map[string]any{"enum": []string{"list", "create", "update", "delete", "runNow", "stop"}}, "scheduleId": map[string]any{"type": "string"}, "prompt": map[string]any{"type": "string"}, "cron": map[string]any{"type": "string"}, "timezone": map[string]any{"type": "string"}, "paused": map[string]any{"type": "boolean"}, "model": map[string]any{"type": "object", "required": []string{"provider", "id"}, "properties": map[string]any{"provider": map[string]any{"type": "string"}, "id": map[string]any{"type": "string"}}}}, "additionalProperties": false}})
}
