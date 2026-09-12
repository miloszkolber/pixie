package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const testSecret = "assistant-test-secret-0123456789abcdef"

func TestStartProvidesPrivateEndpointAndHello(t *testing.T) {
	handle, err := Start(context.Background(), Config{Host: "127.0.0.1", Port: 0, Secret: testSecret})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close(context.Background())
	if !strings.HasPrefix(handle.Endpoint(), "ws://127.0.0.1:") || !strings.HasSuffix(handle.Endpoint(), "/pi") {
		t.Fatalf("unexpected endpoint %q", handle.Endpoint())
	}
	select {
	case <-handle.Ready():
	case <-time.After(time.Second):
		t.Fatal("embedded assistant did not become ready")
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+testSecret)
	connection, _, err := websocket.Dial(context.Background(), handle.Endpoint(), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "test complete")
	request := []byte(`{"id":1,"method":"runtime.hello","params":{"protocolVersion":1}}`)
	if err := connection.Write(context.Background(), websocket.MessageText, request); err != nil {
		t.Fatal(err)
	}
	_, response, err := connection.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		ID     uint64         `json:"id"`
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.ID != 1 || envelope.Result["protocolVersion"] != float64(1) {
		t.Fatalf("hello response = %s", response)
	}
	operations, ok := envelope.Result["operationSet"].(map[string]any)
	if !ok || operations["session.prompt"] != true || operations["session.prompt.image"] != true || operations["session.delete"] != false || operations["session.configure"] != false || operations["runtime.restart"] != false || operations["mcp.attach"] != false {
		t.Fatalf("hello operationSet is incomplete or unsafe: %#v", operations)
	}
}

func TestNativeOperationSetIsExhaustiveAndFailClosed(t *testing.T) {
	operations := nativeOperationSet()
	// The exact enabled subset is declared here so adding or removing native
	// support is an intentional review step, not an incidental edit.
	wantEnabled := map[string]bool{
		"session.list": true, "session.create": true, "session.load": true,
		"session.prompt": true, "session.cancel": true, "session.prompt.image": true,
		"session.release": true, "runtime.release": true,
	}
	for operation, enabled := range operations {
		if enabled != wantEnabled[operation] {
			t.Errorf("operation %q enabled=%v, want %v", operation, enabled, wantEnabled[operation])
		}
	}
	for operation := range wantEnabled {
		if _, present := operations[operation]; !present {
			t.Errorf("enabled operation %q is absent from the catalog", operation)
		}
	}
	for _, unsupported := range []string{"session.delete", "session.fork", "session.rename", "session.archive", "session.steer", "session.prompt.resource", "session.configure", "runtime.restart", "mcp.attach", "pi.tools.call", "pi.defaults.save"} {
		if value, present := operations[unsupported]; !present || value {
			t.Errorf("unsupported operation %q = %v, present %v", unsupported, value, present)
		}
	}
}

func TestPromptPayloadAcceptsPublicImagesAndRejectsResources(t *testing.T) {
	message, images, err := promptPayload(map[string]any{"content": []any{
		map[string]any{"type": "text", "text": "hello"},
		map[string]any{"type": "image", "data": "aGVsbG8=", "mimeType": "image/png"},
	}})
	if err != nil || message != "hello" || len(images) != 1 {
		t.Fatalf("image prompt = %q, %#v, %v", message, images, err)
	}
	if _, _, err := promptPayload(map[string]any{"content": []any{map[string]any{"type": "resource", "resource": map[string]any{"text": "secret"}}}}); err == nil {
		t.Fatal("text resource prompt was accepted")
	}
}

func TestOnlyAgentSettledTerminatesActiveRun(t *testing.T) {
	run := &nativeRun{sessionID: "a", barrierComplete: true, accepted: true, started: true, settled: make(chan struct{})}
	child := &nativeChild{activeRun: run}
	child.observeRunEvent(map[string]any{"type": "agent_end"})
	child.observeRunEvent(map[string]any{"type": "agent_end"})
	select {
	case <-run.settled:
		t.Fatal("agent_end settled the run")
	default:
	}
	child.observeRunEvent(map[string]any{"type": "agent_settled"})
	select {
	case <-run.settled:
	default:
		t.Fatal("agent_settled did not settle the run")
	}
}

func TestCreateOverridePreflight(t *testing.T) {
	if !hasCreateOverrides(map[string]any{"model": "provider/model"}) || !hasCreateOverrides(map[string]any{"thinkingLevel": "high"}) || !hasCreateOverrides(map[string]any{"mcpServers": []any{map[string]any{"name": "x"}}}) {
		t.Fatal("create-time mutation override was not detected")
	}
	if hasCreateOverrides(map[string]any{"cwd": "/tmp", "mcpServers": []any{}}) {
		t.Fatal("empty MCP list was treated as an override")
	}
}

