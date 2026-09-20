package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/identifier"
	piwire "github.com/miloszkolber/pixie/piprotocol"
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
	// drainAdmissionGrace is reserved for a future settle window before a drain
	// refuses new work. NewAdmissionGate currently ignores it: BeginDrain
	// refuses immediately and only already-admitted work is retained.
	drainAdmissionGrace = 250 * time.Millisecond

	maxWSRequestBytes       = BrowserFrameMaxBytes
	maxConcurrentWSRequests = BrowserOrdinaryInflightPerEngine
)

// BrowserMaxConnections bounds every tracked browser identity: an active
// socket and a disconnected client still inside its replay-reap grace period
// both count. Reconnect load above this bound is shed before it can grow
// sockets, per-socket writers, replay namespaces or reap timers without limit.
const BrowserMaxConnections = 64

const (
	// reconnectWindow and reconnectMaxAttempts throttle a single client
	// identity's upgrade rate; reconnectBaseBackoff..reconnectMaxBackoff cap
	// the penalty. Only upgrades beyond the burst are delayed, so a normal
	// network drop or page reload reconnects immediately.
	reconnectWindow      = 10 * time.Second
	reconnectMaxAttempts = 6
	reconnectBaseBackoff = time.Second
	reconnectMaxBackoff  = 30 * time.Second
)

const (
	// socketHeartbeatInterval pads idle connections so a dead intermediary is
	// detected and the browser's resume path runs, and
	// socketEventBackpressure caps the number of queued event frames per
	// socket independently of the aggregate byte budget.
	socketHeartbeatInterval = 25 * time.Second
)

// socketEventBackpressure is a variable so tests can exercise the cap without
// generating hundreds of frames. Production always uses the constant.
var socketEventBackpressure = 256

var clientKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// browserErrorMaxBytes bounds one browser-visible failure message. Native error
// text is never a shareable guarantee, so the message is both mapped and cut.
const browserErrorMaxBytes = 512

// browserErrorMessage normalizes a handler error for a browser reply. A host
// or SDK error is never echoed: its typed host code maps to a fixed message. A
// controller-owned error keeps its redacted, bounded message so existing typed
// codes remain actionable. The raw cause is logged separately and never
// returned.
func browserErrorMessage(err error) string {
	if code, ok := hostErrorCode(err); ok {
		return piwire.BrowserErrorMessageForHostCode(code)
	}
	message := diagnostics.SanitizeDiagnosticText(err.Error(), browserErrorMaxBytes)
	if message == "" {
		return "Request failed."
	}
	return message
}

// hostErrorCode extracts the typed code from a host error anywhere in the chain
// so diagnostics retain the failure class even though the browser message is
// normalized.
func hostErrorCode(err error) (int, bool) {
	var hostErr *piwire.RequestError
	if errors.As(err, &hostErr) {
		return hostErr.Code, true
	}
	return 0, false
}

