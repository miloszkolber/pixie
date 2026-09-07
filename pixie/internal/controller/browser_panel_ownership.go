package controller

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/miloszkolber/pixie/internal/persist"
)

type browserPanelOwnership struct {
	Version int      `json:"version"`
	IDs     []string `json:"ids"`
}

// NewPersistentBrowserPanels records only controller-owned session IDs. Each
// endpoint has its own journal; changing credentials does not change ownership.
// Loading does not contact the browser service. Runtime.Start resumes cleanup.
func NewPersistentBrowserPanels(auth AuthConfig, client *http.Client, store persist.Store) (*BrowserPanels, error) {
	p := NewBrowserPanels(auth, client)
	p.store = &store
	p.journalName = fmt.Sprintf("browser-panels-%x.json", sha256.Sum256([]byte(strings.TrimRight(auth.BrowserURL, "/"))))
	path := filepath.Join(store.Dir, p.journalName)
	raw, _, err := persist.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// A missing primary with a surviving backup is not a new installation.
		// Restoring an older ownership set could silently lose recent sessions.
		if _, backupErr := os.Stat(path + ".bak"); errors.Is(backupErr, os.ErrNotExist) {
			return p, nil
		}
	}
	var ownership browserPanelOwnership
	if err == nil {
		err = persist.Decode(raw, &ownership, validateBrowserPanelOwnership)
	}
	if err != nil {
		p.cleanupCancel()
		return nil, fmt.Errorf("load browser panel ownership: %w", err)
	}
	for _, id := range ownership.IDs {
		p.panels[id] = browserPanel{id: id, orphan: true}
	}
	return p, nil
}

func validateBrowserPanelOwnership(value browserPanelOwnership) error {
	if value.Version != 1 || len(value.IDs) > maxBrowserPanels {
		return fmt.Errorf("invalid browser panel ownership journal")
	}
	seen := make(map[string]bool, len(value.IDs))
	for _, id := range value.IDs {
		if !browserPanelIDPattern.MatchString(id) || seen[id] {
			return fmt.Errorf("invalid owned browser session ID")
		}
		seen[id] = true
	}
	return nil
}

// ResumeCleanup renews live panel leases and closes only sessions journaled
// by this controller at this endpoint. Failed closes use bounded retries.
func (p *BrowserPanels) ResumeCleanup() {
	p.leaseOnce.Do(func() { go p.keepPanelLeases() })
	go p.closeMatching(p.cleanupCtx, func(panel browserPanel) bool { return panel.orphan })
}

// Caller holds p.mu. Write ownership before exposing a new panel to the client.
func (p *BrowserPanels) saveOwnership() error {
	if p.store == nil {
		return nil
	}
	ids := make([]string, 0, len(p.panels))
	for id := range p.panels {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if err := persist.Write(*p.store, p.journalName, browserPanelOwnership{Version: 1, IDs: ids}, validateBrowserPanelOwnership); err != nil {
		return fmt.Errorf("save browser panel ownership: %w", err)
	}
	return nil
}
