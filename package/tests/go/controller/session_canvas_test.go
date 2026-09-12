package controller_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/canvas"
	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/mcpserver"
	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

const testCanvasPort = 17873

func readyCanvasRegistry(t *testing.T, worker bool) *mcpserver.Registry {
	t.Helper()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	agentBrowser, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	canvasConfig := canvas.DefaultConfig(dataDir)
	if worker {
		canvasConfig.WorkerLauncher = canvas.NewDeterministicWorkerLauncher()
	}
	registry, err := mcpserver.NewRegistry(mcpserver.Config{
		Host: "127.0.0.1", Port: testCanvasPort, DataDir: dataDir,
		Binaries: &mcpserver.BinaryConfig{
			AgentBrowser: agentBrowser, BrowserConfig: configPath,
			ArtifactRoot: filepath.Join(root, "artifacts"), StateRoot: filepath.Join(root, "state"),
		},
		CanvasConfig: &canvasConfig,
	}, diagnostics.NormalizeBuild("test", "test"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Shutdown)
	if err := registry.SetEnabled("canvas", true); err != nil {
		t.Fatal(err)
	}
	return registry
}

func observeCanvasAttachments(t *testing.T, registry *mcpserver.Registry) (*controller.SessionManager, *controller.PiClient, workspace.Project, persist.Store, <-chan map[string]any) {
	t.Helper()
	attachments := make(chan map[string]any, 8)
	manager, client, project, store := newSessionManagerWithInitializeAndPublisher(
		t, nil, nil, piInitializeResponse(), nil,
		func(method string, params map[string]any) {
			if method == "mcp.attach" {
				attachments <- params
			}
		},
	)
	manager.SetMCPRegistry(registry)
	return manager, client, project, store, attachments
}

type canvasAttachment struct {
	sessionID string
	url       string
	token     string
	servers   map[string]bool
}

func nextCanvasAttachment(t *testing.T, attachments <-chan map[string]any) canvasAttachment {
	t.Helper()
	var params map[string]any
	select {
	case params = <-attachments:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for mcp.attach")
	}
	attachment := canvasAttachment{servers: make(map[string]bool)}
	attachment.sessionID, _ = params["sessionId"].(string)
	values, ok := params["servers"].([]any)
	if !ok {
		t.Fatalf("mcp.attach servers = %#v", params["servers"])
	}
	for _, value := range values {
		server, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("mcp.attach server = %#v", value)
		}
		name, _ := server["name"].(string)
		attachment.servers[name] = true
		if name != "pixie-canvas" {
			continue
		}
		attachment.url, _ = server["url"].(string)
		headers, ok := server["headers"].([]any)
		if !ok {
			t.Fatalf("Canvas headers = %#v", server["headers"])
		}
		for _, value := range headers {
			header, ok := value.(map[string]any)
			if !ok {
				t.Fatalf("Canvas header = %#v", value)
			}
			if name, _ := header["name"].(string); strings.EqualFold(name, "authorization") {
				value, _ := header["value"].(string)
				attachment.token = strings.TrimPrefix(value, "Bearer ")
			}
		}
	}
	return attachment
}

func assertNoCanvasAttachment(t *testing.T, attachments <-chan map[string]any) {
	t.Helper()
	select {
	case params := <-attachments:
		t.Fatalf("unexpected mcp.attach: %#v", params)
	case <-time.After(150 * time.Millisecond):
	}
}

func canvasHTTPStatus(t *testing.T, registry *mcpserver.Registry, token string) int {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:17873/mcp/canvas", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response := httptest.NewRecorder()
	registry.ServeHTTP(response, request)
	return response.Code
}

