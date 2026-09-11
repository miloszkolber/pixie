package mcpserver

import (
	"fmt"
	"strings"
)

// ModuleDefinition is the trusted, compile-time declaration for one Pixie
// workspace module. Runtime enablement and readiness are deliberately kept out
// of this record so route ownership remains stable while optional services are
// disabled or unavailable.
//
// Routing and lifecycle decisions consume only these declarative flags, never
// a module-name switch: SessionScoped selects session-capability versus
// publisher-bearer MCP authentication and scoped versus instance-wide
// management; RetainWhileDisabled keeps durable status/removal available while
// disabled; StripMCPrefix maps the external MCP prefix to a service-internal
// root for modules whose handler predates the shared prefix scheme;
// DegradesGatewayWhenDisabled preserves the legacy Browser Tools-UI contract
// where a disabled Browser still degrades the gateway while disabled optional
// modules do not.
type ModuleDefinition struct {
	ID                          string
	ExtensionName               string
	DisplayName                 string
	Description                 string
	Path                        string
	Transport                   string
	DefaultEnabled              bool
	SessionScoped               bool
	RetainWhileDisabled         bool
	StripMCPrefix               bool
	DegradesGatewayWhenDisabled bool
	ManagementPaths             []string
	Frontend                    FrontendDescriptor
}

// DefaultModuleDefinitions returns the registered workspace modules in their
// stable catalog order. The definitions contain no executable or remote code;
// module services are composed by the in-process registry.
func DefaultModuleDefinitions() []ModuleDefinition {
	return []ModuleDefinition{
		{
			ID: "browser", ExtensionName: "pixie-browser", DisplayName: "Pixie Browser",
			Description: "Bounded browser automation and browser guidance.", Path: BrowserRoute,
			Transport: transports, DefaultEnabled: true, Frontend: BrowserDescriptorFixture(),
			StripMCPrefix: true, DegradesGatewayWhenDisabled: true,
		},
		{
			ID: "canvas", ExtensionName: "pixie-canvas", DisplayName: "Pixie Canvas",
			Description: "Session-scoped HTML drafts with bounded offline previews.", Path: CanvasRoute,
			Transport: transports, DefaultEnabled: false, SessionScoped: true, RetainWhileDisabled: true, ManagementPaths: []string{CanvasAPIPrefix}, Frontend: CanvasDescriptorFixture(),
		},
		{
			ID: "design", ExtensionName: "pixie-design", DisplayName: "Pixie Design",
			Description: "Instance-wide bounded Design structure and cover inspection.", Path: DesignRoute,
			Transport: transports, DefaultEnabled: false, RetainWhileDisabled: true, ManagementPaths: []string{DesignAPIPrefix}, Frontend: DesignDescriptorFixture(),
		},
	}
}

// FixtureModuleDescriptor is the trusted combined contribution for the generic
// routing fixture. It mirrors the production descriptor shape without claiming
// a production module ID, so generality tests exercise the same validation
// without shell or catalog edits.
func FixtureModuleDescriptor() FrontendDescriptor {
	return FrontendDescriptor{
		ModuleID: "fixture", Version: "1.0.0", Label: "Fixture", Icon: "fixture",
		Sidebar: true, Viewer: true, ContextScope: "instance", ResourceNamespace: "fixture",
	}
}

// FixtureModuleDefinition is a generic MCP+management+artifact module used only
// by focused routing tests. It is never part of DefaultModuleDefinitions or
// the shipped catalog; tests install it through RegisterTestModule so the
// shared definition-driven router proves generality without a module-name
// branch.
func FixtureModuleDefinition() ModuleDefinition {
	return ModuleDefinition{
		ID: "fixture", ExtensionName: "pixie-fixture", DisplayName: "Pixie Fixture",
		Description: "Generic test fixture exercising MCP, management and artifact surfaces.", Path: "/mcp/fixture",
		Transport: transports, DefaultEnabled: false, RetainWhileDisabled: true, ManagementPaths: []string{"/api/fixture"}, Frontend: FixtureModuleDescriptor(),
	}
}

// DefinitionForMCPRoute resolves the owning definition for an MCP route using
// only exact-or-child matching over the supplied definitions. No module-name
// switch: every module shares the same ownership rule.
func DefinitionForMCPRoute(definitions []ModuleDefinition, route string) (ModuleDefinition, bool) {
	for _, definition := range definitions {
		if definition.Path == "" {
			continue
		}
		if route == definition.Path || strings.HasPrefix(route, definition.Path+"/") {
			return definition, true
		}
	}
	return ModuleDefinition{}, false
}

// DefinitionForManagementRoute resolves the owning definition for a human
// management/artifact route using only exact-or-child matching. A similarly
// named route such as /api/design-evil never matches /api/design.
func DefinitionForManagementRoute(definitions []ModuleDefinition, route string) (ModuleDefinition, bool) {
	for _, definition := range definitions {
		for _, prefix := range definition.ManagementPaths {
			if prefix == "" {
				continue
			}
			if route == prefix || strings.HasPrefix(route, prefix+"/") {
				return definition, true
			}
		}
	}
	return ModuleDefinition{}, false
}

