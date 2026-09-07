package mcphost

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/miloszkolber/pixie/internal/browser"
)

const (
	defaultHost = "127.0.0.1"
	defaultPort = 8787
)

// Config describes the Pixie MCP host. BrowserConfig is an
// internal composition detail: the first module reuses the existing Browser
// service without making its environment part of the catalog API.
type Config struct {
	Host            string
	Port            int
	Authentication  bool
	Token           string
	PublicOrigin    string
	Modules         []string
	DisabledModules []string
	BrowserConfig   browser.Config
}

func ConfigFromEnvironment(lookup func(string) (string, bool)) (Config, error) {
	if lookup == nil {
		return Config{}, fmt.Errorf("MCP environment lookup is not configured")
	}
	host := strings.TrimSpace(environmentValue(lookup, "PIXIE_MCP_HOST", defaultHost))
	port, err := positiveInteger(environmentValue(lookup, "PIXIE_MCP_PORT", strconv.Itoa(defaultPort)), "PIXIE_MCP_PORT")
	if err != nil {
		return Config{}, err
	}
	authentication, err := exactBoolean(environmentValue(lookup, "PIXIE_MCP_AUTH", "true"), "PIXIE_MCP_AUTH")
	if err != nil {
		return Config{}, err
	}
	token := strings.TrimSpace(environmentValue(lookup, "PIXIE_MCP_TOKEN", ""))
	if authentication && !strongToken(token) {
		return Config{}, fmt.Errorf("PIXIE_MCP_TOKEN must be a strong printable random token")
	}
	publicOrigin := strings.TrimSpace(environmentValue(lookup, "PIXIE_MCP_PUBLIC_ORIGIN", ""))
	if publicOrigin != "" {
		publicOrigin, err = normalizeOrigin(publicOrigin)
		if err != nil || !authentication {
			return Config{}, fmt.Errorf("PIXIE_MCP_PUBLIC_ORIGIN requires authentication and one absolute http(s) origin without a path")
		}
	}
	if err := validateHost(host, authentication); err != nil {
		return Config{}, err
	}
	modules, err := parseModuleList(environmentValue(lookup, "PIXIE_MCP_MODULES", "browser"), "PIXIE_MCP_MODULES")
	if err != nil {
		return Config{}, err
	}
	disabled, err := parseModuleList(environmentValue(lookup, "PIXIE_MCP_DISABLED_MODULES", ""), "PIXIE_MCP_DISABLED_MODULES")
	if err != nil {
		return Config{}, err
	}
	for _, id := range append(append([]string{}, modules...), disabled...) {
		if _, ok := moduleFactories[id]; !ok {
			return Config{}, fmt.Errorf("unknown Pixie MCP module %q", id)
		}
	}
	active := subtractModules(modules, disabled)
	browserConfig, err := browser.ConfigFromEnvironment(browserLookup(lookup, host, port, authentication, token, publicOrigin))
	if err != nil {
		return Config{}, fmt.Errorf("configure browser module: %w", err)
	}
	browserConfig.Host = host
	browserConfig.Port = port
	browserConfig.Authentication = authentication
	browserConfig.Token = token
	browserConfig.PublicOrigin = publicOrigin
	return Config{
		Host: host, Port: port, Authentication: authentication, Token: token,
		PublicOrigin: publicOrigin, Modules: active, DisabledModules: disabled,
		BrowserConfig: browserConfig,
	}, nil
}

// HealthURL returns the local liveness URL using the same bind settings as
// the host process. It is used by the container healthcheck and does not
// require the MCP token because liveness is intentionally unauthenticated.
func HealthURL(lookup func(string) (string, bool)) (string, error) {
	config, err := ConfigFromEnvironment(lookup)
	if err != nil {
		return "", err
	}
	host := config.Host
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if host == "::" {
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(config.Port)) + "/health", nil
}

func environmentValue(lookup func(string) (string, bool), key, fallback string) string {
	if value, ok := lookup(key); ok {
		return value
	}
	return fallback
}

func browserLookup(lookup func(string) (string, bool), host string, port int, authentication bool, token, publicOrigin string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		if strings.HasPrefix(key, "PIXIE_BROWSER_") {
			suffix := strings.TrimPrefix(key, "PIXIE_BROWSER_")
			if value, ok := lookup("PIXIE_MCP_" + suffix); ok {
				return value, true
			}
			switch suffix {
			case "HOST":
				return host, true
			case "PORT":
				return strconv.Itoa(port), true
			case "AUTH":
				return strconv.FormatBool(authentication), true
			case "TOKEN":
				return token, true
			case "PUBLIC_ORIGIN":
				return publicOrigin, true
			}
		}
		return lookup(key)
	}
}

func positiveInteger(value, name string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 1 || parsed > 65535 {
		return 0, fmt.Errorf("%s must be between 1 and 65535", name)
	}
	return parsed, nil
}

func exactBoolean(value, name string) (bool, error) {
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be exactly true or false", name)
	}
}

func validateHost(host string, authentication bool) error {
	if host == "localhost" {
		return nil
	}
	parsed := net.ParseIP(host)
	if parsed == nil {
		return fmt.Errorf("PIXIE_MCP_HOST must be localhost or an IP address")
	}
	if !parsed.IsLoopback() && !authentication {
		return fmt.Errorf("non-loopback MCP binding requires PIXIE_MCP_AUTH=true")
	}
	return nil
}

func normalizeOrigin(value string) (string, error) {
	if value == "" || len(value) > 512 || strings.TrimSpace(value) != value {
		return "", fmt.Errorf("invalid origin")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid origin")
	}
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if port != "" {
		number, portErr := strconv.Atoi(port)
		if portErr != nil || number < 1 || number > 65535 {
			return "", fmt.Errorf("invalid origin")
		}
	}
	if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return strings.ToLower(parsed.Scheme) + "://" + host, nil
}

func strongToken(value string) bool {
	if len(value) < 32 || len(value) > 256 {
		return false
	}
	for _, character := range []byte(value) {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return !strings.HasPrefix(value, "replace-with-a-random-") && !strings.HasPrefix(value, "INVALID_REPLACE_WITH_RANDOM_")
}

func parseModuleList(value, name string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return []string{}, nil
	}
	result := make([]string, 0)
	seen := make(map[string]bool)
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			return nil, fmt.Errorf("%s contains an empty or duplicate module ID", name)
		}
		seen[item] = true
		result = append(result, item)
	}
	return result, nil
}

func subtractModules(modules, disabled []string) []string {
	blocked := make(map[string]bool, len(disabled))
	for _, id := range disabled {
		blocked[id] = true
	}
	result := make([]string, 0, len(modules))
	for _, id := range modules {
		if !blocked[id] {
			result = append(result, id)
		}
	}
	return result
}
