package controller

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/miloszkolber/pixie/internal/diagnostics"
	"github.com/miloszkolber/pixie/internal/mcpserver"
)

func TestBrowserStatusUsesTheMergedPublisherWithoutAnExternalURL(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "browser.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	agentBrowser, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := mcpserver.NewRegistry(mcpserver.Config{
		Host: "127.0.0.1", Port: DefaultControllerPort, DataDir: dataDir,
		Binaries: &mcpserver.BinaryConfig{
			AgentBrowser: agentBrowser, BrowserConfig: configPath,
			ArtifactRoot: filepath.Join(root, "artifacts"), StateRoot: filepath.Join(root, "state"),
		},
	}, diagnostics.NormalizeBuild("test", "test"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Shutdown()

	provider := &runtimeStatusProvider{registry: registry, auth: AuthConfig{ControllerPort: DefaultControllerPort}}
	status := provider.browserStatus(context.Background())
	if status.State != "ready" {
		t.Fatalf("merged Browser status = %#v", status)
	}
}
