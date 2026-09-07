package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	controller "github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

const (
	projectID = "fixture-project"
	sessionID = "fixture-1"
)

type fixtureAgent struct {
	release         chan struct{}
	releaseOnce     sync.Once
	modeMu          sync.Mutex
	mode            string
	quality         string
	root            string
	historyRounds   int
	historyInterval time.Duration
}

type rpcWriter struct {
	connection *websocket.Conn
	mu         sync.Mutex
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	fixtureBase := os.Getenv("PIXIE_UI_FIXTURE_BASE")
	if fixtureBase == "" {
		fixtureBase = "/tmp/pixie-ui"
	}
	readyFile := os.Getenv("PIXIE_UI_FIXTURE_READY_FILE")
	if readyFile == "" {
		readyFile = "/tmp/pixie-ui-ready"
	}
	if !filepath.IsAbs(fixtureBase) || !filepath.IsAbs(readyFile) {
		return fmt.Errorf("UI fixture paths must be absolute")
	}
	_ = os.Remove(readyFile)
	root := filepath.Join(filepath.Clean(fixtureBase), "project")
	data := filepath.Join(filepath.Clean(fixtureBase), "state")
	if err := seedProject(root, data); err != nil {
		return err
	}
	policy, err := workspace.NewPathPolicy([]string{root}, false)
	if err != nil {
		return err
	}
	agent := &fixtureAgent{release: make(chan struct{}), mode: "ask", quality: "medium", root: root}
	if configured := os.Getenv("PIXIE_UI_HISTORY_ROUNDS"); configured != "" {
		count, err := strconv.Atoi(configured)
		if err != nil || count < 1 || count > 5000 {
			return fmt.Errorf("PIXIE_UI_HISTORY_ROUNDS must be between 1 and 5000")
		}
		agent.historyRounds = count
		if configured := os.Getenv("PIXIE_UI_HISTORY_INTERVAL_MS"); configured != "" {
			interval, err := strconv.Atoi(configured)
			if err != nil || interval < 0 || interval > 100 {
				return fmt.Errorf("PIXIE_UI_HISTORY_INTERVAL_MS must be between 0 and 100")
			}
			agent.historyInterval = time.Duration(interval) * time.Millisecond
		}
	}
	agentServer, agentURL, err := agent.start()
	if err != nil {
		return err
	}
	port := 7312
	if configured := os.Getenv("PIXIE_UI_FIXTURE_PORT"); configured != "" {
		parsed, parseErr := strconv.Atoi(configured)
		if parseErr != nil || parsed < 1024 || parsed > 65535 {
			return fmt.Errorf("invalid PIXIE_UI_FIXTURE_PORT")
		}
		port = parsed
	}
	staticDir := os.Getenv("PIXIE_UI_STATIC_DIR")
	if staticDir == "" {
		staticDir = "/app/web"
	}
	runtime, err := controller.NewRuntime(controller.RuntimeConfig{
		Host:       "127.0.0.1",
		Port:       port,
		DataDir:    data,
		StaticDir:  staticDir,
		AppVersion: "ui-acceptance",
		PiURL:      agentURL,
		Policy:     policy,
		Getenv:     os.Getenv,
	})
	if err != nil {
		_ = agentServer.Close()
		return err
	}
	endpoint, err := runtime.Start()
	if err != nil {
		_ = agentServer.Close()
		return err
	}
	if err := os.WriteFile(readyFile, []byte(endpoint+"\n"), 0o600); err != nil {
		_ = agentServer.Close()
		return err
	}
	fmt.Printf("UI fixture ready at %s\n", endpoint)

	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGUSR1)
	defer signal.Stop(signals)
	for {
		select {
		case received := <-signals:
			if received == syscall.SIGUSR1 {
				agent.releaseOnce.Do(func() { close(agent.release) })
				continue
			}
			return shutdown(runtime, agentServer)
		case serveErr := <-runtime.Errors():
			if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				return serveErr
			}
			return nil
		}
	}
}

func shutdown(runtime *controller.Runtime, agent *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return errors.Join(runtime.Shutdown(ctx), agent.Shutdown(ctx))
}

