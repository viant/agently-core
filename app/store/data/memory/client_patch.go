package memory

import (
	convcli "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	msgw "github.com/viant/agently-core/pkg/agently/message/write"
	mcallw "github.com/viant/agently-core/pkg/agently/modelcall/write"
	payloadw "github.com/viant/agently-core/pkg/agently/payload/write"
	toolw "github.com/viant/agently-core/pkg/agently/toolcall/write"
	turnw "github.com/viant/agently-core/pkg/agently/turn/write"
)

func applyConversationPatch(dst *agconv.ConversationView, src *convcli.MutableConversation) {
	if src.Has == nil {
		return
	}
	if src.Has.Summary {
		dst.Summary = src.Summary
	}
	if src.Has.AgentId {
		dst.AgentId = src.AgentId
	}
	if src.Has.ConversationParentId {
		dst.ConversationParentId = src.ConversationParentId
	}
	if src.Has.ConversationParentTurnId {
		dst.ConversationParentTurnId = src.ConversationParentTurnId
	}
	if src.Has.Title {
		dst.Title = src.Title
	}
	if src.Has.Visibility && src.Visibility != nil {
		dst.Visibility = *src.Visibility
	}
	if src.Has.Shareable {
		dst.Shareable = src.Shareable
	}
	if src.Has.CreatedAt && src.CreatedAt != nil {
		dst.CreatedAt = *src.CreatedAt
	}
	if src.Has.UpdatedAt && src.UpdatedAt != nil {
		dst.UpdatedAt = src.UpdatedAt
	}
	if src.Has.LastActivity && src.LastActivity != nil {
		dst.LastActivity = src.LastActivity
	}
	if src.Has.UsageInputTokens {
		dst.UsageInputTokens = src.UsageInputTokens
	}
	if src.Has.UsageOutputTokens {
		dst.UsageOutputTokens = src.UsageOutputTokens
	}
	if src.Has.UsageEmbeddingTokens {
		dst.UsageEmbeddingTokens = src.UsageEmbeddingTokens
	}
	if src.Has.CreatedByUserID {
		dst.CreatedByUserId = src.CreatedByUserID
	}
	if src.Has.DefaultModelProvider {
		dst.DefaultModelProvider = src.DefaultModelProvider
	}
	if src.Has.DefaultModel {
		dst.DefaultModel = src.DefaultModel
	}
	if src.Has.DefaultModelParams {
		dst.DefaultModelParams = src.DefaultModelParams
	}
	if src.Has.Metadata {
		dst.Metadata = src.Metadata
	}
	if src.Has.Status {
		dst.Status = src.Status
	}
	if src.Has.Scheduled {
		dst.Scheduled = src.Scheduled
	}
	if src.Has.ScheduleId {
		dst.ScheduleId = src.ScheduleId
	}
	if src.Has.ScheduleRunId {
		dst.ScheduleRunId = src.ScheduleRunId
	}
	if src.Has.ScheduleKind {
		dst.ScheduleKind = src.ScheduleKind
	}
	if src.Has.ScheduleTimezone {
		dst.ScheduleTimezone = src.ScheduleTimezone
	}
	if src.Has.ScheduleCronExpr {
		dst.ScheduleCronExpr = src.ScheduleCronExpr
	}
	if src.Has.ExternalTaskRef {
		dst.ExternalTaskRef = src.ExternalTaskRef
	}
}

