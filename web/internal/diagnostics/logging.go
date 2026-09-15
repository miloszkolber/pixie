package diagnostics

import (
	"context"
	"encoding"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strconv"
	"strings"
	"time"
)

const (
	// maxLogFieldBytes bounds one text field before the sanitizer runs. The
	// sanitizer normalizes the whole input, so pre-truncating keeps a hostile
	// value from forcing unbounded work or allocation.
	maxLogFieldBytes = 4096
	// maxLogGroupDepth bounds recursion through nested slog groups and dynamic
	// values. It caps cyclic structures as well as genuinely deep input.
	maxLogGroupDepth = 16
	// maxLogCollectionItems bounds how many map, slice or struct entries are
	// copied from one slog.Any value.
	maxLogCollectionItems = 256
	// maxLogConvertedNodes bounds the total number of values one slog.Any
	// conversion may walk. Per-level width and depth alone cannot bound the
	// work: a small graph whose children all point at the same next node is
	// re-walked once per path, and every value the redactor emits is serialized
	// even when the Go value shares it. Counting each visit stops both the
	// conversion and its output from growing exponentially.
	maxLogConvertedNodes = 4096
	// maxLogConvertedBytes bounds the redacted text one conversion accumulates.
	// The node budget alone still allows thousands of fields of
	// maxLogFieldBytes each; this caps the aggregate size instead.
	maxLogConvertedBytes = 1 << 18
	// redactedLogValue is the fixed placeholder substituted whenever a dynamic
	// value cannot be inspected safely: an unhandled kind, a cycle, a depth or
	// entry bound, a spent conversion budget, or a user method that panics.
	redactedLogValue = "[redacted value]"
)

// logVisit identifies a pointer-like value already visited on the current
// recursion path, so a self-referential value cannot recurse forever.
type logVisit struct {
	kind reflect.Kind
	ptr  uintptr
}

// logConversion bounds one slog.Any conversion. The active set breaks cycles on
// the current path, while the node and byte counters bound the total work and
// output size so a shared or wide object graph cannot expand without limit.
type logConversion struct {
	active map[logVisit]struct{}
	nodes  int
	bytes  int
}

func newLogConversion() *logConversion {
	return &logConversion{active: make(map[logVisit]struct{})}
}

// spent reports whether the conversion has exhausted its node or byte budget.
func (c *logConversion) spent() bool {
	return c.nodes >= maxLogConvertedNodes || c.bytes > maxLogConvertedBytes
}

// enter charges one value against the node budget and reports whether it may be
// inspected. Once the budget is spent every remaining value becomes the
// placeholder, so both conversion work and serialized size stay bounded.
func (c *logConversion) enter() bool {
	if c.spent() {
		return false
	}
	c.nodes++
	return true
}

// text redacts raw text and charges the redacted size against the byte budget.
func (c *logConversion) text(value string) string {
	return c.capText(redactLogText(value))
}

// capText charges already-redacted text against the byte budget and replaces it
// with the placeholder once the budget is spent.
func (c *logConversion) capText(redacted string) string {
	c.bytes += len(redacted)
	if c.bytes > maxLogConvertedBytes {
		return redactedLogValue
	}
	return redacted
}

// redactingHandler wraps a slog.Handler and applies SanitizeDiagnosticText to
// every emitted message, string attribute and error value. Attributes supplied
// through WithAttrs and the attributes on each record are both redacted, as are
// values nested in groups and slog.Any.
//
// A slog.Any value is arbitrary caller data, so the wrapper inspects it rather
// than handing it to the JSON encoder unchanged: structs and pointers are
// walked, maps have their keys and values sanitized, text marshalers are
// marshaled and then sanitized, and any kind it cannot safely inspect becomes a
// fixed placeholder. User methods invoked along the way are recovered so a
// panicking String/Error/MarshalText cannot terminate the logging goroutine.
// Each top-level attribute is converted under its own node and byte budget so a
// shared or pathologically wide object graph cannot expand without bound.
type redactingHandler struct {
	next slog.Handler
}

func newRedactingHandler(next slog.Handler) slog.Handler {
	if next == nil {
		return nil
	}
	return redactingHandler{next: next}
}

func (h redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	redacted := slog.NewRecord(record.Time, record.Level, redactLogText(record.Message), record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		redacted.AddAttrs(redactLogAttr(attr, 0, newLogConversion()))
		return true
	})
	return h.next.Handle(ctx, redacted)
}

