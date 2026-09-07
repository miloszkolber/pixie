package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/identifier"
)

const (
	BrowserProtocolVersion  = 85
	maxWSRequestBytes       = 32 * 1024 * 1024
	maxConcurrentWSRequests = 256
)

var clientKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type Welcome func(context.Context) (any, error)

type WebSocketServer struct {
	Handler       Handler
	Welcome       Welcome
	LoginSnapshot func(string) any
	// Called after disconnected requests settle, before a replacement is admitted.
	ClientReaped func(string)
	Auth         AuthConfig
	auth         *Auth
	replay       *ReplayCache
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	// Cleanup may take session locks. Keep it outside mu (session events publish
	// through mu), while serializing retirement with replacement connections.
	clientLifecycle sync.Mutex
	sockets         map[string]browserSocket
	reapTimers      map[string]*time.Timer
	inflight        chan struct{}
	handlers        sync.WaitGroup
}

type browserSocket struct {
	connection *websocket.Conn
	expiresAt  time.Time
	output     *socketOutput
}

func NewWebSocketServer(handler Handler, welcome Welcome, config AuthConfig) (*WebSocketServer, error) {
	ctx, cancel := context.WithCancel(context.Background())
	server := &WebSocketServer{Handler: handler, Welcome: welcome, Auth: config, replay: NewReplayCache(), ctx: ctx, cancel: cancel, sockets: make(map[string]browserSocket), reapTimers: make(map[string]*time.Timer), inflight: make(chan struct{}, maxConcurrentWSRequests)}
	if config.Enabled {
		auth, err := NewAuth(config.ControllerToken)
		if err != nil {
			cancel()
			return nil, err
		}
		server.auth = auth
	}
	return server, nil
}

