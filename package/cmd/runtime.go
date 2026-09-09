package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	controller "github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/diagnostics"
)

var version = "0.0.0-dev"
var revision = "unknown"

type runMode string

const (
	modeFullHost   runMode = "full-host"
	modeController runMode = "controller"
)

func parseMode(args []string) (runMode, error) {
	mode := modeFullHost
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
		case modeFullHost, modeController:
			mode = runMode(argument)
		default:
			return "", fmt.Errorf("unsupported serve mode %q", argument)
		}
	}
	return mode, nil
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

// runController is shared by the full-host and controller-only entrypoints.
// The controller owns its runtime; only the full-host composition supplies a
// PiURL from the public assistant facade before calling serveController.
func runController(ctx context.Context, build diagnostics.BuildInfo) error {
	if err := rejectControllerAssistantSettings(os.LookupEnv); err != nil {
		return err
	}
	// Container defaults apply when unset, so plain `go build` binaries keep
	// working outside Docker by pointing these at local directories.
	runtime, err := controller.NewRuntime(controller.RuntimeConfig{AppVersion: build.Version, AppRevision: build.Revision, DataDir: os.Getenv("PIXIE_DATA_DIR"), StaticDir: os.Getenv("PIXIE_STATIC_DIR"), Port: controllerPort()})
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
func rejectControllerAssistantSettings(lookup func(string) (string, bool)) error {
	for _, key := range []string{"PI_CODING_AGENT_DIR", "PIXIE_PI_EXECUTABLE", "PIXIE_PI_ARGS", "PIXIE_LLAMA", "LLAMA_BASE_URL"} {
		if value, ok := lookup(key); ok && strings.TrimSpace(value) != "" {
			return fmt.Errorf("controller-only mode rejects local assistant setting %s", key)
		}
	}
	return nil
}

func fatal(err error) {
	slog.Error("application failed", "error", err)
	os.Exit(1)
}
