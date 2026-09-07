package browser

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/miloszkolber/pixie/internal/diagnostics"
)

const (
	fixedHost             = "127.0.0.1"
	fixedPort             = 8787
	defaultArtifactRoot   = "/var/lib/pixie-browser/artifacts"
	defaultStateRoot      = "/var/lib/pixie-browser/state"
	defaultAgentBrowser   = "/usr/local/bin/agent-browser"
	defaultBrowserConfig  = "/app/config.json"
	maxProcessOutputBytes = 512 * 1024
	maxRequestBytes       = 64 * 1024
	defaultCommandTimeout = 120 * time.Second
	defaultRequestTimeout = 120 * time.Second
	defaultArtifactLimit  = 64 * 1024 * 1024
	defaultTotalArtifact  = 256 * 1024 * 1024
	defaultStateLimit     = 256 * 1024 * 1024
	defaultStateEntries   = 20_000
	defaultSessionLimit   = 16
	agentBrowserSession   = "browser"
	closeCommandTimeout   = 10 * time.Second
	terminateGrace        = 2 * time.Second
	treeReadBatch         = 128
)

var temporaryArtifactPrefix = ".pixie-screenshot-"

// Config defines the browser service's runtime and storage boundaries. It is
// a module-internal composition contract used by the MCP host to embed the
// service without reaching into its process-global environment.
type Config struct {
	Host, Token, PublicOrigin, ArtifactRoot, StateRoot, AgentBrowser, BrowserConfig string
	Port                                                                            int
	Authentication                                                                  bool
	CommandTimeout, RequestTimeout, PanelLeaseTimeout                               time.Duration
	MaxArtifactBytes, MaxTotalArtifactBytes, MaxStateBytes                          int64
	MaxSessions, MaxStateEntries                                                    int
}

func positiveEnvironmentInteger(value string, fallback int) (int, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("expected a positive integer, received: %s", value)
	}
	return parsed, nil
}

func readExactBoolean(value string, set bool) (bool, error) {
	if !set {
		return false, nil
	}
	if value == "true" {
		return true, nil
	}
	if value == "false" {
		return false, nil
	}
	return false, errors.New("PIXIE_BROWSER_AUTH must be exactly true or false")
}

// ConfigFromEnvironment reads and validates the browser service environment.
func ConfigFromEnvironment(lookup func(string) (string, bool)) (Config, error) {
	portValue, _ := lookup("PIXIE_BROWSER_PORT")
	port, err := positiveEnvironmentInteger(portValue, fixedPort)
	if err != nil {
		return Config{}, err
	}
	authValue, authSet := lookup("PIXIE_BROWSER_AUTH")
	auth, err := readExactBoolean(authValue, authSet)
	if err != nil {
		return Config{}, err
	}
	host, hostSet := lookup("PIXIE_BROWSER_HOST")
	if !hostSet {
		host = fixedHost
	}
	token, _ := lookup("PIXIE_BROWSER_TOKEN")
	publicOrigin, _ := lookup("PIXIE_BROWSER_PUBLIC_ORIGIN")
	return validateNetworkConfig(Config{
		Host: host, Port: port, Authentication: auth, Token: token, PublicOrigin: publicOrigin,
		ArtifactRoot: defaultArtifactRoot, StateRoot: defaultStateRoot, AgentBrowser: defaultAgentBrowser, BrowserConfig: defaultBrowserConfig,
		CommandTimeout: defaultCommandTimeout, RequestTimeout: defaultRequestTimeout,
		MaxArtifactBytes: defaultArtifactLimit, MaxTotalArtifactBytes: defaultTotalArtifact, MaxStateBytes: defaultStateLimit, MaxSessions: defaultSessionLimit, MaxStateEntries: defaultStateEntries,
	})
}

type serviceError struct {
	code, message, hint string
	status              int
	cause               error
}

func (e *serviceError) Error() string { return e.message }
func (e *serviceError) Unwrap() error { return e.cause }

func serviceFailure(code, message, hint string, status int, cause error) *serviceError {
	return &serviceError{code: code, message: message, hint: hint, status: status, cause: cause}
}

func quotaError(message string) *serviceError {
	return serviceFailure("quota_exceeded", message, "remove browser artifacts or close the session before retrying", http.StatusRequestEntityTooLarge, nil)
}

type app struct {
	config         Config
	build          diagnostics.BuildInfo
	logger         *slog.Logger
	started        time.Time
	requests       diagnostics.RequestCounter
	appViews       *appViewStore
	accounting     sync.Mutex
	reservations   map[string]int64
	artifactUsage  map[string]int64
	totalArtifacts int64
	activeMu       sync.Mutex
	active         map[uint64]context.CancelFunc
	nextActiveID   uint64
	shuttingDown   bool
	mcpHandler     http.Handler
	leaseCancel    context.CancelFunc
	leaseDone      chan struct{}
}