func (h redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	redacted := make([]slog.Attr, len(attrs))
	for index, attr := range attrs {
		redacted[index] = redactLogAttr(attr, 0, newLogConversion())
	}
	return redactingHandler{next: h.next.WithAttrs(redacted)}
}

func (h redactingHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return redactingHandler{next: h.next.WithGroup(name)}
}

// redactLogAttr resolves a value and redacts the text-bearing kinds. Resolution
// happens before the kind switch so a LogValuer cannot smuggle a secret past
// the wrapper.
func redactLogAttr(attr slog.Attr, depth int, state *logConversion) slog.Attr {
	value := attr.Value.Resolve()
	if value.Kind() == slog.KindAny && value.Any() == nil {
		return attr
	}
	if depth >= maxLogGroupDepth {
		return slog.Attr{Key: attr.Key, Value: slog.StringValue(redactedLogValue)}
	}
	switch value.Kind() {
	case slog.KindString:
		value = slog.StringValue(state.text(value.String()))
	case slog.KindAny:
		value = slog.AnyValue(redactLogDynamic(value.Any(), depth, state))
	case slog.KindGroup:
		group := value.Group()
		redacted := make([]slog.Attr, len(group))
		for index, child := range group {
			redacted[index] = redactLogAttr(child, depth+1, state)
		}
		value = slog.GroupValue(redacted...)
	}
	return slog.Attr{Key: attr.Key, Value: value}
}

// redactLogDynamic sanitizes the text-like values reachable from a slog.Any.
// It never returns an uninspected concrete value for a kind it does not
// understand: structs, pointers, maps, slices and arrays are rebuilt into plain
// JSON-compatible shapes, and anything else is replaced with a fixed
// placeholder. The whole conversion fails closed if inspection panics.
func redactLogDynamic(value any, depth int, state *logConversion) (result any) {
	if value == nil {
		return nil
	}
	if depth >= maxLogGroupDepth || !state.enter() {
		return redactedLogValue
	}
	defer func() {
		if recover() != nil {
			result = redactedLogValue
		}
	}()
	switch typed := value.(type) {
	case time.Time, time.Duration:
		// Legitimate scalar kinds pass through so ordinary logs keep their
		// normal JSON handler form.
		return value
	case slog.Attr:
		// A nested Attr is data, not an instruction. Convert it to the same
		// plain shape a map produces so its Key cannot be serialized verbatim
		// as an exported struct field and its Value is not dropped.
		return redactLogNestedAttr(typed, depth+1, state)
	case slog.Value:
		return redactLogValueAny(typed, depth, state)
	case string:
		return state.text(typed)
	case []byte:
		return state.capText(redactLogBytes(typed))
	case error:
		return state.capText(recoverLogText(typed.Error))
	case fmt.Stringer:
		return state.capText(recoverLogText(typed.String))
	case json.Marshaler:
		return state.capText(recoverLogMarshaled(typed.MarshalJSON))
	case encoding.TextMarshaler:
		return state.capText(recoverLogMarshaled(typed.MarshalText))
	default:
		return redactLogReflectValue(reflect.ValueOf(value), depth, state)
	}
}

// redactLogNestedAttr converts a slog.Attr that appears in value position (for
// example inside a map, a group or a struct field) into the same plain object
// shape used for maps. Returning the raw Attr would let encoding/json emit its
// exported Key field, leaking a credential-bearing key, and would drop its
// Value. The key is sanitized like a map key and the value is redacted.
func redactLogNestedAttr(attr slog.Attr, depth int, state *logConversion) any {
	key := state.capText(redactLogText(attr.Key))
	return map[string]any{key: redactLogValueAny(attr.Value.Resolve(), depth, state)}
}

