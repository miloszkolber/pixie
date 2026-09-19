package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxSlashCommands          = 128
	maxSlashCommandCandidates = 512
	maxSlashCommandBytes      = 64 * 1024
	maxSlashCommandNameBytes  = 256
)

func (a *PiAdmin) completions(ctx context.Context, method string, request map[string]any) (any, error) {
	if a.sessions == nil {
		return nil, fmt.Errorf("sessions are not configured")
	}
	params := map[string]any{}
	if method == "skill.list" {
		project, err := a.sessions.projects.Get(textValue(request["projectId"]))
		if err != nil {
			return nil, err
		}
		cwd, err := project.Root()
		if err != nil {
			return nil, err
		}
		params["cwd"] = cwd
	} else {
		sessionID, err := requiredIdentifier(request["sessionId"], "Session identifier")
		if err != nil {
			return nil, err
		}
		var entry *sessionEntry
		if method == "session.getAgentMentions" {
			projectID := textValue(request["projectId"])
			cwd, err := a.sessions.RecordedCWD(projectID, sessionID)
			if err != nil {
				return nil, err
			}
			entry, err = a.sessions.EnsureAttached(ctx, sessionID, projectID, cwd)
			if err != nil {
				return nil, err
			}
		} else {
			entry, err = a.sessions.entry(sessionID)
			if err != nil {
				return nil, err
			}
		}
		defer a.sessions.releaseEntry(entry)
		if err := a.sessions.lockEntryContext(ctx, sessionID, entry); err != nil {
			return nil, err
		}
		defer entry.op.Unlock()
		if _, err := a.sessions.projects.AssertCWD(entry.projectID, entry.cwd); err != nil {
			return nil, err
		}
		if err := a.sessions.attachLocked(ctx, sessionID, entry); err != nil {
			return nil, err
		}
		ctx = entry.context(ctx)
		params["sessionId"] = sessionID
		if method == "session.getAgentMentions" {
			params["cwd"] = entry.cwd
			response, err := a.objectCall(ctx, "pi.agent-mentions.list", params)
			if err != nil {
				return nil, fmt.Errorf("couldn't load agent mentions")
			}
			return projectAgentMentions(response["agents"])
		}
	}
	response, err := a.objectCall(ctx, "pi.slash-commands.list", params)
	if err != nil {
		return nil, err
	}
	return projectSlashCommands(response["availableCommands"]), nil
}

func projectSlashCommands(value any) []map[string]any {
	return projectSlashCommandsFrom(value, "pi", "Pi")
}

func projectAgentSlashCommands(value any, pi bool) []map[string]any {
	if pi {
		return projectSlashCommands(value)
	}
	return projectSlashCommandsFrom(value, "agent", "Connected agent")
}

func projectSlashCommandsFrom(value any, source, label string) []map[string]any {
	result := make([]map[string]any, 0, min(len(arrayValue(value)), maxSlashCommands))
	optional := make([]map[string]string, 0, cap(result))
	total := 2 // JSON array delimiters.
	for index, value := range arrayValue(value) {
		if index == maxSlashCommandCandidates {
			break
		}
		if len(result) == maxSlashCommands {
			break
		}
		command := mapValue(value)
		name := textValue(command["name"])
		if !validSlashCommandName(name) {
			continue
		}
		entry := map[string]any{
			"name":   name,
			"source": source,
			"sourceInfo": map[string]any{
				"path": name, "source": label, "scope": "temporary", "origin": "top-level",
			},
		}
		encoded, err := json.Marshal(entry)
		separator := 0
		if len(result) > 0 {
			separator = 1
		}
		if err != nil || total+separator+len(encoded) > maxSlashCommandBytes {
			continue
		}
		result = append(result, entry)
		optional = append(optional, map[string]string{
			"description": safeCommandText(command["description"], 2048),
			"inputHint":   safeCommandText(mapValue(command["input"])["hint"], 512),
		})
		total += separator + len(encoded)
	}
	for index, fields := range optional {
		for _, target := range []string{"description", "inputHint"} {
			text := fields[target]
			if text == "" {
				continue
			}
			before, err := json.Marshal(result[index])
			if err != nil {
				continue
			}
			result[index][target] = text
			after, err := json.Marshal(result[index])
			if err != nil || total+len(after)-len(before) > maxSlashCommandBytes {
				delete(result[index], target)
				continue
			}
			total += len(after) - len(before)
		}
	}
	return result
}

func validSlashCommandName(value string) bool {
	return value != "" &&
		len(value) <= maxSlashCommandNameBytes &&
		utf8.ValidString(value) &&
		!strings.ContainsRune(value, 0) &&
		strings.IndexFunc(value, func(character rune) bool {
			return unicode.IsSpace(character) || unicode.IsControl(character) || unicode.Is(unicode.Cf, character)
		}) < 0
}

func safeCommandText(value any, limit int) string {
	text, ok := value.(string)
	if !ok || text == "" || len(text) > limit || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return ""
	}
	return text
}

func projectAgentMentions(value any) ([]map[string]any, error) {
	mentions, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("Pi response is missing agents")
	}
	result := make([]map[string]any, 0)
	total := 0
	for _, value := range mentions {
		raw := mapValue(value)
		entry := map[string]any{}
		size, bounded := 0, true
		for _, field := range []struct {
			key   string
			limit int
		}{{"name", 256}, {"description", 2048}, {"sourceType", 32}, {"mention", 512}} {
			text, ok := raw[field.key].(string)
			if !ok {
				return nil, fmt.Errorf("Pi agent mention is missing %s", field.key)
			}
			entry[field.key] = text
			size += len(text)
			bounded = bounded && len(text) <= field.limit
		}
		if !contains([]string{"skill", "builtinSkill", "agent", "project"}, textValue(entry["sourceType"])) {
			return nil, fmt.Errorf("Pi agent mention has an unsupported source type")
		}
		if path := raw["sourcePath"]; path != nil {
			if _, ok := path.(string); !ok {
				return nil, fmt.Errorf("Pi agent mention has an invalid source path")
			}
		}
		if bounded && total+size <= 32*1024 && len(result) < 64 {
			result = append(result, entry)
			total += size
		}
	}
	return result, nil
}
