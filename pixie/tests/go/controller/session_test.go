package controller_test

import (
	"context"
	"encoding/json"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/miloszkolber/pixie/internal/controller"
	"github.com/miloszkolber/pixie/internal/persist"
	"github.com/miloszkolber/pixie/internal/workspace"
)

func newSessionManager(t *testing.T, loadUpdates []map[string]any, promptRequests chan<- map[string]any) (*controller.SessionManager, *controller.PiClient, workspace.Project, persist.Store) {
	return newSessionManagerWithInitialize(t, loadUpdates, promptRequests, piInitializeResponse())
}

func newSessionManagerWithInitialize(t *testing.T, loadUpdates []map[string]any, promptRequests chan<- map[string]any, initialize map[string]any) (*controller.SessionManager, *controller.PiClient, workspace.Project, persist.Store) {
	return newSessionManagerWithInitializeAndPublisher(t, loadUpdates, promptRequests, initialize, nil)
}

func newSessionManagerWithPublisher(t *testing.T, publish controller.SessionPublisher) (*controller.SessionManager, *controller.PiClient, workspace.Project, persist.Store) {
	return newSessionManagerWithInitializeAndPublisher(t, nil, nil, piInitializeResponse(), publish)
}

func newSessionManagerWithInitializeAndPublisher(t *testing.T, loadUpdates []map[string]any, promptRequests chan<- map[string]any, initialize map[string]any, publish controller.SessionPublisher, observers ...func(string, map[string]any)) (*controller.SessionManager, *controller.PiClient, workspace.Project, persist.Store) {
	t.Helper()
	ctx := t.Context()
	root := t.TempDir()
	policy, err := workspace.NewPathPolicy([]string{root}, false)
	if err != nil {
		t.Fatal(err)
	}
	store := persist.Store{Dir: t.TempDir()}
	projects := workspace.NewProjects(store, policy)
	project, err := projects.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	records := controller.NewSessionRecords(store)
	if err := records.Record(controller.ProjectSessionRecord{ProjectID: project.ID, SessionID: "chat", CWD: project.Roots[0]}); err != nil {
		t.Fatal(err)
	}
	manager := controller.NewSessionManager(projects, policy, records, controller.NewSessionQueues(store), controller.NewObjectives(store), publish)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		for {
			_, payload, err := connection.Read(ctx)
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
			for _, observer := range observers {
				observer(rpc.Method, rpc.Params)
			}
			result := any(map[string]any{})
			switch rpc.Method {
			case "runtime.hello":
				result = initialize
			case "session.load":
				for _, loadUpdate := range loadUpdates {
					method := "session.update"
					if loadUpdate["__piOnly"] == true {
						method = "pi.session.update"
						loadUpdate = maps.Clone(loadUpdate)
						delete(loadUpdate, "__piOnly")
					}
					if writeRPC(connection, map[string]any{
						"jsonrpc": "2.0", "method": method,
						"params": map[string]any{"sessionId": "chat", "update": loadUpdate},
					}) != nil {
						return
					}
				}
			case "session.prompt":
				if promptRequests != nil {
					promptRequests <- map[string]any{"connection": connection, "id": rpc.ID, "params": rpc.Params}
					continue
				}
			}
			if len(rpc.ID) > 0 && writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result}) != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", manager)
	manager.SetClient(client)
	t.Cleanup(client.Close)
	return manager, client, project, store
}

type publishedEvent struct {
	channel string
	data    any
}

