package canvas

import (
	"html"
	"strings"
	"unicode"
)

// The parser is intentionally a bounded, non-executing selector reader. It
// accepts a conservative CSS subset (tag, #id, .class and descendant chains)
// and treats the draft as untrusted text rather than constructing a browser
// DOM or evaluating scripts.
type htmlNode struct {
	tag      string
	id       string
	classes  map[string]struct{}
	start    int
	end      int
	parent   *htmlNode
	children []*htmlNode
}

func selectHTML(content []byte, selector string, maxMatches, maxBytes int, includeDOM bool) (selectedHTML, error) {
	parts, err := parseSelector(selector)
	if err != nil {
		return selectedHTML{}, err
	}
	root, nodes := parseHTMLNodes(string(content))
	matches := make([]*htmlNode, 0, minInt(maxMatches, 16))
	for _, node := range nodes {
		if selectorMatches(node, parts) {
			matches = append(matches, node)
		}
	}
	result := selectedHTML{Total: len(matches)}
	if len(matches) > maxMatches {
		matches = matches[:maxMatches]
		result.Truncated = true
	}
	var dom strings.Builder
	textUsed, domUsed := 0, 0
	for index, node := range matches {
		if textUsed >= maxBytes && (!includeDOM || domUsed >= maxBytes) {
			result.Truncated = true
			break
		}
		if index > 0 {
			if includeDOM && domUsed < maxBytes {
				dom.WriteByte('\n')
				domUsed++
			}
		}
		text := nodeText(string(content), node)
		remainingText := maxBytes - textUsed
		text, textTruncated := boundUTF8(text, remainingText)
		if textTruncated {
			result.Truncated = true
		}
		textUsed += len(text)
		match := ReadMatch{Selector: selector, Text: text}
		if includeDOM {
			raw := nodeRaw(string(content), node)
			remainingDOM := maxBytes - domUsed
			raw, rawTruncated := boundUTF8(raw, remainingDOM)
			if rawTruncated {
				result.Truncated = true
			}
			match.DOM = raw
			dom.WriteString(raw)
			domUsed += len(raw)
		}
		result.Matches = append(result.Matches, match)
	}
	result.DOM, _ = boundUTF8(dom.String(), maxBytes)
	_ = root
	return result, nil
}

type selectorPart struct {
	tag     string
	id      string
	classes []string
}

func parseSelector(selector string) ([]selectorPart, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil, category("invalid_selector", ErrInvalidSelector)
	}
	if len(selector) > MaxSelectorBytes {
		return nil, category("limit_exceeded", ErrLimit)
	}
	if strings.ContainsAny(selector, ",[]>+~:*()[]{}\"'=`/\\") {
		return nil, category("invalid_selector", ErrInvalidSelector)
	}
	raw := strings.Fields(selector)
	if len(raw) == 0 || len(raw) > 8 {
		return nil, category("invalid_selector", ErrInvalidSelector)
	}
	parts := make([]selectorPart, 0, len(raw))
	for _, item := range raw {
		part, err := parseSelectorPart(item)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func parseSelectorPart(value string) (selectorPart, error) {
	part := selectorPart{}
	if value == "" {
		return part, category("invalid_selector", ErrInvalidSelector)
	}
	index := 0
	if value[0] != '#' && value[0] != '.' {
		for index < len(value) && isNameByte(value[index]) {
			index++
		}
		if index == 0 {
			return part, category("invalid_selector", ErrInvalidSelector)
		}
		part.tag = strings.ToLower(value[:index])
	}
	for index < len(value) {
		kind := value[index]
		if kind != '#' && kind != '.' {
			return part, category("invalid_selector", ErrInvalidSelector)
		}
		index++
		start := index
		for index < len(value) && isNameByte(value[index]) {
			index++
		}
		if index == start {
			return part, category("invalid_selector", ErrInvalidSelector)
		}
		name := value[start:index]
		if kind == '#' {
			if part.id != "" {
				return part, category("invalid_selector", ErrInvalidSelector)
			}
			part.id = name
		} else {
			part.classes = append(part.classes, name)
		}
	}
	return part, nil
}

func isNameByte(value byte) bool {
	return (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') || (value >= '0' && value <= '9') || value == '_' || value == '-'
}

func selectorMatches(node *htmlNode, parts []selectorPart) bool {
	if len(parts) == 0 || node == nil {
		return false
	}
	current := node
	for index := len(parts) - 1; index >= 0; index-- {
		if current == nil {
			return false
		}
		if !simpleMatches(current, parts[index]) {
			if index == len(parts)-1 {
				// The right-most selector is the selected element itself;
				// only earlier descendant parts may walk ancestors.
				return false
			}
			for current = current.parent; current != nil && !simpleMatches(current, parts[index]); current = current.parent {
			}
			if current == nil {
				return false
			}
		}
		if index > 0 {
			current = current.parent
		}
	}
	return true
}

func simpleMatches(node *htmlNode, part selectorPart) bool {
	if part.tag != "" && node.tag != part.tag {
		return false
	}
	if part.id != "" && node.id != part.id {
		return false
	}
	for _, class := range part.classes {
		if _, ok := node.classes[class]; !ok {
			return false
		}
	}
	return true
}

func parseHTMLNodes(source string) (*htmlNode, []*htmlNode) {
	root := &htmlNode{tag: "#document", start: 0, end: len(source)}
	stack := []*htmlNode{root}
	all := make([]*htmlNode, 0, 32)
	for offset := 0; offset < len(source); {
		open := strings.IndexByte(source[offset:], '<')
		if open < 0 {
			break
		}
		open += offset
		close := strings.IndexByte(source[open+1:], '>')
		if close < 0 {
			break
		}
		close += open + 1
		token := strings.TrimSpace(source[open+1 : close])
		if strings.HasPrefix(token, "!--") {
			endComment := strings.Index(source[close+1:], "-->")
			if endComment >= 0 {
				offset = close + 1 + endComment + 3
				continue
			}
		}
		if token == "" || token[0] == '!' || token[0] == '?' {
			offset = close + 1
			continue
		}
		if token[0] == '/' {
			name := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(token, "/")))
			fields := strings.Fields(name)
			if len(fields) == 0 {
				offset = close + 1
				continue
			}
			name = fields[0]
			for index := len(stack) - 1; index > 0; index-- {
				if stack[index].tag == name {
					for closeIndex := len(stack) - 1; closeIndex >= index; closeIndex-- {
						stack[closeIndex].end = open
					}
					stack = stack[:index]
					break
				}
			}
			offset = close + 1
			continue
		}
		selfClosing := strings.HasSuffix(token, "/")
		if selfClosing {
			token = strings.TrimSpace(strings.TrimSuffix(token, "/"))
		}
		fields := strings.Fields(token)
		if len(fields) == 0 || !isNameByte(fields[0][0]) {
			offset = close + 1
			continue
		}
		node := &htmlNode{tag: strings.ToLower(fields[0]), start: close + 1, end: len(source), parent: stack[len(stack)-1], classes: make(map[string]struct{})}
		parseAttributes(token[len(fields[0]):], node)
		node.parent.children = append(node.parent.children, node)
		all = append(all, node)
		if !selfClosing && !isVoidTag(node.tag) {
			stack = append(stack, node)
		} else {
			node.end = open
		}
		offset = close + 1
	}
	for _, node := range stack[1:] {
		if node.end == 0 || node.end == len(source) {
			node.end = len(source)
		}
	}
	return root, all
}

