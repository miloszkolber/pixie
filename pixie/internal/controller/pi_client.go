package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/coder/websocket"
	piwire "github.com/miloszkolber/pixie/internal/piprotocol"
)

const (
	defaultPiURL         = "ws://127.0.0.1:3284/pi"
	defaultPiTimeout     = 30 * time.Second
	maxAgentNameRunes    = 128
	maxAgentVersionRunes = 64
	// Matches the Web UI socket ceiling and accommodates a maximally escaped
	// App resource plus its bounded JSON-RPC envelope.
	piReadLimit = 32 * 1024 * 1024
)

type PiEvents interface {
	SessionUpdate(context.Context, piwire.SessionNotification) error
	Extension(context.Context, string, json.RawMessage) error
}

type PiClient struct {
	Version        string
	Timeout        time.Duration
	Events         PiEvents
	scope          piConnectionScope
	profileChanged func(AgentProfile)

	mu         sync.Mutex
	connection *piConnection
	connecting *piConnectAttempt
	closed     bool
	generation uint64

	notificationMu     sync.Mutex
	notifiedGeneration uint64
}

// piConnectionScope is resolved once at construction and then used for
// both dialing and durable deletion identity. Keeping it private prevents a
// live connection and its recovery authority from observing different config.
type piConnectionScope struct {
	endpoint      string
	secret        string
	requireSecret bool
	requirePi     bool
}

type piConnectAttempt struct {
	done   chan struct{}
	cancel context.CancelFunc
	err    error // Written before done closes.
}

type piConnection struct {
	client  *piRPC
	stream  *websocket.Conn
	cancel  context.CancelFunc
	profile AgentProfile
}

func NewPiClient(url, secret, version string, events PiEvents) *PiClient {
	requireSecret := url == ""
	requirePi := url == ""
	if url == "" {
		url = defaultPiURL
	}
	return &PiClient{
		Version: version,
		Timeout: defaultPiTimeout,
		Events:  events,
		scope: piConnectionScope{
			endpoint:      url,
			secret:        secret,
			requireSecret: requireSecret,
			requirePi:     requirePi,
		},
	}
}

func (c *PiClient) Ready(ctx context.Context) (uint64, error) {
	generation, _, err := c.Profile(ctx)
	return generation, err
}

// Profile returns the connection generation and the capabilities negotiated
// by that same connection. The returned profile does not share mutable storage
// with the connection.
func (c *PiClient) Profile(ctx context.Context) (generation uint64, profile AgentProfile, err error) {
	connection, generation, err := c.ready(ctx)
	if err != nil {
		return 0, AgentProfile{}, err
	}
	select {
	case <-connection.client.Done():
		c.drop(connection)
		return 0, AgentProfile{}, fmt.Errorf("Pi host agent connection closed")
	default:
		return generation, cloneAgentProfile(connection.profile), nil
	}
}

func (c *PiClient) ready(ctx context.Context) (*piConnection, uint64, error) {
	waited := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil, 0, fmt.Errorf("Pi host client has been shut down")
		}
		if expected, attached := ctx.Value(connectionGenerationKey{}).(uint64); attached {
			if expected == 0 || c.connection == nil || c.generation != expected || isDone(c.connection) {
				c.mu.Unlock()
				return nil, 0, fmt.Errorf("Pi host connection changed; reload the chat before retrying")
			}
		}
		previous := c.connection
		if previous != nil && !isDone(previous) {
			generation := c.generation
			c.mu.Unlock()
			return previous, generation, nil
		}
		if waited {
			c.mu.Unlock()
			if previous != nil {
				c.drop(previous)
			}
			return nil, 0, fmt.Errorf("Pi host connection closed during setup")
		}
		c.connection = nil
		attempt := c.connecting
		if attempt == nil {
			// Setup belongs to the client, not the first readiness probe's deadline.
			bounded, cancel := c.bounded(context.Background())
			attempt = &piConnectAttempt{done: make(chan struct{}), cancel: cancel}
			c.connecting = attempt
			// Never reuse a cancelled attempt's notification generation.
			c.generation++
			go c.connect(bounded, attempt, c.generation)
		}
		c.mu.Unlock()
		if previous != nil {
			previous.close()
		}
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-attempt.done:
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
			if attempt.err != nil {
				return nil, 0, attempt.err
			}
			waited = true
		}
	}
}