// redactLogValueAny converts an already-resolved slog.Value into the same plain
// shape redactLogDynamic returns. Groups become nested objects so a resolved
// LogValuer cannot reintroduce an opaque slog structure.
func redactLogValueAny(value slog.Value, depth int, state *logConversion) any {
	if depth >= maxLogGroupDepth || !state.enter() {
		return redactedLogValue
	}
	switch value.Kind() {
	case slog.KindBool:
		return value.Bool()
	case slog.KindInt64:
		return value.Int64()
	case slog.KindUint64:
		return value.Uint64()
	case slog.KindFloat64:
		return value.Float64()
	case slog.KindString:
		return state.text(value.String())
	case slog.KindTime:
		return value.Time()
	case slog.KindDuration:
		return value.Duration()
	case slog.KindGroup:
		group := value.Group()
		result := make(map[string]any, boundedLogItems(len(group)))
		for index, child := range group {
			if index >= maxLogCollectionItems || state.spent() {
				break
			}
			key := state.capText(redactLogText(child.Key))
			result[key] = redactLogValueAny(child.Value.Resolve(), depth+1, state)
		}
		return result
	case slog.KindLogValuer:
		return redactLogValueAny(value.Resolve(), depth, state)
	case slog.KindAny:
		return redactLogDynamic(value.Any(), depth+1, state)
	default:
		return redactedLogValue
	}
}

// redactLogReflectValue handles the concrete kinds reflection can describe.
// Only plain scalars pass through; every inspectable aggregate is rebuilt and
// every unhandled kind is replaced.
func redactLogReflectValue(reflected reflect.Value, depth int, state *logConversion) any {
	if !reflected.IsValid() {
		return nil
	}
	if depth >= maxLogGroupDepth {
		return redactedLogValue
	}
	switch reflected.Kind() {
	case reflect.Pointer:
		if reflected.IsNil() {
			return nil
		}
		return redactLogPointer(reflected, depth, state)
	case reflect.Interface:
		if reflected.IsNil() {
			return nil
		}
		return redactLogDynamic(reflected.Elem().Interface(), depth+1, state)
	case reflect.Struct:
		return redactLogStruct(reflected, depth, state)
	case reflect.Map:
		return redactLogMap(reflected, depth, state)
	case reflect.Slice:
		if reflected.IsNil() {
			return nil
		}
		if reflected.Type().Elem().Kind() == reflect.Uint8 {
			return state.capText(redactLogBytes(reflected.Bytes()))
		}
		return redactLogSlice(reflected, depth, state)
	case reflect.Array:
		if reflected.Type().Elem().Kind() == reflect.Uint8 {
			return state.capText(redactLogByteArray(reflected))
		}
		return redactLogSlice(reflected, depth, state)
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return reflected.Interface()
	case reflect.String:
		return state.text(reflected.String())
	default:
		// Channels, functions, unsafe pointers, complex numbers and invalid
		// kinds cannot be rendered safely and are replaced wholesale.
		return redactedLogValue
	}
}

func redactLogPointer(reflected reflect.Value, depth int, state *logConversion) any {
	visit := logVisit{kind: reflect.Pointer, ptr: reflected.Pointer()}
	if _, ok := state.active[visit]; ok {
		return redactedLogValue
	}
	state.active[visit] = struct{}{}
	defer delete(state.active, visit)
	return redactLogDynamic(reflected.Elem().Interface(), depth+1, state)
}

func redactLogStruct(reflected reflect.Value, depth int, state *logConversion) any {
	structType := reflected.Type()
	result := make(map[string]any)
	processed := 0
	for index := 0; index < structType.NumField(); index++ {
		if processed >= maxLogCollectionItems || state.spent() {
			break
		}
		field := structType.Field(index)
		if field.PkgPath != "" {
			// Unexported fields are not serialized by encoding/json and cannot
			// be read through reflection; omit them rather than guess.
			continue
		}
		name := logFieldName(field)
		if name == "" {
			continue
		}
		processed++
		result[state.capText(name)] = redactLogDynamic(reflected.Field(index).Interface(), depth+1, state)
	}
	return result
}

// logFieldName mirrors the object key encoding/json would use for an exported
// struct field, honoring an explicit json name and the "-" opt-out.
func logFieldName(field reflect.StructField) string {
	if tag, ok := field.Tag.Lookup("json"); ok {
		name, _, _ := strings.Cut(tag, ",")
		if name == "-" {
			return ""
		}
		if name != "" {
			return name
		}
	}
	return field.Name
}

func redactLogMap(reflected reflect.Value, depth int, state *logConversion) any {
	if reflected.IsNil() {
		return nil
	}
	visit := logVisit{kind: reflect.Map, ptr: reflected.Pointer()}
	if _, ok := state.active[visit]; ok {
		return redactedLogValue
	}
	state.active[visit] = struct{}{}
	defer delete(state.active, visit)
	remaining := boundedLogItems(reflected.Len())
	result := make(map[string]any, remaining)
	iterator := reflected.MapRange()
	for iterator.Next() {
		if remaining <= 0 || state.spent() {
			break
		}
		remaining--
		key := state.capText(redactLogMapKey(iterator.Key()))
		result[key] = redactLogDynamic(iterator.Value().Interface(), depth+1, state)
	}
	return result
}

