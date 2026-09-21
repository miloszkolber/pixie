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
	identity := diagnostics.ResolveProcessRunIdentity(os.LookupEnv, time.Now())
	diagnostics.SetProcessRunIdentity(identity)
	slog.SetDefault(diagnostics.NewLogger(runtimeCLIName, build))
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Printf("%s %s (revision %s)\n", runtimeCLIName, build.Version, build.Revision)
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
		if err := runUtilityCommand(os.Args[1], configPath(os.Args[1:]), os.Stdout); err != nil {
			fatal(err)
		}
		return
	}
	if handled, err := handlePairingCommand(os.Args[1:], os.Stdout); handled {
		if err != nil {
			fatal(err)
		}
		return
	}
	mode, err := parseMode(os.Args[1:])
	if err != nil {
		fatal(err)
	}
	if mode != modeController {
		fatal(fmt.Errorf("unsupported serve mode %q", mode))
	}
	configFile := configPath(os.Args[1:])
	stop, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := runControllerWithConfig(stop, build, configFile); err != nil {
		fatal(err)
	}
}
