package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const questionTimeout = 30 * time.Minute

type questionKey struct{ sessionID, toolCallID string }

func (m *SessionManager) AskQuestion(ctx context.Context, sessionID string, value any) (map[string]any, error) {
	args, err := validateQuestionArgs(value)
	if err != nil {
		return nil, err
	}
	entry, err := m.entry(sessionID)
	if err != nil {
		return nil, err
	}
	defer m.releaseEntry(entry)
	var toolCallID string
	var promptDone <-chan struct{}
	registration, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	for {
		entry.state.Lock()
		toolCallID = latestQuestionToolCall(entry, args)
		if entry.promptActive {
			promptDone = entry.promptDone
		}
		if toolCallID != "" {
			entry.consumedQuestions[toolCallID] = true
		}
		if entry.toolChanged == nil {
			entry.toolChanged = make(chan struct{})
		}
		changed := entry.toolChanged
		entry.state.Unlock()
		if toolCallID != "" {
			break
		}
		select {
		case <-registration.Done():
			return nil, fmt.Errorf("no matching active question: %w", registration.Err())
		case <-changed:
		}
	}
	if toolCallID == "" {
		return nil, fmt.Errorf("no matching ask_user_question tool call is active")
	}
	pending := &pendingQuestion{sessionID: sessionID, args: args, result: make(chan map[string]any, 1)}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return map[string]any{"answers": []any{}, "cancelled": true}, nil
	}
	key := questionKey{sessionID, toolCallID}
	m.questions[key] = pending
	m.mu.Unlock()
	timer := time.NewTimer(questionTimeout)
	defer timer.Stop()
	var result map[string]any
	select {
	case result = <-pending.result:
	case <-promptDone:
		result = map[string]any{"answers": []any{}, "cancelled": true}
	case <-ctx.Done():
		result = map[string]any{"answers": []any{}, "cancelled": true}
	case <-timer.C:
		result = map[string]any{"answers": []any{}, "cancelled": true}
	}
	m.mu.Lock()
	if m.questions[key] == pending {
		delete(m.questions, key)
	}
	m.mu.Unlock()
	return result, nil
}

func (m *SessionManager) ResolveQuestion(sessionID, toolCallID string, result map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := questionKey{sessionID, toolCallID}
	pending := m.questions[key]
	if pending == nil || pending.sessionID != sessionID {
		return fmt.Errorf("question is no longer awaiting input")
	}
	if err := validateQuestionResult(result, pending.args); err != nil {
		return err
	}
	select {
	case pending.result <- result:
		delete(m.questions, key)
		return nil
	default:
		return fmt.Errorf("question is no longer awaiting input")
	}
}

func validateQuestionArgs(value any) (map[string]any, error) {
	args, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("question arguments must be an object")
	}
	questions := arrayValue(args["questions"])
	if len(questions) < 1 || len(questions) > 8 {
		return nil, fmt.Errorf("ask between 1 and 8 questions")
	}
	for _, rawQuestion := range questions {
		question := mapValue(rawQuestion)
		prompt, header := strings.TrimSpace(textValue(question["question"])), strings.TrimSpace(textValue(question["header"]))
		options := arrayValue(question["options"])
		if prompt == "" || utf16Length(prompt) > 2_000 || header == "" || utf16Length(header) > 200 || len(options) < 1 || len(options) > 12 {
			return nil, fmt.Errorf("question text, header, or options are invalid")
		}
		labels := make(map[string]bool)
		for _, rawOption := range options {
			option := mapValue(rawOption)
			label := strings.TrimSpace(textValue(option["label"]))
			if label == "" || labels[label] {
				return nil, fmt.Errorf("question options are invalid")
			}
			labels[label] = true
			if _, ok := option["description"].(string); !ok {
				return nil, fmt.Errorf("question options are invalid")
			}
		}
	}
	return args, nil
}

