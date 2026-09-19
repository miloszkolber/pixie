package canvas_test

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/canvas"
)

func newService(t *testing.T, launcher canvas.WorkerLauncher) *canvas.Service {
	t.Helper()
	config := canvas.DefaultConfig(t.TempDir())
	config.WorkerLauncher = launcher
	service, err := canvas.New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown() })
	return service
}

func attach(t *testing.T, service *canvas.Service, session string) canvas.Authority {
	t.Helper()
	authority, err := service.Attach(session, 1)
	if err != nil {
		t.Fatal(err)
	}
	return authority
}

func TestRevisionCASRetryReadAndScreenshot(t *testing.T) {
	service := newService(t, canvas.NewDeterministicWorkerLauncher())
	authority := attach(t, service, "native-session-a")
	created, err := service.Create(context.Background(), authority)
	if err != nil {
		t.Fatal(err)
	}
	if created.Canvas.Version != 1 || created.Canvas.ID == "" {
		t.Fatalf("unexpected create result: %#v", created)
	}
	written, err := service.Write(context.Background(), authority, canvas.WriteRequest{CanvasID: created.Canvas.ID, ExpectedVersion: 1, HTML: `<html><body><main id="content"><h1>Hello</h1><p>world</p></main></body></html>`, MutationID: "mutation-1"})
	if err != nil {
		t.Fatal(err)
	}
	retry, err := service.Write(context.Background(), authority, canvas.WriteRequest{CanvasID: created.Canvas.ID, ExpectedVersion: 1, HTML: `<html><body><main id="content"><h1>Hello</h1><p>world</p></main></body></html>`, MutationID: "mutation-1"})
	if err != nil || !retry.Idempotent || retry.Version != written.Version {
		t.Fatalf("idempotent retry = %#v, err=%v", retry, err)
	}
	_, err = service.Write(context.Background(), authority, canvas.WriteRequest{CanvasID: created.Canvas.ID, ExpectedVersion: 1, HTML: `<p>different</p>`, MutationID: "mutation-2"})
	if !errors.Is(err, canvas.ErrConflict) {
		t.Fatalf("stale write error = %v, want conflict", err)
	}
	read, err := service.Read(context.Background(), authority, canvas.ReadRequest{CanvasID: created.Canvas.ID, Version: written.Version, Selector: "#content", IncludeDOM: true})
	if err != nil {
		t.Fatal(err)
	}
	if read.Version != written.Version || read.Text != "Hello world" || len(read.Matches) != 1 || read.DOM == "" {
		t.Fatalf("unexpected read result: %#v", read)
	}
	shot, err := service.Screenshot(context.Background(), authority, canvas.ScreenshotRequest{CanvasID: created.Canvas.ID, Version: written.Version, Width: 64, Height: 32})
	if err != nil {
		t.Fatal(err)
	}
	if shot.Version != written.Version || shot.MIME != "image/png" || len(shot.PNG) == 0 || shot.Artifact == "" {
		t.Fatalf("unexpected screenshot: %#v", shot)
	}
	decoded, err := png.DecodeConfig(bytes.NewReader(shot.PNG))
	if err != nil || decoded.Width != 64 || decoded.Height != 32 {
		t.Fatalf("screenshot PNG = %#v, err=%v", decoded, err)
	}
}

func TestAuthorityIsOpaqueSessionScopedAndRevocable(t *testing.T) {
	service := newService(t, canvas.NewDeterministicWorkerLauncher())
	a := attach(t, service, "native-session-a")
	b := attach(t, service, "native-session-b")
	created, err := service.Create(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if a.Token == "" || a.Token == a.SessionKey || created.Canvas.ID == a.Token {
		t.Fatal("authority or canvas identity is not opaque")
	}
	if _, err := service.Read(context.Background(), b, canvas.ReadRequest{CanvasID: created.Canvas.ID}); !errors.Is(err, canvas.ErrNotFound) {
		t.Fatalf("cross-session read = %v, want not found", err)
	}
	if err := service.Authorize(a.Token, a.SessionKey, a.Generation); err != nil {
		t.Fatal(err)
	}
	service.Revoke(a)
	if _, err := service.List(context.Background(), a); !errors.Is(err, canvas.ErrRevoked) {
		t.Fatalf("revoked list = %v, want revoked", err)
	}
}

func TestAuthorityExpiryUsesServerClock(t *testing.T) {
	now := time.Unix(100, 0)
	config := canvas.DefaultConfig(t.TempDir())
	config.Now = func() time.Time { return now }
	service, err := canvas.New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown() })
	configAuthority, err := service.Attach("native-session", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Authorize(configAuthority); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Minute)
	if err := service.Authorize(configAuthority); !errors.Is(err, canvas.ErrExpired) {
		t.Fatalf("expired authority = %v, want expired", err)
	}
}

