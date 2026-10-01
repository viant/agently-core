package conversation

import (
	"github.com/viant/xdatly/response"

	"time"
)

// Public data shapes retained for Go caller and JSON compatibility.

type ConversationInput struct {
	Id                string
	Since             string
	IncludeTranscript bool
	IncludeModelCal   bool
	IncludeToolCall   bool
	AgentId           string
	ParentId          string
	ParentTurnId      string
	ExcludeChildren   bool
	ExcludeScheduled  bool
	ScheduleId        string
	ScheduleRunId     string
	Query             string
	StatusFilter      string
	HasScheduleId     bool
	Has               *ConversationInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type ConversationInputHas struct {
	Id                bool
	Since             bool
	IncludeTranscript bool
	IncludeModelCal   bool
	IncludeToolCall   bool
	AgentId           bool
	ParentId          bool
	ParentTurnId      bool
	ExcludeChildren   bool
	ExcludeScheduled  bool
	ScheduleId        bool
	ScheduleRunId     bool
	Query             bool
	StatusFilter      bool
	HasScheduleId     bool
}

type ConversationOutput struct {
	response.Status `json:",omitempty"`
	Data            []*ConversationView
	Metrics         response.Metrics
}

type ConversationView struct {
	LastTurnId               *string    `sqlx:"last_turn_id"`
	Stage                    string     `sqlx:"stage"`
	Id                       string     `sqlx:"id"`
	Summary                  *string    `sqlx:"summary"`
	LastActivity             *time.Time `sqlx:"last_activity"`
	UsageInputTokens         *int       `sqlx:"usage_input_tokens"`
	UsageOutputTokens        *int       `sqlx:"usage_output_tokens"`
	UsageEmbeddingTokens     *int       `sqlx:"usage_embedding_tokens"`
	CreatedAt                time.Time  `sqlx:"created_at"`
	UpdatedAt                *time.Time `sqlx:"updated_at"`
	CreatedByUserId          *string    `sqlx:"created_by_user_id"`
	AgentId                  *string    `sqlx:"agent_id"`
	DefaultModelProvider     *string    `sqlx:"default_model_provider"`
	DefaultModel             *string    `sqlx:"default_model"`
	DefaultModelParams       *string    `sqlx:"default_model_params"`
	Title                    *string    `sqlx:"title"`
	ConversationParentId     *string    `sqlx:"conversation_parent_id"`
	ConversationParentTurnId *string    `sqlx:"conversation_parent_turn_id"`
	Metadata                 *string    `sqlx:"metadata"`
	Visibility               string     `sqlx:"visibility"`
	Shareable                int        `sqlx:"shareable"`
	Status                   *string    `sqlx:"status"`
	Scheduled                *int       `sqlx:"scheduled"`
	ScheduleId               *string    `sqlx:"schedule_id"`
	ScheduleRunId            *string    `sqlx:"schedule_run_id"`
	ScheduleKind             *string    `sqlx:"schedule_kind"`
	ScheduleTimezone         *string    `sqlx:"schedule_timezone"`
	ScheduleCronExpr         *string    `sqlx:"schedule_cron_expr"`
	ExternalTaskRef          *string    `sqlx:"external_task_ref"`
	Transcript               []*TranscriptView
	Usage                    *UsageView
}

type TranscriptView struct {
	ElapsedInSec          int       `sqlx:"elapsedInSec"`
	Stage                 string    `sqlx:"stage"`
	Id                    string    `sqlx:"id"`
	ConversationId        string    `sqlx:"conversation_id"`
	CreatedAt             time.Time `sqlx:"created_at"`
	QueueSeq              *int      `sqlx:"queue_seq"`
	Origin                *string   `sqlx:"origin"`
	GoalID                *string   `sqlx:"goal_id"`
	StatusReason          *string   `sqlx:"status_reason"`
	Status                string    `sqlx:"status"`
	ErrorMessage          *string   `sqlx:"error_message"`
	StartedByMessageId    *string   `sqlx:"started_by_message_id"`
	RetryOf               *string   `sqlx:"retry_of"`
	AgentIdUsed           *string   `sqlx:"agent_id_used"`
	AgentConfigUsedId     *string   `sqlx:"agent_config_used_id"`
	ModelOverrideProvider *string   `sqlx:"model_override_provider"`
	ModelOverride         *string   `sqlx:"model_override"`
	ModelParamsOverride   *string   `sqlx:"model_params_override"`
	RunId                 *string   `sqlx:"run_id"`
	Message               []*MessageView
}

type MessageView struct {
	Elicitation          Elicitation `sqlx:"ELICITATION"`
	Id                   string      `sqlx:"id"`
	ConversationId       string      `sqlx:"conversation_id"`
	TurnId               *string     `sqlx:"turn_id"`
	Archived             *int        `sqlx:"archived"`
	Sequence             *int        `sqlx:"sequence"`
	CreatedAt            time.Time   `sqlx:"created_at"`
	UpdatedAt            *time.Time  `sqlx:"updated_at"`
	CreatedByUserId      *string     `sqlx:"created_by_user_id"`
	Status               *string     `sqlx:"status"`
	Mode                 *string     `sqlx:"mode"`
	Role                 string      `sqlx:"role"`
	Type                 string      `sqlx:"type"`
	Content              *string     `sqlx:"content"`
	RawContent           *string     `sqlx:"raw_content"`
	Summary              *string     `sqlx:"summary"`
	ContextSummary       *string     `sqlx:"context_summary"`
	Tags                 *string     `sqlx:"tags"`
	Interim              int         `sqlx:"interim"`
	ElicitationId        *string     `sqlx:"elicitation_id"`
	ParentMessageId      *string     `sqlx:"parent_message_id"`
	SupersededBy         *string     `sqlx:"superseded_by"`
	LinkedConversationId *string     `sqlx:"linked_conversation_id"`
	AttachmentPayloadId  *string     `sqlx:"attachment_payload_id"`
	ElicitationPayloadId *string     `sqlx:"elicitation_payload_id"`
	ToolName             *string     `sqlx:"tool_name"`
	EmbeddingIndex       *string     `sqlx:"embedding_index"`
	Narration            *string     `sqlx:"preamble"`
	Iteration            *int        `sqlx:"iteration"`
	Phase                *string     `sqlx:"phase"`
	MessageToolCall      *MessageToolCallView
	ToolMessage          []*ToolMessageView
	UserElicitationData  *UserElicitationDataView
	LinkedConversation   *LinkedConversationView
	Attachment           []*AttachmentView
	ModelCall            *ModelCallView
}

type ToolMessageView struct {
	Id                   string    `sqlx:"id"`
	ParentMessageId      *string   `sqlx:"parent_message_id"`
	CreatedAt            time.Time `sqlx:"created_at"`
	Sequence             *int      `sqlx:"sequence"`
	Type                 string    `sqlx:"type"`
	Content              *string   `sqlx:"content"`
	ToolName             *string   `sqlx:"tool_name"`
	Iteration            *int      `sqlx:"iteration"`
	LinkedConversationId *string   `sqlx:"linked_conversation_id"`
	ToolCall             *ToolCallView
}

type ToolCallView struct {
	MessageSequence   *int       `sqlx:"message_sequence"`
	MessageId         string     `sqlx:"message_id"`
	TurnId            *string    `sqlx:"turn_id"`
	OpId              string     `sqlx:"op_id"`
	Attempt           int        `sqlx:"attempt"`
	ToolName          string     `sqlx:"tool_name"`
	ToolKind          string     `sqlx:"tool_kind"`
	Status            string     `sqlx:"status"`
	RequestHash       *string    `sqlx:"request_hash"`
	ErrorCode         *string    `sqlx:"error_code"`
	ErrorMessage      *string    `sqlx:"error_message"`
	Retriable         *int       `sqlx:"retriable"`
	StartedAt         *time.Time `sqlx:"started_at"`
	CompletedAt       *time.Time `sqlx:"completed_at"`
	LatencyMs         *int       `sqlx:"latency_ms"`
	Cost              *float64   `sqlx:"cost"`
	TraceId           *string    `sqlx:"trace_id"`
	SpanId            *string    `sqlx:"span_id"`
	RequestPayloadId  *string    `sqlx:"request_payload_id"`
	ResponsePayloadId *string    `sqlx:"response_payload_id"`
	RunId             *string    `sqlx:"run_id"`
	Iteration         *int       `sqlx:"iteration"`
	RequestPayload    *ModelCallStreamPayloadView
	ResponsePayload   *ModelCallStreamPayloadView
}

type MessageToolCallView struct {
	MessageSequence        *int       `sqlx:"message_sequence"`
	MessageId              string     `sqlx:"message_id"`
	TurnId                 *string    `sqlx:"turn_id"`
	OpId                   string     `sqlx:"op_id"`
	Attempt                int        `sqlx:"attempt"`
	ToolName               string     `sqlx:"tool_name"`
	ToolKind               string     `sqlx:"tool_kind"`
	Status                 string     `sqlx:"status"`
	RequestHash            *string    `sqlx:"request_hash"`
	ErrorCode              *string    `sqlx:"error_code"`
	ErrorMessage           *string    `sqlx:"error_message"`
	Retriable              *int       `sqlx:"retriable"`
	StartedAt              *time.Time `sqlx:"started_at"`
	CompletedAt            *time.Time `sqlx:"completed_at"`
	LatencyMs              *int       `sqlx:"latency_ms"`
	Cost                   *float64   `sqlx:"cost"`
	TraceId                *string    `sqlx:"trace_id"`
	SpanId                 *string    `sqlx:"span_id"`
	RequestPayloadId       *string    `sqlx:"request_payload_id"`
	ResponsePayloadId      *string    `sqlx:"response_payload_id"`
	RunId                  *string    `sqlx:"run_id"`
	Iteration              *int       `sqlx:"iteration"`
	MessageRequestPayload  *ModelCallStreamPayloadView
	MessageResponsePayload *ModelCallStreamPayloadView
}

type ModelCallStreamPayloadView struct {
	Id          string  `sqlx:"id"`
	InlineBody  *string `sqlx:"inline_body"`
	Compression string  `sqlx:"compression"`
}

type UserElicitationDataView struct {
	InlineBody  *string `sqlx:"inline_body"`
	Compression string  `sqlx:"compression"`
	MessageId   string  `sqlx:"message_id"`
}

type LinkedConversationView struct {
	Id        string     `sqlx:"id"`
	Status    *string    `sqlx:"status"`
	CreatedAt time.Time  `sqlx:"created_at"`
	UpdatedAt *time.Time `sqlx:"updated_at"`
}

type AttachmentView struct {
	InlineBody      *[]uint8 `sqlx:"inline_body"`
	Compression     string   `sqlx:"compression"`
	Uri             *string  `sqlx:"uri"`
	MimeType        string   `sqlx:"mime_type"`
	ParentMessageId *string  `sqlx:"parent_message_id"`
}

type ModelCallView struct {
	CompletedAt                        *time.Time `sqlx:"completed_at"`
	CompletionAcceptedPredictionTokens *int       `sqlx:"completion_accepted_prediction_tokens"`
	CompletionAudioTokens              *int       `sqlx:"completion_audio_tokens"`
	CompletionReasoningTokens          *int       `sqlx:"completion_reasoning_tokens"`
	CompletionRejectedPredictionTokens *int       `sqlx:"completion_rejected_prediction_tokens"`
	CompletionTokens                   *int       `sqlx:"completion_tokens"`
	Cost                               *float64   `sqlx:"cost"`
	ErrorCode                          *string    `sqlx:"error_code"`
	ErrorMessage                       *string    `sqlx:"error_message"`
	FinishReason                       *string    `sqlx:"finish_reason"`
	Iteration                          *int       `sqlx:"iteration"`
	LatencyMs                          *int       `sqlx:"latency_ms"`
	MessageId                          string     `sqlx:"message_id"`
	Model                              string     `sqlx:"model"`
	ModelKind                          string     `sqlx:"model_kind"`
	PromptAudioTokens                  *int       `sqlx:"prompt_audio_tokens"`
	PromptCachedTokens                 *int       `sqlx:"prompt_cached_tokens"`
	PromptTokens                       *int       `sqlx:"prompt_tokens"`
	Provider                           string     `sqlx:"provider"`
	ProviderRequestPayloadId           *string    `sqlx:"provider_request_payload_id"`
	ProviderResponsePayloadId          *string    `sqlx:"provider_response_payload_id"`
	RequestPayloadId                   *string    `sqlx:"request_payload_id"`
	ResponsePayloadId                  *string    `sqlx:"response_payload_id"`
	RunId                              *string    `sqlx:"run_id"`
	SpanId                             *string    `sqlx:"span_id"`
	StartedAt                          *time.Time `sqlx:"started_at"`
	Status                             string     `sqlx:"status"`
	StreamPayloadId                    *string    `sqlx:"stream_payload_id"`
	TotalTokens                        *int       `sqlx:"total_tokens"`
	TraceId                            *string    `sqlx:"trace_id"`
	TurnId                             *string    `sqlx:"turn_id"`
	ModelCallRequestPayload            *ModelCallStreamPayloadView
	ModelCallProviderRequestPayload    *ModelCallStreamPayloadView
	ModelCallResponsePayload           *ModelCallStreamPayloadView
	ModelCallProviderResponsePayload   *ModelCallStreamPayloadView
	ToolCallLinks                      []*ToolCallLinksView
	ModelCallStreamPayload             *ModelCallStreamPayloadView
}

type ToolCallLinksView struct {
	MessageId string  `sqlx:"message_id"`
	OpId      string  `sqlx:"op_id"`
	TraceId   *string `sqlx:"trace_id"`
}

type UsageView struct {
	ConversationId                     string   `sqlx:"conversation_id"`
	Cost                               *float64 `sqlx:"cost"`
	PromptTokens                       *int     `sqlx:"prompt_tokens"`
	PromptCachedTokens                 *int     `sqlx:"prompt_cached_tokens"`
	PromptAudioTokens                  *int     `sqlx:"prompt_audio_tokens"`
	CompletionTokens                   *int     `sqlx:"completion_tokens"`
	CompletionReasoningTokens          *int     `sqlx:"completion_reasoning_tokens"`
	CompletionAudioTokens              *int     `sqlx:"completion_audio_tokens"`
	CompletionAcceptedPredictionTokens *int     `sqlx:"completion_accepted_prediction_tokens"`
	CompletionRejectedPredictionTokens *int     `sqlx:"completion_rejected_prediction_tokens"`
	TotalTokens                        *int     `sqlx:"total_tokens"`
	Model                              []*ModelView
}

type ModelView struct {
	ConversationId                     string   `sqlx:"conversation_id"`
	Provider                           string   `sqlx:"provider"`
	Model                              string   `sqlx:"model"`
	ExecutionRole                      string   `sqlx:"execution_role"`
	PromptTokens                       *int     `sqlx:"prompt_tokens"`
	PromptCachedTokens                 *int     `sqlx:"prompt_cached_tokens"`
	PromptAudioTokens                  *int     `sqlx:"prompt_audio_tokens"`
	CompletionTokens                   *int     `sqlx:"completion_tokens"`
	CompletionReasoningTokens          *int     `sqlx:"completion_reasoning_tokens"`
	CompletionAudioTokens              *int     `sqlx:"completion_audio_tokens"`
	CompletionAcceptedPredictionTokens *int     `sqlx:"completion_accepted_prediction_tokens"`
	CompletionRejectedPredictionTokens *int     `sqlx:"completion_rejected_prediction_tokens"`
	TotalTokens                        *int     `sqlx:"total_tokens"`
	Cost                               *float64 `sqlx:"cost"`
}

var ConversationPathURI = "/v1/api/agently/conversation/{id}"
