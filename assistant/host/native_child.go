package host

// This file owns the private boundary to the selected, official Pi JSONL RPC
// executable. Pi 0.85.1 has process-local session selection and its events do
// not identify a run or session. Consequently each logical session owns one
// child, launched in that session's cwd, for its entire resident lifetime.

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
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	nativeRecordMaxBytes = 32 * 1024 * 1024
	nativeAggregateBytes = 64 * 1024 * 1024
	nativeControlReserve = 1024 * 1024
	nativeControlMaxEach = 64 * 1024
	nativeControlMaxOps  = 8
	nativePendingMax     = 128
	nativeWriteTimeout   = 10 * time.Second
	nativeRequestTimeout = 30 * time.Second
	nativeDrainTimeout   = 25 * time.Second
	nativeAbortGrace     = 10 * time.Second
	nativePromptAccept   = 10 * time.Second
	nativeStartupMax     = 1024 * 1024
	nativeMaxChildren    = 16
	nativeMaxLaunching   = 4
	nativeObserverWait   = 2 * time.Second
)

type nativeReply struct {
	ID      uint64          `json:"id"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data"`
	Error   json.RawMessage `json:"error"`
	Success *bool           `json:"success"`
	Command string          `json:"command"`
}

type nativeCallError struct {
	reply nativeReply
	msg   string
}

func (e *nativeCallError) Error() string { return e.msg }

type promptRejectedError struct{ cause error }

func (e *promptRejectedError) Error() string { return e.cause.Error() }
func (e *promptRejectedError) Unwrap() error { return e.cause }

type promptUncertainError struct{ cause error }

func (e *promptUncertainError) Error() string { return e.cause.Error() }
func (e *promptUncertainError) Unwrap() error { return e.cause }

type nativePending struct {
	result  chan nativeReply
	bytes   int
	control bool
	command string
}

type nativeWrite struct {
	record []byte
	done   chan error
}

type nativeSessionRef struct {
	Path           string `json:"path"`
	CWD            string `json:"cwd"`
	Unmaterialized bool   `json:"unmaterialized,omitempty"`
}

type nativeRegistry struct {
	Version  int                         `json:"version"`
	Sessions map[string]nativeSessionRef `json:"sessions"`
}

type nativeRun struct {
	id                 string
	sessionID          string
	barrierComplete    bool
	accepted           bool
	started            bool
	settlementObserved bool
	postChecked        bool
	requireStart       bool
	cancelRequested    bool
	terminalStopReason string
	terminalError      string
	finished           bool
	settled            chan struct{}
	err                error
}

type nativeEvent struct {
	sessionID string
	event     map[string]any
}

// nativeSupervisor is a session-child registry, not a Pi process. A child is
// never switched for another logical session, including when cwd is shared.
type nativeSupervisor struct {
	config Config

	mu             sync.Mutex
	children       map[string]*nativeChild
	sessions       map[string]nativeSessionRef
	subscribers    map[uint64]func(nativeEvent) error
	nextSubscriber uint64
	stopping       bool
	errors         chan error
	errorsClosed   bool
	launching      int
	uncertain      map[string]string
	lost           map[string]string
	registryLock   *os.File
	hostIdentity   string
}

type nativeChild struct {
	owner  *nativeSupervisor
	config Config
	cwd    string

	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	writes      chan nativeWrite
	childExited chan struct{}
	done        chan struct{}
	errors      chan error

	mu             sync.Mutex
	nextID         uint64
	pending        map[uint64]nativePending
	pendingBytes   int
	controlPending int
	sessionID      string // Set once after exact state verification.
	sessionPath    string // Set once after exact state verification.
	activeRun      *nativeRun
	nextRun        uint64
	startupEvents  []map[string]any
	startupBytes   int
	failed         error
	stopping       bool
	releasing      bool
	closeOnce      sync.Once
	writeTimeout   time.Duration
	acceptTimeout  time.Duration
	launchReserved bool
}

func newNativeSupervisor(config Config) *nativeSupervisor {
	return &nativeSupervisor{
		config:      config,
		children:    make(map[string]*nativeChild),
		sessions:    make(map[string]nativeSessionRef),
		subscribers: make(map[uint64]func(nativeEvent) error),
		errors:      make(chan error, 1),
		uncertain:   make(map[string]string),
		lost:        make(map[string]string),
	}
}

func (s *nativeSupervisor) start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(s.config.PiExecutable) == "" {
		return errors.New("Pi executable is required")
	}
	if _, err := preparePiArgs(s.config.PiArgs); err != nil {
		return err
	}
	if strings.TrimSpace(s.config.AgentDir) == "" || !filepath.IsAbs(s.config.AgentDir) {
		return errors.New("Pi agent directory must be absolute in native mode")
	}
	if err := os.MkdirAll(filepath.Join(s.config.AgentDir, "pixie"), 0o700); err != nil {
		return fmt.Errorf("create native session registry directory: %w", err)
	}
	if err := s.lockInstallation(); err != nil {
		return err
	}
	if err := s.loadRegistry(); err != nil {
		_ = unlockNativeFile(s.registryLock)
		_ = s.registryLock.Close()
		s.registryLock = nil
		return err
	}
	identity, err := loadOrCreateHostIdentity(s.config.AgentDir)
	if err != nil {
		_ = unlockNativeFile(s.registryLock)
		_ = s.registryLock.Close()
		s.registryLock = nil
		return err
	}
	s.hostIdentity = identity
	return nil
}

// loadOrCreateHostIdentity persists a stable host authority in the selected
// agent directory. Recovery binds tombstones to this value instead of the
// process-random runtime or an ephemeral endpoint, so an ordinary restart
// neither loses nor replays destructive work.
func loadOrCreateHostIdentity(agentDir string) (string, error) {
	path := filepath.Join(agentDir, "pixie", "host-identity.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create native host identity directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("native host identity must be a regular non-symlink file")
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", fmt.Errorf("read native host identity: %w", readErr)
		}
		var record struct {
			Version  int    `json:"version"`
			Identity string `json:"identity"`
		}
		if json.Unmarshal(raw, &record) != nil || record.Version != 1 || !validHostIdentity(record.Identity) {
			return "", errors.New("native host identity file is invalid")
		}
		return record.Identity, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat native host identity: %w", err)
	}
	identity := randomID()
	if !validHostIdentity(identity) {
		return "", errors.New("failed to generate a stable native host identity")
	}
	raw, err := json.Marshal(map[string]any{"version": 1, "identity": identity})
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".host-identity-*")
	if err != nil {
		return "", fmt.Errorf("create native host identity: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(raw)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", fmt.Errorf("write native host identity: %w", err)
	}
	if err = os.Rename(name, path); err != nil {
		return "", fmt.Errorf("publish native host identity: %w", err)
	}
	return identity, nil
}