func newAppWithRuntime(config Config, build diagnostics.BuildInfo, logger *slog.Logger) (*app, error) {
	var err error
	config, err = validateNetworkConfig(config)
	if err != nil {
		return nil, err
	}
	if config.Authentication {
		if err := assertStrongToken(config.Token); err != nil {
			return nil, err
		}
	}
	info, err := os.Stat(config.AgentBrowser)
	if err != nil {
		return nil, fmt.Errorf("check browser executable: %w", err)
	}
	if info.Mode().Perm()&0111 == 0 {
		return nil, fmt.Errorf("check browser executable: not executable")
	}
	if _, err := os.Stat(config.BrowserConfig); err != nil {
		return nil, fmt.Errorf("check browser config: %w", err)
	}
	if config.PanelLeaseTimeout == 0 {
		config.PanelLeaseTimeout = 5 * time.Minute
	}
	if config.PanelLeaseTimeout < 0 {
		return nil, fmt.Errorf("panel lease timeout must be positive")
	}
	build = diagnostics.NormalizeBuild(build.Version, build.Revision)
	if logger == nil {
		logger = diagnostics.NewLogger("browser", build)
	}
	app := &app{config: config, build: build, logger: logger, started: time.Now(), appViews: newAppViewStore(), reservations: map[string]int64{}, artifactUsage: map[string]int64{}, active: map[uint64]context.CancelFunc{}}
	if err := app.initializeStorage(); err != nil {
		return nil, err
	}
	app.mcpHandler = app.newMCPHandler()
	app.startPanelLeases()
	return app, nil
}

// Service is the composable browser HTTP service. Its exported surface is kept
// to request handling and lifecycle; browser sessions and authority remain
// private to the package.
type Service struct {
	app *app
}

// NewService validates config and initializes browser storage and MCP routes.
func NewService(config Config, build diagnostics.BuildInfo, logger *slog.Logger) (*Service, error) {
	application, err := newAppWithRuntime(config, build, logger)
	if err != nil {
		return nil, err
	}
	return &Service{app: application}, nil
}

// ServeHTTP serves browser, artifact, diagnostic, App-view, and MCP routes.
func (s *Service) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	s.app.ServeHTTP(response, request)
}

// Ready reports whether the browser executable, configuration and storage
// boundaries can accept work. It is intentionally a small lifecycle surface
// for composite hosts; detailed diagnostics remain available at /readyz.
func (s *Service) Ready() bool { return s.app.readiness().Ready }

// Shutdown rejects new work, cancels active commands, and revokes App views.
func (s *Service) Shutdown() {
	s.app.shutdown()
}

func within(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}

func ensureDirectory(path, root string) error {
	if !within(root, path) {
		return serviceFailure("unsafe_path", "directory escaped its root", "", 400, nil)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return serviceFailure("unsafe_path", "not a real directory: "+path, "", 400, nil)
	}
	return nil
}

func temporaryArtifact(name string) bool {
	if !strings.HasPrefix(name, temporaryArtifactPrefix) || !strings.HasSuffix(name, ".tmp") {
		return false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(name, temporaryArtifactPrefix), ".tmp")
	if id == "" {
		return false
	}
	for _, character := range id {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '_' || character == '-') {
			return false
		}
	}
	return true
}

func measureTree(path, root string, ignoreTemporary bool) (int64, error) {
	bytes, _, err := measureTreeUsage(path, root, ignoreTemporary)
	return bytes, err
}

func measureTreeUsage(path, root string, ignoreTemporary bool) (int64, int, error) {
	return measureTreeUsageBounded(path, root, ignoreTemporary, -1, -1)
}

func measureTreeUsageBounded(path, root string, ignoreTemporary bool, maxBytes int64, maxEntries int) (int64, int, error) {
	var total int64
	count := 0
	directories := []string{path}
	for len(directories) > 0 {
		current := directories[len(directories)-1]
		directories = directories[:len(directories)-1]
		directory, err := os.Open(current)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, 0, err
		}
		for {
			entries, readErr := directory.ReadDir(treeReadBatch)
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				directory.Close()
				return 0, 0, readErr
			}
			for _, entry := range entries {
				if ignoreTemporary && temporaryArtifact(entry.Name()) {
					continue
				}
				child := filepath.Join(current, entry.Name())
				if !within(root, child) {
					directory.Close()
					return 0, 0, serviceFailure("unsafe_path", "path escaped quota root", "", 400, nil)
				}
				info, statErr := os.Lstat(child)
				if errors.Is(statErr, fs.ErrNotExist) {
					continue
				}
				if statErr != nil {
					directory.Close()
					return 0, 0, statErr
				}
				if info.Mode()&os.ModeSymlink != 0 {
					continue
				}
				count++
				if info.IsDir() {
					directories = append(directories, child)
				} else if info.Mode().IsRegular() {
					total += info.Size()
				}
				if (maxBytes >= 0 && total > maxBytes) || (maxEntries >= 0 && count > maxEntries) {
					directory.Close()
					return total, count, nil
				}
			}
			if errors.Is(readErr, io.EOF) || len(entries) == 0 {
				break
			}
		}
		if err := directory.Close(); err != nil {
			return 0, 0, err
		}
	}
	return total, count, nil
}

func listDirectoryNames(root string) (map[string]bool, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if !within(root, path) {
			return nil, serviceFailure("unsafe_path", "path escaped session root", "", 400, nil)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			names[entry.Name()] = true
		}
	}
	return names, nil
}

