//go:build !controller

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	assistantHost "github.com/miloszkolber/pixie/assistant/host"
	controller "github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/diagnostics"
)

func main() {
	build := diagnostics.NormalizeBuild(version, revision)
	slog.SetDefault(diagnostics.NewLogger("pixie", build))
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Printf("pixie %s (revision %s)\n", build.Version, build.Revision)
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		host := "127.0.0.1"
		if configured := strings.TrimSpace(os.Getenv("PIXIE_CONTROLLER_HOST")); configured == "::" || configured == "::1" {
			host = "[::1]"
		}
		client := http.Client{Timeout: 3 * time.Second}
		response, err := client.Get(fmt.Sprintf("http://%s:%d/livez", host, controllerPort()))
		if err != nil {
			fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			fatal(fmt.Errorf("application healthcheck returned %d", response.StatusCode))
		}
		return
	}
	mode, err := parseMode(os.Args[1:])
	if err != nil {
		fatal(err)
	}
	stop, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := run(stop, build, mode); err != nil {
		fatal(err)
	}
}

func run(ctx context.Context, build diagnostics.BuildInfo, mode runMode) error {
	if mode == modeFullHost {
		return runFullHost(ctx, build)
	}
	return runController(ctx, build)
}

func runFullHost(ctx context.Context, build diagnostics.BuildInfo) error {
	// Full-host composition obtains the assistant through the public facade.
	// The facade owns the engine lifecycle and private transport; the
	// controller never reaches into assistant internals.
	assistant, err := assistantHost.Start(ctx, assistantHost.Config{
		Host:     "127.0.0.1",
		Port:     0,
		AgentDir: os.Getenv("PI_CODING_AGENT_DIR"),
	})
	if err != nil {
		return fmt.Errorf("start embedded assistant: %w", err)
	}
	defer func() { _ = assistant.Close(context.Background()) }()
	if assistant.Endpoint() == "" {
		return errors.New("embedded assistant did not provide a private transport endpoint")
	}
	runtime, err := controller.NewRuntime(controller.RuntimeConfig{
		AppVersion:  build.Version,
		AppRevision: build.Revision,
		DataDir:     os.Getenv("PIXIE_DATA_DIR"),
		StaticDir:   os.Getenv("PIXIE_STATIC_DIR"),
		Port:        controllerPort(),
		PiURL:       assistant.Endpoint(),
	})
	if err != nil {
		return err
	}
	return serveController(ctx, runtime)
}
