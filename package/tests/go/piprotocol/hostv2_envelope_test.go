package piprotocol_test

import (
	"encoding/json"
	"strings"
	"testing"

	piwire "github.com/miloszkolber/pixie/internal/piprotocol"
)

func TestHostV2RequestFrameAcceptsValidEnvelope(t *testing.T) {
	raw := []byte(`{"id":1,"method":"session.list","params":{}}`)
	req, err := piwire.ParseHostV2RequestFrame(raw)
	if err != nil {
		t.Fatalf("valid host v2 frame rejected: %v", err)
	}
	if int64(req.ID) != 1 || req.Method != "session.list" {
		t.Fatalf("parsed envelope mismatch: %#v", req)
	}
	if got := piwire.HostV2ProtocolVersion; got != 2 {
		t.Fatalf("host v2 protocol version moved: %d", got)
	}
}

func TestHostV2FrameCloseCodes(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    piwire.HostV2CloseCode
	}{
		{"malformed json closes 1007", `{"id":1,`, piwire.HostV2CloseInvalidJSON},
		{"array envelope closes 1008", `[1,2,3]`, piwire.HostV2CloseInvalidEnvelope},
		{"string id closes 1008", `{"id":"001","method":"session.list","params":{}}`, piwire.HostV2CloseInvalidEnvelope},
		{"zero id closes 1008", `{"id":0,"method":"session.list","params":{}}`, piwire.HostV2CloseInvalidEnvelope},
		{"unsafe id closes 1008", `{"id":9007199254740992,"method":"session.list","params":{}}`, piwire.HostV2CloseInvalidEnvelope},
		{"empty method closes 1008", `{"id":1,"method":"","params":{}}`, piwire.HostV2CloseInvalidEnvelope},
		{"array params close 1008", `{"id":1,"method":"session.list","params":[]}`, piwire.HostV2CloseInvalidEnvelope},
		{"missing hello closes 1008", `{"id":1,"method":"session.list","params":{}}`, piwire.HostV2CloseInvalidEnvelope},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.name == "missing hello closes 1008" {
				req, parseErr := piwire.ParseHostV2RequestFrame([]byte(tc.payload))
				if parseErr != nil {
					t.Fatalf("setup frame should parse: %v", parseErr)
				}
				err = piwire.ValidateHostV2HelloRequest(req)
			} else {
				_, err = piwire.ParseHostV2RequestFrame([]byte(tc.payload))
			}
			if err == nil {
				t.Fatal("invalid frame was admitted")
			}
			code, ok := piwire.HostV2CloseCodeFor(err)
			if !ok || code != tc.want {
				t.Fatalf("close code = %v,%v, want %v", code, ok, tc.want)
			}
		})
	}
}