func cleanupStaleTemps(root string, removeLocks bool) error {
	sessions, err := listDirectoryNames(root)
	if err != nil {
		return err
	}
	for session := range sessions {
		sessionDir := filepath.Join(root, session)
		info, err := os.Lstat(sessionDir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		entries, err := os.ReadDir(sessionDir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !(temporaryArtifact(entry.Name()) || (removeLocks && entry.Name() == ".lock")) {
				continue
			}
			path := filepath.Join(sessionDir, entry.Name())
			item, err := os.Lstat(path)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if item.Mode().IsRegular() || item.Mode()&os.ModeSymlink != 0 {
				if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
			}
		}
	}
	return nil
}

func (a *app) initializeStorage() error {
	for _, root := range []string{a.config.ArtifactRoot, a.config.StateRoot} {
		if err := ensureDirectory(root, root); err != nil {
			return fmt.Errorf("initialize storage: %w", err)
		}
	}
	if err := cleanupStaleTemps(a.config.ArtifactRoot, false); err != nil {
		return err
	}
	if err := cleanupStaleTemps(a.config.StateRoot, true); err != nil {
		return err
	}
	sessions, err := listDirectoryNames(a.config.ArtifactRoot)
	if err != nil {
		return err
	}
	for session := range sessions {
		bytes, err := measureTree(filepath.Join(a.config.ArtifactRoot, session), a.config.ArtifactRoot, true)
		if err != nil {
			return err
		}
		a.artifactUsage[session] = bytes
		a.totalArtifacts += bytes
	}
	return nil
}

func (a *app) updateArtifactUsage(session string, bytes int64) {
	a.totalArtifacts += bytes - a.artifactUsage[session]
	a.artifactUsage[session] = bytes
}

func (a *app) prepareSession(session string, closing, leased bool) (string, string, error) {
	a.accounting.Lock()
	defer a.accounting.Unlock()
	artifactDir, stateDir := filepath.Join(a.config.ArtifactRoot, session), filepath.Join(a.config.StateRoot, session)
	if closing {
		if _, err := os.Lstat(stateDir); errors.Is(err, fs.ErrNotExist) {
			// A retried close must not create a session or require a free slot.
			// No runtime can be addressed without its per-session state directory.
			if err := a.removeSessionStorage(session, stateDir, artifactDir); err != nil {
				return "", "", err
			}
			return "", "", nil
		} else if err != nil {
			return "", "", err
		}
	}
	sessions, err := listDirectoryNames(a.config.StateRoot)
	if err != nil {
		return "", "", err
	}
	if !sessions[session] && len(sessions) >= a.config.MaxSessions {
		return "", "", serviceFailure("session_limit", "browser session limit has been reached", "close an existing browser session before starting another", http.StatusTooManyRequests, nil)
	}
	if leased && !closing && sessions[session] {
		if _, err := panelLeaseTime(stateDir); err != nil {
			return "", "", serviceFailure("session_conflict", "browser session is not a leased panel", "open a new browser panel", http.StatusConflict, nil)
		}
	}
	if err := ensureDirectory(artifactDir, a.config.ArtifactRoot); err != nil {
		return "", "", err
	}
	if err := ensureDirectory(stateDir, a.config.StateRoot); err != nil {
		return "", "", err
	}
	for _, path := range []string{filepath.Join(stateDir, "home"), filepath.Join(stateDir, "tmp"), filepath.Join(stateDir, "run")} {
		if err := ensureDirectory(path, stateDir); err != nil {
			return "", "", err
		}
	}
	if leased && !closing && !sessions[session] {
		if err := os.WriteFile(filepath.Join(stateDir, panelLeaseFile), []byte("1"), 0600); err != nil {
			return "", "", err
		}
	}
	return artifactDir, stateDir, nil
}

func acquireLock(stateDir string) (func(), error) {
	path := filepath.Join(stateDir, ".lock")
	handle, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, fs.ErrExist) {
		return nil, serviceFailure("session_busy", "browser session is busy", "retry after the active command finishes or use another session name", http.StatusConflict, nil)
	}
	if err != nil {
		return nil, err
	}
	info, err := handle.Stat()
	if err != nil {
		_ = handle.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return func() {
		_ = handle.Close()
		current, err := os.Lstat(path)
		if err == nil && os.SameFile(info, current) {
			_ = os.Remove(path)
		}
	}, nil
}

func randomID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

type artifactReservation struct {
	app      *app
	id       string
	released bool
}

func (a *app) reserveArtifactCapacity(bytes int64) (*artifactReservation, error) {
	a.accounting.Lock()
	defer a.accounting.Unlock()
	var reserved int64
	for _, bytes := range a.reservations {
		reserved += bytes
	}
	if a.totalArtifacts+reserved+bytes > a.config.MaxTotalArtifactBytes {
		return nil, quotaError("browser artifact storage quota is exceeded")
	}
	id, err := randomID()
	if err != nil {
		return nil, err
	}
	a.reservations[id] = bytes
	return &artifactReservation{app: a, id: id}, nil
}

func (r *artifactReservation) setBytes(bytes int64) {
	r.app.accounting.Lock()
	defer r.app.accounting.Unlock()
	if !r.released {
		r.app.reservations[r.id] = bytes
	}
}
func (r *artifactReservation) release() {
	r.app.accounting.Lock()
	defer r.app.accounting.Unlock()
	if !r.released {
		delete(r.app.reservations, r.id)
		r.released = true
	}
}

func (a *app) checkGlobalArtifactQuota() error {
	a.accounting.Lock()
	defer a.accounting.Unlock()
	if a.totalArtifacts > a.config.MaxTotalArtifactBytes {
		return quotaError("browser artifact storage quota is exceeded")
	}
	return nil
}

func runtimeEnvironment(stateDir, agentBrowser string) []string {
	home := filepath.Join(stateDir, "home")
	return []string{
		"PATH=" + filepath.Dir(agentBrowser), "HOME=" + home, "TMPDIR=" + filepath.Join(stateDir, "tmp"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"), "AGENT_BROWSER_SOCKET_DIR=" + filepath.Join(stateDir, "run"),
		"AGENT_BROWSER_CONTENT_BOUNDARIES=1", "AGENT_BROWSER_MAX_OUTPUT=20000",
	}
}

type runningCommand struct {
	command       *exec.Cmd
	done          chan struct{}
	result        chan error
	terminateOnce sync.Once
}

func monitorCommand(command *exec.Cmd) *runningCommand {
	running := &runningCommand{
		command: command,
		done:    make(chan struct{}),
		result:  make(chan error, 1),
	}
	go func() {
		running.result <- command.Wait()
		close(running.done)
	}()
	return running
}

func (r *runningCommand) wait() error {
	<-r.done
	return <-r.result
}

func (r *runningCommand) terminate() {
	r.terminateOnce.Do(func() {
		if r.command == nil || r.command.Process == nil {
			return
		}
		// Reaping the leader does not mean descendants have left its process group.
		pid := r.command.Process.Pid
		if err := syscall.Kill(-pid, syscall.SIGTERM); errors.Is(err, syscall.ESRCH) {
			return
		}
		timer := time.NewTimer(terminateGrace)
		defer timer.Stop()
		poll := time.NewTicker(20 * time.Millisecond)
		defer poll.Stop()
		for {
			select {
			case <-r.done:
				// The leader has exited; finish remaining descendants without
				// waiting for already-dead group members to be reaped.
				_ = syscall.Kill(-pid, syscall.SIGKILL)
				return
			case <-poll.C:
				if err := syscall.Kill(-pid, 0); errors.Is(err, syscall.ESRCH) {
					return
				}
			case <-timer.C:
				_ = syscall.Kill(-pid, syscall.SIGKILL)
				return
			}
		}
	})
}

func (a *app) closeSession(session, stateDir, artifactDir string) bool {
	command := exec.Command(a.config.AgentBrowser, "--config", a.config.BrowserConfig, "--session", agentBrowserSession, "close")
	command.Env = runtimeEnvironment(stateDir, a.config.AgentBrowser)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return false
	}
	running := monitorCommand(command)
	succeeded := false
	select {
	case <-running.done:
		err := running.wait()
		succeeded = err == nil
		if err != nil {
			running.terminate()
		}
	case <-time.After(closeCommandTimeout):
		running.terminate()
		_ = running.wait()
	}
	a.accounting.Lock()
	defer a.accounting.Unlock()
	if succeeded {
		return a.removeSessionStorage(session, stateDir, artifactDir) == nil
	}
	// Keep the daemon's socket location and ownership when cleanup fails, so a
	// subsequent close can still reach it instead of leaving an orphan process.
	return false
}

// Caller holds a.accounting. Only discard runtime addressing after close succeeds.
func (a *app) removeSessionStorage(session, stateDir, artifactDir string) error {
	if err := os.RemoveAll(artifactDir); err != nil {
		return err
	}
	if err := os.RemoveAll(stateDir); err != nil {
		return err
	}
	a.totalArtifacts -= a.artifactUsage[session]
	delete(a.artifactUsage, session)
	return nil
}

type outputCollector struct {
	mutex          sync.Mutex
	total          int
	exceeded       bool
	stdout, stderr bytes.Buffer
	limit          chan struct{}
}

type outputWriter struct {
	collector *outputCollector
	target    *bytes.Buffer
}

func (w outputWriter) Write(data []byte) (int, error) {
	c := w.collector
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if !c.exceeded {
		c.total += len(data)
		if c.total > maxProcessOutputBytes {
			c.exceeded = true
			close(c.limit)
		} else {
			_, _ = w.target.Write(data)
		}
	}
	return len(data), nil
}

func captureCommandOutput(command *exec.Cmd) *outputCollector {
	collector := &outputCollector{limit: make(chan struct{})}
	// os/exec joins its writer copies before Wait returns. StdoutPipe/StderrPipe
	// instead close at Wait and can discard a short child's unread output.
	command.Stdout = outputWriter{collector, &collector.stdout}
	command.Stderr = outputWriter{collector, &collector.stderr}
	// A descendant retaining a pipe must not keep a reaped command waiting forever.
	command.WaitDelay = terminateGrace
	return collector
}

func (c *outputCollector) result() (string, string, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.stdout.String(), c.stderr.String(), c.exceeded
}

func cleanupCode(err error) bool {
	var failure *serviceError
	return errors.As(err, &failure) && (failure.code == "command_timeout" || failure.code == "output_limit" || failure.code == "child_process" || failure.code == "request_cancelled")
}

func (a *app) runBrowser(ctx context.Context, request browserRequest) (result map[string]any, resultErr error) {
	started := a.requests.Begin()
	defer func() { a.requests.End(started, resultErr != nil) }()
	ctx, cancel := context.WithTimeout(ctx, a.config.RequestTimeout)
	a.activeMu.Lock()
	if a.shuttingDown {
		a.activeMu.Unlock()
		cancel()
		return nil, serviceFailure("shutting_down", "the browser service is shutting down", "", http.StatusServiceUnavailable, nil)
	}
	a.nextActiveID++
	activeID := a.nextActiveID
	a.active[activeID] = cancel
	a.activeMu.Unlock()
	defer func() {
		a.activeMu.Lock()
		delete(a.active, activeID)
		a.activeMu.Unlock()
		cancel()
	}()

	artifactDir, stateDir, err := a.prepareSession(request.Session, request.Command == "close", request.panelLease)
	if err != nil {
		return nil, err
	}
	if stateDir == "" {
		return map[string]any{"outcome": "completed", "command": "close", "code": 0, "stdout": "", "stderr": ""}, nil
	}
	releaseLock, err := acquireLock(stateDir)
	if err != nil {
		return nil, err
	}
	defer releaseLock()
	if request.expireLease {
		modified, err := panelLeaseTime(stateDir)
		if err != nil || time.Since(modified) < a.config.PanelLeaseTimeout {
			return map[string]any{"outcome": "completed", "command": "close", "code": 0}, nil
		}
	}
	if request.Command != "close" {
		// Active commands hold the same lock as expiry. Renew on completion too.
		if err := touchPanelLease(stateDir); err != nil {
			return nil, err
		}
		defer func() { _ = touchPanelLease(stateDir) }()
	}

	var temporaryPath, finalPath string
	var reservation *artifactReservation
	var command *exec.Cmd
	var running *runningCommand
	defer func() {
		if reservation != nil {
			reservation.release()
		}
		if temporaryPath != "" {
			_ = os.Remove(temporaryPath)
		}
	}()
	fail := func(err error) (map[string]any, error) {
		if running != nil {
			running.terminate()
		}
		if cleanupCode(err) {
			a.closeSession(request.Session, stateDir, artifactDir)
		}
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return fail(serviceFailure("request_cancelled", "the browser request was cancelled", "retry the browser action", 499, err))
	}
	closing := request.Command == "close"
	artifactBytes, err := measureTree(artifactDir, artifactDir, false)
	if err != nil {
		return fail(err)
	}
	stateBytes, stateEntries, err := measureTreeUsageBounded(stateDir, stateDir, false, a.config.MaxStateBytes, a.config.MaxStateEntries)
	if err != nil {
		return fail(err)
	}
	a.accounting.Lock()
	a.updateArtifactUsage(request.Session, artifactBytes)
	a.accounting.Unlock()
	if !closing && (artifactBytes > a.config.MaxArtifactBytes || stateBytes > a.config.MaxStateBytes || stateEntries > a.config.MaxStateEntries) {
		return fail(quotaError("browser session storage quota is exceeded"))
	}
	if !closing {
		if err := a.checkGlobalArtifactQuota(); err != nil {
			return fail(err)
		}
	}

	args := append([]string(nil), request.Args...)
	if outputName := screenshotFilename(request); outputName != "" {
		finalPath = filepath.Join(artifactDir, outputName)
		if !within(artifactDir, finalPath) || filepath.Base(finalPath) != outputName {
			return fail(serviceFailure("unsafe_path", "invalid screenshot filename", "", 400, nil))
		}
		if _, err := os.Lstat(finalPath); err == nil {
			return fail(serviceFailure("artifact_exists", "screenshot output must be a new path", "choose a new screenshot filename", http.StatusConflict, nil))
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fail(err)
		}
		if a.config.MaxArtifactBytes-artifactBytes <= 0 {
			return fail(quotaError("browser session artifact quota is exceeded"))
		}
		reservation, err = a.reserveArtifactCapacity(min64(a.config.MaxArtifactBytes-artifactBytes, a.config.MaxTotalArtifactBytes))
		if err != nil {
			return fail(err)
		}
		id, err := randomID()
		if err != nil {
			return fail(err)
		}
		temporaryPath = filepath.Join(artifactDir, temporaryArtifactPrefix+id+".tmp")
		args[request.Positionals[0].index] = temporaryPath
	}

	// Each API session has its own socket directory, so the subprocess session
	// name need not repeat the public identifier. Keeping it short prevents the
	// Unix socket path from exceeding Chromium's platform limit.
	command = exec.Command(a.config.AgentBrowser, append([]string{"--config", a.config.BrowserConfig, "--session", agentBrowserSession, request.Command}, args...)...)
	command.Dir, command.Env = artifactDir, runtimeEnvironment(stateDir, a.config.AgentBrowser)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	collector := captureCommandOutput(command)
	if err := command.Start(); err != nil {
		return fail(serviceFailure("child_process", "the browser process could not be started or reaped", "verify the browser executable and retry", 502, err))
	}
	running = monitorCommand(command)
	outputLimit := collector.limit
	timedOut, artifactExceeded, stateExceeded, cancelled := false, false, false, false
	var monitorErr error
	timeout := time.NewTimer(a.config.CommandTimeout)
	defer timeout.Stop()
	poll := time.NewTicker(25 * time.Millisecond)
	defer poll.Stop()
	statePollInterval := 250 * time.Millisecond
	statePoll := time.NewTimer(statePollInterval)
	defer statePoll.Stop()
	lastStateBytes := stateBytes
	var waitErr error
	waited := false
	for !waited {
		select {
		case <-running.done:
			waitErr = running.wait()
			waited = true
		case <-ctx.Done():
			cancelled = true
			running.terminate()
			waitErr = running.wait()
			waited = true
		case <-timeout.C:
			timedOut = true
			running.terminate()
			waitErr = running.wait()
			waited = true
		case <-outputLimit:
			outputLimit = nil
			running.terminate()
		case <-poll.C:
			if temporaryPath != "" {
				if info, statErr := os.Lstat(temporaryPath); statErr == nil && info.Size() > a.config.MaxArtifactBytes-artifactBytes {
					artifactExceeded = true
					running.terminate()
				}
			}
		case <-statePoll.C:
			if !closing {
				var currentStateBytes int64
				var currentStateEntries int
				currentStateBytes, currentStateEntries, monitorErr = measureTreeUsageBounded(stateDir, stateDir, false, a.config.MaxStateBytes, a.config.MaxStateEntries)
				if monitorErr != nil || currentStateBytes > a.config.MaxStateBytes || currentStateEntries > a.config.MaxStateEntries {
					stateExceeded = currentStateBytes > a.config.MaxStateBytes || currentStateEntries > a.config.MaxStateEntries
					running.terminate()
				}
				if currentStateBytes == lastStateBytes {
					statePollInterval = minDuration(2*time.Second, statePollInterval*2)
				} else {
					statePollInterval = 250 * time.Millisecond
					lastStateBytes = currentStateBytes
				}
			}
			statePoll.Reset(statePollInterval)
		}
	}
	stdoutText, stderrText, outputExceeded := collector.result()
	if cancelled {
		return fail(serviceFailure("request_cancelled", "the browser request was cancelled", "retry the browser action", 499, ctx.Err()))
	}
	if timedOut {
		return fail(serviceFailure("command_timeout", fmt.Sprintf("browser command exceeded %dms", a.config.CommandTimeout.Milliseconds()), "split the operation or retry with a simpler page action", http.StatusGatewayTimeout, nil))
	}
	if outputExceeded {
		return fail(serviceFailure("output_limit", "browser command output exceeded its limit", "request a smaller snapshot or more focused selector", http.StatusRequestEntityTooLarge, nil))
	}
	if artifactExceeded {
		return fail(quotaError("browser screenshot exceeded its artifact quota"))
	}
	if monitorErr != nil {
		return fail(monitorErr)
	}
	if stateExceeded {
		a.closeSession(request.Session, stateDir, artifactDir)
		return nil, quotaError("browser session state exceeded its storage quota")
	}
	if waitErr != nil {
		var exit *exec.ExitError
		if !errors.As(waitErr, &exit) {
			return fail(serviceFailure("child_process", "the browser process could not be started or reaped", "verify the browser executable and retry", 502, waitErr))
		}
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return fail(serviceFailure("child_process", fmt.Sprintf("agent-browser terminated by signal %s", status.Signal()), "retry the action after the browser session is reset", 502, waitErr))
		}
		return fail(serviceFailure("browser_failed", fmt.Sprintf("agent-browser exited with status %d", exit.ExitCode()), "retry the action; close the session if the failure persists", http.StatusUnprocessableEntity, waitErr))
	}

	finalStateBytes, finalStateEntries, err := measureTreeUsageBounded(stateDir, stateDir, false, a.config.MaxStateBytes, a.config.MaxStateEntries)
	if err != nil {
		return fail(err)
	}
	finalArtifactBytes, err := measureTree(artifactDir, artifactDir, false)
	if err != nil {
		return fail(err)
	}
	if !closing && (finalStateBytes > a.config.MaxStateBytes || finalStateEntries > a.config.MaxStateEntries) {
		a.closeSession(request.Session, stateDir, artifactDir)
		return nil, quotaError("browser output exceeded its state storage quota")
	}
	if !closing && finalArtifactBytes > a.config.MaxArtifactBytes {
		return fail(quotaError("browser output exceeded its storage quota"))
	}
	var artifact any
	if temporaryPath != "" {
		info, err := os.Lstat(temporaryPath)
		if err != nil {
			return fail(err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fail(serviceFailure("invalid_artifact", "screenshot output was not a regular file", "", 400, nil))
		}
		reservation.setBytes(info.Size())
		a.accounting.Lock()
		measureErr := error(nil)
		if a.totalArtifacts+info.Size() > a.config.MaxTotalArtifactBytes {
			measureErr = quotaError("browser output exceeded the global artifact quota")
		}
		if measureErr == nil {
			measureErr = os.Link(temporaryPath, finalPath)
		}
		if measureErr == nil {
			measureErr = os.Remove(temporaryPath)
			temporaryPath = ""
			a.updateArtifactUsage(request.Session, finalArtifactBytes)
		}
		a.accounting.Unlock()
		if errors.Is(measureErr, fs.ErrExist) {
			return fail(serviceFailure("artifact_exists", "screenshot output must be a new path", "choose a new screenshot filename", http.StatusConflict, measureErr))
		}
		if measureErr != nil {
			return fail(measureErr)
		}
		artifact = map[string]string{"session": request.Session, "name": filepath.Base(finalPath), "url": "/v1/artifacts/" + url.PathEscape(request.Session) + "/" + url.PathEscape(filepath.Base(finalPath))}
	} else if !closing {
		a.accounting.Lock()
		a.updateArtifactUsage(request.Session, finalArtifactBytes)
		exceeded := a.totalArtifacts > a.config.MaxTotalArtifactBytes
		a.accounting.Unlock()
		if exceeded {
			return fail(quotaError("browser output exceeded the global artifact quota"))
		}
	}
	if closing {
		a.accounting.Lock()
		err := a.removeSessionStorage(request.Session, stateDir, artifactDir)
		a.accounting.Unlock()
		if err != nil {
			return nil, err
		}
	}
	result = map[string]any{"outcome": "completed", "command": request.Command, "code": 0, "stdout": stdoutText, "stderr": stderrText}
	if artifact != nil {
		result["artifact"] = artifact
	}
	return result, nil
}

func min64(first, second int64) int64 {
	if first < second {
		return first
	}
	return second
}

func minDuration(first, second time.Duration) time.Duration {
	if first < second {
		return first
	}
	return second
}

func (a *app) authorized(request *http.Request) bool {
	if !a.config.Authentication {
		return true
	}
	provided := ""
	if len(request.Header.Values("Authorization")) != 1 {
		return false
	}
	if value := request.Header.Get("Authorization"); strings.HasPrefix(value, "Bearer ") {
		provided = strings.TrimPrefix(value, "Bearer ")
	}
	expected := []byte(a.config.Token)
	supplied := []byte(provided)
	return len(expected) > 0 && len(expected) == len(supplied) && subtle.ConstantTimeCompare(expected, supplied) == 1
}

func writeJSON(response http.ResponseWriter, status int, body any, headers http.Header) {
	for name, values := range headers {
		for _, value := range values {
			response.Header().Add(name, value)
		}
	}
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	encoded, err := json.Marshal(body)
	if err == nil {
		_, _ = response.Write(encoded)
	}
}

func rejectMethod(response http.ResponseWriter, request *http.Request, allowed string) {
	_, _ = io.Copy(io.Discard, request.Body)
	writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"outcome": "rejected", "code": "method_not_allowed"}, http.Header{"Allow": []string{allowed}})
}