func validHostIdentity(identity string) bool {
	if len(identity) != 32 {
		return false
	}
	for _, char := range identity {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func preparePiArgs(args []string) ([]string, error) {
	for index, arg := range args {
		name, _, _ := strings.Cut(arg, "=")
		switch name {
		case "--session", "--session-id", "--resume", "--continue", "--fork", "--prompt", "--no-session", "-r", "-c", "-p":
			return nil, fmt.Errorf("Pi argument %q may select or mutate a session before host binding", arg)
		}
		if arg == "--mode" {
			if index+1 >= len(args) {
				return nil, errors.New("Pi args --mode requires a value")
			}
			if args[index+1] != "rpc" {
				return nil, fmt.Errorf("Pi RPC requires --mode rpc, got %q", args[index+1])
			}
		}
		if strings.HasPrefix(arg, "--mode=") && strings.TrimPrefix(arg, "--mode=") != "rpc" {
			return nil, fmt.Errorf("Pi RPC requires --mode rpc, got %q", strings.TrimPrefix(arg, "--mode="))
		}
	}
	return append([]string(nil), args...), nil
}

func hasPiRPCMode(args []string) bool {
	for index, arg := range args {
		if arg == "--mode" && index+1 < len(args) && args[index+1] == "rpc" || arg == "--mode=rpc" {
			return true
		}
	}
	return false
}

func (s *nativeSupervisor) launch(ctx context.Context, cwd string) (*nativeChild, error) {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return nil, errors.New("Pi supervisor is stopping")
	}
	if len(s.children)+s.launching >= nativeMaxChildren {
		s.mu.Unlock()
		return nil, fmt.Errorf("native child capacity is full (%d)", nativeMaxChildren)
	}
	if s.launching >= nativeMaxLaunching {
		s.mu.Unlock()
		return nil, fmt.Errorf("native child launch capacity is full (%d)", nativeMaxLaunching)
	}
	s.launching++
	s.mu.Unlock()
	launched := false
	defer func() {
		if !launched {
			s.mu.Lock()
			s.launching--
			s.mu.Unlock()
		}
	}()
	clean, err := exactDirectory(cwd)
	if err != nil {
		return nil, err
	}
	args, err := preparePiArgs(s.config.PiArgs)
	if err != nil {
		return nil, err
	}
	if !hasPiRPCMode(args) {
		args = append(args, "--mode", "rpc")
	}
	cmd := exec.Command(strings.TrimSpace(s.config.PiExecutable), args...)
	cmd.Dir = clean
	cmd.Env = childEnvironment(s.config.AgentDir)
	configureNativeProcess(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open Pi stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open Pi stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("open Pi stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start Pi executable %q: %w", s.config.PiExecutable, err)
	}
	child := &nativeChild{
		owner: s, config: s.config, cwd: clean, cmd: cmd, stdin: stdin, stdout: stdout,
		writes: make(chan nativeWrite, nativePendingMax), childExited: make(chan struct{}),
		done: make(chan struct{}), errors: make(chan error, 1), pending: make(map[uint64]nativePending),
		writeTimeout: nativeWriteTimeout, acceptTimeout: nativePromptAccept, launchReserved: true,
	}
	go drainNativeStderr(stderr)
	go child.writerLoop()
	go child.waitChild()
	go child.readLoop()
	launched = true
	return child, nil
}

func (s *nativeSupervisor) releaseLaunchReservation(child *nativeChild) {
	s.mu.Lock()
	s.releaseLaunchReservationLocked(child)
	s.mu.Unlock()
}

func (s *nativeSupervisor) releaseLaunchReservationLocked(child *nativeChild) {
	if child != nil && child.launchReserved {
		child.launchReserved = false
		s.launching--
	}
}

func exactDirectory(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", errors.New("session cwd must be absolute")
	}
	clean := filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", fmt.Errorf("resolve session cwd: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("session cwd is not a directory")
	}
	return filepath.Clean(resolved), nil
}

func childEnvironment(agentDir string) []string {
	allowedExact := map[string]bool{
		"PATH": true, "HOME": true, "TMPDIR": true, "LANG": true,
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true,
		"OPENAI_API_KEY": true, "ANTHROPIC_API_KEY": true, "GOOGLE_API_KEY": true,
		"GEMINI_API_KEY": true, "MISTRAL_API_KEY": true, "GROQ_API_KEY": true,
		"DEEPSEEK_API_KEY": true, "XAI_API_KEY": true, "COHERE_API_KEY": true,
		"OPENROUTER_API_KEY": true, "PERPLEXITY_API_KEY": true, "AZURE_OPENAI_API_KEY": true,
		"AWS_ACCESS_KEY_ID": true, "AWS_SECRET_ACCESS_KEY": true, "AWS_SESSION_TOKEN": true,
		"AWS_PROFILE": true, "GOOGLE_APPLICATION_CREDENTIALS": true, "GITHUB_TOKEN": true,
		"LLAMA_BASE_URL": true, "OLLAMA_HOST": true,
		"XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_CACHE_HOME": true,
	}
	result := make([]string, 0, len(os.Environ())+1)
	for _, value := range os.Environ() {
		name, _, ok := strings.Cut(value, "=")
		if !ok || strings.HasPrefix(name, "PIXIE_") {
			continue
		}
		if allowedExact[name] || strings.HasPrefix(name, "LC_") {
			result = append(result, value)
		}
	}
	if strings.TrimSpace(agentDir) != "" {
		result = append(result, "PI_CODING_AGENT_DIR="+agentDir)
	}
	return result
}

func drainNativeStderr(reader io.Reader) { _, _ = io.Copy(io.Discard, reader) }

func (c *nativeChild) writerLoop() {
	for {
		select {
		case <-c.done:
			// The child owns the only close of done. Exiting here releases the
			// writer goroutine on failure or teardown instead of parking it for
			// the life of the process.
			return
		case request := <-c.writes:
			written, err := c.stdin.Write(request.record)
			if err == nil && written != len(request.record) {
				err = io.ErrShortWrite
			}
			request.done <- err
			if err != nil {
				c.failNative(fmt.Errorf("write Pi RPC: %w", err))
				return
			}
		}
	}
}

func (c *nativeChild) waitChild() {
	err := c.cmd.Wait()
	if c.stdout != nil {
		_ = c.stdout.Close()
	}
	close(c.childExited)
	c.failNative(fmt.Errorf("Pi child exited: %s", nativeExitDescription(err)))
}

func nativeExitDescription(err error) string {
	if err == nil {
		return "successfully"
	}
	return err.Error()
}

