// Package assistantconfig reads the private v2 service configuration shared by
// the pixie_cli and pixie_full launchers. It intentionally contains no Pi
// package selector: archive launchers provide that through a private env var.
package assistantconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const SchemaVersion = 2

// Config contains only the v2 startup facts needed by the archive launchers.
type Config struct {
	Host             string
	Port             int
	AgentDir         string
	AllowSelfRestart bool
}

var allowedFields = map[string]struct{}{
	"schemaVersion":    {},
	"host":             {},
	"port":             {},
	"agentDir":         {},
	"allowSelfRestart": {},
}

// Load validates the full v2 object rather than accepting a partial object
// that an older assistant happened to interpret. This keeps an archive
// launcher and its hosted assistant on one selection contract.
func Load(path string) (Config, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return Config{}, errors.New("--config must be an absolute path")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, errors.New("could not read assistant configuration")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return Config{}, errors.New("assistant configuration must be a JSON object")
	}
	if _, selected := object["piPackage"]; selected {
		return Config{}, errors.New("assistant config must not select piPackage; pixie_cli supplies the bundled Pi archive")
	}
	for key := range object {
		if _, allowed := allowedFields[key]; !allowed {
			return Config{}, fmt.Errorf("assistant config has unsupported field %q", key)
		}
	}
	version, err := requiredInteger(object, "schemaVersion")
	if err != nil || version != SchemaVersion {
		if err != nil {
			return Config{}, err
		}
		return Config{}, fmt.Errorf("assistant config schemaVersion must be %d", SchemaVersion)
	}
	host, err := requiredString(object, "host")
	if err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(host) == "" {
		return Config{}, errors.New("assistant config host must not be empty")
	}
	port, err := requiredInteger(object, "port")
	if err != nil || port < 1 || port > 65535 {
		if err != nil {
			return Config{}, err
		}
		return Config{}, errors.New("assistant config port must be a port 1-65535")
	}
	agentDir, err := requiredString(object, "agentDir")
	if err != nil {
		return Config{}, err
	}
	agentDir = strings.TrimSpace(agentDir)
	if !filepath.IsAbs(agentDir) {
		return Config{}, errors.New("assistant config agentDir must be an absolute path")
	}
	allowSelfRestart, err := requiredBoolean(object, "allowSelfRestart")
	if err != nil {
		return Config{}, err
	}
	return Config{
		Host:             host,
		Port:             port,
		AgentDir:         filepath.Clean(agentDir),
		AllowSelfRestart: allowSelfRestart,
	}, nil
}

func requiredString(object map[string]json.RawMessage, key string) (string, error) {
	raw, ok := object[key]
	if !ok {
		return "", fmt.Errorf("assistant config %s is required", key)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("assistant config %s must be a string", key)
	}
	return value, nil
}

func requiredInteger(object map[string]json.RawMessage, key string) (int, error) {
	raw, ok := object[key]
	if !ok {
		return 0, fmt.Errorf("assistant config %s is required", key)
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("assistant config %s must be an integer", key)
	}
	return value, nil
}

func requiredBoolean(object map[string]json.RawMessage, key string) (bool, error) {
	raw, ok := object[key]
	if !ok {
		return false, fmt.Errorf("assistant config %s is required", key)
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("assistant config %s must be a boolean", key)
	}
	return value, nil
}
