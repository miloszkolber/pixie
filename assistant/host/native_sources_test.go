package host

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func startAgentSupervisor(t *testing.T) (*nativeSupervisor, string, string) {
	t.Helper()
	agentDir := t.TempDir()
	cwd := t.TempDir()
	supervisor := newNativeSupervisor(Config{PiExecutable: "/bin/true", AgentDir: agentDir})
	if err := supervisor.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := supervisor.close(ctx); err != nil {
			t.Errorf("close supervisor: %v", err)
		}
	})
	return supervisor, agentDir, cwd
}

func callAgent(t *testing.T, supervisor *nativeSupervisor, method string, params map[string]any) map[string]any {
	t.Helper()
	raw, err := supervisor.callHost(context.Background(), method, params)
	if err != nil {
		t.Fatalf("%s %#v: %v", method, params, err)
	}
	result := map[string]any{}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("%s result %s: %v", method, raw, err)
	}
	return result
}

func callAgentError(t *testing.T, supervisor *nativeSupervisor, method string, params map[string]any) error {
	t.Helper()
	_, err := supervisor.callHost(context.Background(), method, params)
	if err == nil {
		t.Fatalf("%s %#v unexpectedly succeeded", method, params)
	}
	return err
}

func TestAgentSourcesCRUDMirrorsLegacyContract(t *testing.T) {
	supervisor, agentDir, cwd := startAgentSupervisor(t)

	created := callAgent(t, supervisor, "pi.sources.create", map[string]any{
		"type": "agent", "name": "Reviewer", "description": "Review",
		"content": "Review carefully",
		"properties": map[string]any{
			"model": "fixture/echo", "custom": "keep",
		},
	})
	source, ok := created["source"].(map[string]any)
	if !ok {
		t.Fatalf("create source = %#v", created)
	}
	if source["type"] != "agent" || source["name"] != "Reviewer" || source["description"] != "Review" ||
		source["content"] != "Review carefully" || source["global"] != true || source["writable"] != true ||
		source["executionEligibility"] != "unknown" {
		t.Fatalf("create source = %#v", source)
	}
	wantPath := filepath.Join(agentDir, "agents", "Reviewer.md")
	if source["path"] != wantPath {
		t.Fatalf("create path = %v, want %v", source["path"], wantPath)
	}
	revision, _ := source["revision"].(string)
	if !strings.HasPrefix(revision, "sha256:") || len(revision) != len("sha256:")+64 {
		t.Fatalf("create revision = %q", revision)
	}
	properties, _ := source["properties"].(map[string]any)
	if properties["model"] != "fixture/echo" || properties["custom"] != "keep" {
		t.Fatalf("create properties = %#v", properties)
	}

	listed := callAgent(t, supervisor, "pi.sources.list", map[string]any{"type": "agent", "projectDir": cwd})
	sources, _ := listed["sources"].([]any)
	if len(sources) != 1 {
		t.Fatalf("list sources = %#v", listed)
	}
	warnings, _ := listed["warnings"].([]any)
	if len(warnings) != 0 {
		t.Fatalf("list warnings = %#v", warnings)
	}

	mentions := callAgent(t, supervisor, "pi.agent-mentions.list", map[string]any{"cwd": cwd})
	agents, _ := mentions["agents"].([]any)
	if len(agents) != 1 {
		t.Fatalf("mentions = %#v", mentions)
	}
	if first, _ := agents[0].(map[string]any); first["mention"] != "@Reviewer" || first["sourceType"] != "agent" || first["name"] != "Reviewer" {
		t.Fatalf("mention entry = %#v", agents[0])
	}

	updated := callAgent(t, supervisor, "pi.sources.update", map[string]any{
		"type": "agent", "path": wantPath, "expectedRevision": revision,
		"name": "Renamed", "description": "Changed", "content": "Review carefully",
		"properties": map[string]any{"model": nil},
	})
	updatedSource, _ := updated["source"].(map[string]any)
	updatedProperties, _ := updatedSource["properties"].(map[string]any)
	if updatedSource["name"] != "Renamed" || updatedSource["description"] != "Changed" {
		t.Fatalf("updated source = %#v", updatedSource)
	}
	if _, present := updatedProperties["model"]; present {
		t.Fatalf("null model was not removed: %#v", updatedProperties)
	}
	if updatedProperties["custom"] != "keep" {
		t.Fatalf("unknown property was not preserved: %#v", updatedProperties)
	}
	updatedRevision, _ := updatedSource["revision"].(string)
	if updatedRevision == revision || updatedRevision == "" {
		t.Fatalf("revision did not change: %q", updatedRevision)
	}
	raw, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "name: Renamed") || !strings.Contains(string(raw), "Review carefully") {
		t.Fatalf("saved document = %s", raw)
	}

	if err := callAgentError(t, supervisor, "pi.sources.update", map[string]any{
		"type": "agent", "path": wantPath, "expectedRevision": revision,
		"name": "Renamed", "content": "Stale edit",
	}); !strings.Contains(err.Error(), "changed on disk") {
		t.Fatalf("stale update error = %v", err)
	}
	raw, _ = os.ReadFile(wantPath)
	if !strings.Contains(string(raw), "Changed") {
		t.Fatalf("stale update modified the file: %s", raw)
	}

	if err := callAgentError(t, supervisor, "pi.sources.delete", map[string]any{
		"type": "agent", "path": wantPath, "expectedRevision": revision,
	}); !strings.Contains(err.Error(), "changed on disk") {
		t.Fatalf("stale delete error = %v", err)
	}
	deleted := callAgent(t, supervisor, "pi.sources.delete", map[string]any{
		"type": "agent", "path": wantPath, "expectedRevision": updatedRevision,
	})
	if deleted["ok"] != true {
		t.Fatalf("delete result = %#v", deleted)
	}
	listed = callAgent(t, supervisor, "pi.sources.list", map[string]any{"type": "agent"})
	if len(listed["sources"].([]any)) != 0 {
		t.Fatalf("list after delete = %#v", listed)
	}
}

