package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// writeAdminStubPackage creates a minimal selected-installation directory with
// a valid public identity. The Go bridge only reads package.json during
// verification; the sidecar stub owns the hello reply.
func writeAdminStubPackage(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := fmt.Sprintf(
		`{"name":%q,"version":%q,"type":"module","exports":{".":{"import":"./dist/index.js"}}}`,
		nativeAdminBridgePackageName, version,
	)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "dist"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dist", "index.js"), []byte("export {};\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeAdminStubSidecar writes a tiny POSIX shell sidecar. It never executes
// arbitrary payloads: it parses the documented flags, replies to
// bridge.hello plus representative FC17/FC19/FC20 methods, and errors on
// everything else.
func writeAdminStubSidecar(t *testing.T) string {
	t.Helper()
	script := `#!/bin/sh
mode="good"
pkg=""
while [ $# -gt 0 ]; do
  case "$1" in
    --mode) mode="$2"; shift 2;;
    --package) pkg="$2"; shift 2;;
    --agent-dir) shift 2;;
    *) shift;;
  esac
done
version=$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$pkg/package.json" | sed -n '1p')
entry="$pkg/dist/index.js"
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"bridge.hello"'*)
      if [ "$mode" = "bad" ]; then
        printf '{"id":%s,"ok":true,"result":{"protocolVersion":1,"packageName":"evil","packageVersion":"0.0.0","moduleOrigin":"/tmp/evil.js"}}\n' "$id"
      else
        printf '{"id":%s,"ok":true,"result":{"protocolVersion":1,"packageName":"@earendil-works/pi-coding-agent","packageVersion":"%s","packageDir":"%s","moduleOrigin":"%s"}}\n' "$id" "$version" "$pkg" "$entry"
      fi
      ;;
    *'"method":"pi.providers.list"'*)
      printf '{"id":%s,"ok":true,"result":{"entries":[]}}\n' "$id"
      ;;
    *'"method":"pi.defaults.read"'*)
      printf '{"id":%s,"ok":true,"result":{"providerId":null,"modelId":null}}\n' "$id"
      ;;
    *'"method":"pi.preferences.read"'*)
      printf '{"id":%s,"ok":true,"result":{"values":[{"key":"piThinkingEffort","value":null},{"key":"compactionReserveTokens","value":null}]}}\n' "$id"
      ;;
    *'"method":"pi.extensions.list"'*)
      printf '{"id":%s,"ok":true,"result":{"version":1,"context":{"cwd":"/tmp","sessionId":null,"reader":"service"},"configurationRevisions":{"user":"0000000000000000000000000000000000000000000000000000000000000000","project":"0000000000000000000000000000000000000000000000000000000000000000"},"packages":[],"paths":[],"resources":[],"extensions":[],"errors":[],"warnings":[],"trust":{"projectTrusted":true,"decision":null,"requiresDecision":false}}}\n' "$id"
      ;;
    *)
      printf '{"id":%s,"ok":false,"error":"unsupported"}\n' "$id"
      ;;
  esac
done
`
	dir := t.TempDir()
	path := filepath.Join(dir, "sidecar.sh")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeAdminEventSidecar is the stub used to prove that an unsolicited sidecar
// event frame is forwarded even though the triggering request still needs its
// reply.
func writeAdminEventSidecar(t *testing.T) string {
	t.Helper()
	script := `#!/bin/sh
pkg=""
while [ $# -gt 0 ]; do
  case "$1" in
    --package) pkg="$2"; shift 2;;
    *) shift;;
  esac
done
version=$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$pkg/package.json" | sed -n '1p')
entry="$pkg/dist/index.js"
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"bridge.hello"'*)
      printf '{"id":%s,"ok":true,"result":{"protocolVersion":1,"packageName":"@earendil-works/pi-coding-agent","packageVersion":"%s","packageDir":"%s","moduleOrigin":"%s"}}\n' "$id" "$version" "$pkg" "$entry"
      ;;
    *'"method":"pi.providers.list"'*)
      printf '{"event":"provider.login","params":{"loginId":"login-1","providerId":"alpha","frame":{"kind":"progress","message":"working"}}}\n'
      printf '{"id":%s,"ok":true,"result":{"entries":[]}}\n' "$id"
      ;;
    *)
      printf '{"id":%s,"ok":false,"error":"unsupported"}\n' "$id"
      ;;
  esac
done
`
	dir := t.TempDir()
	path := filepath.Join(dir, "sidecar-event.sh")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func testAdminBridgeSupervisor(t *testing.T, config AdminBridgeConfig, agentDir string) *nativeSupervisor {
	t.Helper()
	supervisor := newNativeSupervisor(Config{AgentDir: agentDir, AdminBridge: config})
	supervisor.adminBridge = newNativeAdminBridge(config, agentDir)
	supervisor.adminBridge.onEvent = supervisor.publishAdminEvent
	return supervisor
}

func TestAdminBridgeDisabledFailsClosed(t *testing.T) {
	agentDir := t.TempDir()
	config := AdminBridgeConfig{
		Enabled:     false,
		Executable:  "/bin/sh",
		Args:        []string{writeAdminStubSidecar(t)},
		PackagePath: writeAdminStubPackage(t, "0.85.1"),
	}
	supervisor := testAdminBridgeSupervisor(t, config, agentDir)
	for _, operation := range nativeAdminBridgeOperations {
		if supervisor.operationSet()[operation] {
			t.Fatalf("disabled bridge advertised %q", operation)
		}
	}
	if _, err := supervisor.callHost(context.Background(), "pi.providers.list", map[string]any{}); err == nil {
		t.Fatal("disabled bridge served an administration operation")
	}
}

func TestAdminBridgeUnverifiedPackageFailsClosed(t *testing.T) {
	agentDir := t.TempDir()
	missing := filepath.Join(t.TempDir(), "not-installed")
	config := AdminBridgeConfig{
		Enabled:     true,
		Executable:  "/bin/sh",
		Args:        []string{writeAdminStubSidecar(t)},
		PackagePath: missing,
	}
	supervisor := testAdminBridgeSupervisor(t, config, agentDir)
	if supervisor.operationSet()["pi.providers.list"] {
		t.Fatal("unverified package advertised an administration operation")
	}
	if _, err := supervisor.callHost(context.Background(), "pi.providers.list", map[string]any{}); err == nil {
		t.Fatal("unverified package served an administration operation")
	}
}

func TestAdminBridgeHelloVerificationFailureWithdrawsAdvertisement(t *testing.T) {
	agentDir := t.TempDir()
	config := AdminBridgeConfig{
		Enabled:     true,
		Executable:  "/bin/sh",
		Args:        []string{writeAdminStubSidecar(t), "--mode", "bad"},
		PackagePath: writeAdminStubPackage(t, "0.85.1"),
	}
	supervisor := testAdminBridgeSupervisor(t, config, agentDir)
	if !supervisor.operationSet()["pi.providers.list"] {
		t.Fatal("locally verified package did not advertise before the hello attestation")
	}
	if _, err := supervisor.callHost(context.Background(), "pi.providers.list", map[string]any{}); err == nil {
		t.Fatal("failed hello attestation still served an administration operation")
	}
	if supervisor.operationSet()["pi.providers.list"] {
		t.Fatal("failed hello attestation did not withdraw the administration advertisement")
	}
}

func TestAdminBridgeProxiesFC17Operations(t *testing.T) {
	agentDir := t.TempDir()
	config := AdminBridgeConfig{
		Enabled:     true,
		Executable:  "/bin/sh",
		Args:        []string{writeAdminStubSidecar(t)},
		PackagePath: writeAdminStubPackage(t, "0.85.1"),
	}
	supervisor := testAdminBridgeSupervisor(t, config, agentDir)
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := supervisor.adminBridge.close(shutdown); err != nil {
			t.Errorf("close administration bridge: %v", err)
		}
	})
	raw, err := supervisor.callHost(context.Background(), "pi.providers.list", map[string]any{})
	if err != nil {
		t.Fatalf("proxied pi.providers.list: %v", err)
	}
	if string(raw) != `{"entries":[]}` {
		t.Fatalf("proxied pi.providers.list = %s", raw)
	}
	for _, operation := range nativeAdminBridgeOperations {
		if !supervisor.operationSet()[operation] {
			t.Fatalf("bridge operation %q is not advertised after verification", operation)
		}
	}
	// The sidecar's fixed allowlist still rejects the FC26 runtime registration
	// rows that need a live adapter event bus.
	if _, err := supervisor.adminBridge.call(context.Background(), "mcp.attach", map[string]any{}); err == nil {
		t.Fatal("bridge proxied an unimplemented FC26 operation")
	}
}

func TestAdminBridgeProxiesFC19AndFC20Operations(t *testing.T) {
	agentDir := t.TempDir()
	config := AdminBridgeConfig{
		Enabled:     true,
		Executable:  "/bin/sh",
		Args:        []string{writeAdminStubSidecar(t)},
		PackagePath: writeAdminStubPackage(t, "0.85.1"),
	}
	supervisor := testAdminBridgeSupervisor(t, config, agentDir)
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := supervisor.adminBridge.close(shutdown); err != nil {
			t.Errorf("close administration bridge: %v", err)
		}
	})
	raw, err := supervisor.callHost(context.Background(), "pi.defaults.read", map[string]any{})
	if err != nil {
		t.Fatalf("proxied pi.defaults.read: %v", err)
	}
	if string(raw) != `{"providerId":null,"modelId":null}` {
		t.Fatalf("proxied pi.defaults.read = %s", raw)
	}
	raw, err = supervisor.callHost(context.Background(), "pi.extensions.list", map[string]any{})
	if err != nil {
		t.Fatalf("proxied pi.extensions.list: %v", err)
	}
	var inventory struct {
		Version int `json:"version"`
		Context struct {
			Reader string `json:"reader"`
		} `json:"context"`
		Packages []any `json:"packages"`
	}
	if json.Unmarshal(raw, &inventory) != nil || inventory.Version != 1 || inventory.Context.Reader != "service" || inventory.Packages == nil {
		t.Fatalf("proxied pi.extensions.list = %s", raw)
	}
	if !supervisor.operationSet()["pi.defaults.read"] || !supervisor.operationSet()["pi.extensions.list"] {
		t.Fatal("FC19/FC20 operations are not advertised after verification")
	}
}

