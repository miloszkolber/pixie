package host

// This file owns the small process boundary between the Go host facade and a
// selected Pi executable.  Pi's public RPC is JSONL over stdin/stdout.  The
// wire is intentionally kept private to this package: callers only see the
// host websocket facade, while the child remains the owner of sessions,
// transcripts, providers and tools.

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
	nativeHelloTimeout   = 10 * time.Second
	nativeWriteTimeout   = 10 * time.Second
	nativeRequestTimeout = 30 * time.Second
	nativeDrainTimeout   = 25 * time.Second
	nativeAbortGrace     = 10 * time.Second
)

type nativeMode uint8

const (
	nativeModeUnknown nativeMode = iota
	nativeModePiRPC
	nativeModeMethodRPC
)

type nativeReply struct {
	ID              uint64          `json:"id"`
	Type            string          `json:"type"`
	Result          json.RawMessage `json:"result"`
	Data            json.RawMessage `json:"data"`
	Error           json.RawMessage `json:"error"`
	ProtocolVersion int             `json:"protocolVersion"`
	Success         *bool           `json:"success"`
	Command         string          `json:"command"`
	Accepted        bool            `json:"accepted"`
}

type nativeCallError struct {
	reply nativeReply
	msg   string
}

func (e *nativeCallError) Error() string { return e.msg }

type nativePending struct {
	result  chan nativeReply
	bytes   int
	control bool
}

type nativeSupervisor struct {
	config Config
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	mu             sync.Mutex
	writeMu        sync.Mutex
	nextID         uint64
	pending        map[uint64]nativePending
	pendingBytes   int
	controlPending int
	events         map[uint64]chan map[string]any
	nextSubscriber uint64
	mode           nativeMode
	capabilities   map[string]int
	currentSession string
	failed         error
	done           chan struct{}
	childExited    chan struct{}
	closeOnce      sync.Once

	// Errors is closed after an exit.  A non-nil value is sent only for an
	// unexpected child failure; normal Close never reports an error here.
	errors   chan error
	stopping bool
}

func newNativeSupervisor(config Config) *nativeSupervisor {
	return &nativeSupervisor{
		config:       config,
		pending:      make(map[uint64]nativePending),
		events:       make(map[uint64]chan map[string]any),
		done:         make(chan struct{}),
		childExited:  make(chan struct{}),
		errors:       make(chan error, 1),
		capabilities: map[string]int{"sessions": 1},
	}
}

