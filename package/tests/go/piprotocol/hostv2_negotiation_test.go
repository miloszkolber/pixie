package piprotocol_test

import (
	"testing"

	piwire "github.com/miloszkolber/pixie/contracts/piprotocol"
)

func TestParseHostProtocolMode(t *testing.T) {
	cases := []struct {
		raw  string
		want piwire.HostProtocolMode
	}{
		{"", piwire.HostProtocolV1},
		{"v1", piwire.HostProtocolV1},
		{"V1", piwire.HostProtocolV1},
		{" v1 ", piwire.HostProtocolV1},
		{"auto", piwire.HostProtocolAuto},
		{"AUTO", piwire.HostProtocolAuto},
		{"v2", piwire.HostProtocolV2},
		{"V2", piwire.HostProtocolV2},
	}
	for _, tc := range cases {
		got, err := piwire.ParseHostProtocolMode(tc.raw)
		if err != nil || got != tc.want {
			t.Fatalf("ParseHostProtocolMode(%q) = %v,%v want %v", tc.raw, got, err, tc.want)
		}
	}
	for _, raw := range []string{"v3", "1", "legacy", "v1,v2"} {
		if _, err := piwire.ParseHostProtocolMode(raw); err == nil {
			t.Fatalf("invalid protocol mode %q was accepted", raw)
		}
	}
}

func TestHostV2SelectVersionPicksHighestMutual(t *testing.T) {
	host := piwire.HostV2SupportedVersions()
	selected, err := piwire.HostV2SelectVersion([]int{2, 1}, host)
	if err != nil || selected != 2 {
		t.Fatalf("mutual [2,1] selection = %d,%v want 2", selected, err)
	}
	selected, err = piwire.HostV2SelectVersion([]int{1}, host)
	if err != nil || selected != 1 {
		t.Fatalf("v1-only selection = %d,%v want 1", selected, err)
	}
	// A peer offering a newer version still negotiates the highest mutual one
	// instead of failing or claiming the newer version.
	selected, err = piwire.HostV2SelectVersion([]int{3, 2}, host)
	if err != nil || selected != 2 {
		t.Fatalf("newer peer selection = %d,%v want 2", selected, err)
	}
	if _, err := piwire.HostV2SelectVersion([]int{3}, host); err == nil {
		t.Fatal("empty intersection was admitted")
	}
}

func TestHostV2ValidateNegotiatedRejectsMixedPeers(t *testing.T) {
	offered := piwire.HostV2SupportedVersions()
	// A host that claims [2,1] support but silently answers v1 is rejected.
	if err := piwire.HostV2ValidateNegotiated(offered, offered, 1); err == nil {
		t.Fatal("silent downgrade to v1 was admitted")
	}
	// A selection outside the offered intersection is rejected.
	if err := piwire.HostV2ValidateNegotiated(offered, []int{3}, 3); err == nil {
		t.Fatal("out-of-intersection selection was admitted")
	}
	// A selection the host does not advertise is rejected.
	if err := piwire.HostV2ValidateNegotiated(offered, []int{1}, 2); err == nil {
		t.Fatal("selection outside the advertised set was admitted")
	}
	// A legacy v1 host (no advertisement) is allowed for auto; strict v2 is
	// enforced separately by the caller.
	if err := piwire.HostV2ValidateNegotiated(offered, nil, 1); err != nil {
		t.Fatalf("legacy v1 host rejected by negotiation: %v", err)
	}
	// A normal v2 host passes.
	if err := piwire.HostV2ValidateNegotiated(offered, offered, 2); err != nil {
		t.Fatalf("matching v2 host rejected: %v", err)
	}
}