func (s *WebSocketServer) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.Auth.IsExpectedOrigin(request) {
		http.Error(response, "forbidden", http.StatusForbidden)
		return
	}
	var expiresAt time.Time
	if s.Auth.Enabled {
		var ok bool
		if expiresAt, ok = s.auth.SessionExpiresAt(ReadAuthCookie(request)); !ok {
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	connection, err := websocket.Accept(response, request, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
		// IsExpectedOrigin already enforced the exact public origin above. The
		// library's Host-based check would reject trusted reverse-proxy origins.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	connection.SetReadLimit(maxWSRequestBytes)
	defer connection.CloseNow()
	clientKey := request.URL.Query().Get("client")
	if !clientKeyPattern.MatchString(clientKey) {
		clientKey = "anon-" + identifier.New()
	}
	output := newSocketOutput(connection)
	defer output.stop()
	if !s.replace(clientKey, browserSocket{connection: connection, expiresAt: expiresAt, output: output}) {
		return
	}
	defer s.remove(clientKey, connection)
	connectionContext, disconnect := context.WithCancel(request.Context())
	defer disconnect()
	if !expiresAt.IsZero() {
		var cancel context.CancelFunc
		connectionContext, cancel = context.WithDeadline(connectionContext, expiresAt)
		defer cancel()
	}

	if s.Welcome != nil {
		welcome, welcomeErr := s.Welcome(connectionContext)
		if welcomeErr != nil {
			connection.Close(websocket.StatusInternalError, "welcome unavailable")
			return
		}
		if err := writeJSON(connectionContext, connection, map[string]any{"channel": "server.welcome", "data": welcome}); err != nil {
			return
		}
	}
	if s.LoginSnapshot != nil {
		if snapshot := s.LoginSnapshot(clientKey); snapshot != nil {
			if err := writeJSON(connectionContext, connection, map[string]any{"channel": "provider.login", "data": snapshot}); err != nil {
				return
			}
		}
	}
	go output.run(connectionContext)
	for {
		messageType, payload, readErr := connection.Read(connectionContext)
		if readErr != nil {
			if !expiresAt.IsZero() && !time.Now().Before(expiresAt) {
				_ = connection.Close(websocket.StatusPolicyViolation, "authentication expired")
			}
			return
		}
		if messageType != websocket.MessageText {
			continue
		}
		if len(payload) > maxWSRequestBytes {
			connection.Close(websocket.StatusMessageTooBig, "message too large")
			return
		}
		s.handle(s.ctx, output, clientKey, payload)
	}
}

func (s *WebSocketServer) handle(ctx context.Context, output *socketOutput, clientKey string, payload []byte) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(payload, &envelope) != nil {
		return
	}
	if rawAck, ok := envelope["ack"]; ok {
		if len(envelope) != 1 {
			return
		}
		var ids []string
		if json.Unmarshal(rawAck, &ids) == nil {
			s.replay.Acknowledge(clientKey, ids)
			output.replay.Acknowledge(clientKey, ids)
		}
		return
	}
	if rawResume, ok := envelope["resume"]; ok {
		if len(envelope) != 1 {
			return
		}
		var ids []string
		if json.Unmarshal(rawResume, &ids) == nil {
			s.replay.Retain(clientKey, ids)
			output.replay.Retain(clientKey, ids)
		}
		return
	}
	var id, method string
	if json.Unmarshal(envelope["id"], &id) != nil || json.Unmarshal(envelope["method"], &method) != nil || id == "" || method == "" {
		return
	}
	params := envelope["params"]
	if len(params) == 0 {
		params = json.RawMessage("null")
	}
	fingerprint := requestFingerprint(method, params, envelope["sessionId"])
	requestKey := sha256.Sum256([]byte(clientKey + "\x00" + id))
	requestContext := context.WithValue(ctx, queueRequestIdentityContextKey{}, queueRequestIdentity{Key: hex.EncodeToString(requestKey[:]), Fingerprint: fingerprint})
	select {
	case s.inflight <- struct{}{}:
	case <-ctx.Done():
		return
	default:
		_ = output.connection.Close(websocket.StatusTryAgainLater, "too many pending requests; reconnect to resume")
		return
	}
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		<-s.inflight
		return
	}
	s.handlers.Add(1)
	s.mu.Unlock()
	go func() {
		defer func() {
			<-s.inflight
			s.handlers.Done()
		}()
		var after func()
		execute := func() ([]byte, error) {
			result, handleErr := s.Handler.Handle(requestContext, method, params, clientKey)
			if handleErr != nil {
				failure := map[string]any{"id": id, "ok": false, "error": handleErr.Error()}
				var coded interface{ ErrorCode() string }
				if errors.As(handleErr, &coded) {
					failure["errorCode"] = coded.ErrorCode()
				}
				return json.Marshal(failure)
			}
			if deferred, ok := result.(deferredResponse); ok {
				result, after = deferred.result, deferred.after
			}
			return json.Marshal(struct {
				ID     string `json:"id"`
				OK     bool   `json:"ok"`
				Result any    `json:"result"`
			}{id, true, result})
		}
		var response []byte
		var err error
		if method == "session.getMessages" {
			// Coalesce retries on this socket, but establish a fresh response boundary
			// after replacement. Replaying an older socket's snapshot could otherwise
			// place stale state after a newer event on the replacement connection.
			response, err = output.replay.Run(ctx, clientKey, id, fingerprint, execute)
		} else {
			response, err = s.replay.Run(ctx, clientKey, id, fingerprint, execute)
		}
		if err != nil {
			response, _ = json.Marshal(map[string]any{"id": id, "ok": false, "error": err.Error()})
		}
		_ = output.enqueue(ctx, response)
		if after != nil {
			after()
		}
	}()
}

func (s *WebSocketServer) PublishToClient(ctx context.Context, clientKey, channel string, data any) error {
	s.mu.Lock()
	socket := s.sockets[clientKey]
	s.mu.Unlock()
	if socket.connection == nil {
		return nil
	}
	if !socket.expiresAt.IsZero() && !time.Now().Before(socket.expiresAt) {
		return nil
	}
	payload, err := json.Marshal(map[string]any{"channel": channel, "data": data})
	if err != nil {
		return err
	}
	return socket.output.enqueue(ctx, payload)
}

