package host

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	piwire "github.com/miloszkolber/pixie/contracts/piprotocol"
)

// TestNativeOperationSetTracksGeneratedCatalog binds the hand-written host
// operation map used by v2 method validation to the generated catalog so a
// protocol-catalog change cannot silently diverge.
func TestNativeOperationSetTracksGeneratedCatalog(t *testing.T) {
	operations := nativeOperationSet()
	for _, operation := range piwire.CatalogHostOperations {
		if _, present := operations[operation]; !present {
			t.Errorf("generated catalog operation %q is absent from nativeOperationSet", operation)
		}
	}
	for operation := range operations {
		if !piwire.CatalogHostOperationSet[operation] {
			t.Errorf("nativeOperationSet operation %q is absent from the generated catalog", operation)
		}
	}
}

func startV2TestHost(t *testing.T, protocolMode string) *Handle {
	t.Helper()
	handle, err := Start(context.Background(), Config{
		Host:         "127.0.0.1",
		Port:         0,
		Secret:       testSecret,
		AgentDir:     t.TempDir(),
		PiExecutable: "/bin/true",
		ProtocolMode: protocolMode,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close(context.Background()) })
	return handle
}

func dialHostConnection(t *testing.T, handle *Handle) *websocket.Conn {
	t.Helper()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+testSecret)
	connection, _, err := websocket.Dial(context.Background(), handle.Endpoint(), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close(websocket.StatusNormalClosure, "test complete") })
	return connection
}

func readHostFrame(t *testing.T, connection *websocket.Conn) []byte {
	t.Helper()
	readContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, raw, err := connection.Read(readContext)
	if err != nil {
		t.Fatalf("read host frame: %v", err)
	}
	return raw
}

func TestHostV2HelloRequestResponseAndEvent(t *testing.T) {
	handle := startV2TestHost(t, "v2")
	connection := dialHostConnection(t, handle)
	hello := []byte(`{"id":1,"method":"runtime.hello","params":{"protocolVersion":1,"supportedProtocolVersions":[2,1],"preferProtocolVersion":2}}`)
	if err := connection.Write(context.Background(), websocket.MessageText, hello); err != nil {
		t.Fatal(err)
	}
	raw := readHostFrame(t, connection)
	var helloResult struct {
		ID     int            `json:"id"`
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(raw, &helloResult); err != nil {
		t.Fatal(err)
	}
	if helloResult.ID != 1 || helloResult.Result["protocolVersion"] != float64(2) {
		t.Fatalf("v2 hello result = %s", raw)
	}
	supported, ok := helloResult.Result["supportedProtocolVersions"].([]any)
	if !ok || len(supported) != 2 || supported[0] != float64(2) || supported[1] != float64(1) {
		t.Fatalf("v2 hello advertisement = %#v", helloResult.Result["supportedProtocolVersions"])
	}
	if hostIdentity, _ := helloResult.Result["hostIdentity"].(string); hostIdentity == "" {
		t.Fatalf("v2 hello misses host identity: %s", raw)
	}
	if bootID, _ := helloResult.Result["bootId"].(string); bootID == "" {
		t.Fatalf("v2 hello misses boot identity: %s", raw)
	}
	operations, ok := helloResult.Result["operationSet"].(map[string]any)
	if !ok || operations["session.list"] != true || operations["session.delete"] != false {
		t.Fatalf("v2 hello operationSet is unsafe: %#v", helloResult.Result["operationSet"])
	}

	if err := connection.Write(context.Background(), websocket.MessageText, []byte(`{"id":2,"method":"session.list","params":{}}`)); err != nil {
		t.Fatal(err)
	}
	raw = readHostFrame(t, connection)
	var listed struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	if listed.ID != 2 || string(listed.Result) != `{"sessions":[]}` {
		t.Fatalf("v2 session.list result = %s", raw)
	}

	deadline := time.Now().Add(time.Second)
	for handle.supervisor.testSubscriberCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := handle.supervisor.publish(nativeEvent{sessionID: "session-1", event: map[string]any{"type": "session_info_changed", "name": "v2"}}); err != nil {
		t.Fatal(err)
	}
	raw = readHostFrame(t, connection)
	var event struct {
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	if event.Method != "session.event" || event.Params["sessionId"] != "session-1" {
		t.Fatalf("v2 event frame = %s", raw)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	if _, hasID := probe["id"]; hasID {
		t.Fatalf("v2 event carried a transport id: %s", raw)
	}
}

func TestHostV2RejectsV1OnlyPeer(t *testing.T) {
	handle := startV2TestHost(t, "v2")
	connection := dialHostConnection(t, handle)
	// A v1-only offer has no [2,1] intersection with a strict v2 host.
	if err := connection.Write(context.Background(), websocket.MessageText, []byte(`{"id":1,"method":"runtime.hello","params":{"protocolVersion":1}}`)); err != nil {
		t.Fatal(err)
	}
	readContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := connection.Read(readContext); err == nil {
		t.Fatal("strict v2 host served a v1-only peer")
	}
}

func TestHostAutoNegotiatesV1PeerDown(t *testing.T) {
	handle := startV2TestHost(t, "auto")
	connection := dialHostConnection(t, handle)
	if err := connection.Write(context.Background(), websocket.MessageText, []byte(`{"id":1,"method":"runtime.hello","params":{"protocolVersion":1}}`)); err != nil {
		t.Fatal(err)
	}
	raw := readHostFrame(t, connection)
	var result struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Result["protocolVersion"] != float64(1) {
		t.Fatalf("auto host did not negotiate v1: %s", raw)
	}
	// The negotiated-v1 connection serves the legacy frame shape.
	if err := connection.Write(context.Background(), websocket.MessageText, []byte(`{"id":2,"method":"session.list","params":{}}`)); err != nil {
		t.Fatal(err)
	}
	raw = readHostFrame(t, connection)
	if string(raw) != `{"id":2,"result":{"sessions":[]}}` {
		t.Fatalf("negotiated v1 response = %s", raw)
	}
}

func TestHostV1HelloRemainsUnchanged(t *testing.T) {
	// Default mode: no protocol fields, transport-only composition.
	handle, err := Start(context.Background(), Config{Host: "127.0.0.1", Port: 0, Secret: testSecret})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close(context.Background())
	connection := dialHostConnection(t, handle)
	request := []byte(`{"id":1,"method":"runtime.hello","params":{"protocolVersion":1}}`)
	if err := connection.Write(context.Background(), websocket.MessageText, request); err != nil {
		t.Fatal(err)
	}
	raw := readHostFrame(t, connection)
	var envelope struct {
		ID     uint64                     `json:"id"`
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.ID != 1 || string(envelope.Result["protocolVersion"]) != "1" {
		t.Fatalf("v1 hello result = %s", raw)
	}
	legacyKeys := []string{"protocolVersion", "runtimeId", "bootId", "version", "capabilities", "operationSet"}
	if len(envelope.Result) != len(legacyKeys) {
		t.Fatalf("v1 hello result keys moved: %s", raw)
	}
	for _, key := range legacyKeys {
		if _, ok := envelope.Result[key]; !ok {
			t.Fatalf("v1 hello lost key %q: %s", key, raw)
		}
	}
	for _, forbidden := range []string{"supportedProtocolVersions", "preferProtocolVersion", "hostIdentity", "nativeVersion"} {
		if _, ok := envelope.Result[forbidden]; ok {
			t.Fatalf("v1 hello gained negotiation field %q: %s", forbidden, raw)
		}
	}
}