func (c *PiClient) connect(ctx context.Context, attempt *piConnectAttempt, generation uint64) {
	defer attempt.cancel()
	connection, err := dialPi(ctx, c.scope.endpoint, c.scope.secret, c.Events, generation)
	if err == nil {
		// The SDK writes before waiting on the request context. Closing the stream
		// also interrupts a blocked initialize write on cancellation or shutdown.
		stop := context.AfterFunc(ctx, connection.close)
		connection.profile, err = c.initialize(ctx, connection)
		stop()
		if ctx.Err() != nil {
			err = ctx.Err()
		}
	}
	var profileChanged func(AgentProfile)
	var profile AgentProfile
	c.mu.Lock()
	if c.connecting == attempt {
		c.connecting = nil
		if err == nil {
			c.connection = connection
			profileChanged = c.profileChanged
			profile = cloneAgentProfile(connection.profile)
		}
		attempt.err = err
		close(attempt.done)
	} else if err == nil {
		// Reset or Close retired this attempt while its network work completed.
		err = context.Canceled
	}
	c.mu.Unlock()
	if err != nil && connection != nil {
		connection.close()
	}
	if err == nil && profileChanged != nil {
		c.publishProfile(generation, profileChanged, profile)
	}
}

func (c *PiClient) publishProfile(generation uint64, publish func(AgentProfile), profile AgentProfile) {
	c.notificationMu.Lock()
	defer c.notificationMu.Unlock()
	if generation <= c.notifiedGeneration {
		return
	}
	c.notifiedGeneration = generation
	publish(profile)
}

func (c *PiClient) initialize(ctx context.Context, connection *piConnection) (AgentProfile, error) {
	var response struct {
		ProtocolVersion int            `json:"protocolVersion"`
		RuntimeID       string         `json:"runtimeId"`
		Version         string         `json:"version"`
		Capabilities    map[string]int `json:"capabilities"`
	}
	if err := connection.client.call(ctx, "runtime.hello", map[string]any{"protocolVersion": 1}, &response); err != nil {
		return AgentProfile{}, err
	}
	if response.ProtocolVersion != 1 || response.RuntimeID == "" {
		return AgentProfile{}, fmt.Errorf("incompatible Pi host service")
	}
	caps := response.Capabilities
	p := AgentProfile{Name: "Pi", Version: response.Version, Pi: true, Compatible: true, MissingRequired: []string{}, Capabilities: caps, identity: "pi:" + response.RuntimeID}
	p.Operations = AgentOperations{DeleteSession: true, ForkSession: true, PromptImage: true, PromptEmbeddedContext: true, Steer: true, RenameSession: true, ArchiveSession: true, Administration: true, HTTPMCP: caps["mcp"] == 1}
	for _, capability := range []string{"sessions", "providers"} {
		if caps[capability] != 1 {
			p.Compatible = false
			p.MissingRequired = append(p.MissingRequired, capability)
		}
	}
	return p, nil
}

func cloneAgentProfile(profile AgentProfile) AgentProfile {
	caps := make(map[string]int, len(profile.Capabilities))
	for key, value := range profile.Capabilities {
		caps[key] = value
	}
	profile.Capabilities = caps
	profile.MissingRequired = append([]string{}, profile.MissingRequired...)
	return profile
}

func boundedAgentText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := make([]rune, 0, min(len(value), limit))
	for _, character := range value {
		if unicode.IsControl(character) || unicode.Is(unicode.Cf, character) {
			continue
		}
		runes = append(runes, character)
		if len(runes) == limit {
			break
		}
	}
	return string(runes)
}

func agentProfileIdentityDigest(name, version string, operations AgentOperations) string {
	encoded, _ := json.Marshal(struct {
		Name       string          `json:"name"`
		Version    string          `json:"version"`
		Operations AgentOperations `json:"operations"`
	}{Name: name, Version: version, Operations: operations})
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest)
}

// deletionAgentBinding binds a recoverable delete to both the logical agent
// and the exact endpoint configuration that authenticated it. Only the digest
// is persisted; endpoint credentials never leave PiClient.
func (c *PiClient) deletionAgentBinding(agentIdentity string) (string, error) {
	if !stableDeletionAgentIdentity(agentIdentity) {
		return "", fmt.Errorf("connected Pi host agent has no stable identity for recoverable session deletion")
	}
	encoded, _ := json.Marshal([]any{
		"pixie/session-deletion-agent-binding/v1",
		agentIdentity,
		c.scope.endpoint,
		c.scope.secret,
		c.scope.requirePi,
		c.scope.requireSecret,
	})
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest), nil
}

func (c *PiClient) CallPi(ctx context.Context, method string, params any) (json.RawMessage, error) {
	bounded, cancel := c.bounded(ctx)
	defer cancel()
	return c.CallPiUntilDone(bounded, method, params)
}

