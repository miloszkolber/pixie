package host

// This file owns the explicitly opt-in administration bridge. The Go host
// cannot load the selected Pi installation's SDK in-process, so when an
// operator enables the bridge the host spawns a bounded sidecar lazily on the
// first administration request and proxies exactly the allowlisted FC17,
// FC18, FC19, FC20, FC21 and FC26 operations. Everything here fails closed: a
// disabled or unverified bridge advertises no administration operation and
// refuses every request. The sidecar command, selected package path and agent
// directory are supplied by explicit configuration only; there is no global or
// bundled SDK fallback.
//
// The sidecar protocol has two server-to-client frame directions: an `id`
// reply for each request, and an unsolicited `event`/`params` frame that is
// forwarded verbatim to the controller's method/params event path while the
// responsible request is still streaming (for example `provider.login`).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	nativeAdminBridgePackageName    = "@earendil-works/pi-coding-agent"
	nativeAdminBridgeHelloTimeout   = 10 * time.Second
	nativeAdminBridgeRequestTimeout = 30 * time.Second
	nativeAdminBridgeShutdownGrace  = 5 * time.Second
	nativeAdminBridgeMaxFrameBytes  = 4 * 1024 * 1024
	nativeAdminBridgeMaxPending     = 8
	nativeAdminBridgeMaxPackageJSON = 64 * 1024
)

// nativeAdminBridgeOperations is the only administration surface this bridge
// proxies. Rows that cannot be implemented honestly through the selected
// installation's public exports stay absent and therefore fail closed.
var nativeAdminBridgeOperations = []string{
	"pi.providers.list",
	"pi.providers.readiness.check",
	"pi.providers.inventory.refresh",
	"pi.providers.canonical-model-info",
	"pi.defaults.read",
	"pi.defaults.save",
	"pi.defaults.clear",
	"pi.preferences.read",
	"pi.preferences.save",
	"pi.preferences.reset",
	"pi.extensions.list",
	"pi.extensions.configure",
	"pi.config.extensions.list",
	"pi.config.extensions.add",
	"pi.config.extensions.set-enabled",
	"pi.config.extensions.remove",
	"pi.session.extensions.list",
	"pi.session.extensions.add",
	"pi.session.extensions.remove",
	"pi.slash-commands.list",
	"provider.loginStart",
	"provider.loginBegin",
	"provider.loginReply",
	"provider.loginCancel",
	"provider.logout",
	"pi.providers.config.delete",
}

var nativeAdminBridgeVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(?:[-+][A-Za-z0-9.+-]+)?$`)

// AdminBridgeConfig is the explicit operator opt-in for the administration
// sidecar. Zero value (Enabled false) keeps the bridge entirely off.
type AdminBridgeConfig struct {
	// Enabled is the opt-in switch. It comes from PIXIE_ADMIN_BRIDGE=1 or the
	// assistant config file and never defaults on.
	Enabled bool
	// Executable runs the sidecar (normally a Bun binary). Args precede the
	// manager-appended --package/--agent-dir pair.
	Executable string
	Args       []string
	// PackagePath is the absolute path to the selected installation directory
	// or its package.json. It is verified before any operation is advertised.
	PackagePath string
}

// nativeAdminResponse is one sidecar reply frame.
type nativeAdminResponse struct {
	ID     uint64          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

// nativeAdminBridge owns the sidecar process and its bounded request channel.
// It is created even when disabled so the operation set can consult it
// uniformly; available() then stays false.
type nativeAdminBridge struct {
	config   AdminBridgeConfig
	agentDir string

	verifyErr      error
	packageName    string
	packageVersion string
	packageDir     string

	startMu sync.Mutex

	// onEvent forwards one unsolicited sidecar event to the host event path.
	// It is set once before the sidecar starts and may be nil in tests.
	onEvent func(method string, params map[string]any)

	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdinMu  sync.Mutex
	exited   chan struct{}
	done     chan struct{}
	doneOnce sync.Once
	pending  map[uint64]chan nativeAdminResponse
	nextID   uint64
	failed   error
	started  bool
	closed   bool
}

func newNativeAdminBridge(config AdminBridgeConfig, agentDir string) *nativeAdminBridge {
	bridge := &nativeAdminBridge{
		config:   config,
		agentDir: strings.TrimSpace(agentDir),
		done:     make(chan struct{}),
		pending:  make(map[uint64]chan nativeAdminResponse),
	}
	if !config.Enabled {
		return bridge
	}
	if strings.TrimSpace(config.Executable) == "" {
		bridge.verifyErr = errors.New("administration bridge has no sidecar executable")
		return bridge
	}
	if bridge.agentDir == "" || !filepath.IsAbs(bridge.agentDir) {
		bridge.verifyErr = errors.New("administration bridge requires an absolute Pi agent directory")
		return bridge
	}
	name, version, dir, err := verifySelectedPiPackage(config.PackagePath)
	if err != nil {
		bridge.verifyErr = err
		return bridge
	}
	bridge.packageName, bridge.packageVersion, bridge.packageDir = name, version, dir
	return bridge
}

// verifySelectedPiPackage resolves exactly the explicit package path and
// checks the selected installation's public package identity. It never
// searches a global or bundled location.
func verifySelectedPiPackage(packagePath string) (name, version, dir string, err error) {
	path := strings.TrimSpace(packagePath)
	if path == "" {
		return "", "", "", errors.New("administration bridge has no selected Pi package path")
	}
	if strings.ContainsRune(path, 0) || !filepath.IsAbs(path) {
		return "", "", "", errors.New("administration bridge package path must be an absolute path")
	}
	clean := filepath.Clean(path)
	info, statErr := os.Lstat(clean)
	if statErr != nil {
		return "", "", "", fmt.Errorf("administration bridge cannot stat the selected Pi package: %w", statErr)
	}
	switch {
	case info.Mode().IsRegular():
		dir = filepath.Dir(clean)
	case info.IsDir():
		dir = clean
	default:
		return "", "", "", errors.New("administration bridge package path is not a directory or package.json")
	}
	manifestPath := filepath.Join(dir, "package.json")
	manifestInfo, manifestErr := os.Lstat(manifestPath)
	if manifestErr != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 {
		return "", "", "", errors.New("selected Pi package has no regular package.json")
	}
	if manifestInfo.Size() > nativeAdminBridgeMaxPackageJSON {
		return "", "", "", errors.New("selected Pi package.json exceeds the bridge bound")
	}
	raw, readErr := os.ReadFile(manifestPath)
	if readErr != nil {
		return "", "", "", fmt.Errorf("read selected Pi package.json: %w", readErr)
	}
	var manifest struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if json.Unmarshal(raw, &manifest) != nil {
		return "", "", "", errors.New("selected Pi package.json is not valid JSON")
	}
	if manifest.Name != nativeAdminBridgePackageName {
		return "", "", "", fmt.Errorf("selected package is not %s", nativeAdminBridgePackageName)
	}
	if !nativeAdminBridgeVersionPattern.MatchString(manifest.Version) {
		return "", "", "", errors.New("selected Pi package has no valid version")
	}
	return manifest.Name, manifest.Version, dir, nil
}

// verificationError reports the construction-time verification failure, if any.
func (b *nativeAdminBridge) verificationError() error {
	if b == nil {
		return errors.New("administration bridge is not configured")
	}
	return b.verifyErr
}

// available is the single enablement gate for advertisement and dispatch. It
// is false for a disabled bridge, a package that failed verification, a
// sidecar whose hello attestation failed, and any exited/closed bridge.
func (b *nativeAdminBridge) available() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.config.Enabled || b.verifyErr != nil || b.failed != nil || b.closed {
		return false
	}
	return true
}

// applyOperationSet enables exactly the proxied FC17 operations when the
// bridge is enabled and verified.

func isNativeAdminBridgeOperation(method string) bool {
	for _, operation := range nativeAdminBridgeOperations {
		if operation == method {
			return true
		}
	}
	return false
}

// call proxies one allowlisted administration method. The sidecar starts
// lazily here, and a failed hello permanently disables the bridge for the
// process lifetime.
func (b *nativeAdminBridge) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	if b == nil {
		return nil, errors.New("administration bridge is unavailable")
	}
	if !isNativeAdminBridgeOperation(method) {
		return nil, fmt.Errorf("administration method %q is not bridged", method)
	}
	if !b.available() {
		return nil, fmt.Errorf("administration bridge is unavailable: %w", b.unavailableReason())
	}
	if err := b.ensureStarted(ctx); err != nil {
		b.fail(err)
		return nil, err
	}
	return b.request(ctx, method, params)
}

func (b *nativeAdminBridge) unavailableReason() error {
	if b == nil {
		return errors.New("not configured")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case b.verifyErr != nil:
		return b.verifyErr
	case b.failed != nil:
		return b.failed
	case b.closed:
		return errors.New("bridge is closed")
	case !b.config.Enabled:
		return errors.New("bridge is disabled")
	default:
		return errors.New("bridge is unavailable")
	}
}

// ensureStarted spawns the sidecar once and completes the bounded
// bridge.hello attestation against the locally verified package identity.
func (b *nativeAdminBridge) ensureStarted(ctx context.Context) error {
	b.startMu.Lock()
	defer b.startMu.Unlock()

	b.mu.Lock()
	if b.failed != nil {
		err := b.failed
		b.mu.Unlock()
		return err
	}
	if b.closed {
		b.mu.Unlock()
		return errors.New("administration bridge is closed")
	}
	if b.started {
		b.mu.Unlock()
		return nil
	}
	b.mu.Unlock()

	args := append([]string(nil), b.config.Args...)
	args = append(args, "--package", b.packageDir, "--agent-dir", b.agentDir)
	command := exec.Command(strings.TrimSpace(b.config.Executable), args...)
	command.Env = childEnvironment(b.agentDir)
	configureNativeProcess(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		return fmt.Errorf("open administration bridge stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("open administration bridge stdout: %w", err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("start administration bridge: %w", err)
	}
	exited := make(chan struct{})
	b.mu.Lock()
	b.cmd = command
	b.stdin = stdin
	b.exited = exited
	b.started = true
	b.mu.Unlock()

	go b.readLoop(stdout)
	go b.waitLoop(command, exited)

	helloCtx, cancel := context.WithTimeout(ctx, nativeAdminBridgeHelloTimeout)
	defer cancel()
	raw, err := b.request(helloCtx, "bridge.hello", map[string]any{})
	if err != nil {
		b.stopProcess()
		return fmt.Errorf("administration bridge hello failed: %w", err)
	}
	var hello struct {
		ProtocolVersion int    `json:"protocolVersion"`
		PackageName     string `json:"packageName"`
		PackageVersion  string `json:"packageVersion"`
		PackageDir      string `json:"packageDir"`
		ModuleOrigin    string `json:"moduleOrigin"`
	}
	if json.Unmarshal(raw, &hello) != nil {
		b.stopProcess()
		return errors.New("administration bridge hello is not a valid object")
	}
	if hello.ProtocolVersion < 1 {
		b.stopProcess()
		return errors.New("administration bridge hello is missing a protocol version")
	}
	if hello.PackageName != b.packageName || hello.PackageVersion != b.packageVersion {
		b.stopProcess()
		return fmt.Errorf(
			"administration bridge hello identity %q %q does not match the verified package",
			hello.PackageName, hello.PackageVersion,
		)
	}
	if !pathWithin(b.packageDir, hello.ModuleOrigin) {
		b.stopProcess()
		return errors.New("administration bridge hello entrypoint is outside the selected package")
	}
	return nil
}

// stopProcess kills the sidecar after a failed attestation so an unverified
// helper cannot keep running in the background.
func (b *nativeAdminBridge) stopProcess() {
	b.mu.Lock()
	command := b.cmd
	b.mu.Unlock()
	if command != nil && command.Process != nil {
		killNativeProcess(command.Process)
	}
}

// request sends one bounded JSONL frame and waits for its matching reply.
func (b *nativeAdminBridge) request(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	b.mu.Lock()
	if b.failed != nil {
		err := b.failed
		b.mu.Unlock()
		return nil, err
	}
	if b.stdin == nil || b.closed {
		b.mu.Unlock()
		return nil, errors.New("administration bridge is not running")
	}
	if len(b.pending) >= nativeAdminBridgeMaxPending {
		b.mu.Unlock()
		return nil, errors.New("administration bridge has too many pending requests")
	}
	b.nextID++
	if b.nextID == 0 {
		b.nextID = 1
	}
	id := b.nextID
	pending := make(chan nativeAdminResponse, 1)
	b.pending[id] = pending
	b.mu.Unlock()

	record, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		b.removePending(id)
		return nil, err
	}
	if len(record) > nativeAdminBridgeMaxFrameBytes {
		b.removePending(id)
		return nil, fmt.Errorf("administration bridge request exceeds %d bytes", nativeAdminBridgeMaxFrameBytes)
	}
	b.stdinMu.Lock()
	_, err = b.stdin.Write(append(record, '\n'))
	b.stdinMu.Unlock()
	if err != nil {
		b.removePending(id)
		b.fail(fmt.Errorf("write administration bridge request: %w", err))
		return nil, err
	}
	select {
	case response := <-pending:
		if !response.OK {
			return nil, errors.New(adminBridgeErrorMessage(response))
		}
		if len(response.Result) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return response.Result, nil
	case <-ctx.Done():
		b.removePending(id)
		return nil, ctx.Err()
	case <-b.done:
		b.removePending(id)
		return nil, b.failure()
	}
}

func (b *nativeAdminBridge) removePending(id uint64) {
	b.mu.Lock()
	delete(b.pending, id)
	b.mu.Unlock()
}

func adminBridgeErrorMessage(response nativeAdminResponse) string {
	if message := strings.TrimSpace(response.Error); message != "" {
		return message
	}
	return "administration bridge request failed"
}

func (b *nativeAdminBridge) readLoop(reader io.Reader) {
	buffered := bufio.NewReaderSize(reader, 64*1024)
	for {
		line, err := readBridgeLine(buffered)
		if err != nil {
			b.fail(err)
			return
		}
		var frame map[string]json.RawMessage
		if json.Unmarshal(line, &frame) != nil {
			b.fail(errors.New("administration bridge returned an invalid frame"))
			return
		}
		if _, isReply := frame["id"]; !isReply {
			method, params, eventErr := parseAdminEventFrame(frame)
			if eventErr != nil {
				b.fail(eventErr)
				return
			}
			b.mu.Lock()
			callback := b.onEvent
			b.mu.Unlock()
			if callback != nil {
				callback(method, params)
			}
			continue
		}
		var response nativeAdminResponse
		if json.Unmarshal(line, &response) != nil || response.ID == 0 || response.ID > 9_007_199_254_740_991 {
			b.fail(errors.New("administration bridge returned an invalid reply frame"))
			return
		}
		b.mu.Lock()
		pending := b.pending[response.ID]
		delete(b.pending, response.ID)
		b.mu.Unlock()
		if pending != nil {
			pending <- response
		}
	}
}

// parseAdminEventFrame validates one unsolicited server-to-client event frame.
// The only accepted shape is {"event": <string>, "params": <object>}.
func parseAdminEventFrame(frame map[string]json.RawMessage) (string, map[string]any, error) {
	rawEvent, present := frame["event"]
	if !present {
		return "", nil, errors.New("administration bridge returned a frame without an id or event")
	}
	var method string
	if json.Unmarshal(rawEvent, &method) != nil || method == "" ||
		len(method) > 128 || strings.ContainsRune(method, 0) {
		return "", nil, errors.New("administration bridge returned an invalid event name")
	}
	params := map[string]any{}
	if rawParams, exists := frame["params"]; exists && len(rawParams) > 0 {
		if json.Unmarshal(rawParams, &params) != nil {
			return "", nil, errors.New("administration bridge returned invalid event parameters")
		}
		if params == nil {
			params = map[string]any{}
		}
	}
	return method, params, nil
}

func (b *nativeAdminBridge) waitLoop(command *exec.Cmd, exited chan struct{}) {
	waitErr := command.Wait()
	b.mu.Lock()
	select {
	case <-exited:
	default:
		close(exited)
	}
	b.mu.Unlock()
	if waitErr == nil {
		b.fail(errors.New("administration bridge exited"))
	} else {
		b.fail(fmt.Errorf("administration bridge exited: %w", waitErr))
	}
}

func (b *nativeAdminBridge) fail(err error) {
	if b == nil {
		return
	}
	b.mu.Lock()
	if b.failed == nil {
		b.failed = err
	}
	b.mu.Unlock()
	b.doneOnce.Do(func() { close(b.done) })
}

func (b *nativeAdminBridge) failure() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failed != nil {
		return b.failed
	}
	return errors.New("administration bridge is unavailable")
}

// close terminates the sidecar and releases it with the child lifecycle.
func (b *nativeAdminBridge) close(ctx context.Context) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	command := b.cmd
	stdin := b.stdin
	exited := b.exited
	b.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if command != nil && command.Process != nil {
		interruptNativeProcess(command.Process)
		if exited != nil {
			select {
			case <-exited:
			case <-ctx.Done():
				killNativeProcess(command.Process)
			case <-time.After(nativeAdminBridgeShutdownGrace):
				killNativeProcess(command.Process)
			}
		}
	}
	b.doneOnce.Do(func() { close(b.done) })
	return nil
}

// readBridgeLine reads one size-bounded, UTF-8 JSONL record.
func readBridgeLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > nativeAdminBridgeMaxFrameBytes+1 {
			return nil, fmt.Errorf("administration bridge frame exceeds %d bytes", nativeAdminBridgeMaxFrameBytes)
		}
		if err == nil {
			line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte{'\n'}), []byte{'\r'})
			if len(bytes.TrimSpace(line)) == 0 {
				line = nil
				continue
			}
			if !utf8.Valid(line) {
				return nil, errors.New("administration bridge frame is not valid UTF-8")
			}
			return line, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil, errors.New("administration bridge closed stdout with an incomplete frame")
		}
		return nil, fmt.Errorf("read administration bridge stdout: %w", err)
	}
}

func pathWithin(root, candidate string) bool {
	if root == "" || candidate == "" {
		return false
	}
	cleanRoot := filepath.Clean(root)
	cleanCandidate := filepath.Clean(candidate)
	return cleanCandidate == cleanRoot || strings.HasPrefix(cleanCandidate, cleanRoot+string(filepath.Separator))
}
