package host

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const testSecret = "assistant-test-secret-0123456789abcdef"

func TestStartProvidesPrivateEndpointAndHello(t *testing.T) {
	handle, err := Start(context.Background(), Config{Host: "127.0.0.1", Port: 0, Secret: testSecret})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close(context.Background())
	if !strings.HasPrefix(handle.Endpoint(), "ws://127.0.0.1:") || !strings.HasSuffix(handle.Endpoint(), "/pi") {
		t.Fatalf("unexpected endpoint %q", handle.Endpoint())
	}
	select {
	case <-handle.Ready():
	case <-time.After(time.Second):
		t.Fatal("embedded assistant did not become ready")
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+testSecret)
	connection, _, err := websocket.Dial(context.Background(), handle.Endpoint(), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "test complete")
	request := []byte(`{"id":1,"method":"runtime.hello","params":{"protocolVersion":1}}`)
	if err := connection.Write(context.Background(), websocket.MessageText, request); err != nil {
		t.Fatal(err)
	}
	_, response, err := connection.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		ID     uint64         `json:"id"`
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.ID != 1 || envelope.Result["protocolVersion"] != float64(1) {
		t.Fatalf("hello response = %s", response)
	}
}

func TestStartRejectsNonLoopbackAndCloseIsIdempotent(t *testing.T) {
	if _, err := Start(context.Background(), Config{Host: "0.0.0.0", Secret: testSecret}); err == nil {
		t.Fatal("non-loopback bind was accepted")
	}
	handle, err := Start(context.Background(), Config{Host: "localhost", Port: 0, Secret: testSecret})
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	client := http.Client{Timeout: time.Second}
	if _, err := client.Get(strings.Replace(handle.Endpoint(), "ws://", "http://", 1) + "/../livez"); err == nil {
		// The server has been closed; this assertion merely documents that Close
		// is effective rather than testing a transport-specific error string.
		t.Fatal("closed assistant still served HTTP")
	}
}

func TestHostRejectsUnsafeRequestID(t *testing.T) {
	handle, err := Start(context.Background(), Config{Host: "127.0.0.1", Port: 0, Secret: testSecret})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close(context.Background())
	header := http.Header{}
	header.Set("Authorization", "Bearer "+testSecret)
	connection, _, err := websocket.Dial(context.Background(), handle.Endpoint(), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "test complete")
	if err := connection.Write(context.Background(), websocket.MessageText, []byte(`{"id":9007199254740992,"method":"runtime.hello","params":{"protocolVersion":1}}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := connection.Read(context.Background()); err == nil {
		t.Fatal("unsafe request ID was accepted")
	}
}

func TestStartSupervisesSelectedPiRPCCoreSessionTransport(t *testing.T) {
	agentDir := t.TempDir()
	fixture := filepath.Join(t.TempDir(), "fake-pi")
	const script = `#!/bin/sh
printf '%s\n' "$@" > "$PI_CODING_AGENT_DIR/fake-argv"
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *hello*) printf '{"id":%s,"type":"hello","protocolVersion":1,"result":{"capabilities":{"sessions":1}}}\n' "$id" ;;
    *runtime.shutdown*) printf '{"id":%s,"result":{}}\n' "$id"; exit 0 ;;
    *session.prompt*) printf '{"method":"session.event","params":{"sessionId":"fixture","event":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}}}\n'; printf '{"id":%s,"result":{"stopReason":"end_turn"}}\n' "$id" ;;
    *session.list*) printf '{"id":%s,"result":{"sessions":[{"sessionId":"fixture","cwd":"/tmp/project"}]}}\n' "$id" ;;
    *) printf '{"id":%s,"result":{"sessionId":"fixture","capabilities":{"sessions":1},"configOptions":[],"messages":[]}}\n' "$id" ;;
  esac
