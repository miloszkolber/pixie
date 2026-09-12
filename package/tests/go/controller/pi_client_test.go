package controller_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	piwire "github.com/miloszkolber/pixie/contracts/piprotocol"
	"github.com/miloszkolber/pixie/internal/controller"
)

func TestPiClientFramesPiAndOrdersNotifications(t *testing.T) {
	serverErrors := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-secret" {
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			serverErrors <- err
			return
		}
		defer connection.CloseNow()
		for {
			messageType, payload, err := connection.Read(context.Background())
			if err != nil {
				return
			}
			if messageType != websocket.MessageText || strings.ContainsRune(string(payload), '\n') {
				serverErrors <- errors.New("Pi was not framed as one compact text message")
				return
			}
			var rpc struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(payload, &rpc); err != nil {
				serverErrors <- err
				return
			}
			switch rpc.Method {
			case "runtime.hello":
				var params map[string]any
				if err := json.Unmarshal(rpc.Params, &params); err != nil || params["protocolVersion"] != float64(1) {
					serverErrors <- errors.New("invalid Pi initialization")
					return
				}
				if err := writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": piInitializeResponse()}); err != nil {
					serverErrors <- err
					return
				}
			case "pi.providers.list":
				if err := writeRPC(connection, map[string]any{"jsonrpc": "2.0", "method": "pi.session.update", "params": map[string]any{"kind": "fixture"}}); err != nil {
					serverErrors <- err
					return
				}
				if err := writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": map[string]any{"providers": []any{}}}); err != nil {
					serverErrors <- err
					return
				}
			}
		}
	}))
	defer server.Close()

	events := &recordingEvents{}
	client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "test-secret", "test", events)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := client.CallPi(ctx, "pi.providers.list", map[string]any{})
	if err != nil || string(result) != `{"providers":[]}` {
		t.Fatalf("provider response: %s, %v", result, err)
	}
	if methods := events.snapshot(); len(methods) != 1 || methods[0] != "pi.session.update" {
		t.Fatalf("notification was not handled before the response: %#v", methods)
	}
	_, profile, err := client.Profile(ctx)
	if err != nil || profile.BootID != "fixture-boot" || !profile.Pi || !profile.Compatible || !profile.Operations.DeleteSession || !profile.Operations.PromptImage || !profile.Operations.HTTPMCP {
		t.Fatalf("unexpected capability profile: %#v, %v", profile, err)
	}
	select {
	case err := <-serverErrors:
		t.Fatal(err)
	default:
	}
}

func TestInvalidProtocolModeFailsStartup(t *testing.T) {
	_, err := controller.NewRuntime(controller.RuntimeConfig{
		Getenv: func(key string) string {
			if key == piwire.HostProtocolEnvVar {
				return "bogus"
			}
			return ""
		},
	})
	if err == nil || !strings.Contains(err.Error(), piwire.HostProtocolEnvVar) {
		t.Fatalf("runtime accepted an invalid protocol mode: %v", err)
	}
}