func (c *nativeChild) readLoop() {
	reader := bufio.NewReaderSize(c.stdout, 64*1024)
	for {
		line, err := readNativeLine(reader)
		if err != nil {
			c.failNative(err)
			return
		}
		var frame map[string]json.RawMessage
		if json.Unmarshal(line, &frame) != nil || frame == nil {
			c.failNative(errors.New("invalid Pi JSONL object"))
			return
		}
		var frameType string
		_ = json.Unmarshal(frame["type"], &frameType)
		if frameType == "response" {
			var reply nativeReply
			if json.Unmarshal(line, &reply) != nil || reply.ID == 0 || reply.ID > 9_007_199_254_740_991 || reply.Command == "" {
				c.failNative(errors.New("Pi response has an invalid official RPC envelope"))
				return
			}
			c.mu.Lock()
			pending, found := c.pending[reply.ID]
			if found {
				delete(c.pending, reply.ID)
				c.pendingBytes -= pending.bytes
				if pending.control {
					c.controlPending--
				}
			}
			c.mu.Unlock()
			if found {
				if pending.command != reply.Command {
					c.failNative(fmt.Errorf("Pi response command %q does not match %q", reply.Command, pending.command))
					return
				}
				pending.result <- reply
			}
			continue
		}
		// A result/method-shaped frame is the removed child-side compatibility
		// protocol, not an official Pi event. Never silently accept it.
		if frame["method"] != nil || frame["result"] != nil || frame["id"] != nil {
			c.failNative(errors.New("child-side method RPC compatibility is unsupported; official Pi RPC is required"))
			return
		}
		var event map[string]any
		if json.Unmarshal(line, &event) != nil {
			c.failNative(errors.New("invalid Pi event"))
			return
		}
		c.mu.Lock()
		sessionID := c.sessionID
		if sessionID == "" {
			if c.startupBytes+len(line) > nativeStartupMax {
				c.mu.Unlock()
				c.failNative(errors.New("Pi startup event buffer exceeded before exact session binding"))
				return
			}
			c.startupEvents = append(c.startupEvents, event)
			c.startupBytes += len(line)
			c.mu.Unlock()
			continue
		}
		c.mu.Unlock()
		// publish is a barrier: every connection has enqueued this event on its
		// single outbound writer before settlement can release prompt response.
		if err := c.owner.publish(nativeEvent{sessionID: sessionID, event: event}); err != nil {
			c.failNative(err)
			return
		}
		c.observeRunEvent(event)
	}
}

func (c *nativeChild) observeRunEvent(event map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	run := c.activeRun
	if run == nil {
		return
	}
	kind, _ := event["type"].(string)
	switch kind {
	case "agent_start":
		if run.barrierComplete {
			if run.requireStart {
				run.settlementObserved = false
			}
			run.started = true
		}
	case "message_end":
		message, _ := event["message"].(map[string]any)
		if role, _ := message["role"].(string); role == "assistant" {
			run.terminalStopReason, _ = message["stopReason"].(string)
			run.terminalError, _ = message["errorMessage"].(string)
		}
	case "agent_settled":
		// Before the pre-dispatch state barrier this could only belong to startup
		// or prior native work. After dispatch, model runs require agent_start;
		// accepted input handlers are classified by the post-acceptance state probe.
		if run.barrierComplete {
			run.settlementObserved = true
			c.finishRunLocked(run)
		}
	}
}

func (c *nativeChild) finishRunLocked(run *nativeRun) {
	if run.finished || !run.accepted || !run.settlementObserved || !run.started && (!run.postChecked || run.requireStart) {
		return
	}
	run.finished = true
	close(run.settled)
	c.activeRun = nil
}

func readNativeLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > nativeRecordMaxBytes+1 {
			return nil, fmt.Errorf("Pi JSONL record exceeds %d bytes", nativeRecordMaxBytes)
		}
		if err == nil {
			line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte{'\n'}), []byte{'\r'})
			if len(bytes.TrimSpace(line)) == 0 {
				line = nil
				continue
			}
			if !utf8.Valid(line) {
				return nil, errors.New("Pi JSONL record is not valid UTF-8")
			}
			return line, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil, errors.New("Pi child closed stdout with an incomplete JSONL record")
		}
		return nil, fmt.Errorf("read Pi stdout: %w", err)
	}
}

func (c *nativeChild) callPi(ctx context.Context, command string, params map[string]any, control bool) (nativeReply, error) {
	if err := ctx.Err(); err != nil {
		return nativeReply{}, err
	}
	c.mu.Lock()
	if c.failed != nil {
		err := c.failed
		c.mu.Unlock()
		return nativeReply{}, err
	}
	if len(c.pending) >= nativePendingMax {
		c.mu.Unlock()
		return nativeReply{}, errors.New("Pi RPC has too many pending requests")
	}
	c.nextID++
	if c.nextID > 9_007_199_254_740_991 {
		c.nextID = 1
	}
	payload := map[string]any{"id": c.nextID, "type": command}
	for key, value := range params {
		if key != "type" && key != "id" {
			payload[key] = value
		}
	}
	record, err := json.Marshal(payload)
	if err != nil {
		c.mu.Unlock()
		return nativeReply{}, err
	}
	if len(record) > nativeRecordMaxBytes {
		c.mu.Unlock()
		return nativeReply{}, fmt.Errorf("Pi RPC request exceeds %d bytes", nativeRecordMaxBytes)
	}
	reserved := len(record) + 1
	limit := nativeAggregateBytes
	if !control {
		limit -= nativeControlReserve
	}
	if c.pendingBytes+reserved > limit {
		c.mu.Unlock()
		return nativeReply{}, errors.New("Pi RPC aggregate admission exceeded")
	}
	if control && (reserved > nativeControlMaxEach || c.controlPending >= nativeControlMaxOps) {
		c.mu.Unlock()
		return nativeReply{}, errors.New("Pi RPC control lane is saturated")
	}
	pending := nativePending{result: make(chan nativeReply, 1), bytes: reserved, control: control, command: command}
	id := c.nextID
	c.pending[id] = pending
	c.pendingBytes += reserved
	if control {
		c.controlPending++
	}
	c.mu.Unlock()

	if err := c.writeRecord(ctx, append(record, '\n')); err != nil {
		c.removePending(id)
		return nativeReply{}, err
	}
	select {
	case reply, ok := <-pending.result:
		if !ok {
			return nativeReply{}, c.failure()
		}
		if reply.Success == nil || !*reply.Success {
			return reply, &nativeCallError{reply: reply, msg: nativeErrorMessage(reply.Error)}
		}
		return reply, nil
	case <-ctx.Done():
		c.removePending(id)
		return nativeReply{}, ctx.Err()
	case <-c.done:
		return nativeReply{}, c.failure()
	}
}

func (c *nativeChild) removePending(id uint64) {
	c.mu.Lock()
	if item, ok := c.pending[id]; ok {
		delete(c.pending, id)
		c.pendingBytes -= item.bytes
		if item.control {
			c.controlPending--
		}
	}
	c.mu.Unlock()
}

func (c *nativeChild) failure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failed != nil {
		return c.failed
	}
	return errors.New("Pi child exited")
}

