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
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/identifier"
)

// Browser transport bounds for LIMIT-01/X07-X08.
//
// Effective units and bounds at this level:
//   - Frame/record: serialized UTF-8 bytes per browser/host frame,
//     BrowserFrameMaxBytes (32 MiB). Not an allocation budget; decoded
//     structures are bounded separately below.
//   - Ordinary in-flight: BrowserOrdinaryInflightPerConnection (128 per
//     connection) and BrowserOrdinaryInflightPerEngine (256 per controller
//     process), additionally constrained by aggregate bytes.
//   - Aggregate: BrowserAggregateMaxBytes (64 MiB serialized request bytes)
//     per controller process across admitted ordinary in-flight browser
//     inputs. Per-socket output (32 MiB, see socket_output.go) and replay
//     retention (16 MiB per namespace, see replay.go) compose under the same
//     process ceiling; reservations are held while a request is dispatched
//     and released on every terminal path.
//   - Control lane: BrowserControlInflightMax (8) small operations per
//     engine, each at most BrowserControlFrameMaxBytes (64 KiB) within
//     BrowserControlReserveBytes (1 MiB) reserved storage. Only Stop/UI
//     cancellation (session.abort, session.uiCancel) use this lane; normal
//     traffic cannot consume it and it never bypasses auth/schema checks.
//   - Decoded structures: envelope keys, id/method lengths and ack/resume
//     fan-out bounded separately from serialized bytes; Unicode is counted
//     in UTF-8 bytes so escaping cannot bypass byte limits.
const (
	BrowserProtocolVersion = 88

	BrowserFrameMaxBytes     = 32 * 1024 * 1024
	BrowserAggregateMaxBytes = 64 * 1024 * 1024

	BrowserOrdinaryInflightPerConnection = 128
	BrowserOrdinaryInflightPerEngine     = 256

	BrowserControlInflightMax   = 8
	BrowserControlFrameMaxBytes = 64 * 1024
	BrowserControlReserveBytes  = 1024 * 1024

	BrowserEnvelopeMaxKeys   = 8
	BrowserIDMaxBytes        = 256
	BrowserMethodMaxBytes    = 128
	BrowserAckMaxIDs         = 512
	BrowserSessionIDMaxBytes = 4096
)

const (
	maxWSRequestBytes       = BrowserFrameMaxBytes
	maxConcurrentWSRequests = BrowserOrdinaryInflightPerEngine
)

var clientKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// IsBrowserControlMethod reports whether a browser method uses the reserved
// control lane. Only Stop (session.abort) and UI cancellation
// (session.uiCancel) are control; history/data work stays ordinary.
func IsBrowserControlMethod(method string) bool {
	return method == "session.abort" || method == "session.uiCancel"
}

