package mcpserver

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Trusted module-resource HTTP boundary (MODULE-01).
//
// Module resource requests present a server-issued scope token bound to the
// exact module/resource/session/generation tuple (see descriptor_scope.go).
// Caller-supplied IDs alone never authorize access. Forged, mismatched,
// expired, or revoked tokens fail closed at this boundary without delegating
// to the module service. Sidebar-only versus viewer-only descriptor
// declarations are enforced here via the explicit surface parameter.
//
// Token transport: the scope token travels only in the ScopeTokenHeader
// header. It must never appear in URLs, prompts, layout state, or artifacts.
// Requests carrying a token-like query key fail closed.

const (
	// ModuleResourcePrefix owns the controller module-resource namespace. It
	// stays outside the reserved /api/* and /mcp/* publisher namespaces so
	// wiring never alters reserved-route normalization.
	ModuleResourcePrefix = "/v1/modules/"
	// ModuleResourceSubpath is the only resource collection under a module.
	ModuleResourceSubpath = "resources"
	// ScopeTokenHeader carries the opaque scope credential. Header-only keeps
	// secrets out of URLs, logs, and referrers.
	ScopeTokenHeader = "X-Module-Scope"
	// ModuleSurfaceSidebar requests the sidebar (slot 5) surface.
	ModuleSurfaceSidebar = "sidebar"
	// ModuleSurfaceViewer requests the viewer (slot 4) surface.
	ModuleSurfaceViewer = "viewer"
)

// ModuleScopeRequest is the parsed, not-yet-authorized module resource
// request. Token is the opaque credential; the remaining fields are the
// claimed binding that must exactly match the issued tuple.
type ModuleScopeRequest struct {
	ModuleID   string
	ResourceID string
	SessionID  string
	Generation uint64
	Surface    string
	Token      string
}

// DefaultModuleDescriptors returns the committed fixture descriptors keyed by
// module ID: the combined Browser contribution plus sidebar-only and
// viewer-only fixtures that prove generality without shell edits.
func DefaultModuleDescriptors() map[string]FrontendDescriptor {
	descriptors := make(map[string]FrontendDescriptor, 5)
	for _, descriptor := range []FrontendDescriptor{
		BrowserDescriptorFixture(),
		CanvasDescriptorFixture(),
		DesignDescriptorFixture(),
		SidebarOnlyFixture(),
		ViewerOnlyFixture(),
	} {
		descriptors[descriptor.ModuleID] = descriptor
	}
	return descriptors
}

// IsModuleResourceRoute reports whether the normalized route path is a module
// resource request: /v1/modules/<module>/resources with an optional trailing
// slash. Normalized input is expected (see controller normalizeRoutePath).
// No module-name branches: every module shares this shape.
func IsModuleResourceRoute(route string) bool {
	_, ok := ModuleIDFromRoute(route)
	return ok
}

// ModuleIDFromRoute extracts the owning module ID from a normalized module
// resource route. Unknown shapes, missing modules, and extra segments fail
// closed with ok=false.
func ModuleIDFromRoute(route string) (string, bool) {
	if !strings.HasPrefix(route, ModuleResourcePrefix) {
		return "", false
	}
	rest := strings.TrimPrefix(route, ModuleResourcePrefix)
	if rest == "" {
		return "", false
	}
	// Accept an optional trailing slash preserved by route normalization.
	rest = strings.TrimSuffix(rest, "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		return "", false
	}
	moduleID, subpath := parts[0], parts[1]
	if moduleID == "" || subpath != ModuleResourceSubpath {
		return "", false
	}
	return moduleID, true
}

// AuthorizeModuleScope enforces the descriptor and scope binding for one
// parsed request. Unknown modules, invalid descriptors, disallowed surfaces,
// cross-namespace resources, and scope-authority rejections all fail closed
// with an error and grant nothing.
func AuthorizeModuleScope(authority *ScopeAuthority, descriptors map[string]FrontendDescriptor, request ModuleScopeRequest) error {
	descriptor, ok := descriptors[request.ModuleID]
	if !ok {
		return fmt.Errorf("unknown module %q", request.ModuleID)
	}
	if err := ValidateDescriptor(descriptor); err != nil {
		return fmt.Errorf("invalid descriptor for module %q: %w", request.ModuleID, err)
	}
	switch request.Surface {
	case ModuleSurfaceSidebar:
		if !descriptor.Sidebar {
			return fmt.Errorf("module %q does not contribute a sidebar", request.ModuleID)
		}
	case ModuleSurfaceViewer:
		if !descriptor.Viewer {
			return fmt.Errorf("module %q does not contribute a viewer", request.ModuleID)
		}
	default:
		return fmt.Errorf("invalid surface %q: want %q or %q", request.Surface, ModuleSurfaceSidebar, ModuleSurfaceViewer)
	}
	if err := ValidateResourceID(request.ResourceID, request.ModuleID); err != nil {
		return err
	}
	if authority == nil {
		return fmt.Errorf("scope authority is not configured")
	}
	return authority.Authorize(request.Token, request.ModuleID, request.ResourceID, request.SessionID, request.Generation)
}

