package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// One bounded writer per browser isolates slow readers without reordering its
// events and responses. Responses remain recoverable through the replay cache.
type socketOutput struct {
	connection *websocket.Conn
	clientKey  string
	queue      chan socketOutputItem
	replay     *ReplayCache
	aggregate  *AggregateByteAdmission
	mu         sync.Mutex
	writeMu    sync.Mutex
	bytes      int
	events     int
	large      bool
	closed     bool
}

type socketOutputItem struct {
	payload  []byte
	control  bool
	reserved int
}

const (
	socketOutputBudget   = 32 * 1024 * 1024
	socketOutputMaxItems = 256
	socketHeartbeatFrame = `{"channel":"server.heartbeat","data":{"padded":"................................................................"}}`
)

// socketHeartbeatPayload is a fixed, secret-free keepalive frame. It is padded
// so a proxy treats it as a normal data frame rather than coalescing it away.
var socketHeartbeatPayload = []byte(socketHeartbeatFrame)

func newSocketOutput(connection *websocket.Conn, aggregate *AggregateByteAdmission, clientKey string) *socketOutput {
	return &socketOutput{connection: connection, clientKey: clientKey, queue: make(chan socketOutputItem, socketOutputMaxItems), replay: NewReplayCacheWithAdmission(aggregate), aggregate: aggregate}
}

// enqueue borrows immutable bytes, which can also belong to the replay cache.
// Callers must not modify or pool the payload after enqueueing it.
func (o *socketOutput) enqueue(ctx context.Context, payload []byte) error {
	return o.enqueueWithLane(ctx, payload, false)
}

func (o *socketOutput) enqueueWithLane(ctx context.Context, payload []byte, control bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return fmt.Errorf("browser connection closed")
	}
	if len(payload) > BrowserFrameMaxBytes {
		return fmt.Errorf("browser output exceeds the %d-byte frame limit", BrowserFrameMaxBytes)
	}
	if control && len(payload) > BrowserControlFrameMaxBytes {
		return fmt.Errorf("browser control output exceeds the %d-byte limit", BrowserControlFrameMaxBytes)
	}
	if o.aggregate != nil {
		if control {
			if !o.aggregate.TryAcquireControl(len(payload)) {
				o.failClosed(websocket.StatusTryAgainLater, "browser control output limit exceeded")
				return fmt.Errorf("browser control output limit exceeded")
			}
		} else if !o.aggregate.TryAcquireOrdinary(len(payload)) {
			o.failClosed(websocket.StatusTryAgainLater, "browser aggregate output limit exceeded")
			return fmt.Errorf("browser aggregate output limit exceeded")
		}
	}
	releaseAdmission := func() {
		if o.aggregate == nil {
			return
		}
		if control {
			o.aggregate.ReleaseControl(len(payload))
		} else {
			o.aggregate.ReleaseOrdinary(len(payload))
		}
	}
	// Bound accumulated output, not the size of a single history response.
	// One large frame can coexist with the ordinary bounded backlog.
	large := len(payload) > socketOutputBudget
	if (large && !o.large) || (!large && o.bytes+len(payload) <= socketOutputBudget) {
		select {
		case o.queue <- socketOutputItem{payload: payload, control: control, reserved: len(payload)}:
			if large {
				o.large = true
			} else {
				o.bytes += len(payload)
			}
			o.events++
			return nil
		default:
		}
	}
	releaseAdmission()
	o.closed = true
	o.closeWithStatus(websocket.StatusTryAgainLater, "browser is too slow; reconnect to resume")
	return fmt.Errorf("browser output limit exceeded")
}

// trackEvent applies the explicit AUX-16 backpressure cap on top of the byte
// budget. A browser that falls this many channel frames behind has missed the
// revision chain, so the socket is closed and the client resyncs rather than
// silently diverging.
func (o *socketOutput) trackEvent() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return false
	}
	return o.events < socketEventBackpressure
}

// runHeartbeat writes a fixed padded control frame on an idle socket so a dead
// intermediary is noticed and the browser's resume path runs. Writes are
// serialized with the main writer through writeMu.
func (o *socketOutput) runHeartbeat(ctx context.Context) {
	ticker := time.NewTicker(socketHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := o.writeControl(ctx, socketHeartbeatPayload); err != nil {
				return
			}
		}
	}
}

// writeControl serializes one control frame with the ordinary writer. It does
// not consume the event queue or its backpressure budget.
func (o *socketOutput) writeControl(ctx context.Context, payload []byte) error {
	if o.connection == nil {
		return nil
	}
	o.writeMu.Lock()
	defer o.writeMu.Unlock()
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return o.connection.Write(bounded, websocket.MessageText, payload)
}

// failClosed stops accepting output for this socket and asks the peer to
// reconnect. Marking the socket closed before the asynchronous close keeps
// later publishes from queueing or spawning additional close attempts.
// Callers hold o.mu.
func (o *socketOutput) failClosed(code websocket.StatusCode, reason string) {
	o.closed = true
	o.closeWithStatus(code, reason)
}

// failClosedSlow sheds a socket that exceeded the explicit event backpressure
// cap. The peer reconnects and resyncs rather than receiving a partial chain.
func (o *socketOutput) failClosedSlow() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return
	}
	o.failClosed(websocket.StatusTryAgainLater, "browser event backpressure limit reached; reconnect to resync")
}

func (o *socketOutput) closeWithStatus(code websocket.StatusCode, reason string) {
	if o.connection == nil {
		return
	}
	go o.connection.Close(code, reason)
}

func (o *socketOutput) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-o.queue:
			bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := o.connection.Write(bounded, websocket.MessageText, item.payload)
			cancel()
			o.mu.Lock()
			o.release(item)
			o.mu.Unlock()
			if err != nil {
				o.connection.CloseNow()
				return
			}
		}
	}
}

func (o *socketOutput) stop() {
	o.mu.Lock()
	o.closed = true
	for {
		select {
		case item := <-o.queue:
			o.release(item)
		default:
			o.mu.Unlock()
			// Release this socket's private replay namespace and its aggregate
			// reservations. Without this, every disconnect or replacement would
			// retain the retained-response budget until process restart.
			if o.replay != nil {
				o.replay.ReleaseClient(o.clientKey)
			}
			return
		}
	}
}

// The caller holds mu, including while stopping and draining queued frames.
func (o *socketOutput) release(item socketOutputItem) {
	if len(item.payload) > socketOutputBudget {
		o.large = false
	} else {
		o.bytes -= len(item.payload)
	}
	if o.events > 0 {
		o.events--
	}
	if o.aggregate != nil && item.reserved > 0 {
		if item.control {
			o.aggregate.ReleaseControl(item.reserved)
		} else {
			o.aggregate.ReleaseOrdinary(item.reserved)
		}
	}
}