func redactLogSlice(reflected reflect.Value, depth int, state *logConversion) any {
	length := reflected.Len()
	bounded := boundedLogItems(length)
	if reflected.Kind() == reflect.Slice && reflected.Pointer() != 0 && bounded > 0 {
		visit := logVisit{kind: reflect.Slice, ptr: reflected.Pointer()}
		if _, ok := state.active[visit]; ok {
			return redactedLogValue
		}
		state.active[visit] = struct{}{}
		defer delete(state.active, visit)
	}
	result := make([]any, 0, bounded)
	for index := 0; index < bounded; index++ {
		if state.spent() {
			result = append(result, redactedLogValue)
			break
		}
		result = append(result, redactLogDynamic(reflected.Index(index).Interface(), depth+1, state))
	}
	if bounded < length {
		result = append(result, redactedLogValue)
	}
	return result
}

func redactLogByteArray(reflected reflect.Value) string {
	bounded := boundedLogItems(reflected.Len())
	raw := make([]byte, 0, bounded)
	for index := 0; index < bounded; index++ {
		raw = append(raw, byte(reflected.Index(index).Uint()))
	}
	return redactLogText(string(raw))
}

// redactLogMapKey sanitizes an object key. JSON object keys are always text, so
// non-string keys are rendered with the same text or string marshaler the JSON
// encoder would consult and then redacted, never copied verbatim.
func redactLogMapKey(key reflect.Value) string {
	if !key.IsValid() {
		return redactedLogValue
	}
	switch key.Kind() {
	case reflect.String:
		return redactLogText(key.String())
	case reflect.Bool:
		return strconv.FormatBool(key.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(key.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(key.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(key.Float(), 'g', -1, 64)
	case reflect.Pointer, reflect.Interface:
		if key.IsNil() {
			return redactedLogValue
		}
	}
	value := key.Interface()
	if marshaler, ok := value.(encoding.TextMarshaler); ok {
		payload, ok := safeLogMarshal(marshaler.MarshalText)
		if !ok {
			return redactedLogValue
		}
		return redactLogBytes(payload)
	}
	if stringer, ok := value.(fmt.Stringer); ok {
		return recoverLogText(stringer.String)
	}
	return redactedLogValue
}

// redactLogBytes bounds a byte payload before it becomes a string, so a
// hostile marshaler cannot force an unbounded allocation, then sanitizes it.
func redactLogBytes(payload []byte) string {
	if len(payload) > maxLogFieldBytes {
		payload = payload[:maxLogFieldBytes]
	}
	return redactLogText(string(payload))
}

// recoverLogText invokes a user String or Error method and substitutes the
// placeholder if it panics. A panicking user method would otherwise unwind
// through slog and terminate the process.
func recoverLogText(method func() string) (result string) {
	defer func() {
		if recover() != nil {
			result = redactedLogValue
		}
	}()
	return redactLogText(method())
}

// recoverLogMarshaled invokes a user JSON or text marshaler and sanitizes the
// produced bytes. A panic or error becomes the fixed placeholder.
func recoverLogMarshaled(marshal func() ([]byte, error)) string {
	payload, ok := safeLogMarshal(marshal)
	if !ok {
		return redactedLogValue
	}
	return redactLogBytes(payload)
}

// safeLogMarshal runs a user marshaler with panic recovery and reports whether
// usable bytes were produced.
func safeLogMarshal(marshal func() ([]byte, error)) (payload []byte, ok bool) {
	defer func() {
		if recover() != nil {
			payload, ok = nil, false
		}
	}()
	payload, err := marshal()
	if err != nil {
		return nil, false
	}
	return payload, true
}

func boundedLogItems(length int) int {
	if length > maxLogCollectionItems {
		return maxLogCollectionItems
	}
	if length < 0 {
		return 0
	}
	return length
}

// redactLogText is the single log-facing entry point to the existing
// diagnostic sanitizer. Pre-truncating bounds the normalizer's working set; the
// sanitizer then redacts credentials, identifiers and paths.
func redactLogText(value string) string {
	if value == "" {
		return ""
	}
	return SanitizeDiagnosticText(truncateUTF8(value, maxLogFieldBytes), maxLogFieldBytes)
}
