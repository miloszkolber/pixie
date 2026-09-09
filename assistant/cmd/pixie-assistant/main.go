package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/miloszkolber/pixie/assistant/host"
)

var version = "0.0.0-dev"
var revision = "unknown"

type assistantConfig struct {
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Secret       string `json:"secret"`
	AgentDir     string `json:"agentDir"`
	PiExecutable string `json:"piExecutable"`
}

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Printf("pixie-assistant %s (revision %s)\n", version, revision)
		return
	}
	command, configPath, err := parseArgs(os.Args[1:])
	if err != nil {
		fail(err)
	}
	fileConfig, err := readConfig(configPath)
	if err != nil {
		fail(err)
	}
	if command == "doctor" {
		fmt.Printf("pixie-assistant doctor: config=%s\n", configPath)
		return
	}
	if command == "uninstall" {
		// Uninstall is deliberately a plan-only operation. Removing a user unit
		// or native Pi state requires the operator's explicit service-manager
		// action and is never performed by a diagnostic binary.
		fmt.Println("pixie-assistant uninstall: stop and remove the selected user unit and binary")
		return
	}

	assistant, err := host.Start(context.Background(), host.Config{
		Host:         firstNonEmpty(os.Getenv("PIXIE_ASSISTANT_HOST"), fileConfig.Host, "127.0.0.1"),
		Port:         assistantPort(fileConfig.Port),
		Secret:       firstNonEmpty(os.Getenv("PIXIE_PI_SECRET_KEY"), fileConfig.Secret),
		AgentDir:     expandHomePath(firstNonEmpty(os.Getenv("PI_CODING_AGENT_DIR"), fileConfig.AgentDir)),
		PiExecutable: expandHomePath(firstNonEmpty(os.Getenv("PIXIE_PI_EXECUTABLE"), fileConfig.PiExecutable)),
	})
	if err != nil {
		fail(err)
	}
	stop, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	select {
	case err := <-assistant.Errors():
		if err != nil {
			fmt.Fprintf(os.Stderr, "pixie-assistant: %v\n", err)
			os.Exit(1)
		}
	case <-stop.Done():
	}
	shutdown, release := context.WithTimeout(context.Background(), 25*time.Second)
	defer release()
	if err := assistant.Close(shutdown); err != nil {
		fail(fmt.Errorf("shutdown: %w", err))
	}
}

func readConfig(path string) (assistantConfig, error) {
	if strings.TrimSpace(path) == "" {
		return assistantConfig{}, nil
	}
	if !filepath.IsAbs(path) {
		return assistantConfig{}, fmt.Errorf("--config must be an absolute path")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return assistantConfig{}, fmt.Errorf("read config: %w", err)
	}
	var config assistantConfig
	if err := json.Unmarshal(content, &config); err != nil {
		return assistantConfig{}, fmt.Errorf("decode config: %w", err)
	}
	return config, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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

func assistantPort(configured int) int {
	if configured != 0 {
		return configured
	}
	if raw := strings.TrimSpace(os.Getenv("PIXIE_ASSISTANT_PORT")); raw != "" {
		port, err := strconv.Atoi(raw)
		if err == nil {
			return port
		}
	}
	return 3284
}

func parseArgs(args []string) (command, configPath string, err error) {
	command = "serve"
	configPath = ""
	commandSeen := false
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "serve" || argument == "doctor" || argument == "uninstall":
			if commandSeen {
				return "", "", fmt.Errorf("multiple commands supplied")
			}
			command = argument
			commandSeen = true
		case argument == "--config":
			if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
				return "", "", fmt.Errorf("--config requires a value")
			}
			index++
			configPath = args[index]
		case strings.HasPrefix(argument, "--config="):
			configPath = strings.TrimPrefix(argument, "--config=")
			if strings.TrimSpace(configPath) == "" {
				return "", "", fmt.Errorf("--config requires a value")
			}
		default:
			return "", "", fmt.Errorf("unknown argument %q", argument)
		}
	}
	return command, configPath, nil
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "pixie-assistant: %v\n", err)
	os.Exit(1)
}
