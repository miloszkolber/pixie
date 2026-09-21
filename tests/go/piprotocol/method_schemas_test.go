package piprotocol_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/piprotocol"
)

// TestHostMethodParamsRejectMalformed proves the controller/host boundary
// rejects a malformed payload before dispatch while accepting additive fields.
func TestHostMethodParamsRejectMalformed(t *testing.T) {
	cases := []struct {
		name   string
		method string
		raw    string
		ok     bool
	}{
		{"valid prompt", "session.prompt", `{"sessionId":"s","content":[{"type":"text","text":"hi"}]}`, true},
		{"empty prompt content probe", "session.prompt", `{"sessionId":"s","content":null}`, true},
		{"additive field", "session.prompt", `{"sessionId":"s","future":1}`, true},
		{"missing sessionId", "session.prompt", `{"content":[]}`, false},
		{"wrong type", "session.prompt", `{"sessionId":7}`, false},
		{"not an object", "session.prompt", `"nope"`, false},
		{"valid create", "session.create", `{"cwd":"/tmp"}`, true},
		{"unknown method is envelope only", "not.a.method", `{"anything":true}`, true},
	}
	for _, test := range cases {
		err := piprotocol.ValidateHostMethodParams(test.method, json.RawMessage(test.raw))
		if test.ok && err != nil {
			t.Fatalf("%s: unexpected error: %v", test.name, err)
		}
		if !test.ok && err == nil {
			t.Fatalf("%s: expected an error", test.name)
		}
	}
}

// TestHostMethodResultsRejectInvalid proves an invalid host result fails before
// it reaches the controller projection.
func TestHostMethodResultsRejectInvalid(t *testing.T) {
	if err := piprotocol.ValidateHostMethodResult("session.create", json.RawMessage(`{"sessionId":"s","additive":1}`)); err != nil {
		t.Fatalf("additive create result rejected: %v", err)
	}
	if err := piprotocol.ValidateHostMethodResult("session.create", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "sessionId") {
		t.Fatalf("missing create sessionId was accepted: %v", err)
	}
	if err := piprotocol.ValidateHostMethodResult("session.list", json.RawMessage(`{"sessions":[]}`)); err != nil {
		t.Fatalf("valid list result rejected: %v", err)
	}
	if err := piprotocol.ValidateHostMethodResult("session.list", json.RawMessage(`{"sessions":"no"}`)); err == nil {
		t.Fatal("invalid list result was accepted")
	}
	if err := piprotocol.ValidateHostMethodResult("not.a.method", json.RawMessage(`null`)); err != nil {
		t.Fatalf("unknown method result was not skipped: %v", err)
	}
}

// TestExtendedHostMethodParamsRejectMalformed covers the AUX-34 extension to
// the remaining core and mutating host routes: a missing required field fails
// before dispatch and an additive unknown field stays compatible.
func TestExtendedHostMethodParamsRejectMalformed(t *testing.T) {
	cases := []struct {
		name   string
		method string
		raw    string
		ok     bool
	}{
		{"sources create valid", "pi.sources.create", `{"type":"agent","projectDir":"/p","name":"A","description":"d","content":"c","target":{}}`, true},
		{"sources create additive", "pi.sources.create", `{"type":"agent","projectDir":"/p","name":"A","description":"d","content":"c","target":{},"future":1}`, true},
		{"sources create missing name", "pi.sources.create", `{"type":"agent","projectDir":"/p","description":"d","content":"c","target":{}}`, false},
		{"sources list missing type", "pi.sources.list", `{"includeProjectSources":true}`, false},
		{"sources list valid with additive", "pi.sources.list", `{"type":"agent","includeProjectSources":false,"future":true}`, true},
		{"sources delete missing revision", "pi.sources.delete", `{"type":"agent","projectDir":"/p","path":"/p/a"}`, false},
		{"mcp probe missing definition", "pi.mcp.servers.probe", `{}`, false},
		{"mcp probe valid", "pi.mcp.servers.probe", `{"definition":{"url":"https://example"}}`, true},
		{"mcp remove missing token", "pi.mcp.servers.remove", `{"name":"n","requireNoOtherLayerCollisions":true}`, false},
		{"login start missing loginId", "provider.loginStart", `{"providerId":"p","type":"oauth"}`, false},
		{"login reply valid", "provider.loginReply", `{"loginId":"l","value":"v"}`, true},
		{"preferences read valid additive", "pi.preferences.read", `{"keys":["a"],"future":true}`, true},
		{"preferences save missing values", "pi.preferences.save", `{}`, false},
		{"extensions configure missing confirmation", "pi.extensions.configure", `{"scope":"user","enabled":true,"resourceKey":"r","expectedRevision":"e"}`, false},
		// An omitted optional providerIds stays compatible with a selective
		// caller; a present wrong type still fails.
		{"providers list empty valid", "pi.providers.list", `{}`, true},
		{"providers list wrong type", "pi.providers.list", `{"providerIds":"nope"}`, false},
	}
	for _, test := range cases {
		err := piprotocol.ValidateHostMethodParams(test.method, json.RawMessage(test.raw))
		if test.ok && err != nil {
			t.Fatalf("%s: unexpected error: %v", test.name, err)
		}
		if !test.ok && err == nil {
			t.Fatalf("%s: expected an error", test.name)
		}
	}
}

// TestHostErrorEnvelopeRequiresCodeAndMessage pins the versioned host error shape.
func TestHostErrorEnvelopeRequiresCodeAndMessage(t *testing.T) {
	if err := piprotocol.ValidateHostErrorEnvelope(json.RawMessage(`{"code":-32004,"message":"nope"}`)); err != nil {
		t.Fatalf("valid error envelope rejected: %v", err)
	}
	if err := piprotocol.ValidateHostErrorEnvelope(json.RawMessage(`{"code":-32004}`)); err == nil {
		t.Fatal("error envelope without a message was accepted")
	}
	if err := piprotocol.ValidateHostErrorEnvelope(json.RawMessage(`"nope"`)); err == nil {
		t.Fatal("non-object error envelope was accepted")
	}
}

// TestControllerMethodSchemasPinned keeps a generated example stable so the
// TypeScript drift check has a concrete Go counterpart.
func TestControllerMethodSchemasPinned(t *testing.T) {
	found := false
	for _, schema := range piprotocol.CatalogControllerMethodSchemas {
		if schema.Name != "model.clampThinking" {
			continue
		}
		found = true
		if len(schema.Result) != 1 || schema.Result[0].Name != "level" || schema.Result[0].Type != "string" {
			t.Fatalf("model.clampThinking result schema drifted: %#v", schema.Result)
		}
	}
	if !found {
		t.Fatal("generated controller method schemas are missing model.clampThinking")
	}
}