func TestUserObjectiveAndThinkingMutationsPublishAuthoritativeState(t *testing.T) {
	events := make(chan publishedEvent, 16)
	manager, _, project, _ := newSessionManagerWithPublisher(t, func(channel string, data any) {
		events <- publishedEvent{channel: channel, data: data}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}

	goal := "Keep every connected client current"
	updated, err := manager.UpdateObjective(ctx, project.ID, "chat", &goal, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertPublishedObjective(t, nextPublishedEvent(t, events, "session.objectiveChanged"), updated)

	cleared, err := manager.ClearObjectiveGoal(ctx, project.ID, "chat")
	if err != nil {
		t.Fatal(err)
	}
	assertPublishedObjective(t, nextPublishedEvent(t, events, "session.objectiveChanged"), cleared)

	if err := manager.SetThinking(ctx, "chat", "high"); err != nil {
		t.Fatal(err)
	}
	thinking := nextPublishedEvent(t, events, "agent.event")
	payload, ok := thinking.data.(map[string]any)
	if !ok || payload["sessionId"] != "chat" {
		t.Fatalf("thinking payload: %#v", thinking.data)
	}
	event, ok := payload["event"].(map[string]any)
	if !ok || event["type"] != "config" {
		t.Fatalf("thinking event: %#v", payload["event"])
	}
}

func TestSessionInfoTitleIsDurableAcrossControllerRestart(t *testing.T) {
	manager, _, project, store := newSessionManager(t, []map[string]any{{
		"sessionUpdate": "session_info_update",
		"title":         "Generated title",
	}}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client-a"); err != nil {
		t.Fatal(err)
	}
	records, err := controller.NewSessionRecords(store).List()
	if err != nil || len(records) != 1 || records[0].Title != "Generated title" {
		t.Fatalf("generated title was not persisted: %#v, %v", records, err)
	}
}

func nextPublishedEvent(t *testing.T, events <-chan publishedEvent, channel string) publishedEvent {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event.channel == channel {
				return event
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for published %s event", channel)
		}
	}
}

func assertPublishedObjective(t *testing.T, event struct {
	channel string
	data    any
}, want controller.SessionGoal) {
	t.Helper()
	if event.channel != "session.objectiveChanged" {
		t.Fatalf("objective channel: got %q", event.channel)
	}
	got, ok := event.data.(controller.SessionGoal)
	if !ok || got.SessionID != want.SessionID || got.ProjectID != want.ProjectID || !equalOptionalString(got.Goal, want.Goal) || len(got.Tasks) != len(want.Tasks) {
		t.Fatalf("objective event: got %#v, want %#v", event.data, want)
	}
}

func equalOptionalString(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func TestPiUsageUpdateProjectsCumulativeTokenStats(t *testing.T) {
	manager, _, project, _ := newSessionManager(t, []map[string]any{{
		"__piOnly":                true,
		"sessionUpdate":           "usage_update",
		"accumulatedInputTokens":  120,
		"accumulatedOutputTokens": 30,
		"accumulatedCost":         0.125,
		"costCurrency":            "USD",
		"contextLimit":            200_000,
		"used":                    5_000,
	}}, nil)
	if _, err := manager.Messages(t.Context(), "chat", project.ID, project.Roots[0], "client"); err != nil {
		t.Fatal(err)
	}
	stats, err := manager.Stats("chat")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Tokens.Input != 120 || stats.Tokens.Output != 30 || stats.Tokens.Total != 150 || stats.Cost != 0.125 || stats.CostCurrency != "USD" || stats.Reported["total"] != true {
		t.Fatalf("usage stats were not projected: %#v", stats)
	}
	if stats.ContextUsage["tokens"] != int64(5000) || stats.ContextUsage["contextWindow"] != int64(200000) {
		t.Fatalf("context usage was not projected: %#v", stats.ContextUsage)
	}
}

func TestPromptEmbedsBoundedTextResourcesWithImages(t *testing.T) {
	requests := make(chan map[string]any, 1)
	manager, _, project, _ := newSessionManager(t, nil, requests)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Prompt(ctx, "chat", "Review both attachments", []controller.ImageContent{{Type: "image", MimeType: "image/png", Data: "AA=="}}, []controller.TextResourceAttachment{{Type: "text", Name: "review.ts", MimeType: "text/x-typescript", Text: "export const answer = 42\n"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-requests:
		prompt, ok := request["params"].(map[string]any)["content"].([]any)
		if !ok || len(prompt) != 3 {
			t.Fatalf("prompt blocks: %#v", request)
		}
		image, _ := prompt[1].(map[string]any)
		if image["type"] != "image" || image["mimeType"] != "image/png" {
			t.Fatalf("image block: %#v", image)
		}
		resource, _ := prompt[2].(map[string]any)
		embedded, _ := resource["resource"].(map[string]any)
		if resource["type"] != "resource" || embedded["uri"] != "pixie://attachment/review.ts" || embedded["mimeType"] != "text/x-typescript" || embedded["text"] != "export const answer = 42\n" {
			t.Fatalf("text resource block: %#v", resource)
		}
	case <-ctx.Done():
		t.Fatal("prompt was not sent")
	}
}

func TestPromptRejectsUnsupportedAndInvalidTextResources(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	manager, _, project, _ := newSessionManager(t, nil, nil)
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		resources []controller.TextResourceAttachment
		contains  string
	}{
		{name: "binary", resources: []controller.TextResourceAttachment{{Type: "text", Name: "binary.ts", MimeType: "text/x-typescript", Text: "bad\x00text"}}, contains: "malformed"},
		{name: "oversize", resources: []controller.TextResourceAttachment{{Type: "text", Name: "large.txt", MimeType: "text/plain", Text: strings.Repeat("x", 1024*1024+1)}}, contains: "1 MiB"},
		{name: "request count", resources: []controller.TextResourceAttachment{{Type: "text", Name: "one.txt", MimeType: "text/plain", Text: "x"}, {Type: "text", Name: "two.txt", MimeType: "text/plain", Text: "x"}, {Type: "text", Name: "three.txt", MimeType: "text/plain", Text: "x"}, {Type: "text", Name: "four.txt", MimeType: "text/plain", Text: "x"}, {Type: "text", Name: "five.txt", MimeType: "text/plain", Text: "x"}}, contains: "4 files"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := manager.Prompt(ctx, "chat", "", nil, test.resources); err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("invalid text attachment accepted: %v", err)
			}
		})
	}
}

func TestTextResourceReplayProjectsOnlyBoundedAttachmentMarkers(t *testing.T) {
	oversized := strings.Repeat("x", 1024*1024+1)
	manager, _, project, _ := newSessionManager(t, []map[string]any{
		{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "Review these"}},
		{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "resource", "resource": map[string]any{"uri": "pixie://attachment/review.ts", "mimeType": "text/x-typescript", "text": "const hidden = true\n"}}},
		{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "resource", "resource": map[string]any{"uri": "pixie://attachment/notes.md", "mimeType": "text/markdown", "text": "# private notes\n"}}},
		{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "resource", "resource": map[string]any{"uri": "file:///etc/passwd", "mimeType": "text/plain", "text": "root"}}},
		{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "resource", "resource": map[string]any{"uri": "pixie://attachment/large.txt", "mimeType": "text/plain", "text": oversized}}},
		{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "Done"}},
		{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "resource", "resource": map[string]any{"uri": "pixie://attachment/follow-up.txt", "mimeType": "text/plain", "text": "resource-only source"}}},
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snapshot, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client")
	if err != nil {
		t.Fatal(err)
	}
	messages := snapshot["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("replay messages: %#v", messages)
	}
	first := messages[0].(map[string]any)
	blocks := first["content"].([]any)
	if len(blocks) != 3 || blocks[1].(map[string]any)["name"] != "review.ts" || blocks[2].(map[string]any)["name"] != "notes.md" {
		t.Fatalf("text resource markers: %#v", blocks)
	}
	last := messages[2].(map[string]any)["content"].([]any)
	if len(last) != 1 || last[0].(map[string]any)["name"] != "follow-up.txt" {
		t.Fatalf("resource-only marker: %#v", last)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil || strings.Contains(string(encoded), "private notes") || strings.Contains(string(encoded), "resource-only source") || strings.Contains(string(encoded), oversized[:64]) {
		t.Fatalf("resource source leaked into replay: %v %s", err, encoded)
	}
}

