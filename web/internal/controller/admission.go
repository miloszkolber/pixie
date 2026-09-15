package controller

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Drain-gated methods create or resume runnable agent work. Once the controller
// is quiescing these are refused so an update or rollback can let in-flight work
// settle first. Stop/UI-cancel and read-only inspection stay available so an
// operator can always observe or interrupt a drain.
var drainGatedMethods = map[string]bool{
	"session.create": true,
	"session.fork":   true,
	"session.prompt": true,
}

// ErrControllerQuiescing is the typed refusal returned to a caller that asks
// for new work while the controller is draining. It is a deliberate state, not
// a failure, so the Web UI can report quiescing instead of an error.
type ErrControllerQuiescing struct{}

func (ErrControllerQuiescing) Error() string {
	return "The controller is quiescing for an update; new work is temporarily paused while in-flight runs finish."
}

func (ErrControllerQuiescing) ErrorCode() string { return "controller_quiescing" }

// IsDrainGatedMethod reports whether a browser method creates or resumes
// runnable work and therefore participates in the admission gate.
func IsDrainGatedMethod(method string) bool { return drainGatedMethods[method] }

// AdmissionGate is the process-local drain gate. BeginDrain flips it to
// refusing new gated work; WaitForDrain then blocks until the already-admitted
// set empties (or the context ends). The zero value is not usable; use
// NewAdmissionGate.
type AdmissionGate struct {
	mu       sync.Mutex
	quiesce  atomic.Bool
	inactive chan struct{}
	active   int
}

// NewAdmissionGate builds an open gate. The grace argument is retained for
// callers that want a settle window before they invoke WaitForDrain; it no
// longer delays the refusal, which is immediate.
func NewAdmissionGate(grace time.Duration) *AdmissionGate {
	_ = grace
	gate := &AdmissionGate{inactive: make(chan struct{})}
	close(gate.inactive)
	return gate
}

// Quiescing reports whether the gate is refusing new runnable work.
func (g *AdmissionGate) Quiescing() bool {
	return g != nil && g.quiesce.Load()
}

// BeginDrain flips the gate to refusing new runnable work. It is idempotent and
// never revokes work that was already admitted. It returns only after the
// refusal is visible, so a caller that proceeds knows new work is paused.
func (g *AdmissionGate) BeginDrain() {
	if g == nil {
		return
	}
	g.quiesce.Store(true)
}

// TryAdmit reports whether a method may start now. Run-creating methods are
// counted as in-flight so a drain waits for them; every admitted gated method
// must call Release exactly once.
func (g *AdmissionGate) TryAdmit(method string) bool {
	if !IsDrainGatedMethod(method) {
		return true
	}
	if g == nil || g.quiesce.Load() {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	// Re-check under the lock so a concurrent BeginDrain cannot race an
	// admission that passed the unlocked check.
	if g.quiesce.Load() {
		return false
	}
	if g.active == 0 {
		g.inactive = make(chan struct{})
	}
	g.active++
	return true
}

// Release settles one previously admitted gated method. It reports whether the
// in-flight set is now empty.
func (g *AdmissionGate) Release() bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active <= 0 {
		return true
	}
	g.active--
	if g.active == 0 {
		close(g.inactive)
		return true
	}
	return false
}

// WaitForDrain blocks until no admitted gated method remains or ctx ends.
func (g *AdmissionGate) WaitForDrain(ctx context.Context) {
	if g == nil {
		return
	}
	for {
		g.mu.Lock()
		if g.active == 0 {
			g.mu.Unlock()
			return
		}
		inactive := g.inactive
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-inactive:
		}
	}
}
