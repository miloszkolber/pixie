package design

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const designGuideURI = "pixie://design/guide"

// GuideURI is the stable Design guidance resource URI.
const GuideURI = designGuideURI

//go:embed guide.md
var designGuide string

// Guide returns static offline call-order and limit guidance.
func Guide() string { return designGuide }

type designStatusArgs struct {
	DocumentID         string `json:"documentId,omitempty"`
	ExpectedGeneration uint64 `json:"expectedGeneration,omitempty"`
	SelectionRevision  uint64 `json:"selectionRevision,omitempty"`
}
type designStructureArgs struct {
	DocumentID         string `json:"documentId"`
	ExpectedGeneration uint64 `json:"expectedGeneration,omitempty"`
	SelectionRevision  uint64 `json:"selectionRevision,omitempty"`
	PageID             string `json:"pageId,omitempty"`
	ParentID           string `json:"parentId,omitempty"`
	Depth              int    `json:"depth,omitempty"`
	Cursor             string `json:"cursor,omitempty"`
	Limit              int    `json:"limit,omitempty"`
}
type designNodeArgs struct {
	DocumentID         string `json:"documentId"`
	NodeID             string `json:"nodeId"`
	ExpectedGeneration uint64 `json:"expectedGeneration,omitempty"`
	SelectionRevision  uint64 `json:"selectionRevision,omitempty"`
}
type designTextArgs struct {
	DocumentID         string `json:"documentId"`
	ExpectedGeneration uint64 `json:"expectedGeneration,omitempty"`
	SelectionRevision  uint64 `json:"selectionRevision,omitempty"`
	PageID             string `json:"pageId,omitempty"`
	RootNodeID         string `json:"rootNodeId,omitempty"`
	Cursor             string `json:"cursor,omitempty"`
	Limit              int    `json:"limit,omitempty"`
}
type designPreviewArgs struct {
	DocumentID         string `json:"documentId"`
	ExpectedGeneration uint64 `json:"expectedGeneration,omitempty"`
	SelectionRevision  uint64 `json:"selectionRevision,omitempty"`
	Kind               string `json:"kind,omitempty"`
	NodeID             string `json:"nodeId,omitempty"`
}

// MCPHandler exposes exactly five read tools plus a guidance fallback. Upload,
// removal and focus mutations intentionally do not appear in this handler.
func (s *Service) MCPHandler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "pixie-design", Version: "1"}, &mcp.ServerOptions{
		Instructions: "Design is instance-wide and read-only to agents. Call design_status first; pass the returned documentId, generation, and selectionRevision to bounded structure/node/text/cover queries. Read pixie://design/guide for limits and stale-identity behavior.",
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}, Resources: &mcp.ResourceCapabilities{}},
	})
	server.AddResource(&mcp.Resource{URI: designGuideURI, Name: "design-guide", Title: "Design guide", Description: "Openfig structure MVP call order, bounds and identity guards.", MIMEType: "text/markdown"}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: designGuideURI, MIMEType: "text/markdown", Text: designGuide}}}, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "design_status", Title: "Design status", Description: "Read instance-wide document metadata, availability, warnings and shared focus.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}, s.callStatus)
	mcp.AddTool(server, &mcp.Tool{Name: "design_structure", Title: "Design structure", Description: "Read bounded ordered pages or layer children using an opaque cursor.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}, s.callStructure)
	mcp.AddTool(server, &mcp.Tool{Name: "design_node", Title: "Design node", Description: "Read one normalized node's bounded geometry, style, direct text and override metadata.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}, s.callNode)
	mcp.AddTool(server, &mcp.Tool{Name: "design_text", Title: "Design text", Description: "Read paginated direct text records with stable node references.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}, s.callText)
	mcp.AddTool(server, &mcp.Tool{Name: "design_preview", Title: "Design cover preview", Description: "Read actual bounded PNG content for the saved document cover; frame previews are unavailable in Structure MVP.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}, s.callPreview)
	mcp.AddTool(server, &mcp.Tool{Name: "design_guidance", Title: "Design guide", Description: "Return Design guidance when resource reads are unavailable.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: designGuide}}}, map[string]any{"guide": designGuide}, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true, DisableLocalhostProtection: true, MaxRequestBodyBytes: 256 * 1024})
}

func (s *Service) callStatus(ctx context.Context, _ *mcp.CallToolRequest, args designStatusArgs) (*mcp.CallToolResult, any, error) {
	result, err := s.Status(ctx, StatusRequest{DocumentID: args.DocumentID, ExpectedGeneration: args.ExpectedGeneration, SelectionRevision: args.SelectionRevision})
	return toolResponse(result, err)
}

func (s *Service) callStructure(ctx context.Context, _ *mcp.CallToolRequest, args designStructureArgs) (*mcp.CallToolResult, any, error) {
	result, err := s.Structure(ctx, StructureRequest{DocumentID: args.DocumentID, ExpectedGeneration: args.ExpectedGeneration, ExpectedRevision: args.SelectionRevision, PageID: args.PageID, ParentID: args.ParentID, Depth: args.Depth, Cursor: args.Cursor, Limit: args.Limit})
	return toolResponse(result, err)
}

func (s *Service) callNode(ctx context.Context, _ *mcp.CallToolRequest, args designNodeArgs) (*mcp.CallToolResult, any, error) {
	result, err := s.Node(ctx, NodeRequest{DocumentID: args.DocumentID, NodeID: args.NodeID, ExpectedGeneration: args.ExpectedGeneration, ExpectedRevision: args.SelectionRevision})
	return toolResponse(result, err)
}

func (s *Service) callText(ctx context.Context, _ *mcp.CallToolRequest, args designTextArgs) (*mcp.CallToolResult, any, error) {
	result, err := s.Text(ctx, TextRequest{DocumentID: args.DocumentID, ExpectedGeneration: args.ExpectedGeneration, ExpectedRevision: args.SelectionRevision, PageID: args.PageID, RootNodeID: args.RootNodeID, Cursor: args.Cursor, Limit: args.Limit})
	return toolResponse(result, err)
}

func (s *Service) callPreview(ctx context.Context, _ *mcp.CallToolRequest, args designPreviewArgs) (*mcp.CallToolResult, any, error) {
	result, err := s.Preview(ctx, PreviewRequest{DocumentID: args.DocumentID, ExpectedGeneration: args.ExpectedGeneration, ExpectedRevision: args.SelectionRevision, Kind: args.Kind, NodeID: args.NodeID})
	if err != nil {
		return toolFailure(err)
	}
	encoded, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return nil, nil, marshalErr
	}
	return &mcp.CallToolResult{StructuredContent: json.RawMessage(encoded), Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}, &mcp.ImageContent{Data: result.PNG, MIMEType: result.MIME}}}, result, nil
}

func toolResponse(value any, err error) (*mcp.CallToolResult, any, error) {
	if err != nil {
		return toolFailure(err)
	}
	encoded, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return nil, nil, marshalErr
	}
	return &mcp.CallToolResult{StructuredContent: json.RawMessage(encoded), Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}, value, nil
}

func toolFailure(err error) (*mcp.CallToolResult, any, error) {
	value := map[string]any{"outcome": "rejected", "code": Code(err), "message": err.Error()}
	encoded, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return nil, nil, marshalErr
	}
	return &mcp.CallToolResult{StructuredContent: json.RawMessage(encoded), Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}, IsError: true}, value, nil
}

// ToolServer is a compatibility alias for integrations that expect a handler.
func (s *Service) ToolServer() http.Handler { return s.MCPHandler() }