func decodeJSONBody(request *http.Request) (any, error) {
	defer request.Body.Close()
	limited := io.LimitReader(request.Body, maxRequestBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(body) > maxRequestBytes {
		return nil, serviceFailure("request_too_large", "request body is too large", "", http.StatusRequestEntityTooLarge, nil)
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, serviceFailure("invalid_json", "request body must contain one JSON object", "", 400, err)
	}
	return decoded, nil
}

func requireJSONContentType(request *http.Request) error {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(request.Header.Get("Content-Type"), ";", 2)[0]))
	if mediaType != "application/json" {
		return serviceFailure("unsupported_media_type", "POST requests must use application/json", "set Content-Type: application/json", http.StatusUnsupportedMediaType, nil)
	}
	return nil
}

func parseRequestURL(request *http.Request) (*url.URL, error) {
	if strings.HasPrefix(request.RequestURI, "http://") || strings.HasPrefix(request.RequestURI, "https://") {
		return nil, serviceFailure("invalid_url", "request URL is malformed", "send an origin-form request path", 400, nil)
	}
	parsed, err := url.ParseRequestURI(request.RequestURI)
	if err != nil || parsed.IsAbs() {
		return nil, serviceFailure("invalid_url", "request URL is malformed", "send an origin-form request path", 400, err)
	}
	return parsed, nil
}