func parseAttributes(value string, node *htmlNode) {
	for index := 0; index < len(value); {
		for index < len(value) && unicode.IsSpace(rune(value[index])) {
			index++
		}
		start := index
		for index < len(value) && isNameByte(value[index]) {
			index++
		}
		if index == start {
			index++
			continue
		}
		name := strings.ToLower(value[start:index])
		for index < len(value) && unicode.IsSpace(rune(value[index])) {
			index++
		}
		if index >= len(value) || value[index] != '=' {
			continue
		}
		index++
		for index < len(value) && unicode.IsSpace(rune(value[index])) {
			index++
		}
		if index >= len(value) {
			break
		}
		quote := byte(0)
		if value[index] == '\'' || value[index] == '"' {
			quote = value[index]
			index++
		}
		valueStart := index
		if quote != 0 {
			for index < len(value) && value[index] != quote {
				index++
			}
		} else {
			for index < len(value) && !unicode.IsSpace(rune(value[index])) {
				index++
			}
		}
		attributeValue := html.UnescapeString(value[valueStart:index])
		if quote != 0 && index < len(value) {
			index++
		}
		switch name {
		case "id":
			node.id = attributeValue
		case "class":
			for _, class := range strings.Fields(attributeValue) {
				node.classes[class] = struct{}{}
			}
		}
	}
}

func isVoidTag(tag string) bool {
	switch tag {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	default:
		return false
	}
}

func nodeRaw(source string, node *htmlNode) string {
	if node == nil || node.start < 0 || node.end < node.start || node.end > len(source) {
		return ""
	}
	return source[node.start:node.end]
}

func nodeText(source string, node *htmlNode) string {
	raw := nodeRaw(source, node)
	var out strings.Builder
	inTag := false
	inComment := false
	for index := 0; index < len(raw); index++ {
		if !inTag && raw[index] == '<' {
			if end := strings.IndexByte(raw[index:], '>'); end >= 0 {
				token := strings.TrimSpace(raw[index+1 : index+end])
				name := strings.TrimLeft(token, "/")
				if spaceAfterTag(name) {
					out.WriteByte(' ')
				}
			}
			if strings.HasPrefix(raw[index:], "<!--") {
				inComment = true
			}
			inTag = true
			continue
		}
		if inTag {
			if inComment && strings.HasPrefix(raw[index:], "-->") {
				inComment = false
			}
			if raw[index] == '>' {
				inTag = false
			}
			continue
		}
		out.WriteByte(raw[index])
	}
	value := html.UnescapeString(out.String())
	return strings.Join(strings.Fields(value), " ")
}

func spaceAfterTag(token string) bool {
	if token == "" || token[0] == '!' || token[0] == '?' {
		return false
	}
	name := token
	if index := strings.IndexAny(name, " \t\r\n/"); index >= 0 {
		name = name[:index]
	}
	switch strings.ToLower(name) {
	case "address", "article", "aside", "blockquote", "br", "dd", "div", "dl", "dt", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "li", "main", "nav", "ol", "p", "pre", "section", "table", "td", "th", "tr", "ul":
		return true
	default:
		return false
	}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