// routesOverlap reports exact or parent/child ownership overlap in either
// direction. /mcp/canvas overlaps /mcp/canvas/sub but not /mcp/canvas-evil.
func routesOverlap(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	return strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

// coreReservedRoutes are never claimable by a workspace module. The top-level
// router owns /ws, /auth and /mcp/objective plus health, catalog/status and
// file/artifact namespaces; a module definition overlapping any of them is a
// core-route takeover.
func coreReservedRoutes() []string {
	return []string{
		"/ws",
		"/auth",
		"/mcp/objective",
		CatalogPath,
		StatusPath,
		"/health",
		"/livez",
		"/readyz",
		"/files",
		ModuleResourcePrefix,
		"/v1/artifacts",
	}
}

// ValidateModuleDefinitions rejects duplicate IDs, duplicate/overlapping route
// ownership and core-route takeover without executing or routing anything. It
// is the single gate for both the compiled defaults and test-registered
// fixtures.
func ValidateModuleDefinitions(definitions []ModuleDefinition) error {
	seenIDs := make(map[string]struct{}, len(definitions))
	var owned []string
	for _, definition := range definitions {
		if !isValidDescriptorModuleID(definition.ID) {
			return fmt.Errorf("invalid module id %q", definition.ID)
		}
		if _, duplicated := seenIDs[definition.ID]; duplicated {
			return fmt.Errorf("duplicate module id %q", definition.ID)
		}
		seenIDs[definition.ID] = struct{}{}
		if definition.Path == "" || !strings.HasPrefix(definition.Path, "/mcp/") || strings.HasSuffix(definition.Path, "/") {
			return fmt.Errorf("invalid module MCP path %q for %q: want /mcp/<name>", definition.Path, definition.ID)
		}
		for _, prefix := range definition.ManagementPaths {
			if prefix == "" || !strings.HasPrefix(prefix, "/api/") || strings.HasSuffix(prefix, "/") {
				return fmt.Errorf("invalid module management prefix %q for %q: want /api/<name>", prefix, definition.ID)
			}
		}
		candidates := append([]string{definition.Path}, definition.ManagementPaths...)
		for _, candidate := range candidates {
			for _, existing := range owned {
				if routesOverlap(candidate, existing) {
					return fmt.Errorf("overlapping module route %q conflicts with %q", candidate, existing)
				}
			}
			for _, core := range coreReservedRoutes() {
				if routesOverlap(candidate, core) {
					return fmt.Errorf("module route %q for %q takes over core route %q", candidate, definition.ID, core)
				}
			}
			owned = append(owned, candidate)
		}
		if err := ValidateDescriptor(definition.Frontend); err != nil {
			return fmt.Errorf("invalid descriptor for module %q: %w", definition.ID, err)
		}
		if definition.Frontend.ModuleID != definition.ID {
			return fmt.Errorf("descriptor module id %q must equal definition id %q", definition.Frontend.ModuleID, definition.ID)
		}
	}
	return nil
}

// IsRegisteredMCPRoute reports exact or child routes owned by a registered
// module. Unknown /mcp/* paths remain reserved and are rejected by the
// controller rather than reaching the SPA fallback.
func IsRegisteredMCPRoute(route string) bool {
	_, ok := DefinitionForMCPRoute(DefaultModuleDefinitions(), route)
	return ok
}

// IsSessionScopedMCPRoute reports whether the module authenticates each MCP
// request with a session-scoped capability instead of the publisher-wide
// bearer. The top-level controller still applies Host/Origin/transport policy,
// then delegates capability validation to the owning module. Keep this tied to
// the trusted route declarations so adding another module cannot accidentally
// inherit Canvas' narrower authentication policy.
func IsSessionScopedMCPRoute(route string) bool {
	definition, ok := DefinitionForMCPRoute(DefaultModuleDefinitions(), route)
	if !ok {
		return false
	}
	return definition.SessionScoped
}

// IsRegisteredManagementRoute reports routes owned by a module's human
// management/artifact surface. Prefix matching is constrained to the declared
// path and cannot make a similarly named route such as /api/design-evil owned.
func IsRegisteredManagementRoute(route string) bool {
	_, ok := DefinitionForManagementRoute(DefaultModuleDefinitions(), route)
	return ok
}

// ModuleDefinitionByID resolves one registered definition without exposing a
// mutable registry-owned slice.
func ModuleDefinitionByID(id string) (ModuleDefinition, bool) {
	for _, definition := range DefaultModuleDefinitions() {
		if definition.ID == id {
			return definition, true
		}
	}
	return ModuleDefinition{}, false
}

// IsRegisteredRoute reports either MCP or management ownership.
func IsRegisteredRoute(route string) bool {
	return IsRegisteredMCPRoute(route) || IsRegisteredManagementRoute(route)
}