func (c *nativeChild) writeRecord(ctx context.Context, record []byte) error {
	if len(record) > nativeRecordMaxBytes+1 {
		return fmt.Errorf("Pi RPC write exceeds %d bytes", nativeRecordMaxBytes)
	}
	c.mu.Lock()
	failed := c.failed
	c.mu.Unlock()
	if failed != nil {
		return failed
	}
	request := nativeWrite{record: record, done: make(chan error, 1)}
	timeout := c.writeTimeout
	if timeout <= 0 {
		timeout = nativeWriteTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case c.writes <- request:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.failure()
	case <-timer.C:
		c.poison(errors.New("Pi RPC write queue stalled"))
		return errors.New("Pi RPC write stalled")
	}
	select {
	case err := <-request.done:
		if err != nil {
			return fmt.Errorf("write Pi RPC: %w", err)
		}
		return nil
	case <-ctx.Done():
		c.poison(fmt.Errorf("Pi RPC write outcome is uncertain: %w", ctx.Err()))
		return ctx.Err()
	case <-c.done:
		return c.failure()
	case <-timer.C:
		c.poison(errors.New("Pi RPC write stalled"))
		return errors.New("Pi RPC write stalled")
	}
}

func nativeErrorMessage(raw json.RawMessage) string {
	var object struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &object) == nil && object.Message != "" {
		return object.Message
	}
	var text string
	if json.Unmarshal(raw, &text) == nil && text != "" {
		return text
	}
	return "Pi RPC request failed"
}

func (c *nativeChild) bind(sessionID, path string, publishStartup bool) error {
	if sessionID == "" || path == "" || !filepath.IsAbs(path) {
		return errors.New("Pi state is missing an exact session id or absolute session file")
	}
	c.mu.Lock()
	path = filepath.Clean(path)
	if c.sessionID != "" && (c.sessionID != sessionID || c.sessionPath != path) {
		c.mu.Unlock()
		return errors.New("Pi child session identity changed")
	}
	c.sessionID, c.sessionPath = sessionID, path
	if !publishStartup {
		c.startupEvents = nil
		c.startupBytes = 0
	}
	c.mu.Unlock()
	return nil
}

func (c *nativeChild) flushStartupEvents() error {
	c.mu.Lock()
	events := c.startupEvents
	c.startupEvents = nil
	c.startupBytes = 0
	sessionID := c.sessionID
	c.mu.Unlock()
	for _, event := range events {
		if err := c.owner.publish(nativeEvent{sessionID: sessionID, event: event}); err != nil {
			return err
		}
	}
	return nil
}

type piState struct {
	SessionID     string         `json:"sessionId"`
	SessionFile   string         `json:"sessionFile"`
	SessionName   string         `json:"sessionName"`
	Model         map[string]any `json:"model"`
	ThinkingLevel string         `json:"thinkingLevel"`
	IsStreaming   bool           `json:"isStreaming"`
}

func (c *nativeChild) state(ctx context.Context) (piState, error) {
	reply, err := c.callPi(ctx, "get_state", nil, true)
	if err != nil {
		return piState{}, err
	}
	var state piState
	if json.Unmarshal(reply.Data, &state) != nil || state.SessionID == "" || !filepath.IsAbs(state.SessionFile) {
		return piState{}, errors.New("Pi state is missing an exact session id or absolute session file")
	}
	state.SessionFile = filepath.Clean(state.SessionFile)
	return state, nil
}

