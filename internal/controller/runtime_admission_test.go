package controller

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/workspace"
)

// AUX-19 wiring: Runtime installs one admission gate shared by the WebSocket
// server and the session manager, so a drain started from the runtime also
// refuses controller-owned follow-up dispatch and WaitForDrain accounts for it.
func TestRuntimeSharesAdmissionGateWithSessionManager(t *testing.T) {
	policy, err := workspace.NewPathPolicy([]string{t.TempDir()}, false)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	runtime, err := NewRuntime(RuntimeConfig{Host: "127.0.0.1", Port: port, DataDir: t.TempDir(), StaticDir: t.TempDir(), Policy: policy, Getenv: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.sessions == nil || runtime.socket == nil || runtime.sessions.gate == nil || runtime.sessions.gate != runtime.socket.gate {
		t.Fatal("runtime did not share the admission gate with the session manager")
	}
	runtime.BeginDrain()
	if !runtime.Quiescing() || !runtime.sessions.gate.Quiescing() {
		t.Fatal("BeginDrain did not reach the session manager's gate")
	}
}

// AUX-19 admission gate: while the controller is quiescing, new prompts, forks
// and resume-style session creation are refused so in-flight work can settle,
// while Stop remains available and READ-only methods are unaffected.
func TestAdmissionGateRefusesNewWorkWhileDraining(t *testing.T) {
	gate := NewAdmissionGate(time.Nanosecond)
	if gate.Quiescing() {
		t.Fatal("a fresh gate reported quiescing")
	}
	if !gate.TryAdmit("session.prompt") {
		t.Fatal("a prompt was refused before drain began")
	}
	gate.BeginDrain()
	if !gate.Quiescing() {
		t.Fatal("BeginDrain did not enter the quiescing state")
	}
	for _, method := range []string{"session.create", "session.fork", "session.prompt", "session.queueAdd", "session.queueRetry"} {
		if gate.TryAdmit(method) {
			t.Fatalf("%s was admitted while draining", method)
		}
	}
	// Stop and read-only inspection must keep working during a drain so an
	// operator can always cancel or observe in-flight work.
	for _, method := range []string{"session.abort", "session.getMessages", "runtime.status"} {
		if !gate.TryAdmit(method) {
			t.Fatalf("%s was refused while draining", method)
		}
	}
	if !gate.Release("session.prompt") {
		t.Fatal("releasing the pre-drain prompt did not settle the gate")
	}
	waited := make(chan struct{})
	go func() {
		gate.WaitForDrain(context.Background())
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForDrain did not observe an empty in-flight set")
	}
}

// BeginDrain refuses new work immediately, and WaitForDrain does not report
// completion until work admitted before the drain has settled.
func TestAdmissionGateWaitsForAdmittedWork(t *testing.T) {
	gate := NewAdmissionGate(0)
	if !gate.TryAdmit("session.prompt") {
		t.Fatal("prompt refused before drain")
	}
	gate.BeginDrain()
	if !gate.Quiescing() {
		t.Fatal("drain did not become visible")
	}
	if gate.TryAdmit("session.prompt") {
		t.Fatal("new prompt was admitted while draining")
	}
	drained := make(chan struct{})
	go func() {
		gate.WaitForDrain(context.Background())
		close(drained)
	}()
	select {
	case <-drained:
		t.Fatal("WaitForDrain settled before the admitted prompt released")
	case <-time.After(20 * time.Millisecond):
	}
	if !gate.Release("session.prompt") {
		t.Fatal("release did not report the in-flight set empty")
	}
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForDrain did not observe the released prompt")
	}
}

// AUX-19: queueing a message or retrying a blocked one can schedule a
// follow-up prompt, so both are run-creating admission. An admission granted
// before the drain is retained and accounted for by WaitForDrain, while new
// queueAdd/queueRetry work is refused for the whole quiesce window.
func TestAdmissionGateAccountsForQueuedFollowUpAdmission(t *testing.T) {
	gate := NewAdmissionGate(0)
	if !gate.TryAdmit("session.queueAdd") {
		t.Fatal("queueAdd was refused before drain began")
	}
	gate.BeginDrain()
	for _, method := range []string{"session.queueAdd", "session.queueRetry"} {
		if gate.TryAdmit(method) {
			t.Fatalf("%s was admitted while draining", method)
		}
	}
	drained := make(chan struct{})
	go func() {
		gate.WaitForDrain(context.Background())
		close(drained)
	}()
	select {
	case <-drained:
		t.Fatal("WaitForDrain settled before the admitted queued follow-up released")
	case <-time.After(20 * time.Millisecond):
	}
	if !gate.Release("session.queueAdd") {
		t.Fatal("release did not report the in-flight set empty")
	}
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForDrain did not observe the released queued follow-up")
	}
}

