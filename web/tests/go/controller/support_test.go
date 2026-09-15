package controller_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
	piwire "github.com/miloszkolber/pixie/shared/piprotocol"
)

type recordingEvents struct {
	mu      sync.Mutex
	methods []string
}

func (e *recordingEvents) SessionUpdate(context.Context, piwire.SessionNotification) error {
	return nil
}

func (e *recordingEvents) Extension(_ context.Context, method string, _ json.RawMessage) error {
	e.mu.Lock()
	e.methods = append(e.methods, method)
	e.mu.Unlock()
	return nil
}

func (e *recordingEvents) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.methods...)
}

// piInitializeResponse is a deliberately broad LEGACY test double.
//
// It keeps advertising routes (session.delete, mcp.attach, adapter.status,
// session.prompt.resource, ...) that the shared catalog marks absent or
// unavailable so the individual projection tests that predate AUX-25 keep
// exercising their handler paths. It is not an installed-host contract and must
// never be used to claim route support. New tests use bunHostInitializeResponse
// (exactly the generated available set) or a purpose-built profile.
//
// The explicit allowlist below records every file still permitted to call it.
// TestLegacyFixtureUsageIsAllowlisted fails when a file outside the allowlist
// adopts the legacy fixture, so route support cannot re-enter through a
// fixture-only success.
func piInitializeResponse() map[string]any {
	return map[string]any{"protocolVersion": 1, "runtimeId": "fixture-runtime", "bootId": "fixture-boot", "version": "0.85.1", "capabilities": map[string]any{"sessions": 1, "providers": 1, "mcp": 1, "agents": 1, "plans": 1}, "operationSet": map[string]bool{
		"session.list": true, "session.create": true, "session.load": true, "session.prompt": true, "session.cancel": true,
		"session.delete": true, "session.fork": true, "session.prompt.image": true, "session.prompt.resource": true,
		"session.steer": true, "session.rename": true, "session.configure": true,
		"session.release": true, "runtime.release": true, "runtime.releaseToTui": true,
		"session.uiResponse": true, "session.uiCancel": true, "mcp.attach": true,
		"pi.session.info": true, "pi.session.rename": true, "pi.session.steer": true,
		"pi.tools.list": true, "runtime.capabilities": true, "pi.slash-commands.list": true,
		"pi.providers.list": true, "pi.providers.canonical-model-info": true, "pi.providers.inventory.refresh": true, "pi.providers.readiness.check": true,
		"provider.loginStart": true, "provider.loginBegin": true, "provider.loginReply": true, "provider.loginCancel": true,
		"pi.providers.config.read": true, "pi.providers.config.delete": true, "pi.defaults.read": true, "pi.defaults.save": true, "pi.defaults.clear": true,
		"pi.preferences.read": true, "pi.preferences.save": true, "pi.preferences.reset": true, "pi.extensions.list": true, "pi.extensions.configure": true,
		"pi.sources.list": true, "pi.sources.create": true, "pi.sources.update": true, "pi.sources.delete": true, "pi.agent-mentions.list": true,
		"pi.todo.plan": true, "pi.config.extensions.list": true, "pi.config.extensions.add": true, "pi.config.extensions.set-enabled": true,
		"pi.config.extensions.remove": true, "pi.session.extensions.list": true, "pi.session.extensions.add": true, "pi.session.extensions.remove": true,
		"adapter.status": true, "adapter.registerBrowser": true, "adapter.session.forget": true,
	}}
}

// legacyFixtureAllowlist is the AUX-25 migration record for the broad legacy
// hello double. Each entry names the test file and the action that removes it
// from this list. The session and deletion projection files are owned by other
// workers: they are recorded here, not migrated, so this change does not
// collide with their edits. A stale entry is harmless; a new file that calls
// piInitializeResponse without an entry fails the guard test.
var legacyFixtureAllowlist = map[string]string{
	"browser_mcp_test.go":          "stub runtime.hello with bunHostInitializeResponse; keep the unavailable adapter routes as explicit fail-closed cases",
	"deletion_authority_test.go":   "stub runtime.hello with bunHostInitializeResponse and drive deletion through the controller-owned recoverable path",
	"life_stop_test.go":            "stub runtime.hello with bunHostInitializeResponse; the broad release/stop routes are not host routes",
	"mcp_registry_test.go":         "stub runtime.hello with bunHostInitializeResponse; the live mcp.attach route is unavailable",
	"pi_admin_test.go":             "stub runtime.hello with bunHostInitializeResponse; provider routes already exist in the generated available set",
	"pi_client_test.go":            "stub runtime.hello with bunHostInitializeResponse; move the broad DeleteSession/HTTPMCP/Administration compatibility assertion to a purpose-built legacy profile",
	"pi_events_test.go":            "stub runtime.hello with bunHostInitializeResponse before asserting the event projection",
	"pi_native_extensions_test.go": "stub runtime.hello with bunHostInitializeResponse; the generic native-extension route is unavailable",
	"session_canvas_test.go":       "stub runtime.hello with bunHostInitializeResponse; canvas is controller-owned",
	"session_dialogs_test.go":      "stub runtime.hello with bunHostInitializeResponse before driving the UI dialog projection",
	"session_lifecycle_test.go":    "stub runtime.hello with bunHostInitializeResponse; migrate the shared newSessionManager* helper first",
	"session_test.go":              "stub runtime.hello with bunHostInitializeResponse; migrate the shared newSessionManager/newSessionManagerWithPublish helpers first",
	"session_unknown_test.go":      "stub runtime.hello with bunHostInitializeResponse before feeding unknown records",
	"todo_plan_projection_test.go": "stub runtime.hello with bunHostInitializeResponse; plan state is controller-owned with no host route",
}