func TestPiClientV1HelloBytesUnchanged(t *testing.T) {
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		_, payload, err := connection.Read(context.Background())
		if err != nil {
			return
		}
		received <- string(payload)
		var rpc struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(payload, &rpc)
		_ = writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": piInitializeResponse()})
		for {
			if _, _, err := connection.Read(context.Background()); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", nil)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _, _ = client.Ready(ctx) }()
	select {
	case frame := <-received:
		if frame != `{"id":1,"method":"runtime.hello","params":{"protocolVersion":1}}` {
			t.Fatalf("v1 hello bytes moved: %s", frame)
		}
	case <-time.After(time.Second):
		t.Fatal("v1 hello was not sent")
	}
}

func TestPiClientV2AdapterRoundTripAndEvent(t *testing.T) {
	helloParams := make(chan map[string]any, 1)
	serverErrors := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-secret" {
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			serverErrors <- err
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
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(payload, &rpc); err != nil {
				serverErrors <- err
				return
			}
			switch rpc.Method {
			case "runtime.hello":
				var params map[string]any
				if err := json.Unmarshal(rpc.Params, &params); err != nil {
					serverErrors <- err
					return
				}
				helloParams <- params
				if err := writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": piInitializeV2Response()}); err != nil {
					serverErrors <- err
					return
				}
			case "pi.providers.list":
				if err := writeRPC(connection, map[string]any{"jsonrpc": "2.0", "method": "pi.session.update", "params": map[string]any{"kind": "v2"}}); err != nil {
					serverErrors <- err
					return
				}
				if err := writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": map[string]any{"providers": []any{}}}); err != nil {
					serverErrors <- err
					return
				}
			}
		}
	}))
	defer server.Close()

	events := &recordingEvents{}
	client := controller.NewPiClientWithProtocol("ws"+strings.TrimPrefix(server.URL, "http"), "test-secret", "test", events, piwire.HostProtocolAuto)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, profile, err := client.Profile(ctx)
	if err != nil {
		t.Fatalf("v2 profile: %v", err)
	}
	if profile.Version != "0.85.1" || profile.BootID != "fixture-boot" || !profile.Pi || !profile.Compatible {
		t.Fatalf("unexpected v2 profile: %#v", profile)
	}
	result, err := client.CallPi(ctx, "pi.providers.list", map[string]any{})
	if err != nil || string(result) != `{"providers":[]}` {
		t.Fatalf("v2 provider response: %s, %v", result, err)
	}
	select {
	case params := <-helloParams:
		if params["protocolVersion"] != float64(1) || params["preferProtocolVersion"] != float64(2) {
			t.Fatalf("v2 hello offer = %#v", params)
		}
		supported, ok := params["supportedProtocolVersions"].([]any)
		if !ok || len(supported) != 2 || supported[0] != float64(2) || supported[1] != float64(1) {
			t.Fatalf("v2 hello supported list = %#v", params["supportedProtocolVersions"])
		}
	case <-time.After(time.Second):
		t.Fatal("v2 hello offer was not observed")
	}
	if methods := events.snapshot(); len(methods) != 1 || methods[0] != "pi.session.update" {
		t.Fatalf("v2 event was not delivered before the response: %#v", methods)
	}
	select {
	case err := <-serverErrors:
		t.Fatal(err)
	default:
	}
}

func TestPiClientV2RejectsInconsistentPeer(t *testing.T) {
	// A peer that advertises [2,1] but answers protocolVersion 1 must not be
	// silently downgraded.
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
				ID json.RawMessage `json:"id"`
			}
			if json.Unmarshal(payload, &rpc) != nil {
				return
			}
			result := piInitializeResponse()
			result["supportedProtocolVersions"] = []int{2, 1}
			_ = writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
		}
	}))
	defer server.Close()

	client := controller.NewPiClientWithProtocol("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", nil, piwire.HostProtocolAuto)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.Ready(ctx); err == nil || !strings.Contains(err.Error(), "incompatible Pi host service") {
		t.Fatalf("inconsistent peer was admitted: %v", err)
	}
}

func TestPiClientStrictV2RejectsV1Host(t *testing.T) {
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
				ID json.RawMessage `json:"id"`
			}
			if json.Unmarshal(payload, &rpc) != nil {
				return
			}
			_ = writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": piInitializeResponse()})
		}
	}))
	defer server.Close()

	client := controller.NewPiClientWithProtocol("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", nil, piwire.HostProtocolV2)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.Ready(ctx); err == nil || !strings.Contains(err.Error(), "incompatible Pi host service") {
		t.Fatalf("strict v2 client admitted a v1 host: %v", err)
	}
}

