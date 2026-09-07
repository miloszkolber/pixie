package controller_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/miloszkolber/pixie/internal/controller"
)

func TestBrowserArtifactProxyUsesSameOriginResourcePolicy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/artifacts/panel/screen.png" {
			t.Fatalf("unexpected artifact path: %s", request.URL.Path)
		}
		response.Header().Set("Content-Type", "image/png")
		response.Header().Set("Content-Length", "3")
		_, _ = response.Write([]byte("png"))
	}))
	defer upstream.Close()
	handler, err := controller.NewHTTPHandler(nil, controller.ObjectiveHandler{}, nil, nil, controller.AuthConfig{BrowserURL: upstream.URL}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://controller/v1/artifacts/panel/screen.png", nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("artifact status: %d", response.Code)
	}
	if policy := response.Header().Get("Cross-Origin-Resource-Policy"); policy != "same-origin" {
		t.Fatalf("artifact resource policy: %q", policy)
	}
}