func TestHostV2OversizeFrameCloses1009(t *testing.T) {
	big := make([]byte, piwire.FrameMaxBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	if _, err := piwire.ParseHostV2RequestFrame(big); err == nil {
		t.Fatal("oversize frame was admitted")
	} else if code, _ := piwire.HostV2CloseCodeFor(err); code != piwire.HostV2CloseMessageTooBig {
		t.Fatalf("oversize close = %v, want 1009", code)
	}
}

func TestHostV2HelloRequiresVersionTwo(t *testing.T) {
	v2, err := piwire.ParseHostV2RequestFrame([]byte(`{"id":1,"method":"runtime.hello","params":{"protocolVersion":2}}`))
	if err != nil {
		t.Fatalf("v2 hello rejected: %v", err)
	}
	if err := piwire.ValidateHostV2HelloRequest(v2); err != nil {
		t.Fatalf("v2 hello handshake rejected: %v", err)
	}
	v1, err := piwire.ParseHostV2RequestFrame([]byte(`{"id":2,"method":"runtime.hello","params":{"protocolVersion":1}}`))
	if err != nil {
		t.Fatalf("v1-shaped frame should still parse as envelope: %v", err)
	}
	if err := piwire.ValidateHostV2HelloRequest(v1); err == nil {
		t.Fatal("v1 hello was accepted as v2 handshake")
	} else if code, _ := piwire.HostV2CloseCodeFor(err); code != piwire.HostV2CloseInvalidEnvelope {
		t.Fatalf("v1 hello close = %v, want 1008", code)
	}
}

func TestHostV2HelloResultCarriesIdentityWithoutCredentials(t *testing.T) {
	result := piwire.HostV2HelloResult{
		ProtocolVersion:  2,
		HostIdentity:     "host-123",
		BootID:           "boot-456",
		SourceCommit:     "0123456789abcdef0123456789abcdef01234567",
		ReleaseID:        "sha-0123456789ab",
		NativeVersion:    "0.85.1",
		NativeExecutable: "/usr/bin/pi",
		Capabilities:     map[string]int{"sessions": 1, "providers": 1},
	}
	if err := piwire.ValidateHostV2HelloResult(result); err != nil {
		t.Fatalf("valid hello result rejected: %v", err)
	}
	raw, _ := json.Marshal(result)
	if err := piwire.HostV2HelloResultHasNoCredentials(raw); err != nil {
		t.Fatalf("valid hello flagged as credential leak: %v", err)
	}
	leaky := []byte(`{"protocolVersion":2,"hostIdentity":"h","bootId":"b","secret":"s"}`)
	if err := piwire.HostV2HelloResultHasNoCredentials(leaky); err == nil {
		t.Fatal("hello carrying a secret was admitted")
	}
	// Browser protocol 88 is a separate baseline, never a host version.
	if piwire.HostV2ProtocolVersion == 88 {
		t.Fatal("host v2 must not alias browser protocol 88")
	}
}

func TestHostV2DuplicateClosesWhileV1ShimKeepsFrame(t *testing.T) {
	set := piwire.NewHostV2InflightSet()
	if frameErr, closeErr := set.Add(1); frameErr != nil || closeErr != nil {
		t.Fatalf("first add rejected: %v %v", frameErr, closeErr)
	}
	_, closeErr := set.Add(1)
	if closeErr == nil || closeErr.Code != piwire.HostV2CloseInvalidEnvelope {
		t.Fatalf("v2 duplicate must close 1008, got %v", closeErr)
	}
	// v1 compatibility: the same duplicate would have been an error frame.
	legacy := piwire.HostV2V1DuplicateErrorFrame(1)
	if legacy.Error.Code != -32000 {
		t.Fatalf("v1 duplicate shim code = %v, want -32000", legacy.Error.Code)
	}
	if int64(legacy.ID) != 1 {
		t.Fatalf("v1 duplicate shim id mismatch: %#v", legacy)
	}
}

func TestHostV2TypedErrorsAndUnknownMethod(t *testing.T) {
	notFound := piwire.NewHostV2MethodNotFound("session.unknown")
	if notFound.Code != -32601 || notFound.Reason != piwire.HostV2ReasonMethodNotFound {
		t.Fatalf("method-not-found mistyped: %#v", notFound)
	}
	conflict := piwire.NewHostV2ResourceConflict("revision mismatch")
	capability := piwire.NewHostV2CapabilityUnavailable("mcp unavailable")
	delivery := piwire.NewHostV2DeliveryUncertain("lost acknowledgement")
	persistence := piwire.NewHostV2PersistenceUncertain("installed but unconfirmed")
	seen := map[piwire.HostV2ErrorCode]bool{}
	for _, detail := range []*piwire.HostV2ErrorDetail{conflict, capability, delivery, persistence, notFound} {
		if seen[detail.Code] {
			t.Fatalf("typed error code collision: %v", detail.Code)
		}
		seen[detail.Code] = true
		raw, _ := json.Marshal(detail)
		var roundTrip piwire.HostV2ErrorDetail
		if err := json.Unmarshal(raw, &roundTrip); err != nil || roundTrip.Code != detail.Code || roundTrip.Reason != detail.Reason {
			t.Fatalf("typed error round-trip failed: %#v %v", detail, err)
		}
	}
	// Unicode method text still counts in UTF-8 bytes against the frame cap.
	unicodeMethod := strings.Repeat("é", 4)
	if err := piwire.ValidateHostV2Method(unicodeMethod); err != nil {
		t.Fatalf("unicode method rejected: %v", err)
	}
}

func TestHostV2EventHasNoID(t *testing.T) {
	_, err := piwire.ParseHostV2EventFrame([]byte(`{"id":1,"method":"session.event","params":{"sessionId":"s"}}`))
	if err == nil {
		t.Fatal("event carrying an id was admitted")
	}
	event, err := piwire.ParseHostV2EventFrame([]byte(`{"method":"session.event","params":{"sessionId":"s"}}`))
	if err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
	if event.Method != "session.event" {
		t.Fatalf("event method mismatch: %#v", event)
	}
}
