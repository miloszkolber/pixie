package diagnostics_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/diagnostics"
)

// NewLoggerWithWriter exposes the production handler sink so a test can prove
// the redaction contract: hostile bearer, URL, API-key and absolute-path values
// must never appear in the JSON output, while ordinary fields survive.
func TestNewLoggerRedactsHostileValuesAcrossFieldShapes(t *testing.T) {
	const (
		hostileURL    = "https://url-user:url-password@example.invalid/support?access_token=query-token-value"
		hostileBearer = "Bearer bearer-token-value"
		hostileAPIKey = "sk_live_hostile-api-token-value"
		hostilePath   = "/home/alice/.pi/agent/config.json"
	)
	hostileError := errors.New("dial " + hostileURL + " while reading " + hostilePath + " with " + hostileAPIKey)

	var output bytes.Buffer
	logger := diagnostics.NewLoggerWithWriter(&output, "controller", diagnostics.NormalizeBuild("v1.2.3", "unknown"))
	logger.Warn("canvas request failed "+hostileBearer+" at "+hostilePath,
		"module", "canvas",
		"attempt", 3,
		"ok", true,
		"label", "safe-label",
		"error", hostileError,
		"apiKey", hostileAPIKey,
		slog.Group("target", "url", hostileURL, "path", hostilePath, "error", hostileError),
		"metadata", map[string]any{"path": hostilePath, "authorization": hostileBearer, "secret": hostileAPIKey},
		"tags", []string{hostileBearer, "safe-tag"},
		"blob", []byte(hostilePath),
		"counts", []int{1, 2, 3},
	)
	derived := logger.With("authorization", hostileBearer, "endpoint", hostileURL)
	derived.Warn("derived logger", "attempt", 2)

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two JSON log lines, got %d: %q", len(lines), output.String())
	}
	encoded := output.String()
	for _, forbidden := range []string{"bearer-token-value", "url-user", "url-password", "example.invalid", "query-token-value", "hostile-api-token-value", "alice", "/home/alice"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("redacted log output leaked %q: %s", forbidden, encoded)
		}
	}

	first := decodeLogRecord(t, lines[0])
	if first["component"] != "controller" || first["version"] != "v1.2.3" || first["revision"] != "unknown" {
		t.Fatalf("build identity did not survive redaction: %#v", first)
	}
	if first["module"] != "canvas" || first["label"] != "safe-label" {
		t.Fatalf("ordinary string fields did not survive: %#v", first)
	}
	if first["attempt"] != float64(3) || first["ok"] != true {
		t.Fatalf("non-string fields did not survive: %#v", first)
	}
	errorText, ok := first["error"].(string)
	if !ok || !strings.Contains(errorText, "dial") || !strings.Contains(errorText, "redacted") {
		t.Fatalf("error value was not redacted in place: %#v", first["error"])
	}
	target, ok := first["target"].(map[string]any)
	if !ok || len(target) != 3 {
		t.Fatalf("nested group did not survive: %#v", first["target"])
	}
	for _, key := range []string{"url", "path", "error"} {
		if text, ok := target[key].(string); !ok || !strings.Contains(text, "redacted") {
			t.Fatalf("nested group %q was not redacted: %#v", key, target)
		}
	}
	metadata, ok := first["metadata"].(map[string]any)
	if !ok || len(metadata) != 3 {
		t.Fatalf("dynamic map did not survive: %#v", first["metadata"])
	}
	for key, value := range metadata {
		if text, ok := value.(string); !ok || !strings.Contains(text, "redacted") {
			t.Fatalf("dynamic map value %q was not redacted: %#v", key, metadata)
		}
	}
	tags, ok := first["tags"].([]any)
	if !ok || len(tags) != 2 || tags[1] != "safe-tag" {
		t.Fatalf("dynamic slice did not survive: %#v", first["tags"])
	}

	second := decodeLogRecord(t, lines[1])
	if second["component"] != "controller" || second["attempt"] != float64(2) {
		t.Fatalf("derived logger fields did not survive: %#v", second)
	}
}

// tokenStruct is a plain struct with credential-bearing and ordinary fields.
// The handler must walk it instead of handing the raw struct to the JSON
// encoder.
type tokenStruct struct {
	Token string
	Path  string
	Label string
	Count int
	Ratio float64
	OK    bool
}

// tokenJSONMarshaler leaks a token and a path through its custom JSON form.
type tokenJSONMarshaler struct {
	Token string
	Path  string
}

func (v tokenJSONMarshaler) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`{"token":%q,"path":%q}`, v.Token, v.Path)), nil
}

// tokenTextMarshaler leaks a token through encoding.TextMarshaler.
type tokenTextMarshaler struct {
	Token string
}

func (v tokenTextMarshaler) MarshalText() ([]byte, error) {
	return []byte(v.Token), nil
}

// textMapKey leaks a credential through a non-string map key's text form.
type textMapKey struct {
	Value string
}

