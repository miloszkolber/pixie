package controller

import (
	"encoding/json"
	"testing"
)

// AUX-16: an appended list snapshot is sent as a delta carrying rev, baseRev
// and appended, while the first frame and a broken chain carry the full data.
func TestStreamTrackerEmitsAppendDeltaAndResyncOnBrokenChain(t *testing.T) {
	tracker := newStreamTracker()
	first := tracker.stamp("agent.event", []byte(`[{"id":"a"}]`))
	seq, rev, baseRev, data, appended := decodeStreamFrame(t, first)
	if seq != 1 || rev != 1 || baseRev != 0 || appended != nil {
		t.Fatalf("first frame = seq %d rev %d base %d appended=%s", seq, rev, baseRev, appended)
	}
	if string(data) != `[{"id":"a"}]` {
		t.Fatalf("first frame data = %s", data)
	}

	second := tracker.stamp("agent.event", []byte(`[{"id":"a"},{"id":"b"}]`))
	seq, rev, baseRev, data, appended = decodeStreamFrame(t, second)
	if seq != 2 || rev != 2 || baseRev != 1 {
		t.Fatalf("append frame = seq %d rev %d base %d", seq, rev, baseRev)
	}
	if data != nil {
		t.Fatalf("append frame repeated the full payload: %s", data)
	}
	if string(appended) != `[{"id":"b"}]` {
		t.Fatalf("append suffix = %s", appended)
	}

	// A replacement in the middle is not an append: emit the full payload so
	// the client resyncs instead of silently keeping stale entries.
	broken := tracker.stamp("agent.event", []byte(`[{"id":"x"},{"id":"b"}]`))
	seq, rev, baseRev, data, appended = decodeStreamFrame(t, broken)
	if seq != 3 || baseRev != 0 || appended != nil {
		t.Fatalf("broken chain = seq %d base %d appended=%s", seq, baseRev, appended)
	}
	if string(data) != `[{"id":"x"},{"id":"b"}]` {
		t.Fatalf("broken chain data = %s", data)
	}
	if rev != 3 {
		t.Fatalf("broken chain rev = %d, want 3", rev)
	}

	// Each channel keeps an independent revision chain but a shared sequence.
	other := tracker.stamp("session.deleted", []byte(`{"projectId":"p","sessionId":"s"}`))
	seq, rev, baseRev, data, _ = decodeStreamFrame(t, other)
	if seq != 4 || rev != 1 || baseRev != 0 || data == nil {
		t.Fatalf("other channel = seq %d rev %d base %d data=%s", seq, rev, baseRev, data)
	}
}

func decodeStreamFrame(t *testing.T, payload []byte) (uint64, uint64, uint64, json.RawMessage, json.RawMessage) {
	t.Helper()
	var frame struct {
		Seq      uint64          `json:"seq"`
		Rev      uint64          `json:"rev"`
		BaseRev  uint64          `json:"baseRev"`
		Data     json.RawMessage `json:"data"`
		Appended json.RawMessage `json:"appended"`
	}
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatalf("decode stream frame %s: %v", payload, err)
	}
	return frame.Seq, frame.Rev, frame.BaseRev, frame.Data, frame.Appended
}

// AUX-16: the heartbeat is a padded, secret-free control frame so an idle
// intermediary is exercised without carrying any session data.
func TestSocketHeartbeatFrameIsPaddedControlData(t *testing.T) {
	if len(socketHeartbeatPayload) < 64 {
		t.Fatalf("heartbeat payload too small to pad an idle connection: %d bytes", len(socketHeartbeatPayload))
	}
	var frame map[string]any
	if err := json.Unmarshal(socketHeartbeatPayload, &frame); err != nil {
		t.Fatalf("heartbeat is not valid JSON: %v", err)
	}
	if frame["channel"] != "server.heartbeat" {
		t.Fatalf("heartbeat channel = %#v", frame["channel"])
	}
}
