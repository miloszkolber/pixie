package controller

import (
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	browserSessionPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,38}$`)
	browserArtifactPattern = regexp.MustCompile(`(?i)^[A-Za-z0-9][A-Za-z0-9._-]{0,126}\.(?:png|jpe?g|webp)$`)
)

func (h *HTTPHandler) serveBrowserArtifact(response http.ResponseWriter, request *http.Request) {
	if !h.Auth.IsAuthorizedHTTPRequest(request, h.auth) {
		http.Error(response, "unauthorized", http.StatusUnauthorized)
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(response, http.MethodGet)
		return
	}
	parts := strings.Split(strings.TrimPrefix(request.URL.EscapedPath(), "/v1/artifacts/"), "/")
	if len(parts) != 2 {
		http.NotFound(response, request)
		return
	}
	session, sessionErr := url.PathUnescape(parts[0])
	name, nameErr := url.PathUnescape(parts[1])
	if sessionErr != nil || nameErr != nil || !browserSessionPattern.MatchString(session) || !browserArtifactPattern.MatchString(name) {
		http.NotFound(response, request)
		return
	}
	upstream, err := http.NewRequestWithContext(request.Context(), http.MethodGet, h.Auth.BrowserURL+"/v1/artifacts/"+url.PathEscape(session)+"/"+url.PathEscape(name), nil)
	if err != nil {
		http.Error(response, "browser artifact proxy unavailable", http.StatusBadGateway)
		return
	}
	if browserAuth, browserToken := h.Auth.BrowserServiceAuth(); browserAuth {
		if !strongToken(browserToken) {
			http.Error(response, "browser artifact proxy unavailable", http.StatusServiceUnavailable)
			return
		}
		upstream.Header.Set("Authorization", "Bearer "+browserToken)
	}
	result, err := h.browserClient.Do(upstream)
	if err != nil {
		http.Error(response, "browser artifact proxy unavailable", http.StatusBadGateway)
		return
	}
	defer result.Body.Close()
	if result.StatusCode >= 300 && result.StatusCode < 400 {
		http.Error(response, "browser artifact proxy unavailable", http.StatusBadGateway)
		return
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		http.NotFound(response, request)
		return
	}
	contentType, _, err := mime.ParseMediaType(result.Header.Get("Content-Type"))
	if err != nil || (contentType != "image/png" && contentType != "image/jpeg" && contentType != "image/webp") || result.ContentLength < 0 || result.ContentLength > 64*1024*1024 {
		http.Error(response, "invalid browser artifact", http.StatusBadGateway)
		return
	}
	response.Header().Set("Content-Type", contentType)
	response.Header().Set("Content-Length", strconv.FormatInt(result.ContentLength, 10))
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.CopyN(response, result.Body, result.ContentLength)
}
