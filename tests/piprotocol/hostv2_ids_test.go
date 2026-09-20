package piprotocol_test

import (
	"encoding/json"
	"testing"

	piwire "github.com/miloszkolber/pixie/piprotocol"
)

func TestHostV2IDDomainsRejectCrossDomainJSON(t *testing.T) {
	browser, err := piwire.HostV2ParseBrowserID(json.RawMessage(`"001"`))
	if err != nil || string(browser) != "001" {
		t.Fatalf("browser string 001 = %q, %v", browser, err)
	}
	host, err := piwire.HostV2ParseHostID(json.RawMessage(`1`))
	if err != nil || host != 1 {
		t.Fatalf("host integer 1 = %v, %v", host, err)
	}
	for _, raw := range []string{`"001"`, `"1"`, `1.0`, `1e3`, `0`, `-1`, `9007199254740992`, `18446744073709551615`, `null`} {
		if _, err := piwire.HostV2ParseHostID(json.RawMessage(raw)); err == nil {
			t.Fatalf("host accepted non-canonical id %s", raw)
		}
	}
	for _, raw := range []string{`1`, `null`, `""`} {
		if _, err := piwire.HostV2ParseBrowserID(json.RawMessage(raw)); err == nil {
			t.Fatalf("browser accepted invalid id %s", raw)
		}
	}
}
