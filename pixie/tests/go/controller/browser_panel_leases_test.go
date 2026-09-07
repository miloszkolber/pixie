package controller_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/controller"
)

func TestBrowserPanelHeartbeatUsesAuthenticatedLiveOwnershipAndStopsOnShutdown(t *testing.T) {
	renewals := make(chan []string, 2)
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+browserPanelToken {
			t.Error("lease request lost authentication")
		}
		if r.URL.Path == "/v1/browser/leases" {
			var body struct{ Sessions []string }
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid lease request")
			}
			renewals <- body.Sessions
			<-r.Context().Done()
			close(cancelled)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"outcome": "completed", "command": "close", "code": 0})
	}))
	defer server.Close()
	panels := controller.NewBrowserPanels(controller.AuthConfig{BrowserURL: server.URL, BrowserEnabled: true, BrowserToken: browserPanelToken}, server.Client())
	defer panels.CloseAll(context.Background())
	live, err := panels.Open("client", "project")
	if err != nil {
		t.Fatal(err)
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
		if len(ids) != 1 || ids[0] != live {
			t.Fatalf("renewal included absent ownership: %v", ids)
		}
	case <-time.After(time.Second):
		t.Fatal("lease renewal did not start")
	}
	panels.CloseAll(context.Background())
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("shutdown left lease renewal running")
	}
}
