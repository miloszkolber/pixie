package controller

import (
	"context"
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

// Process-local partial-create scratch must not survive a controlled shutdown;
// otherwise a long-lived controller would accumulate claims for sessions whose
// creation never committed.
func TestSessionManagerShutdownDropsPartialCreateScratch(t *testing.T) {
	manager := NewSessionManager(nil, nil, nil, nil, nil, nil)
	manager.scheduleCreationRoots["partial-native-session"] = "/project"
	manager.shutdown(context.Background())
	if len(manager.scheduleCreationRoots) != 0 {
		t.Fatalf("partial-create scratch survived shutdown: %#v", manager.scheduleCreationRoots)
	}
}
