package api

import (
	"time"

	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/runtime/streaming"
)

// Pagination re-exports from data layer.
type PageInput = data.PageInput
type Direction = data.Direction

const (
	DirectionBefore = data.DirectionBefore
	DirectionAfter  = data.DirectionAfter
	DirectionLatest = data.DirectionLatest
)

type MessagePage = data.MessagePage
type ConversationPage = data.ConversationPage

type GetMessagesInput struct {
	ConversationID string
	Page           *PageInput
	ID             string
	TurnID         string
	Roles          []string
	Types          []string
}

type ListConversationsInput struct {
	AgentID          string
	ParentID         string
	ParentTurnID     string
	ExcludeScheduled bool
	Query            string
	Status           string
	Page             *PageInput
}

type ListLinkedConversationsInput struct {
	ParentConversationID string
	ParentTurnID         string
	Page                 *PageInput
}

type LinkedConversationEntry struct {
	ConversationID       string     `json:"conversationId"`
	ParentConversationID string     `json:"parentConversationId,omitempty"`
	ParentTurnID         string     `json:"parentTurnId,omitempty"`
	AgentID              string     `json:"agentId,omitempty"`
	Title                string     `json:"title,omitempty"`
	Status               string     `json:"status,omitempty"`
	Response             string     `json:"response,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            *time.Time `json:"updatedAt,omitempty"`
}

type LinkedConversationPage struct {
	Rows       []*LinkedConversationEntry `json:"rows"`
	NextCursor string                     `json:"nextCursor,omitempty"`
	PrevCursor string                     `json:"prevCursor,omitempty"`
	HasMore    bool                       `json:"hasMore"`
}

type SteerTurnInput struct {
	ConversationID string `json:"conversationId"`
	TurnID         string `json:"turnId"`
	Content        string `json:"content"`
	Role           string `json:"role,omitempty"`
}

type SteerTurnOutput struct {
	MessageID      string `json:"messageId"`
	TurnID         string `json:"turnId,omitempty"`
	Status         string `json:"status,omitempty"`
	CanceledTurnID string `json:"canceledTurnId,omitempty"`
}

type MoveQueuedTurnInput struct {
	ConversationID string `json:"conversationId"`
	TurnID         string `json:"turnId"`
	Direction      string `json:"direction"`
}

type EditQueuedTurnInput struct {
	ConversationID string `json:"conversationId"`
	TurnID         string `json:"turnId"`
	Content        string `json:"content"`
}

type CreateConversationInput struct {
	AgentID              string                 `json:"agentId,omitempty"`
	Title                string                 `json:"title,omitempty"`
	Metadata             map[string]interface{} `json:"metadata,omitempty"`
	ParentConversationID string                 `json:"parentConversationId,omitempty"`
	ParentTurnID         string                 `json:"parentTurnId,omitempty"`
}

type UpdateConversationInput struct {
	ConversationID string `json:"-"`
	Title          string `json:"title,omitempty"`
	Visibility     string `json:"visibility,omitempty"`
	Shareable      *bool  `json:"shareable,omitempty"`
}

type StreamEventsInput struct {
	ConversationID string
	Filter         streaming.Filter
}

type ResolveElicitationInput struct {
	ConversationID string
	ElicitationID  string
	Action         string
	Payload        map[string]interface{}
}

type ListPendingElicitationsInput struct {
	ConversationID string
}

type PendingElicitation struct {
	ConversationID string                 `json:"conversationId"`
	ElicitationID  string                 `json:"elicitationId"`
	MessageID      string                 `json:"messageId"`
	Status         string                 `json:"status"`
	Role           string                 `json:"role"`
	Type           string                 `json:"type"`
	CreatedAt      time.Time              `json:"createdAt"`
	Content        string                 `json:"content,omitempty"`
	Elicitation    map[string]interface{} `json:"elicitation,omitempty"`
}

type WorkspaceDefaults struct {
	Agent           string `json:"agent,omitempty"`
	Model           string `json:"model,omitempty"`
	Embedder        string `json:"embedder,omitempty"`
	AutoSelectTools bool   `json:"autoSelectTools,omitempty"`
}

type WorkspaceCapabilities struct {
	AgentAutoSelection    bool `json:"agentAutoSelection,omitempty"`
	ModelAutoSelection    bool `json:"modelAutoSelection,omitempty"`
	ToolAutoSelection     bool `json:"toolAutoSelection,omitempty"`
	CompactConversation   bool `json:"compactConversation,omitempty"`
	PruneConversation     bool `json:"pruneConversation,omitempty"`
	AnonymousSession      bool `json:"anonymousSession,omitempty"`
	MessageCursor         bool `json:"messageCursor,omitempty"`
	StructuredElicitation bool `json:"structuredElicitation,omitempty"`
	TurnStartedEvent      bool `json:"turnStartedEvent,omitempty"`
}

type StarterTask struct {
	ID          string `json:"id,omitempty"`
	Title       string `json:"title,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
}

