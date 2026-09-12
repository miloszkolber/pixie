package piprotocol_test

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	piwire "github.com/miloszkolber/pixie/contracts/piprotocol"
)

func testNativeConfig() piwire.NativeTransportConfig {
	config := piwire.DefaultNativeTransportConfig()
	config.WriteTimeout = 50 * time.Millisecond
	config.ReadStallTimeout = 50 * time.Millisecond
	return config
}

func TestNativeBudgetReservesControlLane(t *testing.T) {
	config := piwire.DefaultNativeTransportConfig()
	budget := piwire.NewNativeAggregateBudget(config)
	fill := config.AggregateMaxBytes - config.ControlReserveBytes
	if err := budget.ReserveOrdinary(fill); err != nil {
		t.Fatalf("ordinary fill rejected: %v", err)
	}
	// Ordinary traffic cannot consume the 1 MiB control reserve.
	if err := budget.ReserveOrdinary(1); err == nil {
		t.Fatal("ordinary traffic consumed the control reserve")
	} else if kind, _ := piwire.NativeTransportErrorKindOf(err); kind != piwire.NativeTransportBackpressure {
		t.Fatalf("reserve overflow kind = %v, want backpressure", kind)
	}
	// The reserved lane still admits a small control operation.
	if err := budget.ReserveControl(1024); err != nil {
		t.Fatalf("control lane blocked under ordinary saturation: %v", err)
	}
	if got := budget.ControlOps(); got != 1 {
		t.Fatalf("control ops = %d, want 1", got)
	}
	budget.ReleaseControl(1024)
	budget.ReleaseOrdinary(fill)
	if got := budget.Used(); got != 0 {
		t.Fatalf("budget leak: used = %d, want 0", got)
	}
}

func TestNativeBudgetControlCaps(t *testing.T) {
	config := piwire.DefaultNativeTransportConfig()
	budget := piwire.NewNativeAggregateBudget(config)
	if err := budget.ReserveControl(config.ControlMaxBytesEach + 1); err == nil {
		t.Fatal("oversize control record was admitted")
	} else if kind, _ := piwire.NativeTransportErrorKindOf(err); kind != piwire.NativeTransportTooBig {
		t.Fatalf("control size kind = %v, want too_big", kind)
	}
	for i := 0; i < config.ControlMaxOps; i++ {
		if err := budget.ReserveControl(1); err != nil {
			t.Fatalf("control op %d rejected: %v", i, err)
		}
	}
	if err := budget.ReserveControl(1); err == nil {
		t.Fatal("ninth control operation was admitted")
	} else if kind, _ := piwire.NativeTransportErrorKindOf(err); kind != piwire.NativeTransportBackpressure {
		t.Fatalf("control saturation kind = %v, want backpressure", kind)
	}
	for i := 0; i < config.ControlMaxOps; i++ {
		budget.ReleaseControl(1)
	}
	if got := budget.ControlOps(); got != 0 {
		t.Fatalf("control ops leak: %d", got)
	}
}

func TestNativePendingCorrelationAndBackpressure(t *testing.T) {
	config := piwire.DefaultNativeTransportConfig()
	config.MaxPending = 2
	pending := piwire.NewNativePendingTable(config)
	if err := pending.Add(0); err == nil {
		t.Fatal("absent correlation was admitted")
	}
	if err := pending.Add(7); err != nil {
		t.Fatalf("first correlation rejected: %v", err)
	}
	if err := pending.Add(7); err == nil {
		t.Fatal("duplicate correlation was admitted")
	} else if kind, _ := piwire.NativeTransportErrorKindOf(err); kind != piwire.NativeTransportDuplicate {
		t.Fatalf("duplicate kind = %v, want duplicate", kind)
	}
	if err := pending.Add(8); err != nil {
		t.Fatalf("second correlation rejected: %v", err)
	}
	if err := pending.Add(9); err == nil {
		t.Fatal("pending overflow was admitted without backpressure")
	} else if kind, _ := piwire.NativeTransportErrorKindOf(err); kind != piwire.NativeTransportBackpressure {
		t.Fatalf("overflow kind = %v, want backpressure", kind)
	}
	pending.Remove(7)
	if got := pending.Len(); got != 1 {
		t.Fatalf("pending len = %d, want 1", got)
	}
}

func TestNativeChildExitDrainsPendingAsUncertain(t *testing.T) {
	config := piwire.DefaultNativeTransportConfig()
	pending := piwire.NewNativePendingTable(config)
	if err := pending.Add(11); err != nil {
		t.Fatalf("add rejected: %v", err)
	}
	if err := pending.Add(12); err != nil {
		t.Fatalf("add rejected: %v", err)
	}
	drained := pending.DrainOnExit()
	if len(drained) != 2 || pending.Len() != 0 {
		t.Fatalf("drain mismatch: %v len %d", drained, pending.Len())
	}
	// Accepted work is interrupted; pre-acceptance loss is uncertain. Both
	// stay terminal and never authorize an automatic resend.
	interrupted := piwire.NativeUnsettledOutcome(true)
	uncertain := piwire.NativeUnsettledOutcome(false)
	if interrupted != piwire.HostV2DeliveryInterrupted {
		t.Fatalf("accepted exit outcome = %q, want interrupted", interrupted)
	}
	if uncertain != piwire.HostV2DeliveryUncertain {
		t.Fatalf("pre-acceptance outcome = %q, want uncertain", uncertain)
	}
	for _, state := range []piwire.HostV2DeliveryState{interrupted, uncertain} {
		if !state.IsTerminal() {
			t.Fatalf("exit outcome %q is not terminal", state)
		}
		settlement := piwire.HostV2Settlement{MutationID: "m-1", DeliveryID: "d-1", Fingerprint: "fp", State: state}
		if settlement.AllowsFollowUp() {
			t.Fatalf("exit outcome %q allowed follow-up dispatch", state)
		}
	}
}