func TestUnavailableRendererAndBoundaries(t *testing.T) {
	service := newService(t, nil)
	authority := attach(t, service, "native-session")
	created, err := service.Create(context.Background(), authority)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Screenshot(context.Background(), authority, canvas.ScreenshotRequest{CanvasID: created.Canvas.ID}); !errors.Is(err, canvas.ErrUnavailable) {
		t.Fatalf("nil worker screenshot = %v, want unavailable", err)
	}
	if _, err := service.Read(context.Background(), authority, canvas.ReadRequest{CanvasID: created.Canvas.ID, Selector: "a[href]"}); !errors.Is(err, canvas.ErrInvalidSelector) {
		t.Fatalf("invalid selector = %v, want invalid selector", err)
	}
	if _, err := service.Write(context.Background(), authority, canvas.WriteRequest{CanvasID: created.Canvas.ID, ExpectedVersion: 1, HTML: string(make([]byte, canvas.MaxHTMLBytes+1)), MutationID: "too-large"}); !errors.Is(err, canvas.ErrLimit) {
		t.Fatalf("oversized HTML = %v, want limit", err)
	}
	if _, err := service.Screenshot(context.Background(), authority, canvas.ScreenshotRequest{CanvasID: created.Canvas.ID, Width: 2048, Height: 2048, DPR: 2}); !errors.Is(err, canvas.ErrLimit) {
		t.Fatalf("oversized pixels = %v, want limit", err)
	}
}

func TestRemoveTombstoneRecreateAndRestart(t *testing.T) {
	root := t.TempDir()
	config := canvas.DefaultConfig(root)
	config.WorkerLauncher = canvas.NewDeterministicWorkerLauncher()
	first, err := canvas.New(config)
	if err != nil {
		t.Fatal(err)
	}
	a := attach(t, first, "native-session")
	created, err := first.Create(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := first.Remove(context.Background(), a, canvas.RemoveRequest{CanvasID: created.Canvas.ID, ExpectedVersion: 1, ExpectedGeneration: created.Canvas.Generation, MutationID: "remove-1"})
	if err != nil || removed.CanvasID != created.Canvas.ID {
		t.Fatalf("remove = %#v, err=%v", removed, err)
	}
	if _, err := first.Read(context.Background(), a, canvas.ReadRequest{CanvasID: created.Canvas.ID}); !errors.Is(err, canvas.ErrRemoved) {
		t.Fatalf("tombstoned read = %v, want removed", err)
	}
	secondCreate, err := first.Create(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if secondCreate.Canvas.ID == created.Canvas.ID || secondCreate.Canvas.Generation == created.Canvas.Generation {
		t.Fatal("recreated Canvas reused identity or generation")
	}
	_ = first.Shutdown()
	second, err := canvas.New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Shutdown() })
	b := attach(t, second, "native-session")
	retry, err := second.Write(context.Background(), b, canvas.WriteRequest{CanvasID: secondCreate.Canvas.ID, ExpectedVersion: 1, HTML: `<!doctype html><html><body></body></html>`, MutationID: "retry-after-restart"})
	if err != nil {
		t.Fatal(err)
	}
	secondRetry, err := second.Write(context.Background(), b, canvas.WriteRequest{CanvasID: secondCreate.Canvas.ID, ExpectedVersion: 1, HTML: `<!doctype html><html><body></body></html>`, MutationID: "retry-after-restart"})
	if err != nil || !secondRetry.Idempotent || secondRetry.Version != retry.Version {
		t.Fatalf("restart mutation retry = %#v, err=%v", secondRetry, err)
	}
	listed, err := second.List(context.Background(), b)
	if err != nil || len(listed.Items) != 1 || listed.Items[0].ID != secondCreate.Canvas.ID {
		t.Fatalf("restart list = %#v, err=%v", listed, err)
	}
	if _, err := second.Read(context.Background(), b, canvas.ReadRequest{CanvasID: created.Canvas.ID}); !errors.Is(err, canvas.ErrRemoved) {
		t.Fatalf("old tombstone after restart = %v, want removed", err)
	}
}

func TestDeleteSessionTombstonesCanvasAndRevokesAuthority(t *testing.T) {
	root := t.TempDir()
	config := canvas.DefaultConfig(root)
	config.WorkerLauncher = canvas.NewDeterministicWorkerLauncher()
	service, err := canvas.New(config)
	if err != nil {
		t.Fatal(err)
	}
	authority := attach(t, service, "native-session")
	created, err := service.Create(context.Background(), authority)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteSession("native-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Read(context.Background(), authority, canvas.ReadRequest{CanvasID: created.Canvas.ID}); !errors.Is(err, canvas.ErrRevoked) {
		t.Fatalf("deleted session authority = %v, want revoked", err)
	}
	if err := service.Shutdown(); err != nil {
		t.Fatal(err)
	}
	restarted, err := canvas.New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Shutdown()
	newAuthority := attach(t, restarted, "native-session")
	listed, err := restarted.List(context.Background(), newAuthority)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 0 {
		t.Fatalf("deleted session Canvas list = %#v, want empty", listed.Items)
	}
	if _, err := restarted.Read(context.Background(), newAuthority, canvas.ReadRequest{CanvasID: created.Canvas.ID}); !errors.Is(err, canvas.ErrRemoved) {
		t.Fatalf("deleted session tombstone = %v, want removed", err)
	}
}

