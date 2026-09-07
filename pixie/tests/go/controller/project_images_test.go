package controller_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

func TestProjectImageReadsInternalSymlinksAndRejectsSwaps(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "image.png"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.png"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "image.png"), filepath.Join(root, "preview.png")); err != nil {
		t.Fatal(err)
	}
	policy, err := workspace.NewPathPolicy([]string{root}, false)
	if err != nil {
		t.Fatal(err)
	}
	projects := workspace.NewProjects(persist.Store{Dir: t.TempDir()}, policy)
	project, err := projects.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, projects, workspace.NewFiles(projects, policy), controller.AuthConfig{}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	request := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "http://controller/files/"+project.ID+"/preview.png", nil)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		handler.ServeHTTP(response, request)
		return response
	}
	if response := request(); response.Code != http.StatusOK || response.Body.String() != "inside" {
		t.Fatalf("internal image symlink: status=%d body=%q", response.Code, response.Body.String())
	}
	direct := httptest.NewRecorder()
	directRequest := httptest.NewRequest(http.MethodGet, "http://controller/files/"+project.ID+"/image.png", nil)
	directRequest.Header.Set("Sec-Fetch-Site", "same-origin")
	handler.ServeHTTP(direct, directRequest)
	if direct.Code != http.StatusOK || direct.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("direct PNG image: status=%d content-type=%q", direct.Code, direct.Header().Get("Content-Type"))
	}
	if err := os.Remove(filepath.Join(root, "preview.png")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.png"), filepath.Join(root, "preview.png")); err != nil {
		t.Fatal(err)
	}
	if response := request(); response.Code != http.StatusNotFound || response.Body.String() == "secret" {
		t.Fatalf("path-swapped image symlink: status=%d body=%q", response.Code, response.Body.String())
	}
}
