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

// AUX-25 removed the broad legacy piInitializeResponse double. Every
// controller fixture now uses bunHostInitializeResponse (the generated
// available set) or one of the narrowly named purpose-built profiles declared
// below. This guard keeps the broad double from re-entering, so a route-support
// claim can never come from a fixture-only success.
func TestNoBroadLegacyHostFixture(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read controller test directory: %v", err)
	}
	var offenders []string
	for _, entry := range entries {
		name := entry.Name()
		// support_test.go declares the fixtures; the guard is about their use.
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || name == "support_test.go" {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(source), "piInitializeResponse(") {
			offenders = append(offenders, name)
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Fatalf("broad legacy host fixture reintroduced in: %s", strings.Join(offenders, ", "))
	}
}

// purposeBuiltProfileOverrides is the AUX-25 justification table for the
// purpose-built test profiles below. A profile may only advertise a host route
// the generated catalog marks absent or unavailable when that exact route is
// declared here. This replaces the per-file broad-fixture allowlist with a
// per-route justification: a profile cannot silently grow broader.
var purposeBuiltProfileOverrides = map[string]map[string]bool{
	"legacyCompatibilityHostResponse": {
		"session.delete":       true,
		"session.prompt.image": true,
		"mcp.attach":           true,
	},
	"deletionAuthorityHostResponse": {"session.delete": true},
	"promptResourceHostResponse":    {"session.prompt.resource": true},
	"adapterStatusHostResponse":     {"adapter.status": true},
	"canvasHostResponse": {
		"mcp.attach":     true,
		"session.delete": true,
	},
}

// TestPurposeBuiltProfilesStayJustified fails when a purpose-built host
// profile advertises an absent or unavailable route that is not declared in
// purposeBuiltProfileOverrides, so fixture-only support cannot re-enter.
func TestPurposeBuiltProfilesStayJustified(t *testing.T) {
	profiles := map[string]map[string]any{
		"legacyCompatibilityHostResponse": legacyCompatibilityHostResponse(),
		"deletionAuthorityHostResponse":   deletionAuthorityHostResponse(),
		"promptResourceHostResponse":      promptResourceHostResponse(),
		"adapterStatusHostResponse":       adapterStatusHostResponse(),
		"canvasHostResponse":              canvasHostResponse(),
	}
	for name, response := range profiles {
		operations, ok := response["operationSet"].(map[string]bool)
		if !ok {
			t.Fatalf("%s has no operationSet", name)
		}
		for method, advertised := range operations {
			if !advertised || piwire.CatalogHostOperationIsAvailable(method) {
				continue
			}
			if !purposeBuiltProfileOverrides[name][method] {
				t.Fatalf("%s advertises %q without a declared justification", name, method)
			}
		}
	}
}

// TestGeneratedSessionListMetadataContract makes the session.list metadata
// requirement explicit in the generated contract: the browser method maps to
// the catalogued host route and is dispatchable, and the host result schema
// requires a `sessions` array. Entry identity and cwd are returned by the host
// projection but are not contract-enforced here; the browser-facing controller
// projection stays controller-owned and is validated separately by its handler.
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
// same operation set and capabilities as the real host profile so profile
// projection is exercised identically.
func piInitializeV2Response() map[string]any {
	response := bunHostInitializeResponse()
	delete(response, "runtimeId")
	delete(response, "version")
	response["protocolVersion"] = 2
	response["supportedProtocolVersions"] = []int{2, 1}
	response["hostIdentity"] = "v2-runtime"
	response["nativeVersion"] = "0.85.1"
	return response
}

// legacyCompatibilityHostResponse is a purpose-built pre-Bun host profile for
// the compatibility projection test. It adds only the retired routes that
// assertion inspects (delete, image and HTTP MCP) on top of the real host set,
// so the broad legacy double is not needed. It is not a general-purpose
// fixture and must not be used to claim live route support.
func legacyCompatibilityHostResponse() map[string]any {
	response := bunHostInitializeResponse()
	operations := response["operationSet"].(map[string]bool)
	operations["session.delete"] = true
	operations["session.prompt.image"] = true
	operations["mcp.attach"] = true
	return response
}

// deletionAuthorityHostResponse is a purpose-built test profile for the
// controller-owned deletion authority/recovery tests. The real Bun host marks
// session.delete absent (deletion is controller-owned), so
// bunHostInitializeResponse never advertises it. This profile enables exactly
// that one route so the paired/legacy/auto binding rules stay executable, and
// it advertises no other non-available route.
func deletionAuthorityHostResponse() map[string]any {
	response := bunHostInitializeResponse()
	response["operationSet"].(map[string]bool)["session.delete"] = true
	return response
}

// promptResourceHostResponse is a purpose-built test profile for the text
// resource embedding and projection tests. The real Bun host marks
// session.prompt.resource unavailable, so bunHostInitializeResponse never
// advertises it. This profile enables exactly that one route so the embedding
// wire shape stays covered; the real-host fail-closed behavior is asserted
// separately.
func promptResourceHostResponse() map[string]any {
	response := bunHostInitializeResponse()
	response["operationSet"].(map[string]bool)["session.prompt.resource"] = true
	return response
}

// adapterStatusHostResponse is a purpose-built test profile for the legacy
// pi-mcp-adapter bridge projection. The real Bun host removed adapter.status,
// so bunHostInitializeResponse never advertises it. This profile enables
// exactly that one route so the compatibility projection stays covered; the
// real-host fail-open behavior is asserted separately by
// TestMCPAdapterStatusFailsOpenOnBunHostWithoutAdapterRoute.
func adapterStatusHostResponse() map[string]any {
	response := bunHostInitializeResponse()
	response["operationSet"].(map[string]bool)["adapter.status"] = true
	return response
}

// canvasHostResponse is a purpose-built test profile for the optional Canvas
// attach path. The real Bun host marks mcp.attach unavailable, so
// bunHostInitializeResponse never advertises it and canvas stays off. This
// profile enables exactly that route (plus session.delete, which the
// controller-owned deletion path needs to revoke Canvas authority) so the
// controller-owned attachment lifecycle stays covered; ordinary chat and the
// real-host absence of Canvas are unaffected.
func canvasHostResponse() map[string]any {
	response := bunHostInitializeResponse()
	operations := response["operationSet"].(map[string]bool)
	operations["mcp.attach"] = true
	operations["session.delete"] = true
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