func TestDisableCancelsWorkerAndAllowsRemoval(t *testing.T) {
	started := make(chan struct{})
	launcher := canvas.WorkerLauncherFunc(func(ctx context.Context, job canvas.RenderJob) (canvas.RenderResult, error) {
		closeOnce(started)
		<-ctx.Done()
		return canvas.RenderResult{}, ctx.Err()
	})
	service := newService(t, launcher)
	authority := attach(t, service, "native-session")
	created, err := service.Create(context.Background(), authority)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, screenshotErr := service.Screenshot(context.Background(), authority, canvas.ScreenshotRequest{CanvasID: created.Canvas.ID, Width: 32, Height: 32})
		result <- screenshotErr
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	service.Disable()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) && !errors.Is(err, canvas.ErrGenerationRevoked) && !errors.Is(err, canvas.ErrDisabled) {
			t.Fatalf("cancelled screenshot = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not cancel")
	}
	if _, err := service.Remove(context.Background(), authority, canvas.RemoveRequest{CanvasID: created.Canvas.ID}); err != nil {
		t.Fatalf("disabled removal = %v", err)
	}
}

func TestHungWorkerIsBoundedByJobDeadline(t *testing.T) {
	launcher := canvas.WorkerLauncherFunc(func(context.Context, canvas.RenderJob) (canvas.RenderResult, error) {
		select {}
	})
	config := canvas.DefaultConfig(t.TempDir())
	config.WorkerLauncher = launcher
	config.WorkerTimeout = 10 * time.Millisecond
	service, err := canvas.New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown() })
	authority := attach(t, service, "native-session")
	created, err := service.Create(context.Background(), authority)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = service.Screenshot(context.Background(), authority, canvas.ScreenshotRequest{CanvasID: created.Canvas.ID, Width: 16, Height: 16})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("hung worker = %v after %s", err, time.Since(started))
	}
}

func TestDefaultDisabledAndHTTPArtifactAuth(t *testing.T) {
	config := canvas.Config{DataDir: t.TempDir()}
	service, err := canvas.New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown() })
	authority := attach(t, service, "native-session")
	if _, err := service.Create(context.Background(), authority); !errors.Is(err, canvas.ErrDisabled) {
		t.Fatalf("default create = %v, want disabled", err)
	}
	if !service.Ready() {
		t.Fatal("disabled service should be healthy")
	}
}

func TestHTTPArtifactRequiresAuthority(t *testing.T) {
	service := newService(t, canvas.NewDeterministicWorkerLauncher())
	authority := attach(t, service, "native-session")
	created, err := service.Create(context.Background(), authority)
	if err != nil {
		t.Fatal(err)
	}
	shot, err := service.Screenshot(context.Background(), authority, canvas.ScreenshotRequest{CanvasID: created.Canvas.ID, Width: 32, Height: 16})
	if err != nil {
		t.Fatal(err)
	}
	artifactParts := strings.Split(strings.TrimPrefix(shot.Artifact, "pixie://canvas/artifact/"), "/")
	if len(artifactParts) != 2 {
		t.Fatalf("unexpected artifact reference %q", shot.Artifact)
	}
	artifactPath := "/mcp/canvas/artifact/" + artifactParts[0] + "/" + artifactParts[1]
	unauthorized := httptest.NewRecorder()
	service.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, artifactPath, nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized artifact status = %d", unauthorized.Code)
	}
	request := httptest.NewRequest(http.MethodGet, artifactPath, nil)
	request.Header.Set("Authorization", "Bearer "+authority.Token)
	response := httptest.NewRecorder()
	service.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" || response.Body.Len() != len(shot.PNG) {
		t.Fatalf("artifact response status=%d type=%q bytes=%d", response.Code, response.Header().Get("Content-Type"), response.Body.Len())
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Fatalf("artifact security headers = nosniff:%q corp:%q", response.Header().Get("X-Content-Type-Options"), response.Header().Get("Cross-Origin-Resource-Policy"))
	}
	queryCredential := httptest.NewRequest(http.MethodGet, artifactPath+"?access_token=must-not-be-used", nil)
	queryCredential.Header.Set("Authorization", "Bearer "+authority.Token)
	queryCredentialResponse := httptest.NewRecorder()
	service.ServeHTTP(queryCredentialResponse, queryCredential)
	if queryCredentialResponse.Code != http.StatusBadRequest {
		t.Fatalf("artifact query credential status = %d %q", queryCredentialResponse.Code, queryCredentialResponse.Body.String())
	}
}