// ValidateBrowserFrame bounds one serialized browser frame and its decoded
// envelope separately. Units are serialized UTF-8 bytes; Unicode/escaping
// cannot bypass the byte limits because len counts UTF-8 bytes.
func ValidateBrowserFrame(payload []byte) error {
	if len(payload) > BrowserFrameMaxBytes {
		return fmt.Errorf("browser frame exceeds the %d-byte limit", BrowserFrameMaxBytes)
	}
	if !utf8.Valid(payload) {
		return fmt.Errorf("browser frame is not valid UTF-8")
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return fmt.Errorf("browser frame is not a JSON object")
	}
	if len(envelope) > BrowserEnvelopeMaxKeys {
		return fmt.Errorf("browser envelope exceeds %d keys", BrowserEnvelopeMaxKeys)
	}
	if rawAck, ok := envelope["ack"]; ok {
		if len(envelope) != 1 {
			return fmt.Errorf("browser ack must be a single-key envelope")
		}
		var ids []string
		if err := json.Unmarshal(rawAck, &ids); err != nil {
			return fmt.Errorf("browser ack is invalid")
		}
		if len(ids) > BrowserAckMaxIDs {
			return fmt.Errorf("browser ack exceeds %d ids", BrowserAckMaxIDs)
		}
		for _, id := range ids {
			if len(id) == 0 || len(id) > BrowserIDMaxBytes {
				return fmt.Errorf("browser ack id out of bounds")
			}
		}
		return nil
	}
	if rawResume, ok := envelope["resume"]; ok {
		if len(envelope) != 1 {
			return fmt.Errorf("browser resume must be a single-key envelope")
		}
		var ids []string
		if err := json.Unmarshal(rawResume, &ids); err != nil {
			return fmt.Errorf("browser resume is invalid")
		}
		if len(ids) > BrowserAckMaxIDs {
			return fmt.Errorf("browser resume exceeds %d ids", BrowserAckMaxIDs)
		}
		for _, id := range ids {
			if len(id) == 0 || len(id) > BrowserIDMaxBytes {
				return fmt.Errorf("browser resume id out of bounds")
			}
		}
		return nil
	}
	var id, method string
	if err := json.Unmarshal(envelope["id"], &id); err != nil || id == "" || len(id) > BrowserIDMaxBytes {
		return fmt.Errorf("browser request id out of bounds")
	}
	if err := json.Unmarshal(envelope["method"], &method); err != nil || method == "" || len(method) > BrowserMethodMaxBytes {
		return fmt.Errorf("browser method out of bounds")
	}
	if rawSession, ok := envelope["sessionId"]; ok && len(rawSession) > 0 && string(rawSession) != "null" {
		if len(rawSession) > BrowserSessionIDMaxBytes {
			return fmt.Errorf("browser sessionId exceeds %d bytes", BrowserSessionIDMaxBytes)
		}
		var sessionID string
		if err := json.Unmarshal(rawSession, &sessionID); err == nil && len(sessionID) > BrowserSessionIDMaxBytes {
			return fmt.Errorf("browser sessionId exceeds %d bytes", BrowserSessionIDMaxBytes)
		}
	}
	return nil
}

// BrowserAdmissionLimits configures composed admission. Zero values select
// the contract defaults via DefaultBrowserAdmissionLimits.
type BrowserAdmissionLimits struct {
	OrdinaryPerEngine     int
	OrdinaryPerConnection int
	AggregateMaxBytes     int
	ControlMax            int
	ControlFrameMaxBytes  int
	ControlReserveBytes   int
}

// DefaultBrowserAdmissionLimits returns the LIMIT-01 contract defaults.
func DefaultBrowserAdmissionLimits() BrowserAdmissionLimits {
	return BrowserAdmissionLimits{
		OrdinaryPerEngine:     BrowserOrdinaryInflightPerEngine,
		OrdinaryPerConnection: BrowserOrdinaryInflightPerConnection,
		AggregateMaxBytes:     BrowserAggregateMaxBytes,
		ControlMax:            BrowserControlInflightMax,
		ControlFrameMaxBytes:  BrowserControlFrameMaxBytes,
		ControlReserveBytes:   BrowserControlReserveBytes,
	}
}

// BrowserAdmission tracks composed ordinary in-flight counts with
// aggregate-byte admission plus an independent control reserve. It is safe
// for concurrent use. Callers must Release every acquired reservation on
// every terminal path.
type BrowserAdmission struct {
	mu               sync.Mutex
	limits           BrowserAdmissionLimits
	perConn          map[string]int
	ordinaryInflight int
	controlInflight  int
	aggregate        *AggregateByteAdmission
}

// NewBrowserAdmission creates an admission tracker; zero limit fields select
// contract defaults.
func NewBrowserAdmission(limits BrowserAdmissionLimits) *BrowserAdmission {
	return newBrowserAdmission(limits, nil)
}

// NewBrowserAdmissionWithAggregate shares one process-wide byte budget with
// replay retention and socket output queues.
func NewBrowserAdmissionWithAggregate(limits BrowserAdmissionLimits, aggregate *AggregateByteAdmission) *BrowserAdmission {
	return newBrowserAdmission(limits, aggregate)
}

