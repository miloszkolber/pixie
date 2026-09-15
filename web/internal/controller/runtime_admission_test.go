package controller

import (
	"context"
	"testing"
	"time"
)

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
	for _, method := range []string{"session.create", "session.fork", "session.prompt"} {
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
	if !gate.Release() {
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
	if !gate.Release() {
		t.Fatal("release did not report the in-flight set empty")
	}
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForDrain did not observe the released prompt")
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
	gate.Release()
	gate.WaitForDrain(context.Background())
}
