package controller_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

const browserPanelToken = "browser-panel-test-token-0123456789"

func browserPanelHandler(serverURL string, client *http.Client) controller.CoreHandler {
	panels := controller.NewBrowserPanels(controller.AuthConfig{
		BrowserEnabled: true,
		BrowserToken:   browserPanelToken,
		BrowserURL:     serverURL,
	}, client)
	return controller.CoreHandler{BrowserPanels: panels}
}

func openBrowserPanel(t *testing.T, handler controller.CoreHandler) string {
	t.Helper()
	result, err := handler.Handle(context.Background(), "browser.panelOpen", []byte(`{"projectId":"project-a"}`), "client-a")
	if err != nil {
		t.Fatal(err)
	}
	panelID := result.(map[string]string)["id"]
	if len(panelID) != 20 || !strings.HasPrefix(panelID, "b-") {
		t.Fatalf("browser panel ID is not a short opaque session name: %q", panelID)
	}
	return panelID
}

func TestBrowserPanelProxyAuthenticatesWithoutLeakingToken(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		if request.URL.Path != "/v1/browser" || request.Method != http.MethodPost {
			t.Fatalf("unexpected upstream request: %s %s", request.Method, request.URL)
		}
		var body map[string]any
		if json.NewDecoder(request.Body).Decode(&body) != nil || body["command"] != "open" {
			t.Fatal("invalid browser command body")
		}
		_ = json.NewEncoder(response).Encode(map[string]any{"outcome": "completed", "command": "open", "code": 0, "stdout": "opened", "stderr": ""})
	}))
	defer server.Close()
	handler := browserPanelHandler(server.URL, server.Client())
	panelID := openBrowserPanel(t, handler)
	result, err := handler.Handle(context.Background(), "browser.panelCommand", []byte(`{"panelId":"`+panelID+`","action":{"type":"open","url":"https://example.com"}}`), "client-a")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if authorization != "Bearer "+browserPanelToken {
		t.Fatalf("missing browser authorization: %q", authorization)
	}
	if strings.Contains(string(encoded), browserPanelToken) || strings.Contains(string(encoded), "Authorization") {
		t.Fatalf("browser credential reached the client result: %s", encoded)
	}
}

func TestBrowserPanelRejectsUnknownProject(t *testing.T) {
	root := t.TempDir()
	policy, err := workspace.NewPathPolicy([]string{root}, false)
	if err != nil {
		t.Fatal(err)
	}
	projects := workspace.NewProjects(persist.Store{Dir: t.TempDir()}, policy)
	handler := controller.CoreHandler{Projects: projects, BrowserPanels: controller.NewBrowserPanels(controller.AuthConfig{}, nil)}
	if _, err := handler.Handle(context.Background(), "browser.panelOpen", []byte(`{"projectId":"unknown"}`), "client-a"); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("unknown project was accepted: %v", err)
	}
}

func TestBrowserPanelRejectsMalformedAndOversizeActionsBeforeProxying(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	handler := browserPanelHandler(server.URL, server.Client())
	panelID := openBrowserPanel(t, handler)
	for _, raw := range []string{
		`{"panelId":"` + panelID + `","action":{"type":"click","ref":"button"}}`,
		`{"panelId":"` + panelID + `","action":{"type":"open","url":"javascript:alert(1)"}}`,
		`{"panelId":"` + panelID + `","action":{"type":"fill","ref":"@field","text":"` + strings.Repeat("a", 8193) + `"}}`,
	} {
		if _, err := handler.Handle(context.Background(), "browser.panelCommand", []byte(raw), "client-a"); err == nil {
			t.Fatalf("accepted invalid browser action: %.80s", raw)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid actions reached browser service: %d", calls.Load())
	}
}

func TestBrowserPanelCloseReleasesItsServerSideSession(t *testing.T) {
	var command string
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		var body struct {
			Command string `json:"command"`
		}
		if json.NewDecoder(request.Body).Decode(&body) != nil {
			t.Fatal("invalid close request")
		}
		command = body.Command
		_ = json.NewEncoder(response).Encode(map[string]any{"outcome": "completed", "command": body.Command, "code": 0, "stdout": "", "stderr": ""})
	}))
	defer server.Close()
	handler := browserPanelHandler(server.URL, server.Client())
	panelID := openBrowserPanel(t, handler)
	if _, err := handler.Handle(context.Background(), "browser.panelClose", []byte(`{"panelId":"`+panelID+`"}`), "client-a"); err != nil {
		t.Fatal(err)
	}
	if command != "close" {
		t.Fatalf("expected close command, got %q", command)
	}
	if _, err := handler.Handle(context.Background(), "browser.panelClose", []byte(`{"panelId":"`+panelID+`"}`), "client-a"); err != nil {
		t.Fatalf("repeated close was not idempotent: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("repeated close reached browser service: %d calls", calls.Load())
	}
}

