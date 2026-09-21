package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// runtimeConfigFile is the controller's complete JSON configuration surface.
// Local assistant settings belong to the host or pairing configuration.
type runtimeConfigFile struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	DataDir   string `json:"dataDir"`
	StaticDir string `json:"staticDir"`
	Mode      string `json:"mode"`
}

func readRuntimeConfig(path string) (runtimeConfigFile, error) {
	var config runtimeConfigFile
	fields, err := loadJSONConfig(path, &config)
	if err != nil {
		return runtimeConfigFile{}, err
	}
	if _, present := fields["mode"]; present && config.Mode != string(modeController) {
		return runtimeConfigFile{}, fmt.Errorf("unsupported config mode %q", config.Mode)
	}
	if _, present := fields["port"]; present && (config.Port < 1 || config.Port > 65535) {
		return runtimeConfigFile{}, errors.New("config port must be a port 1-65535")
	}
	config.DataDir = expandHomePath(config.DataDir)
	config.StaticDir = expandHomePath(config.StaticDir)
	return config, nil
}

// loadJSONConfig shares file selection and shape validation, while each caller
// owns its allowed fields and semantic checks. An omitted file selects defaults.
func loadJSONConfig(path string, target any) (map[string]struct{}, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) {
		return nil, errors.New("--config must be an absolute path")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return decodeJSONConfig(content, target)
}

// Go's struct decoder accepts case variants, null scalars and duplicate keys.
// Validate exact keys and presence before decoding into either CLI config shape.
func decodeJSONConfig(content []byte, target any) (map[string]struct{}, error) {
	targetType := reflect.TypeOf(target)
	if targetType == nil || targetType.Kind() != reflect.Pointer || targetType.Elem().Kind() != reflect.Struct {
		return nil, errors.New("config decoder target must be a struct pointer")
	}
	allowed := make(map[string]struct{}, targetType.Elem().NumField())
	for index := 0; index < targetType.Elem().NumField(); index++ {
		name, _, _ := strings.Cut(targetType.Elem().Field(index).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			allowed[name] = struct{}{}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, errors.New("config must be a JSON object")
	}
	present := make(map[string]struct{}, len(allowed))
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("decode config: %w", err)
		}
		name, ok := key.(string)
		if !ok {
			return nil, errors.New("config object field name must be a string")
		}
		if _, duplicate := present[name]; duplicate {
			return nil, fmt.Errorf("config field %q appears more than once", name)
		}
		if _, ok := allowed[name]; !ok {
			return nil, fmt.Errorf("decode config: unknown field %q", name)
		}
		present[name] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("decode config: %w", err)
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("config field %q must not be null", name)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("config must contain one JSON object")
	}
	if err := json.Unmarshal(content, target); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	return present, nil
}