func (s *nativeSupervisor) start(ctx context.Context) error {
	executable := strings.TrimSpace(s.config.PiExecutable)
	if executable == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	args := append([]string(nil), s.config.PiArgs...)
	preparedArgs, err := preparePiArgs(args)
	if err != nil {
		return err
	}
	args = preparedArgs
	if !hasPiRPCMode(args) {
		// The public Pi CLI enables its JSONL interface with --mode rpc.  A
		// fixture or an independently selected adapter may ignore these flags;
		// this keeps the default executable invocation compatible with Pi while
		// preserving the operator-provided argv verbatim.
		args = append(args, "--mode", "rpc")
	}
	cmd := exec.Command(executable, args...)
	configureNativeProcess(cmd)
	if dir := strings.TrimSpace(s.config.AgentDir); dir != "" {
		if !filepath.IsAbs(dir) {
			return fmt.Errorf("Pi agent directory must be absolute")
		}
		cmd.Dir = dir
	}
	cmd.Env = childEnvironment(s.config.AgentDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open Pi stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("open Pi stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("open Pi stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return fmt.Errorf("start Pi executable %q: %w", executable, err)
	}
	s.cmd, s.stdin, s.stdout = cmd, stdin, stdout
	go drainNativeStderr(stderr)
	go s.waitChild()
	go s.readLoop()

	helloCtx, cancel := context.WithTimeout(ctx, nativeHelloTimeout)
	defer cancel()
	reply, err := s.callRaw(helloCtx, nativeRequest{Type: "hello", ProtocolVersion: 1}, true)
	if err == nil && (reply.ProtocolVersion == 1 || reply.Type == "hello" || len(reply.Result) > 0) {
		s.mu.Lock()
		s.mode = nativeModeMethodRPC
		if len(reply.Result) > 0 {
			var result struct {
				Capabilities    map[string]int `json:"capabilities"`
				ProtocolVersion int            `json:"protocolVersion"`
			}
			if json.Unmarshal(reply.Result, &result) == nil {
				for key, value := range result.Capabilities {
					s.capabilities[key] = value
				}
			}
		}
		s.mu.Unlock()
		return nil
	}
	// Official Pi RPC reports an unknown command for the private hello probe.
	// That response is a useful positive identification, not a startup failure.
	if err != nil && isPiRPCUnknownHello(err) {
		s.mu.Lock()
		s.mode = nativeModePiRPC
		s.mu.Unlock()
		return nil
	}
	s.stopProcess()
	if err != nil {
		return fmt.Errorf("Pi RPC hello: %w", err)
	}
	return fmt.Errorf("Pi RPC hello returned an invalid response")
}

func hasPiRPCMode(args []string) bool {
	for index, arg := range args {
		if arg == "--mode" && index+1 < len(args) && args[index+1] == "rpc" {
			return true
		}
		if arg == "--mode=rpc" {
			return true
		}
	}
	return false
}

func preparePiArgs(args []string) ([]string, error) {
	for index, arg := range args {
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
	return args, nil
}

func childEnvironment(agentDir string) []string {
	// Keep the native process useful for normal Pi installations while never
	// forwarding Pixie's control secrets or its service-specific switches.
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
		if !ok || strings.HasPrefix(name, "PIXIE_") || name == "PIXIE_PI_SECRET_KEY" || name == "PIXIE_MCP_TOKEN" {
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

func drainNativeStderr(reader io.Reader) {
	_, _ = io.Copy(io.Discard, reader)
}

func (s *nativeSupervisor) waitChild() {
	err := s.cmd.Wait()
	// os/exec closes the direct pipes during Wait, but close our reader
	// explicitly as well so a descendant that inherited stdout cannot leave the
	// supervisor's reader goroutine behind after the managed leader exits.
	if s.stdout != nil {
		_ = s.stdout.Close()
	}
	close(s.childExited)
	s.failNative(fmt.Errorf("Pi child exited: %s", nativeExitDescription(err)))
}

func nativeExitDescription(err error) string {
	if err == nil {
		return "successfully"
	}
	return err.Error()
}

func (s *nativeSupervisor) readLoop() {
	reader := bufio.NewReaderSize(s.stdout, 64*1024)
	for {
		line, err := readNativeLine(reader)
		if err != nil {
			s.failNative(err)
			return
		}
		var frame map[string]json.RawMessage
		if err := json.Unmarshal(line, &frame); err != nil {
			s.failNative(fmt.Errorf("invalid Pi JSONL response: %w", err))
			return
		}
		if frame == nil {
			s.failNative(errors.New("Pi JSONL response must be an object"))
			return
		}
		_, hasResult := frame["result"]
		_, hasError := frame["error"]
		_, hasAccepted := frame["accepted"]
		var frameType string
		_ = json.Unmarshal(frame["type"], &frameType)
		isResponse := frameType == "response" || frameType == "error" || frameType == "accepted" || frameType == "hello" || hasResult || hasError || hasAccepted
		if rawID, ok := frame["id"]; ok && isResponse {
			var id uint64
			if json.Unmarshal(rawID, &id) != nil || id == 0 || id > 9_007_199_254_740_991 {
				s.failNative(errors.New("Pi response has an invalid correlation id"))
				return
			}
			var reply nativeReply
			if json.Unmarshal(line, &reply) != nil {
				s.failNative(errors.New("Pi response has an invalid envelope"))
				return
			}
			s.mu.Lock()
			pending, found := s.pending[id]
			if found {
				delete(s.pending, id)
				s.pendingBytes -= pending.bytes
				if pending.control {
					s.controlPending--
				}
			}
			s.mu.Unlock()
			if found {
				pending.result <- reply
			}
			continue
		}
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		s.mu.Lock()
		overflow := false
		for _, subscriber := range s.events {
			select {
			case subscriber <- event:
			default:
				// A slow websocket cannot retain an unbounded native event
				// backlog. Fail closed instead of silently losing transcript
				// events.
				overflow = true
			}
		}
		s.mu.Unlock()
		if overflow {
			s.failNative(errors.New("Pi event delivery backpressure exceeded"))
			return
		}
	}
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
			line = bytes.TrimSuffix(line, []byte{'\n'})
			line = bytes.TrimSuffix(line, []byte{'\r'})
			if len(bytes.TrimSpace(line)) == 0 {
				line = nil
				continue
			}
			if len(line) > nativeRecordMaxBytes {
				return nil, fmt.Errorf("Pi JSONL record exceeds %d bytes", nativeRecordMaxBytes)
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

type nativeRequest struct {
	ID              uint64
	Type            string
	Method          string
	Params          any
	ProtocolVersion int
}

func (s *nativeSupervisor) callRaw(ctx context.Context, request nativeRequest, control bool) (nativeReply, error) {
	if err := ctx.Err(); err != nil {
		return nativeReply{}, err
	}
	s.mu.Lock()
	if s.failed != nil {
		err := s.failed
		s.mu.Unlock()
		return nativeReply{}, err
	}
	if len(s.pending) >= nativePendingMax {
		s.mu.Unlock()
		return nativeReply{}, errors.New("Pi RPC has too many pending requests")
	}
	s.nextID++
	if s.nextID > 9_007_199_254_740_991 {
		s.nextID = 1
	}
	request.ID = s.nextID
	var payload map[string]any
	if request.Type == "hello" {
		payload = map[string]any{"id": request.ID, "type": "hello", "protocolVersion": request.ProtocolVersion}
	} else if request.Type == "request" {
		payload = map[string]any{"id": request.ID, "type": "request", "method": request.Method, "params": request.Params}
	} else if request.Type == "command" {
		payload = map[string]any{"id": request.ID, "type": request.Method}
		if object, ok := request.Params.(map[string]any); ok {
			for key, value := range object {
				if key != "type" {
					payload[key] = value
				}
			}
		}
	} else {
		payload = map[string]any{"id": request.ID, "type": request.Type}
	}
	record, err := json.Marshal(payload)
	if err != nil {
		s.mu.Unlock()
		return nativeReply{}, fmt.Errorf("encode Pi RPC request: %w", err)
	}
	if len(record) > nativeRecordMaxBytes {
		s.mu.Unlock()
		return nativeReply{}, fmt.Errorf("Pi RPC request exceeds %d bytes", nativeRecordMaxBytes)
	}
	bytesReserved := len(record) + 1
	limit := nativeAggregateBytes
	if !control {
		limit -= nativeControlReserve
	}
	if s.pendingBytes+bytesReserved > limit {
		s.mu.Unlock()
		return nativeReply{}, errors.New("Pi RPC aggregate admission exceeded")
	}
	if control {
		if bytesReserved > nativeControlMaxEach {
			s.mu.Unlock()
			return nativeReply{}, errors.New("Pi RPC control request exceeds 64 KiB")
		}
		if s.controlPending >= nativeControlMaxOps {
			s.mu.Unlock()
			return nativeReply{}, errors.New("Pi RPC control lane is saturated")
		}
	}
	pending := nativePending{result: make(chan nativeReply, 1), bytes: bytesReserved, control: control}
	s.pending[request.ID] = pending
	s.pendingBytes += bytesReserved
	if control {
		s.controlPending++
	}
	s.mu.Unlock()

	if err := s.writeRecord(ctx, append(record, '\n')); err != nil {
		s.mu.Lock()
		delete(s.pending, request.ID)
		s.pendingBytes -= bytesReserved
		if control {
			s.controlPending--
		}
		s.mu.Unlock()
		if errors.Is(err, context.DeadlineExceeded) {
			s.failNative(errors.New("Pi RPC write stalled"))
			go s.stopProcess()
		}
		return nativeReply{}, err
	}
	select {
	case reply, ok := <-pending.result:
		if !ok {
			s.mu.Lock()
			err := s.failed
			s.mu.Unlock()
			if err == nil {
				err = errors.New("Pi child exited")
			}
			return nativeReply{}, err
		}
		if len(reply.Error) > 0 && string(bytes.TrimSpace(reply.Error)) != "null" {
			return reply, &nativeCallError{reply: reply, msg: nativeErrorMessage(reply.Error)}
		}
		if reply.Success != nil && !*reply.Success {
			return reply, &nativeCallError{reply: reply, msg: nativeErrorMessage(reply.Error)}
		}
		return reply, nil
	case <-ctx.Done():
		s.mu.Lock()
		if current, ok := s.pending[request.ID]; ok {
			delete(s.pending, request.ID)
			s.pendingBytes -= current.bytes
			if current.control {
				s.controlPending--
			}
		}
		s.mu.Unlock()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			s.failNative(errors.New("Pi RPC response stalled"))
			go s.stopProcess()
		}
		return nativeReply{}, ctx.Err()
	case <-s.done:
		s.mu.Lock()
		err := s.failed
		s.mu.Unlock()
		if err == nil {
			err = errors.New("Pi child exited")
		}
		return nativeReply{}, err
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

func isPiRPCUnknownHello(err error) bool {
	var callErr *nativeCallError
	if !errors.As(err, &callErr) {
		return false
	}
	return callErr.reply.Type == "response" && strings.EqualFold(callErr.reply.Command, "hello")
}

func (s *nativeSupervisor) writeRecord(ctx context.Context, record []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if len(record) > nativeRecordMaxBytes+1 {
		return fmt.Errorf("Pi RPC write exceeds %d bytes", nativeRecordMaxBytes)
	}
	result := make(chan error, 1)
	go func() {
		written, err := s.stdin.Write(record)
		if err == nil && written != len(record) {
			err = io.ErrShortWrite
		}
		result <- err
	}()
	timer := time.NewTimer(nativeWriteTimeout)
	defer timer.Stop()
	select {
	case err := <-result:
		if err != nil {
			return fmt.Errorf("write Pi RPC: %w", err)
		}
		return nil
	case <-timer.C:
		return errors.New("Pi RPC write stalled")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *nativeSupervisor) failNative(err error) {
	s.mu.Lock()
	if s.failed != nil {
		s.mu.Unlock()
		return
	}
	s.failed = err
	close(s.done)
	pending := s.pending
	s.pending = make(map[uint64]nativePending)
	s.pendingBytes = 0
	s.controlPending = 0
	subscribers := s.events
	s.events = make(map[uint64]chan map[string]any)
	for _, item := range pending {
		close(item.result)
	}
	for _, subscriber := range subscribers {
		close(subscriber)
	}
	if !s.stopping {
		select {
		case s.errors <- err:
		default:
		}
	}
	close(s.errors)
	s.mu.Unlock()
}

func (s *nativeSupervisor) subscribe() (<-chan map[string]any, func()) {
	// At most two complete records wait behind one websocket writer. Together
	// with the 32 MiB record ceiling this stays within the native aggregate
	// budget even when a client stops reading.
	channel := make(chan map[string]any, 2)
	s.mu.Lock()
	if s.failed != nil {
		close(channel)
		s.mu.Unlock()
		return channel, func() {}
	}
	s.nextSubscriber++
	id := s.nextSubscriber
	s.events[id] = channel
	s.mu.Unlock()
	return channel, func() {
		s.mu.Lock()
		if current, ok := s.events[id]; ok {
			delete(s.events, id)
			close(current)
		}
		s.mu.Unlock()
	}
}

func (s *nativeSupervisor) callHost(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	s.mu.Lock()
	mode := s.mode
	s.mu.Unlock()
	if mode == nativeModeMethodRPC {
		reply, err := s.callRaw(ctx, nativeRequest{Type: "request", Method: method, Params: params}, isControlMethod(method))
		if err != nil {
			return nil, err
		}
		result := normalizeNativeResult(reply)
		if method == "session.create" || method == "session.load" {
			var snapshot struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(result, &snapshot) == nil && snapshot.SessionID != "" {
				s.mu.Lock()
				s.currentSession = snapshot.SessionID
				s.mu.Unlock()
			}
		}
		return result, nil
	}
	if mode != nativeModePiRPC {
		return nil, errors.New("Pi RPC is unavailable")
	}
	return s.callPiHost(ctx, method, params)
}

func isControlMethod(method string) bool {
	switch method {
	case "session.cancel", "session.configure", "runtime.shutdown", "abort", "set_model", "set_thinking_level", "shutdown":
		return true
	default:
		return false
	}
}

func normalizeNativeResult(reply nativeReply) json.RawMessage {
	if len(reply.Result) > 0 {
		return append(json.RawMessage(nil), reply.Result...)
	}
	if len(reply.Data) > 0 {
		return append(json.RawMessage(nil), reply.Data...)
	}
	return json.RawMessage("null")
}

func (s *nativeSupervisor) callPi(ctx context.Context, command string, params map[string]any) (nativeReply, error) {
	payload := map[string]any{"type": command}
	for key, value := range params {
		payload[key] = value
	}
	return s.callRaw(ctx, nativeRequest{Type: "command", Method: command, Params: payload}, isControlMethod(command))
}

func (s *nativeSupervisor) callPiHost(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	switch method {
	case "session.list":
		state, err := s.piState(ctx)
		if err != nil {
			return nil, err
		}
		result := map[string]any{"sessions": []any{}}
		if state.SessionID != "" {
			result["sessions"] = []any{map[string]any{"sessionId": state.SessionID, "cwd": state.CWD, "title": state.SessionName}}
		}
		return json.Marshal(result)
	case "session.create":
		if _, err := s.callPi(ctx, "new_session", nil); err != nil {
			return nil, err
		}
		return s.snapshot(ctx)
	case "session.load":
		sessionID, _ := params["sessionId"].(string)
		if sessionID == "" {
			return nil, errors.New("sessionId is required")
		}
		if _, err := s.callPi(ctx, "switch_session", map[string]any{"sessionPath": sessionID}); err != nil {
			return nil, err
		}
		return s.snapshot(ctx)
	case "session.prompt":
		message, images := promptPayload(params)
		reply, err := s.callPi(ctx, "prompt", map[string]any{"message": message, "images": images})
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"stopReason": "accepted", "native": normalizeNativeResult(reply)})
	case "session.configure":
		value, _ := params["value"].(string)
		configID, _ := params["configId"].(string)
		if configID == "model" {
			parts := strings.SplitN(value, "/", 2)
			if len(parts) != 2 {
				return nil, errors.New("model config value must be provider/id")
			}
			_, err := s.callPi(ctx, "set_model", map[string]any{"provider": parts[0], "modelId": parts[1]})
			result, marshalErr := json.Marshal(map[string]any{"configOptions": []any{}})
			return result, errors.Join(err, marshalErr)
		}
		if configID == "thinkingLevel" || configID == "thinking_level" {
			_, err := s.callPi(ctx, "set_thinking_level", map[string]any{"level": value})
			result, marshalErr := json.Marshal(map[string]any{"configOptions": []any{}})
			return result, errors.Join(err, marshalErr)
		}
		return nil, fmt.Errorf("Pi RPC does not support config option %q", configID)
	case "session.cancel":
		_, err := s.callPi(ctx, "abort", nil)
		return json.RawMessage(`{}`), err
	default:
		return nil, fmt.Errorf("Pi RPC method %q is unavailable", method)
	}
}

type piState struct {
	SessionID   string `json:"sessionId"`
	SessionFile string `json:"sessionFile"`
	SessionName string `json:"sessionName"`
	CWD         string `json:"cwd"`
}

func (s *nativeSupervisor) piState(ctx context.Context) (piState, error) {
	reply, err := s.callPi(ctx, "get_state", nil)
	if err != nil {
		return piState{}, err
	}
	var state piState
	if err := json.Unmarshal(normalizeNativeResult(reply), &state); err != nil {
		return piState{}, fmt.Errorf("decode Pi state: %w", err)
	}
	if state.SessionID == "" {
		state.SessionID = state.SessionFile
	}
	s.mu.Lock()
	s.currentSession = state.SessionID
	s.mu.Unlock()
	return state, nil
}

func (s *nativeSupervisor) snapshot(ctx context.Context) (json.RawMessage, error) {
	state, err := s.piState(ctx)
	if err != nil {
		return nil, err
	}
	messagesReply, err := s.callPi(ctx, "get_messages", nil)
	if err != nil {
		return nil, err
	}
	var messages struct {
		Messages []any `json:"messages"`
	}
	_ = json.Unmarshal(normalizeNativeResult(messagesReply), &messages)
	result := map[string]any{
		"capabilities":  s.capabilitySnapshot(),
		"sessionId":     state.SessionID,
		"configOptions": []any{},
		"messages":      messages.Messages,
	}
	return json.Marshal(result)
}

func (s *nativeSupervisor) capabilitySnapshot() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]int, len(s.capabilities))
	for key, value := range s.capabilities {
		result[key] = value
	}
	return result
}

func promptPayload(params map[string]any) (string, []any) {
	blocks, _ := params["content"].([]any)
	texts := make([]string, 0, len(blocks))
	images := make([]any, 0)
	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := block["text"].(string); ok {
			texts = append(texts, text)
		}
		if image, ok := block["data"].(string); ok {
			images = append(images, map[string]any{"type": "image", "data": image, "mimeType": block["mimeType"]})
		}
	}
	if len(texts) == 0 {
		if text, ok := params["text"].(string); ok {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n"), images
}

func (s *nativeSupervisor) stopProcess() {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	interruptNativeProcess(s.cmd.Process)
	select {
	case <-s.childExited:
	case <-time.After(nativeAbortGrace):
		killNativeProcess(s.cmd.Process)
	}
}

func (s *nativeSupervisor) close(ctx context.Context) error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.stopping = true
		mode := s.mode
		s.mu.Unlock()
		if mode != nativeModeUnknown && s.cmd != nil {
			shutdownCtx, cancel := context.WithTimeout(ctx, nativeDrainTimeout)
			_, _ = s.callPiOrRaw(shutdownCtx, mode)
			cancel()
		}
		if s.stdin != nil {
			_ = s.stdin.Close()
		}
		select {
		case <-s.childExited:
		case <-ctx.Done():
			closeErr = ctx.Err()
			killNativeProcess(s.cmd.Process)
		case <-time.After(nativeDrainTimeout):
			killNativeProcess(s.cmd.Process)
		}
		select {
		case <-s.childExited:
		case <-time.After(nativeAbortGrace):
			closeErr = errors.Join(closeErr, errors.New("Pi child did not exit during drain"))
		}
		s.failNative(errors.New("Pi supervisor closed"))
	})
	return closeErr
}

func (s *nativeSupervisor) callPiOrRaw(ctx context.Context, mode nativeMode) (nativeReply, error) {
	if mode == nativeModePiRPC {
		return s.callPi(ctx, "shutdown", map[string]any{"reason": "service-drain"})
	}
	return s.callRaw(ctx, nativeRequest{Type: "request", Method: "runtime.shutdown", Params: map[string]any{"reason": "service-drain"}}, true)
}
