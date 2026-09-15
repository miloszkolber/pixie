package diagnostics

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// BootIdentityEnvironment and RunIdentityEnvironment carry the supervisor's
	// boot and per-run identities to managed children. They hold opaque
	// identifiers only: never a secret, path, token or endpoint.
	BootIdentityEnvironment = "PIXIE_BOOT_ID"
	RunIdentityEnvironment  = "PIXIE_RUN_ID"

	maxRunIdentityValue = 64
)

var (
	// runIdentityValuePattern accepts the two formats Pixie generates:
	// "run-"/"boot-" plus 16 random bytes, and the Linux kernel boot-id UUID.
	// It deliberately rejects arbitrary opaque tokens, which could be a
	// credential pasted into the environment.
	runIdentityValuePattern  = regexp.MustCompile(`^(?:run|boot)-[0-9a-f]{32}$`)
	kernelBootIDValuePattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// RunIdentity correlates logs and support output for one supervised topology.
// BootID is stable across restarts on the same host boot (or archive session);
// RunID is unique per launcher invocation. Neither value is derived from a
// path, token or endpoint.
type RunIdentity struct {
	BootID    string `json:"bootId,omitempty"`
	RunID     string `json:"runId,omitempty"`
	StartedAt string `json:"startedAt,omitempty"`
}

// IsZero reports whether no correlatable identity value is present.
func (identity RunIdentity) IsZero() bool {
	return identity.BootID == "" && identity.RunID == "" && identity.StartedAt == ""
}

// SanitizeRunIdentity keeps only well-formed identifiers and an RFC3339 start
// time. It returns false when neither identifier survives, so callers omit the
// group instead of exporting an empty or attacker-shaped value.
func SanitizeRunIdentity(identity RunIdentity) (RunIdentity, bool) {
	result := RunIdentity{}
	if value, ok := sanitizeRunIdentityValue(identity.BootID); ok {
		result.BootID = value
	}
	if value, ok := sanitizeRunIdentityValue(identity.RunID); ok {
		result.RunID = value
	}
	if result.BootID == "" && result.RunID == "" {
		return RunIdentity{}, false
	}
	if started, err := time.Parse(time.RFC3339, identity.StartedAt); err == nil {
		result.StartedAt = started.UTC().Format(time.RFC3339)
	}
	return result, true
}

func sanitizeRunIdentityValue(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) > maxRunIdentityValue {
		return "", false
	}
	if !runIdentityValuePattern.MatchString(value) && !kernelBootIDValuePattern.MatchString(value) {
		return "", false
	}
	return value, true
}

// NewRunIdentity builds a run identity. bootID is used when it is already a
// valid identifier (for example the kernel boot id); otherwise a random value
// is generated. entropy may be nil to use crypto/rand, which keeps the
// generator deterministic for tests.
func NewRunIdentity(now time.Time, bootID string, entropy io.Reader) RunIdentity {
	if entropy == nil {
		entropy = rand.Reader
	}
	identity := RunIdentity{}
	if value, ok := sanitizeRunIdentityValue(bootID); ok {
		identity.BootID = value
	}
	if identity.BootID == "" {
		if value := randomRunIdentityValue(entropy, "boot-"); value != "" {
			identity.BootID = value
		}
	}
	if value := randomRunIdentityValue(entropy, "run-"); value != "" {
		identity.RunID = value
	}
	if identity.BootID == "" && identity.RunID == "" {
		// The entropy source failed; report no identity rather than a partial one.
		return RunIdentity{}
	}
	identity.StartedAt = now.UTC().Format(time.RFC3339)
	return identity
}

func randomRunIdentityValue(entropy io.Reader, prefix string) string {
	buffer := make([]byte, 16)
	if _, err := io.ReadFull(entropy, buffer); err != nil {
		return ""
	}
	return prefix + hex.EncodeToString(buffer)
}

// KernelBootID reads the Linux kernel boot id when it is available and
// well-formed. An unavailable or malformed value returns the empty string so
// the caller generates a random boot identity instead.
func KernelBootID() string {
	content, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	value, ok := sanitizeRunIdentityValue(string(content))
	if !ok {
		return ""
	}
	return value
}

// RunIdentityFromEnvironment reads the identities a supervisor exported. It
// never reads any other environment variable.
func RunIdentityFromEnvironment(lookup func(string) (string, bool)) RunIdentity {
	if lookup == nil {
		return RunIdentity{}
	}
	identity := RunIdentity{}
	if value, ok := lookup(BootIdentityEnvironment); ok {
		identity.BootID = value
	}
	if value, ok := lookup(RunIdentityEnvironment); ok {
		identity.RunID = value
	}
	sanitized, _ := SanitizeRunIdentity(identity)
	return sanitized
}

// ResolveProcessRunIdentity prefers the supervisor's exported identity and
// falls back to generating one for a directly launched process.
func ResolveProcessRunIdentity(lookup func(string) (string, bool), now time.Time) RunIdentity {
	if identity := RunIdentityFromEnvironment(lookup); !identity.IsZero() {
		return identity
	}
	return NewRunIdentity(now, KernelBootID(), nil)
}

// Environment returns the explicitly set identity values for a child process.
func (identity RunIdentity) Environment() map[string]string {
	sanitized, ok := SanitizeRunIdentity(identity)
	if !ok {
		return map[string]string{}
	}
	environment := make(map[string]string, 2)
	if sanitized.BootID != "" {
		environment[BootIdentityEnvironment] = sanitized.BootID
	}
	if sanitized.RunID != "" {
		environment[RunIdentityEnvironment] = sanitized.RunID
	}
	return environment
}

// processRunIdentity is set once by a product entrypoint so support output and
// logs carry the same identity even when the environment is not exported.
var processRunIdentity atomic.Pointer[RunIdentity]

// SetProcessRunIdentity records the identity for this process. The stored value
// is sanitized; an invalid identity clears any previous value.
func SetProcessRunIdentity(identity RunIdentity) {
	sanitized, ok := SanitizeRunIdentity(identity)
	if !ok {
		processRunIdentity.Store(nil)
		return
	}
	processRunIdentity.Store(&sanitized)
}

// ProcessRunIdentity returns the sanitized process identity, if one is set.
func ProcessRunIdentity() RunIdentity {
	if stored := processRunIdentity.Load(); stored != nil {
		return *stored
	}
	return RunIdentity{}
}

// RunIdentityLoggerAttributes returns stable slog attributes for a valid
// identity. It never emits an attribute for an absent or invalid value.
func RunIdentityLoggerAttributes(identity RunIdentity) []any {
	sanitized, ok := SanitizeRunIdentity(identity)
	if !ok {
		return nil
	}
	attributes := make([]any, 0, 2)
	if sanitized.BootID != "" {
		attributes = append(attributes, "bootId", sanitized.BootID)
	}
	if sanitized.RunID != "" {
		attributes = append(attributes, "runId", sanitized.RunID)
	}
	return attributes
}
