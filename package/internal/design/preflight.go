package design

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"unicode/utf8"
)

// PreflightReport contains bounded archive facts. It is useful to management
// callers that want to display why a source was rejected before parsing.
type PreflightReport struct {
	Bytes             int64    `json:"bytes"`
	Entries           int      `json:"entries"`
	DeclaredExpansion int64    `json:"declaredExpansion"`
	ObservedExpansion int64    `json:"observedExpansion"`
	Names             []string `json:"names,omitempty"`
}

// Preflight validates source size and ZIP metadata/content without decoding a
// Figma schema. It rejects path traversal, suffix/flattening collisions,
// excessive entries/expansion and malformed archive signatures.
func Preflight(source []byte) (PreflightReport, error) {
	return preflightWithLimitsContext(context.Background(), source, MaxUploadBytes, MaxArchiveExpansionBytes, MaxArchiveEntries)
}

func preflightWithLimits(source []byte, maxBytes, maxExpansion int64, maxEntries int) (PreflightReport, error) {
	return preflightWithLimitsContext(context.Background(), source, maxBytes, maxExpansion, maxEntries)
}

func preflightWithLimitsContext(ctx context.Context, source []byte, maxBytes, maxExpansion int64, maxEntries int) (PreflightReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return PreflightReport{}, err
	}
	if int64(len(source)) > maxBytes {
		return PreflightReport{}, designError("limit_exceeded", fmt.Errorf("source exceeds %d bytes: %w", maxBytes, ErrLimit))
	}
	if len(source) < 4 || !bytes.Equal(source[:4], []byte("PK\x03\x04")) && !bytes.Equal(source[:4], []byte("PK\x05\x06")) && !bytes.Equal(source[:4], []byte("PK\x07\x08")) {
		return PreflightReport{}, designError("invalid_request", fmt.Errorf("source is not a ZIP .fig archive: %w", ErrInvalidRequest))
	}
	reader, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil {
		return PreflightReport{}, designError("invalid_request", fmt.Errorf("read ZIP metadata: %w", ErrInvalidRequest))
	}
	if len(reader.File) == 0 {
		return PreflightReport{}, designError("invalid_request", fmt.Errorf("ZIP contains no entries: %w", ErrInvalidRequest))
	}
	if len(reader.File) > maxEntries {
		return PreflightReport{}, designError("limit_exceeded", ErrLimit)
	}
	seenPaths := make(map[string]struct{}, len(reader.File))
	seenBasenames := make(map[string]string, len(reader.File))
	report := PreflightReport{Bytes: int64(len(source)), Entries: len(reader.File)}
	var required bool
	for _, file := range reader.File {
		if err := ctx.Err(); err != nil {
			return PreflightReport{}, err
		}
		if file.Mode()&os.ModeSymlink != 0 {
			return PreflightReport{}, designError("invalid_request", ErrInvalidRequest)
		}
		name, directory, err := validateArchivePath(file.Name)
		if err != nil {
			return PreflightReport{}, err
		}
		if _, exists := seenPaths[name]; exists {
			return PreflightReport{}, designError("invalid_request", fmt.Errorf("duplicate ZIP path %q: %w", name, ErrInvalidRequest))
		}
		seenPaths[name] = struct{}{}
		if !directory {
			base := strings.ToLower(path.Base(name))
			if previous, exists := seenBasenames[base]; exists {
				return PreflightReport{}, designError("invalid_request", fmt.Errorf("ambiguous flattened archive basename %q (%s, %s): %w", base, previous, name, ErrInvalidRequest))
			}
			seenBasenames[base] = name
		}
		report.Names = append(report.Names, name)
		if file.UncompressedSize64 > uint64(maxExpansion) || report.DeclaredExpansion > maxExpansion-int64(file.UncompressedSize64) {
			return PreflightReport{}, designError("limit_exceeded", ErrLimit)
		}
		report.DeclaredExpansion += int64(file.UncompressedSize64)
		if !directory && isRequiredArchiveEntry(name) {
			required = true
		}
	}
	if !required {
		return PreflightReport{}, designError("invalid_request", fmt.Errorf("ZIP is missing canvas.fig/design.json input: %w", ErrInvalidRequest))
	}
	// Read every entry through archive decompression as an independent bound;
	// declared sizes alone are not enough for malformed/hostile ZIP metadata.
	for _, file := range reader.File {
		if err := ctx.Err(); err != nil {
			return PreflightReport{}, err
		}
		if _, directory, _ := validateArchivePath(file.Name); directory {
			continue
		}
		stream, openErr := file.Open()
		if openErr != nil {
			return PreflightReport{}, designError("invalid_request", fmt.Errorf("open ZIP entry: %w", ErrInvalidRequest))
		}
		observed, copyErr := io.Copy(io.Discard, io.LimitReader(contextReader{ctx: ctx, reader: stream}, maxExpansion-report.ObservedExpansion+1))
		closeErr := stream.Close()
		if copyErr != nil {
			if errors.Is(copyErr, context.Canceled) || errors.Is(copyErr, context.DeadlineExceeded) {
				return PreflightReport{}, copyErr
			}
			return PreflightReport{}, designError("invalid_request", fmt.Errorf("decompress ZIP entry: %w", ErrInvalidRequest))
		}
		if closeErr != nil {
			return PreflightReport{}, designError("invalid_request", fmt.Errorf("close ZIP entry: %w", ErrInvalidRequest))
		}
		if observed < 0 || report.ObservedExpansion > maxExpansion-observed {
			return PreflightReport{}, designError("limit_exceeded", ErrLimit)
		}
		report.ObservedExpansion += observed
	}
	return report, nil
}