type blockingWriter struct{ entered chan struct{} }

func (w *blockingWriter) Write(record []byte) (int, error) {
	close(w.entered)
	select {}
}

func TestNativeStalledWriteFailsWithExplicitTimeout(t *testing.T) {
	config := testNativeConfig()
	writer := piwire.NewNativeWriter(&blockingWriter{entered: make(chan struct{})}, config)
	record, err := piwire.EncodeNativeJSONLRecord(map[string]any{"method": "prompt"})
	if err != nil {
		t.Fatalf("encode rejected: %v", err)
	}
	start := time.Now()
	err = writer.WriteRecord(record)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("stalled write succeeded")
	}
	kind, ok := piwire.NativeTransportErrorKindOf(err)
	if !ok || kind != piwire.NativeTransportStalled {
		t.Fatalf("write stall kind = %v,%v, want stalled", kind, ok)
	}
	if elapsed < config.WriteTimeout || elapsed > 5*time.Second {
		t.Fatalf("write timeout not explicit: elapsed %s", elapsed)
	}
	huge := make([]byte, piwire.NativeJSONLMaxRecordBytes+2)
	for i := range huge {
		huge[i] = 'a'
	}
	if err := writer.WriteRecord(huge); err == nil {
		t.Fatal("oversize write was admitted")
	} else if kind, _ := piwire.NativeTransportErrorKindOf(err); kind != piwire.NativeTransportTooBig {
		t.Fatalf("oversize write kind = %v, want too_big", kind)
	}
}

func TestNativeWriterSerializesCorrelation(t *testing.T) {
	config := testNativeConfig()
	var writes []string
	writer := piwire.NewNativeWriter(sliceWriter{appendTo: &writes}, config)
	first, _ := piwire.EncodeNativeJSONLRecord(map[string]any{"id": 1})
	second, _ := piwire.EncodeNativeJSONLRecord(map[string]any{"id": 2})
	if err := writer.WriteRecord(first); err != nil {
		t.Fatalf("first write rejected: %v", err)
	}
	if err := writer.WriteRecord(second); err != nil {
		t.Fatalf("second write rejected: %v", err)
	}
	joined := strings.Join(writes, "")
	if got := strings.Count(joined, "\n"); got != 2 {
		t.Fatalf("serialized writes interleaved: %q", joined)
	}
	if !strings.Contains(joined, `"id":1`) || !strings.Contains(joined, `"id":2`) {
		t.Fatalf("writer correlation lost: %q", joined)
	}
}

type sliceWriter struct{ appendTo *[]string }

func (w sliceWriter) Write(record []byte) (int, error) {
	*w.appendTo = append(*w.appendTo, string(record))
	return len(record), nil
}

func TestNativeStalledReadAndChildExit(t *testing.T) {
	config := testNativeConfig()
	budget := piwire.NewNativeAggregateBudget(piwire.DefaultNativeTransportConfig())
	// No writer ever arrives: the read must fail stalled with an explicit
	// timeout instead of growing a buffer without bound.
	reader, writer := newBlockedPipe()
	defer writer.Close()
	done := make(chan struct{})
	start := time.Now()
	_, release, err := piwire.ReadNativeRecord(bufio.NewReader(reader), budget, true, config.ReadStallTimeout, done)
	elapsed := time.Since(start)
	if err == nil {
		release()
		t.Fatal("stalled read succeeded")
	}
	kind, ok := piwire.NativeTransportErrorKindOf(err)
	if !ok || kind != piwire.NativeTransportStalled {
		t.Fatalf("read stall kind = %v,%v, want stalled", kind, ok)
	}
	if elapsed < config.ReadStallTimeout {
		t.Fatalf("read timeout not explicit: elapsed %s", elapsed)
	}
	// Child exit preempts a pending read with a typed exit failure.
	reader2, writer2 := newBlockedPipe()
	defer writer2.Close()
	close(done)
	_, release, err = piwire.ReadNativeRecord(bufio.NewReader(reader2), budget, true, time.Second, done)
	if err == nil {
		release()
		t.Fatal("read after child exit succeeded")
	}
	var childExit *piwire.NativeTransportError
	if !errors.As(err, &childExit) || childExit.Kind != piwire.NativeTransportChildExit {
		t.Fatalf("exit kind = %v, want child_exit", err)
	}
	if got := budget.Used(); got != 0 {
		t.Fatalf("stalled/exit read leaked %d budget bytes", got)
	}
}

func TestNativeReadAdmitsAndReleasesOneRecord(t *testing.T) {
	config := testNativeConfig()
	budget := piwire.NewNativeAggregateBudget(piwire.DefaultNativeTransportConfig())
	reader, writer := newBlockedPipe()
	record, err := piwire.EncodeNativeJSONLRecord(map[string]any{"method": "agent_settled", "id": 3})
	if err != nil {
		t.Fatalf("encode rejected: %v", err)
	}
	go func() {
		_, _ = writer.Write(record)
	}()
	done := make(chan struct{})
	defer close(done)
	parsed, release, err := piwire.ReadNativeRecord(bufio.NewReader(reader), budget, true, time.Second, done)
	if err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}
	if !strings.Contains(string(parsed), "agent_settled") {
		t.Fatalf("record payload mismatch: %s", parsed)
	}
	if got := budget.Used(); got <= 0 {
		t.Fatalf("read did not reserve aggregate budget: used %d", got)
	}
	release()
	if got := budget.Used(); got != 0 {
		t.Fatalf("release leaked %d budget bytes", got)
	}
	_ = config
}

func newBlockedPipe() (*io.PipeReader, *io.PipeWriter) {
	return io.Pipe()
}
