package controller_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/controller"
)

func TestBrowserPanelsLeaseRenewalSendsSortedLiveIDs(t *testing.T) {
	renewals := make(chan []string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/browser/leases" {
			var body struct {
				Sessions []string `json:"sessions"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error("invalid lease renewal body")
				return
			}
			renewals <- append([]string(nil), body.Sessions...)
			_ = json.NewEncoder(w).Encode(map[string]any{"renewed": body.Sessions})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"outcome": "completed", "command": "close", "code": 0})
	}))
	defer server.Close()
	panels := controller.NewBrowserPanels(controller.AuthConfig{BrowserURL: server.URL, BrowserEnabled: true, BrowserToken: browserPanelToken}, server.Client())
	defer panels.CloseAll(context.Background())
	live := make([]string, 0, 3)
	for range 3 {
		id, err := panels.Open("client", "project")
		if err != nil {
			t.Fatal(err)
		}
		live = append(live, id)
	}
	closed, err := panels.Open("client", "project")
	if err != nil {
		t.Fatal(err)
	}
	if err := panels.Close(context.Background(), "client", closed); err != nil {
		t.Fatal(err)
	}
	panels.ResumeCleanup()
	select {
	case ids := <-renewals:
		if len(ids) != len(live) {
			t.Fatalf("renewal included absent ownership: %v live=%v", ids, live)
		}
		if !sort.StringsAreSorted(ids) {
			t.Fatalf("lease renewal is not deterministic: %v", ids)
		}
		want := append([]string(nil), live...)
		sort.Strings(want)
		if strings.Join(ids, ",") != strings.Join(want, ",") {
			t.Fatalf("renewal = %v want %v", ids, want)
		}
		for _, id := range ids {
			if id == closed {
				t.Fatalf("renewal revived closed session %s", closed)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("lease renewal did not start")
	}
}

func TestBrowserPanelsArtifactRejectsForgedSessionContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Session string `json:"session"`
			Command string `json:"command"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Command == "screenshot" {
			forged := map[string]any{
				"outcome": "completed", "command": "screenshot", "code": 0,
				"stdout": "", "stderr": "",
				"artifact": map[string]string{"session": "other-session", "url": "/v1/artifacts/other-session/evil.png"},
			}
			_ = json.NewEncoder(w).Encode(forged)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"outcome": "completed", "command": body.Command, "code": 0, "stdout": "ok", "stderr": ""})
	}))
	defer server.Close()
	handler := browserPanelHandler(server.URL, server.Client())
	panelID := openBrowserPanel(t, handler)
	if _, err := handler.Handle(context.Background(), "browser.panelCommand", []byte(`{"panelId":"`+panelID+`","action":{"type":"screenshot"}}`), "client-a"); err == nil || !strings.Contains(err.Error(), "invalid artifact") {
		t.Fatalf("forged artifact session was accepted: %v", err)
	}
}

func TestBrowserPanelsArtifactAcceptsCaseInsensitiveImageExtension(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Session string   `json:"session"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		name := "screen.png"
		if len(body.Args) > 0 {
			name = strings.ToUpper(body.Args[0][:len(body.Args[0])-4]) + ".PNG"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"outcome": "completed", "command": body.Command, "code": 0,
			"stdout": "shot", "stderr": "",
			"artifact": map[string]string{"session": body.Session, "url": "/v1/artifacts/" + body.Session + "/" + name},
		})
	}))
	defer server.Close()
	handler := browserPanelHandler(server.URL, server.Client())
	panelID := openBrowserPanel(t, handler)
	result, err := handler.Handle(context.Background(), "browser.panelCommand", []byte(`{"panelId":"`+panelID+`","action":{"type":"screenshot"}}`), "client-a")
	if err != nil {
		t.Fatalf("case-insensitive artifact was rejected: %v", err)
	}
	encoded, _ := json.Marshal(result)
	if !strings.Contains(string(encoded), ".PNG") {
		t.Fatalf("artifact URL was not preserved: %s", encoded)
	}
}
