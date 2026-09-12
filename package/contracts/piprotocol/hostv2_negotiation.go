// Host protocol version negotiation for the additive `runtime.hello`
// handshake described by roadmap D2.
//
// The rollout is v1 (byte-identical legacy) -> auto (negotiate, accept v1) ->
// v2 (require protocol version 2). The offer and response fields are additive
// to the v1 hello, so an unmodified v1 peer keeps working.
//
//   - v1 sends exactly {"protocolVersion":1} and the host answers with the
//     legacy result shape.
//   - auto and v2 send protocolVersion:1, supportedProtocolVersions:[2,1] and
//     preferProtocolVersion:2.
//   - a negotiation-aware host advertises supportedProtocolVersions:[2,1] and
//     selects the highest mutually supported version. A peer whose selection
//     falls outside the offered intersection, or whose advertised set is
//     inconsistent with its selection, is closed rather than downgraded.
package piprotocol

import (
	"fmt"
	"sort"
	"strings"
)

// HostProtocolEnvVar is the environment variable selecting the host protocol
// mode. It is shared by the assistant host and the controller so one process
// cannot negotiate differently on each side.
const HostProtocolEnvVar = "PIXIE_PI_PROTOCOL"

// HostProtocolMode selects the protocol negotiation behavior.
type HostProtocolMode int

const (
	// HostProtocolV1 is the default: exact legacy behavior, no negotiation
	// fields on the wire.
	HostProtocolV1 HostProtocolMode = iota
	// HostProtocolAuto offers/accepts [2,1] and permits a v1 peer.
	HostProtocolAuto
	// HostProtocolV2 offers/accepts [2,1] but requires the selected version
	// to be 2.
	HostProtocolV2
)

// String renders the canonical lowercase mode name.
func (m HostProtocolMode) String() string {
	switch m {
	case HostProtocolV1:
		return "v1"
	case HostProtocolAuto:
		return "auto"
	case HostProtocolV2:
		return "v2"
	default:
		return "unknown"
	}
}

// Negotiates reports whether the mode adds the additive hello fields.
func (m HostProtocolMode) Negotiates() bool { return m != HostProtocolV1 }

// RequiresV2 reports whether the mode refuses a v1 selection.
func (m HostProtocolMode) RequiresV2() bool { return m == HostProtocolV2 }

// ParseHostProtocolMode parses PIXIE_PI_PROTOCOL. Case-insensitive; an empty
// value is the v1 default. Any other value is rejected so an invalid selection
// fails startup instead of silently selecting a version.
func ParseHostProtocolMode(raw string) (HostProtocolMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "v1":
		return HostProtocolV1, nil
	case "auto":
		return HostProtocolAuto, nil
	case "v2":
		return HostProtocolV2, nil
	default:
		return HostProtocolV1, fmt.Errorf("%s must be one of v1, auto or v2, got %q", HostProtocolEnvVar, raw)
	}
}

// HostHelloOffer is the additive hello request. The v1 peer omits both
// negotiation fields, which makes the serialized params byte-identical to the
// legacy {"protocolVersion":1}.
type HostHelloOffer struct {
	ProtocolVersion           int   `json:"protocolVersion"`
	SupportedProtocolVersions []int `json:"supportedProtocolVersions,omitempty"`
	PreferProtocolVersion     int   `json:"preferProtocolVersion,omitempty"`
}

// HostV2SupportedVersions returns the negotiation-aware wire advertisement.
// The order is highest-first; callers must not rely on slice order for
// selection.
func HostV2SupportedVersions() []int { return []int{HostV2ProtocolVersion, 1} }

// HostV2IncompatibilityError reports a peer that cannot agree on a protocol
// version or that advertised an inconsistent set. It is never a downgrade.
type HostV2IncompatibilityError struct {
	Reason string
}

func (e *HostV2IncompatibilityError) Error() string {
	if e.Reason == "" {
		return "host protocol versions are incompatible"
	}
	return "host protocol versions are incompatible: " + e.Reason
}

// HostV2VersionPresent reports whether version appears in versions.
func HostV2VersionPresent(versions []int, version int) bool {
	for _, candidate := range versions {
		if candidate == version {
			return true
		}
	}
	return false
}

// HostV2SelectVersion returns the highest version present in both sets.
// An empty intersection is an incompatibility error: the caller closes the
// connection instead of silently selecting a version the peer did not offer.
func HostV2SelectVersion(peerSupported, hostSupported []int) (int, error) {
	best := 0
	for _, version := range peerSupported {
		if version <= 0 || !HostV2VersionPresent(hostSupported, version) {
			continue
		}
		if version > best {
			best = version
		}
	}
	if best == 0 {
		return 0, &HostV2IncompatibilityError{Reason: summarizeVersions(peerSupported, hostSupported)}
	}
	return best, nil
}

// HostV2ValidateNegotiated checks a peer's hello response against the offer
// and the peer's own advertisement. advertised may be empty for a legacy v1
// host; then only the offered intersection and the selected version are
// checked. A non-empty advertised set must contain the selection and must
// resolve to that selection as the highest mutual version, which rejects a
// host that claims v2 support while silently answering v1.
func HostV2ValidateNegotiated(offered, advertised []int, selected int) error {
	if !HostV2VersionPresent(offered, selected) {
		return &HostV2IncompatibilityError{Reason: fmt.Sprintf("peer selected unsupported version %d", selected)}
	}
	if len(advertised) == 0 {
		return nil
	}
	if !HostV2VersionPresent(advertised, selected) {
		return &HostV2IncompatibilityError{Reason: fmt.Sprintf("peer advertised %v but selected %d", advertised, selected)}
	}
	expected, err := HostV2SelectVersion(offered, advertised)
	if err != nil {
		return err
	}
	if expected != selected {
		return &HostV2IncompatibilityError{Reason: fmt.Sprintf("peer selected %d instead of the highest mutual version %d", selected, expected)}
	}
	return nil
}

func summarizeVersions(peerSupported, hostSupported []int) string {
	format := func(versions []int) string {
		copied := append([]int(nil), versions...)
		sort.Ints(copied)
		parts := make([]string, 0, len(copied))
		for _, version := range copied {
			parts = append(parts, fmt.Sprintf("%d", version))
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	return fmt.Sprintf("peer %s has no overlap with host %s", format(peerSupported), format(hostSupported))
}