func respondError(response http.ResponseWriter, err error) {
	status, body := browserErrorResult(err)
	writeJSON(response, status, body, nil)
}

func browserErrorResult(err error) (int, map[string]any) {
	var policy *policyError
	if errors.As(err, &policy) {
		return 400, map[string]any{"outcome": "rejected", "code": policy.code, "warnings": []string{policy.message}, "hints": []string{policy.hint}}
	}
	var service *serviceError
	if errors.As(err, &service) {
		hints := []string{}
		if service.hint != "" {
			hints = append(hints, service.hint)
		}
		return service.status, map[string]any{"outcome": "rejected", "code": service.code, "warnings": []string{service.message}, "hints": hints}
	}
	slog.Error("browser request failed", "error", err)
	return http.StatusInternalServerError, map[string]any{"outcome": "failed", "code": "internal_error"}
}

func (a *app) serveArtifact(response http.ResponseWriter, request *http.Request, path string) {
	parts := strings.Split(strings.TrimPrefix(path, "/v1/artifacts/"), "/")
	if len(parts) != 2 {
		writeJSON(response, 404, map[string]any{"outcome": "rejected", "code": "not_found"}, nil)
		return
	}
	session, err1 := url.PathUnescape(parts[0])
	name, err2 := url.PathUnescape(parts[1])
	if err1 != nil || err2 != nil || !sessionPattern.MatchString(session) || filepath.Base(name) != name || !imageNamePattern.MatchString(name) {
		writeJSON(response, 400, map[string]any{"outcome": "rejected", "code": "invalid_artifact_path"}, nil)
		return
	}
	sessionDir, artifactPath := filepath.Join(a.config.ArtifactRoot, session), filepath.Join(filepath.Join(a.config.ArtifactRoot, session), name)
	if !within(a.config.ArtifactRoot, artifactPath) {
		writeJSON(response, 400, map[string]any{"outcome": "rejected", "code": "invalid_artifact_path"}, nil)
		return
	}
	root, rootErr := os.Lstat(a.config.ArtifactRoot)
	directory, directoryErr := os.Lstat(sessionDir)
	info, infoErr := os.Lstat(artifactPath)
	if rootErr != nil || directoryErr != nil || infoErr != nil || !root.IsDir() || root.Mode()&os.ModeSymlink != 0 || !directory.IsDir() || directory.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > a.config.MaxArtifactBytes {
		writeJSON(response, 404, map[string]any{"outcome": "failed", "code": "artifact_not_found"}, nil)
		return
	}
	fd, err := syscall.Open(artifactPath, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		writeJSON(response, 404, map[string]any{"outcome": "failed", "code": "artifact_not_found"}, nil)
		return
	}
	file := os.NewFile(uintptr(fd), artifactPath)
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		writeJSON(response, 404, map[string]any{"outcome": "failed", "code": "artifact_not_found"}, nil)
		return
	}
	contentType := "image/webp"
	lower := strings.ToLower(filepath.Ext(name))
	if lower == ".png" {
		contentType = "image/png"
	} else if lower == ".jpg" || lower == ".jpeg" {
		contentType = "image/jpeg"
	}
	response.Header().Set("Content-Type", contentType)
	response.Header().Set("Content-Length", strconv.FormatInt(opened.Size(), 10))
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(http.StatusOK)
	_, _ = io.Copy(response, file)
}

