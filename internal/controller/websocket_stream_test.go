package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// aux16FrameFixtureEnv makes TestAUX16ServerFrameFixture emit real stream
// frames for the Web UI round-trip test instead of being skipped.
const aux16FrameFixtureEnv = "PIXIE_AUX16_FRAME_FIXTURE"

// aux16FrameFixturePrefix tags the emitted JSON line so the Bun test can find
// it without depending on `go test -v` line prefixes.
const aux16FrameFixturePrefix = "AUX16_FRAME_FIXTURE="

// TestAUX16ServerFrameFixture emits frames actually produced by the server's
// stream tracker so the Web UI transport test consumes the same bytes the
// controller sends. It is skipped unless the round-trip test asks for it, so a
// plain `go test ./...` never depends on Bun.
func TestAUX16ServerFrameFixture(t *testing.T) {
	if os.Getenv(aux16FrameFixtureEnv) != "1" {
		t.Skip("stream frames are emitted only for the webui round-trip test")
	}
	tracker := newStreamTracker()
	frames := []string{
		string(tracker.stamp("project.updated", []byte(`[{"id":"a"}]`))),
		string(tracker.stamp("project.updated", []byte(`[{"id":"a"},{"id":"b"}]`))),
		string(tracker.stamp("project.updated", []byte(`[{"id":"a"},{"id":"b"},{"id":"c"}]`))),
	}
	encoded, err := json.Marshal(frames)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s%s", aux16FrameFixturePrefix, encoded)
}

type countingHandler struct {
	calls atomic.Int32
}

func (h *countingHandler) Handle(_ context.Context, _ string, raw json.RawMessage, _ string) (any, error) {
	return map[string]any{"call": h.calls.Add(1), "params": json.RawMessage(append([]byte(nil), raw...))}, nil
}

// waitForBrowserSocket waits until ServeHTTP has registered the upgraded
// socket. The upgrade response can reach the client before the identity is
// registered, so a test that publishes immediately after Dial would otherwise
// race registration; production clients sync from the first snapshot frame.
func waitForBrowserSocket(t *testing.T, server *WebSocketServer, clientKey string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		server.mu.Lock()
		socket, ok := server.sockets[clientKey]
		server.mu.Unlock()
		if ok && socket.connection != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("browser socket %q was not registered", clientKey)
		}
		time.Sleep(time.Millisecond)
	}
}

// blockHandler holds one admitted request so the socket's queue can fill.
type blockHandler struct {
	started chan struct{}
	release chan struct{}
}

func (h blockHandler) Handle(_ context.Context, _ string, _ json.RawMessage, _ string) (any, error) {
	select {
	case h.started <- struct{}{}:
	default:
	}
	<-h.release
	return map[string]any{"ok": true}, nil
}

