// Package host is the public composition boundary for the Pixie assistant.
//
// The assistant implementation is deliberately hidden behind this package.
// Consumers (including the full-host binary) must use this facade rather than
// importing an assistant implementation package or carrying a second
// supervisor. The native engine is supplied by the assistant build; this
// package only owns the stable configuration and lifecycle seam shared by both
// entrypoints.
package host

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	piwire "github.com/miloszkolber/pixie/contracts/piprotocol"
)

const (
	minSecretLength      = 32
	hostWriteTimeout     = 10 * time.Second
	hostEventEnqueueTime = time.Second
	// hostRestartDrainGrace lets the controller read the reload acknowledgement
	// before the accepted restart closes the connection.
	hostRestartDrainGrace = 250 * time.Millisecond
)

// Config contains the assistant-owned startup inputs. The full-host
// composition supplies this same value to the shared engine instead of
// constructing a second supervisor or SDK client.
type Config struct {
	Host         string
	Port         int
	Secret       string
	AgentDir     string
	PiExecutable string
	PiArgs       []string
	Llama        bool
	// AllowSelfRestart opts the host into the explicit runtime.restart
	// operation. The response drains the process and the service manager
	// restarts it; the default stays fail-closed.
	AllowSelfRestart bool
	// ProtocolMode is the raw PIXIE_PI_PROTOCOL value (v1, auto or v2,
	// case-insensitive). Empty selects v1. An invalid value fails Start.
	ProtocolMode string
}

// Handle is the lifecycle handle returned by Start. Its methods form the
// deliberately small public surface needed by standalone and full-host
// entrypoints. A zero Handle is safe to close, which keeps composition roots
// straightforward during staged engine migration.
type Handle struct {
	endpoint     string
	ready        <-chan struct{}
	errors       <-chan error
	server       *http.Server
	listener     net.Listener
	closed       chan struct{}
	secret       string
	supervisor   *nativeSupervisor
	protocolMode piwire.HostProtocolMode
	once         sync.Once
	closeErr     error
	restart      chan struct{}
	restartOne   sync.Once
}