// reconnectState is the process-local reconnect budget for one client key. It
// is only recorded while the identity is tracked, so the map stays bounded by
// BrowserMaxConnections.
type reconnectState struct {
	windowStart  time.Time
	attempts     int
	blockedUntil time.Time
	backoff      time.Duration
}

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
	if rawResync, ok := envelope["resync"]; ok {
		if len(envelope) != 1 {
			return fmt.Errorf("browser resync must be a single-key envelope")
		}
		var requested bool
		if err := json.Unmarshal(rawResync, &requested); err != nil || !requested {
			return fmt.Errorf("browser resync is invalid")
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
	// gate is the AUX-19 drain admission gate. NewWebSocketServer installs one;
	// a directly constructed server with a nil gate admits every method.
	gate   *AdmissionGate
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	// Cleanup may take session locks. Keep it outside mu (session events publish
	// through mu), while serializing retirement with replacement connections.
	clientLifecycle sync.Mutex
	sockets         map[string]browserSocket
	reapTimers      map[string]*time.Timer
	reconnects      map[string]reconnectState
	inflight        chan struct{}
	admission       *BrowserAdmission
	aggregate       *AggregateByteAdmission
	handlers        sync.WaitGroup
	// welcomeMu guards the cached login snapshot so a client resync can be
	// served without rebuilding the whole welcome projection.
	welcomeMu   sync.Mutex
	welcomeData []byte
}

type browserSocket struct {
	connection *websocket.Conn
	expiresAt  time.Time
	output     *socketOutput
	// stream is this identity's framing chain. It survives reconnects so a
	// replacement socket continues the same sequence instead of restarting it.
	stream *streamTracker
}

func NewWebSocketServer(handler Handler, welcome Welcome, config AuthConfig) (*WebSocketServer, error) {
	ctx, cancel := context.WithCancel(context.Background())
	aggregate := NewAggregateByteAdmission(BrowserAggregateMaxBytes, BrowserControlReserveBytes)
	server := &WebSocketServer{
		Handler: handler, Welcome: welcome, Auth: config,
		replay: NewReplayCacheWithAdmission(aggregate), ctx: ctx, cancel: cancel,
		gate:       NewAdmissionGate(drainAdmissionGrace),
		sockets:    make(map[string]browserSocket),
		reapTimers: make(map[string]*time.Timer),
		reconnects: make(map[string]reconnectState),
		inflight:   make(chan struct{}, maxConcurrentWSRequests),
		aggregate:  aggregate,
	}
	server.admission = NewBrowserAdmissionWithAggregate(BrowserAdmissionLimits{}, aggregate)
	if config.Enabled {
		// AUX-20 wiring: build the auth from the full hardening config and the
		// data directory so the signing secret and stored scrypt credential are
		// loaded (or generated) like an SSH host key.
		auth, err := NewAuthFromConfig(config, config.DataDir)
		if err != nil {
			cancel()
			return nil, err
		}
		server.auth = auth
	}
	return server, nil
}

// BeginDrain requests the AUX-19 quiesce. New runnable work is refused
// immediately; already-admitted work keeps running, and a browser prompt holds
// its admission until its asynchronous run settles (or the caller's bounded
// WaitForDrain context expires).
func (s *WebSocketServer) BeginDrain() {
	if s != nil {
		s.gate.BeginDrain()
	}
}

// WaitForDrain blocks until admitted runnable work has settled or ctx ends.
func (s *WebSocketServer) WaitForDrain(ctx context.Context) {
	if s != nil {
		s.gate.WaitForDrain(ctx)
	}
}

// Quiescing reports whether the gate is refusing new runnable work.
func (s *WebSocketServer) Quiescing() bool {
	return s != nil && s.gate.Quiescing()
}

// previousStream returns the framing chain of an already-tracked identity so a
// replacement socket continues its sequence instead of restarting it.
func (s *WebSocketServer) previousStream(clientKey string) *streamTracker {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sockets[clientKey].stream
}

// welcomeFrame builds and caches the snapshot-first welcome payload. The
// projection is identity-independent, so rebuilds are cheap and pinned once.
func (s *WebSocketServer) welcomeFrame(ctx context.Context) ([]byte, error) {
	s.welcomeMu.Lock()
	cached := s.welcomeData
	s.welcomeMu.Unlock()
	if cached != nil {
		return cached, nil
	}
	if s.Welcome == nil {
		return nil, nil
	}
	welcome, err := s.Welcome(ctx)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{"channel": "server.welcome", "data": welcome})
	if err != nil {
		return nil, err
	}
	s.welcomeMu.Lock()
	s.welcomeData = payload
	s.welcomeMu.Unlock()
	return payload, nil
}

