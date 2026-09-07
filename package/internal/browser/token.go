package browser

import (
	"fmt"
	"strings"
)

const (
	tokenMinLength = 32
	tokenMaxLength = 256
)

var tokenSentinels = map[string]struct{}{
	"INVALID_REPLACE_WITH_RANDOM_CONTROLLER_TOKEN": {},
	"INVALID_REPLACE_WITH_RANDOM_BROWSER_TOKEN":    {},
	"replace-with-a-random-controller-token":       {},
	"replace-with-a-random-browser-token":          {},
	"replace-with-a-random-token":                  {},
}

func isStrongToken(value string) bool {
	if len(value) < tokenMinLength || len(value) > tokenMaxLength {
		return false
	}
	if _, found := tokenSentinels[value]; found {
		return false
	}
	return strings.IndexFunc(value, func(r rune) bool { return r < '!' || r > '~' }) == -1
}

func assertStrongToken(value string) error {
	if value == "" {
		return fmt.Errorf("PIXIE_BROWSER_TOKEN is required")
	}
	if !isStrongToken(value) {
		return fmt.Errorf("PIXIE_BROWSER_TOKEN must be at least %d printable random-token characters", tokenMinLength)
	}
	return nil
}
