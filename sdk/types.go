package sdk

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

// MessagePage is the paginated result from GetMessages.
type MessagePage = data.MessagePage

// ConversationPage is the paginated result from ListConversations.
type ConversationPage = data.ConversationPage

// GetMessagesInput controls which messages are returned for a conversation.
type GetMessagesInput struct {
	ConversationID string
	Page           *PageInput
	// Optional filters
	ID     string
	TurnID string
	Roles  []string
	Types  []string
}

// ListConversationsInput controls the conversation listing query.
type ListConversationsInput struct {
	AgentID      string
	ParentID     string
	ParentTurnID string
	Query        string
	Status       string
	Page         *PageInput
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
	Role           string `json:"role,omitempty"` // default "user"
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
	Direction      string `json:"direction"` // up|down
}

type EditQueuedTurnInput struct {
	ConversationID string `json:"conversationId"`
	TurnID         string `json:"turnId"`
	Content        string `json:"content"`
}

// CreateConversationInput holds fields needed to create a new conversation.
type CreateConversationInput struct {
	AgentID  string
	Title    string
	Metadata map[string]interface{}
}

// UpdateConversationInput updates mutable conversation fields.
// At least one of Title, Visibility, or Shareable must be provided.
type UpdateConversationInput struct {
	ConversationID string `json:"-"`
	Title          string `json:"title,omitempty"`
	Visibility     string `json:"visibility,omitempty"` // private|public
	Shareable      *bool  `json:"shareable,omitempty"`
}

// StreamEventsInput controls which streaming events to subscribe to.
type StreamEventsInput struct {
	ConversationID string
	Filter         streaming.Filter
}

// ResolveElicitationInput carries the user response for an elicitation prompt.
type ResolveElicitationInput struct {
	ConversationID string
	ElicitationID  string
	Action         string
	Payload        map[string]interface{}
}

// ListPendingElicitationsInput controls pending elicitation lookup.
type ListPendingElicitationsInput struct {
	ConversationID string
}

// PendingElicitation represents one pending elicitation message.
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
	Agent    string `json:"agent,omitempty"`
	Model    string `json:"model,omitempty"`
	Embedder string `json:"embedder,omitempty"`
}

type WorkspaceMetadata struct {
	WorkspaceRoot   string             `json:"workspaceRoot,omitempty"`
	DefaultAgent    string             `json:"defaultAgent,omitempty"`
	DefaultModel    string             `json:"defaultModel,omitempty"`
	DefaultEmbedder string             `json:"defaultEmbedder,omitempty"`
	Defaults        *WorkspaceDefaults `json:"defaults,omitempty"`
	Agents          []string           `json:"agents,omitempty"`
	Models          []string           `json:"models,omitempty"`
	Version         string             `json:"version,omitempty"`
}

type ListPendingToolApprovalsInput struct {
	UserID         string
	ConversationID string
	Status         string
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

type DecideToolApprovalInput struct {
	ID      string                 `json:"id"`
	Action  string                 `json:"action"` // approve|reject
	UserID  string                 `json:"userId,omitempty"`
	Reason  string                 `json:"reason,omitempty"`
	Payload map[string]interface{} `json:"payload,omitempty"`
}

type DecideToolApprovalOutput struct {
	Status string `json:"status"`
}

// UploadFileInput describes a file to upload.
type UploadFileInput struct {
	ConversationID string
	Name           string
	ContentType    string
	Data           []byte
}

// UploadFileOutput is the result of a file upload.
type UploadFileOutput struct {
	ID  string
	URI string
}

// DownloadFileInput identifies a file to download.
type DownloadFileInput struct {
	ConversationID string
	FileID         string
}

// DownloadFileOutput carries the downloaded file contents.
type DownloadFileOutput struct {
	Name        string
	ContentType string
	Data        []byte
}

// ListFilesInput controls which files are listed.
type ListFilesInput struct {
	ConversationID string
}

// FileEntry describes a single file in a conversation.
type FileEntry struct {
	ID          string
	Name        string
	ContentType string
	Size        int64
}

// ListFilesOutput is the result of listing files.
type ListFilesOutput struct {
	Files []*FileEntry
}

// ResourceRef identifies a workspace resource by kind and name.
type ResourceRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// ListResourcesInput controls which resources are listed.
type ListResourcesInput struct {
	Kind string `json:"kind"`
}

// ListResourcesOutput is the result of listing workspace resources.
type ListResourcesOutput struct {
	Names []string `json:"names"`
}

// GetResourceOutput carries the raw content of a workspace resource.
type GetResourceOutput struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Data []byte `json:"data"`
}

// SaveResourceInput describes a workspace resource to create or update.
type SaveResourceInput struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Data []byte `json:"data"`
}

// Resource is a single workspace resource used for bulk import/export.
type Resource struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Data []byte `json:"data"`
}

// ExportResourcesInput controls which resources are exported.
type ExportResourcesInput struct {
	Kinds []string `json:"kinds"` // nil = all
}

// ExportResourcesOutput carries the exported resources.
type ExportResourcesOutput struct {
	Resources []Resource `json:"resources"`
}

// ImportResourcesInput carries resources to import in bulk.
type ImportResourcesInput struct {
	Resources []Resource `json:"resources"`
	Replace   bool       `json:"replace"`
}

// ImportResourcesOutput summarises the import operation.
type ImportResourcesOutput struct {
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
}

// GetTranscriptInput controls which transcript turns are returned for a conversation.
type GetTranscriptInput struct {
	ConversationID    string
	Since             string // optional: turn ID or message ID cursor
	IncludeModelCalls bool
	IncludeToolCalls  bool
}

type QuerySelector struct {
	Limit   int    `json:"limit,omitempty"`
	Offset  int    `json:"offset,omitempty"`
	OrderBy string `json:"orderBy,omitempty"`
}

type TranscriptOption func(*transcriptOptions)

type transcriptOptions struct {
	selectors    map[string]*QuerySelector
	includeFeeds bool
}

const (
	TranscriptSelectorTurn        = "Transcript"
	TranscriptSelectorMessage     = "Message"
	TranscriptSelectorToolMessage = "ToolMessage"
)

func ensureTranscriptSelector(o *transcriptOptions, name string) *QuerySelector {
	if o == nil || name == "" {
		return nil
	}
	if o.selectors == nil {
		o.selectors = map[string]*QuerySelector{}
	}
	if o.selectors[name] == nil {
		o.selectors[name] = &QuerySelector{}
	}
	return o.selectors[name]
}

func WithTranscriptSelector(name string, selector *QuerySelector) TranscriptOption {
	return func(o *transcriptOptions) {
		if selector == nil {
			return
		}
		if o.selectors == nil {
			o.selectors = map[string]*QuerySelector{}
		}
		o.selectors[name] = selector
	}
}

// WithIncludeFeeds enables tool feed resolution in the transcript response.
// When enabled, matching tool calls are scanned and their response payloads
// fetched to populate the Feeds field on ConversationState.
func WithIncludeFeeds() TranscriptOption {
	return func(o *transcriptOptions) {
		o.includeFeeds = true
	}
}

func WithTranscriptTurnSelector(selector *QuerySelector) TranscriptOption {
	return WithTranscriptSelector(TranscriptSelectorTurn, selector)
}

func WithTranscriptMessageSelector(selector *QuerySelector) TranscriptOption {
	return WithTranscriptSelector(TranscriptSelectorMessage, selector)
}

func WithTranscriptToolMessageSelector(selector *QuerySelector) TranscriptOption {
	return WithTranscriptSelector(TranscriptSelectorToolMessage, selector)
}