func TestAgentProjectScopeAndGlobalListing(t *testing.T) {
	supervisor, agentDir, cwd := startAgentSupervisor(t)
	globalPath := filepath.Join(agentDir, "agents", "Global.md")
	projectDir := filepath.Join(cwd, ".pi", "agents")
	projectPath := filepath.Join(projectDir, "Project.md")

	callAgent(t, supervisor, "pi.sources.create", map[string]any{
		"type": "agent", "name": "Global", "content": "global",
	})
	callAgent(t, supervisor, "pi.sources.create", map[string]any{
		"type": "agent", "name": "Project", "content": "project",
		"target": map[string]any{"scope": "projectDir", "projectDir": cwd},
	})
	listed := callAgent(t, supervisor, "pi.sources.list", map[string]any{"type": "agent", "projectDir": cwd})
	sources, _ := listed["sources"].([]any)
	if len(sources) != 2 {
		t.Fatalf("project+global sources = %#v", listed)
	}
	byPath := map[string]map[string]any{}
	for _, value := range sources {
		entry, _ := value.(map[string]any)
		path, _ := entry["path"].(string)
		byPath[path] = entry
	}
	if byPath[globalPath] == nil || byPath[projectPath] == nil {
		t.Fatalf("expected global %q and project %q in %#v", globalPath, projectPath, byPath)
	}
	if byPath[globalPath]["global"] != true || byPath[projectPath]["global"] != false {
		t.Fatalf("scope flags = %#v / %#v", byPath[globalPath]["global"], byPath[projectPath]["global"])
	}
	mentions := callAgent(t, supervisor, "pi.agent-mentions.list", map[string]any{"cwd": cwd})
	if agents, _ := mentions["agents"].([]any); len(agents) != 2 {
		t.Fatalf("mentions = %#v", mentions)
	}
	// A project operation without the project directory must not see the file.
	projectRevision, _ := byPath[projectPath]["revision"].(string)
	if err := callAgentError(t, supervisor, "pi.sources.delete", map[string]any{
		"type": "agent", "path": projectPath, "expectedRevision": projectRevision,
	}); !strings.Contains(err.Error(), "Unknown agent source") {
		t.Fatalf("scopeless project delete error = %v", err)
	}
	callAgent(t, supervisor, "pi.sources.delete", map[string]any{
		"type": "agent", "path": projectPath, "expectedRevision": projectRevision, "projectDir": cwd,
	})
}

