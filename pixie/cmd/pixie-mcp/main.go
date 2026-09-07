package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/mcphost"
)

var (
	version  = "0.0.0-dev"
	revision = "unknown"
)

func main() {
	build := diagnostics.NormalizeBuild(version, revision)
	logger := diagnostics.NewLogger("mcp", build)
	slog.SetDefault(logger)
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		endpoint, err := mcphost.HealthURL(os.LookupEnv)
		if err != nil {
			fatal(logger, err)
		}
		response, err := (&http.Client{Timeout: 3 * time.Second}).Get(endpoint)
		if err != nil {
			fatal(logger, err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			fatal(logger, fmt.Errorf("MCP host healthcheck returned %d", response.StatusCode))
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, build, logger); err != nil {
		fatal(logger, err)
	}
}

func run(ctx context.Context, build diagnostics.BuildInfo, logger *slog.Logger) error {
	config, err := mcphost.ConfigFromEnvironment(os.LookupEnv)
	if err != nil {
		return err
	}
	service, err := mcphost.NewService(config, build, logger)
	if err != nil {
		return err
	}
	server := service.HTTPServer()
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		service.Shutdown()
		return err
	}
	errorsCh := make(chan error, 1)
	go func() { errorsCh <- server.Serve(listener) }()
	logger.Info("MCP host listening", "address", listener.Addr().String(), "modules", config.Modules)
	select {
	case err = <-errorsCh:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	case <-ctx.Done():
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownContext)
	service.Shutdown()
	return errors.Join(err, shutdownErr)
}

func fatal(logger *slog.Logger, err error) {
	logger.Error("MCP host failed", "error", err)
	os.Exit(1)
}