func seedProject(root, data string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(data, 0o700); err != nil {
		return err
	}
	files := map[string][]byte{
		"README.md":   []byte("# UI acceptance\n\nWelcome to acceptance.\n"),
		"dirty.go":    []byte("package fixture\n\nconst Current = \"dirty working tree\"\n"),
		"fixture.png": fixturePNG(),
		"history.txt": []byte("before\n"),
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), contents, 0o600); err != nil {
			return err
		}
	}
	if err := git(root, nil, "init", "-b", "main"); err != nil {
		return err
	}
	if err := git(root, nil, "config", "user.email", "fixture@pixie.invalid"); err != nil {
		return err
	}
	if err := git(root, nil, "config", "user.name", "Pixie fixture"); err != nil {
		return err
	}
	if err := git(root, nil, "add", "."); err != nil {
		return err
	}
	firstDate := []string{"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z"}
	if err := git(root, firstDate, "commit", "-m", "Add fixture"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "history.txt"), []byte("after\n"), 0o600); err != nil {
		return err
	}
	if err := git(root, nil, "add", "history.txt"); err != nil {
		return err
	}
	secondDate := []string{"GIT_AUTHOR_DATE=2026-01-02T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-02T00:00:00Z"}
	if err := git(root, secondDate, "commit", "-m", "Change fixture"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "dirty.go"), []byte("package fixture\n\nconst Current = \"uncommitted change\"\n"), 0o600); err != nil {
		return err
	}
	projects := []workspace.Project{{
		ID: projectID, Name: "Fixture", Roots: []string{root}, Slug: "fixture", LastOpened: 1,
	}}
	encoded, err := json.MarshalIndent(projects, "", "\t")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(data, "projects.json"), append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	records := controller.NewSessionRecords(persist.Store{Dir: data})
	return records.Record(controller.ProjectSessionRecord{ProjectID: projectID, SessionID: sessionID, CWD: root})
}

func git(directory string, extraEnvironment []string, arguments ...string) error {
	command := exec.Command("git", arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), extraEnvironment...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func fixturePNG() []byte {
	canvas := image.NewRGBA(image.Rect(0, 0, 160, 96))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.RGBA{R: 246, G: 247, B: 244, A: 255}), image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(76, 8, 84, 28), image.NewUniform(color.RGBA{R: 39, G: 92, B: 45, A: 255}), image.Point{}, draw.Src)
	for y := 16; y < 90; y++ {
		for x := 42; x < 118; x++ {
			dx, dy := x-80, y-54
			if dx*dx+dy*dy > 36*36 {
				continue
			}
			shade := color.RGBA{R: 106, G: 166, B: 91, A: 255}
			if y > 60 {
				shade = color.RGBA{R: 73, G: 135, B: 69, A: 255}
			}
			if hx, hy := x-67, y-40; hx*hx+hy*hy < 7*7 {
				shade = color.RGBA{R: 220, G: 238, B: 211, A: 255}
			}
			canvas.SetRGBA(x, y, shade)
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		panic(err)
	}
	return encoded.Bytes()
}

