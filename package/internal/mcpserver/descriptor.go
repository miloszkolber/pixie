package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Trusted frontend contribution descriptors (MODULE-01).
//
// A descriptor is a compile-time declaration of what shell surfaces a module
// may occupy. It carries only registered names and scope declarations, never
// remote code, markup, or executable references. The shell owns layout and
// routes; contributions receive validated context through scope credentials
// (see descriptor_scope.go), not mutable global state.
//
// Slots follow roadmap/workspace-ui.md and roadmap/extensions.md: rail is
// slot 6, sidebar is slot 5, and the selected viewer is slot 4.
// Sidebar-only, viewer-only, and combined modules are valid.
const (
	RailSlot    = 6
	SidebarSlot = 5
	ViewerSlot  = 4

	maxDescriptorIDLen      = 64
	maxDescriptorLabelLen   = 64
	maxDescriptorIconLen    = 64
	maxDescriptorVersionLen = 32
	maxDescriptorJSONBytes  = 16 * 1024
	maxResourceIDBytes      = 192
	maxResourceOpaqueBytes  = 128
)

// DescriptorScope names the surface combination a descriptor declares.
type DescriptorScope string

const (
	DescriptorSidebarOnly DescriptorScope = "sidebar-only"
	DescriptorViewerOnly  DescriptorScope = "viewer-only"
	DescriptorCombined    DescriptorScope = "combined"
)

// FrontendDescriptor is the trusted UI contribution for one module. Sidebar
// and Viewer mark which shell surfaces the module occupies. At least one must
// be true. ContextScope declares the required selection context
// ("session", "project", or "instance"). ResourceNamespace must equal
// ModuleID so opaque resource IDs stay namespaced to their owning module.
type FrontendDescriptor struct {
	ModuleID          string `json:"moduleId"`
	Version           string `json:"version"`
	Label             string `json:"label"`
	Icon              string `json:"icon"`
	Sidebar           bool   `json:"sidebar"`
	Viewer            bool   `json:"viewer"`
	ContextScope      string `json:"contextScope"`
	ResourceNamespace string `json:"resourceNamespace"`
}

// ContributionScope reports the declared surface combination.
func (d FrontendDescriptor) ContributionScope() (DescriptorScope, error) {
	switch {
	case d.Sidebar && d.Viewer:
		return DescriptorCombined, nil
	case d.Sidebar && !d.Viewer:
		return DescriptorSidebarOnly, nil
	case !d.Sidebar && d.Viewer:
		return DescriptorViewerOnly, nil
	default:
		return "", fmt.Errorf("descriptor %q must contribute a sidebar and/or viewer", d.ModuleID)
	}
}

// Slots returns the shell slots this descriptor occupies: always the rail
// slot plus the declared sidebar and/or viewer slots.
func (d FrontendDescriptor) Slots() []int {
	slots := []int{RailSlot}
	if d.Sidebar {
		slots = append(slots, SidebarSlot)
	}
	if d.Viewer {
		slots = append(slots, ViewerSlot)
	}
	return slots
}

// BrowserDescriptorFixture is the combined sidebar+viewer contribution for the
// Browser module. It mirrors the registered Browser identity without
// importing the Browser service, so descriptor validation stays decoupled
// from module runtime.
func BrowserDescriptorFixture() FrontendDescriptor {
	return FrontendDescriptor{
		ModuleID: "browser", Version: "1.0.0", Label: "Browser", Icon: "browser",
		Sidebar: true, Viewer: true, ContextScope: "session", ResourceNamespace: "browser",
	}
}

// SidebarOnlyFixture is a minimal sidebar-only contribution used to prove
// generality without shell edits.
func SidebarOnlyFixture() FrontendDescriptor {
	return FrontendDescriptor{
		ModuleID: "fixture-sidebar", Version: "1.0.0", Label: "Sidebar Fixture", Icon: "sidebar-fixture",
		Sidebar: true, Viewer: false, ContextScope: "session", ResourceNamespace: "fixture-sidebar",
	}
}

// ViewerOnlyFixture is a minimal viewer-only contribution used to prove
// generality without shell edits.
func ViewerOnlyFixture() FrontendDescriptor {
	return FrontendDescriptor{
		ModuleID: "fixture-viewer", Version: "1.0.0", Label: "Viewer Fixture", Icon: "viewer-fixture",
		Sidebar: false, Viewer: true, ContextScope: "project", ResourceNamespace: "fixture-viewer",
	}
}

// ValidateDescriptor rejects malformed or untrusted contribution shapes. It
// enforces stable IDs, semver versions, plain-text labels, registered icon
// names, an explicit sidebar/viewer declaration, an explicit context scope,
// and namespace-bound resource ownership. Descriptors carry no code fields,
// so there is nothing to execute.
func ValidateDescriptor(d FrontendDescriptor) error {
	if !isValidDescriptorModuleID(d.ModuleID) {
		return fmt.Errorf("invalid descriptor module id %q", d.ModuleID)
	}
	if !isValidDescriptorVersion(d.Version) {
		return fmt.Errorf("invalid descriptor version %q", d.Version)
	}
	if err := validateDescriptorLabel(d.Label); err != nil {
		return err
	}
	if !isValidDescriptorIcon(d.Icon) {
		return fmt.Errorf("invalid descriptor icon %q: want a registered icon name", d.Icon)
	}
	if !d.Sidebar && !d.Viewer {
		return fmt.Errorf("descriptor %q must contribute a sidebar and/or viewer", d.ModuleID)
	}
	switch d.ContextScope {
	case "session", "project", "instance":
	default:
		return fmt.Errorf("invalid descriptor context scope %q", d.ContextScope)
	}
	if d.ResourceNamespace != d.ModuleID {
		return fmt.Errorf("descriptor resource namespace %q must equal module id %q", d.ResourceNamespace, d.ModuleID)
	}
	return nil
}