func TestBrowserPanelCloseAcceptsMissingValidIDWithoutWeakeningOwnership(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(response).Encode(map[string]any{"outcome": "completed", "command": "close", "code": 0, "stdout": "", "stderr": ""})
	}))
	defer server.Close()
	handler := browserPanelHandler(server.URL, server.Client())
	panelID := openBrowserPanel(t, handler)

	if _, err := handler.Handle(context.Background(), "browser.panelClose", []byte(`{"panelId":"`+panelID+`"}`), "client-b"); err == nil {
		t.Fatal("another client closed an existing panel")
	}
	if calls.Load() != 0 {
		t.Fatalf("unauthorized close reached browser service: %d calls", calls.Load())
	}
	if _, err := handler.Handle(context.Background(), "browser.panelClose", []byte(`{"panelId":"b-000000000000000000"}`), "client-a"); err != nil {
		t.Fatalf("missing panel from an earlier controller generation was not treated as closed: %v", err)
	}
	if _, err := handler.Handle(context.Background(), "browser.panelClose", []byte(`{"panelId":"invalid"}`), "client-a"); err == nil {
		t.Fatal("malformed panel ID was accepted")
	}
	if calls.Load() != 0 {
		t.Fatalf("missing or malformed close reached browser service: %d calls", calls.Load())
	}
	if _, err := handler.Handle(context.Background(), "browser.panelClose", []byte(`{"panelId":"`+panelID+`"}`), "client-a"); err != nil {
		t.Fatalf("owner could not close panel after rejected request: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("owner close did not reach browser service exactly once: %d calls", calls.Load())
	}
}

func TestBrowserPanelProxyReturnsStatusAndTimeoutFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(response).Encode(map[string]any{"warnings": []string{"browser busy"}})
	}))
	defer server.Close()
	handler := browserPanelHandler(server.URL, &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		request.Header.Set("X-Test-Mode", "status")
		return http.DefaultTransport.RoundTrip(request)
	})})
	panelID := openBrowserPanel(t, handler)
	if _, err := handler.Handle(context.Background(), "browser.panelCommand", []byte(`{"panelId":"`+panelID+`","action":{"type":"snapshot"}}`), "client-a"); err == nil || !strings.Contains(err.Error(), "browser busy") {
		t.Fatalf("status failure was not preserved: %v", err)
	}

	timeoutHandler := browserPanelHandler(server.URL, &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})})
	timeoutPanelID := openBrowserPanel(t, timeoutHandler)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, err := timeoutHandler.Handle(ctx, "browser.panelCommand", []byte(`{"panelId":"`+timeoutPanelID+`","action":{"type":"snapshot"}}`), "client-a"); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("timeout was not reported: %v", err)
	}
}

