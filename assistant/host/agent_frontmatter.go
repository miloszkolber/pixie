package host

// This file owns the self-contained YAML subset used for agent Markdown
// frontmatter. The assistant module must not grow a third-party YAML dependency
// because the package module's build graph and lockfile are a shared
// integration boundary. The codec covers the YAML the legacy Bun host writes
// (block mappings, block sequences, flow collections, quoted scalars, block
// literals) and writes back readable block YAML. Anything it cannot parse is
// reported as a warning, exactly like the legacy parser, so an unknown record
// is skipped rather than rewritten.

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var yamlIntegerPattern = regexp.MustCompile(`^[-+]?[0-9]+$`)
var yamlFloatPattern = regexp.MustCompile(`^[-+]?([0-9]+\.[0-9]*|\.[0-9]+|[0-9]+)([eE][-+]?[0-9]+)?$`)

// parseAgentFrontmatter parses one frontmatter block. JSON is accepted first
// because it is a YAML 1.2 subset; otherwise the block-YAML subset is used.
func parseAgentFrontmatter(text string) (map[string]any, error) {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
			if parsed == nil {
				return map[string]any{}, nil
			}
			return parsed, nil
		}
	}
	parser, err := newYAMLParser(text)
	if err != nil {
		return nil, err
	}
	value, err := parser.parseNode(0)
	if err != nil {
		return nil, err
	}
	if value == nil {
		return map[string]any{}, nil
	}
	if object, ok := value.(map[string]any); ok {
		return object, nil
	}
	return map[string]any{}, nil
}

type yamlLine struct {
	raw     string
	indent  int
	content string
	blank   bool
}

type yamlParser struct {
	lines []yamlLine
	pos   int
}

func newYAMLParser(input string) (*yamlParser, error) {
	normalized := strings.ReplaceAll(input, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	rawLines := strings.Split(normalized, "\n")
	lines := make([]yamlLine, 0, len(rawLines))
	for _, raw := range rawLines {
		indent := 0
		for indent < len(raw) && raw[indent] == ' ' {
			indent++
		}
		if indent < len(raw) && raw[indent] == '\t' {
			return nil, errors.New("tabs are not allowed in YAML indentation")
		}
		content := raw[indent:]
		trimmed := strings.TrimSpace(content)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			lines = append(lines, yamlLine{raw: raw, indent: indent, blank: true})
			continue
		}
		lines = append(lines, yamlLine{raw: raw, indent: indent, content: content})
	}
	return &yamlParser{lines: lines}, nil
}

func (p *yamlParser) skipBlank() {
	for p.pos < len(p.lines) && p.lines[p.pos].blank {
		p.pos++
	}
}

func (p *yamlParser) peek() *yamlLine {
	p.skipBlank()
	if p.pos >= len(p.lines) {
		return nil
	}
	return &p.lines[p.pos]
}

func (p *yamlParser) parseNode(indent int) (any, error) {
	line := p.peek()
	if line == nil || line.indent < indent {
		return nil, nil
	}
	if line.indent > indent {
		indent = line.indent
	}
	if line.content == "-" || strings.HasPrefix(line.content, "- ") {
		return p.parseSequence(indent)
	}
	if _, _, ok := splitYAMLKey(line.content); ok {
		return p.parseMapping(indent)
	}
	p.pos++
	return parseYAMLInlineScalar(stripPlainComment(line.content))
}