func (a *fixtureAgent) start() (*http.Server, string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	server := &http.Server{Handler: http.HandlerFunc(a.serveHTTP), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	return server, "ws://" + listener.Addr().String() + "/pi", nil
}

func (a *fixtureAgent) serveHTTP(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/pi" {
		http.NotFound(response, request)
		return
	}
	connection, err := websocket.Accept(response, request, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	writer := &rpcWriter{connection: connection}
	for {
		_, payload, err := connection.Read(request.Context())
		if err != nil {
			return
		}
		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if json.Unmarshal(payload, &rpc) != nil {
			return
		}
		result := any(map[string]any{})
		switch rpc.Method {
		case "pi.providers.list":
			result = map[string]any{"entries": fixtureProviders()}
		case "pi.providers.canonical-model-info":
			result = map[string]any{"modelInfo": nil}
		case "pi.providers.config.read":
			result = map[string]any{"fields": []any{}}
		case "provider.loginStart":
			result = map[string]any{"loginId": rpc.Params["loginId"], "frame": map[string]any{"kind": "progress", "message": "Starting authentication"}}
		case "provider.loginBegin":
			if writer.write(map[string]any{"method": "provider.login", "params": map[string]any{"loginId": rpc.Params["loginId"], "frame": map[string]any{"kind": "prompt", "message": "Enter API key", "secret": true}}}) != nil {
				return
			}
		case "provider.loginReply":
			if writer.write(map[string]any{"method": "provider.login", "params": map[string]any{"loginId": rpc.Params["loginId"], "frame": map[string]any{"kind": "success"}}}) != nil {
				return
			}
		case "provider.loginCancel":
		case "pi.providers.inventory.refresh":
			result = map[string]any{"started": []any{}, "skipped": []any{}}
		case "pi.preferences.read":
			result = map[string]any{"values": []any{map[string]any{"key": "compactionReserveTokens", "value": 16384}, map[string]any{"key": "piThinkingEffort", "value": "medium"}}}
		case "pi.defaults.read":
			result = map[string]any{"providerId": "anthropic", "modelId": "claude-sonnet-4-5"}
		case "pi.config.extensions.list":
			result = map[string]any{"extensions": []any{}, "warnings": []any{}}
		case "pi.session.extensions.list":
			result = map[string]any{"extensions": []any{}}
		case "pi.sources.list":
			result = map[string]any{"sources": []any{}}
		case "pi.session.info":
			result = map[string]any{"sessionId": sessionID, "title": "Fixture chat"}

		case "runtime.hello":
			result = map[string]any{"protocolVersion": 1, "runtimeId": "ui-fixture", "version": "0.85.1", "capabilities": map[string]int{"sessions": 1, "providers": 1, "mcp": 1, "agents": 1, "plans": 1}}
		case "session.list":
			result = map[string]any{"sessions": []any{map[string]any{
				"sessionId": sessionID,
				"cwd":       a.root,
				"title":     "Fixture chat",
				"updatedAt": "2026-01-02T00:00:00Z",
			}}}
		case "session.create":
			result = map[string]any{"sessionId": "new-fixture", "configOptions": a.configOptions()}
		case "session.load":
			id, _ := rpc.Params["sessionId"].(string)
			if id == "" {
				id = sessionID
			}
			for round := range a.historyRounds {
				if a.historyInterval > 0 {
					select {
					case <-time.After(a.historyInterval):
					case <-request.Context().Done():
						return
					}
				}
				for _, update := range []map[string]any{
					{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": fmt.Sprintf("History prompt %05d", round)}},
					{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": fmt.Sprintf("History answer %05d\n\nA **formatted** reply with a [reference](https://example.com), inline `code` and several paragraphs.\n\n- Inspect the workspace\n- Preserve the conversation\n- Verify the result\n\n```typescript\nconst round = %d;\n```", round, round)}},
				} {
					if writer.write(map[string]any{"jsonrpc": "2.0", "method": "session.update", "params": map[string]any{"sessionId": id, "update": update}}) != nil {
						return
					}
				}
			}
			for _, update := range []map[string]any{
				{"sessionUpdate": "plan", "entries": []any{
					map[string]any{"content": "Inspect the workspace", "priority": "high", "status": "completed"},
					map[string]any{"content": "Finish the reply", "priority": "medium", "status": "in_progress"},
				}},
				{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "image", "data": base64.StdEncoding.EncodeToString(fixturePNG()), "mimeType": "image/png"}},
				{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "Show the fixture"}},
				{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "Loaded answer"}},
			} {
				if writer.write(map[string]any{"jsonrpc": "2.0", "method": "session.update", "params": map[string]any{"sessionId": id, "update": update}}) != nil {
					return
				}
			}
			result = map[string]any{"configOptions": a.configOptions()}
		case "session.prompt":
			id, _ := rpc.Params["sessionId"].(string)
			if id == "" {
				id = sessionID
			}
			if writer.write(map[string]any{"jsonrpc": "2.0", "method": "session.update", "params": map[string]any{"sessionId": id, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "Partial reply "}}}}) != nil {
				return
			}
			responseID := append(json.RawMessage(nil), rpc.ID...)
			go func() {
				select {
				case <-a.release:
				case <-request.Context().Done():
					return
				}
				if writer.write(map[string]any{"jsonrpc": "2.0", "method": "session.update", "params": map[string]any{"sessionId": id, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "complete."}}}}) != nil {
					return
				}
				_ = writer.write(map[string]any{"jsonrpc": "2.0", "id": responseID, "result": map[string]any{"stopReason": "end_turn"}})
			}()
			continue
		case "session.configure":
			if rpc.Params["configId"] == "thinking" && (rpc.Params["value"] == "medium" || rpc.Params["value"] == "low") {
				a.modeMu.Lock()
				a.quality = rpc.Params["value"].(string)
				a.modeMu.Unlock()
			}
			result = map[string]any{"configOptions": a.configOptions()}
		case "session.setMode":
			id, _ := rpc.Params["sessionId"].(string)
			modeID, _ := rpc.Params["modeId"].(string)
			if id == "" {
				id = sessionID
			}
			if !a.setMode(modeID) {
				if len(rpc.ID) > 0 {
					_ = writer.write(map[string]any{
						"jsonrpc": "2.0",
						"id":      rpc.ID,
						"error":   map[string]any{"code": -32602, "message": "unknown fixture mode"},
					})
				}
				continue
			}
			if writer.write(map[string]any{
				"jsonrpc": "2.0",
				"method":  "session.update",
				"params": map[string]any{
					"sessionId": id,
					"update": map[string]any{
						"sessionUpdate": "current_mode_update",
						"currentModeId": modeID,
					},
				},
			}); err != nil {
				return
			}
		case "session.cancel":
			a.releaseOnce.Do(func() { close(a.release) })
		}
		if len(rpc.ID) > 0 && writer.write(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result}) != nil {
			return
		}
	}
}