func hostListenerPort(t *testing.T, raw string) int {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// AUX-16: snapshot-first ordering, monotonic sequence, append deltas and a
// client-driven resync on a broken chain.
func TestWebSocketStreamFramesSnapshotFirstAndDeltaOnAppend(t *testing.T) {
	welcome := func(context.Context) (any, error) { return map[string]any{"protocolVersion": 88}, nil }
	server, err := NewWebSocketServer(&countingHandler{}, welcome, AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	host := httptest.NewServer(server)
	defer host.Close()
	server.Auth.ControllerPort = hostListenerPort(t, host.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(host.URL, "http")+"/?client=stream", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {host.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	// Snapshot-first: the welcome frame arrives before any sequenced channel.
	_, raw, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var first map[string]any
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatal(err)
	}
	if first["channel"] != "server.welcome" {
		t.Fatalf("first frame = %s", raw)
	}

	publish := func(value []map[string]any) {
		t.Helper()
		if err := server.Publish(ctx, "project.updated", value); err != nil {
			t.Fatal(err)
		}
	}
	publish([]map[string]any{{"id": "a"}})
	_, raw, err = connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Channel string          `json:"channel"`
		Seq     uint64          `json:"seq"`
		Rev     uint64          `json:"rev"`
		BaseRev uint64          `json:"baseRev"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	// A snapshot must carry baseRev explicitly. Decoding into uint64 cannot
	// distinguish an omitted field from 0, so check the raw object as well.
	var snapshotFields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &snapshotFields); err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshotFields["baseRev"]; !ok {
		t.Fatalf("snapshot frame omitted baseRev: %s", raw)
	}
	if snapshot.Channel != "project.updated" || snapshot.Seq != 1 || snapshot.Rev != 1 || snapshot.BaseRev != 0 || string(snapshot.Data) != `[{"id":"a"}]` {
		t.Fatalf("snapshot frame = %s", raw)
	}

	publish([]map[string]any{{"id": "a"}, {"id": "b"}})
	_, raw, err = connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var delta struct {
		Channel  string          `json:"channel"`
		Seq      uint64          `json:"seq"`
		Rev      uint64          `json:"rev"`
		BaseRev  uint64          `json:"baseRev"`
		Data     json.RawMessage `json:"data"`
		Appended json.RawMessage `json:"appended"`
	}
	if err := json.Unmarshal(raw, &delta); err != nil {
		t.Fatal(err)
	}
	if delta.Seq != 2 || delta.Rev != 2 || delta.BaseRev != 1 || delta.Data != nil || string(delta.Appended) != `[{"id":"b"}]` {
		t.Fatalf("delta frame = %s", raw)
	}

	// A client that lost the chain asks for a fresh snapshot and receives the
	// welcome projection again before divergence.
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"resync":true}`)); err != nil {
		t.Fatal(err)
	}
	_, raw, err = connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resync map[string]any
	if err := json.Unmarshal(raw, &resync); err != nil {
		t.Fatal(err)
	}
	if resync["channel"] != "server.welcome" {
		t.Fatalf("resync response = %s", raw)
	}
}

// AUX-16: an unknown channel is dropped by the allowlist and never reaches the
// browser.
func TestWebSocketPublishAllowlistDropsUnknownChannel(t *testing.T) {
	server, err := NewWebSocketServer(&countingHandler{}, nil, AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	host := httptest.NewServer(server)
	defer host.Close()
	server.Auth.ControllerPort = hostListenerPort(t, host.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(host.URL, "http")+"/?client=allow", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {host.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	waitForBrowserSocket(t, server, "allow")
	if err := server.Publish(ctx, "internal.secret", map[string]any{"value": 1}); err != nil {
		t.Fatal(err)
	}
	if err := server.Publish(ctx, "project.updated", []map[string]any{{"id": "ok"}}); err != nil {
		t.Fatal(err)
	}
	_, raw, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "internal.secret") || strings.Contains(string(raw), `"value"`) {
		t.Fatalf("allowlist leaked an internal channel: %s", raw)
	}
	var frame map[string]any
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatal(err)
	}
	if frame["channel"] != "project.updated" {
		t.Fatalf("expected the allowed channel, got %s", raw)
	}
}

// AUX-16: exceeding the per-socket event cap sheds the socket so the client
// reconnects and resyncs instead of receiving a partial chain.
func TestWebSocketEventBackpressureCapsQueuedFrames(t *testing.T) {
	original := socketEventBackpressure
	// A zero cap sheds the first published event deterministically; a small
	// non-zero cap can be drained by the writer goroutine before five publishes,
	// which made this test depend on runner timing.
	socketEventBackpressure = 0
	defer func() { socketEventBackpressure = original }()

	handler := blockHandler{started: make(chan struct{}, 1), release: make(chan struct{})}
	server, err := NewWebSocketServer(handler, nil, AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	host := httptest.NewServer(server)
	defer host.Close()
	server.Auth.ControllerPort = hostListenerPort(t, host.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(host.URL, "http")+"/?client=slow", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {host.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	waitForBrowserSocket(t, server, "slow")
	// Block the read loop so published frames stay queued.
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"id":"block","method":"block","params":{}}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handler.started:
	case <-ctx.Done():
		t.Fatal("handler did not start")
	}
	var publishErr error
	for index := 0; index < 5; index++ {
		if err := server.Publish(ctx, "project.updated", []map[string]any{{"id": "x"}}); err != nil {
			publishErr = err
			break
		}
	}
	close(handler.release)
	if publishErr == nil || !strings.Contains(publishErr.Error(), "backpressure") {
		t.Fatalf("backpressure did not shed the socket: %v", publishErr)
	}
}