func (p *yamlParser) parseMapping(indent int) (map[string]any, error) {
	result := map[string]any{}
	for {
		line := p.peek()
		if line == nil || line.indent != indent {
			break
		}
		key, rest, ok := splitYAMLKey(line.content)
		if !ok {
			return nil, fmt.Errorf("invalid YAML mapping line %q", line.content)
		}
		p.pos++
		rest = strings.TrimSpace(rest)
		var value any
		var err error
		switch {
		case isBlockScalarIndicator(rest):
			value, err = p.parseBlockScalar(line.indent, rest)
		case rest == "" || strings.HasPrefix(rest, "#"):
			next := p.peek()
			if next != nil && next.indent > indent {
				value, err = p.parseNode(next.indent)
			}
		default:
			value, err = parseYAMLInlineScalar(stripPlainComment(rest))
		}
		if err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, nil
}

func (p *yamlParser) parseSequence(indent int) ([]any, error) {
	result := []any{}
	for {
		line := p.peek()
		if line == nil || line.indent != indent {
			break
		}
		if line.content != "-" && !strings.HasPrefix(line.content, "- ") {
			break
		}
		item := strings.TrimSpace(strings.TrimPrefix(line.content, "-"))
		if item == "" {
			p.pos++
			next := p.peek()
			if next != nil && next.indent > indent {
				value, err := p.parseNode(next.indent)
				if err != nil {
					return nil, err
				}
				result = append(result, value)
			} else {
				result = append(result, nil)
			}
			continue
		}
		if _, _, ok := splitYAMLKey(item); ok {
			// A compact sequence item such as "- name: a" starts a mapping whose
			// continuation lines align under the item content.
			contentIndent := line.indent + 2
			p.lines[p.pos] = yamlLine{raw: strings.Repeat(" ", contentIndent) + item, indent: contentIndent, content: item}
			value, err := p.parseMapping(contentIndent)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
			continue
		}
		p.pos++
		value, err := parseYAMLInlineScalar(stripPlainComment(item))
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func (p *yamlParser) parseBlockScalar(parentIndent int, indicator string) (any, error) {
	style := indicator[0]
	chomping := byte(0)
	explicitIndent := 0
	for index := 1; index < len(indicator); index++ {
		switch character := indicator[index]; {
		case character == '-' || character == '+':
			chomping = character
		case character >= '1' && character <= '9':
			explicitIndent = int(character - '0')
		}
	}
	start := p.pos
	contentIndent := 0
	if explicitIndent > 0 {
		contentIndent = parentIndent + explicitIndent
	}
	last := start
	for last < len(p.lines) {
		line := p.lines[last]
		if line.blank {
			last++
			continue
		}
		if line.indent <= parentIndent {
			break
		}
		if contentIndent == 0 {
			contentIndent = line.indent
		}
		last++
	}
	end := last
	for end > start && p.lines[end-1].blank {
		end--
	}
	var builder strings.Builder
	for index := start; index < end; index++ {
		line := p.lines[index]
		if line.blank {
			builder.WriteString("\n")
			continue
		}
		text := line.raw
		if contentIndent > 0 && len(text) >= contentIndent {
			text = text[contentIndent:]
		} else {
			text = strings.TrimLeft(text, " ")
		}
		builder.WriteString(text)
		builder.WriteString("\n")
	}
	p.pos = end
	content := builder.String()
	if style == '>' {
		content = foldBlockScalar(content)
	}
	switch chomping {
	case '-':
		content = strings.TrimRight(content, "\n")
	case '+':
	default:
		content = strings.TrimRight(content, "\n") + "\n"
	}
	return content, nil
}

func foldBlockScalar(content string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	var builder strings.Builder
	for index, line := range lines {
		if index > 0 {
			if line == "" {
				builder.WriteString("\n")
			} else {
				builder.WriteString(" ")
			}
		}
		builder.WriteString(line)
	}
	if strings.HasSuffix(content, "\n") {
		builder.WriteString("\n")
	}
	return builder.String()
}

func isBlockScalarIndicator(value string) bool {
	if value == "" || (value[0] != '|' && value[0] != '>') {
		return false
	}
	rest := stripPlainComment(value)
	return rest == value || strings.HasPrefix(rest, string(value[0]))
}

func splitYAMLKey(content string) (key, rest string, ok bool) {
	if content == "" {
		return "", "", false
	}
	if content[0] == '"' || content[0] == '\'' {
		quote := content[0]
		index := 1
		for index < len(content) {
			if content[index] == '\\' && quote == '"' {
				index += 2
				continue
			}
			if content[index] == quote {
				break
			}
			index++
		}
		if index >= len(content) {
			return "", "", false
		}
		keyText := content[:index+1]
		after := strings.TrimLeft(content[index+1:], " ")
		if !strings.HasPrefix(after, ":") {
			return "", "", false
		}
		parsed, err := parseYAMLInlineScalar(keyText)
		if err != nil {
			return "", "", false
		}
		key, ok = parsed.(string)
		if !ok {
			return "", "", false
		}
		return key, after[1:], true
	}
	for index := 0; index < len(content); index++ {
		if content[index] != ':' {
			continue
		}
		if index+1 < len(content) && content[index+1] != ' ' {
			continue
		}
		candidate := strings.TrimSpace(content[:index])
		if candidate == "" {
			return "", "", false
		}
		return candidate, content[index+1:], true
	}
	return "", "", false
}

func parseYAMLInlineScalar(value string) (any, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "~" {
		return nil, nil
	}
	switch value[0] {
	case '[':
		return parseFlowSequence(value)
	case '{':
		return parseFlowMapping(value)
	case '"':
		var parsed string
		if err := json.Unmarshal([]byte(value), &parsed); err != nil {
			return nil, err
		}
		return parsed, nil
	case '\'':
		if len(value) < 2 || value[len(value)-1] != '\'' {
			return nil, errors.New("unterminated single-quoted scalar")
		}
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'"), nil
	}
	switch value {
	case "null", "Null", "NULL":
		return nil, nil
	case "true", "True", "TRUE":
		return true, nil
	case "false", "False", "FALSE":
		return false, nil
	}
	if yamlIntegerPattern.MatchString(value) {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			return float64(parsed), nil
		}
	}
	if yamlFloatPattern.MatchString(value) {
		if parsed, err := strconv.ParseFloat(value, 64); err == nil {
			return parsed, nil
		}
	}
	return value, nil
}

func parseFlowSequence(value string) ([]any, error) {
	inner, err := flowInner(value, '[', ']')
	if err != nil {
		return nil, err
	}
	result := []any{}
	for _, part := range splitFlow(inner) {
		if strings.TrimSpace(part) == "" {
			continue
		}
		element, err := parseYAMLInlineScalar(part)
		if err != nil {
			return nil, err
		}
		result = append(result, element)
	}
	return result, nil
}

func parseFlowMapping(value string) (map[string]any, error) {
	inner, err := flowInner(value, '{', '}')
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	for _, part := range splitFlow(inner) {
		if strings.TrimSpace(part) == "" {
			continue
		}
		key, rest, ok := splitYAMLKey(part)
		if !ok {
			return nil, fmt.Errorf("invalid flow mapping entry %q", part)
		}
		element, err := parseYAMLInlineScalar(strings.TrimSpace(rest))
		if err != nil {
			return nil, err
		}
		result[key] = element
	}
	return result, nil
}

func flowInner(value string, open, close byte) (string, error) {
	if len(value) < 2 || value[0] != open || value[len(value)-1] != close {
		return "", fmt.Errorf("invalid flow collection %q", value)
	}
	return value[1 : len(value)-1], nil
}

func splitFlow(value string) []string {
	parts := []string{}
	depth := 0
	single, double := false, false
	start := 0
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch {
		case character == '\'' && !double:
			single = !single
		case character == '"' && !single:
			double = !double
		case single || double:
		case character == '[' || character == '{':
			depth++
		case character == ']' || character == '}':
			depth--
		case character == ',' && depth == 0:
			parts = append(parts, value[start:index])
			start = index + 1
		}
	}
	parts = append(parts, value[start:])
	return parts
}

func stripPlainComment(value string) string {
	single, double := false, false
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch {
		case character == '\'' && !double:
			single = !single
		case character == '"' && !single:
			double = !double
		case character == '#' && !single && !double && (index == 0 || value[index-1] == ' ' || value[index-1] == '\t'):
			return strings.TrimRight(value[:index], " \t")
		}
	}
	return strings.TrimSpace(value)
}

