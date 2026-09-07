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
	queue      chan []byte
	replay     *ReplayCache
	mu         sync.Mutex
	bytes      int
	large      bool
	closed     bool
}

const socketOutputBudget = 32 * 1024 * 1024

func newSocketOutput(connection *websocket.Conn) *socketOutput {
	return &socketOutput{connection: connection, queue: make(chan []byte, 256), replay: NewReplayCache()}
}

// enqueue borrows immutable bytes, which can also belong to the replay cache.
// Callers must not modify or pool the payload after enqueueing it.
func (o *socketOutput) enqueue(ctx context.Context, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return fmt.Errorf("browser connection closed")
	}
	// Bound accumulated output, not the size of a single history response.
	// One large frame can coexist with the ordinary bounded backlog.
	large := len(payload) > socketOutputBudget
	if (large && !o.large) || (!large && o.bytes+len(payload) <= socketOutputBudget) {
		select {
		case o.queue <- payload:
			if large {
				o.large = true
			} else {
				o.bytes += len(payload)
			}
			return nil
		default:
		}
	}
	o.closed = true
	go o.connection.Close(websocket.StatusTryAgainLater, "browser is too slow; reconnect to resume")
	return fmt.Errorf("browser output limit exceeded")
}

func (o *socketOutput) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case payload := <-o.queue:
			bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := o.connection.Write(bounded, websocket.MessageText, payload)
			cancel()
			o.mu.Lock()
			o.release(payload)
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
	defer o.mu.Unlock()
	o.closed = true
	for {
		select {
		case payload := <-o.queue:
			o.release(payload)
		default:
			return
		}
	}
}

// The caller holds mu, including while stopping and draining queued frames.
func (o *socketOutput) release(payload []byte) {
	if len(payload) > socketOutputBudget {
		o.large = false
	} else {
		o.bytes -= len(payload)
	}
}
