package mcpserver_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/mcpserver"
)

func TestDescriptorFixturesRegisterWithoutShellEdits(t *testing.T) {
	combined := mcpserver.BrowserDescriptorFixture()
	sidebarOnly := mcpserver.SidebarOnlyFixture()
	viewerOnly := mcpserver.ViewerOnlyFixture()

	for name, descriptor := range map[string]mcpserver.FrontendDescriptor{
		"browser": combined, "sidebar-only": sidebarOnly, "viewer-only": viewerOnly,
	} {
		if err := mcpserver.ValidateDescriptor(descriptor); err != nil {
			t.Fatalf("%s fixture invalid: %v (%#v)", name, err, descriptor)
		}
	}

	if scope, err := combined.ContributionScope(); err != nil || scope != mcpserver.DescriptorCombined {
		t.Fatalf("browser scope = %q err = %v", scope, err)
	}
	if scope, err := sidebarOnly.ContributionScope(); err != nil || scope != mcpserver.DescriptorSidebarOnly {
		t.Fatalf("sidebar-only scope = %q err = %v", scope, err)
	}
	if scope, err := viewerOnly.ContributionScope(); err != nil || scope != mcpserver.DescriptorViewerOnly {
		t.Fatalf("viewer-only scope = %q err = %v", scope, err)
	}

	if slots := combined.Slots(); !equalSlots(slots, []int{6, 5, 4}) {
		t.Fatalf("browser slots = %v", slots)
	}
	if slots := sidebarOnly.Slots(); !equalSlots(slots, []int{6, 5}) {
		t.Fatalf("sidebar-only slots = %v", slots)
	}
	if slots := viewerOnly.Slots(); !equalSlots(slots, []int{6, 4}) {
		t.Fatalf("viewer-only slots = %v", slots)
	}

	// Fixture resource IDs stay namespaced opaque references, never paths.
	for _, fixture := range []mcpserver.FrontendDescriptor{combined, sidebarOnly, viewerOnly} {
		id := fixture.ResourceNamespace + ":opaque-123_AZ"
		if err := mcpserver.ValidateResourceID(id, fixture.ResourceNamespace); err != nil {
			t.Fatalf("fixture %q resource id invalid: %v", fixture.ModuleID, err)
		}
	}
}

func TestValidateDescriptorRejectsUntrustedShapes(t *testing.T) {
	valid := mcpserver.SidebarOnlyFixture()
	cases := map[string]func(*mcpserver.FrontendDescriptor){
		"empty module":       func(d *mcpserver.FrontendDescriptor) { d.ModuleID, d.ResourceNamespace = "", "" },
		"uppercase module":   func(d *mcpserver.FrontendDescriptor) { d.ModuleID, d.ResourceNamespace = "Fixture", "Fixture" },
		"module with slash":  func(d *mcpserver.FrontendDescriptor) { d.ModuleID, d.ResourceNamespace = "a/b", "a/b" },
		"bad version":        func(d *mcpserver.FrontendDescriptor) { d.Version = "latest" },
		"empty version":      func(d *mcpserver.FrontendDescriptor) { d.Version = "" },
		"label html":         func(d *mcpserver.FrontendDescriptor) { d.Label = "<script>alert(1)</script>" },
		"label javascript":   func(d *mcpserver.FrontendDescriptor) { d.Label = "x javascript:alert(1)" },
		"icon url":           func(d *mcpserver.FrontendDescriptor) { d.Icon = "https://example.com/icon.svg" },
		"icon svg":           func(d *mcpserver.FrontendDescriptor) { d.Icon = "<svg></svg>" },
		"icon path":          func(d *mcpserver.FrontendDescriptor) { d.Icon = "../icons/evil" },
		"neither surface":    func(d *mcpserver.FrontendDescriptor) { d.Sidebar, d.Viewer = false, false },
		"bad context":        func(d *mcpserver.FrontendDescriptor) { d.ContextScope = "global" },
		"empty context":      func(d *mcpserver.FrontendDescriptor) { d.ContextScope = "" },
		"namespace mismatch": func(d *mcpserver.FrontendDescriptor) { d.ResourceNamespace = "other-module" },
		"empty namespace":    func(d *mcpserver.FrontendDescriptor) { d.ResourceNamespace = "" },
	}
	for name, mutate := range cases {
		descriptor := valid
		mutate(&descriptor)
		if err := mcpserver.ValidateDescriptor(descriptor); err == nil {
			t.Fatalf("%s was accepted (%#v)", name, descriptor)
		}
	}
}

