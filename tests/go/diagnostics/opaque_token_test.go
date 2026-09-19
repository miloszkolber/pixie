package diagnostics_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/diagnostics"
)

// A configured secret has no recognizable prefix, so only the configured-secret
// registry can remove it. Every diagnostic text surface that shares the
// sanitizer must redact it.
func TestSanitizeDiagnosticTextRedactsConfiguredSecrets(t *testing.T) {
	secret := strings.Repeat("s", 40)
	t.Cleanup(func() { diagnostics.ConfigureSanitizerSecrets() })
	diagnostics.ConfigureSanitizerSecrets(secret)

	inputs := []string{
		"dial failed with " + secret,
		"PIXIE_PI_SECRET_KEY=" + secret,
		"prefix" + secret + "suffix",
		"header Bearer " + secret,
	}
	for _, input := range inputs {
		got := diagnostics.SanitizeDiagnosticText(input, 4096)
		if strings.Contains(got, secret) {
			t.Fatalf("configured secret survived sanitization: %q", got)
		}
		if !strings.Contains(got, "redacted") {
			t.Fatalf("configured secret was dropped without a placeholder: %q", got)
		}
	}
}

// An unlabeled opaque credential or digest has no known prefix and no label, so
// the bounded opaque-token rule is the only thing that can catch it.
func TestSanitizeDiagnosticTextRedactsBareOpaqueTokens(t *testing.T) {
	digest := strings.Repeat("a", 64)
	credential := "opq_" + strings.Repeat("B", 40)
	cases := []struct {
		name  string
		input string
		token string
	}{
		{"digest", "artifact digest " + digest + " recorded", digest},
		{"credential", "supplied " + credential + " to the host", credential},
	}
	for _, testCase := range cases {
		got := diagnostics.SanitizeDiagnosticText(testCase.input, 4096)
		if strings.Contains(got, testCase.token) {
			t.Fatalf("%s opaque token survived: %q", testCase.name, got)
		}
		if !strings.Contains(got, "redacted") {
			t.Fatalf("%s opaque token was dropped without a placeholder: %q", testCase.name, got)
		}
	}
}

// Recovery needs the identities and ordinary labels that name a run or a
// session. The opaque rule must not erase a well-formed run/boot identity or a
// token that carries an explicit required-identifier label, and ordinary short
// project/session identifiers must pass through unchanged.
func TestSanitizeDiagnosticTextKeepsRequiredIdentifiers(t *testing.T) {
	runID := "run-" + strings.Repeat("a", 32)
	kernelBootID := "01234567-89ab-cdef-0123-456789abcdef"
	labelledRuntimeID := strings.Repeat("c", 40)
	input := "runId=" + runID +
		" bootId=" + kernelBootID +
		" runtimeId=" + labelledRuntimeID +
		" session-42 project-alpha"
	got := diagnostics.SanitizeDiagnosticText(input, 4096)
	for _, keep := range []string{runID, kernelBootID, labelledRuntimeID, "session-42", "project-alpha"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("required identifier %q was erased: %q", keep, got)
		}
	}
}

// The child-stderr ring is one of the surfaces that must never retain a
// configured secret.
func TestStderrRingRedactsConfiguredSecret(t *testing.T) {
	secret := strings.Repeat("k", 40)
	t.Cleanup(func() { diagnostics.ConfigureSanitizerSecrets() })
	diagnostics.ConfigureSanitizerSecrets(secret)

	ring := diagnostics.NewStderrRing()
	if _, err := fmt.Fprintf(ring, "child booted with %s\n", secret); err != nil {
		t.Fatal(err)
	}
	summary := ring.Snapshot()
	if summary.Retained != 1 {
		t.Fatalf("retained = %d, want 1: %#v", summary.Retained, summary)
	}
	if strings.Contains(summary.Entries[0].Text, secret) {
		t.Fatalf("stderr ring retained the configured secret: %q", summary.Entries[0].Text)
	}
}

// A very short configured value would over-redact ordinary text without adding
// protection, so it is not applied.
func TestConfigureSanitizerSecretsIgnoresShortValues(t *testing.T) {
	short := "abc"
	t.Cleanup(func() { diagnostics.ConfigureSanitizerSecrets() })
	diagnostics.ConfigureSanitizerSecrets(short)

	if got := diagnostics.SanitizeDiagnosticText("value "+short, 4096); got != "value "+short {
		t.Fatalf("short configured secret was applied: %q", got)
	}
}