// A cancelled wait must return instead of hanging, and must not corrupt the
// gate for a later drain.
func TestAdmissionGateWaitForDrainHonorsCancellation(t *testing.T) {
	gate := NewAdmissionGate(0)
	if !gate.TryAdmit("session.prompt") {
		t.Fatal("prompt refused before drain")
	}
	gate.BeginDrain()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	gate.WaitForDrain(cancelled)
	if time.Since(started) > time.Second {
		t.Fatal("cancelled drain wait blocked")
	}
	gate.Release("session.prompt")
	gate.WaitForDrain(context.Background())
}

// AUX-19 admission accounting: only a gated method that was admitted can
// settle a slot. A non-gated request completing during a drain must leave the
// admitted prompt's accounting intact so WaitForDrain keeps blocking.
func TestAdmissionGateNonGatedReleaseDoesNotSettleAdmittedWork(t *testing.T) {
	gate := NewAdmissionGate(0)
	if !gate.TryAdmit("session.prompt") {
		t.Fatal("prompt refused before drain")
	}
	gate.BeginDrain()
	// A read-only request admitted during the drain is counted zero times.
	if !gate.TryAdmit("session.getMessages") {
		t.Fatal("read-only method was refused while draining")
	}
	if gate.Release("session.getMessages") {
		t.Fatal("a non-gated release reported the gated in-flight set empty")
	}
	drained := make(chan struct{})
	go func() {
		gate.WaitForDrain(context.Background())
		close(drained)
	}()
	select {
	case <-drained:
		t.Fatal("a non-gated request settled WaitForDrain while a prompt was admitted")
	case <-time.After(20 * time.Millisecond):
	}
	if !gate.Release("session.prompt") {
		t.Fatal("releasing the admitted prompt did not report the set empty")
	}
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForDrain did not settle after the admitted prompt released")
	}
}

// AUX-19 run handoff: an admission adopted by the asynchronous run is settled
// exactly once when that run ends, and the transport's post-dispatch settle is
// a no-op. An unadopted admission is settled by whoever still owns it.
func TestRunAdmissionHandoffSettlesExactlyOnce(t *testing.T) {
	settles := 0
	release := func() { settles++ }

	adopted := newRunAdmission(release)
	if !adopted.Adopt() {
		t.Fatal("Adopt refused a fresh admission")
	}
	if !adopted.Adopted() {
		t.Fatal("adopted admission did not report adoption")
	}
	// The transport settles after dispatch only when no run adopted the
	// admission; this path must not release an adopted run.
	if !adopted.Adopted() {
		adopted.Settle()
	}
	if settles != 0 {
		t.Fatalf("transport settled an adopted run: settles=%d", settles)
	}
	adopted.Settle()
	adopted.Settle()
	if settles != 1 {
		t.Fatalf("adopted run settled %d times, want 1", settles)
	}

	dispatchOwned := newRunAdmission(release)
	if dispatchOwned.Adopted() {
		t.Fatal("an unadopted admission reported adoption")
	}
	dispatchOwned.Settle()
	dispatchOwned.Settle()
	if settles != 2 {
		t.Fatalf("dispatch-owned admission settled %d times total, want 2", settles)
	}
}

// The run admission must survive the handler boundary through the request
// context and be absent from a context that never carried one.
func TestRunAdmissionContextRoundTrip(t *testing.T) {
	admission := newRunAdmission(func() {})
	carried := withRunAdmission(context.Background(), admission)
	if runAdmissionFromContext(carried) != admission {
		t.Fatal("run admission did not survive the request context")
	}
	if runAdmissionFromContext(context.Background()) != nil {
		t.Fatal("a context without a run admission reported one")
	}
	if withRunAdmission(context.Background(), nil) == nil {
		t.Fatal("withRunAdmission dropped the base context")
	}
}