func (c *PiClient) CallPiUntilDone(ctx context.Context, method string, params any) (json.RawMessage, error) {
	connection, _, err := c.ready(ctx)
	if err != nil {
		return nil, err
	}
	if !connection.profile.Operations.Administration {
		return nil, unsupportedAgentCapability("Pi-specific operations")
	}
	result, err := connection.client.CallExtension(ctx, method, params)
	if err != nil {
		if isDone(connection) {
			c.drop(connection)
		}
		return nil, err
	}
	return result, nil
}

func (c *PiClient) Reset() {
	c.disconnect(false)
}

func (c *PiClient) ListSessions(ctx context.Context, request piwire.ListSessionsRequest) (piwire.ListSessionsResponse, error) {
	connection, _, err := c.ready(ctx)
	if err != nil {
		return piwire.ListSessionsResponse{}, err
	}
	if !hasAgentCapability(connection.profile, "session.list") {
		return piwire.ListSessionsResponse{}, unsupportedAgentCapability("session.list")
	}
	bounded, cancel := c.bounded(ctx)
	defer cancel()
	return connection.client.ListSessions(bounded, request)
}

func (c *PiClient) NewSession(ctx context.Context, request piwire.NewSessionRequest) (piwire.NewSessionResponse, error) {
	connection, _, err := c.ready(ctx)
	if err != nil {
		return piwire.NewSessionResponse{}, err
	}
	bounded, cancel := c.bounded(ctx)
	defer cancel()
	return connection.client.NewSession(bounded, request)
}

func (c *PiClient) LoadSession(ctx context.Context, request piwire.LoadSessionRequest) (piwire.LoadSessionResponse, error) {
	connection, _, err := c.ready(ctx)
	if err != nil {
		return piwire.LoadSessionResponse{}, err
	}
	if !hasAgentCapability(connection.profile, "session.load") {
		return piwire.LoadSessionResponse{}, unsupportedAgentCapability("session.load")
	}
	bounded, cancel := c.bounded(ctx)
	defer cancel()
	return connection.client.LoadSession(bounded, request)
}

func (c *PiClient) ForkSession(ctx context.Context, request piwire.UnstableForkSessionRequest) (piwire.UnstableForkSessionResponse, error) {
	connection, _, err := c.ready(ctx)
	if err != nil {
		return piwire.UnstableForkSessionResponse{}, err
	}
	if !connection.profile.Operations.ForkSession {
		return piwire.UnstableForkSessionResponse{}, unsupportedAgentCapability("session.fork")
	}
	bounded, cancel := c.bounded(ctx)
	defer cancel()
	return connection.client.UnstableForkSession(bounded, request)
}

func (c *PiClient) DeleteSession(ctx context.Context, sessionID string) error {
	connection, _, err := c.ready(ctx)
	if err != nil {
		return err
	}
	if !connection.profile.Operations.DeleteSession {
		return unsupportedAgentCapability("session.delete")
	}
	bounded, cancel := c.bounded(ctx)
	defer cancel()
	_, err = connection.client.CallExtension(bounded, "session.delete", map[string]any{"sessionId": sessionID})
	return err
}

func (c *PiClient) Prompt(ctx context.Context, request piwire.PromptRequest) (piwire.PromptResponse, error) {
	connection, _, err := c.ready(ctx)
	if err != nil {
		return piwire.PromptResponse{}, err
	}
	if promptContainsImage(request) && !connection.profile.Operations.PromptImage {
		return piwire.PromptResponse{}, unsupportedAgentCapability("image prompts")
	}
	if promptContainsEmbeddedContext(request) && !connection.profile.Operations.PromptEmbeddedContext {
		return piwire.PromptResponse{}, unsupportedAgentCapability("text resource prompts")
	}
	return connection.client.Prompt(ctx, request)
}

func hasAgentCapability(profile AgentProfile, method string) bool {
	for _, missing := range profile.MissingRequired {
		if missing == method {
			return false
		}
	}
	return true
}

func promptContainsImage(request piwire.PromptRequest) bool {
	for _, block := range request.Prompt {
		if block.Image != nil {
			return true
		}
	}
	return false
}

func promptContainsEmbeddedContext(request piwire.PromptRequest) bool {
	for _, block := range request.Prompt {
		if block.Resource != nil {
			return true
		}
	}
	return false
}

func unsupportedAgentCapability(capability string) error {
	return &codedError{code: "UNSUPPORTED_AGENT_CAPABILITY", message: "Connected agent does not support " + capability}
}

