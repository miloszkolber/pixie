package controller_test

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/controller"
)

func newStaticHandler(t *testing.T) http.Handler {
	t.Helper()
	staticDir := t.TempDir()
	files := map[string]string{
		"index.html":          `<!doctype html><script>window.__boot = true;</script><script type="module" src="/chunk-abc12345.js"></script><main>application</main>`,
		"chunk-abc12345.js":   `window.__asset = true;`,
		"styles-abc12345.css": `body { color: black; }`,
		"robots.txt":          "User-agent: *\nDisallow: /\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(staticDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".css") {
			if err := os.WriteFile(filepath.Join(staticDir, name+".gz"), gzipBytes(t, []byte(content)), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	handler, err := controller.NewHTTPHandler(
		nil,
		controller.ObjectiveHandler{},
		nil,
		nil,
		controller.AuthConfig{
			BrowserURL:          "http://127.0.0.1:8787",
			BrowserPublicOrigin: "https://browser.example",
			PublicOrigin:        "https://pixie.example",
		},
		staticDir,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func gzipBytes(t *testing.T, content []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func requestStatic(t *testing.T, handler http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestStaticApplicationRoutesAndAssetsUseDistinctCachePolicies(t *testing.T) {
	handler := newStaticHandler(t)
	for _, target := range []string{"https://pixie.example/", "https://pixie.example/projects/example/chats/one"} {
		response := requestStatic(t, handler, http.MethodGet, target)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "<main>application</main>") {
			t.Fatalf("SPA route %q = %d %q", target, response.Code, response.Body.String())
		}
		if cache := response.Header().Get("Cache-Control"); cache != "no-cache" {
			t.Fatalf("SPA route %q cache policy = %q", target, cache)
		}
	}

	immutable := requestStatic(t, handler, http.MethodGet, "https://pixie.example/chunk-abc12345.js?revision=ignored")
	if immutable.Code != http.StatusOK || immutable.Body.String() != `window.__asset = true;` {
		t.Fatalf("immutable asset = %d %q", immutable.Code, immutable.Body.String())
	}
	if cache := immutable.Header().Get("Cache-Control"); cache != "public, max-age=31536000, immutable" {
		t.Fatalf("immutable asset cache policy = %q", cache)
	}

	revalidated := requestStatic(t, handler, http.MethodGet, "https://pixie.example/robots.txt")
	if revalidated.Code != http.StatusOK || revalidated.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("unhashed asset = %d cache=%q", revalidated.Code, revalidated.Header().Get("Cache-Control"))
	}
}

func TestMissingStaticAssetsDoNotFallBackToTheApplication(t *testing.T) {
	handler := newStaticHandler(t)
	for _, target := range []string{
		"https://pixie.example/chunk-missing99.js",
		"https://pixie.example/assets/styles-missing99.css",
		"https://pixie.example/favicon.ico",
		"https://pixie.example/chunk-abc12345.js.gz",
	} {
		response := requestStatic(t, handler, http.MethodGet, target)
		if response.Code != http.StatusNotFound {
			t.Fatalf("missing asset %q returned %d: %q", target, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "<main>application</main>") {
			t.Fatalf("missing asset %q returned the SPA document", target)
		}
		if cache := response.Header().Get("Cache-Control"); cache != "no-store" {
			t.Fatalf("missing asset %q cache policy = %q", target, cache)
		}
	}
}

func TestStaticAssetsUsePrecompressedRepresentations(t *testing.T) {
	handler := newStaticHandler(t)
	request := httptest.NewRequest(http.MethodGet, "https://pixie.example/chunk-abc12345.js", nil)
	request.Header.Set("Accept-Encoding", "br, gzip")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("compressed asset = %d encoding=%q", response.Code, response.Header().Get("Content-Encoding"))
	}
	if response.Header().Get("Vary") != "Accept-Encoding" || response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("compressed headers: vary=%q cache=%q", response.Header().Get("Vary"), response.Header().Get("Cache-Control"))
	}
	reader, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	decompressed, err := io.ReadAll(reader)
	if closeErr := reader.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(decompressed) != `window.__asset = true;` {
		t.Fatalf("compressed body = %q", decompressed)
	}

	identity := httptest.NewRequest(http.MethodGet, "https://pixie.example/chunk-abc12345.js", nil)
	identity.Header.Set("Accept-Encoding", "gzip;q=0, *;q=1")
	identityResponse := httptest.NewRecorder()
	handler.ServeHTTP(identityResponse, identity)
	if identityResponse.Header().Get("Content-Encoding") != "" || identityResponse.Body.String() != `window.__asset = true;` {
		t.Fatalf("identity asset: encoding=%q body=%q", identityResponse.Header().Get("Content-Encoding"), identityResponse.Body.String())
	}
}

func TestStaticApplicationResponsesSetARestrictiveContentPolicy(t *testing.T) {
	handler := newStaticHandler(t)
	response := requestStatic(t, handler, http.MethodGet, "https://pixie.example/projects/example")
	if response.Code != http.StatusOK {
		t.Fatalf("application response = %d %q", response.Code, response.Body.String())
	}
	for name, expected := range map[string]string{
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Referrer-Policy":              "no-referrer",
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
	} {
		if actual := response.Header().Get(name); actual != expected {
			t.Fatalf("%s = %q", name, actual)
		}
	}

	digest := sha256.Sum256([]byte("window.__boot = true;"))
	expectedHash := "'sha256-" + base64.StdEncoding.EncodeToString(digest[:]) + "'"
	csp := response.Header().Get("Content-Security-Policy")
	for _, directive := range []string{
		"default-src 'none'",
		"base-uri 'none'",
		"connect-src 'self' wss://pixie.example",
		"font-src 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
		"frame-src 'self' https://browser.example",
		"img-src 'self' data:",
		"object-src 'none'",
		"script-src 'self' " + expectedHash,
		"style-src 'self' 'unsafe-inline'",
	} {
		if !strings.Contains(csp, directive) {
			t.Fatalf("CSP omitted %q: %s", directive, csp)
		}
	}
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") || strings.Contains(csp, "*") {
		t.Fatalf("CSP relaxed script execution: %s", csp)
	}
}

func TestStaticHeadAndMethodHandlingRemainBounded(t *testing.T) {
	handler := newStaticHandler(t)
	head := requestStatic(t, handler, http.MethodHead, "https://pixie.example/styles-abc12345.css")
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("HEAD asset = %d body=%q cache=%q", head.Code, head.Body.String(), head.Header().Get("Cache-Control"))
	}
	post := requestStatic(t, handler, http.MethodPost, "https://pixie.example/projects/example")
	if post.Code != http.StatusMethodNotAllowed || post.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("POST route = %d allow=%q", post.Code, post.Header().Get("Allow"))
	}
}
