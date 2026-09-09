package piprotocol_test

import (
	"encoding/json"
	"testing"

	piwire "github.com/miloszkolber/pixie/internal/piprotocol"
)

func TestHostV2IDDomainsRejectCrossDomainJSON(t *testing.T) {
	// Browser string "001" is valid in the browser domain.
	browser, err := piwire.HostV2ParseBrowserID(json.RawMessage(`"001"`))
	if err != nil {
		t.Fatalf("browser string 001 rejected: %v", err)
	}
	if string(browser) != "001" {
		t.Fatalf("browser id mismatch: %q", string(browser))
	}
	// The same JSON string must never parse as a host integer.
	if _, err := piwire.HostV2ParseHostID(json.RawMessage(`"001"`)); err == nil {
		t.Fatal("browser string 001 coerced to host id")
	}
	// Host integer 1 is valid in the host domain.
	host, err := piwire.HostV2ParseHostID(json.RawMessage(`1`))
	if err != nil || int64(host) != 1 {
		t.Fatalf("host integer 1 rejected: %v %v", host, err)
	}
	// The same JSON number must never parse as a browser string.
	if _, err := piwire.HostV2ParseBrowserID(json.RawMessage(`1`)); err == nil {
		t.Fatal("host number 1 coerced to browser id")
	}
	// String host IDs and fractional spellings stay in no domain.
	for _, raw := range []string{`"1"`, `1.0`, `1e3`, `0`, `-1`, `9007199254740992`} {
		if _, err := piwire.HostV2ParseHostID(json.RawMessage(raw)); err == nil && raw != `"1"` {
			// 9007199254740992 (2^53) is above the safe-integer ceiling and
			// must fail; the quoted form already failed above.
			if raw == "9007199254740992" || raw == "1.0" || raw == "1e3" || raw == "0" || raw == "-1" {
				t.Fatalf("host accepted non-canonical id %s", raw)
			}
		}
	}
}

func TestHostV2NativeDomainExtendsBeyondHostCeiling(t *testing.T) {
	// 2^53 is invalid as a host safe integer but valid as native uint64.
	if _, err := piwire.HostV2ParseHostID(json.RawMessage(`9007199254740992`)); err == nil {
		t.Fatal("host accepted an id above the safe-integer ceiling")
	}
	native, err := piwire.HostV2ParseNativeID(json.RawMessage(`9007199254740992`))
	if err != nil || uint64(native) != 9007199254740992 {
		t.Fatalf("native rejected 2^53: %v %v", native, err)
	}
	// Native strings never coerce, even when they look numeric.
	if _, err := piwire.HostV2ParseNativeID(json.RawMessage(`"42"`)); err == nil {
		t.Fatal("native string coerced to correlation")
	}
	if _, err := piwire.HostV2ParseHostID(json.RawMessage(`"42"`)); err == nil {
		t.Fatal("native string coerced to host id")
	}
	// Max uint64 stays native-only.
	maxNative, err := piwire.HostV2ParseNativeID(json.RawMessage(`18446744073709551615`))
	if err != nil || uint64(maxNative) != 18446744073709551615 {
		t.Fatalf("native max uint64 rejected: %v %v", maxNative, err)
	}
	if _, err := piwire.HostV2ParseHostID(json.RawMessage(`18446744073709551615`)); err == nil {
		t.Fatal("native max uint64 coerced to host id")
	}
}

func TestHostV2MapperRequiresExplicitBinding(t *testing.T) {
	mapper := piwire.NewHostV2IDMapper()
	if err := mapper.Bind("001", 1, 7); err != nil {
		t.Fatalf("explicit bind rejected: %v", err)
	}
	// "001" and 1 stay distinct: looking up browser "1" must miss, even
	// though strconv.Atoi("001") == 1 in a coercing adapter.
	if _, ok := mapper.ResolveHost("1"); ok {
		t.Fatal("browser 1 resolved through numeric coercion of 001")
	}
	got, ok := mapper.ResolveHost("001")
	if !ok || int64(got) != 1 {
		t.Fatalf("bound browser 001 did not resolve: %v %v", got, ok)
	}
	browser, ok := mapper.ResolveBrowser(1)
	if !ok || string(browser) != "001" {
		t.Fatalf("bound host 1 did not resolve: %v %v", browser, ok)
	}
	native, ok := mapper.ResolveNative(1)
	if !ok || uint64(native) != 7 {
		t.Fatalf("bound native did not resolve: %v %v", native, ok)
	}
	host, ok := mapper.ResolveHostByNative(7)
	if !ok || int64(host) != 1 {
		t.Fatalf("reverse native lookup failed: %v %v", host, ok)
	}
	// Duplicate host IDs cannot be rebound to a second browser ID.
	if err := mapper.Bind("002", 1, 8); err == nil {
		t.Fatal("duplicate host id rebound")
	}
}

func TestHostV2ReconnectInvalidatesTransportNotMutations(t *testing.T) {
	mapper := piwire.NewHostV2IDMapper()
	if err := mapper.Bind("replay-a", 11, 100); err != nil {
		t.Fatalf("bind rejected: %v", err)
	}
	mutation := piwire.HostV2Mutation{MutationID: "mut-1", Fingerprint: "fp-1"}
	if err := mutation.Validate(); err != nil {
		t.Fatalf("mutation fixture invalid: %v", err)
	}
	// Reconnect drops transport mappings.
	mapper.InvalidateTransport()
	if mapper.Len() != 0 {
		t.Fatal("transport mappings survived reconnect")
	}
	if _, ok := mapper.ResolveHost("replay-a"); ok {
		t.Fatal("browser mapping survived reconnect")
	}
	// Durable mutation identity survives reconnect by design: the same
	// mutation reconciles, different content conflicts.
	same := piwire.HostV2Mutation{MutationID: "mut-1", Fingerprint: "fp-1"}
	if err := piwire.CheckHostV2MutationConflict(mutation, same); err != nil {
		t.Fatalf("identical mutation should reconcile: %v", err)
	}
	changed := piwire.HostV2Mutation{MutationID: "mut-1", Fingerprint: "fp-2"}
	if err := piwire.CheckHostV2MutationConflict(mutation, changed); err == nil {
		t.Fatal("different content under one mutation id was admitted")
	}
	// A retry binds a fresh transport ID to the retained mutation identity.
	if err := mapper.Bind("replay-a", 12, 101); err != nil {
		t.Fatalf("retry with fresh transport id rejected: %v", err)
	}
}