func (c *nativeChild) snapshot(ctx context.Context) (json.RawMessage, error) {
	state, err := c.state(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	id, path := c.sessionID, c.sessionPath
	c.mu.Unlock()
	if id == "" || state.SessionID != id || state.SessionFile != path {
		return nil, errors.New("Pi child returned a different exact session id or path")
	}
	messagesReply, err := c.callPi(ctx, "get_messages", nil, false)
	if err != nil {
		return nil, err
	}
	var messages struct {
		Messages []any `json:"messages"`
	}
	_ = json.Unmarshal(messagesReply.Data, &messages)
	options := []any{}
	metadata := map[string]any{}
	provider, _ := state.Model["provider"].(string)
	model, _ := state.Model["id"].(string)
	if provider != "" {
		options = append(options, map[string]any{"id": "provider", "currentValue": provider})
		metadata["providerId"] = provider
	}
	if model != "" {
		options = append(options, map[string]any{"id": "model", "currentValue": model})
		metadata["modelId"] = model
	}
	if state.ThinkingLevel != "" {
		options = append(options, map[string]any{"id": "thinking", "currentValue": state.ThinkingLevel})
	}
	c.mu.Lock()
	runID := ""
	if c.activeRun != nil {
		runID = c.activeRun.id
	}
	c.mu.Unlock()
	return json.Marshal(map[string]any{"capabilities": map[string]int{"sessions": 1}, "sessionId": id, "cwd": c.cwd, "configOptions": options, "metadata": metadata, "messages": messages.Messages, "runId": runID, "isStreaming": state.IsStreaming || runID != ""})
}

func (s *nativeSupervisor) callHost(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	if !nativeOperationSet()[method] {
		return nil, fmt.Errorf("operation %q is unsupported by the Go assistant", method)
	}
	switch method {
	case "session.list":
		s.mu.Lock()
		ids := make([]string, 0, len(s.sessions))
		for id := range s.sessions {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		items := make([]any, 0, len(ids))
		for _, id := range ids {
			ref := s.sessions[id]
			items = append(items, map[string]any{"sessionId": id, "cwd": ref.CWD})
		}
		s.mu.Unlock()
		return json.Marshal(map[string]any{"sessions": items})
	case "session.create":
		if hasCreateOverrides(params) {
			return nil, errors.New("Go assistant does not support create-time MCP, model, or thinking overrides")
		}
		cwd, _ := params["cwd"].(string)
		child, err := s.launch(ctx, cwd)
		if err != nil {
			return nil, err
		}
		defer s.releaseLaunchReservation(child)
		owned := false
		defer func() {
			if !owned {
				_ = child.close(context.Background())
			}
		}()
		// A fresh Pi RPC launch already owns a fresh native session. Calling
		// new_session would create an extra transcript and weaken launch identity.
		requestCtx, cancel := context.WithTimeout(ctx, nativeRequestTimeout)
		state, err := child.state(requestCtx)
		cancel()
		if err != nil {
			return nil, err
		}
		if state.IsStreaming {
			return nil, errors.New("fresh Pi launch unexpectedly resumed streaming work")
		}
		if err := child.bind(state.SessionID, state.SessionFile, true); err != nil {
			return nil, err
		}
		if err := s.install(state.SessionID, nativeSessionRef{Path: state.SessionFile, CWD: child.cwd}, child); err != nil {
			return nil, err
		}
		owned = true
		if err := child.flushStartupEvents(); err != nil {
			child.poison(err)
			return nil, err
		}
		raw, err := child.snapshot(ctx)
		return s.withRunState(state.SessionID, raw, err)
	case "session.load":
		id, _ := params["sessionId"].(string)
		requestedCWD, _ := params["cwd"].(string)
		child, err := s.loadChild(ctx, id, requestedCWD)
		if err != nil {
			return nil, err
		}
		raw, err := child.snapshot(ctx)
		return s.withRunState(id, raw, err)
	case "session.prompt":
		message, images, err := promptPayload(params)
		if err != nil {
			return nil, &promptRejectedError{cause: err}
		}
		id, _ := params["sessionId"].(string)
		s.mu.Lock()
		uncertain := s.uncertain[id]
		s.mu.Unlock()
		if uncertain != "" {
			return nil, fmt.Errorf("session has an uncertain accepted native run; reload and inspect the transcript: %s", uncertain)
		}
		child, err := s.resident(id)
		if err != nil {
			return nil, err
		}
		return child.prompt(ctx, message, images)
	case "session.cancel":
		id, _ := params["sessionId"].(string)
		child, err := s.resident(id)
		if err != nil {
			return nil, err
		}
		return child.cancel(ctx, id)
	case "session.release", "runtime.release":
		id, _ := params["sessionId"].(string)
		cwd, _ := params["cwd"].(string)
		return s.release(ctx, id, cwd)
	default:
		return nil, fmt.Errorf("operation %q is unsupported by the Go assistant", method)
	}
}

func (s *nativeSupervisor) withRunState(id string, raw json.RawMessage, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	uncertain := s.uncertain[id]
	s.mu.Unlock()
	if uncertain == "" {
		return raw, nil
	}
	result := map[string]any{}
	if json.Unmarshal(raw, &result) != nil {
		return nil, errors.New("encode uncertain native snapshot")
	}
	result["runId"] = "uncertain"
	result["isStreaming"] = true
	metadata, _ := result["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["activeRunUncertain"] = uncertain
	result["metadata"] = metadata
	return json.Marshal(result)
}

func (s *nativeSupervisor) install(id string, ref nativeSessionRef, child *nativeChild) error {
	ref, err := prepareRegistryRef(ref)
	if err != nil {
		return err
	}
	s.mu.Lock()
	child.mu.Lock()
	// Lock order is supervisor.mu then child.mu whenever both are required.
	// Child failure callbacks always release child.mu before taking supervisor.mu.
	defer func() { child.mu.Unlock(); s.mu.Unlock() }()
	if s.stopping {
		return errors.New("Pi supervisor is stopping")
	}
	if child.failed != nil || child.stopping {
		return errors.New("Pi child stopped before session installation")
	}
	if _, exists := s.sessions[id]; exists {
		return fmt.Errorf("native session id %q already exists", id)
	}
	for other, existing := range s.sessions {
		if existing.Path == ref.Path {
			return fmt.Errorf("native session path is already registered to %q", other)
		}
	}
	s.sessions[id] = ref
	if err := s.saveRegistryLocked(); err != nil {
		delete(s.sessions, id)
		return err
	}
	s.children[id] = child
	delete(s.lost, id)
	s.releaseLaunchReservationLocked(child)
	return nil
}

func (s *nativeSupervisor) loadChild(ctx context.Context, id, cwd string) (*nativeChild, error) {
	if id == "" {
		return nil, errors.New("sessionId is required")
	}
	clean, err := exactDirectory(cwd)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	ref, known := s.sessions[id]
	existing := s.children[id]
	s.mu.Unlock()
	if !known {
		return nil, fmt.Errorf("unknown native session %q; no durable registry entry exists", id)
	}
	if clean != ref.CWD {
		return nil, fmt.Errorf("session cwd does not match the durable native registry")
	}
	if existing != nil {
		return existing, nil
	}
	if err := validateRegistryRef(ref); err != nil {
		return nil, fmt.Errorf("native session %q is not materialized for reload: %w", id, err)
	}
	child, err := s.launch(ctx, ref.CWD)
	if err != nil {
		return nil, err
	}
	defer s.releaseLaunchReservation(child)
	owned := false
	defer func() {
		if !owned {
			_ = child.close(context.Background())
		}
	}()
	requestCtx, cancel := context.WithTimeout(ctx, nativeRequestTimeout)
	reply, err := child.callPi(requestCtx, "switch_session", map[string]any{"sessionPath": ref.Path}, true)
	cancel()
	if err != nil {
		return nil, err
	}
	var switched struct {
		Cancelled *bool `json:"cancelled"`
	}
	if json.Unmarshal(reply.Data, &switched) != nil || switched.Cancelled == nil || *switched.Cancelled {
		return nil, errors.New("Pi switch_session did not confirm cancelled:false")
	}
	requestCtx, cancel = context.WithTimeout(ctx, nativeRequestTimeout)
	state, err := child.state(requestCtx)
	cancel()
	if err != nil {
		return nil, err
	}
	if state.SessionID != id || state.SessionFile != ref.Path {
		return nil, fmt.Errorf("Pi selected a different session than %q", id)
	}
	if err := child.bind(id, ref.Path, false); err != nil {
		return nil, err
	}
	current, err := s.installLoadedChild(id, child)
	if err != nil {
		return nil, err
	}
	if current != child {
		return current, nil
	}
	owned = true
	return child, nil
}

func (s *nativeSupervisor) installLoadedChild(id string, child *nativeChild) (*nativeChild, error) {
	s.mu.Lock()
	child.mu.Lock()
	defer func() { child.mu.Unlock(); s.mu.Unlock() }()
	if current := s.children[id]; current != nil {
		return current, nil
	}
	if s.stopping {
		return nil, errors.New("Pi supervisor is stopping")
	}
	if child.failed != nil || child.stopping {
		return nil, errors.New("Pi child stopped before session installation")
	}
	s.children[id] = child
	delete(s.lost, id)
	s.releaseLaunchReservationLocked(child)
	return child, nil
}

func (s *nativeSupervisor) resident(id string) (*nativeChild, error) {
	s.mu.Lock()
	child := s.children[id]
	s.mu.Unlock()
	if child == nil {
		return nil, fmt.Errorf("session %q is not loaded", id)
	}
	return child, nil
}

func (s *nativeSupervisor) release(ctx context.Context, id, cwd string) (json.RawMessage, error) {
	if cwd == "" || !filepath.IsAbs(cwd) || filepath.Clean(cwd) != cwd {
		return nil, errors.New("session release cwd must be an exact absolute path")
	}
	clean, err := exactDirectory(cwd)
	if err != nil || clean != cwd {
		return nil, errors.New("session release cwd must be an exact existing non-symlink directory")
	}
	s.mu.Lock()
	child := s.children[id]
	ref, known := s.sessions[id]
	if child == nil || !known {
		s.mu.Unlock()
		return nil, fmt.Errorf("session %q is not loaded", id)
	}
	child.mu.Lock()
	if ref.CWD != cwd || child.cwd != cwd || child.sessionID != id || child.sessionPath != ref.Path {
		child.mu.Unlock()
		s.mu.Unlock()
		return nil, errors.New("session release identity does not match the durable native registry")
	}
	if child.releasing {
		child.mu.Unlock()
		s.mu.Unlock()
		return nil, errors.New("native session release is already in progress")
	}
	if child.activeRun != nil || child.failed != nil || child.stopping {
		child.mu.Unlock()
		s.mu.Unlock()
		return nil, errors.New("native session is not verifiably idle")
	}
	child.releasing = true
	child.mu.Unlock()
	s.mu.Unlock()
	committed := false
	defer func() {
		if committed {
			return
		}
		child.mu.Lock()
		child.releasing = false
		child.mu.Unlock()
	}()
	probeCtx, cancel := context.WithTimeout(ctx, nativeRequestTimeout)
	state, err := child.state(probeCtx)
	cancel()
	if err != nil {
		return nil, err
	}
	child.mu.Lock()
	idle := child.releasing && child.activeRun == nil && child.failed == nil && !child.stopping && !state.IsStreaming && state.SessionID == child.sessionID && state.SessionFile == child.sessionPath
	child.mu.Unlock()
	if !idle {
		return nil, errors.New("native session is not verifiably idle")
	}
	s.mu.Lock()
	if s.children[id] != child {
		s.mu.Unlock()
		return nil, errors.New("native session residence changed during release")
	}
	if err := s.refreshMaterializationLocked(id); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	delete(s.children, id)
	s.mu.Unlock()
	committed = true
	// Residence is committed once the fenced child is removed. Cleanup cannot
	// roll that decision back, so finish it independently of the caller timeout.
	go func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), nativeDrainTimeout+nativeAbortGrace)
		defer cancel()
		_ = child.close(closeCtx)
	}()
	return json.RawMessage(`{"ok":true}`), nil
}

