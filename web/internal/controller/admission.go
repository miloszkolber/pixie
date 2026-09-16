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
//
// Queueing a message or retrying a blocked one ends in scheduleFollowUp, which
// can dispatch a fresh prompt. Both are therefore run-creating admission and
// are refused during drain so no queued prompt enters the quiesce window.
var drainGatedMethods = map[string]bool{
	"session.create":     true,
	"session.fork":       true,
	"session.prompt":     true,
	"session.queueAdd":   true,
	"session.queueRetry": true,
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

// runAdmission is a one-shot handoff of one gate admission from synchronous
// dispatch to the asynchronous agent run it starts. The transport (or the
// controller-owned follow-up dispatcher) creates it and keeps ownership until
// dispatch returns; a run that starts adopts it and settles it when the native
// prompt returns. If no run starts, the dispatcher settles it so the gate never
// leaks. Because the admission outlives the dispatch, WaitForDrain waits for
// the run itself, bounded by the caller's context, not merely for the handler
// to return.
type runAdmission struct {
	release func()
	once    sync.Once
	adopted atomic.Bool
}

func newRunAdmission(release func()) *runAdmission {
	return &runAdmission{release: release}
}

// Adopt transfers settlement ownership to the asynchronous run. It reports
// false when a run already adopted the admission.
func (a *runAdmission) Adopt() bool {
	return a != nil && a.adopted.CompareAndSwap(false, true)
}

// Adopted reports whether a run took ownership of the admission. A nil
// admission counts as adopted, so an absent handoff never settles anything.
func (a *runAdmission) Adopted() bool {
	return a == nil || a.adopted.Load()
}

// Settle releases the admission exactly once, from whichever path owns it.
func (a *runAdmission) Settle() {
	if a == nil {
		return
	}
	a.once.Do(func() {
		if a.release != nil {
			a.release()
		}
	})
}

// runAdmissionContextKey carries a run admission from the transport into the
// handler and the asynchronous work it starts. It is structural: the value is
// never serialized or exposed to the browser.
type runAdmissionContextKey struct{}

func withRunAdmission(ctx context.Context, admission *runAdmission) context.Context {
	if admission == nil {
		return ctx
	}
	return context.WithValue(ctx, runAdmissionContextKey{}, admission)
}

func runAdmissionFromContext(ctx context.Context) *runAdmission {
	if ctx == nil {
		return nil
	}
	admission, _ := ctx.Value(runAdmissionContextKey{}).(*runAdmission)
	return admission
}

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

// NewAdmissionGate builds an open gate. BeginDrain refuses new runnable work
// immediately: the grace argument is accepted for call-site compatibility but
// does not delay the refusal or wait for work already on the wire.
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
// counted as in-flight so a drain waits for them. An admission that starts an
// asynchronous run is handed to that run through runAdmission, which holds the
// count past dispatch and releases it when the run settles; every admission
// must be released exactly once with the same method (see Release). Non-gated
// methods are never counted.
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

// Release settles one admission for method. It is method-scoped: a non-gated
// method never decrements the in-flight count, so a read-only request
// completing can never settle an admitted runnable slot. Call it exactly once
// for each gated method that TryAdmit admitted. It reports whether the gated
// in-flight set is now empty.
func (g *AdmissionGate) Release(method string) bool {
	if g == nil {
		return true
	}
	if !IsDrainGatedMethod(method) {
		return false
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
