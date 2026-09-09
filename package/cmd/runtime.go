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
	for index := 1; index < len(args); index++ {
		argument := args[index]
		if argument == "--mode" {
			if index+1 >= len(args) {
				return "", errors.New("--mode requires a value")
			}
			index++
			argument = args[index]
		} else if strings.HasPrefix(argument, "--mode=") {
			argument = strings.TrimPrefix(argument, "--mode=")
		} else {
			continue
		}
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
	shutdownContext, release := context.WithTimeout(context.Background(), 15*time.Second)
	defer release()
	return errors.Join(err, runtime.Shutdown(shutdownContext))
}

func fatal(err error) {
	slog.Error("application failed", "error", err)
	os.Exit(1)
}