func latestQuestionToolCall(entry *sessionEntry, args map[string]any) string {
	wanted := stableJSON(args)
	for messageIndex := len(entry.messages) - 1; messageIndex >= 0; messageIndex-- {
		message := mapValue(entry.messages[messageIndex])
		if message["role"] == "user" {
			break
		}
		if message["role"] != "assistant" {
			continue
		}
		content := arrayValue(message["content"])
		for blockIndex := len(content) - 1; blockIndex >= 0; blockIndex-- {
			block := mapValue(content[blockIndex])
			id := textValue(block["id"])
			if _, active := entry.pendingToolOutputs[id]; active && block["type"] == "toolCall" && block["name"] == "ask_user_question" && !entry.consumedQuestions[id] && stableJSON(block["arguments"]) == wanted {
				return id
			}
		}
	}
	return ""
}

func validateQuestionResult(result, args map[string]any) error {
	answers, answersOK := result["answers"].([]any)
	_, cancelledOK := result["cancelled"].(bool)
	questions := arrayValue(args["questions"])
	if !answersOK || !cancelledOK || len(answers) > len(questions) || (result["cancelled"] == false && len(answers) != len(questions)) {
		return fmt.Errorf("malformed question response")
	}
	seen := make(map[int]bool)
	for _, rawAnswer := range answers {
		answer := mapValue(rawAnswer)
		indexValue, ok := numeric(answer["questionIndex"])
		index := int(indexValue)
		if !ok || int64(index) != indexValue || index < 0 || index >= len(questions) || seen[index] {
			return fmt.Errorf("malformed question response")
		}
		question := mapValue(questions[index])
		kind := textValue(answer["kind"])
		if textValue(answer["question"]) != textValue(question["question"]) || (kind != "option" && kind != "custom" && kind != "multi") {
			return fmt.Errorf("malformed question response")
		}
		if _, exists := answer["answer"]; !exists {
			return fmt.Errorf("malformed question response")
		}
		if value := answer["answer"]; value != nil {
			text, ok := value.(string)
			if !ok || utf16Length(text) > 8_000 {
				return fmt.Errorf("malformed question response")
			}
		}
		if value, exists := answer["selected"]; exists {
			selected, ok := value.([]any)
			if !ok || len(selected) > 12 {
				return fmt.Errorf("malformed question response")
			}
			for _, label := range selected {
				text, ok := label.(string)
				if !ok || utf16Length(text) > 500 {
					return fmt.Errorf("malformed question response")
				}
			}
		}
		for _, key := range []string{"notes", "preview"} {
			if value, exists := answer[key]; exists {
				text, ok := value.(string)
				if !ok || utf16Length(text) > 8_000 {
					return fmt.Errorf("malformed question response")
				}
			}
		}
		labels := make(map[string]map[string]any)
		for _, rawOption := range arrayValue(question["options"]) {
			option := mapValue(rawOption)
			labels[textValue(option["label"])] = option
		}
		if kind == "option" {
			selected := labels[textValue(answer["answer"])]
			if selected == nil || answer["preview"] != selected["preview"] {
				return fmt.Errorf("malformed question response")
			}
		} else if _, exists := answer["preview"]; exists {
			return fmt.Errorf("malformed question response")
		}
		if kind == "multi" {
			if question["multiSelect"] != true {
				return fmt.Errorf("question does not allow multiple selections")
			}
			selected, ok := answer["selected"].([]any)
			if !ok || len(selected) > 12 {
				return fmt.Errorf("malformed question response")
			}
			selectedLabels := make(map[string]bool)
			for _, label := range selected {
				text, ok := label.(string)
				if !ok || labels[text] == nil || selectedLabels[text] {
					return fmt.Errorf("malformed question response")
				}
				selectedLabels[text] = true
			}
		}
		seen[index] = true
	}
	return nil
}

func stableJSON(value any) string {
	// encoding/json sorts string map keys, including nested objects.
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func (m *SessionManager) cancelQuestions(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, pending := range m.questions {
		if sessionID == "" || key.sessionID == sessionID {
			select {
			case pending.result <- map[string]any{"answers": []any{}, "cancelled": true}:
			default:
			}
			delete(m.questions, key)
		}
	}
}