func TestPiClientUsesOperationSetWithoutProviderAdministrationGate(t *testing.T) {
	seen := make(chan string, 4)
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
			}
			if err := json.Unmarshal(payload, &rpc); err != nil {
				return
			}
			seen <- rpc.Method
			if rpc.Method == "runtime.hello" {
				_ = writeRPC(connection, map[string]any{
					"jsonrpc": "2.0",
					"id":      rpc.ID,
					"result": map[string]any{
						"protocolVersion": 1,
						"runtimeId":       "operation-set-fixture",
						"bootId":          "operation-set-boot",
						"version":         "0.85.1",
						"capabilities":    map[string]any{"sessions": 1},
						"operationSet":    map[string]bool{"session.delete": true},
					},
				})
			}
		}
	}))
	defer server.Close()

	client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", nil)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, profile, err := client.Profile(ctx)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if profile.Compatible || profile.Operations.DeleteSession || profile.OperationSet["pi.tools.call"] || !containsAll(profile.MissingRequired, "session.list", "session.create", "session.load", "session.prompt", "session.cancel") {
		t.Fatalf("unexpected operation-set profile: %#v", profile)
	}
	if _, err := client.CallPi(ctx, "pi.tools.call", map[string]any{}); err == nil || !strings.Contains(err.Error(), "pi.tools.call") {
		t.Fatalf("unsupported operation was dispatched: %v", err)
	}
	if _, err := client.CallPi(ctx, "pi.unadvertised", map[string]any{}); err == nil || !strings.Contains(err.Error(), "pi.unadvertised") {
		t.Fatalf("unadvertised operation was dispatched: %v", err)
	}
	if err := client.DeleteSession(ctx, "session"); err == nil {
		t.Fatal("unsupported delete was dispatched")
	}
	if _, err := client.NewSession(ctx, piwire.NewSessionRequest{Cwd: "/tmp", Meta: map[string]any{"thinkingLevel": "high"}}); err == nil {
		t.Fatal("create-time thinking override was dispatched")
	}
	if _, err := client.Prompt(ctx, piwire.PromptRequest{SessionId: "session", Prompt: []piwire.ContentBlock{piwire.ResourceBlock(piwire.EmbeddedResourceResource{TextResourceContents: &piwire.TextResourceContents{Text: "x", Uri: "file:///x"}})}}); err == nil {
		t.Fatal("resource prompt was dispatched")
	}
	if _, err := client.SetConfig(ctx, piwire.SetSessionConfigOptionRequest{ValueId: &piwire.SetSessionConfigOptionValueId{SessionId: "session", ConfigId: "model", Value: "provider/model"}}); err == nil {
		t.Fatal("model mutation was dispatched")
	}
	for {
		select {
		case method := <-seen:
			if method != "runtime.hello" {
				t.Fatalf("unsupported operation reached Pi: %s", method)
			}
		default:
			return
		}
	}
}

func containsAll(values []string, wanted ...string) bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	for _, value := range wanted {
		if !set[value] {
			return false
		}
	}
	return true
}

func TestPiClientSharesCancellableSetupAndReconnectsAfterReset(t *testing.T) {
	type setupRequest struct {
		connection *websocket.Conn
		id         json.RawMessage
	}
	requests := make(chan setupRequest, 4)
	var dials atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		dials.Add(1)
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
			}
			if json.Unmarshal(payload, &rpc) == nil && rpc.Method == "runtime.hello" {
				requests <- setupRequest{connection: connection, id: rpc.ID}
			}
		}
	}))
	defer server.Close()
	client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", nil)
	client.Timeout = 3 * time.Second
	defer client.Close()

	type readyResult struct {
		generation uint64
		err        error
	}
	ready := func(ctx context.Context) <-chan readyResult {
		result := make(chan readyResult, 1)
		go func() {
			generation, err := client.Ready(ctx)
			result <- readyResult{generation: generation, err: err}
		}()
		return result
	}
	takeSetup := func() setupRequest {
		t.Helper()
		select {
		case request := <-requests:
			return request
		case <-time.After(time.Second):
			t.Fatal("Pi initialization did not start")
			return setupRequest{}
		}
	}
	takeReady := func(result <-chan readyResult) readyResult {
		t.Helper()
		select {
		case value := <-result:
			return value
		case <-time.After(time.Second):
			t.Fatal("readiness did not settle")
			return readyResult{}
		}
	}
	respond := func(request setupRequest, version int) {
		t.Helper()
		response := piInitializeResponse()
		response["protocolVersion"] = version
		if err := writeRPC(request.connection, map[string]any{"jsonrpc": "2.0", "id": request.id, "result": response}); err != nil {
			t.Fatal(err)
		}
	}

	firstContext, cancelFirst := context.WithCancel(context.Background())
	first := ready(firstContext)
	setup := takeSetup()
	second := ready(context.Background())
	cancelFirst()
	if result := takeReady(first); !errors.Is(result.err, context.Canceled) {
		t.Fatalf("cancelled setup waiter: %v", result.err)
	}
	respond(setup, 1)
	if result := takeReady(second); result.err != nil || result.generation != 1 {
		t.Fatalf("shared setup: %#v", result)
	}
	if dials.Load() != 1 {
		t.Fatalf("concurrent callers opened %d Pi connections", dials.Load())
	}

	client.Reset()
	retry := ready(context.Background())
	setup = takeSetup()
	respond(setup, 1+1)
	if result := takeReady(retry); result.err == nil || !strings.Contains(result.err.Error(), "incompatible Pi host service") {
		t.Fatalf("unsupported version was accepted: %#v", result)
	}
	retry = ready(context.Background())
	setup = takeSetup()
	respond(setup, 1)
	if result := takeReady(retry); result.err != nil || result.generation != 3 {
		t.Fatalf("failed setup did not retry on a fresh generation: %#v", result)
	}
}
