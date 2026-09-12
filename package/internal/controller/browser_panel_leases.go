package controller

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

// Leases cover a lost controller; the ownership journal still handles startup
// cleanup and older Browser services. Never renew orphaned or closing panels.
func (p *BrowserPanels) keepPanelLeases() {
	p.renewPanelLeases()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-p.cleanupCtx.Done():
			return
		case <-ticker.C:
			p.renewPanelLeases()
		}
	}
}

func (p *BrowserPanels) renewPanelLeases() {
	p.mu.Lock()
	ids := make([]string, 0, len(p.panels))
	if !p.draining {
		for id, panel := range p.panels {
			if !panel.orphan && !panel.closing && panel.retry == nil {
				ids = append(ids, id)
			}
		}
	}
	p.mu.Unlock()
	if len(ids) == 0 {
		return
	}
	// Deterministic renewal order keeps the lease payload stable across
	// controllers and avoids map-iteration flakiness in regressions.
	sort.Strings(ids)
	body, err := json.Marshal(map[string]any{"sessions": ids})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(p.cleanupCtx, browserPanelTimeout)
	defer cancel()
	_, _, _ = p.browserCall(ctx, "/v1/browser/leases", body, false)
}