// sendLoginSnapshot delivers the per-client provider login projection. When the
// server has a cached welcome it is rebuilt on the next connect; this frame is
// not part of the append chain.
func (s *WebSocketServer) sendLoginSnapshot(ctx context.Context, output *socketOutput, clientKey string) error {
	if s.LoginSnapshot == nil {
		return nil
	}
	snapshot := s.LoginSnapshot(clientKey)
	if snapshot == nil {
		return nil
	}
	payload, err := json.Marshal(map[string]any{"channel": "provider.login", "data": snapshot})
	if err != nil {
		return err
	}
	return output.enqueue(ctx, payload)
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
	// Shed excess load before the upgrade handshake. An already-tracked
	// identity is always allowed through so a reconnect at capacity is not
	// mistaken for a new storm; replace performs the authoritative check under
	// lock.
	clientKey := request.URL.Query().Get("client")
	if !clientKeyPattern.MatchString(clientKey) {
		clientKey = "anon-" + identifier.New()
	}
	if !s.hasConnectionCapacity(clientKey) {
		http.Error(response, "connection capacity reached", http.StatusServiceUnavailable)
		return
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
	output := newSocketOutput(connection, s.aggregate, clientKey)
	defer output.stop()
	// Continue this identity's sequence/revision chain across a reconnect.
	stream := s.previousStream(clientKey)
	if stream == nil {
		stream = newStreamTracker()
	}
	if admitted, reason := s.replace(clientKey, browserSocket{connection: connection, expiresAt: expiresAt, output: output, stream: stream}); !admitted {
		if reason != "" {
			_ = connection.Close(websocket.StatusTryAgainLater, reason)
		}
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
	go output.runHeartbeat(connectionContext)

	// Snapshot-first ordering: the welcome projection is the first frame on a
	// fresh socket, before any sequenced channel event can arrive.
	if s.Welcome != nil {
		payload, err := s.welcomeFrame(connectionContext)
		if err != nil {
			connection.Close(websocket.StatusInternalError, "welcome unavailable")
			return
		}
		if err := output.enqueue(connectionContext, payload); err != nil {
			return
		}
	}
	s.sendLoginSnapshot(connectionContext, output, clientKey)
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
	if rawResync, ok := envelope["resync"]; ok {
		// AUX-16 client resync: a client that detected a sequence gap or a
		// broken revision chain asks for a fresh snapshot instead of diverging.
		var requested bool
		if len(envelope) == 1 && json.Unmarshal(rawResync, &requested) == nil && requested {
			// Restart this identity's chain so the next frames are full
			// snapshots, then resend the snapshot-first projection.
			if stream := s.previousStream(clientKey); stream != nil {
				stream.reset()
			}
			payload, err := s.welcomeFrame(ctx)
			if err == nil && payload != nil {
				_ = output.enqueue(ctx, payload)
			}
			_ = s.sendLoginSnapshot(ctx, output, clientKey)
		}
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
	// AUX-19 drain admission: a run-creating method is refused up front while
	// the controller is quiescing, before any replay retention or dispatch. It
	// is a deliberate state, so the browser receives a typed quiescing error it
	// can surface as status instead of a generic failure.
	if !s.gate.TryAdmit(method) {
		denied, _ := json.Marshal(map[string]any{
			"id": id, "ok": false, "error": (ErrControllerQuiescing{}).Error(), "errorCode": (ErrControllerQuiescing{}).ErrorCode(),
		})
		_ = output.enqueue(ctx, denied)
		return
	}
	// AUX-19 run handoff: session.prompt starts an asynchronous agent run.
	// Carry a run admission through the request so the run adopts the drain
	// admission this dispatch already holds and settles it when the native
	// prompt ends. If no run starts, the transport settles it once dispatch
	// returns. Other run-creating methods are settled by the follow-up
	// dispatcher or start no run at all.
	var promptAdmission *runAdmission
	if method == "session.prompt" {
		promptAdmission = newRunAdmission(func() { s.gate.Release(method) })
	}
	fingerprint := requestFingerprint(method, params, envelope["sessionId"])
	requestKey := sha256.Sum256([]byte(clientKey + "\x00" + id))
	requestContext := context.WithValue(ctx, queueRequestIdentityContextKey{}, queueRequestIdentity{Key: hex.EncodeToString(requestKey[:]), Fingerprint: fingerprint})
	requestContext = withRunAdmission(requestContext, promptAdmission)
	settleAdmission := func() {
		if promptAdmission == nil {
			s.gate.Release(method)
			return
		}
		if !promptAdmission.Adopted() {
			promptAdmission.Settle()
		}
	}
	serve := func() {
		var after func()
		execute := func() ([]byte, error) {
			// This transport boundary deliberately does not treat loopback as an
			// export authorization mode. CoreHandler repeats the typed check so
			// embeddings cannot bypass it by calling Handle directly.
			if method == "runtime.supportSnapshot" && !s.Auth.Enabled {
				denied := &SupportSnapshotAuthenticationRequiredError{}
				return json.Marshal(map[string]any{"id": id, "ok": false, "error": denied.Error(), "errorCode": denied.ErrorCode()})
			}
			result, handleErr := s.Handler.Handle(requestContext, method, params, clientKey)
			if handleErr != nil {
				// AUX-32: the browser reply carries only a bounded, secret-free
				// message; the raw cause is logged redacted. A typed host error is
				// mapped by code so native text can never be reflected.
				attributes := []any{"method", method, "cause", diagnostics.SanitizeDiagnosticText(handleErr.Error(), browserErrorMaxBytes)}
				if code, ok := hostErrorCode(handleErr); ok {
					attributes = append(attributes, "hostCode", code)
				}
				slog.Error("controller request failed", attributes...)
				failure := map[string]any{"id": id, "ok": false, "error": browserErrorMessage(handleErr)}
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
			settleAdmission()
			_ = output.connection.Close(websocket.StatusMessageTooBig, "control message too large")
			return
		}
		if !s.admission.TryAcquireControl(len(payload)) {
			settleAdmission()
			_ = output.connection.Close(websocket.StatusTryAgainLater, "too many pending requests; reconnect to resume")
			return
		}
		s.mu.Lock()
		if s.ctx.Err() != nil {
			s.mu.Unlock()
			s.admission.ReleaseControl(len(payload))
			settleAdmission()
			return
		}
		s.handlers.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.admission.ReleaseControl(len(payload))
			defer s.handlers.Done()
			defer settleAdmission()
			serve()
		}()
		return
	}
	if !s.admission.TryAcquireOrdinary(clientKey, len(payload)) {
		settleAdmission()
		_ = output.connection.Close(websocket.StatusTryAgainLater, "too many pending requests; reconnect to resume")
		return
	}
	select {
	case s.inflight <- struct{}{}:
	case <-ctx.Done():
		s.admission.ReleaseOrdinary(clientKey, len(payload))
		settleAdmission()
		return
	default:
		s.admission.ReleaseOrdinary(clientKey, len(payload))
		settleAdmission()
		_ = output.connection.Close(websocket.StatusTryAgainLater, "too many pending requests; reconnect to resume")
		return
	}
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		<-s.inflight
		s.admission.ReleaseOrdinary(clientKey, len(payload))
		settleAdmission()
		return
	}
	s.handlers.Add(1)
	s.mu.Unlock()
	go func() {
		defer func() {
			<-s.inflight
			s.admission.ReleaseOrdinary(clientKey, len(payload))
			s.handlers.Done()
			settleAdmission()
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

func (s *WebSocketServer) hasConnectionCapacity(clientKey string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, active := s.sockets[clientKey]
	_, pending := s.reapTimers[clientKey]
	return active || pending || len(s.sockets)+len(s.reapTimers) < BrowserMaxConnections
}

// replace admits a browser socket under the connection and reconnect bounds.
// It returns false with a non-empty reason when a new identity exceeds tracked
// capacity or the reconnect budget; an empty reason means the server is
// shutting down. Replacing an already-tracked identity never counts as new.
func (s *WebSocketServer) replace(clientKey string, socket browserSocket) (bool, string) {
	s.clientLifecycle.Lock()
	defer s.clientLifecycle.Unlock()
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return false, ""
	}
	_, active := s.sockets[clientKey]
	_, pending := s.reapTimers[clientKey]
	if !active && !pending && len(s.sockets)+len(s.reapTimers) >= BrowserMaxConnections {
		s.mu.Unlock()
		return false, "controller connection capacity reached; retry shortly"
	}
	if _, ok := s.allowReconnectLocked(clientKey, time.Now()); !ok {
		s.mu.Unlock()
		return false, "reconnect rate limited; retry shortly"
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
	return true, ""
}

// allowReconnectLocked applies a per-identity token bucket. time.Now carries
// its monotonic reading here, so a wall-clock jump cannot extend a backoff.
func (s *WebSocketServer) allowReconnectLocked(clientKey string, now time.Time) (time.Duration, bool) {
	if s.reconnects == nil {
		s.reconnects = make(map[string]reconnectState)
	}
	state := s.reconnects[clientKey]
	if !state.blockedUntil.IsZero() {
		if now.Before(state.blockedUntil) {
			return state.blockedUntil.Sub(now), false
		}
		state.blockedUntil = time.Time{}
	}
	if state.windowStart.IsZero() || now.Sub(state.windowStart) > reconnectWindow {
		state.windowStart = now
		state.attempts = 0
		state.backoff = 0
	}
	state.attempts++
	if state.attempts > reconnectMaxAttempts {
		backoff := state.backoff
		if backoff <= 0 {
			backoff = reconnectBaseBackoff
		}
		state.blockedUntil = now.Add(backoff)
		state.backoff = backoff * 2
		if state.backoff > reconnectMaxBackoff {
			state.backoff = reconnectMaxBackoff
		}
		state.attempts = 0
		state.windowStart = now
		s.reconnects[clientKey] = state
		return backoff, false
	}
	s.reconnects[clientKey] = state
	return 0, true
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
	delete(s.reconnects, clientKey)
	s.mu.Unlock()
	if s.ClientReaped != nil {
		s.ClientReaped(clientKey)
	}
}

func (s *WebSocketServer) Publish(ctx context.Context, channel string, data any) error {
	// AUX-16 event allowlist: only the fixed browser surface can be published.
	// An unknown channel is dropped rather than becoming an accidental browser
	// inject surface.
	if !streamAllowedChannels[channel] {
		return nil
	}
	payload, err := json.Marshal(data)
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
		frame := payload
		if socket.stream != nil {
			frame = socket.stream.stamp(channel, payload)
		}
		if !socket.output.trackEvent() {
			socket.output.failClosedSlow()
			if firstErr == nil {
				firstErr = fmt.Errorf("publish %s: browser event backpressure limit reached", channel)
			}
			continue
		}
		err := socket.output.enqueue(ctx, frame)
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
	s.reconnects = make(map[string]reconnectState)
	s.mu.Unlock()
	for _, socket := range sockets {
		if socket.connection != nil {
			_ = socket.connection.CloseNow()
		}
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
