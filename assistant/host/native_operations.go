package host

// This file owns the vanilla Pi RPC operations that the controller and direct
// RPC clients invoke by logical session. Every handler resolves the immutable
// per-session resident child from the supervisor registry; none of them keeps
// or consults a shared mutable "current session". Read-only projections return
// Pi's data unchanged, mutations return the authoritative state projection,
// and everything the pinned 0.85.1 command union cannot express stays absent
// from nativeOperationSet and therefore fails closed in callHost.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
)

// configOptionsFromState projects one authored Pi state into the host's config
// option shape. Pi remains the authority for the current provider, model and
// thinking level; the host only labels the selection for the controller.
func configOptionsFromState(state piState) []any {
	options := []any{}
	if provider, _ := state.Model["provider"].(string); provider != "" {
		options = append(options, map[string]any{"id": "provider", "currentValue": provider})
	}
	if model, _ := state.Model["id"].(string); model != "" {
		options = append(options, map[string]any{"id": "model", "currentValue": model})
	}
	if state.ThinkingLevel != "" {
		options = append(options, map[string]any{"id": "thinking", "currentValue": state.ThinkingLevel})
	}
	return options
}

// nativeResult returns Pi's response data, substituting an empty object for
// the commands that acknowledge success without carrying data.
func nativeResult(reply nativeReply) json.RawMessage {
	if len(reply.Data) == 0 {
		return json.RawMessage(`{}`)
	}
	return reply.Data
}

// forwardPi routes one command to the resident child for params.sessionId. The
// caller owns any additional payload shaping and result interpretation.
func (s *nativeSupervisor) forwardPi(ctx context.Context, params map[string]any, command string, payload map[string]any, control bool) (json.RawMessage, error) {
	id, _ := params["sessionId"].(string)
	child, err := s.resident(id)
	if err != nil {
		return nil, err
	}
	reply, err := child.callPi(ctx, command, payload, control)
	if err != nil {
		return nil, err
	}
	return nativeResult(reply), nil
}

// configure applies a per-session model or thinking selection. The controller
// sends {configId, value}; direct RPC clients may send the native provider,
// modelId/model and thinkingLevel fields instead. Create-time-only MCP
// overrides stay rejected because attaching servers is a separate operation.
func (s *nativeSupervisor) configure(ctx context.Context, params map[string]any) (json.RawMessage, error) {
	id, _ := params["sessionId"].(string)
	child, err := s.resident(id)
	if err != nil {
		return nil, err
	}
	if _, ok := params["mcpServers"]; ok {
		return nil, errors.New("session.configure does not accept create-time MCP servers")
	}
	configID, _ := params["configId"].(string)
	value, _ := params["value"].(string)
	thinking := ""
	provider, _ := params["provider"].(string)
	modelID, _ := params["modelId"].(string)
	if model, ok := params["model"].(map[string]any); ok {
		if modelID == "" {
			modelID, _ = model["id"].(string)
		}
		if provider == "" {
			provider, _ = model["provider"].(string)
		}
	}
	if fallback, _ := params["thinkingLevel"].(string); fallback != "" {
		thinking = fallback
	} else if fallback, _ := params["thinking"].(string); fallback != "" {
		thinking = fallback
	}
	switch configID {
	case "thinking", "thinking_level", "thinkingLevel":
		if value == "" {
			return nil, errors.New("session.configure requires a thinking value")
		}
		thinking = value
		value = ""
	case "provider":
		if value == "" {
			return nil, errors.New("session.configure requires a provider value")
		}
		resolved, err := s.firstModelForProvider(ctx, child, value)
		if err != nil {
			return nil, err
		}
		provider, modelID, value = value, resolved, ""
	case "model", "modelId":
		if value == "" {
			return nil, errors.New("session.configure requires a model value")
		}
		modelID, value = value, ""
	case "":
		// Fall through to the explicit provider/model/thinking fields.
	default:
		return nil, fmt.Errorf("session.configure does not support configuration %q", configID)
	}
	if thinking != "" {
		if _, err := child.callPi(ctx, "set_thinking_level", map[string]any{"level": thinking}, true); err != nil {
			return nil, err
		}
	}
	if modelID != "" {
		if provider == "" {
			state, err := child.state(ctx)
			if err != nil {
				return nil, err
			}
			provider, _ = state.Model["provider"].(string)
		}
		if provider == "" {
			return nil, errors.New("session.configure cannot resolve the provider for the model")
		}
		if _, err := child.callPi(ctx, "set_model", map[string]any{"provider": provider, "modelId": modelID}, true); err != nil {
			return nil, err
		}
	}
	if thinking == "" && modelID == "" {
		return nil, errors.New("session.configure requires a configuration value")
	}
	state, err := child.state(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"sessionId":     state.SessionID,
		"model":         state.Model,
		"thinkingLevel": state.ThinkingLevel,
		"configOptions": configOptionsFromState(state),
	})
}

