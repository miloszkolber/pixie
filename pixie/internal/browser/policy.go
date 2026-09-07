package browser

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxWaitMS  = 30_000
	maxArgs    = 64
	maxArgSize = 16 * 1024
)

var (
	sessionPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,38}$`)
	imageNamePattern  = regexp.MustCompile(`(?i)^[A-Za-z0-9][A-Za-z0-9._-]{0,126}\.(png|jpe?g|webp)$`)
	positiveIntegerRE = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)
	integerRE         = regexp.MustCompile(`^[0-9]+$`)
	urlSchemeRE       = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://`)
)

type policyError struct {
	code, message, hint string
}

func (e *policyError) Error() string { return e.message }

func reject(message string, hints ...string) error {
	hint := "use only the documented bounded browser operations"
	if len(hints) > 0 {
		hint = hints[0]
	}
	return &policyError{code: "invalid_request", message: message, hint: hint}
}

type browserRequest struct {
	panelLease, expireLease bool
	Session                 string
	Command                 string
	Args                    []string
	Positionals             []positional
}

type positional struct {
	value string
	index int
}

var allowedCommands = set(
	"open", "back", "forward", "reload", "close",
	"click", "dblclick", "fill", "type", "hover", "focus", "check", "uncheck", "select", "press", "scroll", "scrollintoview", "wait",
	"read", "snapshot", "screenshot", "get", "is", "a11y", "vitals", "set",
)

var commandOptions = map[string]map[string]bool{
	"read":       set("--json", "--timeout"),
	"wait":       set("--json", "--timeout"),
	"snapshot":   set("--json", "-i", "--interactive", "-c", "--compact", "--depth", "--selector"),
	"screenshot": set("--json", "--annotate"),
	"a11y":       set("--json", "--selector", "--tags"),
	"scroll":     set("--json", "--selector"),
}

var commandOptionOrder = map[string][]string{
	"read":       {"--json", "--timeout"},
	"wait":       {"--json", "--timeout"},
	"snapshot":   {"--json", "-i", "--interactive", "-c", "--compact", "--depth", "--selector"},
	"screenshot": {"--json", "--annotate"},
	"a11y":       {"--json", "--selector", "--tags"},
	"scroll":     {"--json", "--selector"},
}

var valueOptions = set("--timeout", "--selector", "--depth", "--tags")
var booleanOptions = set("--json", "-i", "-c", "--interactive", "--compact", "--annotate")

func set(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func isPositiveInteger(value string, maximum int) bool {
	if !positiveIntegerRE.MatchString(value) {
		return false
	}
	parsed, err := strconv.Atoi(value)
	return err == nil && parsed <= maximum
}

func safeHTTPURL(value string) bool {
	if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
		return false
	}
	if strings.IndexFunc(value, func(r rune) bool { return r <= 0x20 || r == 0x7f }) >= 0 {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != "" && parsed.User == nil
}

func rejectExecutableScheme(value string) error {
	lowered := strings.ToLower(value)
	for _, scheme := range []string{"file:", "data:", "javascript:", "about:", "chrome:", "chrome-extension:"} {
		if strings.HasPrefix(lowered, scheme) {
			return reject("local or executable URL schemes are not permitted", "use only plain http:// or https:// URLs")
		}
	}
	if urlSchemeRE.MatchString(value) && !safeHTTPURL(value) {
		return reject("URLs must be http(s) without embedded credentials", "remove credentials and use a plain http(s) URL")
	}
	return nil
}

func allowedFor(command, option string) bool {
	options, found := commandOptions[command]
	if !found {
		return option == "--json"
	}
	return options[option]
}

func validateOption(command, option, value string) error {
	if !allowedFor(command, option) {
		values := commandOptionOrder[command]
		if values == nil {
			values = []string{"--json"}
		}
		return reject(fmt.Sprintf("option is not permitted for %s: %s", command, option), fmt.Sprintf("permitted options: %s", strings.Join(values, " ")))
	}
	if option == "--timeout" && !isPositiveInteger(value, maxWaitMS) {
		return reject(fmt.Sprintf("--timeout must be between 1 and %d milliseconds", maxWaitMS))
	}
	if option == "--depth" && !isPositiveInteger(value, 100) {
		return reject("--depth must be a whole number from 1 through 100")
	}
	return nil
}

func parseArguments(command string, raw []any) ([]string, []positional, error) {
	if len(raw) > maxArgs {
		return nil, nil, reject(fmt.Sprintf("too many arguments; maximum is %d", maxArgs))
	}
	args := make([]string, len(raw))
	for i, rawValue := range raw {
		value, ok := rawValue.(string)
		if !ok {
			return nil, nil, reject("every browser argument must be a string")
		}
		if len(value) > maxArgSize {
			return nil, nil, reject("browser argument is too large")
		}
		if strings.IndexByte(value, 0) >= 0 {
			return nil, nil, reject("browser arguments must not contain NUL bytes")
		}
		if !utf8.ValidString(value) {
			return nil, nil, reject("browser arguments must be valid UTF-8")
		}
		if err := rejectExecutableScheme(value); err != nil {
			return nil, nil, err
		}
		args[i] = value
	}

	positionals := make([]positional, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if booleanOptions[arg] {
			if err := validateOption(command, arg, ""); err != nil {
				return nil, nil, err
			}
			continue
		}
		if valueOptions[arg] {
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") {
				return nil, nil, reject(arg+" requires a value", "provide a value immediately after "+arg)
			}
			if err := validateOption(command, arg, args[index+1]); err != nil {
				return nil, nil, err
			}
			index++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return nil, nil, reject("option is not permitted: " + strings.SplitN(arg, "=", 2)[0])
		}
		positionals = append(positionals, positional{value: arg, index: index})
	}
	return args, positionals, nil
}

