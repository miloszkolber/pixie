// Package designfixture supplies an explicit JSON fixture parser for integration
// tests. Production packages cannot import it through Go's internal boundary.
package designfixture

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/miloszkolber/pixie/internal/design"
)

// NewParser accepts preflighted ZIP fixtures containing design.json or
// fixture.json. It does not decode Figma or stand in for a production worker.
func NewParser() design.Parser { return design.ParserFunc(parse) }

func parse(ctx context.Context, source []byte) (design.NormalizedDocument, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return design.NormalizedDocument{}, err
	}
	data := bytes.TrimSpace(source)
	var cover []byte
	if len(data) == 0 || data[0] != '{' {
		var err error
		data, cover, err = extract(ctx, source)
		if err != nil {
			return design.NormalizedDocument{}, err
		}
	}
	if len(data) == 0 {
		return design.NormalizedDocument{}, fmt.Errorf("fixture archive is missing design.json: %w", design.ErrInvalidRequest)
	}
	var document design.NormalizedDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return design.NormalizedDocument{}, fmt.Errorf("decode fixture JSON: %w", design.ErrInvalidRequest)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return design.NormalizedDocument{}, fmt.Errorf("fixture contains trailing JSON: %w", design.ErrInvalidRequest)
	}
	if document.ParserVersion == "" {
		document.ParserVersion = "fixture-parser-v1"
	}
	document.CoverPNG = cover
	return document, nil
}

func extract(ctx context.Context, source []byte) ([]byte, []byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil {
		return nil, nil, err
	}
	var data, cover []byte
	for _, file := range reader.File {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if file.FileInfo().IsDir() {
			continue
		}
		switch strings.ToLower(path.Base(file.Name)) {
		case "thumbnail.png", "cover.png":
			if entry, err := readEntry(ctx, file, design.MaxPreviewBytes); err == nil {
				cover = entry
			}
		case "design.json", "fixture.json", "document.json", "canvas.json":
			if data != nil {
				return nil, nil, fmt.Errorf("multiple design JSON entries: %w", design.ErrInvalidRequest)
			}
			data, err = readEntry(ctx, file, design.MaxUploadBytes)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	return data, cover, nil
}

func readEntry(ctx context.Context, file *zip.File, max int64) ([]byte, error) {
	stream, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx, stream}, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, design.ErrLimit
	}
	return data, nil
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
