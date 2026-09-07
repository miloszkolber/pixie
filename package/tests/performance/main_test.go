package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestPercentilesAndUnchangedBudget(t *testing.T) {
	values := []float64{5, 1, 3, 2, 4}
	if percentile(values, 50) != 3 || percentile(values, 95) != 5 || !slices.Equal(values, []float64{5, 1, 3, 2, 4}) {
		t.Fatal("percentiles must preserve sample order and the reference convention")
	}
	for _, test := range []struct {
		candidate, reference float64
		pass                 bool
	}{
		{1.05, 1, true}, {1.050001, 1, false}, {0.9, 1, true}, {0, 1, false}, {1, 0, false},
	} {
		if withinBudget(test.candidate, test.reference) != test.pass {
			t.Errorf("budget(%v, %v) should be %v", test.candidate, test.reference, test.pass)
		}
	}
}

func TestProbeAuthenticatesAcknowledgesAndRejectsFailures(t *testing.T) {
	acknowledged := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "http://"+r.Host {
			http.Error(w, "origin", 403)
			return
		}
		cookie, _ := r.Cookie("probe")
		switch r.URL.Path {
		case "/auth/status":
			_ = json.NewEncoder(w).Encode(map[string]bool{"authenticationEnabled": true, "authenticated": cookie != nil && cookie.Value == "authenticated"})
		case "/auth/login":
			var body map[string]string
			if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Sec-Fetch-Site") != "same-origin" || json.NewDecoder(r.Body).Decode(&body) != nil || body["token"] != "synthetic-test-token" {
				http.Error(w, "login", 403)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "probe", Value: "authenticated", Path: "/", HttpOnly: true})
			_, _ = w.Write([]byte(`{"authenticated":true}`))
		case "/ws":
			if cookie == nil || cookie.Value != "authenticated" {
				http.Error(w, "cookie", 401)
				return
			}
			connection, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer connection.CloseNow()
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			_ = connection.Write(ctx, websocket.MessageText, []byte(`{"channel":"server.welcome","data":{"protocolVersion":67}}`))
			for range 2 {
				_, body, err := connection.Read(ctx)
				if err != nil {
					return
				}
				var request struct{ ID, Method string }
				if json.Unmarshal(body, &request) != nil {
					return
				}
				if request.Method == "fail" {
					failure, _ := json.Marshal(map[string]any{"id": request.ID, "ok": false, "error": "fixture failure"})
					_ = connection.Write(ctx, websocket.MessageText, failure)
					return
				}
				// Unrelated pushes must not become latency samples or confuse the ACK.
				_ = connection.Write(ctx, websocket.MessageText, []byte(`{"channel":"fixture","data":{}}`))
				response, _ := json.Marshal(map[string]any{"id": request.ID, "ok": true, "result": []string{"fixture"}})
				_ = connection.Write(ctx, websocket.MessageText, response)
				_, body, err = connection.Read(ctx)
				var ack struct {
					ACK []string `json:"ack"`
				}
				if err == nil && json.Unmarshal(body, &ack) == nil && slices.Equal(ack.ACK, []string{request.ID}) {
					acknowledged <- request.ID
				}
			}
		}
	}))
	defer server.Close()
	endpoint := target{URL: server.URL}
	if endpoint.login("", true) == nil {
		t.Fatal("must reject a mislabeled unauthenticated comparison")
	}
	if err := endpoint.login("synthetic-test-token", false); err != nil {
		t.Fatal(err)
	}
	defer endpoint.client.CloseIdleConnections()
	connection, protocol, err := endpoint.connect()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if protocol != 67 {
		t.Fatal("lost observed protocol")
	}
	body, err := rpc(connection, "project.list", map[string]any{})
	if err != nil || string(body) != `["fixture"]` {
		t.Fatalf("response: %s, %v", body, err)
	}
	select {
	case <-acknowledged:
	case <-time.After(3 * time.Second):
		t.Fatal("missing response ACK")
	}
	if _, err := rpc(connection, "fail", nil); err == nil || !strings.Contains(err.Error(), "fixture failure") {
		t.Fatalf("accepted failed request: %v", err)
	}
}