func (a *fixtureAgent) sessionModes() map[string]any {
	a.modeMu.Lock()
	defer a.modeMu.Unlock()
	return map[string]any{
		"currentModeId": a.mode,
		"availableModes": []any{
			map[string]any{"id": "ask", "name": "Ask", "description": "Discuss before changing files"},
			map[string]any{"id": "code", "name": "Code", "description": "Work directly in the project"},
		},
	}
}

func (a *fixtureAgent) setMode(modeID string) bool {
	if modeID != "ask" && modeID != "code" {
		return false
	}
	a.modeMu.Lock()
	a.mode = modeID
	a.modeMu.Unlock()
	return true
}

func (w *rpcWriter) write(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.connection.Write(context.Background(), websocket.MessageText, payload)
}

func fixtureProviders() []any {
	model := func(id string) any {
		return map[string]any{"id": id, "name": id, "contextLimit": 200000, "reasoning": true}
	}
	entry := func(id, name string, visible bool, models []any) map[string]any {
		if id == "anthropic" {
			return map[string]any{"providerId": id, "providerName": name, "configured": true, "available": true, "visibleInSetup": visible, "models": models, "configKeys": []any{map[string]any{"name": "ANTHROPIC_API_KEY", "required": true, "primary": true, "secret": true}}}
		}
		return map[string]any{"providerId": id, "providerName": name, "configured": true, "available": true, "visibleInSetup": visible, "readinessCheck": false, "models": models, "configKeys": []any{map[string]any{"name": "COMMAND", "required": true, "primary": true, "secret": false, "default": "command"}}}
	}
	return []any{
		entry("claude-code", "Claude Code CLI", false, []any{model("sonnet")}),
		entry("cursor-agent", "Cursor Agent", true, []any{model("auto")}),
		entry("gemini-cli", "Gemini CLI", false, []any{model("gemini-2.5-pro")}),
		entry("atomic_chat", "Atomic Chat", true, []any{}),
		entry("anthropic", "Anthropic", true, []any{model("claude-sonnet-4-5"), model("claude-opus-long-model-name-with-extra-identifiers-for-responsive-layout-review")}),
		map[string]any{"providerId": "openai", "providerName": "OpenAI", "configured": false, "available": false, "visibleInSetup": true, "models": []any{model("gpt-5")}, "configKeys": []any{map[string]any{"name": "OPENAI_API_KEY", "required": true, "primary": true, "secret": true}}},
	}
}

func fixtureIdentity() map[string]any {
	if os.Getenv("PIXIE_UI_FIXTURE_PI") == "1" {
		return map[string]any{"name": "pi", "version": "1.49.0"}
	}
	return map[string]any{"name": "fixture-agent", "version": "1.0.0"}
}

func (a *fixtureAgent) configOptions() []any {
	a.modeMu.Lock()
	defer a.modeMu.Unlock()
	return []any{
		map[string]any{"id": "provider", "name": "Provider", "category": "provider", "type": "select", "currentValue": "anthropic", "options": []any{map[string]any{"value": "anthropic", "name": "Anthropic"}}},
		map[string]any{"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": "claude-sonnet-4-5", "options": []any{map[string]any{"value": "claude-sonnet-4-5", "name": "Claude Sonnet"}}},
		map[string]any{"id": "thinking", "name": "Thinking", "category": "thinking", "type": "select", "currentValue": a.quality, "options": []any{map[string]any{"value": "medium", "name": "Medium"}, map[string]any{"value": "low", "name": "Low"}}},
	}
}
