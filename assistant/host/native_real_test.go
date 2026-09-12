package host

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This opt-in/installed-binary harness exercises only session ownership. It
// never submits a model prompt and all Pi state is redirected to temporary
// directories. PIXIE_TEST_PI_EXECUTABLE can select an exact pinned binary.
func TestInstalledOfficialPiSessionOwnership(t *testing.T) {
	executable := os.Getenv("PIXIE_TEST_PI_EXECUTABLE")
	if executable == "" {
		var err error
		executable, err = exec.LookPath("pi")
		if err != nil {
			t.Skip("official Pi executable is not installed")
		}
	}
	if !filepath.IsAbs(executable) {
		resolved, err := exec.LookPath(executable)
		if err != nil {
			t.Skipf("selected Pi executable is unavailable: %v", err)
		}
		executable = resolved
	}
	versionCtx, stopVersion := context.WithTimeout(t.Context(), 5*time.Second)
	versionOutput, versionErr := exec.CommandContext(versionCtx, executable, "--version").CombinedOutput()
	stopVersion()
	if versionErr != nil || !strings.Contains(string(versionOutput), "0.85.1") {
		t.Skipf("installed Pi is not the pinned official 0.85.1 executable: %s (%v)", versionOutput, versionErr)
	}
	agentDir, cwd := t.TempDir(), t.TempDir()
	supervisor := newNativeSupervisor(Config{PiExecutable: executable, AgentDir: agentDir})
	if err := supervisor.start(t.Context()); err != nil {
		t.Fatalf("start official Pi supervisor: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = supervisor.close(ctx)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	raw, err := supervisor.callHost(ctx, "session.create", map[string]any{"cwd": cwd, "mcpServers": []any{}})
	if err != nil {
		t.Fatalf("bind fresh official Pi launch: %v", err)
	}
	var created struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(raw, &created) != nil || created.SessionID == "" {
		t.Fatalf("invalid official Pi create snapshot: %s", raw)
	}
	child := supervisor.testChild(created.SessionID)
	if child == nil || child.nextID == 0 || child.nextID > 9_007_199_254_740_991 {
		t.Fatalf("official Pi requests did not use bounded numeric IDs: %#v", child)
	}
	ref, _ := supervisor.testSession(created.SessionID)
	if !filepath.IsAbs(ref.Path) || ref.CWD != cwd || !ref.Unmaterialized {
		t.Fatalf("official Pi identity was not exact: %#v", ref)
	}
	if _, err := supervisor.callHost(ctx, "session.release", map[string]any{"sessionId": created.SessionID, "cwd": cwd}); err == nil || !strings.Contains(err.Error(), "transcript validation") {
		t.Fatalf("unmaterialized official Pi release error = %v", err)
	}
	currentRef, _ := supervisor.testSession(created.SessionID)
	if supervisor.testChild(created.SessionID) != child || currentRef != ref || !ref.Unmaterialized {
		t.Fatal("rejected release did not retain the unmaterialized resident child")
	}
	if _, err := os.Lstat(ref.Path); !os.IsNotExist(err) {
		t.Fatalf("fresh official Pi session unexpectedly materialized: %v", err)
	}
	if err := supervisor.close(ctx); err != nil {
		t.Fatalf("stop initial official Pi supervisor: %v", err)
	}
	restarted := newNativeSupervisor(Config{PiExecutable: executable, AgentDir: agentDir})
	if err := restarted.start(ctx); err != nil {
		t.Fatalf("unmaterialized session blocked supervisor restart: %v", err)
	}
	t.Cleanup(func() {
		closeCtx, stopClose := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopClose()
		_ = restarted.close(closeCtx)
	})
	if _, exists := restarted.testSession(created.SessionID); exists {
		t.Fatal("restart retained the missing unmaterialized registry entry")
	}
	registryRaw, err := os.ReadFile(restarted.registryPath())
	if err != nil {
		t.Fatalf("read pruned official Pi registry: %v", err)
	}
	if strings.Contains(string(registryRaw), created.SessionID) || strings.Contains(string(registryRaw), ref.Path) {
		t.Fatalf("pruned official Pi identity remains in registry: %s", registryRaw)
	}
	if _, err := restarted.callHost(ctx, "session.load", map[string]any{"sessionId": created.SessionID, "cwd": cwd}); err == nil || !strings.Contains(err.Error(), "unknown native session") {
		t.Fatalf("load of pruned official Pi session did not fail explicitly: %v", err)
	}
}

// TestInstalledOfficialPiPromptSettles is an opt-in live-provider check. It
// requires PIXIE_TEST_PI_PROMPT=1 and an absolute PIXIE_TEST_PI_AGENT_DIR that
// holds provider credentials. It never touches the default agent directory.
func TestInstalledOfficialPiPromptSettles(t *testing.T) {
	if os.Getenv("PIXIE_TEST_PI_PROMPT") != "1" {
		t.Skip("set PIXIE_TEST_PI_PROMPT=1 to run the live provider prompt")
	}
	agentDir := os.Getenv("PIXIE_TEST_PI_AGENT_DIR")
	if agentDir == "" || !filepath.IsAbs(agentDir) {
		t.Skip("set PIXIE_TEST_PI_AGENT_DIR to an absolute agent directory with credentials")
	}
	executable := os.Getenv("PIXIE_TEST_PI_EXECUTABLE")
	if executable == "" {
		resolved, err := exec.LookPath("pi")
		if err != nil {
			t.Skipf("official Pi executable is unavailable: %v", err)
		}
		executable = resolved
	}
	versionCtx, stopVersion := context.WithTimeout(t.Context(), 5*time.Second)
	versionOutput, versionErr := exec.CommandContext(versionCtx, executable, "--version").CombinedOutput()
	stopVersion()
	if versionErr != nil || !strings.Contains(string(versionOutput), "0.85.1") {
		t.Skipf("installed Pi is not the pinned 0.85.1 executable: %s (%v)", versionOutput, versionErr)
	}
	cwd := t.TempDir()
	supervisor := newNativeSupervisor(Config{PiExecutable: executable, AgentDir: agentDir})
	if err := supervisor.start(t.Context()); err != nil {
		t.Fatalf("start official Pi supervisor: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = supervisor.close(ctx)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	raw, err := supervisor.callHost(ctx, "session.create", map[string]any{"cwd": cwd, "mcpServers": []any{}})
	if err != nil {
		t.Fatalf("bind fresh official Pi launch: %v", err)
	}
	var created struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(raw, &created) != nil || created.SessionID == "" {
		t.Fatalf("invalid official Pi create snapshot: %s", raw)
	}
	promptRaw, err := supervisor.callHost(ctx, "session.prompt", map[string]any{"sessionId": created.SessionID, "content": []any{map[string]any{"type": "text", "text": "Reply with exactly the word pong and nothing else."}}})
	if err != nil {
		t.Fatalf("official Pi prompt did not settle: %v", err)
	}
	var result struct {
		StopReason string `json:"stopReason"`
	}
	if json.Unmarshal(promptRaw, &result) != nil {
		t.Fatalf("invalid official Pi prompt result: %s", promptRaw)
	}
	if result.StopReason == "" || result.StopReason == "error" || result.StopReason == "aborted" {
		t.Fatalf("official Pi prompt settled as %q: %s", result.StopReason, promptRaw)
	}
	loadRaw, err := supervisor.callHost(ctx, "session.load", map[string]any{"sessionId": created.SessionID, "cwd": cwd})
	if err != nil {
		t.Fatalf("reload prompt session: %v", err)
	}
	if !strings.Contains(string(loadRaw), "\"assistant\"") {
		t.Fatalf("prompt transcript did not contain an assistant message: %s", loadRaw)
	}
}