// marshalAgentFrontmatter renders properties as deterministic block YAML.
func marshalAgentFrontmatter(properties map[string]any) (string, error) {
	if len(properties) == 0 {
		return "{}\n", nil
	}
	var builder strings.Builder
	if err := writeYAMLMapping(&builder, properties, 0); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func writeYAMLMapping(builder *strings.Builder, value map[string]any, indent int) error {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	padding := strings.Repeat(" ", indent)
	for _, key := range keys {
		builder.WriteString(padding)
		builder.WriteString(yamlKey(key))
		builder.WriteString(":")
		if err := writeYAMLValue(builder, value[key], indent); err != nil {
			return err
		}
	}
	return nil
}

func writeYAMLValue(builder *strings.Builder, value any, indent int) error {
	switch typed := value.(type) {
	case nil:
		builder.WriteString(" null\n")
	case map[string]any:
		if len(typed) == 0 {
			builder.WriteString(" {}\n")
			return nil
		}
		builder.WriteString("\n")
		return writeYAMLMapping(builder, typed, indent+2)
	case []any:
		if len(typed) == 0 {
			builder.WriteString(" []\n")
			return nil
		}
		builder.WriteString("\n")
		return writeYAMLSequence(builder, typed, indent+2)
	default:
		scalar, err := yamlScalar(value)
		if err != nil {
			return err
		}
		builder.WriteString(" ")
		builder.WriteString(scalar)
		builder.WriteString("\n")
	}
	return nil
}

func writeYAMLSequence(builder *strings.Builder, value []any, indent int) error {
	padding := strings.Repeat(" ", indent)
	for _, item := range value {
		builder.WriteString(padding)
		builder.WriteString("-")
		switch typed := item.(type) {
		case map[string]any:
			if len(typed) == 0 {
				builder.WriteString(" {}\n")
				continue
			}
			builder.WriteString("\n")
			if err := writeYAMLMapping(builder, typed, indent+2); err != nil {
				return err
			}
		case []any:
			if len(typed) == 0 {
				builder.WriteString(" []\n")
				continue
			}
			builder.WriteString("\n")
			if err := writeYAMLSequence(builder, typed, indent+2); err != nil {
				return err
			}
		default:
			scalar, err := yamlScalar(item)
			if err != nil {
				return err
			}
			builder.WriteString(" ")
			builder.WriteString(scalar)
			builder.WriteString("\n")
		}
	}
	return nil
}

func yamlScalar(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "null", nil
	case bool:
		if typed {
			return "true", nil
		}
		return "false", nil
	case string:
		return yamlString(typed), nil
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
}

func yamlKey(key string) string {
	if key != "" && isYAMLPlainSafe(key, true) {
		return key
	}
	encoded, _ := json.Marshal(key)
	return string(encoded)
}

func yamlString(value string) string {
	if value != "" && isYAMLPlainSafe(value, false) {
		return value
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func isYAMLPlainSafe(value string, key bool) bool {
	switch value {
	case "null", "Null", "NULL", "~", "true", "True", "TRUE", "false", "False", "FALSE",
		"yes", "Yes", "YES", "no", "No", "NO", "on", "On", "ON", "off", "Off", "OFF":
		return false
	}
	if strings.HasPrefix(value, " ") || strings.HasSuffix(value, " ") {
		return false
	}
	if strings.ContainsAny(value, "\n\r\t\x00") {
		return false
	}
	if strings.ContainsAny(value, ":#") {
		return false
	}
	if strings.ContainsRune("-?:,[]{}#&*!|>'\"%@`", rune(value[0])) {
		return false
	}
	if yamlIntegerPattern.MatchString(value) || yamlFloatPattern.MatchString(value) {
		return false
	}
	if key && strings.Contains(value, " ") {
		return false
	}
	return true
}
