package controller

import (
	"fmt"
)

type questionKey struct{ sessionID, toolCallID string }

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
