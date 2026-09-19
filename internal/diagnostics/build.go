package diagnostics

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"unicode"
)

const (
	defaultVersion  = "0.0.0-dev"
	defaultRevision = "unknown"
	maxBuildValue   = 128
)

type BuildInfo struct {
	Version  string `json:"version"`
	Revision string `json:"revision"`
}

func NormalizeBuild(version, revision string) BuildInfo {
	return BuildInfo{
		Version:  normalizeBuildValue(version, defaultVersion),
		Revision: normalizeBuildValue(revision, defaultRevision),
	}
}

func normalizeBuildValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	runes := []rune(value)
	if len(runes) > maxBuildValue {
		runes = runes[:maxBuildValue]
	}
	for _, character := range runes {
		if unicode.IsControl(character) || unicode.Is(unicode.Cf, character) {
			return fallback
		}
	}
	return string(runes)
}

// NewLogger builds a JSON logger for one component. Every message, string
// attribute and error value is redacted through SanitizeDiagnosticText before
// it is encoded, including values nested in groups and slog.Any.
func NewLogger(component string, build BuildInfo) *slog.Logger {
	return NewLoggerWithWriter(os.Stderr, component, build)
}

// NewLoggerWithWriter is NewLogger with an explicit sink. It keeps the same
// redaction contract and lets a caller or test capture the encoded output.
func NewLoggerWithWriter(writer io.Writer, component string, build BuildInfo) *slog.Logger {
	if writer == nil {
		writer = io.Discard
	}
	handler := newRedactingHandler(slog.NewJSONHandler(writer, nil))
	logger := slog.New(handler).With(
		"component", component,
		"version", build.Version,
		"revision", build.Revision,
	)
	if attributes := RunIdentityLoggerAttributes(ProcessRunIdentity()); len(attributes) > 0 {
		logger = logger.With(attributes...)
	}
	return logger
}