func TestManagementAuthorityReadsAndRemovesRetainedCanvasWhenDisabled(t *testing.T) {
	service := newService(t, canvas.NewDeterministicWorkerLauncher())
	native := attach(t, service, "native-session")
	created, err := service.Create(context.Background(), native)
	if err != nil {
		t.Fatal(err)
	}
	shot, err := service.Screenshot(context.Background(), native, canvas.ScreenshotRequest{CanvasID: created.Canvas.ID, Width: 32, Height: 16})
	if err != nil {
		t.Fatal(err)
	}
	service.Disable()
	management, err := service.AttachManagement("native-session", 1)
	if err != nil {
		t.Fatal(err)
	}
	mcpRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/mcp/canvas", nil)
	mcpRequest.Header.Set("Authorization", "Bearer "+management.Token)
	mcpResponse := httptest.NewRecorder()
	service.ServeHTTP(mcpResponse, mcpRequest)
	if mcpResponse.Code != http.StatusUnauthorized {
		t.Fatalf("management authority reached MCP = %d", mcpResponse.Code)
	}
	managementContext := func(request *http.Request) *http.Request {
		return request.WithContext(canvas.ContextWithManagementAuthority(request.Context(), management))
	}
	statusRequest := managementContext(httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/canvas/status", nil))
	statusResponse := httptest.NewRecorder()
	service.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK || !strings.Contains(statusResponse.Body.String(), created.Canvas.ID) || !strings.Contains(statusResponse.Body.String(), "disabled") {
		t.Fatalf("disabled management status = %d %q", statusResponse.Code, statusResponse.Body.String())
	}
	artifactParts := strings.Split(strings.TrimPrefix(shot.Artifact, "pixie://canvas/artifact/"), "/")
	artifactRequest := managementContext(httptest.NewRequest(http.MethodGet, "/api/canvas/artifact/"+artifactParts[0]+"/"+artifactParts[1], nil))
	artifactResponse := httptest.NewRecorder()
	service.ServeHTTP(artifactResponse, artifactRequest)
	if artifactResponse.Code != http.StatusOK || !bytes.Equal(artifactResponse.Body.Bytes(), shot.PNG) {
		t.Fatalf("disabled management artifact = %d (%d bytes)", artifactResponse.Code, artifactResponse.Body.Len())
	}
	removeRequest := managementContext(httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/canvas/remove", strings.NewReader(`{"canvasId":"`+created.Canvas.ID+`","expectedGeneration":1,"expectedVersion":1,"mutationId":"remove-1"}`)))
	removeRequest.Header.Set("Content-Type", "application/json")
	removeResponse := httptest.NewRecorder()
	service.ServeHTTP(removeResponse, removeRequest)
	if removeResponse.Code != http.StatusOK || !strings.Contains(removeResponse.Body.String(), `"outcome":"removed"`) {
		t.Fatalf("disabled management remove = %d %q", removeResponse.Code, removeResponse.Body.String())
	}
	service.Revoke(management)
	revokedStatus := httptest.NewRecorder()
	service.ServeHTTP(revokedStatus, statusRequest)
	if revokedStatus.Code != http.StatusUnauthorized {
		t.Fatalf("revoked management status = %d", revokedStatus.Code)
	}
}

func TestQuotaReservesBeforeRevisionPublication(t *testing.T) {
	config := canvas.DefaultConfig(t.TempDir())
	config.WorkerLauncher = canvas.NewDeterministicWorkerLauncher()
	config.MaxStorageBytes = 5 * 1024
	service, err := canvas.New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown() })
	authority := attach(t, service, "native-session")
	created, err := service.Create(context.Background(), authority)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Write(context.Background(), authority, canvas.WriteRequest{CanvasID: created.Canvas.ID, ExpectedVersion: 1, HTML: strings.Repeat("x", 2048), MutationID: "quota-write"})
	if !errors.Is(err, canvas.ErrQuotaExceeded) {
		t.Fatalf("quota write = %v, want quota exceeded", err)
	}
	if _, err := service.Read(context.Background(), authority, canvas.ReadRequest{CanvasID: created.Canvas.ID}); err != nil {
		t.Fatalf("quota rejection damaged prior revision: %v", err)
	}
}

var onceMu sync.Mutex
var onceSignals = map[chan struct{}]bool{}

func closeOnce(signal chan struct{}) {
	onceMu.Lock()
	defer onceMu.Unlock()
	if onceSignals[signal] {
		return
	}
	onceSignals[signal] = true
	close(signal)
}
