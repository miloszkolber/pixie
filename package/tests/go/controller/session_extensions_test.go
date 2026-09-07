package controller_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/workspace"
)

func TestSessionExtensionsUsePiKeysForProjectionAndRemoval(t *testing.T) {
	server, calls := sessionExtensionPi(t, []any{map[string]any{"extensionKey": "active-key", "extension": map[string]any{"type": "streamable_http", "name": "developer"}}})
	defer server.Close()
	runtime, host, projectID := sessionExtensionRuntime(t, server.URL)
	defer runtime.Shutdown(context.Background())
	connection := dialRuntimeSocket(t, context.Background(), host, "client")
	projectID = callBrowser(t, connection, "open", "project.open", map[string]any{"path": projectID})["result"].(map[string]any)["id"].(string)
	createResponse := callBrowser(t, connection, "create", "session.create", map[string]any{"projectId": projectID})
	if createResponse["ok"] != true {
		t.Fatalf("session create failed: %#v", createResponse)
	}
	session := createResponse["result"].(map[string]any)
	sessionID := session["sessionId"].(string)
	result := callBrowser(t, connection, "list", "session.extensionList", map[string]any{"projectId": projectID, "sessionId": sessionID})
	if result["ok"] != true {
		t.Fatalf("session extension list failed: %#v", result)
	}
	entries := result["result"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["extensionKey"] != "active-key" {
		t.Fatalf("session extension projection: %#v", result)
	}
	callBrowser(t, connection, "remove", "session.extensionRemove", map[string]any{"projectId": projectID, "sessionId": sessionID, "extensionKey": "active-key", "name": "wrong-name"})
	for _, call := range calls.snapshot() {
		if call.method == "pi.session.extensions.remove" && call.params["extensionKey"] == "active-key" && call.params["name"] == nil {
			return
		}
	}
	t.Fatal("removal did not use extensionKey")
}

func TestSessionExtensionsRejectMissingAndDuplicateKeys(t *testing.T) {
	for name, extensions := range map[string][]any{
		"missing":   {map[string]any{"extension": map[string]any{"type": "streamable_http", "name": "developer"}}},
		"duplicate": {map[string]any{"extensionKey": "same", "extension": map[string]any{"type": "streamable_http", "name": "developer"}}, map[string]any{"extensionKey": "same", "extension": map[string]any{"type": "streamable_http", "name": "tutorial"}}},
	} {
		t.Run(name, func(t *testing.T) {
			server, _ := sessionExtensionPi(t, extensions)
			defer server.Close()
			runtime, host, projectID := sessionExtensionRuntime(t, server.URL)
			defer runtime.Shutdown(context.Background())
			connection := dialRuntimeSocket(t, context.Background(), host, "client")
			projectID = callBrowser(t, connection, "open", "project.open", map[string]any{"path": projectID})["result"].(map[string]any)["id"].(string)
			createResponse := callBrowser(t, connection, "create", "session.create", map[string]any{"projectId": projectID})
			if createResponse["ok"] != true {
				t.Fatalf("session create failed: %#v", createResponse)
			}
			session := createResponse["result"].(map[string]any)
			response := callBrowser(t, connection, "list", "session.extensionList", map[string]any{"projectId": projectID, "sessionId": session["sessionId"]})
			if response["ok"] != false {
				t.Fatalf("malformed session extensions accepted: %#v", response)
			}
		})
	}
}

type sessionExtensionCall struct {
	method string
	params map[string]any
}

type sessionExtensionCalls struct {
	mu     sync.Mutex
	values []sessionExtensionCall
}

func (c *sessionExtensionCalls) append(call sessionExtensionCall) {
	c.mu.Lock()
	c.values = append(c.values, call)
	c.mu.Unlock()
}

func (c *sessionExtensionCalls) snapshot() []sessionExtensionCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]sessionExtensionCall(nil), c.values...)
}

func sessionExtensionPi(t *testing.T, extensions []any) (*httptest.Server, *sessionExtensionCalls) {
	t.Helper()
	calls := &sessionExtensionCalls{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		for {
			_, payload, err := connection.Read(context.Background())
			if err != nil {
				return
			}
			var rpc struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params map[string]any  `json:"params"`
			}
			if json.Unmarshal(payload, &rpc) != nil {
				return
			}
			calls.append(sessionExtensionCall{rpc.Method, rpc.Params})
			result := any(map[string]any{})
			switch rpc.Method {
			case "runtime.hello":
				result = piInitializeResponse()
			case "pi.session.extensions.list":
				result = map[string]any{"extensions": extensions}
			case "session.list":
				result = map[string]any{"sessions": []any{}}
			case "session.create":
				result = map[string]any{"sessionId": "chat"}
			}
			if len(rpc.ID) > 0 {
				_ = writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
			}
		}
	}))
	t.Cleanup(server.Close)
	return server, calls
}

func sessionExtensionRuntime(t *testing.T, piURL string) (*controller.Runtime, string, string) {
	root := t.TempDir()
	policy, _ := workspace.NewPathPolicy([]string{root}, false)
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	runtime, err := controller.NewRuntime(controller.RuntimeConfig{Host: "127.0.0.1", Port: port, DataDir: t.TempDir(), StaticDir: t.TempDir(), PiURL: "ws" + strings.TrimPrefix(piURL, "http"), Policy: policy, Getenv: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	host, err := runtime.Start()
	if err != nil {
		t.Fatal(err)
	}
	return runtime, host, root
}

func dialRuntimeSocket(t *testing.T, ctx context.Context, host, client string) *websocket.Conn {
	t.Helper()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(host, "http")+"/ws?client="+client, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {host}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.CloseNow() })
	return connection
}

func callBrowser(t *testing.T, connection *websocket.Conn, id, method string, params map[string]any) map[string]any {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	_ = connection.Write(context.Background(), websocket.MessageText, payload)
	for {
		_, raw, err := connection.Read(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var response map[string]any
		_ = json.Unmarshal(raw, &response)
		if response["id"] == id {
			return response
		}
	}
}
