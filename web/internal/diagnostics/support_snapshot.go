package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	piwire "github.com/miloszkolber/pixie/shared/piprotocol"
)

const (
	// SupportSnapshotSchemaVersion versions the deliberately small support
	// export independently from the browser transport.
	SupportSnapshotSchemaVersion = 1
	// SupportSnapshotMaxBytes applies to the encoded snapshot before it crosses
	// the browser transport. The event count and every text field are bounded as
	// well, so this is a final invariant rather than the primary limiter.
	SupportSnapshotMaxBytes  = 64 * 1024
	SupportSnapshotMaxEvents = 64

	maxSupportDetailBytes = 256
	maxSupportCount       = 1_000_000

	// Configured secrets come from the process environment, so they are
	// normalized before use: a value shorter than minSanitizerSecretBytes is not
	// distinctive enough to redact without over-matching ordinary text, the
	// count and per-value length are capped so sanitization work stays bounded,
	// and duplicates collapse.
	minSanitizerSecretBytes = 8
	maxSanitizerSecretCount = 16
	maxSanitizerSecretBytes = 256

	// diagnosticInputFactor and diagnosticInputFloorBytes bound the raw value
	// the sanitizer feeds to its regexes. RE2 has no catastrophic backtracking,
	// but a bounded repetition such as the glued-path pattern still costs a
	// per-state step, so an unbounded input stalls the caller (a 32 MiB host
	// error could block for minutes). The factor keeps redaction context while
	// making the work proportional to the requested output limit instead of the
	// attacker-controlled input length.
	diagnosticInputFactor     = 4
	diagnosticInputFloorBytes = 4096
	// diagnosticTruncationMarker is appended when the raw value is cut at the
	// input bound so a removed tail is explicit rather than silently dropped.
	diagnosticTruncationMarker = " [truncated]"

	supportRequestFailedCode                 = "request.failed"
	supportRequestCanceledCode               = "request.canceled"
	supportRequestTimedOutCode               = "request.timed_out"
	supportRequestAuthenticationRequiredCode = "request.authentication_required"
	supportRequestStaleStateCode             = "request.stale_state"
	supportRequestCapabilityUnavailableCode  = "request.capability_unavailable"
)

