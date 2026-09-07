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
	if err != nil || !profile.Pi || !profile.Compatible || !profile.Operations.Administration || !profile.Operations.DeleteSession || !profile.Operations.PromptImage || !profile.Operations.HTTPMCP {
		t.Fatalf("unexpected capability profile: %#v, %v", profile, err)
	}
	select {
	case err := <-serverErrors:
		t.Fatal(err)
	default:
	}
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
