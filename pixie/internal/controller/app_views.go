package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	appViewPath             = "/v1/app-views"
	appViewMediaType        = "text/html;profile=mcp-app"
	maxAppViewRequestBytes  = 64 * 1024
	maxAppViewResponseBytes = 64 * 1024
	// Leave enough room for the JSON-RPC envelope when every input byte needs
	// the longest JSON escape on Pi's shared Pi connection.
	maxAppViewHTMLBytes   = 5 * 1024 * 1024
	appViewRequestTimeout = 10 * time.Second
	appViewOpenTimeout    = 15 * time.Second
	appViewCleanupTimeout = time.Second
)

var appViewTicketPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// AppViews is the narrow authenticated adapter between a trusted Pi MCP App
// attachment and the credential-free browser sandbox host.
type AppViews struct {
	sessions       *SessionManager
	auth           AuthConfig
	controllerPort int
	client         *http.Client
	// browser serves the in-process Browser REST surface when
	// PIXIE_BROWSER_URL is unset (merged publisher deployment).
	browser          func() http.Handler
	mu               sync.Mutex
	views            map[string]*appViewBinding
	earlyCancels     map[appViewOperationKey]struct{}
	earlyCancelOrder []appViewOperationKey
	activeOperations int
	openingViews     int
	contentBytes     int
	maxContentBytes  int
	viewLease        time.Duration
	cleanup          sync.WaitGroup
	closed           bool
}

type appViewResource struct {
	HTML        string         `json:"html"`
	CSP         map[string]any `json:"csp,omitempty"`
	Permissions map[string]any `json:"permissions,omitempty"`
}

type AppViewOpenResult struct {
	ViewID   string              `json:"viewId"`
	URL      string              `json:"url"`
	Resource appViewOpenResource `json:"resource"`
}

type appViewOpenResource struct {
	ByteLength  int            `json:"byteLength"`
	CSP         map[string]any `json:"csp,omitempty"`
	Permissions map[string]any `json:"permissions,omitempty"`
}