func startStatefulPiFixture(t *testing.T) (*nativeSupervisor, string, string) {
	t.Helper()
	cwd := t.TempDir()
	agentDir := t.TempDir()
	logPath := filepath.Join(agentDir, "calls.log")
	fixture := filepath.Join(t.TempDir(), "pi")
	script := `#!/bin/sh
state=initial-$$
path="$PI_CODING_AGENT_DIR/$state.jsonl"
: > "$path"
printf 'launch:%s:%s\n' "$$" "$PWD" >> 'LOG'
printf '{"type":"agent_settled"}\n'
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"type":"get_state"'*) printf '{"id":%s,"type":"response","command":"get_state","success":true,"data":{"sessionId":"%s","sessionFile":"%s","isStreaming":false,"thinkingLevel":"medium","model":{"id":"fixture","provider":"fixture"}}}\n' "$id" "$state" "$path" ;;
    *'"type":"get_messages"'*) printf '{"id":%s,"type":"response","command":"get_messages","success":true,"data":{"messages":[]}}\n' "$id" ;;
		*'"type":"new_session"'*) state=session-$$; path="$PI_CODING_AGENT_DIR/$state.jsonl"; : > "$path"; printf 'new:%s\n' "$state" >> 'LOG'; printf '{"id":%s,"type":"response","command":"new_session","success":true,"data":{"cancelled":false}}\n' "$id" ;;
		*'"type":"switch_session"'*) path=$(printf '%s' "$line" | sed -n 's/.*"sessionPath":"\([^"]*\)".*/\1/p'); state=$(basename "$path" .jsonl); printf 'switch:%s\n' "$path" >> 'LOG'; printf '{"id":%s,"type":"response","command":"switch_session","success":true,"data":{"cancelled":false}}\n' "$id" ;;
		*'"type":"clear_queue"'*) printf 'clear_queue\n' >> 'LOG'; printf '{"id":%s,"type":"response","command":"clear_queue","success":true,"data":{}}\n' "$id" ;;
		*'"type":"prompt"'*) printf 'prompt:%s\n' "$state" >> 'LOG'; printf '{"id":%s,"type":"response","command":"prompt","success":true,"data":{"accepted":true}}\n' "$id"; printf '{"type":"agent_start"}\n'; (sleep 0.2; printf '{"type":"message_end","message":{"role":"assistant","stopReason":"end_turn"}}\n'; printf 'settled:%s\n' "$state" >> 'LOG'; printf '{"type":"agent_settled"}\n') & ;;
		*'"type":"abort"'*) printf 'abort\n' >> 'LOG'; printf '{"type":"agent_settled"}\n'; printf '{"id":%s,"type":"response","command":"abort","success":true,"data":{"aborted":false}}\n' "$id" ;;
    *'"type":"shutdown"'*) exit 0 ;;
  esac
done
`
	script = strings.ReplaceAll(script, "LOG", logPath)
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	supervisor := newNativeSupervisor(Config{PiExecutable: fixture, AgentDir: agentDir})
	if err := supervisor.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := supervisor.close(ctx); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	})
	return supervisor, logPath, cwd
}

