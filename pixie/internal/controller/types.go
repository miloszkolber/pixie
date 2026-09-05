package controller

type AgentOperations struct {
	DeleteSession         bool `json:"deleteSession"`
	ForkSession           bool `json:"forkSession"`
	PromptImage           bool `json:"promptImage"`
	PromptEmbeddedContext bool `json:"promptEmbeddedContext"`
	HTTPMCP               bool `json:"httpMcp"`
	Steer                 bool `json:"steer"`
	RenameSession         bool `json:"renameSession"`
	ArchiveSession        bool `json:"archiveSession"`
	Administration        bool `json:"administration"`
}

type AgentProfile struct {
	Capabilities    map[string]int  `json:"capabilities"`
	Name            string          `json:"name"`
	Version         string          `json:"version"`
	Pi              bool            `json:"pi"`
	Compatible      bool            `json:"compatible"`
	MissingRequired []string        `json:"missingRequired"`
	Operations      AgentOperations `json:"operations"`
	identity        string
}

type WireModel struct {
	MetadataComplete *bool    `json:"metadataComplete,omitempty"`
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Provider         string   `json:"provider"`
	ContextWindow    *int     `json:"contextWindow,omitempty"`
	MaxTokens        *int     `json:"maxTokens,omitempty"`
	Reasoning        *bool    `json:"reasoning,omitempty"`
	ThinkingLevels   []string `json:"thinkingLevels,omitempty"`
	Input            []string `json:"input,omitempty"`
	Cost             any      `json:"cost,omitempty"`
	Available        bool     `json:"available"`
	Hidden           bool     `json:"hidden"`
}

type SessionSettlement struct {
	StopReason   string `json:"stopReason"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

type SessionPlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority"`
	Status   string `json:"status"`
}

type SessionPlanState struct {
	Entries   []SessionPlanEntry `json:"entries"`
	Truncated bool               `json:"truncated,omitempty"`
}

type SessionQueue struct {
	Revision string      `json:"revision"`
	Steering []string    `json:"steering"`
	FollowUp []string    `json:"followUp"`
	Blocked  *QueueBlock `json:"blocked,omitempty"`
}

type QueueBlock struct {
	Lane   string `json:"lane"`
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

type SessionSummary struct {
	Capabilities    map[string]int     `json:"capabilities,omitempty"`
	ConfigOptions   []any              `json:"configOptions,omitempty"`
	SessionID       string             `json:"sessionId"`
	ProjectID       string             `json:"projectId"`
	CWD             string             `json:"cwd"`
	ParentSessionID string             `json:"parentSessionId,omitempty"`
	Title           string             `json:"title"`
	Model           *WireModel         `json:"model"`
	ThinkingLevel   string             `json:"thinkingLevel"`
	IsStreaming     bool               `json:"isStreaming"`
	MessageCount    int                `json:"messageCount"`
	UpdatedAt       int64              `json:"updatedAt"`
	Live            bool               `json:"live"`
	Archived        bool               `json:"archived"`
	LastSettlement  *SessionSettlement `json:"lastSettlement,omitempty"`
	Queue           *SessionQueue      `json:"queue,omitempty"`
}

type SessionStats struct {
	SessionID     string          `json:"sessionId"`
	TotalMessages int             `json:"totalMessages"`
	Tokens        SessionTokens   `json:"tokens"`
	Cost          float64         `json:"cost"`
	CostCurrency  string          `json:"costCurrency,omitempty"`
	Reported      map[string]bool `json:"reported,omitempty"`
	ContextUsage  map[string]any  `json:"contextUsage,omitempty"`
}

type SessionTokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Total      int64 `json:"total"`
}

type codedError struct {
	code    string
	message string
}

func (e *codedError) Error() string     { return e.message }
func (e *codedError) ErrorCode() string { return e.code }

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsNUL(value string) bool {
	for _, character := range value {
		if character == 0 {
			return true
		}
	}
	return false
}