func (k textMapKey) MarshalText() ([]byte, error) {
	return []byte(k.Value), nil
}

// TestLoggerRedactsDynamicValueLeaks is the regression for the dynamic-value
// bypass: a struct, a pointer, a JSON and a text marshaler, a secret string map
// key and a non-string-keyed map must all be redacted, while ordinary fields
// survive.
func TestLoggerRedactsDynamicValueLeaks(t *testing.T) {
	const (
		structToken = "Bearer struct-bearer-token-value"
		structPath  = "/home/carol/.pi/agent/config.json"
		jsonToken   = "Bearer json-marshaler-token-value"
		jsonPath    = "/home/carol/.cache/token.json"
		textToken   = "Bearer text-marshaler-token-value"
		stringKey   = "sk_live_string-map-key-secret-0000"
		intMapValue = "Bearer int-map-value-token-value"
		textKeyVal  = "sk_live_text-map-key-secret-0000"
		textMapVal  = "Bearer text-map-value-token-value"
		ordinary    = "ordinary-value"
	)

	structValue := tokenStruct{
		Token: structToken,
		Path:  structPath,
		Label: ordinary,
		Count: 7,
		Ratio: 1.5,
		OK:    true,
	}

	var output bytes.Buffer
	logger := diagnostics.NewLoggerWithWriter(&output, "controller", diagnostics.NormalizeBuild("v1.2.3", "unknown"))

	logWithoutPanic(t, func() {
		logger.Warn("dynamic value leaks",
			"struct", structValue,
			"structPtr", &structValue,
			"jsonMarshaler", tokenJSONMarshaler{Token: jsonToken, Path: jsonPath},
			"textMarshaler", tokenTextMarshaler{Token: textToken},
			"secretKeyMap", map[string]string{stringKey: ordinary},
			"intKeyMap", map[int]string{7: intMapValue, 8: ordinary},
			"textKeyMap", map[textMapKey]string{{Value: textKeyVal}: textMapVal},
		)
	})

	encoded := output.String()
	for _, forbidden := range []string{
		"struct-bearer-token-value",
		"carol",
		"json-marshaler-token-value",
		"cache/token.json",
		"text-marshaler-token-value",
		"string-map-key-secret",
		"int-map-value-token-value",
		"text-map-key-secret",
		"text-map-value-token-value",
	} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("dynamic redaction leaked %q: %s", forbidden, encoded)
		}
	}

	record := decodeLogRecord(t, strings.TrimSpace(encoded))

	structField, ok := record["struct"].(map[string]any)
	if !ok {
		t.Fatalf("struct was not rebuilt as an object: %#v", record["struct"])
	}
	if structField["Label"] != ordinary || structField["Count"] != float64(7) ||
		structField["Ratio"] != 1.5 || structField["OK"] != true {
		t.Fatalf("ordinary struct fields did not survive: %#v", structField)
	}
	assertRedactedString(t, structField["Token"], "struct token")
	assertRedactedString(t, structField["Path"], "struct path")

	pointerField, ok := record["structPtr"].(map[string]any)
	if !ok {
		t.Fatalf("pointer to struct was not rebuilt as an object: %#v", record["structPtr"])
	}
	if pointerField["Label"] != ordinary {
		t.Fatalf("ordinary pointer field did not survive: %#v", pointerField)
	}
	assertRedactedString(t, pointerField["Token"], "pointer struct token")
	assertRedactedString(t, pointerField["Path"], "pointer struct path")

	assertRedactedString(t, record["jsonMarshaler"], "json marshaler")
	assertRedactedString(t, record["textMarshaler"], "text marshaler")

	secretKeyMap, ok := record["secretKeyMap"].(map[string]any)
	if !ok || len(secretKeyMap) != 1 {
		t.Fatalf("secret-key map did not survive as one entry: %#v", record["secretKeyMap"])
	}
	if secretKeyMap["[redacted credential]"] != ordinary {
		t.Fatalf("secret map key was not sanitized in place: %#v", secretKeyMap)
	}

	intKeyMap, ok := record["intKeyMap"].(map[string]any)
	if !ok || intKeyMap["8"] != ordinary {
		t.Fatalf("non-string-keyed map did not survive: %#v", record["intKeyMap"])
	}
	assertRedactedString(t, intKeyMap["7"], "int-keyed map value")

	textKeyMap, ok := record["textKeyMap"].(map[string]any)
	if !ok || len(textKeyMap) != 1 {
		t.Fatalf("text-marshaler-key map did not survive: %#v", record["textKeyMap"])
	}
	for key, value := range textKeyMap {
		if key != "[redacted credential]" {
			t.Fatalf("text-marshaler map key was not sanitized: %#v", textKeyMap)
		}
		assertRedactedString(t, value, "text-marshaler map value")
	}
}

// panickingStringer reproduces user code that panics from String.
type panickingStringer struct{}

func (panickingStringer) String() string {
	panic("Stringer must be invoked under recovery")
}