func requireArity(command string, values []string, minimum, maximum int) error {
	if len(values) >= minimum && len(values) <= maximum {
		return nil
	}
	expected := fmt.Sprintf("between %d and %d", minimum, maximum)
	if minimum == maximum {
		expected = fmt.Sprintf("exactly %d", minimum)
	}
	suffix := "s"
	if maximum == 1 {
		suffix = ""
	}
	return reject(fmt.Sprintf("%s requires %s positional argument%s", command, expected, suffix))
}

func validatePositionals(command string, positionals []positional) error {
	values := make([]string, len(positionals))
	for i, entry := range positionals {
		values[i] = entry.value
	}
	navigation := set("open", "back", "forward", "reload")
	if navigation[command] {
		if command == "open" && (len(values) != 1 || !safeHTTPURL(values[0])) {
			return reject(command + " requires exactly one http(s) URL without credentials")
		}
		if command != "open" {
			if err := requireArity(command, values, 0, 0); err != nil {
				return err
			}
		}
	}
	if command == "close" {
		if err := requireArity(command, values, 0, 0); err != nil {
			return err
		}
	}
	if command == "read" || command == "a11y" || command == "vitals" {
		if len(values) > 1 || (len(values) == 1 && !safeHTTPURL(values[0])) {
			return reject(command + " accepts at most one plain http(s) URL")
		}
	}
	if set("click", "dblclick", "focus", "hover", "check", "uncheck", "scrollintoview")[command] {
		if err := requireArity(command, values, 1, 1); err != nil {
			return err
		}
	}
	if set("fill", "type", "select")[command] {
		if err := requireArity(command, values, 2, 2); err != nil {
			return err
		}
	}
	if command == "press" {
		if err := requireArity(command, values, 1, 1); err != nil {
			return err
		}
	}
	if command == "scroll" {
		if err := requireArity(command, values, 1, 2); err != nil {
			return err
		}
		if values[0] != "up" && values[0] != "down" && values[0] != "left" && values[0] != "right" {
			return reject("scroll direction must be up, down, left, or right")
		}
		if len(values) == 2 && !isPositiveInteger(values[1], 10_000) {
			return reject("scroll distance must be a whole number from 1 through 10000")
		}
	}
	if command == "snapshot" {
		if err := requireArity(command, values, 0, 0); err != nil {
			return err
		}
	}
	if command == "get" {
		kinds := set("text", "html", "value", "attr", "title", "url", "count", "box", "styles")
		if len(values) < 1 || !kinds[values[0]] {
			return reject("get requires a permitted information type")
		}
		if values[0] == "url" || values[0] == "title" {
			return requireArity(command, values, 1, 1)
		}
		if values[0] == "attr" {
			return requireArity(command, values, 3, 3)
		}
		return requireArity(command, values, 2, 2)
	}
	if command == "is" {
		if len(values) != 2 || !set("visible", "enabled", "checked")[values[0]] {
			return reject("is requires one permitted state check and one selector")
		}
	}
	if command == "wait" {
		if len(values) != 1 {
			return reject("wait requires one selector or bounded millisecond value")
		}
		if integerRE.MatchString(values[0]) && !isPositiveInteger(values[0], maxWaitMS) {
			return reject(fmt.Sprintf("wait must be between 1 and %d milliseconds", maxWaitMS))
		}
	}
	if command == "set" {
		if len(values) != 3 || values[0] != "viewport" || !isPositiveInteger(values[1], 1920) || !isPositiveInteger(values[2], 1200) {
			return reject("set permits only viewport WIDTH HEIGHT within 320-1920 by 240-1200")
		}
		width, _ := strconv.Atoi(values[1])
		height, _ := strconv.Atoi(values[2])
		if width < 320 || height < 240 {
			return reject("set permits only viewport WIDTH HEIGHT within 320-1920 by 240-1200")
		}
	}
	if command == "screenshot" {
		if len(values) != 1 {
			return reject("screenshot requires one new output filename")
		}
		if !imageNamePattern.MatchString(values[0]) {
			return reject("screenshot output must be a simple png, jpg, jpeg, or webp filename")
		}
	}
	return nil
}

func validateBrowserRequest(body any) (browserRequest, error) {
	object, ok := body.(map[string]any)
	if !ok {
		return browserRequest{}, reject("request body must be an object")
	}
	for key := range object {
		if key != "session" && key != "command" && key != "args" {
			return browserRequest{}, reject("request contains unknown fields")
		}
	}
	session, sessionOK := object["session"].(string)
	command, commandOK := object["command"].(string)
	if !sessionOK || !sessionPattern.MatchString(session) {
		return browserRequest{}, reject("session must contain 1-38 letters, digits, underscores, or hyphens")
	}
	if !commandOK || !allowedCommands[command] {
		return browserRequest{}, reject(fmt.Sprintf("unsupported browser command: %v", object["command"]))
	}
	var rawArgs []any
	if raw, found := object["args"]; found {
		if raw == nil {
			return validateBrowserRequest(map[string]any{"session": session, "command": command})
		}
		var argsOK bool
		rawArgs, argsOK = raw.([]any)
		if !argsOK {
			return browserRequest{}, reject("args must be an array")
		}
	}
	args, positionals, err := parseArguments(command, rawArgs)
	if err != nil {
		return browserRequest{}, err
	}
	if err := validatePositionals(command, positionals); err != nil {
		return browserRequest{}, err
	}
	return browserRequest{Session: session, Command: command, Args: args, Positionals: positionals}, nil
}

func screenshotFilename(request browserRequest) string {
	if request.Command == "screenshot" && len(request.Positionals) == 1 {
		return request.Positionals[0].value
	}
	return ""
}