func createFixtureSession(t *testing.T, supervisor *nativeSupervisor, cwd string) string {
	t.Helper()
	raw, err := supervisor.callHost(context.Background(), "session.create", map[string]any{"cwd": cwd, "mcpServers": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.SessionID == "" {
		t.Fatalf("create result = %s, %v", raw, err)
	}
	return result.SessionID
}

func TestOfficialPiUsesOneImmutableChildPerSessionIncludingSameCWD(t *testing.T) {
	supervisor, logPath, cwd := startStatefulPiFixture(t)
	first := createFixtureSession(t, supervisor, cwd)
	second := createFixtureSession(t, supervisor, cwd)
	if first == second {
		t.Fatal("two native children returned the same session identity")
	}
	events := make(chan nativeEvent, 8)
	unsubscribe := supervisor.subscribe(func(event nativeEvent) error { events <- event; return nil })
	defer unsubscribe()
	var wait sync.WaitGroup
	errs := make(chan error, 2)
	started := time.Now()
	for _, id := range []string{first, second} {
		wait.Add(1)
		go func(sessionID string) {
			defer wait.Done()
			_, err := supervisor.callHost(context.Background(), "session.prompt", map[string]any{"sessionId": sessionID, "content": []any{map[string]any{"type": "text", "text": sessionID}}})
			errs <- err
		}(id)
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(started); elapsed > 350*time.Millisecond {
		t.Fatalf("independent session children were serialized: %v", elapsed)
	}
	counts := map[string]int{}
	for len(events) > 0 {
		counts[(<-events).sessionID]++
	}
	if counts[first] != 4 || counts[second] != 4 {
		t.Fatalf("events were not immutably attributed: %#v", counts)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "launch:") != 2 || strings.Count(string(raw), ":"+cwd+"\n") != 2 {
		t.Fatalf("children were not both launched in exact cwd: %s", raw)
	}
	if strings.Contains(string(raw), "new:") {
		t.Fatalf("create called new_session instead of binding launch identity: %s", raw)
	}
}

func TestPromptResponseWaitsUntilSettledEventIsEnqueued(t *testing.T) {
	supervisor, _, cwd := startStatefulPiFixture(t)
	id := createFixtureSession(t, supervisor, cwd)
	settledSeen := make(chan struct{})
	release := make(chan struct{})
	started := false
	unsubscribe := supervisor.subscribe(func(event nativeEvent) error {
		if event.event["type"] == "agent_start" {
			started = true
		}
		if event.event["type"] == "agent_settled" && started {
			close(settledSeen)
			<-release
		}
		return nil
	})
	defer unsubscribe()
	done := make(chan error, 1)
	go func() {
		_, err := supervisor.callHost(context.Background(), "session.prompt", map[string]any{"sessionId": id, "content": []any{map[string]any{"type": "text", "text": "hi"}}})
		done <- err
	}()
	select {
	case <-settledSeen:
	case <-time.After(time.Second):
		t.Fatal("settlement event not observed")
	}
	select {
	case err := <-done:
		t.Fatalf("prompt completed before event enqueue barrier: %v", err)
	default:
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("prompt did not complete")
	}
}

func TestPromptUsesTerminalAssistantStopReason(t *testing.T) {
	supervisor, _, cwd := startStatefulPiFixture(t)
	id := createFixtureSession(t, supervisor, cwd)
	raw, err := supervisor.callHost(context.Background(), "session.prompt", map[string]any{"sessionId": id, "content": []any{map[string]any{"type": "text", "text": "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		StopReason string `json:"stopReason"`
	}
	if json.Unmarshal(raw, &response) != nil || response.StopReason != "end_turn" {
		t.Fatalf("prompt response = %s", raw)
	}
}

func TestStaleSettlementAfterFirstStateCannotSettleNewPrompt(t *testing.T) {
	supervisor, _, cwd := startStatefulPiFixture(t)
	id := createFixtureSession(t, supervisor, cwd)
	var mu sync.Mutex
	types := []string{}
	unsubscribe := supervisor.subscribe(func(event nativeEvent) error {
		if event.sessionID == id {
			kind, _ := event.event["type"].(string)
			mu.Lock()
			types = append(types, kind)
			mu.Unlock()
		}
		return nil
	})
	defer unsubscribe()
	started := time.Now()
	if _, err := supervisor.callHost(context.Background(), "session.prompt", map[string]any{"sessionId": id, "content": []any{map[string]any{"type": "text", "text": "barrier"}}}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 180*time.Millisecond {
		t.Fatalf("stale pre-run settlement completed prompt in %v", elapsed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(types) < 3 || types[0] != "agent_settled" || types[1] != "agent_start" || types[len(types)-1] != "agent_settled" {
		t.Fatalf("barrier event order = %#v", types)
	}
}

func TestAcceptedIdleInputHandlerSettlesWithoutAgentStart(t *testing.T) {
	agentDir, cwd := t.TempDir(), t.TempDir()
	fixture := filepath.Join(t.TempDir(), "pi")
	script := `#!/bin/sh
: > 'AGENT/handled.jsonl'
while IFS= read -r line; do
 id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
 case "$line" in
  *'"type":"get_state"'*) printf '{"id":%s,"type":"response","command":"get_state","success":true,"data":{"sessionId":"handled","sessionFile":"AGENT/handled.jsonl","isStreaming":false}}\n' "$id" ;;
  *'"type":"get_messages"'*) printf '{"id":%s,"type":"response","command":"get_messages","success":true,"data":{"messages":[]}}\n' "$id" ;;
  *'"type":"clear_queue"'*) printf '{"id":%s,"type":"response","command":"clear_queue","success":true,"data":{}}\n' "$id" ;;
  *'"type":"abort"'*) printf '{"type":"agent_settled"}\n'; printf '{"id":%s,"type":"response","command":"abort","success":true,"data":{"aborted":false}}\n' "$id" ;;
  *'"type":"prompt"'*) printf '{"id":%s,"type":"response","command":"prompt","success":true,"data":{"accepted":true}}\n' "$id" ;;
  *'"type":"shutdown"'*) exit 0 ;;
 esac
done
`
	script = strings.ReplaceAll(script, "AGENT", agentDir)
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	supervisor := newNativeSupervisor(Config{PiExecutable: fixture, AgentDir: agentDir})
	if err := supervisor.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = supervisor.close(ctx)
	})
	id := createFixtureSession(t, supervisor, cwd)
	raw, err := supervisor.callHost(context.Background(), "session.prompt", map[string]any{"sessionId": id, "content": []any{map[string]any{"type": "text", "text": "handled"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"stopReason":"handled"`) {
		t.Fatalf("handled response = %s", raw)
	}
}

func TestConcurrentCancelUsesOnlyActiveSessionLane(t *testing.T) {
	supervisor, logPath, cwd := startStatefulPiFixture(t)
	id := createFixtureSession(t, supervisor, cwd)
	started := make(chan struct{})
	unsubscribe := supervisor.subscribe(func(event nativeEvent) error {
		if event.event["type"] == "agent_start" {
			select {
			case <-started:
			default:
				close(started)
			}
		}
		return nil
	})
	defer unsubscribe()
	done := make(chan error, 1)
	go func() {
		_, err := supervisor.callHost(context.Background(), "session.prompt", map[string]any{"sessionId": id, "content": []any{map[string]any{"type": "text", "text": "hi"}}})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("run did not start")
	}
	if _, err := supervisor.callHost(context.Background(), "session.cancel", map[string]any{"sessionId": "wrong"}); err == nil {
		t.Fatal("wrong session cancel succeeded")
	}
	if _, err := supervisor.callHost(context.Background(), "session.cancel", map[string]any{"sessionId": id}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(logPath)
	if strings.Count(string(raw), "abort\n") != 2 {
		t.Fatalf("cancel log = %s", raw)
	}
}

func TestSnapshotPreservesActiveRunAndActiveFailureUncertainty(t *testing.T) {
	supervisor, _, cwd := startStatefulPiFixture(t)
	id := createFixtureSession(t, supervisor, cwd)
	started := make(chan struct{})
	unsubscribe := supervisor.subscribe(func(event nativeEvent) error {
		if event.sessionID == id && event.event["type"] == "agent_start" {
			select {
			case <-started:
			default:
				close(started)
			}
		}
		return nil
	})
	defer unsubscribe()
	promptDone := make(chan error, 1)
	go func() {
		_, err := supervisor.callHost(context.Background(), "session.prompt", map[string]any{"sessionId": id, "content": []any{map[string]any{"type": "text", "text": "hi"}}})
		promptDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("prompt did not start")
	}
	raw, err := supervisor.callHost(context.Background(), "session.load", map[string]any{"sessionId": id, "cwd": cwd})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		RunID       string `json:"runId"`
		IsStreaming bool   `json:"isStreaming"`
	}
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.RunID == "" || !snapshot.IsStreaming {
		t.Fatalf("active snapshot = %s", raw)
	}
	child := supervisor.children[id]
	killNativeProcess(child.cmd.Process)
	if err := <-promptDone; err == nil {
		t.Fatal("active child failure reported success")
	}
	select {
	case <-child.done:
	case <-time.After(time.Second):
		t.Fatal("active child did not fail")
	}
	deadline := time.Now().Add(time.Second)
	for {
		supervisor.mu.Lock()
		current := supervisor.children[id]
		supervisor.mu.Unlock()
		if current == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("active failed child remained resident")
		}
		time.Sleep(time.Millisecond)
	}
	raw, err = supervisor.callHost(context.Background(), "session.load", map[string]any{"sessionId": id, "cwd": cwd})
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.RunID != "uncertain" || !snapshot.IsStreaming {
		t.Fatalf("uncertain snapshot = %s", raw)
	}
	if _, err := supervisor.callHost(context.Background(), "session.prompt", map[string]any{"sessionId": id, "content": []any{map[string]any{"type": "text", "text": "again"}}}); err == nil {
		t.Fatal("uncertain session accepted another prompt")
	}
}

func TestDurableRegistryReloadUsesExactPathAndCWD(t *testing.T) {
	supervisor, logPath, cwd := startStatefulPiFixture(t)
	id := createFixtureSession(t, supervisor, cwd)
	ref := supervisor.sessions[id]
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := supervisor.close(ctx); err != nil {
		t.Fatal(err)
	}
	reloaded := newNativeSupervisor(supervisor.config)
	if err := reloaded.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = reloaded.close(closeCtx)
	})
	if _, err := reloaded.callHost(context.Background(), "session.load", map[string]any{"sessionId": id, "cwd": cwd}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(logPath)
	if !strings.Contains(string(raw), "switch:"+ref.Path) {
		t.Fatalf("exact registered path was not loaded: %s", raw)
	}
	if _, err := reloaded.callHost(context.Background(), "session.load", map[string]any{"sessionId": id, "cwd": filepath.Dir(cwd)}); err == nil {
		t.Fatal("wrong cwd was accepted")
	}
}

func TestRegistryDropsOnlyMissingUnmaterializedSessionAtStartup(t *testing.T) {
	agentDir, cwd := t.TempDir(), t.TempDir()
	registryDir := filepath.Join(agentDir, "pixie")
	if err := os.MkdirAll(registryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	materializedPath := filepath.Join(agentDir, "materialized.jsonl")
	if err := os.WriteFile(materializedPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plannedPath := filepath.Join(agentDir, "planned.jsonl")
	registry := nativeRegistry{Version: 1, Sessions: map[string]nativeSessionRef{
		"materialized": {Path: materializedPath, CWD: cwd},
		"planned":      {Path: plannedPath, CWD: cwd, Unmaterialized: true},
	}}
	raw, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(registryDir, "native-sessions.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	supervisor := newNativeSupervisor(Config{PiExecutable: "/bin/true", AgentDir: agentDir})
	if err := supervisor.start(context.Background()); err != nil {
		t.Fatalf("missing planned transcript blocked startup: %v", err)
	}
	t.Cleanup(func() { _ = supervisor.close(context.Background()) })
	if _, ok := supervisor.sessions["planned"]; ok {
		t.Fatal("missing unmaterialized session survived restart")
	}
	if ref, ok := supervisor.sessions["materialized"]; !ok || ref.Path != materializedPath {
		t.Fatalf("materialized session was not preserved: %#v", supervisor.sessions)
	}
	persisted, err := os.ReadFile(supervisor.registryPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted), plannedPath) {
		t.Fatalf("removed planned session remained in registry: %s", persisted)
	}
}

func TestUnmaterializedTranscriptMustExistAndBeRegularBeforeReload(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	planned := filepath.Join(agentDir, "planned.jsonl")
	supervisor := newNativeSupervisor(Config{PiExecutable: "/bin/true", AgentDir: agentDir})
	supervisor.sessions["planned"] = nativeSessionRef{Path: planned, CWD: cwd, Unmaterialized: true}
	if _, err := supervisor.loadChild(context.Background(), "planned", cwd); err == nil || !strings.Contains(err.Error(), "not materialized") {
		t.Fatalf("missing planned transcript reload error = %v", err)
	}
	target := filepath.Join(agentDir, "target.jsonl")
	if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, planned); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := supervisor.loadChild(context.Background(), "planned", cwd); err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
		t.Fatalf("symlink transcript reload error = %v", err)
	}
}

func TestIdleChildFailureCanReloadExactRegisteredSession(t *testing.T) {
	supervisor, logPath, cwd := startStatefulPiFixture(t)
	id := createFixtureSession(t, supervisor, cwd)
	child := supervisor.children[id]
	killNativeProcess(child.cmd.Process)
	select {
	case <-child.done:
	case <-time.After(time.Second):
		t.Fatal("failed child was not reaped")
	}
	deadline := time.Now().Add(time.Second)
	for {
		supervisor.mu.Lock()
		resident := supervisor.children[id]
		supervisor.mu.Unlock()
		if resident == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("failed exact child remained resident")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := supervisor.callHost(context.Background(), "session.load", map[string]any{"sessionId": id, "cwd": cwd}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(logPath)
	if !strings.Contains(string(raw), "switch:"+supervisor.sessions[id].Path) {
		t.Fatalf("idle crash did not reload exact path: %s", raw)
	}
}

func TestHostIdentityIsStableAcrossRestart(t *testing.T) {
	agentDir := t.TempDir()
	first, err := loadOrCreateHostIdentity(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if !validHostIdentity(first) {
		t.Fatalf("host identity is not a durable hex value: %q", first)
	}
	second, err := loadOrCreateHostIdentity(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("host identity changed across restarts: %q -> %q", first, second)
	}
	info, err := os.Stat(filepath.Join(agentDir, "pixie", "host-identity.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("host identity permissions = %v, %v", info, err)
	}
}

func TestSelfRestartIsExplicitlyOptIn(t *testing.T) {
	executable, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true binary available")
	}
	agentDir := t.TempDir()
	disabled, err := Start(context.Background(), Config{Host: "127.0.0.1", Port: 0, Secret: testSecret, AgentDir: agentDir, PiExecutable: executable})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.operationSet()["runtime.restart"] {
		t.Fatal("runtime.restart was advertised without explicit opt-in")
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = disabled.Close(closeCtx)
	cancel()
	enabled, err := Start(context.Background(), Config{Host: "127.0.0.1", Port: 0, Secret: testSecret, AgentDir: agentDir, PiExecutable: executable, AllowSelfRestart: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = enabled.Close(cleanupCtx)
	})
	if !enabled.operationSet()["runtime.restart"] {
		t.Fatal("runtime.restart was not advertised with explicit opt-in")
	}
	enabled.requestRestart()
	select {
	case <-enabled.RestartRequested():
	case <-time.After(time.Second):
		t.Fatal("accepted restart did not signal the process owner")
	}
}

func TestIdleChildLossDegradesReadinessUntilReload(t *testing.T) {
	supervisor, _, cwd := startStatefulPiFixture(t)
	id := createFixtureSession(t, supervisor, cwd)
	child := supervisor.children[id]
	killNativeProcess(child.cmd.Process)
	select {
	case <-child.done:
	case <-time.After(2 * time.Second):
		t.Fatal("lost child was not reaped")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if healthy, _ := supervisor.health(); !healthy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("idle child loss did not degrade readiness")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := supervisor.callHost(context.Background(), "session.load", map[string]any{"sessionId": id, "cwd": cwd}); err != nil {
		t.Fatal(err)
	}
	if healthy, detail := supervisor.health(); !healthy {
		t.Fatalf("reloaded session did not restore readiness: %s", detail)
	}
}

func TestActiveChildLossReachesSupervisorErrors(t *testing.T) {
	supervisor, _, cwd := startStatefulPiFixture(t)
	id := createFixtureSession(t, supervisor, cwd)
	prompt := make(chan error, 1)
	go func() {
		_, err := supervisor.callHost(context.Background(), "session.prompt", map[string]any{"sessionId": id, "content": []any{map[string]any{"type": "text", "text": "work"}}})
		prompt <- err
	}()
	child := supervisor.children[id]
	deadline := time.Now().Add(2 * time.Second)
	for {
		child.mu.Lock()
		active := child.activeRun != nil
		child.mu.Unlock()
		if active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("prompt never became active")
		}
		time.Sleep(time.Millisecond)
	}
	killNativeProcess(child.cmd.Process)
	select {
	case err := <-supervisor.errors:
		if err == nil {
			t.Fatal("active child loss reported a nil failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("active child loss did not reach the supervisor error channel")
	}
	if err := <-prompt; err == nil {
		t.Fatal("prompt survived active child loss")
	}
}

func TestIdleReleaseClosesOnlyResidentAndRetainsRegistry(t *testing.T) {
	supervisor, _, cwd := startStatefulPiFixture(t)
	first := createFixtureSession(t, supervisor, cwd)
	second := createFixtureSession(t, supervisor, cwd)
	if _, err := supervisor.callHost(context.Background(), "session.release", map[string]any{"sessionId": first, "cwd": filepath.Dir(cwd)}); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("mismatched release cwd error = %v", err)
	}
	if supervisor.children[first] == nil {
		t.Fatal("mismatched release cwd removed resident child")
	}
	if _, err := supervisor.callHost(context.Background(), "session.release", map[string]any{"sessionId": first, "cwd": cwd}); err != nil {
		t.Fatal(err)
	}
	supervisor.mu.Lock()
	_, firstResident := supervisor.children[first]
	_, firstRegistered := supervisor.sessions[first]
	_, secondResident := supervisor.children[second]
	supervisor.mu.Unlock()
	if firstResident || !firstRegistered || !secondResident {
		t.Fatalf("release residence: first=%v registered=%v second=%v", firstResident, firstRegistered, secondResident)
	}
	if _, err := supervisor.callHost(context.Background(), "session.load", map[string]any{"sessionId": first, "cwd": cwd}); err != nil {
		t.Fatal(err)
	}
}

func TestPromptAndReleaseAdmissionAreMutuallyExclusive(t *testing.T) {
	supervisor, _, cwd := startStatefulPiFixture(t)
	id := createFixtureSession(t, supervisor, cwd)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := supervisor.callHost(context.Background(), "session.prompt", map[string]any{"sessionId": id, "content": []any{map[string]any{"type": "text", "text": "race"}}})
		results <- err
	}()
	go func() {
		<-start
		_, err := supervisor.callHost(context.Background(), "session.release", map[string]any{"sessionId": id, "cwd": cwd})
		results <- err
	}()
	close(start)
	firstErr, secondErr := <-results, <-results
	if (firstErr == nil) == (secondErr == nil) {
		t.Fatalf("prompt/release outcomes must have one winner: %v, %v", firstErr, secondErr)
	}
}

func TestConcurrentReleaseHasOneCommittedWinner(t *testing.T) {
	supervisor, _, cwd := startStatefulPiFixture(t)
	id := createFixtureSession(t, supervisor, cwd)
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := supervisor.callHost(context.Background(), "session.release", map[string]any{"sessionId": id, "cwd": cwd})
			results <- err
		}()
	}
	close(start)
	firstErr, secondErr := <-results, <-results
	if (firstErr == nil) == (secondErr == nil) {
		t.Fatalf("concurrent release outcomes must have one winner: %v, %v", firstErr, secondErr)
	}
	if supervisor.children[id] != nil {
		t.Fatal("committed release retained resident child")
	}
}

func TestFailedOrStoppingChildCannotEnterCreateOrLoadRegistry(t *testing.T) {
	for _, stopping := range []bool{false, true} {
		t.Run(fmt.Sprintf("stopping=%v", stopping), func(t *testing.T) {
			cwd, agentDir := t.TempDir(), t.TempDir()
			path := filepath.Join(agentDir, "session.jsonl")
			if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			child := &nativeChild{cwd: cwd, sessionID: "session", sessionPath: path, stopping: stopping}
			if !stopping {
				child.failed = errors.New("fixture child failed")
			}
			created := newNativeSupervisor(Config{AgentDir: agentDir})
			if err := created.install("session", nativeSessionRef{Path: path, CWD: cwd}, child); err == nil {
				t.Fatal("failed create child was installed")
			}
			if len(created.sessions) != 0 || len(created.children) != 0 {
				t.Fatalf("failed create child changed registry: %#v %#v", created.sessions, created.children)
			}
			loaded := newNativeSupervisor(Config{AgentDir: agentDir})
			loaded.sessions["session"] = nativeSessionRef{Path: path, CWD: cwd}
			if _, err := loaded.installLoadedChild("session", child); err == nil {
				t.Fatal("failed load child was installed")
			}
			if len(loaded.children) != 0 {
				t.Fatalf("failed load child became resident: %#v", loaded.children)
			}
		})
	}
}

func TestUnsafeSessionSelectingPiArgsRejectedBeforeLaunch(t *testing.T) {
	for _, arg := range []string{"--resume", "--session=abc", "--fork", "--prompt=hello", "--no-session", "-p"} {
		if _, err := preparePiArgs([]string{arg}); err == nil {
			t.Errorf("unsafe Pi argument %q was accepted", arg)
		}
	}
}

func TestSupervisorBoundsResidentChildrenAndInstallationOwner(t *testing.T) {
	supervisor := newNativeSupervisor(Config{PiExecutable: "/bin/true", AgentDir: t.TempDir()})
	for index := 0; index < nativeMaxChildren; index++ {
		supervisor.children[strconv.Itoa(index)] = &nativeChild{}
	}
	if _, err := supervisor.launch(context.Background(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("resident capacity error = %v", err)
	}
	agentDir := t.TempDir()
	first := newNativeSupervisor(Config{PiExecutable: "/bin/true", AgentDir: agentDir})
	if err := first.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := newNativeSupervisor(Config{PiExecutable: "/bin/true", AgentDir: agentDir})
	if err := second.start(context.Background()); err == nil {
		t.Fatal("second installation owner acquired the same lock")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := first.close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestStartRejectsNonLoopbackAndCloseIsIdempotent(t *testing.T) {
	if _, err := Start(context.Background(), Config{Host: "0.0.0.0", Secret: testSecret}); err == nil {
		t.Fatal("non-loopback bind was accepted")
	}
	handle, err := Start(context.Background(), Config{Host: "localhost", Port: 0, Secret: testSecret})
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	client := http.Client{Timeout: time.Second}
	if _, err := client.Get(strings.Replace(handle.Endpoint(), "ws://", "http://", 1) + "/../livez"); err == nil {
		// The server has been closed; this assertion merely documents that Close
		// is effective rather than testing a transport-specific error string.
		t.Fatal("closed assistant still served HTTP")
	}
}

func TestHostRejectsUnsafeRequestID(t *testing.T) {
	handle, err := Start(context.Background(), Config{Host: "127.0.0.1", Port: 0, Secret: testSecret})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close(context.Background())
	header := http.Header{}
	header.Set("Authorization", "Bearer "+testSecret)
	connection, _, err := websocket.Dial(context.Background(), handle.Endpoint(), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "test complete")
	if err := connection.Write(context.Background(), websocket.MessageText, []byte(`{"id":9007199254740992,"method":"runtime.hello","params":{"protocolVersion":1}}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := connection.Read(context.Background()); err == nil {
		t.Fatal("unsafe request ID was accepted")
	}
}

func obsoleteMethodRPCTransportFixture(t *testing.T) {
	agentDir := t.TempDir()
	fixture := filepath.Join(t.TempDir(), "fake-pi")
	const script = `#!/bin/sh
printf '%s\n' "$@" > "$PI_CODING_AGENT_DIR/fake-argv"
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *hello*) printf '{"id":%s,"type":"hello","protocolVersion":1,"result":{"capabilities":{"sessions":1}}}\n' "$id" ;;
    *runtime.shutdown*) printf '{"id":%s,"result":{}}\n' "$id"; exit 0 ;;
    *session.prompt*) printf '{"method":"session.event","params":{"sessionId":"fixture","event":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}}}\n'; printf '{"id":%s,"result":{"stopReason":"end_turn"}}\n' "$id" ;;
    *session.list*) printf '{"id":%s,"result":{"sessions":[{"sessionId":"fixture","cwd":"/tmp/project"}]}}\n' "$id" ;;
    *) printf '{"id":%s,"result":{"sessionId":"fixture","capabilities":{"sessions":1},"configOptions":[],"messages":[]}}\n' "$id" ;;
  esac
done
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	handle, err := Start(context.Background(), Config{
		Host:         "127.0.0.1",
		Port:         0,
		Secret:       testSecret,
		AgentDir:     agentDir,
		PiExecutable: fixture,
		PiArgs:       []string{"--fixture-arg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close(context.Background())
	argv, err := os.ReadFile(filepath.Join(agentDir, "fake-argv"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(argv), "--fixture-arg") || !strings.Contains(string(argv), "--mode") {
		t.Fatalf("Pi argv = %q", argv)
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+testSecret)
	connection, _, err := websocket.Dial(context.Background(), handle.Endpoint(), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "test complete")
	call := func(id int, method string, params string) map[string]any {
		t.Helper()
		payload := []byte(`{"id":` + strconv.Itoa(id) + `,"method":"` + method + `","params":` + params + `}`)
		if err := connection.Write(context.Background(), websocket.MessageText, payload); err != nil {
			t.Fatal(err)
		}
		for {
			readContext, cancel := context.WithTimeout(context.Background(), time.Second)
			_, raw, readErr := connection.Read(readContext)
			cancel()
			if readErr != nil {
				t.Fatal(readErr)
			}
			var envelope map[string]any
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope["id"] == float64(id) {
				return envelope
			}
			if envelope["method"] != "session.event" {
				t.Fatalf("unexpected event frame %s", raw)
			}
		}
	}
	call(1, "runtime.hello", `{"protocolVersion":1}`)
	call(2, "session.list", `{}`)
	created := call(3, "session.create", `{"cwd":"/tmp/project","mcpServers":[]}`)
	if created["error"] != nil {
		t.Fatalf("create response = %#v", created)
	}
	call(4, "session.load", `{"sessionId":"fixture","cwd":"/tmp/project","mcpServers":[]}`)
	call(5, "session.prompt", `{"sessionId":"fixture","content":[{"type":"text","text":"Hi"}]}`)
	call(6, "session.configure", `{"sessionId":"fixture","configId":"thinkingLevel","value":"high"}`)
	call(7, "session.cancel", `{"sessionId":"fixture"}`)
}

func obsoleteEagerPiRPCFixture(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "pi")
	const script = `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *hello*) printf '{"id":%s,"type":"response","command":"hello","success":false,"error":"Unknown command"}\n' "$id" ;;
    *get_state*) printf '{"id":%s,"type":"response","command":"get_state","success":true,"data":{"sessionId":"official","sessionFile":"/tmp/official","cwd":"/tmp"}}\n' "$id" ;;
    *shutdown*) exit 0 ;;
  esac
done
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	handle, err := Start(context.Background(), Config{Host: "127.0.0.1", Port: 0, Secret: testSecret, PiExecutable: fixture})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close(context.Background())
	header := http.Header{"Authorization": []string{"Bearer " + testSecret}}
	connection, _, err := websocket.Dial(context.Background(), handle.Endpoint(), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "test complete")
	requests := []struct {
		id      int
		request string
	}{
		{1, `{"id":1,"method":"runtime.hello","params":{"protocolVersion":1}}`},
		{2, `{"id":2,"method":"session.list","params":{}}`},
	}
	for _, request := range requests {
		if err := connection.Write(context.Background(), websocket.MessageText, []byte(request.request)); err != nil {
			t.Fatal(err)
		}
		_, raw, err := connection.Read(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.ID != request.id {
			t.Fatalf("response id = %d, want %d", envelope.ID, request.id)
		}
		if request.id == 2 && !strings.Contains(string(envelope.Result), "official") {
			t.Fatalf("session.list result = %s", envelope.Result)
		}
	}
}

func TestMethodRPCChildIsRejected(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "pi")
	script := `#!/bin/sh
: > 'AGENT/timeout.jsonl'
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  printf '{"id":%s,"result":{}}\n' "$id"
done
`
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	supervisor := newNativeSupervisor(Config{PiExecutable: fixture, AgentDir: t.TempDir()})
	if err := supervisor.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err := supervisor.callHost(context.Background(), "session.create", map[string]any{"cwd": t.TempDir(), "mcpServers": []any{}})
	if err == nil {
		t.Fatalf("method RPC error = %v", err)
	}
	supervisor.mu.Lock()
	children, launching := len(supervisor.children), supervisor.launching
	supervisor.mu.Unlock()
	if children != 0 || launching != 0 {
		t.Fatalf("failed child consumed capacity: children=%d launching=%d", children, launching)
	}
}

func TestPromptAcceptanceTimeoutPoisonsSessionWithoutRetry(t *testing.T) {
	agentDir, cwd := t.TempDir(), t.TempDir()
	logPath := filepath.Join(agentDir, "prompt.log")
	fixture := filepath.Join(t.TempDir(), "pi")
	script := `#!/bin/sh
: > 'AGENT/timeout.jsonl'
while IFS= read -r line; do
 id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
 case "$line" in
  *'"type":"new_session"'*) printf '{"id":%s,"type":"response","command":"new_session","success":true,"data":{}}\n' "$id" ;;
  *'"type":"get_state"'*) printf '{"id":%s,"type":"response","command":"get_state","success":true,"data":{"sessionId":"timeout","sessionFile":"AGENT/timeout.jsonl"}}\n' "$id" ;;
  *'"type":"get_messages"'*) printf '{"id":%s,"type":"response","command":"get_messages","success":true,"data":{"messages":[]}}\n' "$id" ;;
  *'"type":"clear_queue"'*) printf '{"id":%s,"type":"response","command":"clear_queue","success":true,"data":{}}\n' "$id" ;;
  *'"type":"abort"'*) printf '{"id":%s,"type":"response","command":"abort","success":true,"data":{"aborted":false}}\n' "$id" ;;
  *'"type":"prompt"'*) printf 'prompt\n' >> 'LOG' ;;
  *'"type":"shutdown"'*) exit 0 ;;
 esac
done
`
	script = strings.ReplaceAll(strings.ReplaceAll(script, "AGENT", agentDir), "LOG", logPath)
	if err := os.WriteFile(fixture, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	supervisor := newNativeSupervisor(Config{PiExecutable: fixture, AgentDir: agentDir})
	if err := supervisor.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	id := createFixtureSession(t, supervisor, cwd)
	supervisor.children[id].acceptTimeout = 25 * time.Millisecond
	_, err := supervisor.callHost(context.Background(), "session.prompt", map[string]any{"sessionId": id, "content": []any{map[string]any{"type": "text", "text": "one"}}})
	if err == nil || !strings.Contains(err.Error(), "acceptance is uncertain") {
		t.Fatalf("acceptance timeout = %v", err)
	}
	if _, err := supervisor.callHost(context.Background(), "session.prompt", map[string]any{"sessionId": id, "content": []any{map[string]any{"type": "text", "text": "two"}}}); err == nil {
		t.Fatal("poisoned session accepted a second prompt")
	}
	raw, _ := os.ReadFile(logPath)
	if strings.Count(string(raw), "prompt\n") != 1 {
		t.Fatalf("prompt was retried: %s", raw)
	}
}

type blockingWriteCloser struct {
	mu     sync.Mutex
	writes int
	closed chan struct{}
	once   sync.Once
}

func (w *blockingWriteCloser) Write([]byte) (int, error) {
	w.mu.Lock()
	w.writes++
	w.mu.Unlock()
	<-w.closed
	return 0, errors.New("closed")
}
func (w *blockingWriteCloser) Close() error {
	w.once.Do(func() { close(w.closed) })
	return nil
}

func TestStalledNativeWriteIsFatalAndNoSecondWriteStarts(t *testing.T) {
	writer := &blockingWriteCloser{closed: make(chan struct{})}
	child := &nativeChild{stdin: writer, writes: make(chan nativeWrite, 2), done: make(chan struct{}), errors: make(chan error, 1), pending: map[uint64]nativePending{}, writeTimeout: 20 * time.Millisecond}
	go child.writerLoop()
	if err := child.writeRecord(context.Background(), []byte("one\n")); err == nil || !strings.Contains(err.Error(), "stalled") {
		t.Fatalf("stalled write = %v", err)
	}
	if err := child.writeRecord(context.Background(), []byte("two\n")); err == nil {
		t.Fatal("second write was accepted")
	}
	time.Sleep(10 * time.Millisecond)
	writer.mu.Lock()
	writes := writer.writes
	writer.mu.Unlock()
	if writes != 1 {
		t.Fatalf("native writes = %d, want 1", writes)
	}
}

func TestPrivateAssistantRequiresSecretForWebSocketAndReadiness(t *testing.T) {
	handle, err := Start(context.Background(), Config{Host: "127.0.0.1", Port: 0, Secret: testSecret})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close(context.Background())

	endpoint := strings.TrimSuffix(strings.Replace(handle.Endpoint(), "ws://", "http://", 1), "/pi")
	response, err := http.Get(endpoint + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated readiness status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}

	wrong := http.Header{}
	wrong.Set("Authorization", "Bearer wrong-secret-012345678901234567890123")
	request, err := http.NewRequest(http.MethodGet, endpoint+"/readyz", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header = wrong
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong-secret readiness status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}

	connection, _, err := websocket.Dial(context.Background(), handle.Endpoint(), nil)
	if err == nil {
		connection.Close(websocket.StatusNormalClosure, "test complete")
		t.Fatal("unauthenticated WebSocket was accepted")
	}

	authorized := http.Header{}
	authorized.Set("Authorization", "Bearer "+testSecret)
	request, err = http.NewRequest(http.MethodGet, endpoint+"/readyz", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header = authorized
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authorized readiness status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}

func TestStartRequiresMinimumSecretLength(t *testing.T) {
	if _, err := Start(context.Background(), Config{Host: "127.0.0.1", Port: 0, Secret: strings.Repeat("x", minSecretLength-1)}); err == nil {
		t.Fatal("short assistant secret was accepted")
	}
}
