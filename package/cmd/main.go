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
	if len(os.Args) > 1 && (os.Args[1] == "doctor" || os.Args[1] == "uninstall") {
		if err := runUtilityCommand(os.Args[1], configPath(os.Args[1:])); err != nil {
			fatal(err)
		}
		return
	}
	mode, err := parseMode(os.Args[1:])
	if err != nil {
		fatal(err)
	}
	configFile := configPath(os.Args[1:])
	if _, err := runtimeConfigFor(configFile, mode); err != nil {
		fatal(err)
	}
	stop, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := runWithConfig(stop, build, mode, configFile); err != nil {
		if errors.Is(err, errRestartRequested) {
			slog.Info("restart requested by an authorized operation; exiting for the service manager")
			os.Exit(restartExitCode)
		}
		fatal(err)
	}
}

func run(ctx context.Context, build diagnostics.BuildInfo, mode runMode) error {
	return runWithConfig(ctx, build, mode, "")
}

func runWithConfig(ctx context.Context, build diagnostics.BuildInfo, mode runMode, configPath string) error {
	if mode == modeFullHost {
		return runFullHostWithConfig(ctx, build, configPath)
	}
	return runControllerWithConfig(ctx, build, configPath)
}

func runFullHost(ctx context.Context, build diagnostics.BuildInfo) error {
	return runFullHostWithConfig(ctx, build, "")
}

func runFullHostWithConfig(ctx context.Context, build diagnostics.BuildInfo, configPath string) error {
	fileConfig, err := runtimeConfigFor(configPath, modeFullHost)
	if err != nil {
		return err
	}
	if strings.TrimSpace(os.Getenv("PIXIE_PI_URL")) != "" || strings.TrimSpace(os.Getenv("PIXIE_PI_PORT")) != "" {
		return errors.New("full-host mode does not accept an external Pi endpoint; use controller mode")
	}
	agentDir := expandHomePath(os.Getenv("PI_CODING_AGENT_DIR"))
	if agentDir == "" {
		agentDir = fileConfig.AgentDir
	}
	piExecutable := expandHomePath(os.Getenv("PIXIE_PI_EXECUTABLE"))
	if piExecutable == "" {
		piExecutable = fileConfig.PiExecutable
	}
	// Full-host mode must never run without a native supervisor. The packaged
	// config previously selected neither path, which let a full host serve a
	// capability-less controller. Resolve and verify the selection first.
	piExecutable, err = validateFullHostPiSelection(agentDir, piExecutable)
	if err != nil {
		return err
	}
	// Full-host composition obtains the assistant through the public facade.
	// The facade owns the engine lifecycle and private transport; the
	// controller never reaches into assistant internals.
	assistant, err := assistantHost.Start(ctx, assistantHost.Config{
		Host:             "127.0.0.1",
		Port:             0,
		Secret:           os.Getenv("PIXIE_PI_SECRET_KEY"),
		AgentDir:         agentDir,
		PiExecutable:     piExecutable,
		AllowSelfRestart: fileConfig.AllowSelfRestart || selfRestartAllowed(),
	})
	if err != nil {
		return fmt.Errorf("start embedded assistant: %w", err)
	}
	defer func() { _ = assistant.Close(context.Background()) }()
	if assistant.Endpoint() == "" {
		return errors.New("embedded assistant did not provide a private transport endpoint")
	}
	host := strings.TrimSpace(os.Getenv("PIXIE_CONTROLLER_HOST"))
	if host == "" {
		host = fileConfig.Host
	}
	port := controllerPort()
	if strings.TrimSpace(os.Getenv("PIXIE_CONTROLLER_PORT")) == "" && fileConfig.Port != 0 {
		port = fileConfig.Port
	}
	dataDir := os.Getenv("PIXIE_DATA_DIR")
	if strings.TrimSpace(dataDir) == "" {
		dataDir = fileConfig.DataDir
	}
	staticDir := os.Getenv("PIXIE_STATIC_DIR")
	if strings.TrimSpace(staticDir) == "" {
		staticDir = fileConfig.StaticDir
	}
	runtime, err := controller.NewRuntime(controller.RuntimeConfig{
		Host:        host,
		AppVersion:  build.Version,
		AppRevision: build.Revision,
		DataDir:     dataDir,
		StaticDir:   staticDir,
		Port:        port,
		PiURL:       assistant.Endpoint(),
	})
	if err != nil {
		return err
	}
	serveErr := serveFullHost(ctx, runtime, assistant)
	shutdownContext, release := context.WithTimeout(context.Background(), applicationDrainTimeout)
	defer release()
	return errors.Join(serveErr, assistant.Close(shutdownContext))
}

// serveFullHost runs the controller until either side of the composition fails
// or an accepted reload is requested. Joining the assistant error channel is
// what turns a lost native engine into a process restart instead of a
// half-alive host.
func serveFullHost(ctx context.Context, runtime *controller.Runtime, assistant *assistantHost.Handle) error {
	endpoint, err := runtime.Start()
	if err != nil {
		return err
	}
	slog.Info("listening", "address", endpoint)
	result := waitFullHost(ctx, runtime.Errors(), assistant.Errors(), assistant.RestartRequested())
	shutdownContext, release := context.WithTimeout(context.Background(), applicationDrainTimeout)
	defer release()
	shutdownErr := runtime.Shutdown(shutdownContext)
	if errors.Is(result, errRestartRequested) {
		return errors.Join(errRestartRequested, shutdownErr)
	}
	return errors.Join(result, shutdownErr)
}

// waitFullHost selects the first authoritative composition signal. It is
// factored out so the join semantics are testable without a live listener.
func waitFullHost(ctx context.Context, runtimeErr, assistantErr <-chan error, restart <-chan struct{}) error {
	select {
	case err := <-runtimeErr:
		return err
	case err := <-assistantErr:
		if err != nil {
			return fmt.Errorf("assistant engine failed: %w", err)
		}
		return nil
	case <-restart:
		return errRestartRequested
	case <-ctx.Done():
		return nil
	}
}
