package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/miloszkolber/pixie/internal/identifier"
	"github.com/miloszkolber/pixie/internal/persist"
)

const (
	maxBrowserPanels        = 16
	browserPanelTimeout     = 30 * time.Second
	maxBrowserPanelBody     = 512 * 1024
	maxBrowserPanelURLBytes = 2048
	maxBrowserPanelText     = 8 * 1024
	maxBrowserPanelRequest  = 16 * 1024
	browserCloseWorkers     = 4
)

var (
	browserPanelIDPattern  = regexp.MustCompile(`^b-[a-f0-9]{18}$`)
	browserSnapshotRef     = regexp.MustCompile(`^@[A-Za-z0-9_-]{1,128}$`)
	browserArtifactURLPath = regexp.MustCompile(`^/v1/artifacts/[A-Za-z0-9_-]{1,38}/[A-Za-z0-9][A-Za-z0-9._-]{0,126}\.(?:png|jpe?g|webp)$`)
)

type browserPanel struct {
	id, clientKey, projectID string
	closing                  bool
	retry                    *time.Timer
	retryDelay               time.Duration
	orphan                   bool
}

// BrowserPanels keeps random browser session identifiers on the application
// side. Browser remains authoritative for session serialization and quotas.
type BrowserPanels struct {
	auth          AuthConfig
	client        *http.Client
	mu            sync.Mutex
	panels        map[string]browserPanel
	draining      bool
	cleanupCtx    context.Context
	cleanupCancel context.CancelFunc
	store         *persist.Store
	journalName   string
	leaseOnce     sync.Once
}