func TestTextResourceEchoDoesNotDuplicateOptimisticMarkers(t *testing.T) {
	requests := make(chan map[string]any, 1)
	manager, _, project, _ := newSessionManager(t, nil, requests)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client"); err != nil {
		t.Fatal(err)
	}
	resources := []controller.TextResourceAttachment{
		{Type: "text", Name: "review.ts", MimeType: "text/x-typescript", Text: "const hidden = true\n"},
		{Type: "text", Name: "notes.md", MimeType: "text/markdown", Text: "# private notes\n"},
	}
	if err := manager.Prompt(ctx, "chat", "Review these", nil, resources); err != nil {
		t.Fatal(err)
	}
	request := <-requests
	connection := request["connection"].(*websocket.Conn)
	for _, update := range []map[string]any{
		{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "Review these"}},
		{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "resource", "resource": map[string]any{"uri": "pixie://attachment/review.ts", "mimeType": "text/x-typescript", "text": resources[0].Text}}},
		{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "resource", "resource": map[string]any{"uri": "pixie://attachment/notes.md", "mimeType": "text/markdown", "text": resources[1].Text}}},
	} {
		if err := writeRPC(connection, map[string]any{"jsonrpc": "2.0", "method": "session.update", "params": map[string]any{"sessionId": "chat", "update": update}}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client")
	if err != nil {
		t.Fatal(err)
	}
	messages := snapshot["messages"].([]any)
	if len(messages) != 1 || len(messages[0].(map[string]any)["content"].([]any)) != 3 {
		t.Fatalf("echo duplicated resource markers: %#v", messages)
	}
	if err := writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"stopReason": "end_turn"}}); err != nil {
		t.Fatal(err)
	}
}

