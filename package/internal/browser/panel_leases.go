package browser

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

const (
	panelLeaseHeader = "X-Pixie-Panel-Lease"
	panelLeaseFile   = ".controller-lease"
)

var panelSessionPattern = regexp.MustCompile(`^b-[a-f0-9]{18}$`)

// A marker is created only with a new, explicitly leased HTTP panel. Its mtime
// survives service restarts. Unmarked MCP/HTTP sessions are never reclaimed.
func panelLeaseTime(stateDir string) (time.Time, error) {
	dir, err := os.Lstat(stateDir)
	if err != nil {
		return time.Time{}, err
	}
	if !dir.IsDir() {
		return time.Time{}, fs.ErrInvalid
	}
	info, err := os.Lstat(filepath.Join(stateDir, panelLeaseFile))
	if err != nil {
		return time.Time{}, err
	}
	if !info.Mode().IsRegular() || info.Size() != 1 {
		return time.Time{}, fs.ErrInvalid
	}
	return info.ModTime(), nil
}

// Caller holds the session command lock. Never creates a marker or a session.
func touchPanelLease(stateDir string) error {
	if _, err := panelLeaseTime(stateDir); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	now := time.Now()
	return os.Chtimes(filepath.Join(stateDir, panelLeaseFile), now, now)
}

func (a *app) startPanelLeases() {
	ctx, cancel := context.WithCancel(context.Background())
	a.leaseCancel, a.leaseDone = cancel, make(chan struct{})
	go func() {
		defer close(a.leaseDone)
		ticker := time.NewTicker(minDuration(30*time.Second, max(time.Millisecond, a.config.PanelLeaseTimeout/4)))
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.expirePanelLeases(ctx)
			}
		}
	}()
}

func (a *app) expirePanelLeases(ctx context.Context) {
	sessions, err := listDirectoryNames(a.config.StateRoot)
	if err != nil {
		return
	}
	var workers sync.WaitGroup
	slots := make(chan struct{}, 4)
	defer workers.Wait()
	for session := range sessions {
		if ctx.Err() != nil {
			return
		}
		if !panelSessionPattern.MatchString(session) {
			continue
		}
		modified, err := panelLeaseTime(filepath.Join(a.config.StateRoot, session))
		if err != nil || time.Since(modified) < a.config.PanelLeaseTimeout {
			continue
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer func() { <-slots }()
			// Recheck expiry under the command lock; successful renewals win.
			bounded, cancel := context.WithTimeout(ctx, closeCommandTimeout)
			defer cancel()
			_, err := a.runBrowser(bounded, browserRequest{Session: session, Command: "close", expireLease: true})
			var failure *serviceError
			if err != nil && !(errors.As(err, &failure) && failure.code == "session_busy") && ctx.Err() == nil {
				a.logger.Warn("browser panel lease cleanup failed", "session", session, "error", err)
			}
		}()
	}
}

func (a *app) renewPanelLeases(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		rejectMethod(response, request, http.MethodPost)
		return
	}
	if !a.authorized(request) {
		writeJSON(response, http.StatusUnauthorized, map[string]string{"code": "unauthorized"}, nil)
		return
	}
	if err := requireJSONContentType(request); err != nil {
		respondError(response, err)
		return
	}
	body, err := decodeJSONBody(request)
	if err != nil {
		respondError(response, err)
		return
	}
	fields, ok := body.(map[string]any)
	values, valid := fields["sessions"].([]any)
	if !ok || !valid || len(fields) != 1 || len(values) > 16 {
		respondError(response, reject("invalid browser panel leases"))
		return
	}
	sessions := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		session, ok := value.(string)
		if !ok || !panelSessionPattern.MatchString(session) || seen[session] {
			respondError(response, reject("invalid browser panel leases"))
			return
		}
		seen[session] = true
		sessions = append(sessions, session)
	}
	renewed := make([]string, 0, len(sessions))
	for _, session := range sessions {
		stateDir := filepath.Join(a.config.StateRoot, session)
		if _, err := panelLeaseTime(stateDir); err != nil {
			continue
		}
		release, err := acquireLock(stateDir)
		if err != nil {
			continue
		}
		if _, err := panelLeaseTime(stateDir); err == nil && touchPanelLease(stateDir) == nil {
			renewed = append(renewed, session)
		}
		release()
	}
	writeJSON(response, http.StatusOK, map[string]any{"renewed": renewed}, nil)
}
