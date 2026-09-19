package design

import "context"

// DeterministicParser is a small offline fixture adapter for focused tests and
// development. It accepts a preflighted ZIP archive containing fixture.json or
// design.json, rejects any archive without one, and never claims to implement
// openfig-core or Figma decoding.
type DeterministicParser struct{}

// NewDeterministicParser returns the explicit fixture parser adapter.
func NewDeterministicParser() Parser { return DeterministicParser{} }

func (DeterministicParser) Parse(ctx context.Context, source []byte) (NormalizedDocument, error) {
	return parseFixtureJSON(ctx, source)
}
