// Command browser-worker runs the untrusted Browser service behind a separate
// service identity. It serves GET /isolation (a fact report about its own
// containment) and POST /run (the bounded browser operation surface), and
// forwards the service's other bounded routes; it has no arbitrary command
// interface. A controller verifies the report before it unlocks the Browser
// module in package/internal/mcpserver.
//
// The process is expected to run under the dedicated systemd profile in
// package/systemd/pixie-browser-worker.service. Running it with the
// controller's uid deliberately fails boundary verification.
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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/miloszkolber/pixie/internal/browser"
	"github.com/miloszkolber/pixie/internal/diagnostics"
)

var version = "0.0.0-dev"
var revision = "unknown"

const (
	defaultWorkerAddress = "127.0.0.1:8788"
	readHeaderTimeout    = 10 * time.Second
	idleTimeout          = 2 * time.Minute
	shutdownTimeout      = 25 * time.Second
)

func main() {
	build := diagnostics.NormalizeBuild(version, revision)
	logger := diagnostics.NewLogger("browser-worker", build)
	slog.SetDefault(logger)
	if err := run(logger, build); err != nil {
		logger.Error("browser worker failed", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, build diagnostics.BuildInfo) error {
	address := strings.TrimSpace(os.Getenv("PIXIE_BROWSER_WORKER_ADDR"))
	if address == "" {
		address = defaultWorkerAddress
	}
	if err := validateLoopbackAddress(address); err != nil {
		return err
	}
	token := strings.TrimSpace(os.Getenv("PIXIE_BROWSER_WORKER_TOKEN"))
	config, err := browser.ConfigFromEnvironment(os.LookupEnv)
	if err != nil {
		return err
	}
	// Bind the wrapped service's own Host/Origin authority to the worker
	// listener so its existing /mcp and /status checks match the address the
	// controller probes.
	if host, portText, splitErr := net.SplitHostPort(address); splitErr == nil {
		config.Host = host
		if port, portErr := strconv.Atoi(portText); portErr == nil {
			config.Port = port
		}
	}
	// The worker bearer covers the whole worker API. Reusing it as the
	// service token keeps /run's own authorization consistent with
	// /isolation instead of introducing a second credential.
	config.Authentication = token != ""
	config.Token = token
	if root := strings.TrimSpace(os.Getenv("PIXIE_BROWSER_ARTIFACT_ROOT")); root != "" {
		config.ArtifactRoot = root
	}
	if root := strings.TrimSpace(os.Getenv("PIXIE_BROWSER_STATE_ROOT")); root != "" {
		config.StateRoot = root
	}
	service, err := browser.NewService(config, build, logger)
	if err != nil {
		return err
	}
	defer service.Shutdown()
	worker, err := browser.NewWorker(service, build.Version, token)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: worker, ReadHeaderTimeout: readHeaderTimeout, IdleTimeout: idleTimeout}
	stop, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	failures := make(chan error, 1)
	go func() { failures <- server.Serve(listener) }()
	logger.Info("browser worker listening", "address", listener.Addr().String())
	select {
	case err := <-failures:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-stop.Done():
	}
	// Stop accepting work, cancel active browser commands and then drain the
	// HTTP handlers within one bounded window.
	shutdownContext, release := context.WithTimeout(context.Background(), shutdownTimeout)
	defer release()
	service.Shutdown()
	if err := server.Shutdown(shutdownContext); err != nil {
		return err
	}
	return nil
}

func validateLoopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("PIXIE_BROWSER_WORKER_ADDR must be host:port: %w", err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("PIXIE_BROWSER_WORKER_ADDR must bind a loopback address, got %q", host)
}