func applyMessagePatch(dst *agconv.MessageView, src *msgw.Message) {
	if src.Has == nil {
		return
	}
	if src.Has.ConversationID {
		dst.ConversationId = src.ConversationID
	}
	if src.Has.TurnID {
		dst.TurnId = src.TurnID
	}
	if src.Has.Sequence {
		dst.Sequence = src.Sequence
	}
	if src.Has.CreatedAt && src.CreatedAt != nil {
		dst.CreatedAt = *src.CreatedAt
	}
	if src.Has.CreatedByUserID {
		dst.CreatedByUserId = src.CreatedByUserID
	}
	if src.Has.Role {
		dst.Role = src.Role
	}
	if src.Has.Status {
		dst.Status = src.Status
	}
	if src.Has.Type {
		dst.Type = src.Type
	}
	if src.Has.Content {
		if src.Content == nil || *src.Content == "" {
			dst.Content = nil
		} else {
			s := *src.Content
			dst.Content = &s
		}
	}
	if src.Has.RawContent {
		if src.RawContent == nil || *src.RawContent == "" {
			dst.RawContent = nil
		} else {
			val := *src.RawContent
			dst.RawContent = &val
		}
	}
	if src.Has.ContextSummary {
		dst.ContextSummary = src.ContextSummary
	}
	if src.Has.Preamble {
		dst.Preamble = src.Preamble
	}
	if src.Has.Tags {
		dst.Tags = src.Tags
	}
	if src.Has.Interim {
		if src.Interim != nil {
			dst.Interim = *src.Interim
		}
	}
	if src.Has.ElicitationID {
		dst.ElicitationId = src.ElicitationID
	}
	if src.Has.ParentMessageID {
		dst.ParentMessageId = src.ParentMessageID
	}
	if src.Has.LinkedConversationID {
		dst.LinkedConversationId = src.LinkedConversationID
	}
	if src.Has.SupersededBy {
		dst.SupersededBy = src.SupersededBy
	}
	if src.Has.ToolName {
		dst.ToolName = src.ToolName
	}
	if src.Has.AttachmentPayloadID {
		dst.AttachmentPayloadId = src.AttachmentPayloadID
	}
	if src.Has.ElicitationPayloadID {
		dst.ElicitationPayloadId = src.ElicitationPayloadID
	}
}

func applyModelCallPatch(dst *agconv.ModelCallView, src *mcallw.ModelCall) {
	if src.Has == nil {
		return
	}
	if src.Has.TurnID {
		dst.TurnId = src.TurnID
	}
	if src.Has.Provider {
		dst.Provider = src.Provider
	}
	if src.Has.Model {
		dst.Model = src.Model
	}
	if src.Has.ModelKind {
		dst.ModelKind = src.ModelKind
	}
	if src.Has.Status {
		dst.Status = src.Status
	}
	if src.Has.ErrorCode {
		dst.ErrorCode = src.ErrorCode
	}
	if src.Has.ErrorMessage {
		dst.ErrorMessage = src.ErrorMessage
	}
	if src.Has.PromptTokens {
		dst.PromptTokens = src.PromptTokens
	}
	if src.Has.PromptCachedTokens {
		dst.PromptCachedTokens = src.PromptCachedTokens
	}
	if src.Has.CompletionTokens {
		dst.CompletionTokens = src.CompletionTokens
	}
	if src.Has.TotalTokens {
		dst.TotalTokens = src.TotalTokens
	}
	if src.Has.StartedAt {
		dst.StartedAt = src.StartedAt
	}
	if src.Has.CompletedAt {
		dst.CompletedAt = src.CompletedAt
	}
	if src.Has.LatencyMS {
		dst.LatencyMs = src.LatencyMS
	}
	if src.Has.Cost {
		dst.Cost = src.Cost
	}
	if src.Has.TraceID {
		dst.TraceId = src.TraceID
	}
	if src.Has.SpanID {
		dst.SpanId = src.SpanID
	}
	if src.Has.RequestPayloadID {
		dst.RequestPayloadId = src.RequestPayloadID
	}
	if src.Has.ResponsePayloadID {
		dst.ResponsePayloadId = src.ResponsePayloadID
	}
	if src.Has.ProviderRequestPayloadID {
		dst.ProviderRequestPayloadId = src.ProviderRequestPayloadID
	}
	if src.Has.ProviderResponsePayloadID {
		dst.ProviderResponsePayloadId = src.ProviderResponsePayloadID
	}
	if src.Has.StreamPayloadID {
		dst.StreamPayloadId = src.StreamPayloadID
	}
}

