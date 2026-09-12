package controller_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
)

type countingHandler struct {
	calls atomic.Int32
}

func (h *countingHandler) Handle(_ context.Context, _ string, raw json.RawMessage, _ string) (any, error) {
	return map[string]any{"call": h.calls.Add(1), "params": json.RawMessage(append([]byte(nil), raw...))}, nil
}

type blockingHandler struct {
	started chan struct{}
	release chan struct{}
}

func (h blockingHandler) Handle(ctx context.Context, _ string, _ json.RawMessage, _ string) (any, error) {
	close(h.started)
	<-ctx.Done()
	<-h.release
	return nil, ctx.Err()
}

func dialBrowserSocket(t *testing.T, ctx context.Context, host, client string) *websocket.Conn {
	t.Helper()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(host, "http")+"/?client="+client, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {host}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.CloseNow() })
	return connection
}

func setWebSocketListenerPort(t *testing.T, server *controller.WebSocketServer, host *httptest.Server) {
	t.Helper()
	parsed, err := url.Parse(host.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatal(err)
	}
	server.Auth.ControllerPort = port
}

func trustedLoopbackProxy(t *testing.T) []net.IPNet {
	t.Helper()
	_, network, err := net.ParseCIDR("127.0.0.1/32")
	if err != nil {
		t.Fatal(err)
	}
	return []net.IPNet{*network}
}