func requestFingerprint(method string, params, sessionID json.RawMessage) string {
	var normalizedParams, normalizedSession any
	_ = json.Unmarshal(params, &normalizedParams)
	if len(sessionID) > 0 {
		_ = json.Unmarshal(sessionID, &normalizedSession)
	}
	payload, _ := json.Marshal([]any{method, normalizedParams, normalizedSession})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func writeJSON(ctx context.Context, connection *websocket.Conn, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return connection.Write(bounded, websocket.MessageText, payload)
}

func (s *WebSocketServer) replace(clientKey string, socket browserSocket) bool {
	s.clientLifecycle.Lock()
	defer s.clientLifecycle.Unlock()
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return false
	}
	previous := s.sockets[clientKey]
	s.sockets[clientKey] = socket
	if timer := s.reapTimers[clientKey]; timer != nil {
		timer.Stop()
		delete(s.reapTimers, clientKey)
	}
	s.mu.Unlock()
	if previous.connection != nil && previous.connection != socket.connection {
		previous.output.stop()
		previous.connection.CloseNow()
	}
	return true
}

func (s *WebSocketServer) remove(clientKey string, connection *websocket.Conn) {
	s.mu.Lock()
	if s.sockets[clientKey].connection == connection {
		delete(s.sockets, clientKey)
		if s.ctx.Err() == nil {
			s.armReapLocked(clientKey)
		}
	}
	s.mu.Unlock()
}

func (s *WebSocketServer) armReapLocked(clientKey string) {
	var timer *time.Timer
	timer = time.AfterFunc(time.Minute, func() { s.reapClient(clientKey, timer) })
	s.reapTimers[clientKey] = timer
}

func (s *WebSocketServer) reapClient(clientKey string, timer *time.Timer) {
	s.clientLifecycle.Lock()
	defer s.clientLifecycle.Unlock()
	s.mu.Lock()
	// A stopped callback may already be waiting for mu while a newer connection
	// disconnects. It must not consume that connection's grace period.
	if s.reapTimers[clientKey] != timer {
		s.mu.Unlock()
		return
	}
	delete(s.reapTimers, clientKey)
	if s.sockets[clientKey].connection != nil || s.ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	if !s.replay.ClearClient(clientKey) {
		s.armReapLocked(clientKey)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	if s.ClientReaped != nil {
		s.ClientReaped(clientKey)
	}
}

func (s *WebSocketServer) Publish(ctx context.Context, channel string, data any) error {
	payload, err := json.Marshal(map[string]any{"channel": channel, "data": data})
	if err != nil {
		return err
	}
	s.mu.Lock()
	sockets := make([]browserSocket, 0, len(s.sockets))
	for _, socket := range s.sockets {
		sockets = append(sockets, socket)
	}
	s.mu.Unlock()
	var firstErr error
	for _, socket := range sockets {
		if !socket.expiresAt.IsZero() && !time.Now().Before(socket.expiresAt) {
			continue
		}
		err := socket.output.enqueue(ctx, payload)
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("publish %s: %w", channel, err)
		}
	}
	return firstErr
}

func (s *WebSocketServer) Close(ctx context.Context) {
	s.cancel()
	s.mu.Lock()
	sockets := s.sockets
	s.sockets = make(map[string]browserSocket)
	for _, timer := range s.reapTimers {
		timer.Stop()
	}
	s.reapTimers = make(map[string]*time.Timer)
	s.mu.Unlock()
	for _, socket := range sockets {
		_ = socket.connection.CloseNow()
	}
	settled := make(chan struct{})
	go func() {
		s.handlers.Wait()
		close(settled)
	}()
	select {
	case <-settled:
	case <-ctx.Done():
	}
}
