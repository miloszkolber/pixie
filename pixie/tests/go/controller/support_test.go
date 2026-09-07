package controller_test

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/coder/websocket"
	piwire "github.com/miloszkolber/pixie/internal/piprotocol"
)

type recordingEvents struct {
	mu      sync.Mutex
	methods []string
}

func (e *recordingEvents) SessionUpdate(context.Context, piwire.SessionNotification) error {
	return nil
}

func (e *recordingEvents) Extension(_ context.Context, method string, _ json.RawMessage) error {
	e.mu.Lock()
	e.methods = append(e.methods, method)
	e.mu.Unlock()
	return nil
}

func (e *recordingEvents) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.methods...)
}

func piInitializeResponse() map[string]any {
	return map[string]any{"protocolVersion": 1, "runtimeId": "fixture-runtime", "version": "0.85.1", "capabilities": map[string]any{"sessions": 1, "providers": 1, "mcp": 1, "agents": 1, "plans": 1}}
}

func writeRPC(connection *websocket.Conn, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return connection.Write(context.Background(), websocket.MessageText, payload)
}