func TestValidateDescriptorJSONRejectsRemoteCode(t *testing.T) {
	fixture := mcpserver.ViewerOnlyFixture()
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mcpserver.ValidateDescriptorJSON(encoded); err != nil {
		t.Fatalf("valid descriptor JSON rejected: %v", err)
	}

	remoteCodeDocs := []string{
		`{"moduleId":"fixture-viewer","version":"1.0.0","label":"Viewer","icon":"viewer","sidebar":false,"viewer":true,"contextScope":"project","resourceNamespace":"fixture-viewer","scriptUrl":"https://example.com/app.js"}`,
		`{"moduleId":"fixture-viewer","version":"1.0.0","label":"Viewer","icon":"viewer","sidebar":false,"viewer":true,"contextScope":"project","resourceNamespace":"fixture-viewer","html":"<div>hi</div>"}`,
		`{"moduleId":"fixture-viewer","version":"1.0.0","label":"Viewer","icon":"viewer","sidebar":false,"viewer":true,"contextScope":"project","resourceNamespace":"fixture-viewer","svg":"<svg></svg>"}`,
		`{"moduleId":"fixture-viewer","version":"1.0.0","label":"Viewer","icon":"viewer","sidebar":false,"viewer":true,"contextScope":"project","resourceNamespace":"fixture-viewer","remoteEntry":"https://example.com/remote.js"}`,
		`{"moduleId":"fixture-viewer","version":"1.0.0","label":"x javascript:alert(1)","icon":"viewer","sidebar":false,"viewer":true,"contextScope":"project","resourceNamespace":"fixture-viewer"}`,
		`{"moduleId":"fixture-viewer","version":"1.0.0","label":"Viewer","icon":"https://example.com/i.svg","sidebar":false,"viewer":true,"contextScope":"project","resourceNamespace":"fixture-viewer"}`,
	}
	for _, doc := range remoteCodeDocs {
		if _, err := mcpserver.ValidateDescriptorJSON([]byte(doc)); err == nil {
			t.Fatalf("remote-code descriptor accepted: %s", doc)
		}
	}

	if _, err := mcpserver.ValidateDescriptorJSON(nil); err == nil {
		t.Fatal("empty descriptor document was accepted")
	}
	oversized := []byte(`{"moduleId":"a","version":"1.0.0","label":"` + strings.Repeat("x", 17*1024) + `"}`)
	if _, err := mcpserver.ValidateDescriptorJSON(oversized); err == nil {
		t.Fatal("oversized descriptor document was accepted")
	}
}

func TestValidateResourceIDRejectsForgedShapes(t *testing.T) {
	if err := mcpserver.ValidateResourceID("fixture-sidebar:opaque-1_AZ", "fixture-sidebar"); err != nil {
		t.Fatalf("valid resource id rejected: %v", err)
	}
	forged := []struct{ id, namespace string }{
		{"opaque-1", "fixture-sidebar"},
		{"fixture-viewer:opaque-1", "fixture-sidebar"},
		{"fixture-sidebar:", "fixture-sidebar"},
		{"fixture-sidebar:a/b", "fixture-sidebar"},
		{"fixture-sidebar:a.b", "fixture-sidebar"},
		{"https://example.com/x", "fixture-sidebar"},
		{"fixture-sidebar:a:b", "fixture-sidebar"},
		{"", "fixture-sidebar"},
		{"fixture-sidebar:ok", ""},
	}
	for _, item := range forged {
		if err := mcpserver.ValidateResourceID(item.id, item.namespace); err == nil {
			t.Fatalf("forged resource id accepted: %q ns=%q", item.id, item.namespace)
		}
	}
}

func equalSlots(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
