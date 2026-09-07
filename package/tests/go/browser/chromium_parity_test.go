package browser_test

// Stage G Chromium acceptance behind the Browser MCP surface.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miloszkolber/pixie/internal/browser"
	"github.com/miloszkolber/pixie/internal/diagnostics"
)

type liveFixture struct {
	t       *testing.T
	handler http.Handler
	base    string
	server  *httptest.Server
}

func startLiveFixtures(t *testing.T) *liveFixture {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(response, `<!doctype html><html><head><title>Pixie live home</title></head><body>
<h1>Welcome to Pixie live</h1>
<p class="intro">Acceptance fixture for agent-browser.</p>
<button id="count" onclick="window.__count=(window.__count||0)+1;document.getElementById('count-label').textContent='count '+window.__count">Count</button>
<span id="count-label">count 0</span>
<input id="name" name="name" value="">
<a id="to-form" href="/form">Sign in</a>
</body></html>`)
	})
	mux.HandleFunc("/form", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(response, `<!doctype html><html><head><title>Pixie sign in</title></head><body>
<h1>Sign in</h1>
<form id="login" onsubmit="event.preventDefault();document.getElementById('welcome').textContent='Welcome, '+document.getElementById('user').value">
<label>User <input id="user" name="user" value=""></label>
<label>Password <input id="pass" name="pass" type="password" value=""></label>
<button id="go" type="submit">Sign in</button>
</form>
<p id="welcome"></p>
</body></html>`)
	})
	mux.HandleFunc("/large", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		var page strings.Builder
		page.WriteString(`<!doctype html><html><head><title>Large DOM</title></head><body><h1>Large DOM</h1><table>`)
		for i := 0; i < 2000; i++ {
			fmt.Fprintf(&page, `<tr><td>row-%d</td><td>value-%d</td></tr>`, i, i)
		}
		page.WriteString(`</table></body></html>`)
		_, _ = fmt.Fprint(response, page.String())
	})
	mux.HandleFunc("/scroll", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(response, `<!doctype html><html><head><title>Infinite scroll</title></head><body>
<h1>Feed</h1><div id="feed"></div>
<script>let n=0;function more(){for(let i=0;i<20&&n<100;i++,n++){const d=document.createElement('div');d.style.minHeight='300px';d.textContent='item-'+n;document.getElementById('feed').appendChild(d);}}more();window.addEventListener('scroll',more);</script>
</body></html>`)
	})
	mux.HandleFunc("/frames", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(response, `<!doctype html><html><head><title>Frames</title></head><body>
<h1>Frame host</h1><iframe id="inner-frame" src="/inner" width="400" height="200"></iframe>
</body></html>`)
	})
	mux.HandleFunc("/inner", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(response, `<!doctype html><html><head><title>Inner</title></head><body><p>Inner frame content</p></body></html>`)
	})
	mux.HandleFunc("/js", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(response, `<!doctype html><html><head><title>JS heavy</title></head><body>
<h1>JS counter</h1><p id="value">0</p>
<script>let v=0;setInterval(()=>{v++;document.getElementById('value').textContent=String(v);},100);</script>
</body></html>`)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &liveFixture{t: t, handler: mux, base: server.URL, server: server}
}

type liveRuntime struct {
	service *browser.Service
	config  browser.Config
}

