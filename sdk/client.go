package sdk

import (
	"context"

	"github.com/viant/agently-core/app/store/conversation"
	agrun "github.com/viant/agently-core/pkg/agently/run"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/service/a2a"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/service/scheduler"
)

var (
	_ Client = (*EmbeddedClient)(nil)
	_ Client = (*HTTPClient)(nil)
)

type Mode string

const (
	ModeEmbedded Mode = "embedded"
	ModeHTTP     Mode = "http"
)

type Client interface {
	Mode() Mode

	// Query sends a user message and returns the agent response (ReAct loop).
	Query(ctx context.Context, input *agentsvc.QueryInput) (*agentsvc.QueryOutput, error)

	// GetMessages returns a cursor-paginated page of messages for a conversation.
	GetMessages(ctx context.Context, input *GetMessagesInput) (*MessagePage, error)

	// StreamEvents subscribes to real-time streaming events for a conversation.
	StreamEvents(ctx context.Context, input *StreamEventsInput) (streaming.Subscription, error)

	// CreateConversation initialises a new conversation record.
	CreateConversation(ctx context.Context, input *CreateConversationInput) (*conversation.Conversation, error)

	// ListConversations returns a cursor-paginated list of conversations.
	ListConversations(ctx context.Context, input *ListConversationsInput) (*ConversationPage, error)
	// ListLinkedConversations returns child conversations for a given parent conversation/turn,
	// including status and latest assistant response.
	ListLinkedConversations(ctx context.Context, input *ListLinkedConversationsInput) (*LinkedConversationPage, error)

	// GetConversation retrieves a single conversation by ID.
	GetConversation(ctx context.Context, id string) (*conversation.Conversation, error)
	// UpdateConversation updates mutable conversation fields such as visibility and shareability.
	UpdateConversation(ctx context.Context, input *UpdateConversationInput) (*conversation.Conversation, error)

	// GetRun returns the current state of a run.
	GetRun(ctx context.Context, id string) (*agrun.RunRowsView, error)

	// CancelTurn aborts the active turn. Returns true if a running turn was found.
	CancelTurn(ctx context.Context, turnID string) (bool, error)
	// SteerTurn injects a user message into a currently running turn.
	SteerTurn(ctx context.Context, input *SteerTurnInput) (*SteerTurnOutput, error)
	// CancelQueuedTurn cancels a queued turn.
	CancelQueuedTurn(ctx context.Context, conversationID, turnID string) error
	// MoveQueuedTurn reorders a queued turn up/down in queue order.
	MoveQueuedTurn(ctx context.Context, input *MoveQueuedTurnInput) error
	// EditQueuedTurn updates queued turn starter message content.
	EditQueuedTurn(ctx context.Context, input *EditQueuedTurnInput) error
	// ForceSteerQueuedTurn cancels queued turn and injects its message into active turn.
	ForceSteerQueuedTurn(ctx context.Context, conversationID, turnID string) (*SteerTurnOutput, error)

	// ResolveElicitation delivers the user response for an elicitation prompt.
	ResolveElicitation(ctx context.Context, input *ResolveElicitationInput) error
	// ListPendingElicitations returns unresolved elicitation prompts for a conversation.
	ListPendingElicitations(ctx context.Context, input *ListPendingElicitationsInput) ([]*PendingElicitation, error)
	// ListPendingToolApprovals returns queued tool approvals for a user/conversation.
	ListPendingToolApprovals(ctx context.Context, input *ListPendingToolApprovalsInput) ([]*PendingToolApproval, error)
	// DecideToolApproval resolves a queued tool request with approve/reject.
	DecideToolApproval(ctx context.Context, input *DecideToolApprovalInput) (*DecideToolApprovalOutput, error)

	// ExecuteTool invokes a registered tool by name and returns its textual result.
	ExecuteTool(ctx context.Context, name string, args map[string]interface{}) (string, error)

	// ListToolDefinitions returns the set of tool definitions available in the workspace.
	ListToolDefinitions(ctx context.Context) ([]ToolDefinitionInfo, error)

	// UploadFile stores a file associated with a conversation.
	UploadFile(ctx context.Context, input *UploadFileInput) (*UploadFileOutput, error)

	// DownloadFile retrieves a previously uploaded file.
	DownloadFile(ctx context.Context, input *DownloadFileInput) (*DownloadFileOutput, error)

	// ListFiles returns all files associated with a conversation.
	ListFiles(ctx context.Context, input *ListFilesInput) (*ListFilesOutput, error)

	// ListResources returns resource names for a workspace kind.
	ListResources(ctx context.Context, input *ListResourcesInput) (*ListResourcesOutput, error)

	// GetResource retrieves a single workspace resource by kind and name.
	GetResource(ctx context.Context, input *ResourceRef) (*GetResourceOutput, error)

	// SaveResource creates or updates a workspace resource.
	SaveResource(ctx context.Context, input *SaveResourceInput) error

	// DeleteResource removes a workspace resource.
	DeleteResource(ctx context.Context, input *ResourceRef) error

	// ExportResources exports all resources of the given kinds.
	ExportResources(ctx context.Context, input *ExportResourcesInput) (*ExportResourcesOutput, error)

	// ImportResources imports resources in bulk.
	ImportResources(ctx context.Context, input *ImportResourcesInput) (*ImportResourcesOutput, error)

	// TerminateConversation cancels all active turns and marks the conversation as canceled.
	TerminateConversation(ctx context.Context, conversationID string) error

	// CompactConversation generates an LLM summary of conversation history, archiving old messages.
	CompactConversation(ctx context.Context, conversationID string) error

	// PruneConversation uses an LLM to select and remove low-value messages from conversation history.
	PruneConversation(ctx context.Context, conversationID string) error

	// GetTranscript returns the canonical conversation state for rendering.
	GetTranscript(ctx context.Context, input *GetTranscriptInput, options ...TranscriptOption) (*ConversationStateResponse, error)

	// GetLiveState returns the current canonical state snapshot with an EventCursor.
	// On SSE connect or reconnect the client should call GetLiveState first, then
	// consume the event stream starting at EventCursor to avoid replaying history
	// and to avoid missing events that arrived between page load and SSE connect.
	GetLiveState(ctx context.Context, conversationID string, options ...TranscriptOption) (*ConversationStateResponse, error)

	// GetPayloads returns a map of payload ID → payload for the given IDs.
	// Used for batch payload resolution in feed rendering, replacing per-step fetches.
	GetPayloads(ctx context.Context, ids []string) (map[string]*conversation.Payload, error)

	// GetA2AAgentCard returns the A2A agent card for the given agent.
	GetA2AAgentCard(ctx context.Context, agentID string) (*a2a.AgentCard, error)

	// SendA2AMessage sends a message to an A2A agent and returns the task envelope.
	SendA2AMessage(ctx context.Context, agentID string, req *a2a.SendMessageRequest) (*a2a.SendMessageResponse, error)

	// ListA2AAgents returns agent IDs that have A2A serving enabled.
	ListA2AAgents(ctx context.Context, agentIDs []string) ([]string, error)

	// GetSchedule returns a schedule by ID.
	GetSchedule(ctx context.Context, id string) (*scheduler.Schedule, error)

	// ListSchedules returns all schedules visible to the caller.
	ListSchedules(ctx context.Context) ([]*scheduler.Schedule, error)

	// UpsertSchedules creates or updates schedules in batch.
	UpsertSchedules(ctx context.Context, schedules []*scheduler.Schedule) error

	// RunScheduleNow triggers immediate execution of a schedule.
	RunScheduleNow(ctx context.Context, id string) error
}
