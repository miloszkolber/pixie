package controller

import (
	"context"
	"testing"
	"time"
)

func TestReconnectGateBoundsBurstAndDoublesBackoff(t *testing.T) {
	server := &WebSocketServer{reconnects: make(map[string]reconnectState)}
	now := time.Now()
	for attempt := 0; attempt < reconnectMaxAttempts; attempt++ {
		if _, ok := server.allowReconnectLocked("browser", now); !ok {
			t.Fatalf("attempt %d within the burst was rejected", attempt)
		}
	}
	retryAfter, ok := server.allowReconnectLocked("browser", now)
	if ok || retryAfter <= 0 {
		t.Fatalf("burst beyond %d attempts was admitted: retryAfter=%v ok=%v", reconnectMaxAttempts, retryAfter, ok)
	}
	if _, ok := server.allowReconnectLocked("browser", now.Add(retryAfter/2)); ok {
		t.Fatal("a reconnect before the backoff elapsed was admitted")
	}
	if _, ok := server.allowReconnectLocked("browser", now.Add(retryAfter)); !ok {
		t.Fatal("a reconnect after the backoff elapsed was rejected")
	}
	if len(server.reconnects) != 1 {
		t.Fatalf("reconnect state grew to %d entries for one identity", len(server.reconnects))
	}
}

func TestReplaceShedsNewIdentitiesBeyondTrackedCapacity(t *testing.T) {
	server, err := NewWebSocketServer(nil, nil, AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())

	server.mu.Lock()
	for i := 0; i < BrowserMaxConnections; i++ {
		server.sockets[string(rune('a'+i%26))+string(rune('a'+i/26))] = browserSocket{}
	}
	server.mu.Unlock()

	// Replacing an already-tracked identity is never treated as new capacity.
	if admitted, reason := server.replace("aa", browserSocket{}); !admitted {
		t.Fatalf("replacing a tracked identity was shed: %q", reason)
	}
	// A brand-new identity beyond the bound is shed before it can add a socket,
	// writer or reap timer.
	if admitted, reason := server.replace("overflow", browserSocket{}); admitted || reason == "" {
		t.Fatalf("new identity beyond capacity was admitted: admitted=%v reason=%q", admitted, reason)
	}
}

func TestConnectionCapacityAllowsTrackedIdentityAtCapacity(t *testing.T) {
	server, err := NewWebSocketServer(nil, nil, AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	server.mu.Lock()
	server.sockets["tracked"] = browserSocket{}
	for index := 0; index < BrowserMaxConnections-1; index++ {
		server.sockets[string(rune('a'+index%26))+string(rune('a'+index/26))] = browserSocket{}
	}
	server.mu.Unlock()
	if !server.hasConnectionCapacity("tracked") {
		t.Fatal("an already-tracked identity was denied a reconnect at capacity")
	}
	if server.hasConnectionCapacity("fresh") {
		t.Fatal("a new identity was admitted at capacity")
	}
}