func startLiveRuntime(t *testing.T, agentBrowser, chromium string) *liveRuntime {
	t.Helper()
	// Chromium's SingletonSocket lives under $TMPDIR and inherits its length
	// limit (~107 bytes): use a short root because the default t.TempDir
	// nesting plus a session name already exceeds it (see the documented
	// session-length guidance in docs/mcp.md).
	root, err := os.MkdirTemp("", "pxlive")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	wrapper := filepath.Join(root, "agent-browser")
	script := "#!/bin/sh\nexport PATH=/usr/local/bin:/usr/bin:/bin\nexport AGENT_BROWSER_EXECUTABLE_PATH=" + chromium + "\nexec " + agentBrowser + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(root, "config.json")
	// Mirror the production config shape exactly; only the executable path
	// points at the test host Chromium.
	configJSON, err := json.Marshal(map[string]any{
		"args": "--no-sandbox", "contentBoundaries": true, "debug": false,
		"executablePath": chromium, "hideScrollbars": true, "idleTimeout": "2m", "maxOutput": 20000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configFile, configJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	configuration := browser.Config{
		Host: "127.0.0.1", Port: 8787,
		ArtifactRoot: filepath.Join(root, "artifacts"), StateRoot: filepath.Join(root, "state"),
		AgentBrowser: wrapper, BrowserConfig: configFile,
		CommandTimeout: 60 * time.Second, RequestTimeout: 90 * time.Second,
		MaxArtifactBytes: 64 * 1024 * 1024, MaxTotalArtifactBytes: 256 * 1024 * 1024,
		MaxStateBytes: 256 * 1024 * 1024, MaxSessions: 16, MaxStateEntries: 20_000,
	}
	service, err := browser.NewService(configuration, diagnostics.NormalizeBuild("live", "test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Shutdown)
	return &liveRuntime{service: service, config: configuration}
}

type liveOutcome struct {
	status int
	body   map[string]any
}

func liveCommand(t *testing.T, service *browser.Service, session, command string, args ...string) liveOutcome {
	t.Helper()
	started := time.Now()
	response := postBrowserRequest(t, service, "", command, session, args)
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s %s: invalid JSON %d %s", command, session, response.Code, response.Body.String())
	}
	t.Logf("live %s %s(%q) -> %d in %s", session, command, strings.Join(args, " "), response.Code, time.Since(started).Round(time.Millisecond))
	return liveOutcome{status: response.Code, body: body}
}

func requireCompleted(t *testing.T, outcome liveOutcome, session, command string) string {
	t.Helper()
	if outcome.status != http.StatusOK || outcome.body["outcome"] != "completed" {
		t.Fatalf("%s %s: %#v", session, command, outcome.body)
	}
	stdout, _ := outcome.body["stdout"].(string)
	return stdout
}

func liveBinaries(t *testing.T) (agentBrowser, chromium string) {
	t.Helper()
	if os.Getenv("PIXIE_BROWSER_LIVE") != "1" {
		t.Skip("PIXIE_BROWSER_LIVE=1 not set; skipping live Chromium acceptance")
	}
	agentBrowser = os.Getenv("PIXIE_TEST_AGENT_BROWSER")
	if agentBrowser == "" {
		agentBrowser = "/usr/local/bin/agent-browser"
	}
	chromium = os.Getenv("PIXIE_TEST_CHROMIUM")
	if chromium == "" {
		chromium = "/usr/bin/chromium"
	}
	for _, candidate := range []string{agentBrowser, chromium} {
		info, err := os.Stat(candidate)
		if err != nil || info.Mode().Perm()&0111 == 0 {
			t.Skipf("live browser binary unavailable: %s", candidate)
		}
	}
	version, err := exec.CommandContext(context.Background(), agentBrowser, "--version").Output()
	if err == nil {
		t.Logf("agent-browser version: %s", strings.TrimSpace(string(version)))
	} else {
		t.Logf("agent-browser version unknown: %v", err)
	}
	return agentBrowser, chromium
}

func TestLiveChromiumAcceptance(t *testing.T) {
	agentBrowser, chromium := liveBinaries(t)
	fixture := startLiveFixtures(t)
	runtime := startLiveRuntime(t, agentBrowser, chromium)
	service := runtime.service

	t.Run("connect and navigate simple HTML", func(t *testing.T) {
		stdout := requireCompleted(t, liveCommand(t, service, "live-home", "open", fixture.base+"/"), "live-home", "open")
		_ = stdout
		url := requireCompleted(t, liveCommand(t, service, "live-home", "get", "url"), "live-home", "get url")
		if !strings.Contains(url, "/") {
			t.Fatalf("current url = %q", url)
		}
		title := requireCompleted(t, liveCommand(t, service, "live-home", "get", "title"), "live-home", "get title")
		if !strings.Contains(title, "Pixie live home") {
			t.Fatalf("title = %q", title)
		}
	})

	t.Run("snapshot exposes refs", func(t *testing.T) {
		stdout := requireCompleted(t, liveCommand(t, service, "live-home", "snapshot", "-i"), "live-home", "snapshot")
		for _, marker := range []string{"Welcome to Pixie live", "Count", "Sign in"} {
			if !strings.Contains(stdout, marker) {
				t.Fatalf("snapshot missing %q:\n%s", marker, stdout)
			}
		}
	})

	t.Run("click and keyboard", func(t *testing.T) {
		requireCompleted(t, liveCommand(t, service, "live-home", "click", "button#count"), "live-home", "click")
		requireCompleted(t, liveCommand(t, service, "live-home", "click", "button#count"), "live-home", "click")
		text := requireCompleted(t, liveCommand(t, service, "live-home", "get", "text", "span#count-label"), "live-home", "get text")
		if !strings.Contains(text, "count 2") {
			t.Fatalf("counter label = %q", text)
		}
		requireCompleted(t, liveCommand(t, service, "live-home", "press", "Tab"), "live-home", "press")
	})

	t.Run("fill type and wait", func(t *testing.T) {
		requireCompleted(t, liveCommand(t, service, "live-form", "open", fixture.base+"/form"), "live-form", "open")
		requireCompleted(t, liveCommand(t, service, "live-form", "fill", "input#user", "Ada"), "live-form", "fill")
		requireCompleted(t, liveCommand(t, service, "live-form", "type", "input#pass", "s3cret"), "live-form", "type")
		requireCompleted(t, liveCommand(t, service, "live-form", "click", "button#go"), "live-form", "click")
		requireCompleted(t, liveCommand(t, service, "live-form", "wait", "500"), "live-form", "wait")
		welcome := requireCompleted(t, liveCommand(t, service, "live-form", "get", "text", "p#welcome"), "live-form", "get text")
		if !strings.Contains(welcome, "Welcome, Ada") {
			t.Fatalf("login welcome = %q", welcome)
		}
	})

	t.Run("large DOM snapshot", func(t *testing.T) {
		requireCompleted(t, liveCommand(t, service, "live-large", "open", fixture.base+"/large"), "live-large", "open")
		stdout := requireCompleted(t, liveCommand(t, service, "live-large", "snapshot"), "live-large", "snapshot")
		// Snapshots are capped by AGENT_BROWSER_MAX_OUTPUT (20KB), so a 2000-row
		// table truncates: assert early rows render and record the cap.
		if !strings.Contains(stdout, "row-100") {
			t.Fatalf("large snapshot missing early rows (%d bytes)", len(stdout))
		}
		t.Logf("large DOM snapshot: %d bytes (output cap applies)", len(stdout))
	})

	t.Run("infinite scroll grows content", func(t *testing.T) {
		requireCompleted(t, liveCommand(t, service, "live-scroll", "open", fixture.base+"/scroll"), "live-scroll", "open")
		// Plain snapshots render text nodes; the interactive snapshot only
		// lists focusable elements, so scroll growth is read from feed text.
		before := requireCompleted(t, liveCommand(t, service, "live-scroll", "get", "text", "div#feed"), "live-scroll", "get feed")
		requireCompleted(t, liveCommand(t, service, "live-scroll", "scroll", "down", "2000"), "live-scroll", "scroll")
		requireCompleted(t, liveCommand(t, service, "live-scroll", "wait", "1000"), "live-scroll", "wait")
		after := requireCompleted(t, liveCommand(t, service, "live-scroll", "get", "text", "div#feed"), "live-scroll", "get feed")
		if len(after) <= len(before) || !strings.Contains(after, "item-20") {
			t.Fatalf("scroll did not grow feed: before=%d after=%d", len(before), len(after))
		}
	})

	t.Run("iframe and JS-heavy pages", func(t *testing.T) {
		requireCompleted(t, liveCommand(t, service, "live-frames", "open", fixture.base+"/frames"), "live-frames", "open")
		stdout := requireCompleted(t, liveCommand(t, service, "live-frames", "snapshot"), "live-frames", "snapshot")
		if !strings.Contains(stdout, "Frame host") {
			snippet := stdout
			if len(snippet) > 500 {
				snippet = snippet[:500]
			}
			t.Fatalf("frame snapshot = %q", snippet)
		}
		requireCompleted(t, liveCommand(t, service, "live-js", "open", fixture.base+"/js"), "live-js", "open")
		requireCompleted(t, liveCommand(t, service, "live-js", "wait", "1500"), "live-js", "wait")
		value := requireCompleted(t, liveCommand(t, service, "live-js", "get", "text", "p#value"), "live-js", "get text")
		if strings.TrimSpace(value) == "0" || strings.TrimSpace(value) == "" {
			t.Fatalf("JS interval did not advance: %q", value)
		}
	})

	t.Run("screenshots produce artifacts", func(t *testing.T) {
		outcome := liveCommand(t, service, "live-home", "screenshot", "home.png")
		requireCompleted(t, outcome, "live-home", "screenshot")
		artifact, _ := outcome.body["artifact"].(map[string]any)
		if artifact["url"] != "/v1/artifacts/live-home/home.png" {
			t.Fatalf("artifact = %#v", outcome.body["artifact"])
		}
		path := filepath.Join(runtime.config.ArtifactRoot, "live-home", "home.png")
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			t.Fatalf("screenshot artifact missing: %v", err)
		}
		t.Logf("screenshot artifact: %d bytes", info.Size())
	})

	t.Run("unsupported operations stay rejected", func(t *testing.T) {
		for _, command := range []string{"eval", "pdf", "download"} {
			response := postBrowserRequest(t, service, "", command, "live-home", nil)
			if response.Code != http.StatusBadRequest || responseCode(t, response) != "invalid_request" {
				t.Fatalf("%s not rejected: %d %s", command, response.Code, response.Body.String())
			}
		}
	})

	t.Run("close resets session state", func(t *testing.T) {
		requireCompleted(t, liveCommand(t, service, "live-home", "close"), "live-home", "close")
		for _, dir := range []string{
			filepath.Join(runtime.config.ArtifactRoot, "live-home"),
			filepath.Join(runtime.config.StateRoot, "live-home"),
		} {
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("close retained %s", dir)
			}
		}
		// agent-browser 0.34.0 tears its daemon down asynchronously: an
		// immediate reopen can fail with "Daemon failed to start" while the
		// previous daemon winds down. Retry once after a short settle so the
		// reset assertion is deterministic; both attempts are logged.
		outcome := liveCommand(t, service, "live-home", "open", fixture.base+"/")
		if outcome.status != http.StatusOK {
			t.Logf("immediate reopen raced daemon teardown, retrying after settle: %#v", outcome.body)
			time.Sleep(3 * time.Second)
			outcome = liveCommand(t, service, "live-home", "open", fixture.base+"/")
		}
		requireCompleted(t, outcome, "live-home", "reopen")
		requireCompleted(t, liveCommand(t, service, "live-home", "close"), "live-home", "close")
		for _, session := range []string{"live-form", "live-large", "live-scroll", "live-frames", "live-js"} {
			requireCompleted(t, liveCommand(t, service, session, "close"), session, "close")
		}
	})
}

type liveStep struct {
	command string
	session string
	args    []string
}

func freePort(t *testing.T) string {
	t.Helper()
	listener, port := listenLoopback(t)
	t.Cleanup(func() { _ = listener.Close() })
	_ = listener.Close()
	return fmt.Sprint(port)
}

func waitForCDP(t *testing.T, port string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://127.0.0.1:" + port + "/json/version")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("CDP endpoint 127.0.0.1:%s did not become ready", port)
}