// TestAdminBridgeForwardsSidecarEvents proves the unsolicited event direction:
// the sidecar emits provider.login while the request is in flight and the host
// forwards it to connection subscribers without losing the reply.
func TestAdminBridgeForwardsSidecarEvents(t *testing.T) {
	agentDir := t.TempDir()
	config := AdminBridgeConfig{
		Enabled:     true,
		Executable:  "/bin/sh",
		Args:        []string{writeAdminEventSidecar(t)},
		PackagePath: writeAdminStubPackage(t, "0.85.1"),
	}
	supervisor := testAdminBridgeSupervisor(t, config, agentDir)
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = supervisor.adminBridge.close(shutdown)
	})
	events := make(chan nativeEvent, 4)
	unsubscribe := supervisor.subscribe(func(event nativeEvent) error {
		if event.method != "" {
			events <- event
		}
		return nil
	})
	defer unsubscribe()
	raw, err := supervisor.callHost(context.Background(), "pi.providers.list", map[string]any{})
	if err != nil {
		t.Fatalf("proxied pi.providers.list: %v", err)
	}
	if string(raw) != `{"entries":[]}` {
		t.Fatalf("proxied pi.providers.list = %s", raw)
	}
	select {
	case event := <-events:
		if event.method != "provider.login" {
			t.Fatalf("forwarded event method = %q", event.method)
		}
		if event.event["loginId"] != "login-1" || event.event["providerId"] != "alpha" {
			t.Fatalf("forwarded event params = %#v", event.event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sidecar event was not forwarded to subscribers")
	}
}

func TestVerifySelectedPiPackageRejectsWrongIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"not-pi","version":"1.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := verifySelectedPiPackage(dir); err == nil {
		t.Fatal("wrong package identity was accepted")
	}
	if _, _, _, err := verifySelectedPiPackage("relative/path"); err == nil {
		t.Fatal("relative package path was accepted")
	}
	if _, _, _, err := verifySelectedPiPackage(""); err == nil {
		t.Fatal("empty package path was accepted")
	}
}

