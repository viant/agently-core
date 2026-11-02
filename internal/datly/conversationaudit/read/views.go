package read

import "time"

type Row struct {
	Id                    string     `sqlx:"id,primaryKey=true" json:"id"`
	ConversationId        string     `sqlx:"conversation_id,refTable=conversation,refColumn=id,required=true" json:"conversationId"`
	CreatedAt             time.Time  `sqlx:"created_at,required=true" json:"createdAt"`
	Status                string     `sqlx:"status,required=true" json:"status"`
	GoalId                *string    `sqlx:"goal_id" json:"goalId"`
	QueueSeq              *int       `sqlx:"queue_seq" json:"queueSeq"`
	Origin                *string    `sqlx:"origin" json:"origin"`
	StatusReason          *string    `sqlx:"status_reason" json:"statusReason"`
	FeedbackRating        *int       `sqlx:"feedback_rating,required=true" json:"feedbackRating"`
	FeedbackComment       *string    `sqlx:"feedback_comment" json:"feedbackComment"`
	FeedbackUpdatedAt     *time.Time `sqlx:"feedback_updated_at" json:"feedbackUpdatedAt"`
	ErrorMessage          *string    `sqlx:"error_message" json:"errorMessage"`
	StartedByMessageId    *string    `sqlx:"started_by_message_id" json:"startedByMessageId"`
	RetryOf               *string    `sqlx:"retry_of" json:"retryOf"`
	AgentIdUsed           *string    `sqlx:"agent_id_used" json:"agentIdUsed"`
	AgentConfigUsedId     *string    `sqlx:"agent_config_used_id" json:"agentConfigUsedId"`
	ModelOverrideProvider *string    `sqlx:"model_override_provider" json:"modelOverrideProvider"`
	ModelOverride         *string    `sqlx:"model_override" json:"modelOverride"`
	ModelParamsOverride   *string    `sqlx:"model_params_override" json:"modelParamsOverride"`
	RunId                 *string    `sqlx:"run_id" json:"runId"`
	OwnerID               string     `sqlx:"owner_id" json:"userId"`
	ConversationTitle     *string    `sqlx:"conversation_title" json:"conversationTitle"`
	ConversationCreatedAt time.Time  `sqlx:"conversation_created_at" json:"conversationCreatedAt"`
	ModelCalls            int        `sqlx:"model_calls" json:"modelCalls"`
	ToolCalls             int        `sqlx:"tool_calls" json:"toolCalls"`
	InputTokens           *int       `sqlx:"input_tokens" json:"inputTokens"`
	OutputTokens          *int       `sqlx:"output_tokens" json:"outputTokens"`
	Cost                  *float64   `sqlx:"cost" json:"cost"`
	InputPreview          *string    `sqlx:"input_preview" json:"inputPreview"`
	OutputPreview         *string    `sqlx:"output_preview" json:"outputPreview"`
	KnownInputTokens      int        `sqlx:"known_input_tokens" json:"knownInputTokens"`
	KnownOutputTokens     int        `sqlx:"known_output_tokens" json:"knownOutputTokens"`
	KnownCosts            int        `sqlx:"known_costs" json:"knownCosts"`
	Runs                  []*Run     `view:"runs,type=Run,table=run" on:"Id:reader.id=TurnID:runs.turn_id" json:"runs,omitempty" sql:"uri=datly_conversation_audit:sql/run.sql"`

	Message []*Message `view:"message,type=Message,table=message" on:"Id:reader.id=TurnId:message.turn_id" json:"message,omitempty" sql:"uri=datly_conversation_audit:sql/message.sql"`
}