func TestWebSocketReplaySurvivesReconnectUntilAcknowledged(t *testing.T) {
	handler := &countingHandler{}
	server, err := controller.NewWebSocketServer(handler, nil, controller.AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	host := httptest.NewServer(server)
	defer host.Close()
	setWebSocketListenerPort(t, server, host)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request := []byte(`{"id":"one","method":"mutate","params":{"value":1}}`)

	read := func(connection *websocket.Conn) map[string]any {
		t.Helper()
		_, raw, err := connection.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var response map[string]any
		if err := json.Unmarshal(raw, &response); err != nil || response["ok"] != true {
			t.Fatalf("unexpected response %s: %v", raw, err)
		}
		return response
	}

	first := dialBrowserSocket(t, ctx, host.URL, "stable")
	if err := first.Write(ctx, websocket.MessageText, request); err != nil {
		t.Fatal(err)
	}
	read(first)
	first.CloseNow()

	reconnected := dialBrowserSocket(t, ctx, host.URL, "stable")
	if err := reconnected.Write(ctx, websocket.MessageText, request); err != nil {
		t.Fatal(err)
	}
	result := read(reconnected)["result"].(map[string]any)
	if handler.calls.Load() != 1 || result["call"] != float64(1) {
		t.Fatalf("reconnect repeated an acknowledged mutation boundary: calls=%d result=%#v", handler.calls.Load(), result)
	}

	conflict := []byte(`{"id":"one","method":"mutate","params":{"value":2}}`)
	if err := reconnected.Write(ctx, websocket.MessageText, conflict); err != nil {
		t.Fatal(err)
	}
	_, raw, err := reconnected.Read(ctx)
	if err != nil || !strings.Contains(string(raw), "different payload") || handler.calls.Load() != 1 {
		t.Fatalf("request identity conflict: %s, %v, calls=%d", raw, err, handler.calls.Load())
	}

	if err := reconnected.Write(ctx, websocket.MessageText, []byte(`{"ack":["one"]}`)); err != nil {
		t.Fatal(err)
	}
	if err := reconnected.Write(ctx, websocket.MessageText, request); err != nil {
		t.Fatal(err)
	}
	result = read(reconnected)["result"].(map[string]any)
	if handler.calls.Load() != 2 || result["call"] != float64(2) {
		t.Fatalf("ACK did not release replay identity: calls=%d result=%#v", handler.calls.Load(), result)
	}
}

func TestReplayCacheCoalescesInflightRetriesAndHonorsCancellation(t *testing.T) {
	cache := controller.NewReplayCache()
	started, release := make(chan struct{}), make(chan struct{})
	var executions atomic.Int32
	first := make(chan []byte, 1)
	go func() {
		value, _ := cache.Run(context.Background(), "client", "request", "same", func() ([]byte, error) {
			executions.Add(1)
			close(started)
			<-release
			return []byte("stable"), nil
		})
		first <- value
	}()
	<-started
	if cache.ClearClient("client") {
		t.Fatal("cleared an active replay identity")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cache.Run(cancelled, "client", "request", "same", func() ([]byte, error) {
		t.Fatal("duplicate request executed")
		return nil, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled replay waiter: %v", err)
	}
	if _, err := cache.Run(context.Background(), "client", "request", "different", func() ([]byte, error) {
		t.Fatal("conflicting request executed")
		return nil, nil
	}); err == nil || !strings.Contains(err.Error(), "different payload") {
		t.Fatalf("conflicting replay identity: %v", err)
	}
	close(release)
	if value := <-first; string(value) != "stable" {
		t.Fatalf("first replay result: %q", value)
	}
	value, err := cache.Run(context.Background(), "client", "request", "same", func() ([]byte, error) {
		t.Fatal("settled request executed twice")
		return nil, nil
	})
	if err != nil || string(value) != "stable" || executions.Load() != 1 {
		t.Fatalf("retained result: %q, %v, executions=%d", value, err, executions.Load())
	}
	cache.Acknowledge("client", []string{"request"})
	if !cache.ClearClient("client") {
		t.Fatal("acknowledged replay namespace was not releasable")
	}
}

func TestWebSocketOriginPolicyAndConcurrentShutdown(t *testing.T) {
	const (
		configured      = "https://pixie.example"
		controllerToken = "controller-token-0123456789abcdef0123456789"
	)
	for _, test := range []struct {
		name         string
		publicOrigin string
		origin       string
		sameHost     bool
		proxy        bool
		auth         bool
		allowed      bool
	}{
		{name: "default same origin", sameHost: true, allowed: true},
		{name: "default cross origin", origin: "https://untrusted.example"},
		{name: "unauthenticated configured reverse proxy", publicOrigin: configured, origin: configured, proxy: true},
		{name: "authenticated configured reverse proxy", publicOrigin: configured, origin: configured, proxy: true, auth: true, allowed: true},
		{name: "configured internal host", publicOrigin: configured, sameHost: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := controller.AuthConfig{PublicOrigin: test.publicOrigin}
			if test.proxy {
				config.TrustedProxyCIDRs = trustedLoopbackProxy(t)
			}
			if test.auth {
				config.Enabled = true
				config.ControllerToken = controllerToken
			}
			server, err := controller.NewWebSocketServer(&countingHandler{}, nil, config)
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close(context.Background())
			host := httptest.NewServer(server)
			defer host.Close()
			setWebSocketListenerPort(t, server, host)
			origin := test.origin
			if test.sameHost {
				origin = host.URL
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			headers := http.Header{"Origin": {origin}}
			hostHeader := ""
			if test.proxy {
				hostHeader = "pixie.example"
				headers.Set("X-Forwarded-Proto", "https")
			}
			if test.auth {
				auth, err := controller.NewAuth(controllerToken)
				if err != nil {
					t.Fatal(err)
				}
				session, ok := auth.Login(controllerToken)
				if !ok {
					t.Fatal("controller token was rejected")
				}
				headers.Set("Cookie", controller.AuthCookieName+"="+session)
			}
			connection, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(host.URL, "http"), &websocket.DialOptions{Host: hostHeader, HTTPHeader: headers})
			if connection != nil {
				defer connection.CloseNow()
			}
			if test.allowed && err != nil {
				t.Fatalf("trusted origin rejected: %v", err)
			}
			if !test.allowed && (err == nil || response == nil || response.StatusCode != http.StatusForbidden) {
				t.Fatalf("untrusted origin accepted: response=%v error=%v", response, err)
			}
		})
	}

	started, release := make(chan struct{}), make(chan struct{})
	server, err := controller.NewWebSocketServer(blockingHandler{started: started, release: release}, nil, controller.AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(server)
	defer host.Close()
	setWebSocketListenerPort(t, server, host)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection := dialBrowserSocket(t, ctx, host.URL, "shutdown")
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"id":"one","method":"block","params":{}}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("handler did not start")
	}
	closed := make(chan struct{})
	go func() {
		server.Close(context.Background())
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("shutdown returned before an admitted handler settled")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("shutdown did not finish after the handler settled")
	}
}

func TestTrustedProxyRequiresControllerAuthAtWebSocketAdmission(t *testing.T) {
	trusted := controller.AuthConfig{PublicOrigin: "https://pixie.example", TrustedProxyCIDRs: trustedLoopbackProxy(t)}
	server, err := controller.NewWebSocketServer(&countingHandler{}, nil, trusted)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	request := httptest.NewRequest(http.MethodGet, "https://pixie.example/ws", nil)
	request.Host = "pixie.example"
	request.RemoteAddr = "127.0.0.1:41000"
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated trusted-proxy WebSocket request returned %d", response.Code)
	}

	lanServer, err := controller.NewWebSocketServer(&countingHandler{}, nil, controller.AuthConfig{PublicOrigin: "http://pixie.example", AllowRemoteWithout: true})
	if err != nil {
		t.Fatal(err)
	}
	defer lanServer.Close(context.Background())
	host := httptest.NewServer(lanServer)
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(host.URL, "http"), &websocket.DialOptions{
		Host:       "pixie.example",
		HTTPHeader: http.Header{"Origin": {"http://pixie.example"}},
	})
	if err != nil {
		t.Fatalf("unauthenticated LAN WebSocket request was rejected: %v", err)
	}
	connection.CloseNow()
}

