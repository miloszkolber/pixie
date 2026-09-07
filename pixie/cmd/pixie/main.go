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

	controller "github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/diagnostics"
)

var version = "0.0.0-dev"
var revision = "unknown"

func main() {
	build := diagnostics.NormalizeBuild(version, revision)
	slog.SetDefault(diagnostics.NewLogger("pixie", build))
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		host := "127.0.0.1"
		if configured := strings.TrimSpace(os.Getenv("PIXIE_CONTROLLER_HOST")); configured == "::" || configured == "::1" {
			host = "[::1]"
		}
		client := http.Client{Timeout: 3 * time.Second}
		response, err := client.Get(fmt.Sprintf("http://%s:%d/livez", host, controller.DefaultControllerPort))
		if err != nil {
			fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			fatal(fmt.Errorf("application healthcheck returned %d", response.StatusCode))
		}
		return
	}
	stop, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := run(stop, build); err != nil {
		fatal(err)
	}
}

func run(ctx context.Context, build diagnostics.BuildInfo) error {
	runtime, err := controller.NewRuntime(controller.RuntimeConfig{AppVersion: build.Version, AppRevision: build.Revision})
	if err != nil {
		return err
	}
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
