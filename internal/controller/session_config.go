package controller

import (
	"context"
	"fmt"
)

// Selectors retain agent-provided IDs and ordering. Categories describe UI
// meaning; neither a Pi-specific ID nor a recognized category is required.
func projectConfigOptions(options []any) []any {
	result := make([]any, 0, len(options))
	for _, raw := range options {
		option := mapValue(raw)
		if option["type"] != "select" || textValue(option["id"]) == "" {
			continue
		}
		choices := []any{}
		for _, rawChoice := range arrayValue(option["options"]) {
			choice := mapValue(rawChoice)
			group := arrayValue(choice["options"])
			if len(group) == 0 {
				group = []any{choice}
			}
			for _, item := range group {
				value := mapValue(item)
				if textValue(value["value"]) != "" {
					choices = append(choices, map[string]any{"value": value["value"], "name": textValue(value["name"])})
				}
			}
		}
		result = append(result, map[string]any{"id": option["id"], "name": textValue(option["name"]), "type": "select", "category": textValue(option["category"]), "currentValue": textValue(option["currentValue"]), "options": choices})
	}
	return result
}

func (m *SessionManager) SetConfigOption(ctx context.Context, sessionID, configID, value string) error {
	entry, err := m.entry(sessionID)
	if err != nil {
		return err
	}
	defer m.releaseEntry(entry)
	if err := m.lockEntryContext(ctx, sessionID, entry); err != nil {
		return err
	}
	defer entry.op.Unlock()
	if err := m.attachLocked(ctx, sessionID, entry); err != nil {
		return err
	}
	entry.state.Lock()
	options := projectConfigOptions(entry.configOptions)
	entry.state.Unlock()
	allowed := false
	for _, raw := range options {
		option := mapValue(raw)
		if option["id"] != configID {
			continue
		}
		for _, rawChoice := range arrayValue(option["options"]) {
			if mapValue(rawChoice)["value"] == value {
				allowed = true
			}
		}
	}
	if !allowed {
		return fmt.Errorf("configuration option changed; refresh the chat before selecting a value")
	}
	updated, err := m.setConfig(entry.context(ctx), sessionID, configID, value)
	if err != nil {
		return err
	}
	entry.state.Lock()
	entry.configOptions = updated
	entry.thinkingLevel = thinkingFromOptions(updated)
	entry.model = modelFromSetup(updated, nil)
	model := entry.model
	entry.state.Unlock()
	m.emitSessionConfig(sessionID, model, updated)
	return nil
}