func (c *PiClient) Cancel(ctx context.Context, sessionID string) error {
	connection, _, err := c.ready(ctx)
	if err != nil {
		return err
	}
	bounded, cancel := c.bounded(ctx)
	defer cancel()
	return connection.client.call(bounded, "session.cancel", map[string]any{"sessionId": sessionID}, nil)
}

func (c *PiClient) SetConfig(ctx context.Context, request piwire.SetSessionConfigOptionRequest) (piwire.SetSessionConfigOptionResponse, error) {
	connection, _, err := c.ready(ctx)
	if err != nil {
		return piwire.SetSessionConfigOptionResponse{}, err
	}
	bounded, cancel := c.bounded(ctx)
	defer cancel()
	return connection.client.SetSessionConfigOption(bounded, request)
}

func (c *PiClient) SetMode(ctx context.Context, request piwire.SetSessionModeRequest) error {
	connection, _, err := c.ready(ctx)
	if err != nil {
		return err
	}
	bounded, cancel := c.bounded(ctx)
	defer cancel()
	_, err = connection.client.SetSessionMode(bounded, request)
	return err
}

func (c *PiClient) Close() {
	c.disconnect(true)
}

func (c *PiClient) disconnect(shutdown bool) {
	c.mu.Lock()
	c.closed = c.closed || shutdown
	connection := c.connection
	c.connection = nil
	attempt := c.connecting
	c.connecting = nil
	if attempt != nil {
		attempt.err = fmt.Errorf("Pi host connection was reset")
		if c.closed {
			attempt.err = fmt.Errorf("Pi host client has been shut down")
		}
		close(attempt.done)
	}
	c.mu.Unlock()
	if attempt != nil {
		attempt.cancel()
	}
	if connection != nil {
		connection.close()
	}
}

func (c *PiClient) drop(connection *piConnection) {
	c.mu.Lock()
	if c.connection == connection {
		c.connection = nil
	}
	c.mu.Unlock()
	connection.close()
}

func (c *piConnection) close() {
	c.cancel()
	c.stream.CloseNow()
}

func (c *PiClient) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultPiTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func isDone(connection *piConnection) bool {
	select {
	case <-connection.client.Done():
		return true
	default:
		return false
	}
}

func dialPi(ctx context.Context, url, secret string, events PiEvents, generation uint64) (*piConnection, error) {
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+secret)
	socket, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return nil, err
	}
	socket.SetReadLimit(piReadLimit)
	live, cancel := context.WithCancel(context.Background())
	rpc := &piRPC{socket: socket, ctx: live, cancel: cancel, pending: map[uint64]piPending{}, done: make(chan struct{}), events: events, generation: generation}
	go rpc.read()
	return &piConnection{client: rpc, stream: socket, cancel: cancel}, nil
}

type piReply struct {
	ID     uint64               `json:"id"`
	Result json.RawMessage      `json:"result"`
	Error  *piwire.RequestError `json:"error"`
	Method string               `json:"method"`
	Params json.RawMessage      `json:"params"`
}
type piPending struct {
	reply     chan piReply
	method    string
	sessionID string
}

type piRPC struct {
	socket     *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	writeMu    sync.Mutex
	serial     uint64
	pending    map[uint64]piPending
	done       chan struct{}
	events     PiEvents
	generation uint64
}

