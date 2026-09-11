package design

import "context"

// DeterministicParser is a small offline fixture adapter for focused tests and
// development. It accepts a preflighted ZIP archive containing fixture.json or
// design.json and never claims to implement openfig-core or Figma decoding.
type DeterministicParser struct{}

// NewDeterministicParser returns the explicit fixture parser adapter.
func NewDeterministicParser() Parser { return DeterministicParser{} }

// NewFixtureParser is a discoverable alias for test/integration callers.
func NewFixtureParser() Parser { return DeterministicParser{} }

// DeterministicFixtureParser is a function-style alias for callers that name
// the adapter by its fixture role.
func DeterministicFixtureParser() Parser { return DeterministicParser{} }

func (DeterministicParser) Parse(ctx context.Context, source []byte) (NormalizedDocument, error) {
	return parseFixtureJSON(ctx, source)
}
