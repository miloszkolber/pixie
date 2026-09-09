package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/miloszkolber/pixie/assistant/host"
)

var version = "0.0.0-dev"
var revision = "unknown"

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Printf("pixie-assistant %s (revision %s)\n", version, revision)
		return
	}

	assistant, err := host.Start(context.Background(), host.Config{
		Host:     "127.0.0.1",
		Port:     3284,
		Secret:   os.Getenv("PIXIE_PI_SECRET_KEY"),
		AgentDir: os.Getenv("PI_CODING_AGENT_DIR"),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "pixie-assistant: %v\n", err)
		os.Exit(1)
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
		fmt.Fprintf(os.Stderr, "pixie-assistant shutdown: %v\n", err)
		os.Exit(1)
	}
}
