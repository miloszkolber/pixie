package controller

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
)

type piAgentSource struct {
	path, name, description, content string
	global, writable                 bool
	properties                       map[string]any
}

func (a *PiAdmin) handleAgents(ctx context.Context, method string, request map[string]any) (any, error) {
	projectDir := ""
	_, hasProject := request["projectId"]
	_, hasRoot := request["root"]
	if hasProject || hasRoot {
		if !hasProject || !hasRoot || a.sessions == nil {
			return nil, fmt.Errorf("agent project and root must be selected together")
		}
		var err error
		projectDir, err = a.sessions.projects.AssertRoot(textValue(request["projectId"]), textValue(request["root"]))
		if err != nil {
			return nil, err
		}
	}
	if method == "pi.agentList" {
		sources, err := a.agentSources(ctx, projectDir)
		if err != nil {
			return nil, err
		}
		entries := make([]map[string]any, 0)
		for _, source := range sources {
			if entry := source.project(); entry != nil {
				entries = append(entries, entry)
			}
			if len(entries) == 100 {
				break
			}
		}
		return entries, nil
	}
	a.agentMu.Lock()
	defer a.agentMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	params := map[string]any{"type": "agent", "projectDir": projectDir}
	var source piAgentSource
	if method != "pi.agentCreate" {
		id, err := agentText(request["id"], "identifier", 128, false)
		if err != nil {
			return nil, err
		}
		sources, err := a.agentSources(ctx, projectDir)
		if err != nil {
			return nil, err
		}
		for _, candidate := range sources {
			if candidate.id() == id && candidate.properties["kind"] != "check" {
				source = candidate
				break
			}
		}
		if !source.writable {
			return nil, fmt.Errorf("agent is unavailable or read-only")
		}
		params["path"] = source.path
	}
	if method == "pi.agentDelete" {
		if err := a.call(ctx, "pi.sources.delete", params, nil); err != nil {
			return nil, fmt.Errorf("couldn't remove Pi agent")
		}
		return ack(nil)
	}
	name, err := agentName(request["name"])
	if err != nil {
		return nil, err
	}
	description, err := agentText(request["description"], "description", 1000, true)
	if err != nil {
		return nil, err
	}
	content, err := agentText(request["instructions"], "instructions", 64*1024, true)
	if err != nil {
		return nil, err
	}
	params["name"], params["description"], params["content"] = name, description, content
	model := ""
	if request["modelId"] != nil {
		model, err = agentText(request["modelId"], "model", 256, false)
		if err != nil {
			return nil, err
		}
	}
	action := "update"
	if method == "pi.agentCreate" {
		scope := textValue(request["scope"])
		if scope != "global" && scope != "project" || (scope == "project") != (projectDir != "") {
			return nil, fmt.Errorf("invalid agent scope")
		}
		target := map[string]any{"scope": "global"}
		if projectDir != "" {
			target["scope"], target["projectDir"] = "projectDir", projectDir
		}
		params["target"] = target
		if model != "" {
			params["properties"] = map[string]any{"model": model}
		}
		action = "create"
	} else if _, changed := request["modelId"]; changed {
		properties := source.properties
		delete(properties, "model")
		if model != "" {
			properties["model"] = model
		}
		params["properties"] = properties
	}
	response, err := a.objectCall(ctx, "pi.sources."+action, params)
	if err != nil {
		return nil, fmt.Errorf("couldn't save Pi agent")
	}
	updated, err := parseAgentSource(response["source"])
	if err != nil {
		return nil, err
	}
	entry := updated.project()
	if entry == nil {
		return nil, fmt.Errorf("Pi returned an invalid agent")
	}
	return entry, nil
}

func (a *PiAdmin) agentSources(ctx context.Context, projectDir string) ([]piAgentSource, error) {
	params := map[string]any{"type": "agent", "includeProjectSources": false}
	if projectDir != "" {
		params["projectDir"] = projectDir
	}
	response, err := a.objectCall(ctx, "pi.sources.list", params)
	if err != nil {
		return nil, fmt.Errorf("couldn't load Pi agents")
	}
	result := make([]piAgentSource, 0)
	for _, value := range arrayValue(response["sources"]) {
		source, err := parseAgentSource(value)
		if err != nil {
			return nil, err
		}
		result = append(result, source)
	}
	return result, nil
}

func parseAgentSource(value any) (piAgentSource, error) {
	raw := mapValue(value)
	global, globalOK := raw["global"].(bool)
	writable, writableOK := raw["writable"].(bool)
	name, nameOK := raw["name"].(string)
	description, descriptionOK := raw["description"].(string)
	content, contentOK := raw["content"].(string)
	path, pathOK := raw["path"].(string)
	properties := map[string]any{}
	propertiesOK := true
	if raw["properties"] != nil {
		properties, propertiesOK = raw["properties"].(map[string]any)
	}
	if raw["type"] != "agent" || !globalOK || !writableOK || !nameOK || name == "" || !descriptionOK || !contentOK || !pathOK || path == "" || !propertiesOK {
		return piAgentSource{}, fmt.Errorf("Pi returned an invalid agent source")
	}
	return piAgentSource{path: path, name: name, description: description, content: content, global: global, writable: writable, properties: properties}, nil
}

func (s piAgentSource) id() string {
	scope := "project"
	if s.global {
		scope = "global"
	}
	digest := sha256.Sum256([]byte("agent\x00" + scope + "\x00" + s.path))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func (s piAgentSource) project() map[string]any {
	if s.properties["kind"] == "check" {
		return nil
	}
	name, err := agentName(s.name)
	if err != nil {
		return nil
	}
	description, err := agentText(s.description, "description", 1000, true)
	if err != nil {
		return nil
	}
	content, err := agentText(s.content, "instructions", 64*1024, true)
	if err != nil {
		return nil
	}
	scope := "project"
	if s.global {
		scope = "global"
	}
	result := map[string]any{"id": s.id(), "name": name, "description": description, "instructions": content, "scope": scope, "writable": s.writable}
	if model, err := agentText(s.properties["model"], "model", 256, false); err == nil {
		result["modelId"] = model
	}
	return result
}

func agentText(value any, label string, maxBytes int, allowEmpty bool) (string, error) {
	text, ok := value.(string)
	text = strings.TrimSpace(text)
	if !ok || !allowEmpty && text == "" || containsNUL(text) || len(text) > maxBytes {
		return "", fmt.Errorf("agent %s is invalid", label)
	}
	return text, nil
}

func agentName(value any) (string, error) {
	name, err := agentText(value, "name", 80, false)
	if err == nil && strings.ContainsAny(name, "/\\") {
		err = fmt.Errorf("agent name is invalid")
	}
	return name, err
}
