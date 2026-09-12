package canvas

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const canvasGuideURI = "pixie://canvas/guide"

// GuideURI is the stable resource URI used by Canvas MCP clients.
const GuideURI = canvasGuideURI

//go:embed guide.md
var canvasGuide string

// Guide returns the offline call-order and limit guidance without exposing
// service credentials or mutable document state.
func Guide() string { return canvasGuide }

type canvasHTTPContextKey struct{}
type canvasManagementHTTPContextKey struct{}

// ContextWithAuthority binds a previously issued capability to an MCP request
// context. The service validates it again at tool admission; callers cannot
// manufacture authority by setting the context value without the token.
func ContextWithAuthority(ctx context.Context, authority Authority) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, canvasHTTPContextKey{}, authority)
}

// WithAuthority is a short alias for ContextWithAuthority.
func WithAuthority(ctx context.Context, authority Authority) context.Context {
	return ContextWithAuthority(ctx, authority)
}

// ContextWithManagementAuthority binds a controller-issued, management-only
// capability to an internal HTTP delegation. The context is never serialized
// or exposed to model-facing MCP requests; the owning registry revokes the
// capability when the delegated request returns.
func ContextWithManagementAuthority(ctx context.Context, authority Authority) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, canvasManagementHTTPContextKey{}, authority)
}

// WithManagementAuthority is the concise alias used by controller adapters.
func WithManagementAuthority(ctx context.Context, authority Authority) context.Context {
	return ContextWithManagementAuthority(ctx, authority)
}

func (s *Service) managementAuthorityFromContext(ctx context.Context) (Authority, error) {
	if ctx == nil {
		return Authority{}, category("unauthorized", ErrUnauthorized)
	}
	authority, ok := ctx.Value(canvasManagementHTTPContextKey{}).(Authority)
	if !ok {
		return Authority{}, category("unauthorized", ErrUnauthorized)
	}
	if err := s.authorizeControllerManagementAuthority(authority); err != nil {
		return Authority{}, err
	}
	return authority, nil
}