// TestLegacyFixtureUsageIsAllowlisted fails when a new test file adopts the
// broad legacy hello fixture. Only the recorded migration set may reference it.
func TestLegacyFixtureUsageIsAllowlisted(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read controller test directory: %v", err)
	}
	var unexpected []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || name == "support_test.go" {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(source), "piInitializeResponse(") {
			continue
		}
		if _, allowed := legacyFixtureAllowlist[name]; !allowed {
			unexpected = append(unexpected, name)
		}
	}
	sort.Strings(unexpected)
	if len(unexpected) > 0 {
		t.Fatalf("broad legacy piInitializeResponse used without an allowlist entry: %s", strings.Join(unexpected, ", "))
	}
}

// TestGeneratedSessionListMetadataContract makes the session.list metadata
// requirement explicit in the generated contract: the browser method maps to
// the catalogued host route and is dispatchable, and the host result schema
// requires the listing whose entries carry identity and cwd metadata. The
// browser-facing controller projection stays controller-owned and is validated
// separately by its handler.
func TestGeneratedSessionListMetadataContract(t *testing.T) {
	if route := piwire.CatalogControllerMethodRoutes["session.list"]; route != "session.list" {
		t.Fatalf("session.list route = %q, want the catalogued host route", route)
	}
	if !piwire.CatalogControllerMethodIsAvailable("session.list") {
		t.Fatal("session.list is not dispatchable")
	}
	var fields []piwire.CatalogMethodField
	for _, schema := range piwire.CatalogHostMethodSchemas {
		if schema.Name == "session.list" {
			fields = schema.Result
		}
	}
	if len(fields) != 1 || fields[0].Name != "sessions" || fields[0].Type != "array" {
		t.Fatalf("host session.list result schema drifted: %#v", fields)
	}
}

// generatedOperationSet is exactly the catalog status projection: every
// catalogued operation is present and true only when the catalog marks it
// available. A fixture that advertises anything else is not the Bun host.
func generatedOperationSet() map[string]bool {
	operations := make(map[string]bool, len(piwire.CatalogHostOperations))
	for _, method := range piwire.CatalogHostOperations {
		operations[method] = piwire.CatalogHostOperationIsAvailable(method)
	}
	return operations
}

// bunHostInitializeResponse is the real Bun host profile: the generated
// available set, with the opt-in runtime.restart route disabled by default.
func bunHostInitializeResponse() map[string]any {
	operations := generatedOperationSet()
	operations["runtime.restart"] = false
	return map[string]any{"protocolVersion": 1, "runtimeId": "fixture-runtime", "bootId": "fixture-boot", "version": "0.85.1", "capabilities": map[string]any{"sessions": 1, "agents": 1, "images": 1}, "operationSet": operations}
}

// piInitializeV2Response is the negotiation-aware hello result. It keeps the
// same operation set and capabilities as the v1 fixture so profile projection
// is exercised identically.
func piInitializeV2Response() map[string]any {
	response := piInitializeResponse()
	delete(response, "runtimeId")
	delete(response, "version")
	response["protocolVersion"] = 2
	response["supportedProtocolVersions"] = []int{2, 1}
	response["hostIdentity"] = "v2-runtime"
	response["nativeVersion"] = "0.85.1"
	return response
}

func writeRPC(connection *websocket.Conn, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return connection.Write(context.Background(), websocket.MessageText, payload)
}

