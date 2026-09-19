package controller

import (
	"context"
	"testing"
)

// A slow client must be shed once its per-socket output plus the process-wide
// aggregate budget is exhausted, and the shed decision must release every
// admission reservation on stop.
func TestSocketOutputFailsClosedWhenAggregateBudgetExhausted(t *testing.T) {
	aggregate := NewAggregateByteAdmission(8192, 4096)
	output := newSocketOutput(nil, aggregate, "slow-client")
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := output.enqueueWithLane(ctx, make([]byte, 4096), false); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if err := output.enqueueWithLane(ctx, make([]byte, 4096), false); err == nil {
		t.Fatal("overflow beyond the aggregate budget was admitted")
	}
	output.mu.Lock()
	closed := output.closed
	queued := len(output.queue)
	output.mu.Unlock()
	if !closed {
		t.Fatal("overflowed socket was not marked closed")
	}
	if queued != 2 {
		t.Fatalf("queued frames = %d, want the 2 admitted frames", queued)
	}
	// Once closed, further publishes short-circuit instead of growing the queue
	// or spawning more close attempts.
	if err := output.enqueueWithLane(ctx, make([]byte, 1), false); err == nil {
		t.Fatal("closed socket kept accepting output")
	}
	if got := aggregate.OrdinaryBytes(); got != 8192 {
		t.Fatalf("retained ordinary bytes = %d, want 8192", got)
	}
	output.stop()
	if got := aggregate.OrdinaryBytes(); got != 0 {
		t.Fatalf("stop retained %d aggregate bytes", got)
	}
}

// The per-socket replay cache holds aggregate reservations for retained
// responses. Disconnecting must release them or every reconnect would leak the
// process-wide budget.
func TestSocketOutputStopReleasesPrivateReplayReservation(t *testing.T) {
	aggregate := NewAggregateByteAdmission(1<<20, 1<<10)
	output := newSocketOutput(nil, aggregate, "reconnecting-client")
	value, err := output.replay.Run(context.Background(), "reconnecting-client", "history", "fp", func() ([]byte, error) {
		return make([]byte, 64*1024), nil
	})
	if err != nil || len(value) != 64*1024 {
		t.Fatalf("retained history result = %d bytes, %v", len(value), err)
	}
	if got := aggregate.OrdinaryBytes(); got != 64*1024 {
		t.Fatalf("replay reservation = %d, want %d", got, 64*1024)
	}
	output.stop()
	if got := aggregate.OrdinaryBytes(); got != 0 {
		t.Fatalf("per-socket replay retained %d bytes after stop", got)
	}
}

// A release while an execution is still in flight must not let the detached
// namespace reserve bytes after the fact.
func TestReplayReleaseClientDuringInflightExecutionDoesNotReserve(t *testing.T) {
	aggregate := NewAggregateByteAdmission(1<<20, 1<<10)
	cache := NewReplayCacheWithAdmission(aggregate)
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = cache.Run(context.Background(), "client", "pending", "fp", func() ([]byte, error) {
			close(started)
			<-release
			return make([]byte, 4096), nil
		})
	}()
	<-started
	cache.ReleaseClient("client")
	close(release)
	<-done
	if got := aggregate.OrdinaryBytes(); got != 0 {
		t.Fatalf("detached in-flight execution reserved %d bytes", got)
	}
}