type Message struct {
	LinkedConversationId *string    `sqlx:"linked_conversation_id" json:"linkedConversationId"`
	Id                   string     `sqlx:"id,primaryKey=true" json:"id"`
	ConversationId       string     `sqlx:"conversation_id,refTable=conversation,refColumn=id,required=true" json:"conversationId"`
	CreatedAt            time.Time  `sqlx:"created_at,required=true" json:"createdAt"`
	Role                 string     `sqlx:"role,required=true" json:"role"`
	Type                 string     `sqlx:"type,required=true" json:"type"`
	Interim              int        `sqlx:"interim,required=true" json:"interim"`
	Narration            *string    `sqlx:"preamble" json:"narration"`
	TurnId               *string    `sqlx:"turn_id,refTable=turn,refColumn=id" json:"turnId"`
	Archived             *int       `sqlx:"archived" json:"archived"`
	Sequence             *int       `sqlx:"sequence" json:"sequence"`
	UpdatedAt            *time.Time `sqlx:"updated_at" json:"updatedAt"`
	CreatedByUserId      *string    `sqlx:"created_by_user_id" json:"createdByUserId"`
	Status               *string    `sqlx:"status" json:"status"`
	Mode                 *string    `sqlx:"mode" json:"mode"`
	Content              *string    `sqlx:"content" json:"content"`
	RawContent           *string    `sqlx:"raw_content" json:"rawContent"`
	Summary              *string    `sqlx:"summary" json:"summary"`
	ContextSummary       *string    `sqlx:"context_summary" json:"contextSummary"`
	Tags                 *string    `sqlx:"tags" json:"tags"`
	ElicitationId        *string    `sqlx:"elicitation_id" json:"elicitationId"`
	ParentMessageId      *string    `sqlx:"parent_message_id" json:"parentMessageId"`
	SupersededBy         *string    `sqlx:"superseded_by" json:"supersededBy"`
	AttachmentPayloadId  *string    `sqlx:"attachment_payload_id,refTable=call_payload,refColumn=id" json:"attachmentPayloadId"`
	ElicitationPayloadId *string    `sqlx:"elicitation_payload_id,refTable=call_payload,refColumn=id" json:"elicitationPayloadId"`
	ToolName             *string    `sqlx:"tool_name" json:"toolName"`
	EmbeddingIndex       *string    `sqlx:"embedding_index" json:"embeddingIndex"`
	Iteration            *int       `sqlx:"iteration" json:"iteration"`
	Phase                *string    `sqlx:"phase" json:"phase"`
	ModelCall            *ModelCall `view:"modelCall,type=ModelCall,table=model_call" on:"Id:message.id=MessageId:modelCall.message_id" json:"modelCall,omitempty" sql:"uri=datly_conversation_audit:sql/model.sql"`
	MessageToolCall      *ToolCall  `view:"toolCall,type=ToolCall,table=tool_call" on:"Id:message.id=MessageId:toolCall.message_id" json:"messageToolCall,omitempty" sql:"uri=datly_conversation_audit:sql/tool.sql"`
}

type ModelCall struct {
	MessageId                          string     `validate:"required" sqlx:"message_id,primaryKey,refTable=message,refColumn=id" json:"messageId"`
	TurnId                             *string    `sqlx:"turn_id,refTable=turn,refColumn=id" json:"turnId"`
	Provider                           string     `validate:"required" sqlx:"provider,required=true" json:"provider"`
	Model                              string     `validate:"required" sqlx:"model,required=true" json:"model"`
	ModelKind                          string     `validate:"required" sqlx:"model_kind,required=true" json:"modelKind"`
	Status                             string     `validate:"required" sqlx:"status,required=true" json:"status"`
	ErrorCode                          *string    `sqlx:"error_code" json:"errorCode"`
	ErrorMessage                       *string    `sqlx:"error_message" json:"errorMessage"`
	PromptTokens                       *int       `sqlx:"prompt_tokens" json:"promptTokens"`
	PromptCachedTokens                 *int       `sqlx:"prompt_cached_tokens" json:"promptCachedTokens"`
	CompletionTokens                   *int       `sqlx:"completion_tokens" json:"completionTokens"`
	TotalTokens                        *int       `sqlx:"total_tokens" json:"totalTokens"`
	PromptAudioTokens                  *int       `sqlx:"prompt_audio_tokens" json:"promptAudioTokens"`
	CompletionReasoningTokens          *int       `sqlx:"completion_reasoning_tokens" json:"completionReasoningTokens"`
	CompletionAudioTokens              *int       `sqlx:"completion_audio_tokens" json:"completionAudioTokens"`
	CompletionAcceptedPredictionTokens *int       `sqlx:"completion_accepted_prediction_tokens" json:"completionAcceptedPredictionTokens"`
	CompletionRejectedPredictionTokens *int       `sqlx:"completion_rejected_prediction_tokens" json:"completionRejectedPredictionTokens"`
	FinishReason                       *string    `sqlx:"finish_reason" json:"finishReason"`
	StartedAt                          *time.Time `sqlx:"started_at" json:"startedAt"`
	CompletedAt                        *time.Time `sqlx:"completed_at" json:"completedAt"`
	LatencyMs                          *int       `sqlx:"latency_ms" json:"latencyMs"`
	Cost                               *float64   `sqlx:"cost" json:"cost"`
	TraceId                            *string    `sqlx:"trace_id" json:"traceId"`
	SpanId                             *string    `sqlx:"span_id" json:"spanId"`
	RequestPayloadId                   *string    `sqlx:"request_payload_id,refTable=call_payload,refColumn=id" json:"requestPayloadId"`
	ResponsePayloadId                  *string    `sqlx:"response_payload_id,refTable=call_payload,refColumn=id" json:"responsePayloadId"`
	ProviderRequestPayloadId           *string    `sqlx:"provider_request_payload_id,refTable=call_payload,refColumn=id" json:"providerRequestPayloadId"`
	ProviderResponsePayloadId          *string    `sqlx:"provider_response_payload_id,refTable=call_payload,refColumn=id" json:"providerResponsePayloadId"`
	StreamPayloadId                    *string    `sqlx:"stream_payload_id,refTable=call_payload,refColumn=id" json:"streamPayloadId"`
	RunId                              *string    `sqlx:"run_id" json:"runId"`
	Iteration                          *int       `sqlx:"iteration" json:"iteration"`
}