func TestQuestionsStaySessionBoundAndSingleUse(t *testing.T) {
	questionArgs := map[string]any{"questions": []any{map[string]any{
		"header": "Choice", "question": "Choose one", "options": []any{map[string]any{"label": "A", "description": "First option"}},
	}}}
	manager, client, project, _ := newSessionManager(t, []map[string]any{
		{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "Need input"}},
		{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "Choose carefully"}},
		{"sessionUpdate": "tool_call", "toolCallId": "question-tool", "title": "ask_user_question", "rawInput": questionArgs},
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snapshot, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client")
	if err != nil {
		t.Fatal(err)
	}
	assertReplay := func(snapshot map[string]any) {
		t.Helper()
		messages := snapshot["messages"].([]any)
		if len(messages) != 2 || messages[0].(map[string]any)["role"] != "user" || messages[1].(map[string]any)["role"] != "assistant" {
			t.Fatalf("replay message ordering: %#v", messages)
		}
		assistant := messages[1].(map[string]any)["content"].([]any)
		if len(assistant) != 2 || assistant[0].(map[string]any)["text"] != "Choose carefully" || assistant[1].(map[string]any)["id"] != "question-tool" {
			t.Fatalf("replay tool ordering: %#v", assistant)
		}
	}
	assertReplay(snapshot)
	client.Reset()
	snapshot, err = manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client")
	if err != nil {
		t.Fatal(err)
	}
	assertReplay(snapshot)

	emitLiveQuestion(t, manager, "question-tool", questionArgs)
	questionResult := make(chan map[string]any, 1)
	go func() {
		result, _ := manager.AskQuestion(ctx, "chat", questionArgs)
		questionResult <- result
	}()
	malformed := map[string]any{"answers": []any{map[string]any{"questionIndex": 2}}, "cancelled": false}
	for {
		err := manager.ResolveQuestion("chat", "question-tool", malformed)
		if err != nil && strings.Contains(err.Error(), "no longer awaiting") {
			select {
			case <-ctx.Done():
				t.Fatal("question did not become pending")
			case <-time.After(time.Millisecond):
				continue
			}
		}
		if err == nil || !strings.Contains(err.Error(), "malformed") {
			t.Fatalf("malformed question response: %v", err)
		}
		break
	}
	answer := map[string]any{"answers": []any{map[string]any{
		"questionIndex": 0, "question": "Choose one", "kind": "option", "answer": "A",
	}}, "cancelled": false}
	if err := manager.ResolveQuestion("other", "question-tool", answer); err == nil {
		t.Fatal("question response crossed session ownership")
	}
	if err := manager.ResolveQuestion("chat", "question-tool", answer); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-questionResult:
		if result["cancelled"] != false || len(result["answers"].([]any)) != 1 {
			t.Fatalf("question result: %#v", result)
		}
	case <-ctx.Done():
		t.Fatal("question result did not settle")
	}
	if err := manager.ResolveQuestion("chat", "question-tool", answer); err == nil {
		t.Fatal("question response was accepted twice")
	}
}