// panickingError reproduces user code that panics from Error.
type panickingError struct{}

func (panickingError) Error() string {
	panic("Error must be invoked under recovery")
}

// panickingMarshaler reproduces user code that panics from MarshalJSON.
type panickingMarshaler struct{}

func (panickingMarshaler) MarshalJSON() ([]byte, error) {
	panic("MarshalJSON must be invoked under recovery")
}

// TestLoggerRecoversPanickingUserMethods is the regression for the process
// crash: a nil *url.URL and panicking String, Error and MarshalJSON methods
// must become placeholders instead of unwinding through the logging goroutine,
// while ordinary fields survive.
func TestLoggerRecoversPanickingUserMethods(t *testing.T) {
	var output bytes.Buffer
	logger := diagnostics.NewLoggerWithWriter(&output, "controller", diagnostics.NormalizeBuild("v1.2.3", "unknown"))

	logWithoutPanic(t, func() {
		logger.Warn("panicking user methods",
			"nilURL", (*url.URL)(nil),
			"stringer", panickingStringer{},
			"error", panickingError{},
			"marshaler", panickingMarshaler{},
			"ordinary", "ordinary-value",
		)
	})

	record := decodeLogRecord(t, strings.TrimSpace(output.String()))
	for _, key := range []string{"nilURL", "stringer", "error", "marshaler"} {
		if record[key] != "[redacted value]" {
			t.Fatalf("%s was not replaced with the placeholder: %#v", key, record[key])
		}
	}
	if record["ordinary"] != "ordinary-value" {
		t.Fatalf("ordinary field did not survive: %#v", record["ordinary"])
	}
}

// TestLoggerPreservesScalarKinds guards the companion contract that legitimate
// scalar values still reach the JSON handler unchanged.
func TestLoggerPreservesScalarKinds(t *testing.T) {
	var output bytes.Buffer
	logger := diagnostics.NewLoggerWithWriter(&output, "controller", diagnostics.NormalizeBuild("v1.2.3", "unknown"))
	when := time.Date(2026, time.September, 15, 12, 0, 0, 0, time.UTC)

	logWithoutPanic(t, func() {
		logger.Warn("scalars", "when", when, "wait", 90*time.Second, "count", 3, "ok", true, "ratio", 1.5)
	})

	record := decodeLogRecord(t, strings.TrimSpace(output.String()))
	whenText, ok := record["when"].(string)
	if !ok || !strings.HasPrefix(whenText, "2026-09-15T12:00:00") {
		t.Fatalf("time.Time did not survive: %#v", record["when"])
	}
	if record["wait"] != float64(int64(90*time.Second)) {
		t.Fatalf("time.Duration did not survive: %#v", record["wait"])
	}
	if record["count"] != float64(3) || record["ok"] != true || record["ratio"] != 1.5 {
		t.Fatalf("scalar values did not survive: %#v", record)
	}
}

// cyclicNode reproduces a self-referential value that must terminate through
// the cycle bound rather than recurse without bound.
type cyclicNode struct {
	Label string
	Next  *cyclicNode
}

// TestLoggerBoundsCyclicDynamicValues proves a self-referential pointer is
// replaced by the placeholder instead of recursing forever.
func TestLoggerBoundsCyclicDynamicValues(t *testing.T) {
	first := &cyclicNode{Label: "first"}
	first.Next = first

	var output bytes.Buffer
	logger := diagnostics.NewLoggerWithWriter(&output, "controller", diagnostics.NormalizeBuild("v1.2.3", "unknown"))
	logWithoutPanic(t, func() {
		logger.Warn("cycle", "node", first)
	})

	encoded := strings.TrimSpace(output.String())
	if len(encoded) > 4*4096 {
		t.Fatalf("cyclic value was not bounded: %d bytes", len(encoded))
	}
	record := decodeLogRecord(t, encoded)
	node, ok := record["node"].(map[string]any)
	if !ok {
		t.Fatalf("cyclic node was not rebuilt as an object: %#v", record["node"])
	}
	if node["Label"] != "first" {
		t.Fatalf("ordinary cyclic field did not survive: %#v", node)
	}
	if node["Next"] != "[redacted value]" {
		t.Fatalf("cycle was not replaced with the placeholder: %#v", node["Next"])
	}
}

func assertRedactedString(t *testing.T, value any, label string) {
	t.Helper()
	text, ok := value.(string)
	if !ok || !strings.Contains(text, "redacted") {
		t.Fatalf("%s was not redacted: %#v", label, value)
	}
}

// logWithoutPanic turns a handler panic into a clean test failure instead of
// crashing the test process before the redaction contract can be asserted.
func logWithoutPanic(t *testing.T, log func()) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("logger panicked on a dynamic value: %v", recovered)
		}
	}()
	log()
}

func decodeLogRecord(t *testing.T, line string) map[string]any {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("log line is not JSON: %v: %q", err, line)
	}
	return record
}