func (c *nativeChild) prompt(ctx context.Context, message string, images []any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.releasing || c.stopping || c.failed != nil {
		c.mu.Unlock()
		return nil, &promptRejectedError{cause: errors.New("native session is releasing or unavailable")}
	}
	c.mu.Unlock()
	// First verify identity, then clear only Pi's process-local steer/follow-up
	// queue and issue an idle abort barrier. Pi answers abort only after prior
	// lifecycle output, including a late agent_settled after isStreaming=false,
	// has reached stdout. Controller-owned durable queued user work is untouched.
	barrierCtx, barrierCancel := context.WithTimeout(ctx, nativeRequestTimeout)
	state, err := c.state(barrierCtx)
	if err == nil && (state.SessionID != c.sessionID || state.SessionFile != c.sessionPath) {
		err = errors.New("Pi pre-dispatch state does not match the bound session")
	}
	if err == nil {
		_, err = c.callPi(barrierCtx, "clear_queue", nil, true)
	}
	if err == nil {
		_, err = c.callPi(barrierCtx, "abort", nil, true)
	}
	if err == nil {
		state, err = c.state(barrierCtx)
	}
	barrierCancel()
	if err != nil {
		return nil, &promptRejectedError{cause: err}
	}
	c.mu.Lock()
	if c.releasing || c.stopping || c.failed != nil {
		c.mu.Unlock()
		return nil, &promptRejectedError{cause: errors.New("native session is releasing or unavailable")}
	}
	if c.activeRun != nil {
		c.mu.Unlock()
		return nil, &promptRejectedError{cause: errors.New("session already has an active or uncertain prompt")}
	}
	if state.SessionID != c.sessionID || state.SessionFile != c.sessionPath {
		c.mu.Unlock()
		return nil, &promptRejectedError{cause: errors.New("Pi final pre-dispatch state does not match the bound session")}
	}
	if state.IsStreaming {
		c.mu.Unlock()
		return nil, &promptRejectedError{cause: errors.New("Pi final pre-dispatch state is not idle")}
	}
	c.nextRun++
	run := &nativeRun{id: fmt.Sprintf("native-%d", c.nextRun), sessionID: c.sessionID, barrierComplete: true, settled: make(chan struct{})}
	c.activeRun = run
	c.mu.Unlock()
	timeout := c.acceptTimeout
	if timeout <= 0 {
		timeout = nativePromptAccept
	}
	acceptCtx, cancel := context.WithTimeout(ctx, timeout)
	reply, err := c.callPi(acceptCtx, "prompt", map[string]any{"message": message, "images": images}, false)
	cancel()
	if err != nil {
		var rejected *nativeCallError
		if errors.As(err, &rejected) {
			c.clearRun(run)
			return nil, &promptRejectedError{cause: err}
		}
		uncertain := fmt.Errorf("Pi prompt acceptance is uncertain; session is poisoned: %w", err)
		c.mu.Lock()
		run.err = uncertain
		c.mu.Unlock()
		c.poison(uncertain)
		return nil, &promptUncertainError{cause: uncertain}
	}
	c.mu.Lock()
	run.accepted = true
	c.finishRunLocked(run)
	started, finished := run.started, run.finished
	c.mu.Unlock()
	if !finished && !started {
		// Pi may accept an extension/input-handler prompt without starting the
		// model. A response-ordered state probe distinguishes that handled case
		// from asynchronous provider work without imposing a provider deadline.
		postCtx, postCancel := context.WithTimeout(context.WithoutCancel(ctx), nativeRequestTimeout)
		post, stateErr := c.state(postCtx)
		postCancel()
		if stateErr != nil {
			uncertain := fmt.Errorf("Pi prompt was accepted but post-acceptance state is uncertain: %w", stateErr)
			c.mu.Lock()
			run.err = uncertain
			c.mu.Unlock()
			c.poison(uncertain)
			return nil, &promptUncertainError{cause: uncertain}
		}
		if post.SessionID != c.sessionID || post.SessionFile != c.sessionPath {
			uncertain := errors.New("Pi prompt changed exact session identity after acceptance")
			c.mu.Lock()
			run.err = uncertain
			c.mu.Unlock()
			c.poison(uncertain)
			return nil, &promptUncertainError{cause: uncertain}
		}
		c.mu.Lock()
		if c.activeRun == run {
			run.postChecked = true
			run.requireStart = post.IsStreaming
			if run.requireStart && !run.started {
				run.settlementObserved = false
			}
			c.finishRunLocked(run)
			if !run.finished && !run.started && !run.settlementObserved && !post.IsStreaming {
				run.finished = true
				close(run.settled)
				c.activeRun = nil
				if run.cancelRequested {
					run.terminalStopReason = "aborted"
				} else {
					run.terminalStopReason = "handled"
				}
			}
		}
		c.mu.Unlock()
	}
	select {
	case <-run.settled:
		if run.err != nil {
			return nil, run.err
		}
		stopReason := run.terminalStopReason
		if stopReason == "" {
			if run.terminalError != "" {
				stopReason = "error"
			} else if run.cancelRequested {
				stopReason = "aborted"
			} else {
				stopReason = "stop"
			}
		}
		return json.Marshal(map[string]any{"stopReason": stopReason, "native": reply.Data})
	case <-c.done:
		return nil, c.failure()
	}
}

func (c *nativeChild) clearRun(run *nativeRun) {
	c.mu.Lock()
	if c.activeRun == run {
		c.activeRun = nil
	}
	c.mu.Unlock()
}

