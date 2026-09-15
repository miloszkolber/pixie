package diagnostics

import (
	"time"
)

const (
	// MaxHealthTransitions bounds the transition history the support-export
	// boundary will carry. The oldest transition is dropped first.
	MaxHealthTransitions = 32
)

// HealthTransition is one bounded, secret-free component state change exported
// through the diagnostics support snapshot. It is a transport shape only: the
// controller owns the transition ring and maps its own records into this type.
type HealthTransition struct {
	At        string `json:"at"`
	Component string `json:"component"`
	From      string `json:"from,omitempty"`
	To        string `json:"to"`
}

var healthComponentAllowlist = map[string]struct{}{
	"agent":       {},
	"host":        {},
	"application": {},
	"schedule":    {},
	"supervisor":  {},
}

var healthStateAllowlist = map[string]struct{}{
	"unknown":      {},
	"unconfigured": {},
	"unreachable":  {},
	"unavailable":  {},
	"incompatible": {},
	"degraded":     {},
	"ready":        {},
	"healthy":      {},
}

// SanitizeHealthTransitions bounds and validates an externally supplied
// transition history. Entries with an unknown component, unknown state, a
// malformed timestamp or free-text reason are dropped, never rewritten into a
// claim the controller did not make.
func SanitizeHealthTransitions(values []HealthTransition) []HealthTransition {
	if len(values) == 0 {
		return nil
	}
	if len(values) > MaxHealthTransitions {
		values = values[len(values)-MaxHealthTransitions:]
	}
	result := make([]HealthTransition, 0, len(values))
	for _, value := range values {
		if _, ok := healthComponentAllowlist[value.Component]; !ok {
			continue
		}
		if _, ok := healthStateAllowlist[value.To]; !ok {
			continue
		}
		from := value.From
		if from != "" {
			if _, ok := healthStateAllowlist[from]; !ok {
				continue
			}
		}
		parsed, err := time.Parse(time.RFC3339, value.At)
		if err != nil {
			continue
		}
		result = append(result, HealthTransition{
			At:        parsed.UTC().Format(time.RFC3339),
			Component: value.Component,
			From:      from,
			To:        value.To,
		})
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
