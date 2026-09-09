//go:build controller

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

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
		runHealthcheck()
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
	if mode != modeController {
		fatal(fmt.Errorf("controller-only build does not support %q mode", mode))
	}
	configFile := configPath(os.Args[1:])
	if _, err := runtimeConfigFor(configFile, mode); err != nil {
		fatal(err)
	}
	stop, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := runControllerWithConfig(stop, build, configFile); err != nil {
		fatal(err)
	}
}

func runHealthcheck() {
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
}