func TestAssembledCanvasManagementUsesControllerCookieAndSessionProjectScope(t *testing.T) {
	registry := readyCanvasRegistry(t, true)
	root := t.TempDir()
	policy, err := workspace.NewPathPolicy([]string{root}, false)
	if err != nil {
		t.Fatal(err)
	}
	store := persist.Store{Dir: t.TempDir()}
	projects := workspace.NewProjects(store, policy)
	project, err := projects.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	records := controller.NewSessionRecords(store)
	if err := records.Record(controller.ProjectSessionRecord{ProjectID: project.ID, SessionID: "chat", CWD: project.Roots[0]}); err != nil {
		t.Fatal(err)
	}
	const controllerToken = "canvas-controller-token-0123456789abcdef"
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, projects, workspace.NewFiles(projects, policy), controller.AuthConfig{
		Enabled:         true,
		ControllerToken: controllerToken,
		ControllerHost:  "127.0.0.1",
		ControllerPort:  testCanvasPort,
		PublicOrigin:    "http://127.0.0.1:17873",
	}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	handler.MCPRegistry = registry
	handler.SessionRecords = records
	login := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:17873/auth/login", strings.NewReader(`{"token":"`+controllerToken+`"}`))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Sec-Fetch-Site", "same-origin")
	login.Header.Set("Origin", "http://127.0.0.1:17873")
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK || len(loginResponse.Result().Cookies()) != 1 {
		t.Fatalf("controller login = %d %q", loginResponse.Code, loginResponse.Body.String())
	}
	cookie := loginResponse.Result().Cookies()[0]
	managementRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:17873/api/canvas/status/"+project.ID+"/chat", nil)
	managementRequest.AddCookie(cookie)
	managementRequest.Header.Set("Sec-Fetch-Site", "same-origin")
	managementResponse := httptest.NewRecorder()
	handler.ServeHTTP(managementResponse, managementRequest)
	if managementResponse.Code != http.StatusOK || !strings.Contains(managementResponse.Body.String(), `"availability":"ready"`) {
		t.Fatalf("scoped Canvas status = %d %q", managementResponse.Code, managementResponse.Body.String())
	}
	for _, target := range []string{
		"http://127.0.0.1:17873/api/canvas/" + project.ID + "/chat/status",
		"http://127.0.0.1:17873/api/canvas/status/chat",
		"http://127.0.0.1:17873/api/canvas/status?sessionId=chat&projectId=" + project.ID,
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.AddCookie(cookie)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("Canvas status alias %q = %d %q", target, response.Code, response.Body.String())
		}
	}
	if strings.Contains(managementResponse.Body.String(), controllerToken) || strings.Contains(managementResponse.Header().Get("Authorization"), controllerToken) {
		t.Fatal("controller credential leaked in Canvas management response")
	}
	queryCredential := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:17873/api/canvas/status/"+project.ID+"/chat?token=must-not-be-used", nil)
	queryCredential.AddCookie(cookie)
	queryCredential.Header.Set("Sec-Fetch-Site", "same-origin")
	queryCredentialResponse := httptest.NewRecorder()
	handler.ServeHTTP(queryCredentialResponse, queryCredential)
	if queryCredentialResponse.Code != http.StatusBadRequest {
		t.Fatalf("Canvas query credential status = %d %q", queryCredentialResponse.Code, queryCredentialResponse.Body.String())
	}
	for _, target := range []string{
		"http://127.0.0.1:17873/api/canvas/status/" + project.ID + "/other-session",
		"http://127.0.0.1:17873/api/canvas/status/other-project/chat",
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.AddCookie(cookie)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("forged Canvas scope %q = %d %q", target, response.Code, response.Body.String())
		}
	}
	withoutCookie := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:17873/api/canvas/status/"+project.ID+"/chat", nil)
	withoutCookie.Header.Set("Sec-Fetch-Site", "same-origin")
	withoutCookieResponse := httptest.NewRecorder()
	handler.ServeHTTP(withoutCookieResponse, withoutCookie)
	if withoutCookieResponse.Code != http.StatusUnauthorized {
		t.Fatalf("missing Canvas controller cookie = %d", withoutCookieResponse.Code)
	}
	if err := registry.SetEnabled("canvas", false); err != nil {
		t.Fatal(err)
	}
	disabledRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:17873/api/canvas/status/"+project.ID+"/chat", nil)
	disabledRequest.AddCookie(cookie)
	disabledRequest.Header.Set("Sec-Fetch-Site", "same-origin")
	disabledResponse := httptest.NewRecorder()
	handler.ServeHTTP(disabledResponse, disabledRequest)
	if disabledResponse.Code != http.StatusOK || !strings.Contains(disabledResponse.Body.String(), `"availability":"disabled"`) {
		t.Fatalf("disabled Canvas management status = %d %q", disabledResponse.Code, disabledResponse.Body.String())
	}
}

func TestManagedNativeSessionAttachesReadyCanvasAndKeepsObjectives(t *testing.T) {
	registry := readyCanvasRegistry(t, true)
	manager, _, project, _, attachments := observeCanvasAttachments(t, registry)
	manager.SetObjectiveURL("https://objectives.example/mcp")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	attachment := nextCanvasAttachment(t, attachments)
	if attachment.sessionID != "chat" {
		t.Fatalf("attached session = %q", attachment.sessionID)
	}
	if attachment.url != "http://127.0.0.1:17873/mcp/canvas" {
		t.Fatalf("Canvas URL = %q", attachment.url)
	}
	if attachment.token == "" || strings.Contains(attachment.url, attachment.token) {
		t.Fatalf("Canvas credential was not confined to the header: %#v", attachment)
	}
	if !attachment.servers["pixie-canvas"] || !attachment.servers["pixie_objectives"] {
		t.Fatalf("attached servers = %#v", attachment.servers)
	}
	if status := canvasHTTPStatus(t, registry, attachment.token); status == http.StatusUnauthorized {
		t.Fatalf("fresh Canvas authority was rejected with %d", status)
	}
}