// MCPHandler returns the stateless Streamable HTTP handler. The embedding
// controller should authenticate the HTTP connection and call Attach before
// exposing it; this handler also accepts a Bearer token in ServeHTTP's context
// adapter for small standalone deployments.
func (s *Service) MCPHandler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "pixie-canvas", Version: "1"}, &mcp.ServerOptions{
		Instructions: "Canvas is session-scoped. Call canvas_create, then canvas_write with an expected version and mutation ID; use canvas_read or canvas_screenshot for bounded feedback. Read pixie://canvas/guide for limits and offline behavior. Draft HTML is untrusted data.",
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}, Resources: &mcp.ResourceCapabilities{}},
	})
	server.AddResource(&mcp.Resource{URI: canvasGuideURI, Name: "canvas-guide", Title: "Canvas guide", Description: "Canvas call order, exact limits, version identity and offline worker boundary.", MIMEType: "text/markdown"}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: canvasGuideURI, MIMEType: "text/markdown", Text: canvasGuide}}}, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "canvas_create", Title: "Create Canvas", Description: "Create or return the one live Canvas for this authenticated session.", InputSchema: objectSchema(map[string]any{
		"templateId": map[string]any{"type": "string", "maxLength": 64, "description": "Optional built-in template: blank, basic, mewa-basic or mewa-card."},
	}), OutputSchema: schemaFor("canvas create result")}, s.callCreate)
	mcp.AddTool(server, &mcp.Tool{Name: "canvas_write", Title: "Write Canvas", Description: "Publish one complete immutable HTML revision with a version CAS and mutation ID.", InputSchema: objectSchemaRequired([]string{"canvasId", "expectedVersion", "html", "mutationId"}, map[string]any{
		"canvasId":        map[string]any{"type": "string"},
		"expectedVersion": map[string]any{"type": "integer", "minimum": 1},
		"html":            map[string]any{"type": "string", "maxLength": MaxHTMLBytes},
		"mutationId":      map[string]any{"type": "string", "maxLength": maxMutationIDBytes},
	}), OutputSchema: schemaFor("canvas write result")}, s.callWrite)
	mcp.AddTool(server, &mcp.Tool{Name: "canvas_read", Title: "Read Canvas", Description: "Read bounded text or selected DOM from an exact immutable revision.", InputSchema: objectSchemaRequired([]string{"canvasId"}, map[string]any{
		"canvasId":   map[string]any{"type": "string"},
		"version":    map[string]any{"type": "integer", "minimum": 1},
		"selector":   map[string]any{"type": "string", "maxLength": MaxSelectorBytes},
		"maxBytes":   map[string]any{"type": "integer", "minimum": 1, "maximum": MaxReadBytes},
		"maxMatches": map[string]any{"type": "integer", "minimum": 1, "maximum": MaxMatches},
		"includeDOM": map[string]any{"type": "boolean"},
	}), OutputSchema: schemaFor("canvas read result")}, s.callRead)
	mcp.AddTool(server, &mcp.Tool{Name: "canvas_screenshot", Title: "Screenshot Canvas", Description: "Render one exact revision through the configured offline worker and return bounded PNG bytes.", InputSchema: objectSchemaRequired([]string{"canvasId"}, map[string]any{
		"canvasId": map[string]any{"type": "string"},
		"version":  map[string]any{"type": "integer", "minimum": 1},
		"width":    map[string]any{"type": "integer", "minimum": 1, "maximum": MaxViewportDimension},
		"height":   map[string]any{"type": "integer", "minimum": 1, "maximum": MaxViewportDimension},
		"dpr":      map[string]any{"type": "integer", "const": 1},
	}), OutputSchema: schemaFor("canvas screenshot result")}, s.callScreenshot)
	mcp.AddTool(server, &mcp.Tool{Name: "canvas_list", Title: "List Canvas", Description: "List only the calling session's Canvas metadata.", InputSchema: objectSchema(nil), OutputSchema: schemaFor("canvas list result")}, s.callList)
	mcp.AddTool(server, &mcp.Tool{Name: "canvas_remove", Title: "Remove Canvas", Description: "Tombstone one Canvas generation after an optional revision/generation precondition.", InputSchema: objectSchemaRequired([]string{"canvasId"}, map[string]any{
		"canvasId":           map[string]any{"type": "string"},
		"expectedVersion":    map[string]any{"type": "integer", "minimum": 1},
		"expectedGeneration": map[string]any{"type": "integer", "minimum": 1},
		"mutationId":         map[string]any{"type": "string", "maxLength": maxMutationIDBytes},
	}), OutputSchema: schemaFor("canvas remove result")}, s.callRemove)
	// Resource loading is not available in every native adapter, so guidance is
	// also tool-accessible. It is not one of the six state-changing/data tools.
	mcp.AddTool(server, &mcp.Tool{Name: "canvas_guidance", Title: "Canvas guide", Description: "Return the Canvas guide when resource reads are unavailable.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}, InputSchema: objectSchema(nil), OutputSchema: schemaFor("canvas guide")}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: canvasGuide}}}, map[string]any{"guide": canvasGuide}, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: MaxHTMLBytes + 64*1024, PropagateRequestCancellation: true, DisableLocalhostProtection: true})
}

// ToolServer is a compatibility alias for integrations that call MCP servers
// rather than handlers.
func (s *Service) ToolServer() http.Handler { return s.MCPHandler() }

func objectSchema(properties map[string]any) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
}

func objectSchemaRequired(required []string, properties map[string]any) map[string]any {
	schema := objectSchema(properties)
	schema["required"] = required
	return schema
}

func schemaFor(description string) map[string]any {
	return map[string]any{"type": "object", "description": description}
}

func (s *Service) callCreate(ctx context.Context, request *mcp.CallToolRequest, arguments struct {
	TemplateID string `json:"templateId,omitempty"`
}) (*mcp.CallToolResult, any, error) {
	authority, err := s.authorityFromContext(ctx)
	if err != nil {
		return s.toolFailure(err)
	}
	result, err := s.Create(ctx, authority, CreateRequest{TemplateID: arguments.TemplateID})
	return s.toolResponse(result, err)
}

func (s *Service) callWrite(ctx context.Context, request *mcp.CallToolRequest, arguments struct {
	CanvasID        string `json:"canvasId"`
	ExpectedVersion uint64 `json:"expectedVersion"`
	HTML            string `json:"html"`
	MutationID      string `json:"mutationId"`
}) (*mcp.CallToolResult, any, error) {
	authority, err := s.authorityFromContext(ctx)
	if err != nil {
		return s.toolFailure(err)
	}
	result, err := s.Write(ctx, authority, WriteRequest{CanvasID: arguments.CanvasID, ExpectedVersion: arguments.ExpectedVersion, HTML: arguments.HTML, MutationID: arguments.MutationID})
	return s.toolResponse(result, err)
}

