package host

import (
	"reflect"
	"testing"
)

func TestAgentFrontmatterParsesLegacyStringifierShapes(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  map[string]any
	}{
		{
			name:  "plain scalars",
			input: "name: Reviewer\ndescription: Review\nmodel: fixture/echo\ncustom: keep\n",
			want: map[string]any{
				"name": "Reviewer", "description": "Review", "model": "fixture/echo", "custom": "keep",
			},
		},
		{
			name:  "nested and sequence with scalar types",
			input: "name: A\ntools:\n  - read\n  - bash\nlimits:\n  tokens: 100\nchecked: true\ncount: 3\nempty: null\nwhen: 2026-01-02\n",
			want: map[string]any{
				"name":    "A",
				"tools":   []any{"read", "bash"},
				"limits":  map[string]any{"tokens": float64(100)},
				"checked": true,
				"count":   float64(3),
				"empty":   nil,
				"when":    "2026-01-02",
			},
		},
		{
			name:  "quoted scalars and block literal",
			input: "name: Q\nvalue: \"a: b\"\nquote: he said \"hi\"\nhash: \"a # b\"\nbracket: \"[x]\"\nmultiline: |-\n  line1\n  line2\n",
			want: map[string]any{
				"name":      "Q",
				"value":     "a: b",
				"quote":     `he said "hi"`,
				"hash":      "a # b",
				"bracket":   "[x]",
				"multiline": "line1\nline2",
			},
		},
		{
			name:  "compact sequence mapping",
			input: "items:\n  - name: a\n    value: b\n  - name: c\n    value: d\n",
			want: map[string]any{
				"items": []any{
					map[string]any{"name": "a", "value": "b"},
					map[string]any{"name": "c", "value": "d"},
				},
			},
		},
		{
			name:  "flow collections",
			input: "tools: [read, bash]\nlimits: {tokens: 100, nested: [a, b]}\n",
			want: map[string]any{
				"tools":  []any{"read", "bash"},
				"limits": map[string]any{"tokens": float64(100), "nested": []any{"a", "b"}},
			},
		},
		{
			name:  "real native agent frontmatter",
			input: "name: explore\ndescription: Read-only codebase exploration specialist for focused searches, repository reconnaissance, and evidence-backed summaries. Use when you need fast context from files without edits.\ntools: read, grep, find, ls\nsessionPreference: persistent\nsessionHint: Prefer a topic-specific named session for iterative codebase exploration, e.g. session=\"explore-auth\".\n",
			want: map[string]any{
				"name":              "explore",
				"description":       "Read-only codebase exploration specialist for focused searches, repository reconnaissance, and evidence-backed summaries. Use when you need fast context from files without edits.",
				"tools":             "read, grep, find, ls",
				"sessionPreference": "persistent",
				"sessionHint":       `Prefer a topic-specific named session for iterative codebase exploration, e.g. session="explore-auth".`,
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseAgentFrontmatter(test.input)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parsed = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestAgentFrontmatterRoundTripsOwnOutput(t *testing.T) {
	properties := map[string]any{
		"name":        "Reviewer",
		"description": "Use when: reviewing code",
		"model":       "fixture/echo",
		"custom":      "keep",
		"count":       float64(3),
		"enabled":     true,
		"nested":      map[string]any{"tokens": float64(100)},
		"tools":       []any{"read", "bash"},
		"empty":       nil,
	}
	delete(properties, "empty")
	encoded, err := marshalAgentFrontmatter(properties)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseAgentFrontmatter(encoded)
	if err != nil {
		t.Fatalf("parse own output %q: %v", encoded, err)
	}
	if !reflect.DeepEqual(parsed, properties) {
		t.Fatalf("round trip = %#v, want %#v (encoded %q)", parsed, properties, encoded)
	}
}

func TestAgentFrontmatterRejectsMalformedInput(t *testing.T) {
	if _, err := parseAgentFrontmatter("bad: [\n"); err == nil {
		t.Fatal("malformed flow sequence was accepted")
	}
	if _, err := parseAgentFrontmatter("key:\n\tvalue: 1\n"); err == nil {
		t.Fatal("tab indentation was accepted")
	}
}