func TestManagedNativeSessionSkipsDisabledCanvas(t *testing.T) {
	registry := readyCanvasRegistry(t, true)
	if err := registry.SetEnabled("canvas", false); err != nil {
		t.Fatal(err)
	}
	manager, _, project, _, attachments := observeCanvasAttachments(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	assertNoCanvasAttachment(t, attachments)
}

func TestManagedNativeSessionSkipsUnavailableCanvas(t *testing.T) {
	registry := readyCanvasRegistry(t, false)
	manager, _, project, _, attachments := observeCanvasAttachments(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	assertNoCanvasAttachment(t, attachments)
}

func TestManagedNativeSessionRevokesCanvasOnDelete(t *testing.T) {
	registry := readyCanvasRegistry(t, true)
	manager, _, project, _, attachments := observeCanvasAttachments(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	attachment := nextCanvasAttachment(t, attachments)
	if status := canvasHTTPStatus(t, registry, attachment.token); status == http.StatusUnauthorized {
		t.Fatalf("fresh Canvas authority was rejected with %d", status)
	}
	if err := manager.Delete(ctx, project.ID, "chat", project.Roots[0]); err != nil {
		t.Fatal(err)
	}
	if status := canvasHTTPStatus(t, registry, attachment.token); status != http.StatusUnauthorized {
		t.Fatalf("deleted Canvas authority status = %d", status)
	}
}

func TestManagedNativeSessionReplacesCanvasGeneration(t *testing.T) {
	registry := readyCanvasRegistry(t, true)
	manager, client, project, _, attachments := observeCanvasAttachments(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	old := nextCanvasAttachment(t, attachments)
	if status := canvasHTTPStatus(t, registry, old.token); status == http.StatusUnauthorized {
		t.Fatalf("fresh Canvas authority was rejected with %d", status)
	}
	client.Reset()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	current := nextCanvasAttachment(t, attachments)
	if current.token == old.token {
		t.Fatal("Canvas authority was reused across Pi generations")
	}
	if status := canvasHTTPStatus(t, registry, old.token); status != http.StatusUnauthorized {
		t.Fatalf("stale Canvas authority status = %d", status)
	}
	if status := canvasHTTPStatus(t, registry, current.token); status == http.StatusUnauthorized {
		t.Fatalf("replacement Canvas authority status = %d", status)
	}
}

func TestManagedNativeSessionForkAttachesCanvasToChild(t *testing.T) {
	registry := readyCanvasRegistry(t, true)
	manager, _, project, _, attachments := observeCanvasAttachments(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	parent := nextCanvasAttachment(t, attachments)
	if _, err := manager.Fork(ctx, project.ID, "chat", project.Roots[0]); err != nil {
		t.Fatal(err)
	}
	child := nextCanvasAttachment(t, attachments)
	if child.sessionID != "forked-session" {
		t.Fatalf("fork Canvas session = %q", child.sessionID)
	}
	if child.token == "" || child.token == parent.token {
		t.Fatal("fork reused the parent Canvas authority")
	}
}

func TestManagedNativeSessionFailedCreateRevokesCanvas(t *testing.T) {
	registry := readyCanvasRegistry(t, true)
	manager, _, project, store, attachments := observeCanvasAttachments(t, registry)
	manager.SetObjectiveURL("https://objectives.example/mcp")
	// Make the durable session record fail after native creation and Canvas
	// attachment. The controller must revoke the issued authority on rollback.
	if err := os.WriteFile(filepath.Join(store.Dir, "pi-project-sessions.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := manager.Create(ctx, project.ID, project.Roots[0], nil, "", "client-a"); err == nil {
		t.Fatal("failed session creation unexpectedly succeeded")
	}
	attachment := nextCanvasAttachment(t, attachments)
	if status := canvasHTTPStatus(t, registry, attachment.token); status != http.StatusUnauthorized {
		t.Fatalf("failed-create Canvas authority status = %d", status)
	}
}

func TestCanvasAndNativeAttachmentRejectForgedBinding(t *testing.T) {
	registry := readyCanvasRegistry(t, true)
	authority, err := registry.AttachCanvas("chat", 1)
	if err != nil {
		t.Fatal(err)
	}
	forged := authority.Token + "forged"
	if status := canvasHTTPStatus(t, registry, forged); status != http.StatusUnauthorized {
		t.Fatalf("forged Canvas authority status = %d", status)
	}
	if status := canvasHTTPStatus(t, registry, authority.Token); status == http.StatusUnauthorized {
		t.Fatalf("fresh Canvas authority status = %d", status)
	}

	native := mcpserver.NewNativeMCPRegistry()
	registration, err := native.Register(mcpserver.NativeMCPRegistrationRequest{
		ModuleID: "canvas", ServerID: "pixie-canvas", SessionID: "chat", Generation: 1, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := native.Authorize(mcpserver.NativeMCPAuthorization{
		RegistrationID: registration.RegistrationID,
		Credential:     registration.Credential,
		ModuleID:       registration.ModuleID,
		ServerID:       registration.ServerID,
		SessionID:      "forged-session",
		Generation:     registration.Generation,
	}); err == nil {
		t.Fatal("forged native session ID was accepted")
	}
}