type ToolCall struct {
	MessageId         string     `sqlx:"message_id,refTable=message,refColumn=id,primaryKey=true" json:"messageId"`
	OpId              string     `sqlx:"op_id,required=true" json:"opId"`
	Attempt           int        `sqlx:"attempt,required=true" json:"attempt"`
	ToolName          string     `sqlx:"tool_name,required=true" json:"toolName"`
	ToolKind          string     `sqlx:"tool_kind,required=true" json:"toolKind"`
	Status            string     `sqlx:"status,required=true" json:"status"`
	TurnId            *string    `sqlx:"turn_id,refTable=turn,refColumn=id" json:"turnId"`
	RequestHash       *string    `sqlx:"request_hash" json:"requestHash"`
	ErrorCode         *string    `sqlx:"error_code" json:"errorCode"`
	ErrorMessage      *string    `sqlx:"error_message" json:"errorMessage"`
	Retriable         *int       `sqlx:"retriable" json:"retriable"`
	StartedAt         *time.Time `sqlx:"started_at" json:"startedAt"`
	CompletedAt       *time.Time `sqlx:"completed_at" json:"completedAt"`
	LatencyMs         *int       `sqlx:"latency_ms" json:"latencyMs"`
	Cost              *float64   `sqlx:"cost" json:"cost"`
	TraceId           *string    `sqlx:"trace_id" json:"traceId"`
	SpanId            *string    `sqlx:"span_id" json:"spanId"`
	RequestPayloadId  *string    `sqlx:"request_payload_id,refTable=call_payload,refColumn=id" json:"requestPayloadId"`
	ResponsePayloadId *string    `sqlx:"response_payload_id,refTable=call_payload,refColumn=id" json:"responsePayloadId"`
	RunId             *string    `sqlx:"run_id" json:"runId"`
	Iteration         *int       `sqlx:"iteration" json:"iteration"`
	MessageSequence   *int       `sqlx:"message_sequence" json:"messageSequence"`
}

// Run intentionally excludes security_context, credentials and protocol inputs.
type Run struct {
	ID               string     `sqlx:"id,primaryKey=true" json:"id"`
	TurnID           string     `sqlx:"turn_id" json:"turnId"`
	Attempt          int        `sqlx:"attempt" json:"attempt"`
	ResumedFromRunID *string    `sqlx:"resumed_from_run_id" json:"resumedFromRunId"`
	Status           string     `sqlx:"status" json:"status"`
	EffectiveUserID  *string    `sqlx:"effective_user_id" json:"effectiveUserId"`
	ErrorCode        *string    `sqlx:"error_code" json:"errorCode"`
	ErrorMessage     *string    `sqlx:"error_message" json:"errorMessage"`
	StartedAt        *time.Time `sqlx:"started_at" json:"startedAt"`
	CompletedAt      *time.Time `sqlx:"completed_at" json:"completedAt"`
}