var (
	// A support snapshot has no need for endpoint identity. Redact every URL,
	// including one without credentials or a query string, rather than trying to
	// preserve a hostname safely.
	supportURLPattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]{1,31}://[^\s<>"']+`)
	// Credentials commonly surface in URL-like errors, headers and request
	// configuration. These patterns intentionally prefer false positives to
	// retaining a credential in an operator-exported file.
	supportBearerPattern              = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`)
	supportSensitiveAssignmentPattern = regexp.MustCompile(`(?i)\b([a-z][a-z0-9_.-]*(?:token|secret|password|api[_-]?key|key)[a-z0-9_.-]*)\s*=\s*(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
	supportSensitiveLabelPattern      = regexp.MustCompile(`(?i)\b(?:token|api[_ -]?key|access[_ -]?token|bearer[_ -]?token|secret|password)\s*(?::|=)\s*(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
	// A label separated by whitespace ("Authorization: token abc") is as common
	// as an assignment. Prefer a false positive over retaining the value.
	supportSensitivePrefixPattern   = regexp.MustCompile(`(?i)\b(?:token|api[_ -]?key|access[_ -]?token|bearer[_ -]?token|secret|password)\s+[A-Za-z0-9._~+/=-]{6,}`)
	supportAPIKeyPattern            = regexp.MustCompile(`(?i)\b(?:(?:sk|pk|rk|api|gh[pousr]|xox[baprs])[_-][A-Za-z0-9_-]{12,}|AKIA[A-Z0-9]{16})\b`)
	supportIdentifierPattern        = regexp.MustCompile(`(?i)\b(?:project|session|chat)(?:[_ -]?id)?\s*[=:]\s*(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
	supportIdentifierMentionPattern = regexp.MustCompile(`(?i)\b(?:project|session|chat)(?:[_ -]?id)?\s+(?:"[^"]*"|'[^']*'|[A-Za-z0-9_-]{3,})`)
	// URL values are removed before this expression runs. What remains is a
	// filesystem location in a normal error message or assignment. The path is
	// matched after any delimiter that is not a word, dot or slash so bracketed
	// and quoted locations are redacted too.
	supportUnixPathPattern = regexp.MustCompile(`(^|[^\w./])/(?:[^\s'"<>,;:)\]}]+)`)
	// A long opaque run (a credential, digest or identifier) can be glued
	// directly to an absolute path with no delimiter, for example a token
	// followed immediately by /home/operator/.pi/agent/secret.json. The
	// delimiter-based pattern above cannot see that leading slash because the
	// preceding byte is a word character. The run itself is sensitive, so it is
	// consumed along with the path. The run is limited to token characters and
	// a bounded width so ordinary prose and relative paths that merely contain
	// a slash are unaffected; a run wider than the bound is still redacted
	// together with its absolute path, though any prefix beyond the bound is
	// left in place. The path portion reuses the same terminal class as the
	// delimiter-based pattern.
	supportGluedUnixPathPattern = regexp.MustCompile(`[A-Za-z0-9._~+=-]{40,256}/[^\s'"<>,;:)\]}]+`)
	supportWindowsPathPattern   = regexp.MustCompile(`(?i)\b[A-Z]:\\(?:[^\s'"<>,;:)\]}]+)`)
	supportUNCPattern           = regexp.MustCompile(`\\\\[^\s'"<>,;:)\]}]+`)
	// An unlabeled opaque credential, digest or token has no known prefix and no
	// label, so this bounded run is the only rule that removes it. It is applied
	// last, after URLs, paths, labels and known identifier labels, so it cannot
	// split a path or double-redact a labelled value. The match is deliberately
	// restricted to token characters so ordinary prose is unaffected.
	supportOpaqueTokenPattern = regexp.MustCompile(`\b[A-Za-z0-9][A-Za-z0-9_-]{31,}\b`)
	// A run/boot identity and the kernel boot-id UUID are required correlation
	// identifiers. They are longer than the opaque floor, so they are excluded
	// explicitly rather than by luck.
	supportRequiredIdentityPattern = regexp.MustCompile(`^(?:(?:run|boot)-[0-9a-f]{32}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)
	// A token directly preceded by one of these labels is a required recovery
	// identifier, not an opaque credential, so the opaque rule leaves it intact.
	supportRequiredIdentifierLabels = []string{
		"runid", "run_id", "run-id",
		"bootid", "boot_id", "boot-id",
		"runtimeid", "runtime_id", "runtime-id",
		"projectid", "project_id", "project-id",
		"sessionid", "session_id", "session-id",
		"chatid", "chat_id", "chat-id",
	}
	// Support snapshots retain build identity only when it is a known release
	// format. Arbitrary linker values could otherwise carry credentials or IDs.
	supportBuildVersionPattern  = regexp.MustCompile(`^(?:0\.0\.0-dev|sha-[0-9a-f]{12}|v?[0-9]+\.[0-9]+\.[0-9]+)$`)
	supportBuildRevisionPattern = regexp.MustCompile(`^(?:unknown|[0-9a-f]{40})$`)
	supportOperationAllowlist   = makeSupportOperationAllowlist()

	// configuredSanitizerSecrets holds the process credentials that every
	// diagnostic text surface must redact. It is set once by the entrypoint and
	// read lock-free on each sanitize call.
	configuredSanitizerSecrets atomic.Pointer[[]string]
)

// SupportSnapshot is the complete JSON support-export boundary. It contains
// only the fields below: the process run identity, bounded health transitions,
// controller runtime facts and structured controller request outcomes. It never
// gathers logs, configs, transcripts, paths, project/session identities,
// endpoint addresses, credentials, or raw errors.
type SupportSnapshot struct {
	SchemaVersion     int                    `json:"schemaVersion"`
	GeneratedAt       string                 `json:"generatedAt"`
	Identity          *RunIdentity           `json:"identity,omitempty"`
	HealthTransitions []HealthTransition     `json:"healthTransitions,omitempty"`
	Runtime           SupportSnapshotRuntime `json:"runtime"`
	Events            []ControllerEvent      `json:"events"`
}

// SupportSnapshotRuntime is the explicit allowlist of diagnostic facts. Nil
// pointers mean unknown; no missing value is turned into a healthy zero.
type SupportSnapshotRuntime struct {
	Build                 BuildInfo               `json:"build"`
	Host                  SupportSnapshotHost     `json:"host"`
	ActiveRunCount        *int                    `json:"activeRunCount,omitempty"`
	RetainedDeletionCount *int                    `json:"retainedDeletionCount,omitempty"`
	Schedule              SupportSnapshotSchedule `json:"schedule"`
	// SessionSchema is the bounded AUX-12 degradation summary. Nil means the
	// controller has not observed a degraded native session; a present value is
	// normalized to non-negative bounded counters before export.
	SessionSchema *SessionSchemaSummary `json:"sessionSchema,omitempty"`
	// Identity, when set, overrides the process/environment identity. A
	// directly launched controller leaves it nil and the marshaler resolves the
	// supervisor-exported identity.
	Identity *RunIdentity `json:"-"`
	// HealthTransitions is the controller-owned bounded transition history,
	// mapped from controller types by the caller. This package only sanitizes
	// and bounds it.
	HealthTransitions []HealthTransition `json:"-"`
}

type SupportSnapshotHost struct {
	Configured        *bool  `json:"configured,omitempty"`
	Reachable         *bool  `json:"reachable,omitempty"`
	ApplicationReady  *bool  `json:"applicationReady,omitempty"`
	Reason            string `json:"reason,omitempty"`
	ApplicationReason string `json:"applicationReason,omitempty"`
}

// SupportSnapshotSchedule is the allowlisted schedule state.
type SupportSnapshotSchedule struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// SessionSchemaSummary is the bounded AUX-12 native session-schema degradation
// summary. It carries only a header version and record counters; no native
// text, path, session identity or record payload crosses this boundary.
type SessionSchemaSummary struct {
	// WrittenByNewerRuntime reports a native header version above the supported
	// one. Such a session is surfaced, never guessed or rewritten.
	WrittenByNewerRuntime bool `json:"writtenByNewerRuntime,omitempty"`
	// Version is the native session header version the host parsed.
	Version int `json:"version,omitempty"`
	// UnknownRecords counts records whose type the host does not recognize.
	UnknownRecords int `json:"unknownRecords,omitempty"`
	// InvalidRecords counts lines that were not valid record objects.
	InvalidRecords int `json:"invalidRecords,omitempty"`
	// RepairedToolCalls counts dangling tool calls repaired for projection only.
	RepairedToolCalls int `json:"repairedToolCalls,omitempty"`
}

// ControllerEvent records an allowlisted operation and its outcome. Detail is
// a stable allowlisted failure code, never request data or an error string.
type ControllerEvent struct {
	At        string `json:"at"`
	Kind      string `json:"kind"`
	Operation string `json:"operation"`
	Outcome   string `json:"outcome"`
	Detail    string `json:"detail,omitempty"`
}

// ControllerEventRing keeps the most recent structured controller outcomes.
// It is safe to write from concurrent WebSocket handlers while a support
// export copies the current ring.
type ControllerEventRing struct {
	mu     sync.Mutex
	now    func() time.Time
	events []ControllerEvent
}

func NewControllerEventRing() *ControllerEventRing {
	return &ControllerEventRing{now: time.Now, events: make([]ControllerEvent, 0, SupportSnapshotMaxEvents)}
}

// RecordRequest retains only an allowlisted operation and a stable failure
// code. It reads an error only to classify it; it never stores its text.
func (r *ControllerEventRing) RecordRequest(operation string, err error) {
	if r == nil {
		return
	}
	if !isSupportOperation(operation) {
		operation = "unknown"
	}
	event := ControllerEvent{
		At:        r.now().UTC().Format(time.RFC3339),
		Kind:      "request",
		Operation: operation,
		Outcome:   "succeeded",
	}
	if err != nil {
		event.Outcome = "failed"
		event.Detail = supportRequestFailureCodeForError(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) == SupportSnapshotMaxEvents {
		copy(r.events, r.events[1:])
		r.events[len(r.events)-1] = event
		return
	}
	r.events = append(r.events, event)
}

func (r *ControllerEventRing) Snapshot() []ControllerEvent {
	if r == nil {
		return []ControllerEvent{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ControllerEvent(nil), r.events...)
}

// ConfigureSanitizerSecrets records the process credentials that must never
// appear in diagnostic text. Callers pass the resolved secret values, not
// configuration names. Values that are too short, too long or duplicated are
// dropped and the applied set is capped, so sanitization work stays bounded.
// Passing no values clears the set.
func ConfigureSanitizerSecrets(secrets ...string) {
	normalized := normalizeSanitizerSecrets(secrets)
	configuredSanitizerSecrets.Store(&normalized)
}

func normalizeSanitizerSecrets(secrets []string) []string {
	result := make([]string, 0, len(secrets))
	seen := make(map[string]struct{}, len(secrets))
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if len(secret) < minSanitizerSecretBytes || len(secret) > maxSanitizerSecretBytes {
			continue
		}
		if _, duplicate := seen[secret]; duplicate {
			continue
		}
		seen[secret] = struct{}{}
		result = append(result, secret)
		if len(result) >= maxSanitizerSecretCount {
			break
		}
	}
	return result
}

func sanitizerConfiguredSecrets() []string {
	if stored := configuredSanitizerSecrets.Load(); stored != nil {
		return *stored
	}
	return nil
}

// redactConfiguredDiagnosticSecrets replaces every configured secret with the
// fixed credential placeholder. Split/join is linear in the value length, and
// both the secret set and the value are bounded by the caller.
func redactConfiguredDiagnosticSecrets(value string, secrets []string) string {
	for _, secret := range secrets {
		if strings.Contains(value, secret) {
			value = strings.ReplaceAll(value, secret, "[redacted credential]")
		}
	}
	return value
}

// SanitizeDiagnosticDetail is used by authenticated live diagnostics. Support
// snapshots do not use it because text redaction cannot safely turn arbitrary
// handler or error text into a shareable diagnostic.
func SanitizeDiagnosticDetail(value string) string {
	return SanitizeDiagnosticText(value, maxSupportDetailBytes)
}

// SanitizeDiagnosticText applies the same credential, identifier and path
// redaction as SanitizeDiagnosticDetail with an explicit bound. It is exported
// for bounded diagnostic surfaces such as retained child stderr; the result is
// never a shareable guarantee for arbitrary text, only a best-effort redaction.
// The raw value is truncated at the top so no regex ever scans attacker-sized
// input; the requested limit still governs the returned length.
func SanitizeDiagnosticText(value string, limit int) string {
	secrets := sanitizerConfiguredSecrets()
	value = boundDiagnosticInput(value, limit, secrets)
	value = normalizeDiagnosticText(value)
	if value == "" {
		return ""
	}
	value = supportURLPattern.ReplaceAllString(value, "[redacted URL]")
	value = supportBearerPattern.ReplaceAllString(value, "Bearer [redacted]")
	value = supportSensitiveAssignmentPattern.ReplaceAllString(value, "$1=[redacted]")
	value = supportSensitiveLabelPattern.ReplaceAllString(value, "[redacted credential]")
	value = supportSensitivePrefixPattern.ReplaceAllString(value, "[redacted credential]")
	value = supportAPIKeyPattern.ReplaceAllString(value, "[redacted credential]")
	value = supportIdentifierPattern.ReplaceAllString(value, "[redacted identifier]")
	value = supportIdentifierMentionPattern.ReplaceAllString(value, "[redacted identifier]")
	value = supportUnixPathPattern.ReplaceAllString(value, "$1[redacted path]")
	value = supportGluedUnixPathPattern.ReplaceAllString(value, "[redacted path]")
	value = supportUNCPattern.ReplaceAllString(value, "[redacted path]")
	value = supportWindowsPathPattern.ReplaceAllString(value, "[redacted path]")
	// The opaque rule runs last so URLs, labels and paths are already handled
	// and a labelled required identifier can be excluded.
	value = redactOpaqueDiagnosticTokens(value)
	return truncateUTF8(strings.TrimSpace(value), limit)
}

// redactOpaqueDiagnosticTokens replaces an unlabeled opaque run with the
// credential placeholder, except for the required identifiers below. The
// preceding-context exclusion cannot be expressed in RE2, so the match list is
// walked and each token is classified explicitly.
func redactOpaqueDiagnosticTokens(value string) string {
	matches := supportOpaqueTokenPattern.FindAllStringIndex(value, -1)
	if len(matches) == 0 {
		return value
	}
	var builder strings.Builder
	builder.Grow(len(value))
	last := 0
	for _, match := range matches {
		start, end := match[0], match[1]
		builder.WriteString(value[last:start])
		token := value[start:end]
		if isRequiredDiagnosticIdentifier(value, start, token) {
			builder.WriteString(token)
		} else {
			builder.WriteString("[redacted credential]")
		}
		last = end
	}
	builder.WriteString(value[last:])
	return builder.String()
}

// isRequiredDiagnosticIdentifier reports whether an opaque run is a recovery
// identifier rather than a credential: a well-formed run/boot identity, or a
// token directly labelled as a run, boot, runtime, project, session or chat id.
func isRequiredDiagnosticIdentifier(value string, start int, token string) bool {
	if supportRequiredIdentityPattern.MatchString(token) {
		return true
	}
	prefix := strings.TrimRight(strings.ToLower(value[:start]), " \t=:")
	for _, label := range supportRequiredIdentifierLabels {
		if strings.HasSuffix(prefix, label) {
			return true
		}
	}
	return false
}

// boundDiagnosticInput cuts a raw diagnostic value before normalization or any
// regex run, and removes configured secrets before they can be split by the
// cut. The cut is a small multiple of the requested output limit with a floor
// for small limits, so the sanitizer's total work depends on the caller's limit
// rather than on an unbounded host-provided string. One extra secret-length
// window is scanned so a secret straddling the cut is still removed.
func boundDiagnosticInput(value string, limit int, secrets []string) string {
	bound := limit * diagnosticInputFactor
	if bound < diagnosticInputFloorBytes {
		bound = diagnosticInputFloorBytes
	}
	if len(value) <= bound {
		return redactConfiguredDiagnosticSecrets(value, secrets)
	}
	end := bound
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	scanEnd := end + maxSanitizerSecretBytes
	if scanEnd > len(value) {
		scanEnd = len(value)
	}
	redacted := redactConfiguredDiagnosticSecrets(value[:scanEnd], secrets)
	if len(redacted) <= end {
		return redacted + diagnosticTruncationMarker
	}
	cut := end
	for cut > 0 && !utf8.RuneStart(redacted[cut]) {
		cut--
	}
	return redacted[:cut] + diagnosticTruncationMarker
}

func normalizeDiagnosticText(value string) string {
	return strings.Join(strings.FieldsFunc(value, func(character rune) bool {
		return unicode.IsSpace(character) || unicode.IsControl(character) || unicode.Is(unicode.Cf, character)
	}), " ")
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return ""
	}
	end := limit - 3
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end] + "..."
}

// MarshalSupportSnapshot converts source values to allowlisted runtime facts
// and stable diagnostic codes, bounds the event collection, and verifies the
// serialized size before callers hand it to the browser transport.
func MarshalSupportSnapshot(runtime SupportSnapshotRuntime, events []ControllerEvent) (json.RawMessage, error) {
	runtime = sanitizedSupportRuntime(runtime)
	snapshot := SupportSnapshot{
		SchemaVersion:     SupportSnapshotSchemaVersion,
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
		Identity:          resolveSupportIdentity(runtime.Identity),
		HealthTransitions: SanitizeHealthTransitions(runtime.HealthTransitions),
		Runtime:           runtime,
		Events:            sanitizedSupportEvents(events),
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal support snapshot: %w", err)
	}
	if len(payload) > SupportSnapshotMaxBytes {
		return nil, fmt.Errorf("support snapshot exceeds the %d-byte limit", SupportSnapshotMaxBytes)
	}
	return json.RawMessage(payload), nil
}

// resolveSupportIdentity prefers an explicitly supplied identity, then the
// process identity set by the entrypoint, then the supervisor-exported
// environment. Only a sanitized identity is returned.
func resolveSupportIdentity(explicit *RunIdentity) *RunIdentity {
	if explicit != nil {
		if sanitized, ok := SanitizeRunIdentity(*explicit); ok {
			return &sanitized
		}
	}
	if sanitized, ok := SanitizeRunIdentity(ProcessRunIdentity()); ok {
		return &sanitized
	}
	if sanitized, ok := SanitizeRunIdentity(RunIdentityFromEnvironment(os.LookupEnv)); ok {
		return &sanitized
	}
	return nil
}

func sanitizedSupportRuntime(value SupportSnapshotRuntime) SupportSnapshotRuntime {
	value.Build = sanitizedSupportBuild(value.Build)
	value.Host.Reason = supportHostReasonCode(value.Host)
	value.Host.ApplicationReason = supportApplicationReasonCode(value.Host)
	value.ActiveRunCount = safeSupportCount(value.ActiveRunCount)
	value.RetainedDeletionCount = safeSupportCount(value.RetainedDeletionCount)
	if value.Schedule.State != "healthy" && value.Schedule.State != "degraded" && value.Schedule.State != "unknown" {
		value.Schedule.State = "unknown"
	}
	if value.Schedule.State == "degraded" {
		value.Schedule.Reason = "schedule.degraded"
	} else {
		value.Schedule.Reason = ""
	}
	value.SessionSchema = sanitizeSessionSchemaSummary(value.SessionSchema)
	return value
}

// sanitizeSessionSchemaSummary clamps the AUX-12 counters to non-negative,
// bounded values. A summary with no degradation and no newer-runtime flag is
// dropped rather than exported as a healthy zero.
func sanitizeSessionSchemaSummary(value *SessionSchemaSummary) *SessionSchemaSummary {
	if value == nil {
		return nil
	}
	result := SessionSchemaSummary{
		WrittenByNewerRuntime: value.WrittenByNewerRuntime,
		Version:               safeSupportCounter(value.Version),
		UnknownRecords:        safeSupportCounter(value.UnknownRecords),
		InvalidRecords:        safeSupportCounter(value.InvalidRecords),
		RepairedToolCalls:     safeSupportCounter(value.RepairedToolCalls),
	}
	if !result.WrittenByNewerRuntime && result.Version == 0 && result.UnknownRecords == 0 && result.InvalidRecords == 0 && result.RepairedToolCalls == 0 {
		return nil
	}
	return &result
}

// safeSupportCounter maps a missing, negative or excessive counter to zero so
// the export can never claim a negative or unbounded count.
func safeSupportCounter(value int) int {
	if value < 0 || value > maxSupportCount {
		return 0
	}
	return value
}

func safeSupportCount(value *int) *int {
	if value == nil || *value < 0 || *value > maxSupportCount {
		return nil
	}
	copy := *value
	return &copy
}

func sanitizedSupportEvents(events []ControllerEvent) []ControllerEvent {
	if len(events) > SupportSnapshotMaxEvents {
		events = events[len(events)-SupportSnapshotMaxEvents:]
	}
	result := make([]ControllerEvent, 0, len(events))
	for _, event := range events {
		if event.Kind != "request" {
			continue
		}
		if event.Outcome != "succeeded" && event.Outcome != "failed" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, event.At)
		if err != nil {
			continue
		}
		operation := event.Operation
		if !isSupportOperation(operation) {
			operation = "unknown"
		}
		detail := ""
		if event.Outcome == "failed" {
			detail = supportRequestFailureCodeFromStoredDetail(event.Detail)
		}
		result = append(result, ControllerEvent{
			At:        parsed.UTC().Format(time.RFC3339),
			Kind:      "request",
			Operation: operation,
			Outcome:   event.Outcome,
			Detail:    detail,
		})
	}
	return result
}

func makeSupportOperationAllowlist() map[string]struct{} {
	result := make(map[string]struct{}, len(piwire.CatalogControllerMethods)+1)
	result["unknown"] = struct{}{}
	for _, operation := range piwire.CatalogControllerMethods {
		result[operation] = struct{}{}
	}
	return result
}

func isSupportOperation(operation string) bool {
	_, ok := supportOperationAllowlist[operation]
	return ok
}

func supportRequestFailureCodeForError(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return supportRequestCanceledCode
	case errors.Is(err, context.DeadlineExceeded):
		return supportRequestTimedOutCode
	}
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) {
		switch coded.ErrorCode() {
		case "SUPPORT_SNAPSHOT_AUTH_REQUIRED":
			return supportRequestAuthenticationRequiredCode
		case "STALE_TRANSCRIPT_PROJECTION":
			return supportRequestStaleStateCode
		case "UNSUPPORTED_AGENT_CAPABILITY":
			return supportRequestCapabilityUnavailableCode
		}
	}
	return supportRequestFailedCode
}

func supportRequestFailureCodeFromStoredDetail(detail string) string {
	switch detail {
	case supportRequestFailedCode,
		supportRequestCanceledCode,
		supportRequestTimedOutCode,
		supportRequestAuthenticationRequiredCode,
		supportRequestStaleStateCode,
		supportRequestCapabilityUnavailableCode:
		return detail
	default:
		return supportRequestFailedCode
	}
}

func sanitizedSupportBuild(value BuildInfo) BuildInfo {
	version := defaultVersion
	if supportBuildVersionPattern.MatchString(value.Version) {
		version = value.Version
	}
	revision := defaultRevision
	if supportBuildRevisionPattern.MatchString(value.Revision) {
		revision = value.Revision
	}
	return BuildInfo{Version: version, Revision: revision}
}

func supportHostReasonCode(value SupportSnapshotHost) string {
	if value.Configured != nil && !*value.Configured {
		return "host.not_configured"
	}
	if value.Reachable != nil && !*value.Reachable {
		return "host.unreachable"
	}
	if value.Reason != "" {
		return "host.degraded"
	}
	return ""
}

func supportApplicationReasonCode(value SupportSnapshotHost) string {
	if value.ApplicationReady != nil && !*value.ApplicationReady {
		return "application.unavailable"
	}
	if value.ApplicationReason != "" {
		return "application.degraded"
	}
	return ""
}