func artifactRoute(path string) bool {
	if !strings.HasPrefix(path, "/v1/artifacts/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/v1/artifacts/"), "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

func (a *app) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	a.activeMu.Lock()
	shuttingDown := a.shuttingDown
	a.activeMu.Unlock()
	if shuttingDown {
		writeJSON(response, http.StatusServiceUnavailable, map[string]any{"outcome": "rejected", "code": "shutting_down"}, http.Header{"Connection": []string{"close"}})
		return
	}
	parsed, err := parseRequestURL(request)
	if err != nil {
		respondError(response, err)
		return
	}
	path := parsed.EscapedPath()
	if parsed.RawQuery == "" && path == appViewPath {
		if request.Method != http.MethodPost {
			rejectMethod(response, request, http.MethodPost)
		} else {
			a.registerAppView(response, request)
		}
		return
	}
	if parsed.RawQuery == "" {
		if ticket, ok := appViewTicket(path); ok {
			switch request.Method {
			case http.MethodGet:
				a.serveAppView(response, request, ticket)
			case http.MethodDelete:
				a.deleteAppView(response, request, ticket)
			default:
				response.Header().Set("Allow", http.MethodGet+", "+http.MethodDelete)
				writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"outcome": "rejected", "code": "method_not_allowed"}, nil)
			}
			return
		}
	}
	if path == "/mcp" {
		a.serveMCP(response, request)
		return
	}
	if path == "/health" {
		if request.Method != http.MethodGet {
			rejectMethod(response, request, http.MethodGet)
		} else {
			writeJSON(response, 200, map[string]string{"status": "ok"}, nil)
		}
		return
	}
	if path == "/readyz" {
		if request.Method != http.MethodGet {
			rejectMethod(response, request, http.MethodGet)
			return
		}
		readiness := a.readiness()
		status := http.StatusOK
		if !readiness.Ready {
			status = http.StatusServiceUnavailable
		}
		writeJSON(response, status, readiness, nil)
		return
	}
	if path == "/status" {
		if request.Method != http.MethodGet {
			rejectMethod(response, request, http.MethodGet)
			return
		}
		if !a.authorized(request) {
			writeJSON(response, http.StatusUnauthorized, map[string]any{"outcome": "rejected", "code": "unauthorized"}, nil)
			return
		}
		writeJSON(response, http.StatusOK, a.status(), nil)
		return
	}
	if path == "/v1/browser/leases" && parsed.RawQuery == "" {
		a.renewPanelLeases(response, request)
		return
	}
	if path == "/v1/browser" && request.Method != http.MethodPost {
		rejectMethod(response, request, http.MethodPost)
		return
	}
	if artifactRoute(path) {
		if request.Method != http.MethodGet {
			rejectMethod(response, request, http.MethodGet)
			return
		}
		if !a.authorized(request) {
			writeJSON(response, http.StatusUnauthorized, map[string]any{"outcome": "rejected", "code": "unauthorized"}, nil)
			return
		}
		a.serveArtifact(response, request, path)
		return
	}
	if path != "/v1/browser" {
		writeJSON(response, http.StatusNotFound, map[string]any{"outcome": "rejected", "code": "not_found"}, nil)
		return
	}
	if !a.authorized(request) {
		_, _ = io.Copy(io.Discard, request.Body)
		writeJSON(response, http.StatusUnauthorized, map[string]any{"outcome": "rejected", "code": "unauthorized"}, nil)
		return
	}
	if err := requireJSONContentType(request); err != nil {
		_, _ = io.Copy(io.Discard, request.Body)
		respondError(response, err)
		return
	}
	body, err := decodeJSONBody(request)
	if err != nil {
		respondError(response, err)
		return
	}
	browserRequest, err := validateBrowserRequest(body)
	if err != nil {
		respondError(response, err)
		return
	}
	if lease := request.Header.Get(panelLeaseHeader); lease != "" {
		if lease != "1" || !panelSessionPattern.MatchString(browserRequest.Session) {
			respondError(response, reject("invalid browser panel lease"))
			return
		}
		browserRequest.panelLease = true
	}
	result, err := a.runBrowser(request.Context(), browserRequest)
	if err != nil {
		respondError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result, nil)
}

func (a *app) shutdown() {
	a.activeMu.Lock()
	a.shuttingDown = true
	cancels := make([]context.CancelFunc, 0, len(a.active))
	for _, cancel := range a.active {
		cancels = append(cancels, cancel)
	}
	a.activeMu.Unlock()
	a.leaseCancel()
	a.appViews.close()
	for _, cancel := range cancels {
		cancel()
	}
	<-a.leaseDone
}