func TestConcurrentPromptQueuesAndLifecycleRemainSerialized(t *testing.T) {
	requests := make(chan map[string]any, 4)
	manager, _, project, store := newSessionManager(t, nil, requests)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Prompt(ctx, "chat", "first", nil, nil); err != nil {
		t.Fatal(err)
	}
	first := <-requests
	if err := manager.Prompt(ctx, "chat", "overlap", nil, nil); err == nil {
		t.Fatal("accepted a concurrent prompt")
	}
	if err := manager.Archive(ctx, project.ID, "chat", project.Roots[0]); err == nil {
		t.Fatal("archived a running chat")
	}
	if err := manager.Queue(ctx, "chat", "next"); err != nil {
		t.Fatal(err)
	}
	queue, found, err := controller.NewSessionQueues(store).Get(project.ID, "chat")
	if err != nil || !found || len(queue.FollowUp) != 1 || queue.FollowUp[0].Text != "next" {
		t.Fatalf("durable queued follow-up: found=%v state=%#v err=%v", found, queue, err)
	}
	if err := writeRPC(first["connection"].(*websocket.Conn), map[string]any{
		"jsonrpc": "2.0", "id": first["id"], "result": map[string]any{"stopReason": "end_turn"},
	}); err != nil {
		t.Fatal(err)
	}
	var second map[string]any
	select {
	case second = <-requests:
	case <-ctx.Done():
		t.Fatal("queued follow-up did not start")
	}
	prompt := second["params"].(map[string]any)["content"].([]any)
	if len(prompt) != 1 || prompt[0].(map[string]any)["text"] != "next" {
		t.Fatalf("wrong queued prompt: %#v", prompt)
	}
	queue, found, err = controller.NewSessionQueues(store).Get(project.ID, "chat")
	if err != nil || !found || queue.Dispatch == nil || !queue.Dispatch.Attempted || len(queue.FollowUp) != 0 {
		t.Fatalf("dispatch was not made durable before Pi: found=%v state=%#v err=%v", found, queue, err)
	}
	if err := writeRPC(second["connection"].(*websocket.Conn), map[string]any{
		"jsonrpc": "2.0", "id": second["id"], "result": map[string]any{"stopReason": "end_turn"},
	}); err != nil {
		t.Fatal(err)
	}
	for {
		queue, found, err = controller.NewSessionQueues(store).Get(project.ID, "chat")
		if err == nil && found && queue.Dispatch == nil && len(queue.FollowUp) == 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("queue did not settle: found=%v state=%#v err=%v", found, queue, err)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestRuntimeResumesDurableFollowUpsAfterRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	requests := make(chan map[string]any, 2)
	agent := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		for {
			_, payload, err := connection.Read(ctx)
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
			if rpc.Method == "runtime.hello" {
				result = piInitializeResponse()
			}
			if rpc.Method == "session.prompt" {
				requests <- map[string]any{"connection": connection, "id": rpc.ID, "params": rpc.Params}
				continue
			}
			if len(rpc.ID) > 0 && writeRPC(connection, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result}) != nil {
				return
			}
		}
	}))
	defer agent.Close()

	dataDir, root := t.TempDir(), t.TempDir()
	policy, err := workspace.NewPathPolicy([]string{root}, false)
	if err != nil {
		t.Fatal(err)
	}
	store := persist.Store{Dir: dataDir}
	projects := workspace.NewProjects(store, policy)
	project, err := projects.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.NewSessionRecords(store).Record(controller.ProjectSessionRecord{ProjectID: project.ID, SessionID: "chat", CWD: project.Roots[0]}); err != nil {
		t.Fatal(err)
	}
	queueDocument := map[string]any{
		"version": 1, "engine": "pi", "records": []any{map[string]any{
			"projectId": project.ID, "sessionId": "chat", "revision": "restart",
			"followUp": []any{map[string]any{"id": "queued", "text": "resume me"}}, "handled": []any{},
		}},
	}
	if err := persist.Write(store, "pi-session-queues.json", queueDocument, nil); err != nil {
		t.Fatal(err)
	}
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	runtime, err := controller.NewRuntime(controller.RuntimeConfig{
		Host: "127.0.0.1", Port: port, DataDir: dataDir, StaticDir: staticDir,
		PiURL: "ws" + strings.TrimPrefix(agent.URL, "http"), Policy: policy,
		Getenv: func(string) string { return "" },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Start(); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(context.Background())
	var request map[string]any
	select {
	case request = <-requests:
	case <-ctx.Done():
		t.Fatal("restart did not resume queued work")
	}
	prompt := request["params"].(map[string]any)["content"].([]any)
	if len(prompt) != 1 || prompt[0].(map[string]any)["text"] != "resume me" {
		t.Fatalf("wrong recovered prompt: %#v", prompt)
	}
	queue, found, err := controller.NewSessionQueues(store).Get(project.ID, "chat")
	if err != nil || !found || queue.Dispatch == nil || !queue.Dispatch.Attempted {
		t.Fatalf("recovered delivery was not durable before Pi: found=%v state=%#v err=%v", found, queue, err)
	}
	if err := writeRPC(request["connection"].(*websocket.Conn), map[string]any{
		"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"stopReason": "end_turn"},
	}); err != nil {
		t.Fatal(err)
	}
	for {
		queue, found, err = controller.NewSessionQueues(store).Get(project.ID, "chat")
		if err == nil && found && queue.Dispatch == nil && len(queue.FollowUp) == 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("recovered queue did not settle: found=%v state=%#v err=%v", found, queue, err)
		case <-time.After(time.Millisecond):
		}
	}
}

