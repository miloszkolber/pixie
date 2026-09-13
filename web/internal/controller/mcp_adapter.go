package controller

import (
	"context"
	"encoding/json"
)

// AdapterStatus projects the pi-mcp-adapter bridge state (`adapter.status`
// host capability) into the Tools UI. It stays fail-open: when Pi is
// unreachable or the `pi-mcp-adapter` profile is not enabled, the UI shows
// the adapter as unavailable instead of failing the settings load.
func (a *PiAdmin) AdapterStatus(ctx context.Context) map[string]any {
	if a == nil || a.client == nil {
		return map[string]any{"available": false}
	}
	raw, err := a.client.CallPi(ctx, "adapter.status", map[string]any{})
	if err != nil || len(raw) == 0 || string(raw) == "null" {
		return map[string]any{"available": false}
	}
	var status map[string]any
	if err := json.Unmarshal(raw, &status); err != nil {
		return map[string]any{"available": false}
	}
	status["available"] = true
	return status
}