func validateArchivePath(value string) (normalized string, directory bool, err error) {
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 || strings.Contains(value, "\\") || strings.Contains(value, ":") || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "\\") {
		return "", false, designError("invalid_request", ErrInvalidRequest)
	}
	directory = strings.HasSuffix(value, "/")
	trimmed := strings.TrimSuffix(value, "/")
	if trimmed == "" {
		return "", directory, designError("invalid_request", ErrInvalidRequest)
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) > MaxGraphDepth {
		return "", directory, designError("limit_exceeded", ErrLimit)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", directory, designError("invalid_request", ErrInvalidRequest)
		}
	}
	normalized = path.Clean(trimmed)
	if normalized == "." || strings.HasPrefix(normalized, "../") || normalized == ".." {
		return "", directory, designError("invalid_request", ErrInvalidRequest)
	}
	return normalized, directory, nil
}

func isRequiredArchiveEntry(name string) bool {
	base := strings.ToLower(path.Base(name))
	return base == "canvas.fig" || base == "design.json" || base == "fixture.json" || base == "document.json" || base == "canvas.json" || strings.HasSuffix(base, ".fig")
}

// extractFixtureJSON finds the deterministic parser payload from a validated
// archive. The archive's canvas.fig may be opaque binary; fixture.json is the
// explicit offline fixture format used by this package's parser.
func extractFixtureJSON(ctx context.Context, source []byte) ([]byte, []byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil {
		return nil, nil, err
	}
	var design []byte
	var cover []byte
	for _, file := range reader.File {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		name, directory, pathErr := validateArchivePath(file.Name)
		if pathErr != nil || directory {
			continue
		}
		base := strings.ToLower(path.Base(name))
		if base != "design.json" && base != "fixture.json" && base != "document.json" && base != "canvas.json" {
			if base == "thumbnail.png" || base == "cover.png" {
				entry, readErr := readZipEntry(ctx, file, MaxPreviewBytes)
				if readErr == nil {
					cover = entry
				}
			}
			continue
		}
		entry, readErr := readZipEntry(ctx, file, MaxUploadBytes)
		if readErr != nil {
			return nil, nil, readErr
		}
		if design != nil {
			return nil, nil, designError("invalid_request", fmt.Errorf("multiple deterministic design JSON entries: %w", ErrInvalidRequest))
		}
		design = entry
	}
	return design, cover, nil
}

func readZipEntry(ctx context.Context, file *zip.File, max int64) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stream, err := file.Open()
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, designError("invalid_request", ErrInvalidRequest)
	}
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: stream}, max+1))
	if err != nil {
		return nil, designError("invalid_request", ErrInvalidRequest)
	}
	if int64(len(data)) > max {
		return nil, designError("limit_exceeded", ErrLimit)
	}
	return data, nil
}

func parseFixtureJSON(ctx context.Context, source []byte) (NormalizedDocument, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return NormalizedDocument{}, err
	}
	trimmed := bytes.TrimSpace(source)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var value struct {
			Name          string   `json:"name"`
			ParserVersion string   `json:"parserVersion"`
			Pages         []Page   `json:"pages"`
			Nodes         []Node   `json:"nodes"`
			Warnings      []string `json:"warnings"`
		}
		if err := decodeFixture(trimmed, &value); err != nil {
			return NormalizedDocument{}, designError("invalid_request", fmt.Errorf("decode fixture design JSON: %w", ErrInvalidRequest))
		}
		if value.ParserVersion == "" {
			value.ParserVersion = "fixture-parser-v1"
		}
		return NormalizedDocument{Name: value.Name, ParserVersion: value.ParserVersion, Pages: value.Pages, Nodes: value.Nodes, Warnings: value.Warnings}, nil
	}
	design, cover, err := extractFixtureJSON(ctx, source)
	if err != nil {
		return NormalizedDocument{}, err
	}
	if len(design) == 0 {
		// A valid opaque canvas.fig archive remains inspectable through a
		// deterministic one-page fixture projection. This is intentionally a
		// fixture parser behavior, not an upstream parser claim.
		hash := hashBytes(source)
		return NormalizedDocument{Name: "Offline fixture", ParserVersion: "fixture-parser-v1", Pages: []Page{{ID: "page-1", Name: "Page 1", Position: 0}}, Nodes: []Node{{ID: "node-1-" + hash[:12], PageID: "page-1", Position: 0, Depth: 0, Type: "DOCUMENT", Name: "Fixture source", Visible: true}}, CoverPNG: cover}, nil
	}
	var value struct {
		Name          string   `json:"name"`
		ParserVersion string   `json:"parserVersion"`
		Pages         []Page   `json:"pages"`
		Nodes         []Node   `json:"nodes"`
		Warnings      []string `json:"warnings"`
	}
	if err := decodeFixture(design, &value); err != nil {
		return NormalizedDocument{}, designError("invalid_request", fmt.Errorf("decode fixture design JSON: %w", ErrInvalidRequest))
	}
	if value.ParserVersion == "" {
		value.ParserVersion = "fixture-parser-v1"
	}
	return NormalizedDocument{Name: value.Name, ParserVersion: value.ParserVersion, Pages: value.Pages, Nodes: value.Nodes, Warnings: value.Warnings, CoverPNG: cover}, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func decodeFixture(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("fixture contains trailing JSON")
		}
		return err
	}
	return nil
}