func (c *nativeChild) cancel(ctx context.Context, sessionID string) (json.RawMessage, error) {
	c.mu.Lock()
	run := c.activeRun
	allowed := run != nil && run.sessionID == sessionID
	c.mu.Unlock()
	if !allowed {
		return nil, errors.New("session.cancel is allowed only for the active session run")
	}
	c.mu.Lock()
	if c.activeRun == run {
		run.cancelRequested = true
	}
	c.mu.Unlock()
	bounded, cancel := context.WithTimeout(ctx, nativeRequestTimeout)
	defer cancel()
	if _, err := c.callPi(bounded, "abort", nil, true); err != nil {
		return nil, err
	}
	return json.RawMessage(`{}`), nil
}

func hasCreateOverrides(params map[string]any) bool {
	for _, key := range []string{"model", "modelId", "provider", "thinkingLevel", "thinking_level"} {
		if _, ok := params[key]; ok {
			return true
		}
	}
	if servers, ok := params["mcpServers"].([]any); ok && len(servers) > 0 {
		return true
	}
	return false
}

func promptPayload(params map[string]any) (string, []any, error) {
	blocks, _ := params["content"].([]any)
	texts := []string{}
	images := []any{}
	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok {
			return "", nil, errors.New("prompt content block must be an object")
		}
		kind, _ := block["type"].(string)
		switch kind {
		case "text":
			text, ok := block["text"].(string)
			if !ok {
				return "", nil, errors.New("text prompt block is invalid")
			}
			texts = append(texts, text)
		case "image":
			data, ok := block["data"].(string)
			mime, mok := block["mimeType"].(string)
			if !ok || data == "" || !mok || mime == "" {
				return "", nil, errors.New("image prompt block is invalid")
			}
			images = append(images, map[string]any{"type": "image", "data": data, "mimeType": mime})
		case "resource":
			return "", nil, errors.New("Go assistant does not support text resource prompts")
		default:
			return "", nil, fmt.Errorf("unsupported prompt content type %q", kind)
		}
	}
	if len(texts) == 0 && len(images) == 0 {
		return "", nil, errors.New("prompt content is required")
	}
	return strings.Join(texts, "\n"), images, nil
}

func nativeOperationSet() map[string]bool {
	result := map[string]bool{}
	for _, operation := range []string{
		"session.list", "session.create", "session.load", "session.prompt", "session.cancel", "session.configure", "session.delete", "session.fork", "session.steer", "session.rename", "session.archive", "session.release", "runtime.release", "runtime.releaseToTui",
		"session.prompt.image", "session.prompt.resource", "session.uiResponse", "session.uiCancel", "pi.session.info", "pi.session.rename", "pi.session.archive", "pi.session.unarchive", "pi.session.steer", "pi.tools.list", "pi.tools.call", "runtime.capabilities", "runtime.restart", "pi.reload", "pi.slash-commands.list", "pi.providers.list", "pi.providers.canonical-model-info", "pi.providers.inventory.refresh", "pi.providers.readiness.check", "provider.loginStart", "provider.loginBegin", "provider.loginReply", "provider.loginCancel", "pi.providers.config.read", "pi.providers.config.delete", "pi.defaults.read", "pi.defaults.save", "pi.defaults.clear", "pi.preferences.read", "pi.preferences.save", "pi.preferences.reset", "pi.extensions.list", "pi.extensions.configure", "pi.sources.list", "pi.sources.create", "pi.sources.update", "pi.sources.delete", "pi.agent-mentions.list", "pi.subagent.execute", "pi.todo.plan", "pixie.goals.questions", "mcp.attach", "pi.config.extensions.list", "pi.config.extensions.add", "pi.config.extensions.set-enabled", "pi.config.extensions.remove", "pi.session.extensions.list", "pi.session.extensions.add", "pi.session.extensions.remove", "adapter.status", "adapter.registerBrowser", "adapter.session.forget", "pi.llama", "pi.native-extensions",
	} {
		result[operation] = false
	}
	for _, operation := range []string{"session.list", "session.create", "session.load", "session.prompt", "session.cancel", "session.prompt.image", "session.release", "runtime.release"} {
		result[operation] = true
	}
	return result
}

func (s *nativeSupervisor) capabilitySnapshot() map[string]int { return map[string]int{"sessions": 1} }

func (s *nativeSupervisor) subscribe(callback func(nativeEvent) error) func() {
	s.mu.Lock()
	s.nextSubscriber++
	id := s.nextSubscriber
	s.subscribers[id] = callback
	s.mu.Unlock()
	return func() { s.mu.Lock(); delete(s.subscribers, id); s.mu.Unlock() }
}

func (s *nativeSupervisor) publish(event nativeEvent) error {
	s.mu.Lock()
	callbacks := make([]func(nativeEvent) error, 0, len(s.subscribers))
	for _, callback := range s.subscribers {
		callbacks = append(callbacks, callback)
	}
	s.mu.Unlock()
	done := make(chan struct{}, len(callbacks))
	for _, callback := range callbacks {
		go func(deliver func(nativeEvent) error) {
			_ = deliver(event)
			done <- struct{}{}
		}(callback)
	}
	timer := time.NewTimer(nativeObserverWait)
	defer timer.Stop()
	for range callbacks {
		select {
		case <-done:
		case <-timer.C:
			return nil
		}
	}
	return nil
}

func (c *nativeChild) poison(err error) { c.failNative(err); go c.stopProcess() }

func (c *nativeChild) failNative(err error) {
	c.mu.Lock()
	if c.failed != nil {
		c.mu.Unlock()
		return
	}
	c.failed = err
	sessionID := c.sessionID
	stopping := c.stopping
	activeUncertain := c.activeRun != nil
	if c.activeRun != nil {
		c.activeRun.err = err
		close(c.activeRun.settled)
		c.activeRun = nil
	}
	close(c.done)
	pending := c.pending
	c.pending = make(map[uint64]nativePending)
	c.pendingBytes = 0
	c.controlPending = 0
	for _, item := range pending {
		close(item.result)
	}
	if !c.stopping {
		select {
		case c.errors <- err:
		default:
		}
	}
	close(c.errors)
	c.mu.Unlock()
	if c.owner != nil && sessionID != "" {
		c.owner.childFailed(sessionID, c, activeUncertain, err, !stopping)
	}
}

func (s *nativeSupervisor) childFailed(sessionID string, child *nativeChild, active bool, failure error, unexpected bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.children[sessionID] == child {
		delete(s.children, sessionID)
	}
	if active {
		s.uncertain[sessionID] = failure.Error()
	}
	if unexpected {
		s.lost[sessionID] = failure.Error()
		// Losing a child with an active run destroys execution integrity, so it
		// is fatal to the engine. An idle loss is recoverable: the session can
		// be loaded again, and readiness stays degraded until it is.
		if active && !s.errorsClosed {
			select {
			case s.errors <- failure:
			default:
			}
		}
	}
}

// health reports readiness for the native engine. A registered session whose
// child was lost degrades readiness until the session is loaded again; the
// condition is recoverable and clears on install.
func (s *nativeSupervisor) health() (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.lost) > 0 {
		return false, fmt.Sprintf("%d native session(s) lost their child and require reload", len(s.lost))
	}
	return true, ""
}

