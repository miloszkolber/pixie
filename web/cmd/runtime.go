package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	controller "github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/diagnostics"
)

var version = "0.0.0-dev"
var revision = "unknown"

const runtimeCLIName = "pixie_web"

type runMode string

const (
	modeController runMode = "controller"
)

type runtimeConfigFile struct {
	Host         string `json:"host"`
	Port         int    `json:"port"`
	DataDir      string `json:"dataDir"`
	StaticDir    string `json:"staticDir"`
	Mode         string `json:"mode"`
	AgentDir     string `json:"agentDir"`
	PiExecutable string `json:"piExecutable"`
}

// AgentDir stays for the pairing ceremony identity resolution and so
// controller mode can reject it explicitly. PiExecutable stays only so
// controller mode can reject it explicitly; the controller never starts a
// local Pi. The former full-host allowSelfRestart and admin-bridge options
// (adminBridge, piPackage, adminBridgeBun, adminBridgeScript) are removed.

// restartExitCode matches RestartForceExitStatus in the packaged systemd
// units. It is only ever produced by an accepted runtime.restart.
const restartExitCode = 75

// errRestartRequested is the composition sentinel for an accepted reload.
var errRestartRequested = errors.New("restart requested")

func parseMode(args []string) (runMode, error) {
	mode := modeController
	if len(args) == 0 {
		return mode, nil
	}
	if args[0] != "serve" {
		return "", fmt.Errorf("unknown command %q; use `serve`", args[0])
	}
	modeConfigured := false
	for index := 1; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "--mode":
			if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
				return "", errors.New("--mode requires a value")
			}
			index++
			argument = args[index]
		case strings.HasPrefix(argument, "--mode="):
			argument = strings.TrimPrefix(argument, "--mode=")
			if strings.TrimSpace(argument) == "" {
				return "", errors.New("--mode requires a value")
			}
		case argument == "--config":
			if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
				return "", errors.New("--config requires a value")
			}
			index++
			continue
		case strings.HasPrefix(argument, "--config="):
			if strings.TrimSpace(strings.TrimPrefix(argument, "--config=")) == "" {
				return "", errors.New("--config requires a value")
			}
			continue
		default:
			return "", fmt.Errorf("unknown serve argument %q", argument)
		}
		if modeConfigured {
			return "", errors.New("--mode may only be specified once")
		}
		modeConfigured = true
		switch runMode(argument) {
		case modeController:
			mode = runMode(argument)
		default:
			return "", fmt.Errorf("unsupported serve mode %q", argument)
		}
	}
	return mode, nil
}

func configPath(args []string) string {
	for index := 1; index < len(args); index++ {
		switch {
		case args[index] == "--config" && index+1 < len(args):
			return strings.TrimSpace(args[index+1])
		case strings.HasPrefix(args[index], "--config="):
			return strings.TrimSpace(strings.TrimPrefix(args[index], "--config="))
		}
	}
	return ""
}

func readRuntimeConfig(path string, mode runMode) (runtimeConfigFile, error) {
	config, err := decodeRuntimeConfig(path)
	if err != nil {
		return runtimeConfigFile{}, err
	}
	if config.Mode != "" && config.Mode != string(modeController) {
		return runtimeConfigFile{}, fmt.Errorf("unsupported config mode %q", config.Mode)
	}
	if config.Mode != "" && runMode(config.Mode) != mode {
		return runtimeConfigFile{}, fmt.Errorf("config mode %q does not match requested %q mode", config.Mode, mode)
	}
	return config, nil
}

// decodeRuntimeConfig reads and decodes a Pixie JSON configuration without
// binding it to a serve mode. The mode-agnostic utility commands (pairing
// ceremonies) need the shared dataDir/agentDir resolution without selecting a
// topology.
func decodeRuntimeConfig(path string) (runtimeConfigFile, error) {
	if strings.TrimSpace(path) == "" {
		return runtimeConfigFile{}, nil
	}
	if !filepath.IsAbs(path) {
		return runtimeConfigFile{}, errors.New("--config must be an absolute path")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return runtimeConfigFile{}, fmt.Errorf("read config: %w", err)
	}
	var config runtimeConfigFile
	if err := json.Unmarshal(content, &config); err != nil {
		return runtimeConfigFile{}, fmt.Errorf("decode config: %w", err)
	}
	config.DataDir = expandHomePath(config.DataDir)
	config.StaticDir = expandHomePath(config.StaticDir)
	config.AgentDir = expandHomePath(config.AgentDir)
	config.PiExecutable = expandHomePath(config.PiExecutable)
	return config, nil
}

func runtimeConfigFor(path string, mode runMode) (runtimeConfigFile, error) {
	return readRuntimeConfig(path, mode)
}

func runUtilityCommand(command, path string, stdout io.Writer) error {
	if path != "" {
		if !filepath.IsAbs(path) {
			return errors.New("--config must be an absolute path")
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read config: %w", err)
		}
		var value map[string]any
		if err := json.Unmarshal(content, &value); err != nil || value == nil {
			return errors.New("config must be a JSON object")
		}
	}
	if command == "doctor" {
		_, err := fmt.Fprintf(stdout, "%s doctor: configuration is readable (%s)\n", runtimeCLIName, path)
		return err
	}
	// Uninstall is intentionally non-destructive. The package provides the
	// binary/unit/configuration evidence and leaves native Pi state untouched;
	// operators stop and remove the selected unit explicitly.
	_, err := fmt.Fprintf(stdout, "%s uninstall: stop and remove the selected user unit and binary\n", runtimeCLIName)
	return err
}

