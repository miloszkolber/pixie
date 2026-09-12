package controller_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/mcpserver"
)

type moduleScopeSpy struct {
	hits int
}

func (s *moduleScopeSpy) ServeHTTP(response http.ResponseWriter, _ *http.Request) {
	s.hits++
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte(`{"ok":true}`))
}

func newModuleScopeHandler(t *testing.T, spy *moduleScopeSpy) (*controller.HTTPHandler, *mcpserver.ScopeAuthority) {
	t.Helper()
	handler := newStaticHandler(t)
	authority := mcpserver.NewScopeAuthority()
	handler.ModuleScopes = authority
	handler.ModuleDescriptors = mcpserver.DefaultModuleDescriptors()
	if spy != nil {
		handler.ModuleService = spy
	}
	return handler, authority
}

func moduleScopeTarget(moduleID, resourceID, sessionID string, generation uint64, surface string) string {
	values := url.Values{}
	values.Set("resource", resourceID)
	values.Set("session", sessionID)
	values.Set("generation", strconv.FormatUint(generation, 10))
	values.Set("surface", surface)
	return "https://pixie.example/v1/modules/" + moduleID + "/resources?" + values.Encode()
}

func doModuleScopeRequest(handler http.Handler, method, target, token string, extraQuery string) *httptest.ResponseRecorder {
	if extraQuery != "" {
		separator := "&"
		if !strings.Contains(target, "?") {
			separator = "?"
		}
		target += separator + extraQuery
	}
	request := httptest.NewRequest(method, target, nil)
	if token != "" {
		request.Header.Set(mcpserver.ScopeTokenHeader, token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func issueModuleScope(t *testing.T, authority *mcpserver.ScopeAuthority, moduleID, resourceID, sessionID string, generation uint64, ttl time.Duration) mcpserver.IssuedScope {
	t.Helper()
	issued, err := authority.Issue(moduleID, resourceID, sessionID, generation, ttl)
	if err != nil {
		t.Fatal(err)
	}
	return issued
}

func assertModuleScopeFailure(t *testing.T, response *httptest.ResponseRecorder, spy *moduleScopeSpy, target string) {
	t.Helper()
	if response.Code == http.StatusOK {
		t.Fatalf("module scope %q succeeded, want failure: %q", target, response.Body.String())
	}
	if response.Code < http.StatusBadRequest {
		t.Fatalf("module scope %q status = %d, want 4xx", target, response.Code)
	}
	if spy.hits != 0 {
		t.Fatalf("module scope %q delegated to module service on failure (%d hits)", target, spy.hits)
	}
	body := response.Body.String()
	if strings.Contains(body, "<main>application</main>") || strings.Contains(strings.ToLower(body), "<!doctype html") {
		t.Fatalf("module scope %q returned SPA document: %q", target, body)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("module scope %q content type = %q body=%q", target, contentType, body)
	}
	if cache := response.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("module scope %q cache policy = %q", target, cache)
	}
	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("module scope %q body is not JSON: %v (%q)", target, err, body)
	}
}

func TestModuleScopeValidTokenDelegatesToService(t *testing.T) {
	spy := &moduleScopeSpy{}
	handler, authority := newModuleScopeHandler(t, spy)
	fixture := mcpserver.SidebarOnlyFixture()
	issued := issueModuleScope(t, authority, fixture.ModuleID, fixture.ResourceNamespace+":side-res-1", "sess-1", 7, time.Hour)

	target := moduleScopeTarget(issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation, mcpserver.ModuleSurfaceSidebar)
	response := doModuleScopeRequest(handler, http.MethodGet, target, issued.Token, "")
	if response.Code != http.StatusOK {
		t.Fatalf("valid scope status = %d body=%q", response.Code, response.Body.String())
	}
	if spy.hits != 1 {
		t.Fatalf("valid scope delegate hits = %d, want 1", spy.hits)
	}
	if cache := response.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("valid scope cache policy = %q", cache)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("valid scope content type = %q", contentType)
	}
}