// AuthorizeModuleHTTPRequest parses the HTTP framing for one module resource
// request and enforces the descriptor plus scope binding. It returns the
// parsed request with an HTTP status hint: 404 for unknown module routes,
// 400 for malformed framing (including tokens in URLs), and 403 for
// authentication/surface/binding failures. Callers must not delegate to the
// module service when err != nil.
func AuthorizeModuleHTTPRequest(authority *ScopeAuthority, descriptors map[string]FrontendDescriptor, moduleID string, request *http.Request) (ModuleScopeRequest, FrontendDescriptor, int, error) {
	var zero ModuleScopeRequest
	var zeroDescriptor FrontendDescriptor
	if moduleID == "" {
		return zero, zeroDescriptor, http.StatusNotFound, fmt.Errorf("unknown module")
	}
	descriptor, ok := descriptors[moduleID]
	if !ok {
		return zero, zeroDescriptor, http.StatusNotFound, fmt.Errorf("unknown module %q", moduleID)
	}
	if request == nil || request.URL == nil {
		return zero, zeroDescriptor, http.StatusBadRequest, fmt.Errorf("invalid module resource request")
	}
	parsed, status, err := parseModuleScopeRequest(moduleID, request)
	if err != nil {
		return zero, zeroDescriptor, status, err
	}
	if err := AuthorizeModuleScope(authority, descriptors, parsed); err != nil {
		// Preserve the 404 for unknown modules; descriptor/surface/scope
		// failures stay 403 except malformed shapes already mapped to 400.
		// AuthorizeModuleScope only returns unknown-module for a module that
		// parsed but vanished from the map; keep it 404 in that corner.
		if strings.HasPrefix(err.Error(), "unknown module ") {
			return zero, zeroDescriptor, http.StatusNotFound, err
		}
		if statusForScopeError(err) == http.StatusBadRequest {
			return zero, zeroDescriptor, http.StatusBadRequest, err
		}
		return zero, zeroDescriptor, http.StatusForbidden, err
	}
	return parsed, descriptor, http.StatusOK, nil
}

func parseModuleScopeRequest(moduleID string, request *http.Request) (ModuleScopeRequest, int, error) {
	var zero ModuleScopeRequest
	query := request.URL.Query()
	// Secrets never travel in URLs: any token-like query key fails closed
	// before header parsing so a logged URL can never carry authority.
	for key := range query {
		lowered := strings.ToLower(key)
		switch lowered {
		case "scopetoken", "scope_token", "scope-token", "token", "scope":
			return zero, http.StatusBadRequest, fmt.Errorf("scope token must use the %s header, never the URL", ScopeTokenHeader)
		}
	}
	surfaceValues, surfaceOK := query["surface"]
	if !surfaceOK || len(surfaceValues) != 1 {
		return zero, http.StatusBadRequest, fmt.Errorf("invalid surface: want %q or %q", ModuleSurfaceSidebar, ModuleSurfaceViewer)
	}
	surface := surfaceValues[0]
	if surface != ModuleSurfaceSidebar && surface != ModuleSurfaceViewer {
		return zero, http.StatusBadRequest, fmt.Errorf("invalid surface %q: want %q or %q", surface, ModuleSurfaceSidebar, ModuleSurfaceViewer)
	}
	resourceValues, resourceOK := query["resource"]
	if !resourceOK || len(resourceValues) != 1 || resourceValues[0] == "" {
		return zero, http.StatusBadRequest, fmt.Errorf("invalid resource id")
	}
	resourceID := resourceValues[0]
	sessionID := ""
	if values, ok := query["session"]; ok {
		if len(values) != 1 {
			return zero, http.StatusBadRequest, fmt.Errorf("invalid session id")
		}
		sessionID = values[0]
	}
	generationValues, generationOK := query["generation"]
	if !generationOK || len(generationValues) != 1 || generationValues[0] == "" {
		return zero, http.StatusBadRequest, fmt.Errorf("invalid generation")
	}
	generation, err := strconv.ParseUint(generationValues[0], 10, 64)
	if err != nil {
		return zero, http.StatusBadRequest, fmt.Errorf("invalid generation %q", generationValues[0])
	}
	tokens := request.Header.Values(ScopeTokenHeader)
	if len(tokens) != 1 || strings.TrimSpace(tokens[0]) == "" {
		return zero, http.StatusForbidden, fmt.Errorf("missing scope credential")
	}
	token := strings.TrimSpace(tokens[0])
	// A token smuggled into the URL was already rejected above; a token that
	// differs only by surrounding whitespace is still the same credential,
	// so compare the trimmed header value only.
	return ModuleScopeRequest{
		ModuleID: moduleID, ResourceID: resourceID,
		SessionID: sessionID, Generation: generation,
		Surface: surface, Token: token,
	}, http.StatusOK, nil
}

func statusForScopeError(err error) int {
	if err == nil {
		return http.StatusOK
	}
	message := err.Error()
	// Malformed shapes fail as 400; credential/binding/surface failures fail
	// as 403. Unknown modules are handled by the caller as 404.
	for _, prefix := range []string{"invalid resource id", "invalid resource namespace", "resource id "} {
		if strings.HasPrefix(message, prefix) || strings.Contains(message, "invalid resource id") {
			return http.StatusBadRequest
		}
	}
	return http.StatusForbidden
}
