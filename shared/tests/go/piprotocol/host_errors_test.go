package piprotocol_test

import (
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/shared/piprotocol"
)

// AUX-32: the browser-visible host error message is derived from the typed code
// only. It is bounded, stable and free of any native text, so the controller can
// never reflect a path, URL or credential from a host failure.
func TestBrowserErrorMessageForHostCodeIsStableAndBounded(t *testing.T) {
	cases := map[int]string{
		int(piprotocol.HostV2CodeUnknownSession):        "The session is unknown. Reload the workspace and retry.",
		int(piprotocol.HostV2CodeResourceConflict):      "The session changed; reload and retry.",
		int(piprotocol.HostV2CodeCapabilityUnavailable): "The operation is not available for the connected host.",
		int(piprotocol.HostV2CodeDeliveryUncertain):     "The operation may not have completed; reload before retrying.",
		int(piprotocol.HostV2CodePersistenceUncertain):  "The change may not have been saved; reload to confirm.",
		int(piprotocol.HostV2CodeMethodNotFound):        "The operation is not supported by the connected host.",
		int(piprotocol.HostV2CodeInternal):              "The host operation failed.",
	}
	for code, want := range cases {
		got := piprotocol.BrowserErrorMessageForHostCode(code)
		if got != want {
			t.Fatalf("code %d message = %q, want %q", code, got, want)
		}
		if len(got) == 0 || len(got) > 512 {
			t.Fatalf("code %d message is not bounded: %q", code, got)
		}
	}
	// An uncatalogued code never invents support or leaks native text.
	unknown := piprotocol.BrowserErrorMessageForHostCode(-99999)
	if unknown != "The host operation failed." || strings.ContainsAny(unknown, "/\\") {
		t.Fatalf("unknown code message = %q", unknown)
	}
}