// ValidateDescriptorJSON parses and validates a descriptor document. Unknown
// top-level fields are rejected so a document cannot smuggle remote-code
// references (script URLs, HTML/SVG markup, bundle entry points) alongside
// the trusted shape. String values containing executable markers are also
// rejected.
func ValidateDescriptorJSON(data []byte) (FrontendDescriptor, error) {
	var zero FrontendDescriptor
	if len(data) == 0 || len(data) > maxDescriptorJSONBytes {
		return zero, fmt.Errorf("invalid descriptor document size %d", len(data))
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return zero, fmt.Errorf("invalid descriptor document: %w", err)
	}
	allowed := map[string]bool{
		"moduleid": true, "version": true, "label": true, "icon": true,
		"sidebar": true, "viewer": true, "contextscope": true, "resourcenamespace": true,
	}
	for key := range raw {
		if !allowed[strings.ToLower(key)] {
			return zero, fmt.Errorf("unknown descriptor field %q: descriptors carry no remote code", key)
		}
	}
	var parsed FrontendDescriptor
	if err := json.Unmarshal(data, &parsed); err != nil {
		return zero, fmt.Errorf("invalid descriptor document: %w", err)
	}
	for _, value := range []string{parsed.ModuleID, parsed.Version, parsed.Label, parsed.Icon, parsed.ContextScope, parsed.ResourceNamespace} {
		if containsExecutableMarker(value) {
			return zero, fmt.Errorf("descriptor value %q contains an executable marker", value)
		}
	}
	if err := ValidateDescriptor(parsed); err != nil {
		return zero, err
	}
	return parsed, nil
}

// ValidateResourceID enforces stable namespaced opaque resource IDs of the
// form "<namespace>:<opaque>". Paths, URLs, bare IDs, and cross-namespace IDs
// are rejected. The namespace must equal the owning module ID.
func ValidateResourceID(id, namespace string) error {
	if !isValidDescriptorModuleID(namespace) {
		return fmt.Errorf("invalid resource namespace %q", namespace)
	}
	if len(id) == 0 || len(id) > maxResourceIDBytes {
		return fmt.Errorf("invalid resource id length %d", len(id))
	}
	prefix := namespace + ":"
	if !strings.HasPrefix(id, prefix) {
		return fmt.Errorf("resource id %q is not in namespace %q", id, namespace)
	}
	opaque := strings.TrimPrefix(id, prefix)
	if len(opaque) == 0 || len(opaque) > maxResourceOpaqueBytes {
		return fmt.Errorf("invalid resource id opaque part length %d", len(opaque))
	}
	for _, r := range opaque {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return fmt.Errorf("invalid resource id %q: opaque part must match [A-Za-z0-9_-]+", id)
	}
	return nil
}

func isValidDescriptorModuleID(id string) bool {
	if len(id) == 0 || len(id) > maxDescriptorIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9' && i > 0:
		case c == '-' && i > 0:
		default:
			return false
		}
	}
	return true
}

func isValidDescriptorIcon(icon string) bool {
	if len(icon) == 0 || len(icon) > maxDescriptorIconLen {
		return false
	}
	for i := 0; i < len(icon); i++ {
		c := icon[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9' && i > 0:
		case c == '-' && i > 0:
		default:
			return false
		}
	}
	return true
}

func isValidDescriptorVersion(version string) bool {
	if len(version) == 0 || len(version) > maxDescriptorVersionLen {
		return false
	}
	core := version
	if idx := strings.Index(core, "+"); idx >= 0 {
		build := core[idx+1:]
		core = core[:idx]
		if !isValidDescriptorVersionSuffix(build) {
			return false
		}
	}
	if idx := strings.Index(core, "-"); idx >= 0 {
		prerelease := core[idx+1:]
		core = core[:idx]
		if !isValidDescriptorVersionSuffix(prerelease) {
			return false
		}
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if len(part) == 0 || len(part) > 8 {
			return false
		}
		for i := 0; i < len(part); i++ {
			if part[i] < '0' || part[i] > '9' {
				return false
			}
		}
	}
	return true
}

func isValidDescriptorVersionSuffix(suffix string) bool {
	if len(suffix) == 0 || len(suffix) > maxDescriptorVersionLen {
		return false
	}
	for i := 0; i < len(suffix); i++ {
		c := suffix[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c == '-', c == '.':
		default:
			return false
		}
	}
	return true
}

func validateDescriptorLabel(label string) error {
	if utf8.RuneCountInString(label) == 0 || utf8.RuneCountInString(label) > maxDescriptorLabelLen || len(label) > 256 {
		return fmt.Errorf("invalid descriptor label length")
	}
	if strings.TrimSpace(label) != label {
		return fmt.Errorf("invalid descriptor label %q", label)
	}
	for _, r := range label {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("invalid descriptor label %q", label)
		}
		if r == '<' || r == '>' {
			return fmt.Errorf("invalid descriptor label %q: plain text only", label)
		}
	}
	if containsExecutableMarker(label) {
		return fmt.Errorf("invalid descriptor label %q: plain text only", label)
	}
	return nil
}

func containsExecutableMarker(value string) bool {
	lowered := strings.ToLower(value)
	for _, marker := range []string{"<script", "<svg", "<iframe", "javascript:", "data:text/html"} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}