func TestModuleScopeForgedTokensFailClosedWithoutDelegation(t *testing.T) {
	fixture := mcpserver.SidebarOnlyFixture()
	viewer := mcpserver.ViewerOnlyFixture()

	newIssued := func(t *testing.T, authority *mcpserver.ScopeAuthority) mcpserver.IssuedScope {
		return issueModuleScope(t, authority, fixture.ModuleID, fixture.ResourceNamespace+":side-res-1", "sess-1", 7, time.Hour)
	}

	t.Run("ForgedUnknownAndEmptyTokens", func(t *testing.T) {
		spy := &moduleScopeSpy{}
		handler, authority := newModuleScopeHandler(t, spy)
		issued := newIssued(t, authority)
		target := moduleScopeTarget(issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation, mcpserver.ModuleSurfaceSidebar)

		// Missing credential fails closed.
		assertModuleScopeFailure(t, doModuleScopeRequest(handler, http.MethodGet, target, "", ""), spy, "missing token")
		// Forged and empty-presented tokens grant nothing.
		for name, token := range map[string]string{
			"forged": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			"empty":  "   ",
		} {
			spy.hits = 0
			request := httptest.NewRequest(http.MethodGet, target, nil)
			request.Header.Set(mcpserver.ScopeTokenHeader, token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			assertModuleScopeFailure(t, response, spy, name)
		}
	})

	t.Run("ForgedMismatchedBindings", func(t *testing.T) {
		spy := &moduleScopeSpy{}
		handler, authority := newModuleScopeHandler(t, spy)
		issued := newIssued(t, authority)
		cases := []struct {
			name       string
			moduleID   string
			resourceID string
			sessionID  string
			generation uint64
		}{
			{"ForgedModule", viewer.ModuleID, viewer.ResourceNamespace + ":side-res-1", issued.SessionID, issued.Generation},
			{"ForgedResource", issued.ModuleID, issued.ModuleID + ":other-res", issued.SessionID, issued.Generation},
			{"ForgedCrossNamespaceResource", issued.ModuleID, viewer.ResourceNamespace + ":side-res-1", issued.SessionID, issued.Generation},
			{"ForgedSession", issued.ModuleID, issued.ResourceID, "sess-2", issued.Generation},
			{"ForgedGeneration", issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation + 1},
			{"ForgedBareResource", issued.ModuleID, "side-res-1", issued.SessionID, issued.Generation},
		}
		for _, item := range cases {
			spy.hits = 0
			target := moduleScopeTarget(item.moduleID, item.resourceID, item.sessionID, item.generation, mcpserver.ModuleSurfaceSidebar)
			// Viewer-module cases need a viewer surface to isolate the binding
			// mismatch from the contribution mismatch; the binding must still
			// fail because the token was issued for the sidebar tuple.
			if item.moduleID == viewer.ModuleID {
				target = moduleScopeTarget(item.moduleID, item.resourceID, item.sessionID, item.generation, mcpserver.ModuleSurfaceViewer)
			}
			assertModuleScopeFailure(t, doModuleScopeRequest(handler, http.MethodGet, target, issued.Token, ""), spy, item.name)
		}
		// Original binding still works after forged attempts.
		spy.hits = 0
		target := moduleScopeTarget(issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation, mcpserver.ModuleSurfaceSidebar)
		response := doModuleScopeRequest(handler, http.MethodGet, target, issued.Token, "")
		if response.Code != http.StatusOK || spy.hits != 1 {
			t.Fatalf("live binding after forged attempts = %d hits=%d body=%q", response.Code, spy.hits, response.Body.String())
		}
	})

	t.Run("ForgedTokenInURLFailsClosed", func(t *testing.T) {
		spy := &moduleScopeSpy{}
		handler, authority := newModuleScopeHandler(t, spy)
		issued := newIssued(t, authority)
		target := moduleScopeTarget(issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation, mcpserver.ModuleSurfaceSidebar)
		assertModuleScopeFailure(t, doModuleScopeRequest(handler, http.MethodGet, target, issued.Token, "scopeToken="+url.QueryEscape(issued.Token)), spy, "token in URL")
		assertModuleScopeFailure(t, doModuleScopeRequest(handler, http.MethodGet, target, issued.Token, "token="+url.QueryEscape(issued.Token)), spy, "token alias in URL")
	})
}

func TestModuleScopeRevokedAndExpiredFailClosed(t *testing.T) {
	t.Run("RevokedToken", func(t *testing.T) {
		spy := &moduleScopeSpy{}
		handler, authority := newModuleScopeHandler(t, spy)
		fixture := mcpserver.SidebarOnlyFixture()
		issued := issueModuleScope(t, authority, fixture.ModuleID, fixture.ResourceNamespace+":side-res-1", "sess-1", 3, time.Hour)
		authority.Revoke(issued.Token)
		target := moduleScopeTarget(issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation, mcpserver.ModuleSurfaceSidebar)
		assertModuleScopeFailure(t, doModuleScopeRequest(handler, http.MethodGet, target, issued.Token, ""), spy, "revoked token")
	})

	t.Run("RevokedSession", func(t *testing.T) {
		spy := &moduleScopeSpy{}
		handler, authority := newModuleScopeHandler(t, spy)
		fixture := mcpserver.SidebarOnlyFixture()
		first := issueModuleScope(t, authority, fixture.ModuleID, fixture.ResourceNamespace+":side-res-1", "sess-1", 3, time.Hour)
		second := issueModuleScope(t, authority, fixture.ModuleID, fixture.ResourceNamespace+":side-res-2", "sess-1", 3, time.Hour)
		if revoked := authority.RevokeSession("sess-1"); revoked != 2 {
			t.Fatalf("revoked sessions = %d, want 2", revoked)
		}
		for name, issued := range map[string]mcpserver.IssuedScope{"first": first, "second": second} {
			target := moduleScopeTarget(issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation, mcpserver.ModuleSurfaceSidebar)
			assertModuleScopeFailure(t, doModuleScopeRequest(handler, http.MethodGet, target, issued.Token, ""), spy, "session-revoked "+name)
		}
	})

	t.Run("ExpiredScope", func(t *testing.T) {
		spy := &moduleScopeSpy{}
		handler, authority := newModuleScopeHandler(t, spy)
		fixture := mcpserver.SidebarOnlyFixture()
		expired := issueModuleScope(t, authority, fixture.ModuleID, fixture.ResourceNamespace+":side-res-1", "sess-1", 1, -time.Second)
		target := moduleScopeTarget(expired.ModuleID, expired.ResourceID, expired.SessionID, expired.Generation, mcpserver.ModuleSurfaceSidebar)
		assertModuleScopeFailure(t, doModuleScopeRequest(handler, http.MethodGet, target, expired.Token, ""), spy, "expired token")
	})
}

func TestModuleContributionScopeEnforcedAtBoundary(t *testing.T) {
	t.Run("ContributionSidebarOnlyRejectsViewer", func(t *testing.T) {
		spy := &moduleScopeSpy{}
		handler, authority := newModuleScopeHandler(t, spy)
		fixture := mcpserver.SidebarOnlyFixture()
		issued := issueModuleScope(t, authority, fixture.ModuleID, fixture.ResourceNamespace+":side-res-1", "sess-1", 1, time.Hour)
		viewerTarget := moduleScopeTarget(issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation, mcpserver.ModuleSurfaceViewer)
		assertModuleScopeFailure(t, doModuleScopeRequest(handler, http.MethodGet, viewerTarget, issued.Token, ""), spy, "sidebar-only viewer request")
		sidebarTarget := moduleScopeTarget(issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation, mcpserver.ModuleSurfaceSidebar)
		response := doModuleScopeRequest(handler, http.MethodGet, sidebarTarget, issued.Token, "")
		if response.Code != http.StatusOK || spy.hits != 1 {
			t.Fatalf("sidebar-only sidebar request = %d hits=%d body=%q", response.Code, spy.hits, response.Body.String())
		}
	})

	t.Run("ContributionViewerOnlyRejectsSidebar", func(t *testing.T) {
		spy := &moduleScopeSpy{}
		handler, authority := newModuleScopeHandler(t, spy)
		fixture := mcpserver.ViewerOnlyFixture()
		issued := issueModuleScope(t, authority, fixture.ModuleID, fixture.ResourceNamespace+":view-res-9", "proj-1", 1, time.Hour)
		sidebarTarget := moduleScopeTarget(issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation, mcpserver.ModuleSurfaceSidebar)
		assertModuleScopeFailure(t, doModuleScopeRequest(handler, http.MethodGet, sidebarTarget, issued.Token, ""), spy, "viewer-only sidebar request")
		viewerTarget := moduleScopeTarget(issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation, mcpserver.ModuleSurfaceViewer)
		response := doModuleScopeRequest(handler, http.MethodGet, viewerTarget, issued.Token, "")
		if response.Code != http.StatusOK || spy.hits != 1 {
			t.Fatalf("viewer-only viewer request = %d hits=%d body=%q", response.Code, spy.hits, response.Body.String())
		}
	})

	t.Run("ContributionCombinedAllowsBoth", func(t *testing.T) {
		spy := &moduleScopeSpy{}
		handler, authority := newModuleScopeHandler(t, spy)
		fixture := mcpserver.BrowserDescriptorFixture()
		for _, surface := range []string{mcpserver.ModuleSurfaceSidebar, mcpserver.ModuleSurfaceViewer} {
			issued := issueModuleScope(t, authority, fixture.ModuleID, fixture.ResourceNamespace+":res-"+surface, "sess-1", 1, time.Hour)
			target := moduleScopeTarget(issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation, surface)
			spy.hits = 0
			response := doModuleScopeRequest(handler, http.MethodGet, target, issued.Token, "")
			if response.Code != http.StatusOK || spy.hits != 1 {
				t.Fatalf("combined %s request = %d hits=%d body=%q", surface, response.Code, spy.hits, response.Body.String())
			}
		}
	})

	t.Run("ContributionMissingSurfaceFailsClosed", func(t *testing.T) {
		spy := &moduleScopeSpy{}
		handler, authority := newModuleScopeHandler(t, spy)
		fixture := mcpserver.SidebarOnlyFixture()
		issued := issueModuleScope(t, authority, fixture.ModuleID, fixture.ResourceNamespace+":side-res-1", "sess-1", 1, time.Hour)
		values := url.Values{}
		values.Set("resource", issued.ResourceID)
		values.Set("session", issued.SessionID)
		values.Set("generation", strconv.FormatUint(issued.Generation, 10))
		target := "https://pixie.example/v1/modules/" + issued.ModuleID + "/resources?" + values.Encode()
		assertModuleScopeFailure(t, doModuleScopeRequest(handler, http.MethodGet, target, issued.Token, ""), spy, "missing surface")
		badSurface := moduleScopeTarget(issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation, "rail")
		assertModuleScopeFailure(t, doModuleScopeRequest(handler, http.MethodGet, badSurface, issued.Token, ""), spy, "invalid surface")
	})
}

func TestModuleScopeDescriptorFixturesRegister(t *testing.T) {
	descriptors := mcpserver.DefaultModuleDescriptors()
	for _, id := range []string{"browser", "fixture-sidebar", "fixture-viewer"} {
		descriptor, ok := descriptors[id]
		if !ok {
			t.Fatalf("default descriptors missing %q", id)
		}
		if err := mcpserver.ValidateDescriptor(descriptor); err != nil {
			t.Fatalf("default descriptor %q invalid: %v", id, err)
		}
	}
	if scope, err := descriptors["browser"].ContributionScope(); err != nil || scope != mcpserver.DescriptorCombined {
		t.Fatalf("browser contribution scope = %q err=%v", scope, err)
	}
	if scope, err := descriptors["fixture-sidebar"].ContributionScope(); err != nil || scope != mcpserver.DescriptorSidebarOnly {
		t.Fatalf("sidebar contribution scope = %q err=%v", scope, err)
	}
	if scope, err := descriptors["fixture-viewer"].ContributionScope(); err != nil || scope != mcpserver.DescriptorViewerOnly {
		t.Fatalf("viewer contribution scope = %q err=%v", scope, err)
	}
}

func TestModuleScopeUnknownModuleAndMethodFailClosed(t *testing.T) {
	spy := &moduleScopeSpy{}
	handler, authority := newModuleScopeHandler(t, spy)
	fixture := mcpserver.SidebarOnlyFixture()
	issued := issueModuleScope(t, authority, fixture.ModuleID, fixture.ResourceNamespace+":side-res-1", "sess-1", 1, time.Hour)

	unknown := moduleScopeTarget("unknown-module", "unknown-module:opaque-1", "sess-1", 1, mcpserver.ModuleSurfaceSidebar)
	assertModuleScopeFailure(t, doModuleScopeRequest(handler, http.MethodGet, unknown, issued.Token, ""), spy, "unknown module")

	valid := moduleScopeTarget(issued.ModuleID, issued.ResourceID, issued.SessionID, issued.Generation, mcpserver.ModuleSurfaceSidebar)
	post := doModuleScopeRequest(handler, http.MethodPost, valid, issued.Token, "")
	if post.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST module resource status = %d, want %d", post.Code, http.StatusMethodNotAllowed)
	}
	if spy.hits != 0 {
		t.Fatalf("POST delegated to module service (%d hits)", spy.hits)
	}
}