type WorkspaceAgentInfo struct {
	ID           string        `json:"id,omitempty"`
	Name         string        `json:"name,omitempty"`
	ModelRef     string        `json:"modelRef,omitempty"`
	StarterTasks []StarterTask `json:"starterTasks,omitempty"`
}

type WorkspaceModelInfo struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type WorkspaceMetadata struct {
	WorkspaceRoot   string                `json:"workspaceRoot,omitempty"`
	DefaultAgent    string                `json:"defaultAgent,omitempty"`
	DefaultModel    string                `json:"defaultModel,omitempty"`
	DefaultEmbedder string                `json:"defaultEmbedder,omitempty"`
	Defaults        *WorkspaceDefaults    `json:"defaults,omitempty"`
	Capabilities    WorkspaceCapabilities `json:"capabilities,omitempty"`
	Agents          []string              `json:"agents,omitempty"`
	Models          []string              `json:"models,omitempty"`
	AgentInfos      []WorkspaceAgentInfo  `json:"agentInfos,omitempty"`
	ModelInfos      []WorkspaceModelInfo  `json:"modelInfos,omitempty"`
	Version         string                `json:"version,omitempty"`
}

type ListTemplatesInput struct{}

type TemplateListItem struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Format      string `json:"format,omitempty"`
}

type ListTemplatesOutput struct {
	Items []TemplateListItem `json:"items"`
}

type GetTemplateInput struct {
	Name            string `json:"name"`
	IncludeDocument *bool  `json:"includeDocument,omitempty"`
}

type GetTemplateOutput struct {
	Name             string                   `json:"name,omitempty"`
	Format           string                   `json:"format,omitempty"`
	Description      string                   `json:"description,omitempty"`
	Instructions     string                   `json:"instructions,omitempty"`
	Fences           []map[string]interface{} `json:"fences,omitempty"`
	Schema           map[string]interface{}   `json:"schema,omitempty"`
	Examples         []map[string]interface{} `json:"examples,omitempty"`
	IncludedDocument bool                     `json:"includedDocument,omitempty"`
}

type ListPendingToolApprovalsInput struct {
	UserID         string
	ConversationID string
	Status         string
	Limit          int
	Offset         int
}

type PendingToolApproval struct {
	ID             string                 `json:"id"`
	UserID         string                 `json:"userId"`
	ConversationID string                 `json:"conversationId,omitempty"`
	TurnID         string                 `json:"turnId,omitempty"`
	MessageID      string                 `json:"messageId,omitempty"`
	ToolName       string                 `json:"toolName"`
	Title          string                 `json:"title,omitempty"`
	Arguments      map[string]interface{} `json:"arguments,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	Status         string                 `json:"status"`
	Decision       string                 `json:"decision,omitempty"`
	CreatedAt      time.Time              `json:"createdAt"`
	UpdatedAt      *time.Time             `json:"updatedAt,omitempty"`
	ErrorMessage   string                 `json:"errorMessage,omitempty"`
}

type ApprovalOption struct {
	ID          string      `json:"id"`
	Label       string      `json:"label"`
	Description string      `json:"description,omitempty"`
	Item        interface{} `json:"item,omitempty"`
	Selected    bool        `json:"selected"`
}

type ApprovalEditor struct {
	Name        string            `json:"name"`
	Kind        string            `json:"kind"`
	Path        string            `json:"path,omitempty"`
	Label       string            `json:"label,omitempty"`
	Description string            `json:"description,omitempty"`
	Options     []*ApprovalOption `json:"options,omitempty"`
}

type ApprovalCallback struct {
	ElementID string `json:"elementId,omitempty"`
	Event     string `json:"event,omitempty"`
	Handler   string `json:"handler,omitempty"`
}

type ApprovalForgeView struct {
	WindowRef    string              `json:"windowRef,omitempty"`
	ContainerRef string              `json:"containerRef,omitempty"`
	DataSource   string              `json:"dataSource,omitempty"`
	Callbacks    []*ApprovalCallback `json:"callbacks,omitempty"`
}

type ApprovalMeta struct {
	Type        string             `json:"type,omitempty"`
	ToolName    string             `json:"toolName,omitempty"`
	Title       string             `json:"title,omitempty"`
	Message     string             `json:"message,omitempty"`
	AcceptLabel string             `json:"acceptLabel,omitempty"`
	RejectLabel string             `json:"rejectLabel,omitempty"`
	CancelLabel string             `json:"cancelLabel,omitempty"`
	Editors     []*ApprovalEditor  `json:"editors,omitempty"`
	Forge       *ApprovalForgeView `json:"forge,omitempty"`
}

type ApprovalCallbackPayload struct {
	Approval     *ApprovalMeta          `json:"approval,omitempty"`
	EditedFields map[string]interface{} `json:"editedFields,omitempty"`
	OriginalArgs map[string]interface{} `json:"originalArgs,omitempty"`
}

type ApprovalCallbackResult struct {
	Allow   bool                   `json:"allow"`
	Message string                 `json:"message,omitempty"`
	Payload map[string]interface{} `json:"payload,omitempty"`
}

type PendingToolApprovalPage struct {
	Rows    []*PendingToolApproval `json:"rows"`
	Total   int                    `json:"total"`
	Limit   int                    `json:"limit"`
	Offset  int                    `json:"offset,omitempty"`
	HasMore bool                   `json:"hasMore,omitempty"`
}

type DecideToolApprovalInput struct {
	ID            string                 `json:"id,omitempty"`
	Action        string                 `json:"action,omitempty"`
	UserID        string                 `json:"userId,omitempty"`
	Reason        string                 `json:"reason,omitempty"`
	Note          string                 `json:"note,omitempty"`
	Decision      string                 `json:"decision,omitempty"`
	EditedFields  map[string]interface{} `json:"editedFields,omitempty"`
	CallbackState map[string]interface{} `json:"callbackState,omitempty"`
	Payload       map[string]interface{} `json:"payload,omitempty"`
}

type DecideToolApprovalOutput struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type UploadFileInput struct {
	ConversationID string
	Path           string
	Name           string
	ContentType    string
	Data           []byte
}

type UploadFileOutput struct {
	ID       string `json:"id,omitempty"`
	URI      string `json:"uri"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	MimeType string `json:"mimeType,omitempty"`
}