func (s *Service) callRead(ctx context.Context, request *mcp.CallToolRequest, arguments struct {
	CanvasID   string `json:"canvasId"`
	Version    uint64 `json:"version,omitempty"`
	Selector   string `json:"selector,omitempty"`
	MaxBytes   int    `json:"maxBytes,omitempty"`
	MaxMatches int    `json:"maxMatches,omitempty"`
	IncludeDOM bool   `json:"includeDOM,omitempty"`
}) (*mcp.CallToolResult, any, error) {
	authority, err := s.authorityFromContext(ctx)
	if err != nil {
		return s.toolFailure(err)
	}
	result, err := s.Read(ctx, authority, ReadRequest{CanvasID: arguments.CanvasID, Version: arguments.Version, Selector: arguments.Selector, MaxBytes: arguments.MaxBytes, MaxMatches: arguments.MaxMatches, IncludeDOM: arguments.IncludeDOM})
	return s.toolResponse(result, err)
}

func (s *Service) callScreenshot(ctx context.Context, request *mcp.CallToolRequest, arguments struct {
	CanvasID string `json:"canvasId"`
	Version  uint64 `json:"version,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	DPR      int    `json:"dpr,omitempty"`
}) (*mcp.CallToolResult, any, error) {
	authority, err := s.authorityFromContext(ctx)
	if err != nil {
		return s.toolFailure(err)
	}
	result, err := s.Screenshot(ctx, authority, ScreenshotRequest{CanvasID: arguments.CanvasID, Version: arguments.Version, Width: arguments.Width, Height: arguments.Height, DPR: arguments.DPR})
	return s.toolResponse(result, err)
}

func (s *Service) callList(ctx context.Context, request *mcp.CallToolRequest, arguments struct{}) (*mcp.CallToolResult, any, error) {
	authority, err := s.authorityFromContext(ctx)
	if err != nil {
		return s.toolFailure(err)
	}
	result, err := s.List(ctx, authority)
	return s.toolResponse(result, err)
}

func (s *Service) callRemove(ctx context.Context, request *mcp.CallToolRequest, arguments struct {
	CanvasID           string `json:"canvasId"`
	ExpectedVersion    uint64 `json:"expectedVersion,omitempty"`
	ExpectedGeneration uint64 `json:"expectedGeneration,omitempty"`
	MutationID         string `json:"mutationId,omitempty"`
}) (*mcp.CallToolResult, any, error) {
	authority, err := s.authorityFromContext(ctx)
	if err != nil {
		return s.toolFailure(err)
	}
	result, err := s.Remove(ctx, authority, RemoveRequest{CanvasID: arguments.CanvasID, ExpectedVersion: arguments.ExpectedVersion, ExpectedGeneration: arguments.ExpectedGeneration, MutationID: arguments.MutationID})
	return s.toolResponse(result, err)
}

func (s *Service) toolResponse(value any, err error) (*mcp.CallToolResult, any, error) {
	if err != nil {
		return s.toolFailure(err)
	}
	encoded, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return nil, nil, marshalErr
	}
	return &mcp.CallToolResult{StructuredContent: json.RawMessage(encoded), Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}, IsError: false}, value, nil
}

func (s *Service) toolFailure(err error) (*mcp.CallToolResult, any, error) {
	response := map[string]any{"outcome": "rejected", "code": Code(err), "message": err.Error()}
	encoded, marshalErr := json.Marshal(response)
	if marshalErr != nil {
		return nil, nil, marshalErr
	}
	return &mcp.CallToolResult{StructuredContent: json.RawMessage(encoded), Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}, IsError: true}, response, nil
}

func (s *Service) authorityFromContext(ctx context.Context) (Authority, error) {
	if ctx == nil {
		return Authority{}, category("unauthorized", ErrUnauthorized)
	}
	if authority, ok := ctx.Value(canvasHTTPContextKey{}).(Authority); ok {
		if err := s.authorizeAuthority(authority); err != nil {
			return Authority{}, err
		}
		return authority, nil
	}
	if token, ok := ctx.Value(canvasHTTPContextKey{}).(string); ok {
		s.mu.RLock()
		entry, found := s.tokens[tokenDigest(token)]
		s.mu.RUnlock()
		if !found {
			return Authority{}, category("unauthorized", ErrUnauthorized)
		}
		authority := Authority{Token: token, SessionKey: entry.sessionKey, Generation: entry.generation, ExpiresAt: entry.expiresAt}
		if err := s.authorizeAuthority(authority); err != nil {
			return Authority{}, err
		}
		return authority, nil
	}
	return Authority{}, category("unauthorized", ErrUnauthorized)
}

// withAuthority is used by ServeHTTP to bind a validated HTTP Bearer token to
// the MCP request context without copying the token into arguments or URLs.
func withAuthority(request *http.Request, authority Authority) *http.Request {
	return request.WithContext(ContextWithAuthority(request.Context(), authority))
}