func (c *nativeChild) stopProcess() {
	if c.cmd == nil || c.cmd.Process == nil {
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		return
	}
	_ = c.stdin.Close()
	interruptNativeProcess(c.cmd.Process)
	select {
	case <-c.childExited:
	case <-time.After(nativeAbortGrace):
		killNativeProcess(c.cmd.Process)
	}
}

func (c *nativeChild) close(ctx context.Context) error {
	var closeErr error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.stopping = true
		failed := c.failed != nil
		c.mu.Unlock()
		if !failed && c.cmd != nil {
			bounded, cancel := context.WithTimeout(ctx, nativeDrainTimeout)
			_, _ = c.callPi(bounded, "shutdown", map[string]any{"reason": "service-drain"}, true)
			cancel()
		}
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.cmd != nil {
			select {
			case <-c.childExited:
			case <-ctx.Done():
				closeErr = ctx.Err()
				killNativeProcess(c.cmd.Process)
			case <-time.After(nativeDrainTimeout):
				killNativeProcess(c.cmd.Process)
			}
		}
		c.failNative(errors.New("Pi child closed"))
	})
	return closeErr
}

func (s *nativeSupervisor) close(ctx context.Context) error {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return nil
	}
	s.stopping = true
	children := make([]*nativeChild, 0, len(s.children))
	for _, child := range s.children {
		children = append(children, child)
	}
	s.mu.Unlock()
	var result error
	for _, child := range children {
		result = errors.Join(result, child.close(ctx))
	}
	if s.registryLock != nil {
		result = errors.Join(result, unlockNativeFile(s.registryLock), s.registryLock.Close())
		s.registryLock = nil
	}
	// Close the engine-error channel under the same lock childFailed uses so a
	// late unexpected child failure can never send on a closed channel.
	s.mu.Lock()
	if !s.errorsClosed {
		s.errorsClosed = true
		close(s.errors)
	}
	s.mu.Unlock()
	return result
}

func (s *nativeSupervisor) registryPath() string {
	if strings.TrimSpace(s.config.AgentDir) == "" {
		return ""
	}
	return filepath.Join(s.config.AgentDir, "pixie", "native-sessions.json")
}

func (s *nativeSupervisor) loadRegistry() error {
	path := s.registryPath()
	if path == "" {
		return nil
	}
	if !filepath.IsAbs(s.config.AgentDir) {
		return errors.New("Pi agent directory must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create native session registry directory: %w", err)
	}
	info, statErr := os.Lstat(path)
	if errors.Is(statErr, os.ErrNotExist) {
		return nil
	}
	if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("native session registry must be a regular non-symlink file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read native session registry: %w", err)
	}
	var registry nativeRegistry
	if json.Unmarshal(raw, &registry) != nil || registry.Version != 1 || registry.Sessions == nil {
		return errors.New("native session registry is invalid")
	}
	seen := map[string]string{}
	changed := false
	for id, ref := range registry.Sessions {
		if id == "" || filepath.Clean(ref.Path) != ref.Path || filepath.Clean(ref.CWD) != ref.CWD {
			return errors.New("native session registry contains an invalid identity")
		}
		if ref.Unmaterialized {
			_, pathErr := os.Lstat(ref.Path)
			if errors.Is(pathErr, os.ErrNotExist) {
				delete(registry.Sessions, id)
				changed = true
				continue
			}
		}
		if err := validateRegistryRef(ref); err != nil {
			return fmt.Errorf("native session registry entry %q: %w", id, err)
		}
		if ref.Unmaterialized {
			ref.Unmaterialized = false
			registry.Sessions[id] = ref
			changed = true
		}
		if other := seen[ref.Path]; other != "" {
			return fmt.Errorf("native session path is registered to both %q and %q", other, id)
		}
		seen[ref.Path] = id
	}
	s.sessions = registry.Sessions
	if changed {
		return s.saveRegistryLocked()
	}
	return nil
}

func prepareRegistryRef(ref nativeSessionRef) (nativeSessionRef, error) {
	if err := validateRegistryPaths(ref); err != nil {
		return nativeSessionRef{}, err
	}
	pathInfo, err := os.Lstat(ref.Path)
	if errors.Is(err, os.ErrNotExist) {
		ref.Unmaterialized = true
		return ref, nil
	}
	if err != nil || !pathInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 {
		return nativeSessionRef{}, errors.New("registered transcript must be a regular non-symlink file or an absent planned path")
	}
	ref.Unmaterialized = false
	return ref, nil
}

func validateRegistryRef(ref nativeSessionRef) error {
	if err := validateRegistryPaths(ref); err != nil {
		return err
	}
	pathInfo, err := os.Lstat(ref.Path)
	if err != nil || !pathInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("registered transcript must be an existing regular non-symlink file")
	}
	return nil
}

func validateRegistryPaths(ref nativeSessionRef) error {
	if !filepath.IsAbs(ref.Path) || !filepath.IsAbs(ref.CWD) {
		return errors.New("native registry paths must be absolute")
	}
	cwdInfo, err := os.Lstat(ref.CWD)
	if err != nil || !cwdInfo.IsDir() || cwdInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("registered cwd must be an existing non-symlink directory")
	}
	return nil
}

func (s *nativeSupervisor) refreshMaterializationLocked(id string) error {
	ref, ok := s.sessions[id]
	if !ok {
		return fmt.Errorf("native session %q has no durable registry entry", id)
	}
	if err := validateRegistryRef(ref); err != nil {
		return fmt.Errorf("native session %q transcript validation: %w", id, err)
	}
	if !ref.Unmaterialized {
		return nil
	}
	ref.Unmaterialized = false
	s.sessions[id] = ref
	if err := s.saveRegistryLocked(); err != nil {
		ref.Unmaterialized = true
		s.sessions[id] = ref
		return err
	}
	return nil
}

func (s *nativeSupervisor) lockInstallation() error {
	path := s.registryPath()
	if path == "" {
		return errors.New("Pi agent directory must be absolute in native mode")
	}
	file, err := os.OpenFile(filepath.Join(filepath.Dir(path), "native-sessions.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open native installation lock: %w", err)
	}
	if err := lockNativeFile(file); err != nil {
		_ = file.Close()
		return fmt.Errorf("lock native Pi installation: %w", err)
	}
	s.registryLock = file
	return nil
}

func (s *nativeSupervisor) saveRegistryLocked() error {
	path := s.registryPath()
	if path == "" {
		return nil
	}
	raw, err := json.Marshal(nativeRegistry{Version: 1, Sessions: s.sessions})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".native-sessions-*")
	if err != nil {
		return fmt.Errorf("create native session registry: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(raw)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write native session registry: %w", err)
	}
	if err = os.Rename(name, path); err != nil {
		return fmt.Errorf("publish native session registry: %w", err)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err == nil {
		err = dir.Sync()
		_ = dir.Close()
	}
	if err != nil {
		return fmt.Errorf("sync native session registry: %w", err)
	}
	return nil
}