func TestAgentSourcesRejectOversizedStaleTraversalAndUnknown(t *testing.T) {
	supervisor, agentDir, _ := startAgentSupervisor(t)
	created := callAgent(t, supervisor, "pi.sources.create", map[string]any{
		"type": "agent", "name": "Reviewer", "content": "Review",
	})
	source, _ := created["source"].(map[string]any)
	path, _ := source["path"].(string)
	revision, _ := source["revision"].(string)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := callAgentError(t, supervisor, "pi.sources.update", map[string]any{
		"type": "agent", "path": path, "expectedRevision": revision,
		"name": "Reviewer", "content": strings.Repeat("é", 40000),
	}); !strings.Contains(err.Error(), "65536 bytes") {
		t.Fatalf("oversized update error = %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatalf("oversized update changed the file: %s", after)
	}
	if err := callAgentError(t, supervisor, "pi.sources.create", map[string]any{
		"type": "agent", "name": "../escape", "content": "no",
	}); !strings.Contains(err.Error(), "Invalid agent name") {
		t.Fatalf("traversal name error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "escape.md")); !os.IsNotExist(err) {
		t.Fatalf("traversal name created a file: %v", err)
	}
	if err := callAgentError(t, supervisor, "pi.sources.create", map[string]any{
		"type": "agent", "name": "Other", "content": "x", "expectedRevision": revision,
	}); !strings.Contains(err.Error(), "not valid for create") {
		t.Fatalf("create revision error = %v", err)
	}
	if err := callAgentError(t, supervisor, "pi.sources.update", map[string]any{
		"type": "agent", "path": filepath.Join(agentDir, "agents", "Missing.md"),
		"expectedRevision": revision, "name": "Missing", "content": "x",
	}); !strings.Contains(err.Error(), "Unknown agent source") {
		t.Fatalf("unknown update error = %v", err)
	}
}

func TestAgentCreateIsExclusive(t *testing.T) {
	supervisor, _, _ := startAgentSupervisor(t)
	params := map[string]any{"type": "agent", "name": "Concurrent", "content": "First"}
	second := map[string]any{"type": "agent", "name": "Concurrent", "content": "Second"}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, request := range []map[string]any{params, second} {
		wait.Add(1)
		go func(payload map[string]any) {
			defer wait.Done()
			<-start
			_, err := supervisor.callHost(context.Background(), "pi.sources.create", payload)
			results <- err
		}(request)
	}
	close(start)
	wait.Wait()
	close(results)
	successes, failures := 0, 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		failures++
		if !strings.Contains(err.Error(), "Agent already exists") {
			t.Fatalf("losing create error = %v", err)
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("concurrent create outcomes: %d success, %d failure", successes, failures)
	}
	listed := callAgent(t, supervisor, "pi.sources.list", map[string]any{"type": "agent"})
	if len(listed["sources"].([]any)) != 1 {
		t.Fatalf("list after concurrent create = %#v", listed)
	}
}

func TestAgentCreateRejectsSymlinkedRoot(t *testing.T) {
	supervisor, agentDir, _ := startAgentSupervisor(t)
	outside := t.TempDir()
	root := filepath.Join(agentDir, "agents")
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, root); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := callAgentError(t, supervisor, "pi.sources.create", map[string]any{
		"type": "agent", "name": "Escaped", "content": "Nope",
	}); !strings.Contains(err.Error(), "rooted") {
		t.Fatalf("symlinked root error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "Escaped.md")); !os.IsNotExist(err) {
		t.Fatalf("symlinked root wrote outside: %v", err)
	}
}

func TestAgentListWarnsAndPreservesUnrelatedFiles(t *testing.T) {
	supervisor, agentDir, _ := startAgentSupervisor(t)
	agentsDir := filepath.Join(agentDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "broken.md"), []byte("---\nbad: [\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "notes.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	callAgent(t, supervisor, "pi.sources.create", map[string]any{
		"type": "agent", "name": "Valid", "content": "ok",
	})
	listed := callAgent(t, supervisor, "pi.sources.list", map[string]any{"type": "agent"})
	if sources, _ := listed["sources"].([]any); len(sources) != 1 {
		t.Fatalf("sources = %#v", listed)
	}
	warnings, _ := listed["warnings"].([]any)
	if len(warnings) != 1 || !strings.Contains(warnings[0].(string), "broken.md") {
		t.Fatalf("warnings = %#v", warnings)
	}
	if data, err := os.ReadFile(filepath.Join(agentsDir, "notes.txt")); err != nil || string(data) != "keep" {
		t.Fatalf("unrelated file changed: %q %v", data, err)
	}
}

func TestAgentMentionsRequireCWDOrProject(t *testing.T) {
	supervisor, agentDir, cwd := startAgentSupervisor(t)
	callAgent(t, supervisor, "pi.sources.create", map[string]any{
		"type": "agent", "name": "Global", "content": "global",
	})
	callAgent(t, supervisor, "pi.sources.create", map[string]any{
		"type": "agent", "name": "Project", "content": "project",
		"target": map[string]any{"scope": "projectDir", "projectDir": cwd},
	})
	globalOnly := callAgent(t, supervisor, "pi.agent-mentions.list", map[string]any{})
	if agents, _ := globalOnly["agents"].([]any); len(agents) != 1 {
		t.Fatalf("global-only mentions = %#v", globalOnly)
	}
	project := callAgent(t, supervisor, "pi.agent-mentions.list", map[string]any{"cwd": cwd})
	if agents, _ := project["agents"].([]any); len(agents) != 2 {
		t.Fatalf("project mentions = %#v", project)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "agents", "Global.md")); err != nil {
		t.Fatalf("global agent missing: %v", err)
	}
}