func (r *piRPC) Done() <-chan struct{} { return r.done }
func (r *piRPC) read() {
	defer close(r.done)
	defer r.cancel()
	defer r.socket.CloseNow()
	for {
		kind, raw, err := r.socket.Read(r.ctx)
		if err != nil {
			return
		}
		if kind != websocket.MessageText {
			return
		}
		var reply piReply
		if json.Unmarshal(raw, &reply) != nil {
			return
		}
		if reply.ID != 0 {
			r.mu.Lock()
			pending, exists := r.pending[reply.ID]
			delete(r.pending, reply.ID)
			r.mu.Unlock()
			if exists {
				if reply.Error == nil && r.events != nil && (pending.method == "session.load" || pending.method == "session.create" || pending.method == "session.fork") {
					ctx := context.WithValue(r.ctx, connectionGenerationKey{}, r.generation)
					ctx = context.WithValue(ctx, recognizedPiConnectionKey{}, true)
					var snapshot piwire.NewSessionResponse
					if err := json.Unmarshal(reply.Result, &snapshot); err != nil {
						return
					}
					id := snapshot.SessionId
					if id == "" {
						id = pending.sessionID
					}
					for _, message := range snapshot.Messages {
						raw, _ := json.Marshal(map[string]any{"sessionId": id, "event": map[string]any{"type": "replay_message", "message": message}})
						if projectPiEvent(ctx, r.events, raw) != nil {
							return
						}
					}
					for _, update := range []map[string]any{
						{"sessionUpdate": "available_commands_update", "availableCommands": snapshot.Commands},
						{"sessionUpdate": "session_info_update", "_meta": map[string]any{"pi": map[string]any{"activeRunId": snapshot.RunID}}},
					} {
						if r.events.SessionUpdate(ctx, piwire.SessionNotification{SessionId: id, Update: update}) != nil {
							return
						}
					}
				}
				pending.reply <- reply
			}
			continue
		}
		if r.events == nil {
			continue
		}
		ctx := context.WithValue(r.ctx, connectionGenerationKey{}, r.generation)
		ctx = context.WithValue(ctx, recognizedPiConnectionKey{}, true)
		if reply.Method == "session.history" {
			var history struct {
				SessionID string           `json:"sessionId"`
				Messages  []map[string]any `json:"messages"`
			}
			if json.Unmarshal(reply.Params, &history) != nil {
				return
			}
			for _, message := range history.Messages {
				raw, _ := json.Marshal(map[string]any{"sessionId": history.SessionID, "event": map[string]any{"type": "replay_message", "message": message}})
				if projectPiEvent(ctx, r.events, raw) != nil {
					return
				}
			}
			continue
		}
		if reply.Method == "session.event" {
			if err := projectPiEvent(ctx, r.events, reply.Params); err != nil {
				return
			}
		} else if reply.Method == "session.update" {
			var n piwire.SessionNotification
			if json.Unmarshal(reply.Params, &n) != nil {
				return
			}
			if r.events.SessionUpdate(ctx, n) != nil {
				return
			}
		} else if err := r.events.Extension(ctx, reply.Method, reply.Params); err != nil {
			return
		}
	}
}
func (r *piRPC) call(ctx context.Context, method string, params, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	r.serial++
	id := r.serial
	ch := make(chan piReply, 1)
	var sessionID string
	if value, err := json.Marshal(params); err == nil {
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(value, &p)
		sessionID = p.SessionID
	}
	r.pending[id] = piPending{reply: ch, method: method, sessionID: sessionID}
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.pending, id); r.mu.Unlock() }()
	raw, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	r.writeMu.Lock()
	err = r.socket.Write(ctx, websocket.MessageText, raw)
	r.writeMu.Unlock()
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return fmt.Errorf("Pi host connection closed")
	case reply := <-ch:
		if reply.Error != nil {
			return reply.Error
		}
		if out != nil {
			return json.Unmarshal(reply.Result, out)
		}
		return nil
	}
}
func (r *piRPC) CallExtension(ctx context.Context, method string, params any) (json.RawMessage, error) {
	var result json.RawMessage
	err := r.call(ctx, method, params, &result)
	return result, err
}
func (r *piRPC) ListSessions(ctx context.Context, req piwire.ListSessionsRequest) (res piwire.ListSessionsResponse, err error) {
	err = r.call(ctx, "session.list", req, &res)
	return
}
func (r *piRPC) NewSession(ctx context.Context, req piwire.NewSessionRequest) (res piwire.NewSessionResponse, err error) {
	err = r.call(ctx, "session.create", req, &res)
	return
}
func (r *piRPC) LoadSession(ctx context.Context, req piwire.LoadSessionRequest) (res piwire.LoadSessionResponse, err error) {
	err = r.call(ctx, "session.load", req, &res)

	return
}
func (r *piRPC) UnstableForkSession(ctx context.Context, req piwire.UnstableForkSessionRequest) (res piwire.UnstableForkSessionResponse, err error) {
	err = r.call(ctx, "session.fork", req, &res)
	return
}
func (r *piRPC) Prompt(ctx context.Context, req piwire.PromptRequest) (res piwire.PromptResponse, err error) {
	err = r.call(ctx, "session.prompt", req, &res)
	return
}
func (r *piRPC) SetSessionConfigOption(ctx context.Context, req piwire.SetSessionConfigOptionRequest) (res piwire.SetSessionConfigOptionResponse, err error) {
	err = r.call(ctx, "session.configure", req, &res)
	return
}
func (r *piRPC) SetSessionMode(ctx context.Context, req piwire.SetSessionModeRequest) (res map[string]any, err error) {
	err = r.call(ctx, "session.setMode", req, &res)
	return
}