func TestWebSocketAuthorityIsCheckedBeforeOriginAndUpgrade(t *testing.T) {
	const (
		publicOrigin    = "https://pixie.example"
		controllerToken = "controller-token-0123456789abcdef0123456789"
	)
	server, err := controller.NewWebSocketServer(&countingHandler{}, nil, controller.AuthConfig{Enabled: true, ControllerToken: controllerToken, PublicOrigin: publicOrigin, TrustedProxyCIDRs: trustedLoopbackProxy(t)})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := controller.NewAuth(controllerToken)
	if err != nil {
		t.Fatal(err)
	}
	session, ok := auth.Login(controllerToken)
	if !ok {
		t.Fatal("controller token was rejected")
	}
	defer server.Close(context.Background())
	host := httptest.NewServer(server)
	defer host.Close()
	setWebSocketListenerPort(t, server, host)
	endpoint := "ws" + strings.TrimPrefix(host.URL, "http")
	localHost := strings.TrimPrefix(host.URL, "http://")
	dial := func(t *testing.T, hostHeader, origin string, forwarded bool) (*websocket.Conn, *http.Response, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		headers := http.Header{}
		if origin != "" {
			headers.Set("Origin", origin)
		}
		headers.Set("Cookie", controller.AuthCookieName+"="+session)
		if forwarded {
			headers.Set("Forwarded", "for=192.0.2.1;host=evil.example;proto=http")
			headers.Set("X-Forwarded-Host", "evil.example")
			headers.Set("X-Forwarded-Proto", "https")
		}
		return websocket.Dial(ctx, endpoint, &websocket.DialOptions{Host: hostHeader, HTTPHeader: headers})
	}
	for _, test := range []struct {
		name      string
		host      string
		origin    string
		forwarded bool
		allowed   bool
	}{
		{name: "evil Host with valid Origin", host: "evil.example", origin: publicOrigin},
		{name: "valid Host without Origin", host: "pixie.example"},
		{name: "valid Host with mismatched Origin", host: "pixie.example", origin: "https://evil.example"},
		{name: "cleartext public authority without proxy proof", host: "pixie.example", origin: publicOrigin},
		{name: "cleartext loopback authority", host: localHost, origin: publicOrigin, allowed: true},
		{name: "approved proxy Host rewrite", host: "pixie.example", origin: publicOrigin, forwarded: true, allowed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			connection, response, dialErr := dial(t, test.host, test.origin, test.forwarded)
			if connection != nil {
				defer connection.CloseNow()
			}
			if test.allowed {
				if dialErr != nil {
					t.Fatalf("approved WebSocket authority rejected: %v", dialErr)
				}
				return
			}
			if dialErr == nil || response == nil || response.StatusCode != http.StatusForbidden {
				t.Fatalf("unapproved WebSocket authority accepted: response=%v error=%v", response, dialErr)
			}
		})
	}
}