// firstModelForProvider asks Pi for the provider's available models and
// selects the first usable one, matching the legacy host's provider switching
// behavior without inventing a model inventory in the Go host.
func (s *nativeSupervisor) firstModelForProvider(ctx context.Context, child *nativeChild, provider string) (string, error) {
	reply, err := child.callPi(ctx, "get_available_models", nil, true)
	if err != nil {
		return "", err
	}
	var models struct {
		Models []map[string]any `json:"models"`
	}
	if json.Unmarshal(reply.Data, &models) != nil {
		return "", errors.New("Pi get_available_models returned an invalid model list")
	}
	for _, model := range models.Models {
		if candidate, _ := model["provider"].(string); candidate != provider {
			continue
		}
		if id, _ := model["id"].(string); id != "" {
			return id, nil
		}
	}
	return "", fmt.Errorf("Pi has no available model for provider %q", provider)
}

// renameSession maps the generic session.rename operation onto Pi's
// set_session_name command.
func (s *nativeSupervisor) renameSession(ctx context.Context, params map[string]any) (json.RawMessage, error) {
	id, _ := params["sessionId"].(string)
	child, err := s.resident(id)
	if err != nil {
		return nil, err
	}
	name, _ := params["name"].(string)
	if name == "" {
		name, _ = params["title"].(string)
	}
	if name == "" {
		return nil, errors.New("session.rename requires a name")
	}
	reply, err := child.callPi(ctx, "set_session_name", map[string]any{"name": name}, true)
	if err != nil {
		return nil, err
	}
	return nativeResult(reply), nil
}

// steerPayload accepts either Pi's native {message, images} shape or the
// controller's content-block prompt shape.
func steerPayload(params map[string]any) (map[string]any, error) {
	if message, ok := params["message"].(string); ok && message != "" {
		payload := map[string]any{"message": message}
		if images, ok := params["images"].([]any); ok && len(images) > 0 {
			payload["images"] = images
		}
		return payload, nil
	}
	message, images, err := promptPayload(params)
	if err != nil {
		return nil, err
	}
	return map[string]any{"message": message, "images": images}, nil
}

// compactPayload forwards optional compaction instructions.
func compactPayload(params map[string]any) map[string]any {
	if instructions, ok := params["customInstructions"].(string); ok && instructions != "" {
		return map[string]any{"customInstructions": instructions}
	}
	return nil
}

