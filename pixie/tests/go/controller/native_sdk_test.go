package controller_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApplicationThroughNativePiHost(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("native Pi integration requires Bun")
		}
		t.Skip("Bun is needed for native integration")
	}
	for _, profile := range []string{"vanilla", "optional", "project"} {
		t.Run(profile, func(t *testing.T) {
			root := t.TempDir()
			script, _ := filepath.Abs("../../pi-host/native-host-fixture.ts")
			agentDir := t.TempDir()
			cmd := exec.CommandContext(t.Context(), bun, script, agentDir, root, profile)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = os.Stderr
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
			ready := make(chan string, 1)
			go func() {
				scan := bufio.NewScanner(stdout)
				for scan.Scan() {
					if strings.HasPrefix(scan.Text(), "{\"url\"") {
						ready <- scan.Text()
						return
					}
				}
				ready <- ""
			}()
			var info struct {
				URL       string `json:"url"`
				SessionID string `json:"sessionId"`
			}
			select {
			case line := <-ready:
				if json.Unmarshal([]byte(line), &info) != nil {
					t.Fatal("native host failed to start")
				}
			case <-time.After(20 * time.Second):
				t.Fatal("native host startup timed out")
			}
			policy, _ := workspace.NewPathPolicy([]string{root}, false)
			listener, _ := net.Listen("tcp", "127.0.0.1:0")
			port := listener.Addr().(*net.TCPAddr).Port
			_ = listener.Close()
			data := t.TempDir()
			runtime, err := controller.NewRuntime(controller.RuntimeConfig{Host: "127.0.0.1", Port: port, DataDir: data, StaticDir: t.TempDir(), PiURL: info.URL, Policy: policy, Getenv: func(key string) string {
				if key == "PIXIE_PI_SECRET_KEY" {
					return "native-fixture-secret"
				}
				return ""
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Shutdown(context.Background())
			host, err := runtime.Start()
			if err != nil {
				t.Fatal(err)
			}
			ws := dialRuntimeSocket(t, t.Context(), host, "native-client")
			project := callBrowser(t, ws, "project", "project.open", map[string]any{"path": root})["result"].(map[string]any)
			if err := controller.NewSessionRecords(persist.Store{Dir: data}).Record(controller.ProjectSessionRecord{ProjectID: project["id"].(string), SessionID: info.SessionID, CWD: root}); err != nil {
				t.Fatal(err)
			}
			owner := map[string]any{"projectId": project["id"], "root": root, "sessionId": info.SessionID}
			callBrowser(t, ws, "list", "session.list", map[string]any{"projectId": project["id"]})
			snapshot := callBrowser(t, ws, "history", "session.getMessages", owner)
			raw, _ := json.Marshal(snapshot)
			if snapshot["ok"] != true || !strings.Contains(string(raw), "Native summary retained") || strings.Contains(string(raw), "Hidden native entry") {
				t.Fatalf("native history projection: %s", raw)
			}
			capabilities := callBrowser(t, ws, "caps", "pi.capabilities", map[string]any{})["result"].(map[string]any)
			if (capabilities["agents"] == float64(1)) != (profile == "optional") {
				t.Fatalf("wrong capability scope: %#v", capabilities)
			}
			scoped := callBrowser(t, ws, "project-caps", "pi.capabilities", map[string]any{"projectId": project["id"], "root": root})["result"].(map[string]any)
			if (scoped["agents"] == float64(1)) != (profile != "vanilla") {
				t.Fatalf("project capability missing: %#v", scoped)
			}
			if profile == "optional" {
				created := callBrowser(t, ws, "agent", "pi.agentCreate", map[string]any{"name": "Reviewer", "description": "Review", "instructions": "Inspect", "scope": "global", "modelId": "fixture/echo"})
				if created["ok"] != true {
					t.Fatalf("agent create: %#v", created)
				}
				agent := created["result"].(map[string]any)
				updated := callBrowser(t, ws, "edit", "pi.agentUpdate", map[string]any{"id": agent["id"], "name": "Reviewer", "description": "Edited", "instructions": "Inspect carefully"})
				if updated["ok"] != true || updated["result"].(map[string]any)["modelId"] != "fixture/echo" {
					t.Fatalf("model lost across native boundary: %#v", updated)
				}
				if err := os.WriteFile(filepath.Join(agentDir, "agents", "broken.md"), []byte("---\nbad: [\n---\n"), 0600); err != nil {
					t.Fatal(err)
				}
				catalog := callBrowser(t, ws, "catalog", "pi.agentList", map[string]any{})
				result := catalog["result"].(map[string]any)
				if len(result["agents"].([]any)) != 1 || len(result["warnings"].([]any)) != 1 {
					t.Fatalf("catalog did not retain diagnostics and valid agents: %#v", catalog)
				}
			}
			sent := callBrowser(t, ws, "prompt", "session.prompt", map[string]any{"sessionId": info.SessionID, "text": "Hello"})
			if sent["ok"] != true {
				t.Fatalf("native send: %#v", sent)
			}
			deadline := time.Now().Add(5 * time.Second)
			for attempt := 0; ; attempt++ {
				got := callBrowser(t, ws, fmt.Sprintf("reload-%d", attempt), "session.getMessages", owner)
				raw, _ := json.Marshal(got)
				if strings.Contains(string(raw), "Hello from Pi") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("native reply absent: %s", raw)
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}
