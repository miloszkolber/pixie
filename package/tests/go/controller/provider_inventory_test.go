package controller_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
)

// Block field-presence resolution while allowing unrelated mutations and
// inventory requests over the same Pi connection.
func inventoryFixture(t *testing.T) (*controller.PiAdmin, *atomic.Int32, *atomic.Int32, <-chan struct{}, func()) {
	t.Helper()
	var lists, reads atomic.Int32
	var removed atomic.Bool
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			_, raw, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var rpc struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if json.Unmarshal(raw, &rpc) != nil || len(rpc.ID) == 0 {
				continue
			}
			var result any = map[string]any{}
			switch rpc.Method {
			case "runtime.hello":
				result = piInitializeResponse()
			case "pi.providers.list":
				lists.Add(1)
				result = map[string]any{"entries": []any{map[string]any{"providerId": "local-cli", "configured": !removed.Load(), "available": true, "configKeys": []any{map[string]any{"name": "COMMAND", "required": true, "default": "cli"}}}}}
			case "pi.providers.config.read":
				reads.Add(1)
				once.Do(func() { close(started) })
				go func(id json.RawMessage) {
					select {
					case <-release:
						_ = writeRPC(conn, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"fields": []any{map[string]any{"isSet": true}}}})
					case <-r.Context().Done():
					}
				}(rpc.ID)
				continue
			case "pi.providers.config.delete":
				removed.Store(true)
			}
			if writeRPC(conn, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result}) != nil {
				return
			}
		}
	}))
	client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", nil)
	t.Cleanup(server.Close)
	t.Cleanup(client.Close)
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	admin := controller.NewPiAdmin(client, controller.NewSettings(persist.Store{Dir: t.TempDir()}, nil))
	return admin, &lists, &reads, started, unblock
}

func TestProviderInventorySharesPresenceChecksAndIsolatesCallerCancellation(t *testing.T) {
	admin, lists, reads, started, release := inventoryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, cancelFirst := context.WithCancel(ctx)
	firstDone := make(chan error, 1)
	go func() { _, err := admin.ProviderStatus(first); firstDone <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	results := make(chan error, 12)
	for i := range 12 {
		go func() {
			var err error
			if i%2 == 0 {
				_, err = admin.ProviderStatus(ctx)
			} else {
				_, err = admin.Models(ctx)
			}
			results <- err
		}()
	}
	// Keep the upstream request pending while the concurrent consumers join.
	time.Sleep(100 * time.Millisecond)
	cancelFirst()
	if err := <-firstDone; err == nil {
		t.Fatal("cancelled caller continued waiting")
	}
	release()
	for range 12 {
		if err := <-results; err != nil {
			t.Fatalf("one caller cancelled shared work: %v", err)
		}
	}
	if lists.Load() != 1 || reads.Load() != 1 {
		t.Fatalf("duplicated inventory work: lists=%d, presence=%d", lists.Load(), reads.Load())
	}
	if _, err := admin.ProviderStatus(ctx); err != nil {
		t.Fatal(err)
	}
	if lists.Load() != 2 || reads.Load() != 2 {
		t.Fatal("completed inventory was cached")
	}
}

func TestProviderLogoutDoesNotJoinPreMutationInventory(t *testing.T) {
	admin, lists, _, started, release := inventoryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	old := make(chan error, 1)
	go func() { _, err := admin.ProviderStatus(ctx); old <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := admin.LogoutProvider(ctx, "local-cli"); err != nil {
		t.Fatal(err)
	}
	current, err := admin.ProviderStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current["providers"].([]map[string]any)[0]["configured"] != false || lists.Load() != 2 {
		t.Fatalf("logout reused old provider configuration: %#v", current)
	}
	release()
	if err := <-old; err != nil {
		t.Fatal(err)
	}
}