type DownloadFileInput struct {
	ConversationID string
	FileID         string
	URI            string
}

type DownloadFileOutput struct {
	Name        string
	ContentType string
	Data        []byte
}

type ListFilesInput struct {
	ConversationID string
	Prefix         string
	Page           *PageInput
}

type FileEntry struct {
	ID          string    `json:"id,omitempty"`
	URI         string    `json:"uri"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	IsDir       bool      `json:"isDir"`
	ContentType string    `json:"contentType,omitempty"`
	ModifiedAt  time.Time `json:"modifiedAt,omitempty"`
}

type ListFilesOutput struct {
	Files []*FileEntry `json:"files,omitempty"`
	Rows  []*FileEntry `json:"rows,omitempty"`
	Page  *PageInput   `json:"page,omitempty"`
}

type ToolDefinitionInfo struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	Parameters   map[string]interface{} `json:"parameters,omitempty"`
	Required     []string               `json:"required,omitempty"`
	OutputSchema map[string]interface{} `json:"output_schema,omitempty"`
	Cacheable    bool                   `json:"cacheable,omitempty"`
}

type ResourceRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type ListResourcesInput struct {
	Kind string
}

type ListResourcesOutput struct {
	Names     []string       `json:"names,omitempty"`
	Resources []*ResourceRef `json:"resources"`
}

type GetResourceOutput struct {
	Kind     string    `json:"kind,omitempty"`
	Name     string    `json:"name,omitempty"`
	Data     []byte    `json:"data,omitempty"`
	Resource *Resource `json:"resource,omitempty"`
}

type SaveResourceInput struct {
	Kind        string
	Name        string
	Data        []byte
	Content     []byte
	ContentType string
}

type Resource struct {
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	Data        []byte    `json:"data,omitempty"`
	ContentType string    `json:"contentType,omitempty"`
	Content     []byte    `json:"content,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
}

type ExportResourcesInput struct {
	Kinds []string `json:"kinds,omitempty"`
}

type ExportResourcesOutput struct {
	Data      []byte     `json:"data,omitempty"`
	Resources []Resource `json:"resources,omitempty"`
}

type ImportResourcesInput struct {
	Data      []byte     `json:"data,omitempty"`
	Resources []Resource `json:"resources,omitempty"`
	Replace   bool       `json:"replace,omitempty"`
}

type ImportResourcesOutput struct {
	Skipped  int `json:"skipped,omitempty"`
	Imported int `json:"imported"`
}

type GetTranscriptInput struct {
	ConversationID    string
	Since             string
	IncludeModelCalls bool
	IncludeToolCalls  bool
}

type QuerySelector struct {
	Path    string `json:"path,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Offset  int    `json:"offset,omitempty"`
	OrderBy string `json:"orderBy,omitempty"`
}