// TestAdminBridgeAdvertisesAndProxiesOverHost verifies the whole seam: an
// opted-in host advertises the bridge operations at hello and proxies one of
// them through the lazily spawned sidecar.
func TestAdminBridgeAdvertisesAndProxiesOverHost(t *testing.T) {
	handle, err := Start(context.Background(), Config{
		Host:         "127.0.0.1",
		Port:         0,
		Secret:       testSecret,
		AgentDir:     t.TempDir(),
		PiExecutable: "/bin/true",
		AdminBridge: AdminBridgeConfig{
			Enabled:     true,
			Executable:  "/bin/sh",
			Args:        []string{writeAdminStubSidecar(t)},
			PackagePath: writeAdminStubPackage(t, "0.85.1"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close(context.Background())
	connection := dialHostConnection(t, handle)
	if err := connection.Write(context.Background(), websocket.MessageText, []byte(`{"id":1,"method":"runtime.hello","params":{"protocolVersion":1}}`)); err != nil {
		t.Fatal(err)
	}
	var hello struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(readHostFrame(t, connection), &hello); err != nil {
		t.Fatal(err)
	}
	operations, ok := hello.Result["operationSet"].(map[string]any)
	if !ok || operations["pi.providers.list"] != true {
		t.Fatalf("opted-in host did not advertise FC17: %#v", hello.Result["operationSet"])
	}
	if err := connection.Write(context.Background(), websocket.MessageText, []byte(`{"id":2,"method":"pi.providers.list","params":{}}`)); err != nil {
		t.Fatal(err)
	}
	var listed struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(readHostFrame(t, connection), &listed); err != nil {
		t.Fatal(err)
	}
	if listed.ID != 2 || string(listed.Result) != `{"entries":[]}` {
		t.Fatalf("proxied pi.providers.list = %s", listed.Result)
	}
}