type modelSwitchConfigCall struct {
	configID string
	value    string
}

type modelSwitchConfigResult struct {
	options []any
	err     string
}

type modelSwitchAgent struct {
	mu      sync.Mutex
	loads   [][]any
	load    int
	results []modelSwitchConfigResult
	calls   []modelSwitchConfigCall
}

func newModelSwitchManager(t *testing.T, loads [][]any, results []modelSwitchConfigResult) (*controller.SessionManager, workspace.Project, *modelSwitchAgent) {
	t.Helper()
	ctx := t.Context()
	root := t.TempDir()
	policy, err := workspace.NewPathPolicy([]string{root}, false)
	if err != nil {
		t.Fatal(err)
	}
	store := persist.Store{Dir: t.TempDir()}
	projects := workspace.NewProjects(store, policy)
	project, err := projects.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	records := controller.NewSessionRecords(store)
	if err := records.Record(controller.ProjectSessionRecord{ProjectID: project.ID, SessionID: "chat", CWD: project.Roots[0]}); err != nil {
		t.Fatal(err)
	}
	manager := controller.NewSessionManager(projects, policy, records, controller.NewSessionQueues(store), controller.NewObjectives(store), nil)
	agent := &modelSwitchAgent{loads: loads, results: results}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		for {
			_, payload, err := connection.Read(ctx)
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
			var rpcErr string
			switch rpc.Method {
			case "runtime.hello":
				result = piInitializeResponse()
			case "session.load":
				agent.mu.Lock()
				index := min(agent.load, len(agent.loads)-1)
				if index >= 0 {
					result = map[string]any{"configOptions": agent.loads[index]}
				}
				agent.load++
				agent.mu.Unlock()
			case "session.configure":
				call := modelSwitchConfigCall{}
				call.configID, _ = rpc.Params["configId"].(string)
				call.value, _ = rpc.Params["value"].(string)
				agent.mu.Lock()
				index := len(agent.calls)
				agent.calls = append(agent.calls, call)
				if index >= len(agent.results) {
					rpcErr = "unexpected configuration change"
				} else {
					configured := agent.results[index]
					rpcErr = configured.err
					if rpcErr == "" {
						result = map[string]any{"configOptions": configured.options}
					}
				}
				agent.mu.Unlock()
			}
			if len(rpc.ID) == 0 {
				continue
			}
			reply := map[string]any{"jsonrpc": "2.0", "id": rpc.ID}
			if rpcErr != "" {
				reply["error"] = map[string]any{"code": -32000, "message": rpcErr}
			} else {
				reply["result"] = result
			}
			if writeRPC(connection, reply) != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	client := controller.NewPiClient("ws"+strings.TrimPrefix(server.URL, "http"), "", "test", manager)
	manager.SetClient(client)
	t.Cleanup(client.Close)
	return manager, project, agent
}

func modelSwitchOptions(provider, model string) []any {
	return []any{
		map[string]any{"type": "select", "id": "provider", "name": "Provider", "currentValue": provider, "options": []any{}},
		map[string]any{"type": "select", "id": "model", "name": "Model", "currentValue": model, "options": []any{}},
	}
}

func (agent *modelSwitchAgent) snapshot() ([]modelSwitchConfigCall, int) {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	return append([]modelSwitchConfigCall(nil), agent.calls...), agent.load
}

func assertModelSwitchCalls(t *testing.T, calls []modelSwitchConfigCall, want []modelSwitchConfigCall) {
	t.Helper()
	if len(calls) != len(want) {
		t.Fatalf("configuration calls: got %#v, want %#v", calls, want)
	}
	for index := range want {
		if calls[index] != want[index] {
			t.Fatalf("configuration call %d: got %#v, want %#v", index, calls[index], want[index])
		}
	}
}

func TestSetModelRestoresPreviousProviderAndModelAfterModelFailure(t *testing.T) {
	oldOptions := modelSwitchOptions("old-provider", "old-model")
	manager, project, agent := newModelSwitchManager(t, [][]any{oldOptions}, []modelSwitchConfigResult{
		{options: modelSwitchOptions("new-provider", "new-default")},
		{err: "model rejected"},
		{options: oldOptions},
		{options: oldOptions},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client"); err != nil {
		t.Fatal(err)
	}

	err := manager.SetModel(ctx, "chat", controller.WireModel{ID: "new-model", Name: "New model", Provider: "new-provider", Available: true})
	if err == nil || !strings.Contains(err.Error(), "set model \"new-model\" for provider \"new-provider\"") || !strings.Contains(err.Error(), "model rejected") {
		t.Fatalf("model switch error: %v", err)
	}
	calls, loads := agent.snapshot()
	assertModelSwitchCalls(t, calls, []modelSwitchConfigCall{
		{configID: "provider", value: "new-provider"},
		{configID: "model", value: "new-model"},
		{configID: "provider", value: "old-provider"},
		{configID: "model", value: "old-model"},
	})
	if loads != 1 {
		t.Fatalf("authoritative loads: got %d, want 1", loads)
	}

	snapshot, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client")
	if err != nil {
		t.Fatal(err)
	}
	summary := snapshot["summary"].(controller.SessionSummary)
	if summary.Model == nil || summary.Model.Provider != "old-provider" || summary.Model.ID != "old-model" {
		t.Fatalf("model after rollback: %#v", summary.Model)
	}
}

func TestSetModelReconcilesAmbiguousProviderFailure(t *testing.T) {
	oldOptions := modelSwitchOptions("old-provider", "old-model")
	manager, project, agent := newModelSwitchManager(t, [][]any{oldOptions}, []modelSwitchConfigResult{
		{err: "provider response lost"},
		{options: oldOptions},
		{options: oldOptions},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client"); err != nil {
		t.Fatal(err)
	}

	err := manager.SetModel(ctx, "chat", controller.WireModel{ID: "new-model", Provider: "new-provider"})
	if err == nil || !strings.Contains(err.Error(), "provider response lost") {
		t.Fatalf("provider switch error: %v", err)
	}
	calls, _ := agent.snapshot()
	assertModelSwitchCalls(t, calls, []modelSwitchConfigCall{
		{configID: "provider", value: "new-provider"},
		{configID: "provider", value: "old-provider"},
		{configID: "model", value: "old-model"},
	})
}

func TestSetModelRejectsMismatchedSuccessResponse(t *testing.T) {
	oldOptions := modelSwitchOptions("old-provider", "old-model")
	manager, project, agent := newModelSwitchManager(t, [][]any{oldOptions}, []modelSwitchConfigResult{
		{options: modelSwitchOptions("new-provider", "new-default")},
		{options: modelSwitchOptions("new-provider", "different-model")},
		{options: oldOptions},
		{options: oldOptions},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client"); err != nil {
		t.Fatal(err)
	}

	err := manager.SetModel(ctx, "chat", controller.WireModel{ID: "new-model", Provider: "new-provider"})
	if err == nil || !strings.Contains(err.Error(), "returned \"new-provider\"/\"different-model\"") {
		t.Fatalf("mismatched switch error: %v", err)
	}
	calls, _ := agent.snapshot()
	assertModelSwitchCalls(t, calls, []modelSwitchConfigCall{
		{configID: "provider", value: "new-provider"},
		{configID: "model", value: "new-model"},
		{configID: "provider", value: "old-provider"},
		{configID: "model", value: "old-model"},
	})
}

func TestSetModelReloadsAuthoritativeStateWhenProviderRollbackFails(t *testing.T) {
	manager, project, agent := newModelSwitchManager(t, [][]any{
		modelSwitchOptions("old-provider", "old-model"),
		modelSwitchOptions("new-provider", "new-default"),
	}, []modelSwitchConfigResult{
		{options: modelSwitchOptions("new-provider", "new-default")},
		{err: "model rejected"},
		{err: "provider restore rejected"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client"); err != nil {
		t.Fatal(err)
	}

	err := manager.SetModel(ctx, "chat", controller.WireModel{ID: "new-model", Name: "New model", Provider: "new-provider", Available: true})
	if err == nil || !strings.Contains(err.Error(), "model rejected") || !strings.Contains(err.Error(), "restore previous provider \"old-provider\"") || !strings.Contains(err.Error(), "provider restore rejected") {
		t.Fatalf("model switch error: %v", err)
	}
	calls, loads := agent.snapshot()
	assertModelSwitchCalls(t, calls, []modelSwitchConfigCall{
		{configID: "provider", value: "new-provider"},
		{configID: "model", value: "new-model"},
		{configID: "provider", value: "old-provider"},
	})
	if loads != 2 {
		t.Fatalf("authoritative loads: got %d, want 2", loads)
	}

	snapshot, err := manager.Messages(ctx, "chat", project.ID, project.Roots[0], "client")
	if err != nil {
		t.Fatal(err)
	}
	summary := snapshot["summary"].(controller.SessionSummary)
	if summary.Model == nil || summary.Model.Provider != "new-provider" || summary.Model.ID != "new-default" {
		t.Fatalf("model after authoritative reload: %#v", summary.Model)
	}
}