func TestBrowserPanelsRetryFailedCloseAndReleaseOnlyTheProject(t *testing.T) {
	var attempts atomic.Int32
	closed := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body struct {
			Session string `json:"session"`
		}
		_ = json.NewDecoder(request.Body).Decode(&body)
		closed <- body.Session
		if attempts.Add(1) == 1 {
			response.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(response).Encode(map[string]any{"warnings": []string{"retry"}})
			return
		}
		_ = json.NewEncoder(response).Encode(map[string]any{"outcome": "completed", "command": "close", "code": 0, "stdout": "", "stderr": ""})
	}))
	defer server.Close()
	panels := controller.NewBrowserPanels(controller.AuthConfig{BrowserEnabled: true, BrowserToken: browserPanelToken, BrowserURL: server.URL}, server.Client())
	panelA, err := panels.Open("client-a", "project-a")
	if err != nil {
		t.Fatal(err)
	}
	panelB, err := panels.Open("client-a", "project-b")
	if err != nil {
		t.Fatal(err)
	}
	if err := panels.Close(context.Background(), "client-a", panelA); err == nil {
		t.Fatal("failed close was accepted")
	}
	if err := panels.Close(context.Background(), "client-a", panelA); err != nil {
		t.Fatalf("retry close: %v", err)
	}
	panels.ReleaseProject(context.Background(), "project-b")
	got := make([]string, 0, 3)
	for range 3 {
		select {
		case session := <-closed:
			got = append(got, session)
		case <-time.After(time.Second):
			t.Fatal("expected close request")
		}
	}
	if attempts.Load() != 3 {
		t.Fatalf("close attempts: %d", attempts.Load())
	}
	if strings.Count(strings.Join(got, ","), panelA) != 2 || strings.Count(strings.Join(got, ","), panelB) != 1 {
		t.Fatalf("project cleanup closed unexpected sessions: %v", got)
	}
}

func TestBrowserPanelsDrainRejectsConcurrentOpenAndHonorsDeadline(t *testing.T) {
	started := make(chan struct{})
	panels := controller.NewBrowserPanels(controller.AuthConfig{BrowserEnabled: true, BrowserToken: browserPanelToken, BrowserURL: "http://browser.invalid"}, &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})})
	if _, err := panels.Open("client-a", "project-a"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		panels.CloseAll(ctx)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("drain did not begin close")
	}
	if _, err := panels.Open("client-b", "project-b"); err == nil {
		t.Fatal("open succeeded while panels were draining")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("drain ignored context deadline")
	}
}

func TestBrowserPanelOpenFailureDoesNotConsumeCapacity(t *testing.T) {
	panels := controller.NewBrowserPanels(controller.AuthConfig{}, nil)
	if _, err := panels.Open("client-a", ""); err == nil {
		t.Fatal("invalid panel project was accepted")
	}
	for index := 0; index < 16; index++ {
		if _, err := panels.Open("client-a", "project-a"); err != nil {
			t.Fatalf("open %d after failed request: %v", index, err)
		}
	}
	if _, err := panels.Open("client-a", "project-a"); err == nil {
		t.Fatal("browser panel limit was not enforced")
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestBrowserPanelCleanupRetriesWithoutAnotherClientRequest(t *testing.T) {
	var attempts atomic.Int32
	recovered := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"outcome":"completed","command":"close","code":0}`))
		select {
		case recovered <- struct{}{}:
		default:
		}
	}))
	defer server.Close()
	panels := controller.NewBrowserPanels(controller.AuthConfig{BrowserEnabled: true, BrowserToken: browserPanelToken, BrowserURL: server.URL}, server.Client())
	defer panels.CloseAll(context.Background())
	panel, err := panels.Open("client", "project")
	if err != nil {
		t.Fatal(err)
	}
	if err := panels.Close(context.Background(), "client", panel); err == nil {
		t.Fatal("failed cleanup reported success")
	}
	select {
	case <-recovered:
	case <-time.After(4 * time.Second):
		t.Fatal("cleanup had no retry owner")
	}
}