func newBrowserAdmission(limits BrowserAdmissionLimits, aggregate *AggregateByteAdmission) *BrowserAdmission {
	def := DefaultBrowserAdmissionLimits()
	if limits.OrdinaryPerEngine <= 0 {
		limits.OrdinaryPerEngine = def.OrdinaryPerEngine
	}
	if limits.OrdinaryPerConnection <= 0 {
		limits.OrdinaryPerConnection = def.OrdinaryPerConnection
	}
	if limits.AggregateMaxBytes <= 0 {
		limits.AggregateMaxBytes = def.AggregateMaxBytes
	}
	if limits.ControlMax <= 0 {
		limits.ControlMax = def.ControlMax
	}
	if limits.ControlFrameMaxBytes <= 0 {
		limits.ControlFrameMaxBytes = def.ControlFrameMaxBytes
	}
	if limits.ControlReserveBytes <= 0 {
		limits.ControlReserveBytes = def.ControlReserveBytes
	}
	if aggregate == nil {
		aggregate = NewAggregateByteAdmission(limits.AggregateMaxBytes, limits.ControlReserveBytes)
	}
	return &BrowserAdmission{limits: limits, perConn: make(map[string]int), aggregate: aggregate}
}

// TryAcquireOrdinary reserves one ordinary slot plus size serialized bytes.
// Size is counted in serialized UTF-8 bytes.
func (a *BrowserAdmission) TryAcquireOrdinary(clientKey string, size int) bool {
	if size < 0 || size > BrowserFrameMaxBytes {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ordinaryInflight >= a.limits.OrdinaryPerEngine {
		return false
	}
	if a.perConn[clientKey] >= a.limits.OrdinaryPerConnection {
		return false
	}
	if !a.aggregate.TryAcquireOrdinary(size) {
		return false
	}
	a.ordinaryInflight++
	a.perConn[clientKey]++
	return true
}

// ReleaseOrdinary releases a reservation acquired by TryAcquireOrdinary.
func (a *BrowserAdmission) ReleaseOrdinary(clientKey string, size int) {
	if size < 0 {
		size = 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ordinaryInflight > 0 {
		a.ordinaryInflight--
	}
	if count := a.perConn[clientKey]; count <= 1 {
		delete(a.perConn, clientKey)
	} else {
		a.perConn[clientKey] = count - 1
	}
	a.aggregate.ReleaseOrdinary(size)
}

// TryAcquireControl reserves one control slot plus size bytes from the
// independent control reserve. Ordinary load never consumes this reserve.
func (a *BrowserAdmission) TryAcquireControl(size int) bool {
	if size < 0 || size > a.limits.ControlFrameMaxBytes {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.controlInflight >= a.limits.ControlMax {
		return false
	}
	if !a.aggregate.TryAcquireControl(size) {
		return false
	}
	a.controlInflight++
	return true
}

// ReleaseControl releases a reservation acquired by TryAcquireControl.
func (a *BrowserAdmission) ReleaseControl(size int) {
	if size < 0 {
		size = 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.controlInflight > 0 {
		a.controlInflight--
	}
	a.aggregate.ReleaseControl(size)
}

// TryAcquire reserves admission by method kind.
func (a *BrowserAdmission) TryAcquire(clientKey, method string, size int) (isControl bool, ok bool) {
	if IsBrowserControlMethod(method) {
		return true, a.TryAcquireControl(size)
	}
	return false, a.TryAcquireOrdinary(clientKey, size)
}

// Release releases admission by method kind.
func (a *BrowserAdmission) Release(clientKey, method string, size int) {
	if IsBrowserControlMethod(method) {
		a.ReleaseControl(size)
		return
	}
	a.ReleaseOrdinary(clientKey, size)
}

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
	admission       *BrowserAdmission
	aggregate       *AggregateByteAdmission
	handlers        sync.WaitGroup
}

type browserSocket struct {
	connection *websocket.Conn
	expiresAt  time.Time
	output     *socketOutput
}

func NewWebSocketServer(handler Handler, welcome Welcome, config AuthConfig) (*WebSocketServer, error) {
	ctx, cancel := context.WithCancel(context.Background())
	aggregate := NewAggregateByteAdmission(BrowserAggregateMaxBytes, BrowserControlReserveBytes)
	server := &WebSocketServer{Handler: handler, Welcome: welcome, Auth: config, replay: NewReplayCacheWithAdmission(aggregate), ctx: ctx, cancel: cancel, sockets: make(map[string]browserSocket), reapTimers: make(map[string]*time.Timer), inflight: make(chan struct{}, maxConcurrentWSRequests), aggregate: aggregate}
	server.admission = NewBrowserAdmissionWithAggregate(BrowserAdmissionLimits{}, aggregate)
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
	// Keep WebSocket admission on the same independent authority policy as the
	// assembled HTTP handler. Origin is checked separately below; neither an
	// Origin nor an untrusted forwarded header can authorize a Host.
	if !s.Auth.IsAllowedAuthority(request) || !s.Auth.IsAllowedTransport(request) {
		http.Error(response, "forbidden", http.StatusForbidden)
		return
	}
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
	output := newSocketOutput(connection, s.aggregate)
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
	go output.run(connectionContext)

	if s.Welcome != nil {
		welcome, welcomeErr := s.Welcome(connectionContext)
		if welcomeErr != nil {
			connection.Close(websocket.StatusInternalError, "welcome unavailable")
			return
		}
		payload, marshalErr := json.Marshal(map[string]any{"channel": "server.welcome", "data": welcome})
		if marshalErr != nil {
			return
		}
		if err := output.enqueue(connectionContext, payload); err != nil {
			return
		}
	}
	if s.LoginSnapshot != nil {
		if snapshot := s.LoginSnapshot(clientKey); snapshot != nil {
			payload, marshalErr := json.Marshal(map[string]any{"channel": "provider.login", "data": snapshot})
			if marshalErr != nil {
				return
			}
			if err := output.enqueue(connectionContext, payload); err != nil {
				return
			}
		}
	}
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
	// Frame admission precedes decoded retention: oversize input closes
	// 1009-equivalent MessageTooBig without executing a partial request.
	if len(payload) > BrowserFrameMaxBytes {
		_ = output.connection.Close(websocket.StatusMessageTooBig, "message too large")
		return
	}
	// Bound decoded structures separately from serialized bytes. A decoded
	// violation drops the frame without dispatch; nothing partial executes.
	if err := ValidateBrowserFrame(payload); err != nil {
		return
	}
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
	serve := func() {
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
		_ = output.enqueueWithLane(ctx, response, IsBrowserControlMethod(method))
		if after != nil {
			after()
		}
	}
	// Reserve aggregate/count admission before dispatch and release on every
	// terminal path. Control uses its independent reserve so large
	// history/data work cannot consume Stop/UI-cancellation admission.
	if IsBrowserControlMethod(method) {
		if len(payload) > BrowserControlFrameMaxBytes {
			_ = output.connection.Close(websocket.StatusMessageTooBig, "control message too large")
			return
		}
		if !s.admission.TryAcquireControl(len(payload)) {
			_ = output.connection.Close(websocket.StatusTryAgainLater, "too many pending requests; reconnect to resume")
			return
		}
		s.mu.Lock()
		if s.ctx.Err() != nil {
			s.mu.Unlock()
			s.admission.ReleaseControl(len(payload))
			return
		}
		s.handlers.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.admission.ReleaseControl(len(payload))
			defer s.handlers.Done()
			serve()
		}()
		return
	}
	if !s.admission.TryAcquireOrdinary(clientKey, len(payload)) {
		_ = output.connection.Close(websocket.StatusTryAgainLater, "too many pending requests; reconnect to resume")
		return
	}
	select {
	case s.inflight <- struct{}{}:
	case <-ctx.Done():
		s.admission.ReleaseOrdinary(clientKey, len(payload))
		return
	default:
		s.admission.ReleaseOrdinary(clientKey, len(payload))
		_ = output.connection.Close(websocket.StatusTryAgainLater, "too many pending requests; reconnect to resume")
		return
	}
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		<-s.inflight
		s.admission.ReleaseOrdinary(clientKey, len(payload))
		return
	}
	s.handlers.Add(1)
	s.mu.Unlock()
	go func() {
		defer func() {
			<-s.inflight
			s.admission.ReleaseOrdinary(clientKey, len(payload))
			s.handlers.Done()
		}()
		serve()
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
