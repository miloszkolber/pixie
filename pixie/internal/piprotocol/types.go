// Package piprotocol contains Pixie's host-service DTOs. Session projection
// types are controller-local; Pi's native events are adapted by the controller.
package piprotocol

import "encoding/json"

type SessionId = string
type SessionConfigId = string
type SessionConfigValueId = string

type RequestError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RequestError) Error() string { return e.Message }

type HttpHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type McpServerHttpInline struct {
	Type    string       `json:"type"`
	Name    string       `json:"name"`
	Url     string       `json:"url"`
	Headers []HttpHeader `json:"headers"`
}
type McpServer struct{ Http *McpServerHttpInline }

func (s McpServer) MarshalJSON() ([]byte, error) { return json.Marshal(s.Http) }

type NewSessionRequest struct {
	Cwd        string         `json:"cwd"`
	McpServers []McpServer    `json:"mcpServers"`
	Meta       map[string]any `json:"metadata,omitempty"`
}
type LoadSessionRequest struct {
	SessionId  string         `json:"sessionId"`
	Cwd        string         `json:"cwd"`
	McpServers []McpServer    `json:"mcpServers"`
	Meta       map[string]any `json:"metadata,omitempty"`
}
type NewSessionResponse struct {
	Capabilities  map[string]int   `json:"capabilities"`
	SessionId     string           `json:"sessionId"`
	ConfigOptions []map[string]any `json:"configOptions"`
	Meta          map[string]any   `json:"metadata,omitempty"`
	Messages      []map[string]any `json:"messages,omitempty"`
	Commands      []map[string]any `json:"commands,omitempty"`
	RunID         string           `json:"runId,omitempty"`
}
type LoadSessionResponse = NewSessionResponse

type SessionInfo struct {
	SessionId string         `json:"sessionId"`
	Cwd       string         `json:"cwd"`
	Title     *string        `json:"title,omitempty"`
	UpdatedAt *string        `json:"updatedAt,omitempty"`
	Meta      map[string]any `json:"_meta,omitempty"`
}
type ListSessionsRequest struct {
	Cursor *string `json:"cursor,omitempty"`
	Cwd    *string `json:"cwd,omitempty"`
}
type ListSessionsResponse struct {
	Sessions   []SessionInfo `json:"sessions"`
	NextCursor *string       `json:"nextCursor,omitempty"`
}

type TextContent struct {
	Text string `json:"text"`
	Type string `json:"type"`
}
type ImageContent struct {
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
	Type     string `json:"type"`
}
type TextResourceContents struct {
	Uri      string         `json:"uri"`
	MimeType *string        `json:"mimeType,omitempty"`
	Text     string         `json:"text"`
	Meta     map[string]any `json:"_meta,omitempty"`
}
type EmbeddedResourceResource struct{ TextResourceContents *TextResourceContents }

func (r EmbeddedResourceResource) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.TextResourceContents)
}

type ResourceContent struct {
	Type     string                   `json:"type"`
	Resource EmbeddedResourceResource `json:"resource"`
	Meta     map[string]any           `json:"_meta,omitempty"`
}
type ContentBlock struct {
	Text     *TextContent
	Image    *ImageContent
	Resource *ResourceContent
}

func (b ContentBlock) MarshalJSON() ([]byte, error) {
	if b.Text != nil {
		return json.Marshal(b.Text)
	}
	if b.Image != nil {
		return json.Marshal(b.Image)
	}
	return json.Marshal(b.Resource)
}
func TextBlock(text string) ContentBlock {
	return ContentBlock{Text: &TextContent{Text: text, Type: "text"}}
}
func ImageBlock(data, mime string) ContentBlock {
	return ContentBlock{Image: &ImageContent{Data: data, MimeType: mime, Type: "image"}}
}
func ResourceBlock(resource EmbeddedResourceResource) ContentBlock {
	return ContentBlock{Resource: &ResourceContent{Type: "resource", Resource: resource}}
}

type PromptRequest struct {
	SessionId string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"content"`
}
type PromptResponse struct {
	StopReason string `json:"stopReason"`
}

type SetSessionConfigOptionValueId struct {
	SessionId string `json:"sessionId"`
	ConfigId  string `json:"configId"`
	Value     string `json:"value"`
}
type SetSessionConfigOptionRequest struct {
	ValueId *SetSessionConfigOptionValueId
}

func (r SetSessionConfigOptionRequest) MarshalJSON() ([]byte, error) { return json.Marshal(r.ValueId) }

type SetSessionConfigOptionResponse struct {
	ConfigOptions []map[string]any `json:"configOptions"`
}
type SessionNotification struct {
	SessionId string         `json:"sessionId"`
	Update    map[string]any `json:"update"`
}

const (
	PlanEntryPriorityHigh     = "high"
	PlanEntryPriorityMedium   = "medium"
	PlanEntryPriorityLow      = "low"
	PlanEntryStatusCompleted  = "completed"
	PlanEntryStatusInProgress = "in_progress"
	PlanEntryStatusPending    = "pending"
)

// Generic Pi extension UI bridge. The Pi host publishes dialog requests as
// session events; the controller relays them to the Web UI and answers
// through the controller-to-host methods below.
const (
	UiPrimitiveSelect  = "select"
	UiPrimitiveConfirm = "confirm"
	UiPrimitiveInput   = "input"
	UiPrimitiveEditor  = "editor"
	UiPrimitiveNotify  = "notify"
)

const (
	// Host-to-controller session.event types.
	UiRequestEvent = "pixie:ui:request"
	UiNotifyEvent  = "pixie:ui:notify"
	UiCancelEvent  = "pixie:ui:cancel"
	// Controller-to-host JSON-RPC methods.
	UiResponseMethod = "session.uiResponse"
	UiCancelMethod   = "session.uiCancel"
)

// UiRequest is one blocking dialog call from a Pi extension.
type UiRequest struct {
	RequestID   string   `json:"requestId"`
	SessionID   string   `json:"sessionId"`
	Primitive   string   `json:"primitive"`
	Title       string   `json:"title,omitempty"`
	Message     string   `json:"message,omitempty"`
	Options     []string `json:"options,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Prefill     string   `json:"prefill,omitempty"`
	TimeoutMs   int64    `json:"timeout,omitempty"`
}

// UiResult is the user's answer to one dialog. A cancelled dialog carries no value.
type UiResult struct {
	Value     any  `json:"value,omitempty"`
	Cancelled bool `json:"cancelled"`
}

// UiResponse answers one pending dialog.
type UiResponse struct {
	SessionID string   `json:"sessionId"`
	RequestID string   `json:"requestId"`
	Result    UiResult `json:"result"`
}