// branchSession implements session.fork and session.clone on a fresh child so
// the parent's resident child and its session binding are never mutated. The
// child first switches to the parent's verified native path, then Pi forks or
// clones it in place and reports the new session identity, which is bound and
// registered before the snapshot is returned. Pi's fork command requires an
// entryId; the controller's full-conversation fork carries none, so that case
// uses the equivalent clone command, while an explicit entryId uses fork.
func (s *nativeSupervisor) branchSession(ctx context.Context, params map[string]any, clone bool) (json.RawMessage, error) {
	parentID, _ := params["sessionId"].(string)
	s.mu.Lock()
	parent, known := s.sessions[parentID]
	s.mu.Unlock()
	if !known {
		return nil, fmt.Errorf("unknown native session %q; no durable registry entry exists", parentID)
	}
	if err := validateRegistryRef(parent); err != nil {
		return nil, fmt.Errorf("native session %q is not materialized for branching: %w", parentID, err)
	}
	child, err := s.launch(ctx, parent.CWD)
	if err != nil {
		return nil, err
	}
	defer s.releaseLaunchReservation(child)
	owned := false
	defer func() {
		if !owned {
			_ = child.close(context.Background())
		}
	}()
	switchCtx, cancel := context.WithTimeout(ctx, nativeRequestTimeout)
	reply, err := child.callPi(switchCtx, "switch_session", map[string]any{"sessionPath": parent.Path}, true)
	cancel()
	if err != nil {
		return nil, err
	}
	var switched struct {
		Cancelled *bool `json:"cancelled"`
	}
	if json.Unmarshal(reply.Data, &switched) != nil || switched.Cancelled == nil || *switched.Cancelled {
		return nil, errors.New("Pi switch_session did not confirm cancelled:false")
	}
	command := "clone"
	var payload map[string]any
	if !clone {
		if entryID, _ := params["entryId"].(string); entryID != "" {
			command = "fork"
			payload = map[string]any{"entryId": entryID}
		}
	}
	branchCtx, cancel := context.WithTimeout(ctx, nativeRequestTimeout)
	reply, err = child.callPi(branchCtx, command, payload, true)
	cancel()
	if err != nil {
		return nil, err
	}
	if command == "clone" {
		var cloned struct {
			Cancelled *bool `json:"cancelled"`
		}
		if json.Unmarshal(reply.Data, &cloned) != nil || cloned.Cancelled == nil || *cloned.Cancelled {
			return nil, errors.New("Pi clone did not confirm cancelled:false")
		}
	}
	stateCtx, cancel := context.WithTimeout(ctx, nativeRequestTimeout)
	state, err := child.state(stateCtx)
	cancel()
	if err != nil {
		return nil, err
	}
	if state.SessionID == "" || state.SessionFile == "" {
		return nil, errors.New("Pi branch is missing an exact session id or absolute session file")
	}
	if state.SessionID == parentID || filepath.Clean(state.SessionFile) == parent.Path {
		return nil, errors.New("Pi returned the parent session for a branch")
	}
	if err := child.bind(state.SessionID, state.SessionFile, false); err != nil {
		return nil, err
	}
	if err := s.install(state.SessionID, nativeSessionRef{Path: state.SessionFile, CWD: child.cwd}, child); err != nil {
		return nil, err
	}
	owned = true
	raw, err := child.snapshot(ctx)
	return s.withRunState(state.SessionID, raw, err)
}

// switchSession routes to the session's verified native path from the durable
// registry. The caller may name the logical session, the native path, or both;
// the path is matched against the registry rather than trusted, and a logical
// id is never passed to Pi's switch_session.
func (s *nativeSupervisor) switchSession(ctx context.Context, params map[string]any) (json.RawMessage, error) {
	id, _ := params["sessionId"].(string)
	requested, _ := params["sessionPath"].(string)
	if requested != "" {
		if !filepath.IsAbs(requested) {
			return nil, errors.New("session.switch requires an absolute native session path")
		}
		requested = filepath.Clean(requested)
	}
	s.mu.Lock()
	ref, known := s.sessions[id]
	if requested != "" {
		if known && ref.Path != requested {
			s.mu.Unlock()
			return nil, errors.New("session.switch path does not match the verified native registry")
		}
		if !known {
			for candidate, candidateRef := range s.sessions {
				if candidateRef.Path == requested {
					id, ref, known = candidate, candidateRef, true
					break
				}
			}
		}
	}
	s.mu.Unlock()
	if !known {
		return nil, fmt.Errorf("unknown native session %q; no durable registry entry exists", id)
	}
	child, err := s.loadChild(ctx, id, ref.CWD)
	if err != nil {
		return nil, err
	}
	raw, err := child.snapshot(ctx)
	return s.withRunState(id, raw, err)
}
