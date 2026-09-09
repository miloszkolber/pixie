package controller

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type connectionGenerationKey struct{}
type recognizedPiConnectionKey struct{}

// sessionOperationGate keeps Pi calls serialized per session while allowing
// a request that has not entered the session yet to stop waiting when canceled.
// Its lazy initialization preserves the useful zero value of sessionEntry in
// focused state tests.
type sessionOperationGate struct {
	once  sync.Once
	token chan struct{}
}

func (g *sessionOperationGate) ready() chan struct{} {
	g.once.Do(func() {
		g.token = make(chan struct{}, 1)
		g.token <- struct{}{}
	})
	return g.token
}

func (g *sessionOperationGate) Lock() {
	<-g.ready()
}

func (g *sessionOperationGate) LockContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-g.ready():
		if err := ctx.Err(); err != nil {
			g.Unlock()
			return err
		}
		return nil
	}
}

func (g *sessionOperationGate) TryLock() bool {
	select {
	case <-g.ready():
		return true
	default:
		return false
	}
}

func (g *sessionOperationGate) Unlock() {
	select {
	case g.ready() <- struct{}{}:
	default:
		panic("unlock of unlocked session operation gate")
	}
}

func (entry *sessionEntry) context(ctx context.Context) context.Context {
	entry.state.Lock()
	generation := entry.attached
	entry.state.Unlock()
	return context.WithValue(ctx, connectionGenerationKey{}, generation)
}

// Waiting for an operation does not grant authority over a replaced projection.
func (m *SessionManager) lockEntry(sessionID string, entry *sessionEntry) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return m.lockEntryContext(ctx, sessionID, entry)
}

func (m *SessionManager) lockEntryContext(ctx context.Context, sessionID string, entry *sessionEntry) error {
	if err := entry.op.LockContext(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	current := !m.closed && m.sessions[sessionID] == entry && !m.lifecycle[sessionID]
	m.mu.Unlock()
	if !current {
		entry.op.Unlock()
		return fmt.Errorf("session changed while waiting for an operation")
	}
	return nil
}

// Reserve lifecycle changes before sending them to Pi, including for an
// unloaded archived session. The caller already owns entry.op when entry != nil.
func (m *SessionManager) beginLifecycle(sessionID string, entry *sessionEntry) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.sessions[sessionID]
	allowedRefs := 0
	if entry != nil {
		if current != entry {
			return nil, fmt.Errorf("session changed while waiting for an operation")
		}
		allowedRefs = 1
	}
	if m.closed || m.lifecycle[sessionID] || current != nil && current.refs > allowedRefs {
		return nil, fmt.Errorf("wait for the chat to finish loading or updating")
	}
	if current != nil {
		current.state.Lock()
		running := current.streaming || current.promptActive || current.runID != ""
		current.state.Unlock()
		if running {
			return nil, fmt.Errorf("stop the running chat before changing its lifecycle")
		}
	}
	if m.lifecycle == nil {
		m.lifecycle = make(map[string]bool)
	}
	m.lifecycle[sessionID] = true
	return func() {
		m.mu.Lock()
		delete(m.lifecycle, sessionID)
		m.mu.Unlock()
		if entry != nil {
			m.scheduleFollowUp(sessionID, entry)
		}
	}, nil
}

// LIFE-01/X10-X11: distinct Stop reporting and explicit idle-runtime release.
// Close/Archive/Delete never imply Stop; they require an already settled
// session through beginLifecycle's running check above. Stop freezes dispatch
// first and verifies generation quiescence; idle release frees only eligible
// settled residents.

const (
	StopStatusStopping  = "stopping"
	StopStatusStopped   = "stopped"
	StopStatusUncertain = "uncertain"
)

// StopOutcome reports a verified Stop with distinct stopped/uncertain states.
// A forwarded abort/UI response is never treated as acceptance by itself;
// Status is stopped only after generation quiescence is verified.
type StopOutcome struct {
	Status            string `json:"status"`
	Generation        uint64 `json:"generation"`
	RetainedPaused    int    `json:"retainedPaused"`
	ForcedTermination bool   `json:"forcedTermination"`
	Reason            string `json:"reason,omitempty"`
}

// stopQuiescentLocked reports whether no dispatch authority remains.
// The caller holds entry.state.
func stopQuiescentLocked(entry *sessionEntry, generation uint64) (bool, string) {
	if entry.promptGeneration != generation {
		return false, "prompt generation changed during stop"
	}
	if entry.promptActive || entry.streaming {
		return false, "prompt still active"
	}
	if entry.runID != "" {
		return false, "native continuation still active"
	}
	if entry.drainScheduled || entry.drainRetry != nil || entry.replay != nil {
		return false, "dispatch still scheduled"
	}
	return true, ""
}

// pausedOutboxCount counts unsent items Stop retains paused.
func pausedOutboxCount(queue sessionQueueState) int {
	return len(queue.FollowUp)
}

// idleStateEligibleLocked reports whether the projection itself is settled
// with no pending outbox or scheduled work. Liveness pins and dialogs are
// checked by the caller holding SessionManager.mu. The caller holds
// entry.state.
func idleStateEligibleLocked(entry *sessionEntry) (bool, string) {
	if entry.streaming || entry.promptActive || entry.runID != "" {
		return false, "session still running"
	}
	if entry.queue.Dispatch != nil || entry.queue.Blocked != nil {
		return false, "delivery still uncertain or dispatching"
	}
	if len(entry.queue.FollowUp) > 0 || len(entry.queue.Steering) > 0 {
		return false, "queued work still pending"
	}
	if entry.drainScheduled || entry.drainRetry != nil || entry.replay != nil {
		return false, "dispatch still scheduled"
	}
	return true, ""
}

// lockEntryForSettlement lets a prompt settlement clear its own generation
// while Stop holds the dispatch freeze (lifecycle). New dispatch stays
// blocked through admitFollowUp; only the settling generation may proceed.
func (m *SessionManager) lockEntryForSettlement(sessionID string, entry *sessionEntry) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := entry.op.LockContext(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	current := !m.closed && m.sessions[sessionID] == entry
	m.mu.Unlock()
	if !current {
		entry.op.Unlock()
		return fmt.Errorf("session changed while waiting for an operation")
	}
	return nil
}