// Start starts the shared assistant engine for config.
//
// The Go facade owns the private lifecycle endpoint used by the full-host
// composition. When PiExecutable is provided, the selected Pi is launched as
// one bounded child per resident logical session and all native execution remains behind this facade. A
// missing executable keeps the transport useful for composition tests but
// advertises no native capability rather than starting another SDK process.
func Start(ctx context.Context, config Config) (*Handle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	host := strings.TrimSpace(config.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	secret := strings.TrimSpace(config.Secret)
	if len(secret) < minSecretLength {
		return nil, fmt.Errorf("assistant secret must be at least %d characters", minSecretLength)
	}
	if !isLoopbackHost(host) {
		return nil, fmt.Errorf("assistant host must be a literal loopback address")
	}
	if config.Port < 0 || config.Port > 65535 {
		return nil, fmt.Errorf("assistant port must be between 0 and 65535")
	}
	// Resolve the protocol mode before opening the listener so an invalid
	// PIXIE_PI_PROTOCOL fails startup rather than serving a partial transport.
	// An explicit Config value wins; otherwise the standalone host resolves
	// the process environment.
	protocolRaw := strings.TrimSpace(config.ProtocolMode)
	if protocolRaw == "" {
		protocolRaw = strings.TrimSpace(os.Getenv(piwire.HostProtocolEnvVar))
	}
	protocolMode, err := piwire.ParseHostProtocolMode(protocolRaw)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(config.Port)))
	if err != nil {
		return nil, fmt.Errorf("listen for embedded assistant: %w", err)
	}
	var supervisor *nativeSupervisor
	// An explicitly selected agent directory or argv opts into the standard
	// public `pi` executable lookup. Keep the zero Config transport-only so
	// controller composition tests do not require Pi to be installed.
	if strings.TrimSpace(config.PiExecutable) == "" && (strings.TrimSpace(config.AgentDir) != "" || len(config.PiArgs) > 0) {
		config.PiExecutable = "pi"
	}
	if strings.TrimSpace(config.PiExecutable) != "" {
		supervisor = newNativeSupervisor(config)
		if err := supervisor.start(ctx); err != nil {
			_ = listener.Close()
			return nil, err
		}
	}
	// The durable host authority is the deletion/pairing identity; bootID stays
	// process-scoped for diagnostics. Transport-only composition (no
	// supervisor) keeps a random, non-durable identity.
	runtimeID := randomID()
	if supervisor != nil && supervisor.hostIdentity != "" {
		runtimeID = supervisor.hostIdentity
	}
	bootID := randomID()
	ready := make(chan struct{})
	errorsCh := make(chan error, 1)
	closed := make(chan struct{})
	handle := &Handle{
		endpoint:     "ws://" + net.JoinHostPort(host, strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)) + "/pi",
		ready:        ready,
		errors:       errorsCh,
		listener:     listener,
		closed:       closed,
		secret:       secret,
		supervisor:   supervisor,
		protocolMode: protocolMode,
		restart:      make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/livez", func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		_, _ = io.WriteString(response, "ok")
	})
	mux.HandleFunc("/readyz", func(response http.ResponseWriter, request *http.Request) {
		if !authorized(request, secret) {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		if request.Method != http.MethodGet {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		healthy, detail := handle.health()
		if !healthy {
			response.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(response).Encode(map[string]any{
			"protocolVersion": 1,
			"runtimeId":       runtimeID,
			"bootId":          bootID,
			"capabilities":    handle.capabilities(),
			"operationSet":    handle.operationSet(),
			"ready":           healthy,
			"detail":          detail,
		})
	})
	mux.HandleFunc("/pi", func(response http.ResponseWriter, request *http.Request) {
		if !authorized(request, secret) {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		handleConnection(handle, response, request, runtimeID, bootID)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	handle.server = server
	close(ready)
	go func() {
		serveErr := server.Serve(listener)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			select {
			case errorsCh <- serveErr:
			default:
			}
		}
		close(closed)
	}()
	if supervisor != nil {
		go func() {
			if err := <-supervisor.errors; err != nil {
				select {
				case errorsCh <- err:
				default:
				}
			}
		}()
	}
	return handle, nil
}

func authorized(request *http.Request, secret string) bool {
	values := request.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return false
	}
	candidate := strings.TrimPrefix(values[0], "Bearer ")
	expectedDigest := sha256.Sum256([]byte(secret))
	candidateDigest := sha256.Sum256([]byte(candidate))
	return subtle.ConstantTimeCompare(expectedDigest[:], candidateDigest[:]) == 1
}

func (h *Handle) capabilities() map[string]int {
	if h == nil || h.supervisor == nil {
		return map[string]int{}
	}
	return h.supervisor.capabilitySnapshot()
}

func isLoopbackHost(host string) bool {
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	parsed := net.ParseIP(strings.Trim(host, "[]"))
	return parsed != nil && parsed.IsLoopback()
}

func randomID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		// crypto/rand failures are exceptionally rare, but a process-local ID is
		// still preferable to exposing an empty identity in a diagnostic frame.
		return fmt.Sprintf("local-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}

func handleConnection(handle *Handle, response http.ResponseWriter, request *http.Request, runtimeID, bootID string) {
	if request.Header.Get("Origin") != "" {
		response.WriteHeader(http.StatusForbidden)
		return
	}
	connection, err := websocket.Accept(response, request, &websocket.AcceptOptions{
		// This listener is private and is dialled only by the in-process
		// controller. Origin checks are intentionally not used as authority.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer connection.Close(websocket.StatusNormalClosure, "assistant stopped")
	connection.SetReadLimit(32 * 1024 * 1024)
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	type outboundRecord struct {
		payload []byte
		done    chan error
	}
	// Keep only a small number of complete records per observer. A slow client
	// is isolated and forced to reload rather than retaining native event memory.
	outbound := make(chan outboundRecord, 4)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case record := <-outbound:
				writeCtx, stopWrite := context.WithTimeout(ctx, hostWriteTimeout)
				err := connection.Write(writeCtx, websocket.MessageText, record.payload)
				stopWrite()
				if record.done != nil {
					record.done <- err
				}
				if err != nil {
					_ = connection.Close(websocket.StatusTryAgainLater, "event delivery lost; reload required")
					cancel()
					return
				}
			}
		}
	}()
	enqueue := func(callCtx context.Context, payload []byte, wait bool) error {
		var done chan error
		if wait {
			done = make(chan error, 1)
		}
		select {
		case outbound <- outboundRecord{payload: payload, done: done}:
		case <-callCtx.Done():
			return callCtx.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
		if done == nil {
			return nil
		}
		select {
		case err := <-done:
			return err
		case <-callCtx.Done():
			return callCtx.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	write := func(payload []byte) error { return enqueue(ctx, payload, true) }
	var unsubscribe func()
	defer func() {
		if unsubscribe != nil {
			unsubscribe()
		}
	}()
	handshaken := false
	negotiated := 0
	active := make(map[uint64]struct{})
	var activeMu sync.Mutex
	var inflight *piwire.HostV2InflightSet
	var inflightMu sync.Mutex
	eventFrame := func(event nativeEvent) []byte { return mustJSON(nativeEventFrame(event)) }
	startEvents := func() {
		if handle.supervisor == nil || unsubscribe != nil {
			return
		}
		unsubscribe = handle.supervisor.subscribe(func(event nativeEvent) error {
			eventCtx, stopEvent := context.WithTimeout(ctx, hostEventEnqueueTime)
			err := enqueue(eventCtx, eventFrame(event), false)
			stopEvent()
			if err != nil {
				// There is no host replay journal. Closing with an explicit reason is
				// the only conservative recovery signal for a dropped observer event.
				_ = connection.Close(websocket.StatusTryAgainLater, "event delivery lost; reload required")
				cancel()
			}
			return nil
		})
	}
	for {
		kind, payload, readErr := connection.Read(ctx)
		if readErr != nil {
			return
		}
		if kind != websocket.MessageText {
			_ = connection.Close(websocket.StatusUnsupportedData, "text frames required")
			return
		}
		// Negotiation-aware modes handle the additive hello before the legacy
		// v1 envelope checks so a v2 offer is never rejected as a v1 frame.
		if handle.protocolMode.Negotiates() && !handshaken {
			selected, helloFrame, handshakeErr := negotiateHostHello(handle, runtimeID, bootID, payload)
			if handshakeErr != nil {
				closeHostV2Connection(connection, handshakeErr)
				return
			}
			negotiated = selected
			if negotiated == piwire.HostV2ProtocolVersion {
				inflight = piwire.NewHostV2InflightSet()
				eventFrame = func(event nativeEvent) []byte { return mustJSON(nativeEventFrameV2(event)) }
			}
			if err := write(helloFrame); err != nil {
				return
			}
			handshaken = true
			startEvents()
			continue
		}
		if negotiated == piwire.HostV2ProtocolVersion {
			if !serveHostV2Frame(handle, connection, ctx, write, inflight, &inflightMu, payload) {
				return
			}
			continue
		}
		// Legacy v1 envelope path, used by the default mode and by an auto
		// connection that negotiated down to a v1 peer.
		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if json.Unmarshal(payload, &envelope) != nil || len(envelope.ID) == 0 || envelope.Method == "" {
			_ = connection.Close(websocket.StatusPolicyViolation, "invalid request envelope")
			return
		}
		// A no-argument method sends `params: null` (or omits it); both mean an
		// empty parameter object, not an invalid envelope.
		if envelope.Params == nil {
			envelope.Params = map[string]any{}
		}
		var id uint64
		if err := json.Unmarshal(envelope.ID, &id); err != nil || id == 0 || id > 9_007_199_254_740_991 {
			_ = connection.Close(websocket.StatusPolicyViolation, "invalid request id")
			return
		}
		if !handshaken {
			if envelope.Method != "runtime.hello" || numberParam(envelope.Params["protocolVersion"]) != 1 {
				_ = connection.Close(websocket.StatusPolicyViolation, "runtime hello required")
				return
			}
			handshaken = true
			if err := write(mustJSON(map[string]any{"id": id, "result": map[string]any{
				"protocolVersion": 1,
				"runtimeId":       runtimeID,
				"bootId":          bootID,
				"version":         "embedded-go-native",
				"capabilities":    handle.capabilities(),
				"operationSet":    handle.operationSet(),
			}})); err != nil {
				return
			}
			startEvents()
			continue
		}
		activeMu.Lock()
		_, duplicate := active[id]
		tooMany := len(active) >= nativePendingMax
		if !duplicate && !tooMany {
			active[id] = struct{}{}
		}
		activeMu.Unlock()
		if duplicate {
			if err := writeErrorWith(write, id, "request id is already in flight"); err != nil {
				return
			}
			continue
		}
		if tooMany {
			if err := writeErrorWith(write, id, "too many pending requests"); err != nil {
				return
			}
			continue
		}
		go func(id uint64, method string, params map[string]any) {
			defer func() {
				activeMu.Lock()
				delete(active, id)
				activeMu.Unlock()
			}()
			result, detail, restart := runHostOperation(handle, ctx, method, params)
			if detail != nil {
				_ = writeErrorWithCode(write, id, int(detail.Code), detail.Message)
				return
			}
			_ = write(mustJSON(map[string]any{"id": id, "result": json.RawMessage(result)}))
			if restart {
				// Let the controller read the acknowledgement before the
				// process drains for the service manager; otherwise it observes
				// the expected close as a failed reload.
				time.Sleep(hostRestartDrainGrace)
				handle.requestRestart()
			}
		}(id, envelope.Method, envelope.Params)
	}
}

func nativeEventParams(event nativeEvent) map[string]any {
	copyEvent := make(map[string]any, len(event.event))
	for key, value := range event.event {
		copyEvent[key] = value
	}
	return map[string]any{"sessionId": event.sessionID, "event": copyEvent}
}

func nativeEventFrame(event nativeEvent) map[string]any {
	return map[string]any{
		"method": "session.event",
		"params": nativeEventParams(event),
	}
}

func numberParam(value any) int {
	switch number := value.(type) {
	case float64:
		if number == float64(int(number)) {
			return int(number)
		}
	case json.Number:
		parsed, _ := strconv.Atoi(string(number))
		return parsed
	}
	return 0
}

func writeErrorWith(write func([]byte) error, id uint64, message string) error {
	return writeErrorWithCode(write, id, -32000, message)
}

func writeErrorWithCode(write func([]byte) error, id uint64, code int, message string) error {
	return write(mustJSON(map[string]any{
		"id": id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}))
}

func mustJSON(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

// Endpoint returns the authenticated host transport endpoint selected by the
// engine. It is intentionally exposed as a value from the facade so the full
// host can dial the same engine without importing its implementation.
func (h *Handle) Endpoint() string {
	if h == nil {
		return ""
	}
	return h.endpoint
}

// Ready is closed when the shared engine has completed startup.
func (h *Handle) Ready() <-chan struct{} {
	if h == nil || h.ready == nil {
		return closed
	}
	return h.ready
}

// Errors reports asynchronous engine failures. A nil channel means that no
// engine was started.
func (h *Handle) Errors() <-chan error {
	if h == nil {
		return nil
	}
	return h.errors
}

// RestartRequested is closed after an accepted runtime.restart. The process
// owner drains and exits with the service-manager restart code.
func (h *Handle) RestartRequested() <-chan struct{} {
	if h == nil || h.restart == nil {
		return nil
	}
	return h.restart
}

func (h *Handle) restartAllowed() bool {
	return h != nil && h.supervisor != nil && h.supervisor.config.AllowSelfRestart
}

func (h *Handle) requestRestart() {
	if h == nil || h.restart == nil {
		return
	}
	h.restartOne.Do(func() { close(h.restart) })
}

func (h *Handle) operationSet() map[string]bool {
	result := nativeOperationSet()
	if h.restartAllowed() {
		result["runtime.restart"] = true
	}
	return result
}

// health reports whether the native engine can currently serve every
// registered session. A lost child degrades readiness until its session is
// reloaded; transport-only compositions stay ready.
func (h *Handle) health() (bool, string) {
	if h == nil || h.supervisor == nil {
		return true, ""
	}
	return h.supervisor.health()
}

// Close releases assistant-owned resources and waits for the private listener
// to stop, bounded by the caller's context.
func (h *Handle) Close(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.once.Do(func() {
		if h.server != nil {
			h.closeErr = h.server.Close()
		}
		if h.supervisor != nil {
			h.closeErr = errors.Join(h.closeErr, h.supervisor.close(ctx))
		}
	})
	if h.closed != nil {
		select {
		case <-h.closed:
		case <-ctx.Done():
			return errors.Join(h.closeErr, ctx.Err())
		}
	}
	return h.closeErr
}

var closed = func() <-chan struct{} {
	result := make(chan struct{})
	close(result)
	return result
}()