// controllerPort reads PIXIE_CONTROLLER_PORT with the compiled default.
// UI, API and the in-process MCP publisher share one listener, so one port
// covers all three surfaces.
func controllerPort() int {
	raw := strings.TrimSpace(os.Getenv("PIXIE_CONTROLLER_PORT"))
	if raw == "" {
		return controller.DefaultControllerPort
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		fatal(fmt.Errorf("PIXIE_CONTROLLER_PORT must be a port 1-65535, got %q", raw))
	}
	return port
}

// runController starts the controller-only entrypoint. The controller dials
// the separately managed assistant over loopback via PIXIE_PI_PORT/PIXIE_PI_URL;
// it never starts a local Pi.
func runController(ctx context.Context, build diagnostics.BuildInfo) error {
	return runControllerWithConfig(ctx, build, "")
}

func runControllerWithConfig(ctx context.Context, build diagnostics.BuildInfo, configPath string) error {
	if err := rejectControllerAssistantSettings(os.LookupEnv); err != nil {
		return err
	}
	fileConfig, err := runtimeConfigFor(configPath, modeController)
	if err != nil {
		return err
	}
	if err := rejectControllerConfigAssistantSettings(fileConfig); err != nil {
		return err
	}
	// Container defaults apply when unset, so plain `go build` binaries keep
	// working outside Docker by pointing these at local directories.
	host := strings.TrimSpace(os.Getenv("PIXIE_CONTROLLER_HOST"))
	if host == "" {
		host = fileConfig.Host
	}
	port := controllerPort()
	if strings.TrimSpace(os.Getenv("PIXIE_CONTROLLER_PORT")) == "" && fileConfig.Port != 0 {
		port = fileConfig.Port
	}
	dataDir := expandHomePath(os.Getenv("PIXIE_DATA_DIR"))
	if strings.TrimSpace(dataDir) == "" {
		dataDir = fileConfig.DataDir
	}
	staticDir := expandHomePath(os.Getenv("PIXIE_STATIC_DIR"))
	if strings.TrimSpace(staticDir) == "" {
		staticDir = fileConfig.StaticDir
	}
	scheduleRuntime, err := controller.ParseScheduleRuntimePolicy(os.Getenv)
	if err != nil {
		return err
	}
	runtime, err := controller.NewRuntime(controller.RuntimeConfig{Host: host, AppVersion: build.Version, AppRevision: build.Revision, DataDir: dataDir, StaticDir: staticDir, Port: port, ProtocolMode: os.Getenv("PIXIE_PI_PROTOCOL"), ScheduleRuntime: &scheduleRuntime})
	if err != nil {
		return err
	}
	return serveController(ctx, runtime)
}

func serveController(ctx context.Context, runtime *controller.Runtime) error {
	endpoint, err := runtime.Start()
	if err != nil {
		return err
	}
	slog.Info("listening", "address", endpoint)
	select {
	case err = <-runtime.Errors():
	case <-ctx.Done():
	}
	shutdownContext, release := context.WithTimeout(context.Background(), applicationDrainTimeout)
	defer release()
	return errors.Join(err, runtime.Shutdown(shutdownContext))
}

const applicationDrainTimeout = 25 * time.Second

// rejectControllerAssistantSettings keeps controller-only mode from
// accidentally accepting settings that would select or configure a local Pi.
// PIXIE_PI_PORT/PIXIE_PI_URL remain valid: they identify the separately
// managed host service that controller mode is intended to reach.
// PIXIE_PI_PACKAGE and PIXIE_ASSISTANT_* are rejected because a shared
// pixie.env must not silently configure a local assistant the controller
// never starts.
func rejectControllerAssistantSettings(lookup func(string) (string, bool)) error {
	for _, key := range []string{"PI_CODING_AGENT_DIR", "PIXIE_PI_EXECUTABLE", "PIXIE_PI_ARGS", "PIXIE_PI_PACKAGE", "PIXIE_ASSISTANT_PORT", "PIXIE_ASSISTANT_HOST", "PIXIE_LLAMA", "LLAMA_BASE_URL"} {
		if value, ok := lookup(key); ok && strings.TrimSpace(value) != "" {
			return fmt.Errorf("controller-only mode rejects local assistant setting %s", key)
		}
	}
	// A shared pixie.env may export other PIXIE_ASSISTANT_* knobs. lookup
	// cannot enumerate, so use the process environment only to discover
	// candidate keys and let lookup stay authoritative for the value.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "PIXIE_ASSISTANT_") {
			continue
		}
		if key == "PIXIE_ASSISTANT_PORT" || key == "PIXIE_ASSISTANT_HOST" {
			continue
		}
		if value, ok := lookup(key); ok && strings.TrimSpace(value) != "" {
			return fmt.Errorf("controller-only mode rejects local assistant setting %s", key)
		}
	}
	return nil
}

func rejectControllerConfigAssistantSettings(config runtimeConfigFile) error {
	if strings.TrimSpace(config.AgentDir) != "" {
		return fmt.Errorf("controller-only mode rejects local assistant setting agentDir")
	}
	if strings.TrimSpace(config.PiExecutable) != "" {
		return fmt.Errorf("controller-only mode rejects local assistant setting piExecutable")
	}
	return nil
}

func expandHomePath(value string) string {
	value = strings.TrimSpace(value)
	if value != "~" && !strings.HasPrefix(value, "~/") {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return value
	}
	if value == "~" {
		return home
	}
	return filepath.Join(home, strings.TrimPrefix(value, "~/"))
}

func fatal(err error) {
	slog.Error("application failed", "error", err)
	os.Exit(1)
}
