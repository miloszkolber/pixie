package diagnostics

import (
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

func NewLogger(component string, build BuildInfo) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, nil)).With(
		"component", component,
		"version", build.Version,
		"revision", build.Revision,
	)
}
