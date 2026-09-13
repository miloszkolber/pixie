package controller

import (
	"testing"
	"time"
)

func TestClampThinkingRecognizesNativeMaximumOrdering(t *testing.T) {
	manager := &SessionManager{
		now: time.Now,
		sessions: map[string]*sessionEntry{
			"session": {
				configOptions: []any{map[string]any{
					"id":           "thinking",
					"currentValue": "xhigh",
					"options": []any{
						map[string]any{"value": "off"},
						map[string]any{"value": "xhigh"},
					},
				}},
			},
		},
	}

	level, err := manager.ClampThinking("session", "max")
	if err != nil {
		t.Fatal(err)
	}
	if level != "xhigh" {
		t.Fatalf("clamped maximum thinking level = %q, want xhigh", level)
	}
}

func TestClampThinkingPreservesUnknownNativeLevel(t *testing.T) {
	manager := &SessionManager{
		now: time.Now,
		sessions: map[string]*sessionEntry{
			"session": {
				configOptions: []any{map[string]any{
					"id":           "thinking",
					"currentValue": "native-extra",
					"options": []any{
						map[string]any{"value": "off"},
						map[string]any{"value": "native-extra"},
					},
				}},
			},
		},
	}

	level, err := manager.ClampThinking("session", "max")
	if err != nil {
		t.Fatal(err)
	}
	if level != "native-extra" {
		t.Fatalf("clamped unknown thinking level = %q, want native-extra", level)
	}
}
