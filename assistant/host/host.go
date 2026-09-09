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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const minSecretLength = 32

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
}

// Handle is the lifecycle handle returned by Start. Its methods form the
// deliberately small public surface needed by standalone and full-host
// entrypoints. A zero Handle is safe to close, which keeps composition roots
// straightforward during staged engine migration.
type Handle struct {
	endpoint   string
	ready      <-chan struct{}
	errors     <-chan error
	server     *http.Server
	listener   net.Listener
	closed     chan struct{}
	secret     string
	supervisor *nativeSupervisor
	once       sync.Once
	closeErr   error
}

// Start starts the shared assistant engine for config.
//
// The Go facade owns the private lifecycle endpoint used by the full-host
// composition. When PiExecutable is provided, the selected Pi is launched as
// one bounded child and all native execution remains behind this facade. A
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
	runtimeID := randomID()
	bootID := randomID()
	ready := make(chan struct{})
	errorsCh := make(chan error, 1)
	closed := make(chan struct{})
	handle := &Handle{
		endpoint:   "ws://" + net.JoinHostPort(host, strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)) + "/pi",
		ready:      ready,
		errors:     errorsCh,
		listener:   listener,
		closed:     closed,
		secret:     secret,
		supervisor: supervisor,
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
		_ = json.NewEncoder(response).Encode(map[string]any{
			"protocolVersion": 1,
			"runtimeId":       runtimeID,
			"bootId":          bootID,
			"capabilities":    handle.capabilities(),
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
	var writeMu sync.Mutex
	write := func(payload []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return connection.Write(ctx, websocket.MessageText, payload)
	}
	var unsubscribe func()
	defer func() {
		if unsubscribe != nil {
			unsubscribe()
		}
	}()
	handshaken := false
	active := make(map[uint64]struct{})
	var activeMu sync.Mutex
	startEvents := func() {
		if handle.supervisor == nil || unsubscribe != nil {
			return
		}
		events, stop := handle.supervisor.subscribe()
		unsubscribe = stop
		go func() {
			for event := range events {
				frame, ok := nativeEventFrame(handle.supervisor, event)
				if !ok {
					continue
				}
				if err := write(mustJSON(frame)); err != nil {
					return
				}
			}
		}()
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
		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if json.Unmarshal(payload, &envelope) != nil || len(envelope.ID) == 0 || envelope.Method == "" || envelope.Params == nil {
			_ = connection.Close(websocket.StatusPolicyViolation, "invalid request envelope")
			return
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
			if handle.supervisor == nil {
				_ = writeErrorWith(write, id, "embedded assistant native engine is unavailable")
				return
			}
			callContext, release := context.WithTimeout(ctx, nativeRequestTimeout)
			result, callErr := handle.supervisor.callHost(callContext, method, params)
			release()
			if callErr != nil {
				_ = writeErrorWith(write, id, callErr.Error())
				return
			}
			_ = write(mustJSON(map[string]any{"id": id, "result": json.RawMessage(result)}))
		}(id, envelope.Method, envelope.Params)
	}
}

func nativeEventFrame(supervisor *nativeSupervisor, event map[string]any) (map[string]any, bool) {
	copyEvent := make(map[string]any, len(event))
	for key, value := range event {
		copyEvent[key] = value
	}
	if method, ok := event["method"].(string); ok && method != "" {
		if _, ok := event["params"]; ok {
			delete(copyEvent, "id")
			return copyEvent, true
		}
	}
	sessionID, _ := event["sessionId"].(string)
	if sessionID == "" {
		supervisor.mu.Lock()
		sessionID = supervisor.currentSession
		supervisor.mu.Unlock()
	}
	return map[string]any{
		"method": "session.event",
		"params": map[string]any{"sessionId": sessionID, "event": copyEvent},
	}, true
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
	return write(mustJSON(map[string]any{
		"id": id,
		"error": map[string]any{
			"code":    -32000,
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