func applyToolCallPatch(dst *agconv.ToolCallView, src *toolw.ToolCall) {
	if src.Has == nil {
		return
	}
	if src.Has.TurnID {
		dst.TurnId = src.TurnID
	}
	if src.Has.OpID {
		dst.OpId = src.OpID
	}
	if src.Has.Attempt {
		dst.Attempt = src.Attempt
	}
	if src.Has.ToolName {
		dst.ToolName = src.ToolName
	}
	if src.Has.ToolKind {
		dst.ToolKind = src.ToolKind
	}
	if src.Has.Status {
		dst.Status = src.Status
	}
	if src.Has.RequestHash {
		dst.RequestHash = src.RequestHash
	}
	if src.Has.ErrorCode {
		dst.ErrorCode = src.ErrorCode
	}
	if src.Has.ErrorMessage {
		dst.ErrorMessage = src.ErrorMessage
	}
	if src.Has.Retriable {
		dst.Retriable = src.Retriable
	}
	if src.Has.StartedAt {
		dst.StartedAt = src.StartedAt
	}
	if src.Has.CompletedAt {
		dst.CompletedAt = src.CompletedAt
	}
	if src.Has.LatencyMS {
		dst.LatencyMs = src.LatencyMS
	}
	if src.Has.Cost {
		dst.Cost = src.Cost
	}
	if src.Has.TraceID {
		dst.TraceId = src.TraceID
	}
	if src.Has.SpanID {
		dst.SpanId = src.SpanID
	}
	if src.Has.RequestPayloadID {
		dst.RequestPayloadId = src.RequestPayloadID
	}
	if src.Has.ResponsePayloadID {
		dst.ResponsePayloadId = src.ResponsePayloadID
	}
}

func applyTurnPatch(dst *agconv.TranscriptView, src *turnw.Turn) {
	if src.Has == nil {
		return
	}
	if src.Has.ConversationID {
		dst.ConversationId = src.ConversationID
	}
	if src.Has.CreatedAt && src.CreatedAt != nil {
		dst.CreatedAt = *src.CreatedAt
	}
	if src.Has.Status {
		dst.Status = src.Status
	}
	if src.Has.StartedByMessageID {
		dst.StartedByMessageId = src.StartedByMessageID
	}
	if src.Has.RetryOf {
		dst.RetryOf = src.RetryOf
	}
	if src.Has.AgentIDUsed {
		dst.AgentIdUsed = src.AgentIDUsed
	}
	if src.Has.AgentConfigUsedID {
		dst.AgentConfigUsedId = src.AgentConfigUsedID
	}
	if src.Has.ModelOverrideProvider {
		dst.ModelOverrideProvider = src.ModelOverrideProvider
	}
	if src.Has.ModelOverride {
		dst.ModelOverride = src.ModelOverride
	}
	if src.Has.ModelParamsOverride {
		dst.ModelParamsOverride = src.ModelParamsOverride
	}
}

func applyPayloadPatch(dst *convcli.Payload, src *payloadw.Payload) {
	if src.Has == nil {
		return
	}
	if src.Has.TenantID {
		dst.TenantID = src.TenantID
	}
	if src.Has.Kind {
		dst.Kind = src.Kind
	}
	if src.Has.Subtype {
		dst.Subtype = src.Subtype
	}
	if src.Has.MimeType {
		dst.MimeType = src.MimeType
	}
	if src.Has.SizeBytes {
		dst.SizeBytes = src.SizeBytes
	}
	if src.Has.Digest {
		dst.Digest = src.Digest
	}
	if src.Has.Storage {
		dst.Storage = src.Storage
	}
	if src.Has.InlineBody {
		dst.InlineBody = (*[]byte)(src.InlineBody)
	}
	if src.Has.URI {
		dst.URI = src.URI
	}
	if src.Has.Compression {
		dst.Compression = src.Compression
	}
	if src.Has.EncryptionKMSKeyID {
		dst.EncryptionKMSKeyID = src.EncryptionKMSKeyID
	}
	if src.Has.RedactionPolicyVersion {
		dst.RedactionPolicyVersion = src.RedactionPolicyVersion
	}
	if src.Has.Redacted {
		dst.Redacted = src.Redacted
	}
	if src.Has.CreatedAt {
		dst.CreatedAt = src.CreatedAt
	}
	if src.Has.SchemaRef {
		dst.SchemaRef = src.SchemaRef
	}
}