done
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	handle, err := Start(context.Background(), Config{
		Host:         "127.0.0.1",
		Port:         0,
		Secret:       testSecret,
		AgentDir:     agentDir,
		PiExecutable: fixture,
		PiArgs:       []string{"--fixture-arg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close(context.Background())
	argv, err := os.ReadFile(filepath.Join(agentDir, "fake-argv"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(argv), "--fixture-arg") || !strings.Contains(string(argv), "--mode") {
		t.Fatalf("Pi argv = %q", argv)
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+testSecret)
	connection, _, err := websocket.Dial(context.Background(), handle.Endpoint(), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "test complete")
	call := func(id int, method string, params string) map[string]any {
		t.Helper()
		payload := []byte(`{"id":` + strconv.Itoa(id) + `,"method":"` + method + `","params":` + params + `}`)
		if err := connection.Write(context.Background(), websocket.MessageText, payload); err != nil {
			t.Fatal(err)
		}
		for {
			readContext, cancel := context.WithTimeout(context.Background(), time.Second)
			_, raw, readErr := connection.Read(readContext)
			cancel()
			if readErr != nil {
				t.Fatal(readErr)
			}
			var envelope map[string]any
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope["id"] == float64(id) {
				return envelope
			}
			if envelope["method"] != "session.event" {
				t.Fatalf("unexpected event frame %s", raw)
			}
		}
	}
	call(1, "runtime.hello", `{"protocolVersion":1}`)
	call(2, "session.list", `{}`)
	created := call(3, "session.create", `{"cwd":"/tmp/project","mcpServers":[]}`)
	if created["error"] != nil {
		t.Fatalf("create response = %#v", created)
	}
	call(4, "session.load", `{"sessionId":"fixture","cwd":"/tmp/project","mcpServers":[]}`)
	call(5, "session.prompt", `{"sessionId":"fixture","content":[{"type":"text","text":"Hi"}]}`)
	call(6, "session.configure", `{"sessionId":"fixture","configId":"thinkingLevel","value":"high"}`)
	call(7, "session.cancel", `{"sessionId":"fixture"}`)
}

func TestStartRecognizesOfficialPiRPCAndListsCurrentSession(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "pi")
	const script = `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *hello*) printf '{"id":%s,"type":"response","command":"hello","success":false,"error":"Unknown command"}\n' "$id" ;;
    *get_state*) printf '{"id":%s,"type":"response","command":"get_state","success":true,"data":{"sessionId":"official","sessionFile":"/tmp/official","cwd":"/tmp"}}\n' "$id" ;;
    *shutdown*) exit 0 ;;
  esac
done
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	handle, err := Start(context.Background(), Config{Host: "127.0.0.1", Port: 0, Secret: testSecret, PiExecutable: fixture})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close(context.Background())
	header := http.Header{"Authorization": []string{"Bearer " + testSecret}}
	connection, _, err := websocket.Dial(context.Background(), handle.Endpoint(), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "test complete")
	requests := []struct {
		id      int
		request string
	}{
		{1, `{"id":1,"method":"runtime.hello","params":{"protocolVersion":1}}`},
		{2, `{"id":2,"method":"session.list","params":{}}`},
	}
	for _, request := range requests {
		if err := connection.Write(context.Background(), websocket.MessageText, []byte(request.request)); err != nil {
			t.Fatal(err)
		}
		_, raw, err := connection.Read(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.ID != request.id {
			t.Fatalf("response id = %d, want %d", envelope.ID, request.id)
		}
		if request.id == 2 && !strings.Contains(string(envelope.Result), "official") {
			t.Fatalf("session.list result = %s", envelope.Result)
		}
	}
}

func TestPrivateAssistantRequiresSecretForWebSocketAndReadiness(t *testing.T) {
	handle, err := Start(context.Background(), Config{Host: "127.0.0.1", Port: 0, Secret: testSecret})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close(context.Background())

	endpoint := strings.TrimSuffix(strings.Replace(handle.Endpoint(), "ws://", "http://", 1), "/pi")
	response, err := http.Get(endpoint + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated readiness status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}

	wrong := http.Header{}
	wrong.Set("Authorization", "Bearer wrong-secret-012345678901234567890123")
	request, err := http.NewRequest(http.MethodGet, endpoint+"/readyz", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header = wrong
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong-secret readiness status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}

	connection, _, err := websocket.Dial(context.Background(), handle.Endpoint(), nil)
	if err == nil {
		connection.Close(websocket.StatusNormalClosure, "test complete")
		t.Fatal("unauthenticated WebSocket was accepted")
	}

	authorized := http.Header{}
	authorized.Set("Authorization", "Bearer "+testSecret)
	request, err = http.NewRequest(http.MethodGet, endpoint+"/readyz", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header = authorized
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authorized readiness status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}

func TestStartRequiresMinimumSecretLength(t *testing.T) {
	if _, err := Start(context.Background(), Config{Host: "127.0.0.1", Port: 0, Secret: strings.Repeat("x", minSecretLength-1)}); err == nil {
		t.Fatal("short assistant secret was accepted")
	}
}
