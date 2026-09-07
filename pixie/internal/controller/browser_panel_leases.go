package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
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
	body, err := json.Marshal(map[string]any{"sessions": ids})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(p.cleanupCtx, browserPanelTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.auth.BrowserURL+"/v1/browser/leases", bytes.NewReader(body))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	if auth, token := p.auth.BrowserServiceAuth(); auth {
		if !strongToken(token) {
			return
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := p.client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxBrowserPanelBody))
}