func fixtureNotification(method, sessionID string, update map[string]any) map[string]any {
	if method != "session.event" {
		return map[string]any{"sessionId": sessionID, "update": update}
	}
	if event, ok := update["__native"].(map[string]any); ok {
		return map[string]any{"sessionId": sessionID, "event": event}
	}
	var event map[string]any
	switch update["sessionUpdate"] {
	case "session_info_update":
		event = map[string]any{"type": "session_info_changed", "name": update["title"]}
	case "user_message_chunk":
		event = map[string]any{"type": "message_start", "message": map[string]any{"role": "user", "messageId": update["messageId"], "content": []any{update["content"]}}}
	case "agent_message_chunk", "agent_thought_chunk":
		kind := "text_delta"
		if update["sessionUpdate"] == "agent_thought_chunk" {
			kind = "thinking_delta"
		}
		event = map[string]any{"type": "message_update", "message": map[string]any{"role": "assistant", "messageId": update["messageId"]}, "assistantMessageEvent": map[string]any{"type": kind, "delta": update["content"].(map[string]any)["text"]}}
	case "tool_call":
		event = map[string]any{"type": "tool_execution_start", "toolCallId": update["toolCallId"], "toolName": update["title"], "args": update["rawInput"]}
	case "plan":
		event = map[string]any{"type": "plan", "entries": update["entries"]}
	default:
		panic(fmt.Sprintf("fixture needs native event for %v", update["sessionUpdate"]))
	}
	return map[string]any{"sessionId": sessionID, "event": event}
}

// TestGeneratedHostFixtureAdvertisesOnlyAvailableRoutes binds the Go fixture to
// the generated host status vocabulary. A route the catalog marks absent or
// unavailable must be false in the real-host fixture, and an unknown key must
// never appear.
func TestGeneratedHostFixtureAdvertisesOnlyAvailableRoutes(t *testing.T) {
	hello := bunHostInitializeResponse()
	operations, ok := hello["operationSet"].(map[string]bool)
	if !ok {
		t.Fatalf("fixture operationSet has unexpected type %T", hello["operationSet"])
	}
	if len(operations) != len(piwire.CatalogHostOperations) {
		t.Fatalf("fixture advertises %d operations, catalog lists %d", len(operations), len(piwire.CatalogHostOperations))
	}
	for name, advertised := range operations {
		if !piwire.CatalogHostOperationSet[name] {
			t.Fatalf("fixture advertises an unknown native route %q", name)
		}
		expected := piwire.CatalogHostOperationIsAvailable(name) && name != "runtime.restart"
		if advertised != expected {
			t.Fatalf("fixture advertises %q=%v, catalog status %q requires %v", name, advertised, piwire.CatalogHostOperationStatus[name], expected)
		}
	}
	for _, name := range []string{
		"session.delete", "session.archive", "session.steer", "session.prompt.image",
		"session.prompt.resource", "mcp.attach", "adapter.status", "pi.tools.list",
		"runtime.capabilities", "pi.session.info",
	} {
		if operations[name] {
			t.Fatalf("real-host fixture advertises absent/unavailable route %q", name)
		}
	}
}

// TestGeneratedOwnershipMatrixRejectsUnknownAndNonAvailableRoutes exercises the
// generated ownership/status matrix through the Go accessors. The contract must
// fail on an unknown native route and on a route the catalog marks absent or
// unavailable.
func TestGeneratedOwnershipMatrixRejectsUnknownAndNonAvailableRoutes(t *testing.T) {
	if piwire.CatalogNativeRouteKnown("nope.unknown") {
		t.Fatal("ownership matrix accepted an unknown native route")
	}
	if piwire.CatalogNativeRouteKnown("controller:not-a-subsystem") {
		t.Fatal("ownership matrix accepted an unknown controller subsystem")
	}
	if !piwire.CatalogNativeRouteKnown("session.list") {
		t.Fatal("ownership matrix rejected a catalogued host route")
	}
	if !piwire.CatalogControllerMethodIsAvailable("session.list") {
		t.Fatal("ownership matrix marked an available method unavailable")
	}
	for _, method := range []string{"session.archive", "session.unarchive", "session.steer", "session.toolList"} {
		if piwire.CatalogControllerMethodIsAvailable(method) {
			t.Fatalf("ownership matrix advertised non-available method %q as available", method)
		}
	}
	for _, method := range piwire.CatalogControllerMethods {
		route := piwire.CatalogControllerMethodRoutes[method]
		if !piwire.CatalogNativeRouteKnown(route) {
			t.Fatalf("%s maps to unknown native route %q", method, route)
		}
		status := piwire.CatalogControllerMethodStatus[method]
		if status == piwire.CatalogHostOperationAvailable && piwire.CatalogHostOperationStatus[route] != "" && piwire.CatalogHostOperationStatus[route] != piwire.CatalogHostOperationAvailable {
			t.Fatalf("%s advertises non-available host route %q as available", method, route)
		}
	}
}