func NewBrowserPanels(auth AuthConfig, client *http.Client) *BrowserPanels {
	if client == nil {
		client = &http.Client{Timeout: browserPanelTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &BrowserPanels{auth: auth, client: client, panels: make(map[string]browserPanel), cleanupCtx: ctx, cleanupCancel: cancel}
}

func (p *BrowserPanels) Open(clientKey, projectID string) (string, error) {
	if projectID == "" || len(projectID) > 128 || strings.ContainsRune(projectID, 0) {
		return "", fmt.Errorf("invalid browser panel project")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.draining {
		return "", fmt.Errorf("browser panels are shutting down")
	}
	if len(p.panels) >= maxBrowserPanels {
		return "", fmt.Errorf("browser panel limit has been reached")
	}
	// Chromium creates Unix sockets below the browser service's per-session
	// directory. Keep the opaque ID short enough for that nested path.
	panelID := ""
	for panelID == "" || p.panels[panelID].id != "" {
		panelID = "b-" + strings.ReplaceAll(identifier.New(), "-", "")[:18]
	}
	panel := browserPanel{id: panelID, clientKey: clientKey, projectID: projectID}
	p.panels[panel.id] = panel
	if err := p.saveOwnership(); err != nil {
		delete(p.panels, panel.id)
		return "", err
	}
	return panel.id, nil
}

func (p *BrowserPanels) command(ctx context.Context, clientKey, panelID string, action any) (browserPanelResult, error) {
	panel, ok := p.owned(clientKey, panelID)
	if !ok {
		return browserPanelResult{}, fmt.Errorf("browser panel is unavailable")
	}
	command, args, err := browserPanelCommand(action)
	if err != nil {
		return browserPanelResult{}, err
	}
	result, err := p.call(ctx, panel.id, command, args)
	if err != nil {
		return browserPanelResult{}, err
	}
	return result, nil
}

func (p *BrowserPanels) Close(ctx context.Context, clientKey, panelID string) error {
	panel, found, err := p.beginClose(clientKey, panelID)
	if err != nil {
		return err
	}
	if !found {
		// The controller may have restarted while the browser tab remained in
		// the client store. A valid panel ID that is already absent is safe to
		// treat as closed, while existing panels still require ownership below.
		return nil
	}
	_, err = p.call(ctx, panel.id, "close", nil)
	return errors.Join(err, p.finishClose(panel.id, err == nil))
}

// ReleaseClient is called after the controller's reconnect grace period.
func (p *BrowserPanels) ReleaseClient(clientKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), browserPanelTimeout)
	defer cancel()
	p.closeMatching(ctx, func(panel browserPanel) bool { return panel.clientKey == clientKey })
}

// CloseAll releases browser state during application shutdown.
func (p *BrowserPanels) CloseAll(ctx context.Context) {
	p.mu.Lock()
	p.draining = true
	p.cleanupCancel()
	for id, panel := range p.panels {
		if panel.retry != nil {
			panel.retry.Stop()
			panel.retry = nil
			p.panels[id] = panel
		}
	}
	p.mu.Unlock()
	p.closeMatching(ctx, func(browserPanel) bool { return true })
}

// ReleaseProject closes panels as soon as a project is closed, rather than
// waiting for a browser client to reconnect or expire.
func (p *BrowserPanels) ReleaseProject(ctx context.Context, projectID string) {
	p.closeMatching(ctx, func(panel browserPanel) bool { return panel.projectID == projectID })
}

func (p *BrowserPanels) closeMatching(ctx context.Context, match func(browserPanel) bool) {
	p.mu.Lock()
	panels := make([]browserPanel, 0)
	for id, panel := range p.panels {
		if match(panel) && !panel.closing {
			panel.closing = true
			p.panels[id] = panel
			panels = append(panels, panel)
		}
	}
	p.mu.Unlock()
	workers := minBrowserCloseWorkers(len(panels))
	jobs := make(chan browserPanel)
	var pending sync.WaitGroup
	for range workers {
		pending.Add(1)
		go func() {
			defer pending.Done()
			for panel := range jobs {
				_, err := p.call(ctx, panel.id, "close", nil)
				p.finishClose(panel.id, err == nil)
			}
		}()
	}
	for _, panel := range panels {
		select {
		case jobs <- panel:
		case <-ctx.Done():
			p.finishClose(panel.id, false)
		}
	}
	close(jobs)
	pending.Wait()
}

func minBrowserCloseWorkers(count int) int {
	if count < browserCloseWorkers {
		return count
	}
	return browserCloseWorkers
}

func (p *BrowserPanels) owned(clientKey, panelID string) (browserPanel, bool) {
	if !browserPanelIDPattern.MatchString(panelID) {
		return browserPanel{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	panel, ok := p.panels[panelID]
	return panel, ok && !p.draining && !panel.orphan && panel.clientKey == clientKey && !panel.closing && panel.retry == nil
}

func (p *BrowserPanels) beginClose(clientKey, panelID string) (browserPanel, bool, error) {
	if !browserPanelIDPattern.MatchString(panelID) {
		return browserPanel{}, false, fmt.Errorf("browser panel is unavailable")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	panel, ok := p.panels[panelID]
	if !ok {
		return browserPanel{}, false, nil
	}
	if panel.orphan || panel.clientKey != clientKey || panel.closing {
		return browserPanel{}, false, fmt.Errorf("browser panel is unavailable")
	}
	if panel.retry != nil {
		panel.retry.Stop()
		panel.retry = nil
	}
	panel.closing = true
	p.panels[panelID] = panel
	return panel, true, nil
}

func (p *BrowserPanels) finishClose(panelID string, closed bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	panel, ok := p.panels[panelID]
	if !ok {
		return nil
	}
	var persistErr error
	if closed {
		delete(p.panels, panelID)
		persistErr = p.saveOwnership()
		if persistErr == nil {
			return nil
		}
		// Retain ownership until both remote close and the local acknowledgement
		// are durable. Closing an already absent session is idempotent.
	}
	panel.closing = false
	if !p.draining {
		if panel.retry != nil {
			panel.retry.Stop()
		}
		panel.retryDelay = min(30*time.Second, max(time.Second, panel.retryDelay*2))
		panel.retry = time.AfterFunc(panel.retryDelay, func() { p.retryClose(panelID) })
	}
	p.panels[panelID] = panel
	return persistErr
}

func (p *BrowserPanels) retryClose(panelID string) {
	p.mu.Lock()
	panel, ok := p.panels[panelID]
	if !ok || p.draining || panel.closing {
		p.mu.Unlock()
		return
	}
	panel.closing = true
	panel.retry = nil
	p.panels[panelID] = panel
	p.mu.Unlock()
	_, err := p.call(p.cleanupCtx, panelID, "close", nil)
	p.finishClose(panelID, err == nil)
}

type browserPanelResult struct {
	Output        string `json:"output"`
	ScreenshotURL string `json:"screenshotUrl,omitempty"`
}

func decodeBrowserPanelOpen(raw json.RawMessage) (string, bool) {
	if len(raw) > maxBrowserPanelRequest {
		return "", false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 1 || fields["projectId"] == nil {
		return "", false
	}
	var projectID string
	if json.Unmarshal(fields["projectId"], &projectID) != nil || projectID == "" {
		return "", false
	}
	return projectID, true
}

func decodeBrowserPanelCommand(raw json.RawMessage) (string, map[string]any, bool) {
	if len(raw) > maxBrowserPanelRequest {
		return "", nil, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 2 || fields["panelId"] == nil || fields["action"] == nil {
		return "", nil, false
	}
	var panelID string
	var action map[string]any
	if json.Unmarshal(fields["panelId"], &panelID) != nil || json.Unmarshal(fields["action"], &action) != nil || panelID == "" || action == nil {
		return "", nil, false
	}
	return panelID, action, true
}

func decodeBrowserPanelClose(raw json.RawMessage) (string, bool) {
	if len(raw) > 512 {
		return "", false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 1 || fields["panelId"] == nil {
		return "", false
	}
	var panelID string
	if json.Unmarshal(fields["panelId"], &panelID) != nil || panelID == "" {
		return "", false
	}
	return panelID, true
}

func (p *BrowserPanels) call(ctx context.Context, session, command string, args []string) (browserPanelResult, error) {
	body, err := json.Marshal(map[string]any{"session": session, "command": command, "args": args})
	if err != nil {
		return browserPanelResult{}, fmt.Errorf("encode browser command: %w", err)
	}
	bounded, cancel := context.WithTimeout(ctx, browserPanelTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(bounded, http.MethodPost, p.auth.BrowserURL+"/v1/browser", bytes.NewReader(body))
	if err != nil {
		return browserPanelResult{}, fmt.Errorf("create browser command: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Pixie-Panel-Lease", "1")
	if browserAuth, browserToken := p.auth.BrowserServiceAuth(); browserAuth {
		if !strongToken(browserToken) {
			return browserPanelResult{}, fmt.Errorf("browser panel is unavailable")
		}
		request.Header.Set("Authorization", "Bearer "+browserToken)
	}
	response, err := p.client.Do(request)
	if err != nil {
		return browserPanelResult{}, fmt.Errorf("browser command unavailable: %w", err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, maxBrowserPanelBody+1))
	if err != nil || len(content) > maxBrowserPanelBody {
		return browserPanelResult{}, fmt.Errorf("browser command returned an invalid response")
	}
	if response.StatusCode != http.StatusOK {
		return browserPanelResult{}, browserPanelFailure(content, response.StatusCode)
	}
	return parseBrowserPanelResult(content, session, command)
}

func browserPanelFailure(content []byte, status int) error {
	var failure struct {
		Warnings []string `json:"warnings"`
		Hints    []string `json:"hints"`
	}
	if json.Unmarshal(content, &failure) == nil {
		parts := append(failure.Warnings, failure.Hints...)
		if len(parts) > 0 && allBoundedText(parts, maxBrowserPanelText) {
			return fmt.Errorf("browser command rejected: %s", strings.Join(parts, " "))
		}
	}
	return fmt.Errorf("browser command failed with status %d", status)
}

func parseBrowserPanelResult(content []byte, session, command string) (browserPanelResult, error) {
	var result struct {
		Outcome  string `json:"outcome"`
		Command  string `json:"command"`
		Code     int    `json:"code"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		Artifact *struct {
			Session string `json:"session"`
			URL     string `json:"url"`
		} `json:"artifact"`
	}
	if json.Unmarshal(content, &result) != nil || result.Outcome != "completed" || result.Command != command || result.Code != 0 || !boundedText(result.Stdout, maxBrowserPanelBody) || !boundedText(result.Stderr, maxBrowserPanelBody) {
		return browserPanelResult{}, fmt.Errorf("browser command returned an invalid response")
	}
	output := strings.TrimSpace(strings.TrimSpace(result.Stdout) + "\n" + strings.TrimSpace(result.Stderr))
	parsed := browserPanelResult{Output: output}
	if result.Artifact != nil {
		if result.Artifact.Session != session || !strings.HasPrefix(result.Artifact.URL, "/v1/artifacts/"+session+"/") || !browserArtifactURLPath.MatchString(result.Artifact.URL) || strings.ContainsAny(result.Artifact.URL, "?#\\") {
			return browserPanelResult{}, fmt.Errorf("browser command returned an invalid artifact")
		}
		parsed.ScreenshotURL = result.Artifact.URL
	}
	return parsed, nil
}

func browserPanelCommand(value any) (string, []string, error) {
	action, ok := value.(map[string]any)
	if !ok || len(action) == 0 {
		return "", nil, fmt.Errorf("malformed browser panel action")
	}
	typeName, ok := action["type"].(string)
	if !ok {
		return "", nil, fmt.Errorf("malformed browser panel action")
	}
	switch typeName {
	case "back", "forward", "reload", "snapshot":
		if len(action) != 1 {
			return "", nil, fmt.Errorf("malformed browser panel action")
		}
		return typeName, nil, nil
	case "screenshot":
		if len(action) != 1 {
			return "", nil, fmt.Errorf("malformed browser panel action")
		}
		return "screenshot", []string{"panel-" + identifier.New() + ".png"}, nil
	case "open":
		url, ok := action["url"].(string)
		if len(action) != 2 || !ok || !safeBrowserPanelURL(url) {
			return "", nil, fmt.Errorf("browser URL must be a plain http(s) URL without credentials")
		}
		return "open", []string{url}, nil
	case "click":
		ref, ok := action["ref"].(string)
		if len(action) != 2 || !ok || !browserSnapshotRef.MatchString(ref) {
			return "", nil, fmt.Errorf("browser interaction requires a snapshot reference")
		}
		return "click", []string{ref}, nil
	case "fill":
		ref, refOK := action["ref"].(string)
		text, textOK := action["text"].(string)
		if len(action) != 3 || !refOK || !browserSnapshotRef.MatchString(ref) || !textOK || !boundedText(text, maxBrowserPanelText) {
			return "", nil, fmt.Errorf("browser text input is invalid")
		}
		return "fill", []string{ref, text}, nil
	case "viewport":
		width, widthOK := action["width"].(float64)
		height, heightOK := action["height"].(float64)
		if len(action) != 3 || !widthOK || !heightOK || width != float64(int(width)) || height != float64(int(height)) || width < 320 || width > 1920 || height < 240 || height > 1200 {
			return "", nil, fmt.Errorf("browser viewport must be 320-1920 by 240-1200")
		}
		return "set", []string{"viewport", fmt.Sprintf("%d", int(width)), fmt.Sprintf("%d", int(height))}, nil
	default:
		return "", nil, fmt.Errorf("unsupported browser panel action")
	}
}

func safeBrowserPanelURL(value string) bool {
	if !boundedText(value, maxBrowserPanelURLBytes) || strings.IndexFunc(value, func(r rune) bool { return r <= 0x20 || r == 0x7f }) >= 0 {
		return false
	}
	// Browser repeats the full URL policy. This narrow check prevents malformed
	// UI messages from reaching it without duplicating its network policy.
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != "" && parsed.User == nil
}

func boundedText(value string, limit int) bool {
	return utf8.ValidString(value) && len(value) <= limit && !strings.ContainsRune(value, 0)
}

func allBoundedText(values []string, limit int) bool {
	for _, value := range values {
		if !boundedText(value, limit) {
			return false
		}
	}
	return true
}
