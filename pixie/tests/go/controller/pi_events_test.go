package controller_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
	piwire "github.com/miloszkolber/pixie/internal/piprotocol"
)

type nativeProjection struct {
	mu         sync.Mutex
	updates    []map[string]any
	extensions []string
}

func (e *nativeProjection) SessionUpdate(_ context.Context, n piwire.SessionNotification) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.updates = append(e.updates, n.Update)
	return nil
}
func (e *nativeProjection) Extension(_ context.Context, method string, _ json.RawMessage) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.extensions = append(e.extensions, method)
	return nil
}

func TestNativePiSnapshotAndToolEventsProjectBeforeRPCCompletes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		for {
			_, raw, err := ws.Read(r.Context())
			if err != nil {
				return
			}
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(raw, &req) != nil {
				return
			}
			result := any(piInitializeResponse())
			if req.Method == "session.load" {
				if writeRPC(ws, map[string]any{"method": "session.history", "params": map[string]any{"sessionId": "native", "messages": []any{map[string]any{"role": "user", "messageId": "u1", "content": "Inspect"}}}}) != nil {
					return
				}
				result = map[string]any{"sessionId": "native", "messages": []any{
					map[string]any{"role": "assistant", "messageId": "a1", "content": []any{map[string]any{"type": "text", "text": "Working"}, map[string]any{"type": "toolCall", "id": "call1", "name": "browser__show", "arguments": map[string]any{"url": "https://example.com"}}}},
				}}
			}
			if req.Method == "pi.providers.list" {
				result = map[string]any{"entries": []any{}}
				for _, event := range []map[string]any{
					{"type": "tool_execution_update", "toolCallId": "call1", "partialResult": map[string]any{"content": []any{map[string]any{"type": "text", "text": "Partial"}}, "details": map[string]any{"step": 1}}},
					{"type": "tool_execution_end", "toolCallId": "call1", "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "Done"}}, "details": map[string]any{"mcp": map[string]any{"app": map[string]any{"resourceUri": "ui://fixture"}, "isError": true}}}},
				} {
					if writeRPC(ws, map[string]any{"method": "session.event", "params": map[string]any{"sessionId": "native", "event": event}}) != nil {
						return
					}
				}
			}
			if writeRPC(ws, map[string]any{"id": req.ID, "result": result}) != nil {
				return
			}
		}
	}))
	defer server.Close()
	sink := &nativeProjection{}
	client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "test-secret", "test", sink)
	defer client.Close()
	if _, err := client.CallPi(t.Context(), "session.load", map[string]any{"sessionId": "native"}); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	count := len(sink.updates)
	sink.mu.Unlock()
	if count != 5 {
		t.Fatalf("snapshot not projected before return: %d", count)
	}
	if _, err := client.CallPi(t.Context(), "pi.providers.list", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.updates) != 7 {
		t.Fatalf("updates = %#v", sink.updates)
	}
	partial, final := sink.updates[5], sink.updates[6]
	if partial["status"] != "in_progress" || final["status"] != "failed" {
		t.Fatalf("tool lifecycle lost: %#v %#v", partial, final)
	}
	output := final["rawOutput"].(map[string]any)
	if output["details"] == nil || final["_meta"] == nil {
		t.Fatalf("tool details or App metadata lost: %#v", final)
	}
	if sink.updates[0]["messageId"] != "u1" || sink.updates[1]["messageId"] != "a1" {
		t.Fatal("replay identities lost")
	}
}