func NewAppViews(sessions *SessionManager, auth AuthConfig, controllerPort int) *AppViews {
	return &AppViews{
		sessions:        sessions,
		auth:            auth,
		controllerPort:  controllerPort,
		views:           make(map[string]*appViewBinding),
		earlyCancels:    make(map[appViewOperationKey]struct{}),
		maxContentBytes: maxAppViewRetainedHTMLBytes,
		viewLease:       appViewLeaseDuration,
		client: &http.Client{
			Timeout: appViewRequestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// SetBrowserHandler wires the merged publisher's Browser REST surface for
// App view registration and ticket revocation when no external
// PIXIE_BROWSER_URL is configured.
func (a *AppViews) SetBrowserHandler(browser func() http.Handler) {
	a.browser = browser
}

// sandboxOrigin derives a loopback sandbox origin from the requesting parent
// origin by swapping 127.0.0.1 and localhost. The two hostnames are distinct
// origins under the same-origin policy, so the App iframe stays isolated
// from the parent application while remaining served by this controller.
// Custom remote origins fall back to the loopback sandbox with the same
// reachability limits as the former default Browser service address.
func (a *AppViews) sandboxOrigin(parentOrigin string) (string, error) {
	parsed, err := url.Parse(parentOrigin)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("App parent origin is invalid")
	}
	sandboxHost := "localhost"
	if parsed.Hostname() == "localhost" {
		sandboxHost = "127.0.0.1"
	}
	port := a.controllerPort
	if port <= 0 || port > 65535 {
		port = DefaultControllerPort
	}
	return "http://" + net.JoinHostPort(sandboxHost, strconv.Itoa(port)), nil
}

// browserCall posts one App view request either to the in-process module
// (PIXIE_BROWSER_URL unset) or to the configured external Browser service.
// It returns the response status and body for shared validation.
func (a *AppViews) browserCall(ctx context.Context, path string, sandboxOrigin string, body []byte) (int, []byte, error) {
	method := http.MethodPost
	if body == nil {
		method = http.MethodDelete
	}
	if a.auth.BrowserURL != "" {
		target, err := url.Parse(a.auth.BrowserURL + path)
		if err != nil {
			return 0, nil, fmt.Errorf("App sandbox is unavailable")
		}
		request, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
		if err != nil {
			return 0, nil, fmt.Errorf("App sandbox is unavailable")
		}
		if method == http.MethodPost {
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json")
		} else {
			request.Header.Set("Accept", "application/json")
		}
		if browserAuth, browserToken := a.auth.BrowserServiceAuth(); browserAuth {
			if !strongToken(browserToken) {
				return 0, nil, fmt.Errorf("App sandbox is unavailable")
			}
			request.Header.Set("Authorization", "Bearer "+browserToken)
		}
		response, err := a.client.Do(request)
		if err != nil {
			return 0, nil, fmt.Errorf("App sandbox is unavailable")
		}
		defer response.Body.Close()
		content, err := io.ReadAll(io.LimitReader(response.Body, maxAppViewRequestBytes+1))
		if err != nil {
			return 0, nil, fmt.Errorf("App sandbox returned an invalid response")
		}
		return response.StatusCode, content, nil
	}
	if a.browser == nil {
		return 0, nil, fmt.Errorf("App sandbox is unavailable")
	}
	handler := a.browser()
	if handler == nil {
		return 0, nil, fmt.Errorf("App sandbox is unavailable")
	}
	target, err := url.Parse(sandboxOrigin + path)
	if err != nil {
		return 0, nil, fmt.Errorf("App sandbox is unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("App sandbox is unavailable")
	}
	// Browser request validation reads the origin-form RequestURI.
	request.RequestURI = request.URL.RequestURI()
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json")
	} else {
		request.Header.Set("Accept", "application/json")
	}
	if browserAuth, browserToken := a.auth.BrowserServiceAuth(); browserAuth {
		if !strongToken(browserToken) {
			return 0, nil, fmt.Errorf("App sandbox is unavailable")
		}
		request.Header.Set("Authorization", "Bearer "+browserToken)
	}
	recorder := newBrowserResponseRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.code == 0 {
		recorder.code = http.StatusOK
	}
	if len(recorder.body.Bytes()) > maxAppViewRequestBytes+1 {
		return 0, nil, fmt.Errorf("App sandbox returned an invalid response")
	}
	return recorder.code, recorder.body.Bytes(), nil
}

func (a *AppViews) Open(ctx context.Context, projectID, sessionID, toolCallID, parentOrigin, clientKey string) (any, error) {
	if a == nil || a.sessions == nil {
		return nil, fmt.Errorf("App views are unavailable")
	}
	ctx, cancelOpen := context.WithTimeout(ctx, appViewOpenTimeout)
	defer cancelOpen()
	if err := a.reserveView(); err != nil {
		return nil, err
	}
	defer a.releaseViewReservation()
	parentOrigin, err := a.expectedParentOrigin(parentOrigin)
	if err != nil {
		return nil, err
	}
	viewOrigin := a.auth.BrowserPublicOrigin
	if viewOrigin == "" {
		if a.auth.BrowserURL != "" {
			browserOrigin, err := a.browserOrigin()
			if err != nil {
				return nil, err
			}
			viewOrigin = browserOrigin
		} else {
			// Merged publisher: serve the sandbox from this controller on
			// the loopback hostname the parent origin does not use.
			sandbox, err := a.sandboxOrigin(parentOrigin)
			if err != nil {
				return nil, err
			}
			viewOrigin = sandbox
		}
	}
	viewOrigin, err = normalizeOrigin(viewOrigin)
	if err != nil {
		return nil, fmt.Errorf("App sandbox public origin is invalid")
	}
	if sameAppViewOrigin(viewOrigin, parentOrigin) {
		return nil, fmt.Errorf("App sandbox origin is not isolated")
	}
	resource, attachment, err := a.resolveRootResource(ctx, projectID, sessionID, toolCallID)
	if err != nil {
		return nil, err
	}

	registration := map[string]any{"parentOrigin": parentOrigin}
	if resource.CSP != nil {
		registration["csp"] = resource.CSP
	}
	if resource.Permissions != nil {
		registration["permissions"] = resource.Permissions
	}
	body, err := json.Marshal(registration)
	if err != nil || len(body) > maxAppViewRequestBytes {
		return nil, fmt.Errorf("App sandbox policy is invalid")
	}
	status, content, err := a.browserCall(ctx, appViewPath, viewOrigin, body)
	if err != nil {
		return nil, err
	}
	if status != http.StatusCreated {
		return nil, fmt.Errorf("App sandbox rejected the view")
	}
	var registered struct {
		Ticket string `json:"ticket"`
		Path   string `json:"path"`
		URL    string `json:"url"`
		Expiry string `json:"expiresAt"`
	}
	if err := json.Unmarshal(content, &registered); err != nil || !appViewTicketPattern.MatchString(registered.Ticket) {
		if appViewTicketPattern.MatchString(registered.Ticket) {
			a.cleanupTicket(registered.Ticket, viewOrigin)
		}
		return nil, fmt.Errorf("App sandbox returned an invalid view")
	}
	expectedPath := appViewPath + "/" + registered.Ticket
	expectedURL := viewOrigin + expectedPath
	if registered.Path != expectedPath || registered.URL != expectedURL {
		a.cleanupTicket(registered.Ticket, viewOrigin)
		return nil, fmt.Errorf("App sandbox returned an invalid view")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, registered.Expiry)
	if err != nil {
		a.cleanupTicket(registered.Ticket, viewOrigin)
		return nil, fmt.Errorf("App sandbox returned an invalid view")
	}
	if err := ctx.Err(); err != nil {
		a.cleanupTicket(registered.Ticket, viewOrigin)
		return nil, err
	}
	if err := a.registerView(registered.Ticket, viewOrigin, projectID, sessionID, toolCallID, clientKey, attachment, resource.HTML, expiresAt); err != nil {
		a.cleanupTicket(registered.Ticket, viewOrigin)
		return nil, err
	}
	return AppViewOpenResult{
		ViewID: registered.Ticket,
		URL:    expectedURL,
		Resource: appViewOpenResource{
			ByteLength: len(resource.HTML), CSP: resource.CSP, Permissions: resource.Permissions,
		},
	}, nil
}

func (a *AppViews) resolveRootResource(ctx context.Context, projectID, sessionID, toolCallID string) (appViewResource, AppAttachment, error) {
	entry, state, err := a.sessions.appOperation(ctx, projectID, sessionID, toolCallID)
	if err != nil {
		return appViewResource{}, AppAttachment{}, err
	}
	defer a.sessions.releaseAppOperation(entry)

	result, err := a.sessions.readAppResourceLocked(ctx, entry, state, sessionID, state.attachment.ResourceURI)
	if err != nil {
		return appViewResource{}, AppAttachment{}, err
	}
	resource, err := appViewRootResource(state.attachment.ResourceURI, result)
	return resource, state.attachment, err
}

func (a *AppViews) Close(ctx context.Context, viewID, clientKey string) error {
	view, err := a.revokeView(viewID, clientKey)
	if err != nil {
		return err
	}
	return a.deleteTicket(ctx, viewID, view.origin)
}

func (a *AppViews) deleteTicket(ctx context.Context, viewID, origin string) error {
	status, content, err := a.browserCall(ctx, appViewPath+"/"+viewID, origin, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusNotFound {
		return fmt.Errorf("App sandbox could not close the view")
	}
	if !json.Valid(content) {
		return fmt.Errorf("App sandbox returned an invalid response")
	}
	return nil
}

func (a *AppViews) cleanupTicket(viewID, origin string) {
	// Keep a malformed registration below the frontend's open deadline while
	// still making one bounded attempt to revoke the known browser ticket.
	cleanupContext, cancel := context.WithTimeout(context.Background(), appViewCleanupTimeout)
	defer cancel()
	_ = a.deleteTicket(cleanupContext, viewID, origin)
}

func (a *AppViews) browserOrigin() (string, error) {
	browserAuth, browserToken := a.auth.BrowserServiceAuth()
	if !browserAuth || !strongToken(browserToken) {
		return "", fmt.Errorf("authenticated browser service is not configured")
	}
	origin, err := normalizeOrigin(a.auth.BrowserURL)
	if err != nil {
		return "", fmt.Errorf("browser service origin is invalid")
	}
	return origin, nil
}

func (a *AppViews) expectedParentOrigin(value string) (string, error) {
	origin, err := normalizeOrigin(value)
	if err != nil {
		return "", fmt.Errorf("App parent origin is invalid")
	}
	if a.auth.PublicOrigin != "" {
		expected, err := normalizeOrigin(a.auth.PublicOrigin)
		if err != nil || !sameAppViewOrigin(origin, expected) {
			return "", fmt.Errorf("App parent origin is not trusted")
		}
		return origin, nil
	}
	parsed, _ := url.Parse(origin)
	hostname := parsed.Hostname()
	if parsed.Scheme != "http" || (hostname != "localhost" && !net.ParseIP(hostname).IsLoopback()) || effectiveOriginPort(parsed) != a.controllerPort {
		return "", fmt.Errorf("App parent origin is not trusted")
	}
	return origin, nil
}

func sameAppViewOrigin(left, right string) bool {
	leftURL, leftErr := url.Parse(left)
	rightURL, rightErr := url.Parse(right)
	return leftErr == nil && rightErr == nil && strings.EqualFold(leftURL.Scheme, rightURL.Scheme) &&
		strings.EqualFold(leftURL.Hostname(), rightURL.Hostname()) && effectiveOriginPort(leftURL) == effectiveOriginPort(rightURL)
}

func effectiveOriginPort(value *url.URL) int {
	if value.Port() != "" {
		port, err := strconv.Atoi(value.Port())
		if err == nil {
			return port
		}
		return 0
	}
	if value.Scheme == "https" {
		return 443
	}
	return 80
}

func appViewRootResource(resourceURI string, result map[string]any) (appViewResource, error) {
	if validateAppResourceResult(result) != nil {
		return appViewResource{}, fmt.Errorf("App resource is unavailable")
	}
	var match map[string]any
	for _, value := range arrayValue(result["contents"]) {
		content := mapValue(value)
		if content["uri"] != resourceURI || content["mimeType"] != appViewMediaType {
			continue
		}
		if match != nil {
			return appViewResource{}, fmt.Errorf("App resource is ambiguous")
		}
		match = content
	}
	if match == nil {
		return appViewResource{}, fmt.Errorf("App resource is unavailable")
	}
	html, err := appViewHTML(match)
	if err != nil {
		return appViewResource{}, err
	}
	resource := appViewResource{HTML: html}
	meta := mapValue(match["_meta"])
	if rawUI, exists := meta["ui"]; exists && rawUI != nil {
		ui, ok := rawUI.(map[string]any)
		if !ok {
			return appViewResource{}, fmt.Errorf("App sandbox policy is invalid")
		}
		if raw, exists := ui["csp"]; exists && raw != nil {
			csp, ok := raw.(map[string]any)
			if !ok {
				return appViewResource{}, fmt.Errorf("App sandbox policy is invalid")
			}
			resource.CSP = cloneJSON(csp).(map[string]any)
		}
		if raw, exists := ui["permissions"]; exists && raw != nil {
			permissions, ok := raw.(map[string]any)
			if !ok {
				return appViewResource{}, fmt.Errorf("App sandbox policy is invalid")
			}
			resource.Permissions = cloneJSON(permissions).(map[string]any)
		}
	}
	return resource, nil
}

func appViewHTML(content map[string]any) (string, error) {
	if text, ok := content["text"].(string); ok {
		if len(text) > maxAppViewHTMLBytes {
			return "", fmt.Errorf("App resource is too large")
		}
		if !utf8.ValidString(text) {
			return "", fmt.Errorf("App resource is invalid")
		}
		return text, nil
	}
	blob, ok := content["blob"].(string)
	if !ok || base64.StdEncoding.DecodedLen(len(blob)) > maxAppViewHTMLBytes {
		return "", fmt.Errorf("App resource is invalid")
	}
	if strings.ContainsAny(blob, " \t\r\n") {
		return "", fmt.Errorf("App resource is invalid")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(blob)
	if err != nil || len(decoded) > maxAppViewHTMLBytes || !utf8.Valid(decoded) {
		return "", fmt.Errorf("App resource is invalid")
	}
	return string(decoded), nil
}

func readAppViewJSON(response *http.Response, target any) error {
	body, err := readAppViewBody(response)
	if err != nil {
		return err
	}
	if json.Unmarshal(body, target) != nil {
		return fmt.Errorf("invalid JSON")
	}
	return nil
}

func readAppViewBody(response *http.Response) ([]byte, error) {
	if response.ContentLength > maxAppViewResponseBytes {
		return nil, fmt.Errorf("response is too large")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAppViewResponseBytes+1))
	if err != nil || len(body) > maxAppViewResponseBytes {
		return nil, fmt.Errorf("response is too large")
	}
	return body, nil
}
