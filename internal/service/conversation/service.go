package conversation

import (
	"context"
	"errors"
	"reflect"

	"strings"
	"time"

	convcli "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/internal/logx"
	convstore "github.com/viant/agently-core/internal/store/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	generatedfilemodel "github.com/viant/agently-core/model/generatedfile"
	toolapprovalqueuemodel "github.com/viant/agently-core/model/toolapprovalqueue"
	toolcallmodel "github.com/viant/agently-core/model/toolcall"
	mcpname2 "github.com/viant/agently-core/protocol/mcpname"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	toolexec "github.com/viant/agently-core/service/shared/toolexec"
	dexec "github.com/viant/datly/exec"

	hstate "github.com/viant/xdatly/state"
)

type Service struct {
	native    dexec.ComponentInvoker
	streamPub streaming.Publisher
}

func (s *Service) SetStreamPublisher(p streaming.Publisher) {
	if s == nil {
		return
	}
	s.streamPub = p
}

// New binds the conversation API to the application shared Datly runtime.
func New(ctx context.Context, invoker dexec.ComponentInvoker) (*Service, error) {
	if ctx == nil {
		return nil, errors.New("conversation context is required")
	}
	if invoker == nil {
		return nil, errors.New("conversation component invoker is required")
	}
	value := reflect.ValueOf(invoker)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return nil, errors.New("conversation component invoker is required")
	}
	return &Service{native: invoker}, nil
}

func (s *Service) PatchConversations(ctx context.Context, conversations *convcli.MutableConversation) error {
	if conversations != nil {
		logx.Infof("conversation", "PatchConversations start id=%q status=%q visibility=%q metadata_set=%t metadata_len=%d", strings.TrimSpace(conversations.Id), strings.TrimSpace(valueOrEmptyStr(conversations.Status)), strings.TrimSpace(valueOrEmptyStr(conversations.Visibility)), conversations.Has != nil && conversations.Has.Metadata, len(strings.TrimSpace(valueOrEmptyStr(conversations.Metadata))))
	} else {
		logx.Infof("conversation", "PatchConversations start id=\"\" status=\"\" visibility=\"\" (nil input)")
	}

	if err := s.patchConversationNative(ctx, conversations); err != nil {
		return err
	}
	s.publishConversationMetaEvent(ctx, conversations)
	s.publishConversationUsageEvent(ctx, conversations)
	return nil
}

// GetConversations implements conversation.API using the generated component and returns SDK Conversation.
func (s *Service) GetConversations(ctx context.Context, input *convcli.Input) ([]*convcli.Conversation, error) {
	return s.getConversationsNative(ctx, input)
}

// GetConversation implements conversation.API using the generated component and returns SDK Conversation.
func (s *Service) GetConversation(ctx context.Context, id string, options ...convcli.Option) (*convcli.Conversation, error) {
	return s.getConversationNative(ctx, id, options...)
}

func pruneBlankAssistantPlaceholders(turns []*conversationmodel.TranscriptView) {
	for _, turn := range turns {
		if turn == nil || len(turn.Message) == 0 {
			continue
		}
		filtered := turn.Message[:0]
		for _, msg := range turn.Message {
			if shouldDropBlankAssistantPlaceholder(msg) {
				continue
			}
			filtered = append(filtered, msg)
		}
		turn.Message = filtered
	}
}

func shouldDropBlankAssistantPlaceholder(msg *conversationmodel.MessageView) bool {
	if msg == nil {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
		return false
	}
	if msg.Interim != 1 {
		return false
	}
	if valueOrEmptyStr(msg.Content) != "" || valueOrEmptyStr(msg.RawContent) != "" || valueOrEmptyStr(msg.Narration) != "" {
		return false
	}
	if msg.ElicitationId != nil && strings.TrimSpace(*msg.ElicitationId) != "" {
		return false
	}
	return !(msg.ModelCall != nil || len(msg.ToolMessage) > 0)
}

func (s *Service) GetPayload(ctx context.Context, id string) (*convcli.Payload, error) {
	return s.getPayloadNative(ctx, id)
}

func (s *Service) PatchPayload(ctx context.Context, payload *convcli.MutablePayload) error {
	if s == nil || s.native == nil {
		return errors.New("conversation service not configured")
	}
	if payload == nil {
		return errors.New("invalid payload: nil")
	}
	logPayloadDebug := payload.SizeBytes%512 == 0
	if logPayloadDebug {
		logx.Infof("conversation", "PatchPayload start id=%q kind=%q mime=%q size_bytes=%d", strings.TrimSpace(payload.Id), strings.TrimSpace(payload.Kind), strings.TrimSpace(payload.MimeType), payload.SizeBytes)
	}

	if err := s.patchPayloadNative(ctx, payload); err != nil {
		return err
	}
	return nil
}

func (s *Service) GetGeneratedFiles(ctx context.Context, input *generatedfilemodel.Input) ([]*generatedfilemodel.GeneratedFileView, error) {
	return s.getGeneratedFilesNative(ctx, input)
}

func (s *Service) PatchGeneratedFile(ctx context.Context, generatedFile *generatedfilemodel.GeneratedFile) error {
	if s == nil || s.native == nil {
		return errors.New("conversation service not configured")
	}
	if generatedFile == nil {
		return errors.New("invalid generated file: nil")
	}
	logx.Infof("conversation", "PatchGeneratedFile start id=%q provider=%q mode=%q status=%q", strings.TrimSpace(generatedFile.ID), strings.TrimSpace(generatedFile.Provider), strings.TrimSpace(generatedFile.Mode), strings.TrimSpace(generatedFile.Status))

	return s.patchGeneratedFileNative(ctx, generatedFile)

}

func (s *Service) GetMessage(ctx context.Context, id string, options ...convcli.Option) (*convcli.Message, error) {
	return s.getMessageNative(ctx, id, options...)
}

func (s *Service) GetMessageByElicitation(ctx context.Context, conversationID, elicitationID string) (*convcli.Message, error) {
	return s.getMessageByElicitationNative(ctx, conversationID, elicitationID)
}

func (s *Service) GetMessageByParentAndElicitation(ctx context.Context, parentMessageID, elicitationID string) (*convcli.Message, error) {
	return s.getMessageByParentElicitationNative(ctx, parentMessageID, elicitationID)
}

func (s *Service) GetMessageByLinkedConversationAndElicitation(ctx context.Context, linkedConversationID, elicitationID string) (*convcli.Message, error) {
	return s.getMessageByLinkedElicitationNative(ctx, linkedConversationID, elicitationID)
}

func (s *Service) PatchMessage(ctx context.Context, message *convcli.MutableMessage) error {
	if message != nil {
		logx.Infof("conversation", "PatchMessage start id=%q convo=%q turn=%v role=%q type=%q status=%q", message.Id, message.ConversationID, valueOrEmpty(message.TurnID), strings.TrimSpace(message.Role), strings.TrimSpace(message.Type), strings.TrimSpace(valueOrEmptyStr(message.Status)))
	} else {
		logx.Infof("conversation", "PatchMessage start id=\"\" convo=\"\" turn=\"\" role=\"\" type=\"\" status=\"\" (nil input)")
	}

	if err := s.patchMessageNative(ctx, message); err != nil {
		return err
	}
	s.publishMessagePatchEvent(ctx, message)
	return nil
}

// publishMessagePatchEvent forwards a message write to the streaming
// bus as SEMANTIC events. Never emits raw DB column diffs.
//
// There are EXACTLY TWO semantic emissions from this path:
//
//  1. `narration` — when the message carries interim narration text
//     (commentary during tool execution / pre-tool-call framing).
//     Carries the text in `event.Narration`. Interim — not a real
//     turn message.
//
//  2. `assistant` (wire name) — when this write is an explicit ADD
//     (`runtimerequestctx.MessageAddEvent` flag on ctx), fires to
//     signal "a new standalone message exists in the turn". Reducers
//     upsert into `turn.messages` / `turn.users` by messageId, creating
//     the bubble. Patches to EXISTING message rows do NOT emit here —
//     content for page-owned messages is already live on the client
//     via `text_delta` stream accumulation into `page.content`; a
//     redundant patch event would cause double-rendering.
//
// **There is no "final message" event.** Historically we had
// per-message final markers; these kept
// leaking the end-of-turn signal into individual messages and caused
// repeated regressions ("which message is the final?", "why are there
// two final messages?", "why doesn't this message render?"). The
// end-of-turn signal lives exclusively on `EventTypeTurnCompleted` /
// `EventTypeTurnFailed` / `EventTypeTurnCanceled` — fired ONCE per
// turn. A turn can have any number of assistant messages; none of them
// is "the final", they're just messages.
//
// Active-turn invariant: all client state flows from these semantic
// events. Past turns refresh via `applyTranscript`, which populates
// the same fields from the canonical snapshot.
func (s *Service) publishMessagePatchEvent(ctx context.Context, message *convcli.MutableMessage) {
	if s == nil || s.streamPub == nil || message == nil {
		return
	}
	conversationID := strings.TrimSpace(message.ConversationID)
	if conversationID == "" {
		conversationID = strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	}
	if conversationID == "" {
		return
	}
	s.emitCanonicalAssistantEvents(ctx, message, conversationID)
	// Emit `assistant` event ONLY for explicit adds (AddMessage code
	// path sets the MessageAddEvent ctx flag). Patches to existing
	// rows don't fire — the UI already has the content live from
	// text_delta for page-owned messages, and re-emitting would
	// double-render. message/add tool, user-submit, and similar
	// "new message row" call sites DO set the flag and therefore
	// produce a bubble.
	if runtimerequestctx.MessageAddEventFromContext(ctx) {
		s.emitMessageAppendedEvent(ctx, message, conversationID)
	}
}

// emitMessageAppendedEvent publishes a `message_appended` event for a
// user or assistant message row. Idempotent by messageId: the first
// emission creates the client-side bubble, subsequent emissions update
// its fields. Works for ADD (new row) and PATCH (content/status
// update) alike — clients upsert by messageId.
//
// Interim messages (interim=1) are NOT emitted here — they're carried
// as `narration` events instead (interim commentary, not a real turn
// message). This function emits ONLY real messages that belong in
// `turn.messages` / `turn.users`.
func (s *Service) emitMessageAppendedEvent(ctx context.Context, message *convcli.MutableMessage, conversationID string) {
	if s == nil || s.streamPub == nil || message == nil || message.Has == nil {
		return
	}
	role := strings.ToLower(strings.TrimSpace(message.Role))
	if role != "user" && role != "assistant" {
		return
	}
	// Skip interim messages — narration path handles those.
	if message.Has.Interim && message.Interim != nil && *message.Interim == 1 {
		return
	}
	content := ""
	if message.Has.Content && message.Content != nil {
		content = strings.TrimSpace(*message.Content)
	}
	// Require content — an empty add/patch carries no bubble-worthy
	// information; skip it to keep the stream clean.
	if content == "" {
		return
	}
	turnID := ""
	if message.Has.TurnID && message.TurnID != nil {
		turnID = strings.TrimSpace(*message.TurnID)
	}
	if turnID == "" {
		if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
			turnID = strings.TrimSpace(turn.TurnID)
		}
	}
	event := &streaming.Event{
		ID:             strings.TrimSpace(message.Id),
		StreamID:       conversationID,
		ConversationID: conversationID,
		TurnID:         turnID,
		MessageID:      strings.TrimSpace(message.Id),
		Type:           streaming.EventTypeAssistant,
		Mode:           firstNonEmpty(strings.TrimSpace(valueOrEmptyStr(message.Mode)), requestModeForEvent(ctx)),
		Content:        content,
		CreatedAt:      patchEventCreatedAt(message),
	}
	// Carry role, sequence, status via the Patch map so the reducer
	// receives them without widening the Event struct with fields only
	// meaningful for this event type.
	patch := map[string]interface{}{"role": role}
	if message.Has.Sequence && message.Sequence != nil {
		patch["sequence"] = *message.Sequence
	}
	if message.Has.Status && message.Status != nil {
		patch["status"] = strings.TrimSpace(*message.Status)
	}
	event.Patch = patch
	applyIterationPage(event, message.Iteration)
	s.emitTimelineEvent(ctx, event, "PatchMessage publish message_appended")
}

// publishConversationMetaEvent emits a conversation_meta_updated event when
// conversation-level metadata (title, agentId, summary, …) changes.
// Only fields that were explicitly set in the patch are included in the payload.
func (s *Service) publishConversationMetaEvent(ctx context.Context, conv *convcli.MutableConversation) {
	if s == nil || s.streamPub == nil || conv == nil || conv.Has == nil {
		return
	}
	convID := strings.TrimSpace(conv.Id)
	if convID == "" {
		return
	}
	patch := conversationMetaPatch(conv)
	if len(patch) == 0 {
		return
	}
	event := &streaming.Event{
		StreamID:       convID,
		ConversationID: convID,
		Type:           streaming.EventTypeConversationMetaUpdated,
		Patch:          patch,
	}
	s.emitTimelineEvent(ctx, event, "PatchConversations publish meta event")
}

// conversationMetaPatch builds the patch payload from the fields that were
// explicitly set on the MutableConversation.
func conversationMetaPatch(conv *convcli.MutableConversation) map[string]interface{} {
	if conv == nil || conv.Has == nil {
		return nil
	}
	out := map[string]interface{}{}
	if conv.Has.Title && conv.Title != nil {
		out["title"] = strings.TrimSpace(*conv.Title)
	}
	if conv.Has.AgentId && conv.AgentId != nil {
		out["agentId"] = strings.TrimSpace(*conv.AgentId)
	}
	if conv.Has.Summary && conv.Summary != nil {
		out["summary"] = strings.TrimSpace(*conv.Summary)
	}
	if conv.Has.Status && conv.Status != nil {
		out["status"] = strings.TrimSpace(*conv.Status)
	}
	return out
}

func (s *Service) publishConversationUsageEvent(ctx context.Context, conv *convcli.MutableConversation) {
	if s == nil || s.streamPub == nil || conv == nil || conv.Has == nil {
		return
	}
	convID := strings.TrimSpace(conv.Id)
	if convID == "" {
		return
	}
	hasUsage := conv.Has.UsageInputTokens || conv.Has.UsageOutputTokens || conv.Has.UsageEmbeddingTokens
	if !hasUsage {
		return
	}
	ev := &streaming.Event{
		StreamID:             convID,
		ConversationID:       convID,
		Type:                 streaming.EventTypeUsage,
		UsageInputTokens:     intValuePtr(conv.UsageInputTokens),
		UsageOutputTokens:    intValuePtr(conv.UsageOutputTokens),
		UsageEmbeddingTokens: intValuePtr(conv.UsageEmbeddingTokens),
	}
	ev.UsageTotalTokens = ev.UsageInputTokens + ev.UsageOutputTokens + ev.UsageEmbeddingTokens
	s.emitTimelineEvent(ctx, ev, "PatchConversations publish usage event")
}

func intValuePtr(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func isToolMessage(message *convcli.MutableMessage) bool {
	if message == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(message.Role), "tool") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(message.Type), "tool_op")
}

func isToolStatusMessage(message *convcli.MutableMessage) bool {
	if message == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(message.Role), "assistant") {
		return false
	}
	if message.CreatedByUserID == nil || !strings.EqualFold(strings.TrimSpace(*message.CreatedByUserID), "tool") {
		return false
	}
	if message.Mode == nil || !strings.EqualFold(strings.TrimSpace(*message.Mode), "exec") {
		return false
	}
	return message.ToolName != nil && strings.TrimSpace(*message.ToolName) != ""
}

func messagePatchPayload(message *convcli.MutableMessage) map[string]interface{} {
	if message == nil || message.Has == nil {
		return nil
	}
	out := map[string]interface{}{}
	if message.Has.LinkedConversationID && message.LinkedConversationID != nil {
		out["linkedConversationId"] = strings.TrimSpace(*message.LinkedConversationID)
	}
	if message.Has.ParentMessageID && message.ParentMessageID != nil {
		out["parentMessageId"] = strings.TrimSpace(*message.ParentMessageID)
	}
	if message.Has.TurnID && message.TurnID != nil {
		out["turnId"] = strings.TrimSpace(*message.TurnID)
	}
	if message.Has.Phase && message.Phase != nil {
		out["phase"] = strings.TrimSpace(*message.Phase)
	}
	if message.Has.Status && message.Status != nil {
		out["status"] = strings.TrimSpace(*message.Status)
	}
	if message.Has.ToolName && message.ToolName != nil {
		out["toolName"] = mcpname2.Display(strings.TrimSpace(*message.ToolName))
	}
	if message.Has.Interim && message.Interim != nil {
		out["interim"] = *message.Interim
	}
	if message.Has.Narration && message.Narration != nil {
		out["narration"] = strings.TrimSpace(*message.Narration)
	}
	if message.Has.Content && message.Content != nil {
		out["content"] = *message.Content
	}
	if message.Has.RawContent && message.RawContent != nil && strings.TrimSpace(*message.RawContent) != "" {
		out["rawContent"] = *message.RawContent
	}
	if message.Has.Role && strings.TrimSpace(message.Role) != "" {
		out["role"] = strings.TrimSpace(message.Role)
	}
	if message.Has.Mode && strings.TrimSpace(valueOrEmptyStr(message.Mode)) != "" {
		out["mode"] = strings.TrimSpace(valueOrEmptyStr(message.Mode))
	}
	if message.Has.Type && strings.TrimSpace(message.Type) != "" {
		out["messageType"] = strings.TrimSpace(message.Type)
	}
	if message.Has.CreatedAt && message.CreatedAt != nil && !message.CreatedAt.IsZero() {
		out["createdAt"] = message.CreatedAt.Format(time.RFC3339Nano)
	}
	if message.Has.Sequence && message.Sequence != nil {
		out["sequence"] = *message.Sequence
	}
	if message.Has.Iteration && message.Iteration != nil {
		out["iteration"] = *message.Iteration
	}
	return out
}

func patchEventCreatedAt(message *convcli.MutableMessage) time.Time {
	if message != nil && message.Has != nil && message.Has.CreatedAt && message.CreatedAt != nil && !message.CreatedAt.IsZero() {
		return *message.CreatedAt
	}
	return time.Now()
}

func turnEventCreatedAt(turn *convcli.MutableTurn) time.Time {
	if turn != nil && turn.Has != nil && turn.Has.CreatedAt && turn.CreatedAt != nil && !turn.CreatedAt.IsZero() {
		return *turn.CreatedAt
	}
	return time.Now()
}

func applyIterationPage(event *streaming.Event, iteration *int) {
	if event == nil || iteration == nil || *iteration <= 0 {
		return
	}
	event.Iteration = *iteration
	event.PageIndex = *iteration
	event.PageCount = *iteration
	event.LatestPage = true
}

func modelEventPhase(mode string, iteration *int) string {
	normalizedMode := strings.ToLower(strings.TrimSpace(mode))
	switch normalizedMode {
	case "router":
		return "intake"
	case "summary":
		return "summary"
	}
	return ""
}

func requestModeForEvent(ctx context.Context) string {
	mode := strings.TrimSpace(runtimerequestctx.RequestModeFromContext(ctx))
	if mode != "" {
		return mode
	}
	if toolexec.IsChainMode(ctx) {
		return "chain"
	}
	return "task"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func timelineDebugFields(event *streaming.Event) map[string]any {
	if event == nil {
		return nil
	}
	fields := map[string]any{
		"type":                      string(event.Type),
		"op":                        strings.TrimSpace(event.Op),
		"streamID":                  strings.TrimSpace(event.StreamID),
		"conversationID":            strings.TrimSpace(event.ConversationID),
		"turnID":                    strings.TrimSpace(event.TurnID),
		"messageID":                 strings.TrimSpace(event.MessageID),
		"eventSeq":                  event.EventSeq,
		"agentIDUsed":               strings.TrimSpace(event.AgentIDUsed),
		"agentName":                 strings.TrimSpace(event.AgentName),
		"assistantID":               strings.TrimSpace(event.AssistantMessageID),
		"parentID":                  strings.TrimSpace(event.ParentMessageID),
		"userMessageID":             strings.TrimSpace(event.UserMessageID),
		"modelCallID":               strings.TrimSpace(event.ModelCallID),
		"requestID":                 strings.TrimSpace(event.RequestID),
		"responseID":                strings.TrimSpace(event.ResponseID),
		"toolCallID":                strings.TrimSpace(event.ToolCallID),
		"toolMessageID":             strings.TrimSpace(event.ToolMessageID),
		"requestPayloadID":          strings.TrimSpace(event.RequestPayloadID),
		"responsePayloadID":         strings.TrimSpace(event.ResponsePayloadID),
		"providerRequestPayloadID":  strings.TrimSpace(event.ProviderRequestPayloadID),
		"providerResponsePayloadID": strings.TrimSpace(event.ProviderResponsePayloadID),
		"streamPayloadID":           strings.TrimSpace(event.StreamPayloadID),
		"linkedConversationID":      strings.TrimSpace(event.LinkedConversationID),
		"mode":                      strings.TrimSpace(event.Mode),
		"phase":                     strings.TrimSpace(event.Phase),
		"status":                    strings.TrimSpace(event.Status),
		"iteration":                 event.Iteration,
		"pageIndex":                 event.PageIndex,
		"pageCount":                 event.PageCount,
		"latestPage":                event.LatestPage,
		"finalResponse":             event.FinalResponse,
		"toolName":                  strings.TrimSpace(event.ToolName),
		"provider":                  strings.TrimSpace(event.Provider),
		"modelName":                 strings.TrimSpace(event.ModelName),
		"feedID":                    strings.TrimSpace(event.FeedID),
		"createdAt":                 event.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if event.StartedAt != nil && !event.StartedAt.IsZero() {
		fields["startedAt"] = event.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if event.CompletedAt != nil && !event.CompletedAt.IsZero() {
		fields["completedAt"] = event.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	if strings.TrimSpace(event.Content) != "" {
		fields["contentPreview"] = event.Content
	}
	if strings.TrimSpace(event.Narration) != "" {
		fields["narrationPreview"] = event.Narration
	}
	if event.Model != nil {
		fields["model"] = map[string]any{
			"provider": strings.TrimSpace(event.Model.Provider),
			"model":    strings.TrimSpace(event.Model.Model),
			"kind":     strings.TrimSpace(event.Model.Kind),
		}
	}
	if len(event.ToolCallsPlanned) > 0 {
		fields["toolCallsPlanned"] = event.ToolCallsPlanned
	}
	return fields
}

func (s *Service) emitTimelineEvent(ctx context.Context, event *streaming.Event, action string) {
	if s == nil || s.streamPub == nil || event == nil {
		return
	}
	fallbackConversationID := ""
	fallbackTurnID := ""
	if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		fallbackConversationID = strings.TrimSpace(turn.ConversationID)
		fallbackTurnID = strings.TrimSpace(turn.TurnID)
	}
	event.NormalizeIdentity(fallbackConversationID, fallbackTurnID)
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	startedAt := ""
	if event.StartedAt != nil && !event.StartedAt.IsZero() {
		startedAt = event.StartedAt.Format(time.RFC3339Nano)
	}
	completedAt := ""
	if event.CompletedAt != nil && !event.CompletedAt.IsZero() {
		completedAt = event.CompletedAt.Format(time.RFC3339Nano)
	}
	logx.DebugCtxf(ctx, "conversation", "[emitTimelineEvent] %s type=%q op=%q stream_id=%q convo=%q turn=%q msg=%q seq=%d mode=%q agent=%q agent_name=%q user_msg=%q assistant_msg=%q parent_msg=%q model_call=%q tool_call=%q tool_msg=%q tool=%q status=%q final=%v iter=%d page=%d/%d latest=%v linked=%q feed=%q created_at=%q started_at=%q completed_at=%q sent_at=%q req=%q resp=%q preq=%q presp=%q stream=%q id=%q",
		action,
		string(event.Type),
		event.Op,
		event.StreamID,
		event.ConversationID,
		event.TurnID,
		event.MessageID,
		event.EventSeq,
		event.Mode,
		event.AgentIDUsed,
		event.AgentName,
		event.UserMessageID,
		event.AssistantMessageID,
		event.ParentMessageID,
		event.ModelCallID,
		event.ToolCallID,
		event.ToolMessageID,
		event.ToolName,
		event.Status,
		event.FinalResponse,
		event.Iteration,
		event.PageIndex,
		event.PageCount,
		event.LatestPage,
		event.LinkedConversationID,
		event.FeedID,
		event.CreatedAt.Format(time.RFC3339Nano),
		startedAt,
		completedAt,
		time.Now().Format(time.RFC3339Nano),
		event.RequestPayloadID,
		event.ResponsePayloadID,
		event.ProviderRequestPayloadID,
		event.ProviderResponsePayloadID,
		event.StreamPayloadID,
		event.ID,
	)
	if err := s.streamPub.Publish(ctx, event); err != nil {
		logx.Warnf("conversation", "%s error type=%q id=%q convo=%q err=%v", action, strings.TrimSpace(string(event.Type)), strings.TrimSpace(event.ID), strings.TrimSpace(event.ConversationID), err)
		return
	}
	if debugtrace.Enabled() {
		debugtrace.Write("conversation", "timeline", timelineDebugFields(event))
	}
}

func toolCallEvent(ctx context.Context, toolCall *convcli.MutableToolCall) *streaming.Event {
	if toolCall == nil {
		return nil
	}
	turn, _ := runtimerequestctx.TurnMetaFromContext(ctx)
	conversationID := strings.TrimSpace(turn.ConversationID)
	if conversationID == "" {
		conversationID = strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	}
	if conversationID == "" {
		return nil
	}
	status := strings.ToLower(strings.TrimSpace(toolCall.Status))
	if status == "" {
		return nil
	}
	eventType := streaming.EventTypeToolCallStarted
	if status != "running" && status != "thinking" && status != "waiting_for_user" {
		eventType = streaming.EventTypeToolCallCompleted
	}
	event := &streaming.Event{
		ID:                 strings.TrimSpace(toolCall.MessageID),
		StreamID:           conversationID,
		ConversationID:     conversationID,
		MessageID:          strings.TrimSpace(toolCall.MessageID),
		Mode:               requestModeForEvent(ctx),
		Type:               eventType,
		TurnID:             resolveTurnID(ctx, valueOrEmptyStr(toolCall.TurnID)),
		AssistantMessageID: strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx)),
		ParentMessageID:    strings.TrimSpace(turn.ParentMessageID),
		ToolCallID:         strings.TrimSpace(toolCall.OpID),
		ToolMessageID:      strings.TrimSpace(toolCall.MessageID),
		RequestID:          strings.TrimSpace(valueOrEmptyStr(toolCall.TraceID)),
		ResponseID:         strings.TrimSpace(valueOrEmptyStr(toolCall.TraceID)),
		RequestPayloadID:   strings.TrimSpace(valueOrEmptyStr(toolCall.RequestPayloadID)),
		ResponsePayloadID:  strings.TrimSpace(valueOrEmptyStr(toolCall.ResponsePayloadID)),
		ToolName:           mcpname2.Display(strings.TrimSpace(toolCall.ToolName)),
		Status:             strings.TrimSpace(toolCall.Status),
		Phase:              modelEventPhase(requestModeForEvent(ctx), toolCall.Iteration),
		CreatedAt:          time.Now(),
	}
	if event.AssistantMessageID == "" {
		event.AssistantMessageID = strings.TrimSpace(turn.ParentMessageID)
	}
	if event.PageID == "" {
		event.PageID = strings.TrimSpace(event.AssistantMessageID)
	}
	if event.PageID == "" {
		event.PageID = strings.TrimSpace(event.ParentMessageID)
	}
	if toolCall.Has != nil {
		if toolCall.Has.StartedAt && toolCall.StartedAt != nil && !toolCall.StartedAt.IsZero() {
			event.StartedAt = toolCall.StartedAt
			event.CreatedAt = *toolCall.StartedAt
		}
		if toolCall.Has.CompletedAt && toolCall.CompletedAt != nil && !toolCall.CompletedAt.IsZero() {
			event.CompletedAt = toolCall.CompletedAt
			if eventType == streaming.EventTypeToolCallCompleted {
				event.CreatedAt = *toolCall.CompletedAt
			}
		}
		applyIterationPage(event, toolCall.Iteration)
	}
	return event
}

// valueOrEmpty renders pointer values without exposing nil dereference in logs.
func valueOrEmpty[T any](p *T) interface{} {
	if p == nil {
		return ""
	}
	return *p
}

func valueOrEmptyStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (s *Service) PatchModelCall(ctx context.Context, modelCall *convcli.MutableModelCall) error {
	if s == nil || s.native == nil {
		return errors.New("conversation service not configured")
	}
	if modelCall == nil {
		return errors.New("invalid modelCall: nil")
	}
	logx.Infof("conversation", "PatchModelCall start message_id=%q turn_id=%q provider=%q model=%q status=%q", strings.TrimSpace(modelCall.MessageID), strings.TrimSpace(valueOrEmptyStr(modelCall.TurnID)), strings.TrimSpace(modelCall.Provider), strings.TrimSpace(modelCall.Model), strings.TrimSpace(modelCall.Status))

	if err := s.patchModelCallNative(ctx, modelCall); err != nil {
		return err
	}
	s.emitCanonicalModelEvent(ctx, modelCall)
	return nil
}

func (s *Service) PatchToolCall(ctx context.Context, toolCall *convcli.MutableToolCall) error {
	if s == nil || s.native == nil {
		return errors.New("conversation service not configured")
	}
	if toolCall == nil {
		return errors.New("invalid toolCall: nil")
	}
	sanitizeMutableToolCallErrorMessage(toolCall)
	logx.Infof("conversation", "PatchToolCall start message_id=%q op_id=%q tool=%q status=%q", strings.TrimSpace(toolCall.MessageID), strings.TrimSpace(toolCall.OpID), strings.TrimSpace(toolCall.ToolName), strings.TrimSpace(toolCall.Status))

	if err := s.patchToolCallNative(ctx, toolCall); err != nil {
		return err
	}
	if event := toolCallEvent(ctx, toolCall); event != nil {
		s.emitTimelineEvent(ctx, event, "PatchToolCall publish timeline event")
	}
	return nil
}

func sanitizeMutableToolCallErrorMessage(toolCall *convcli.MutableToolCall) {
	if toolCall == nil || toolCall.Has == nil || !toolCall.Has.ErrorMessage || toolCall.ErrorMessage == nil {
		return
	}
	sanitized := toolcallmodel.SanitizeErrorMessage(*toolCall.ErrorMessage)
	toolCall.ErrorMessage = &sanitized
}

func (s *Service) PatchToolApprovalQueue(ctx context.Context, queue *toolapprovalqueuemodel.ToolApprovalQueue) error {
	return s.patchApprovalNative(ctx, queue)
}

func (s *Service) ListToolApprovalQueues(ctx context.Context, in *toolapprovalqueuemodel.QueueRowsInput) ([]*toolapprovalqueuemodel.QueueRowView, error) {
	return s.ListToolApprovalQueuesWithSelectors(ctx, in)
}

func (s *Service) ListToolApprovalQueuesWithSelectors(ctx context.Context, in *toolapprovalqueuemodel.QueueRowsInput, selectors ...*hstate.NamedSelector) ([]*toolapprovalqueuemodel.QueueRowView, error) {
	if s == nil || s.native == nil {
		return nil, errors.New("conversation service not configured: component invoker is nil")
	}
	if in == nil {
		in = &toolapprovalqueuemodel.QueueRowsInput{}
	}
	if err := enforceToolApprovalQueueUserScope(ctx, in); err != nil {
		return nil, err
	}

	return s.listApprovalNative(ctx, in, selectors)

}

func (s *Service) CountToolApprovalQueues(ctx context.Context, in *toolapprovalqueuemodel.QueueTotalInput) (int, error) {
	if s == nil || s.native == nil {
		return 0, errors.New("conversation service not configured: component invoker is nil")
	}
	if in == nil {
		in = &toolapprovalqueuemodel.QueueTotalInput{}
	}
	if userID := strings.TrimSpace(authctx.EffectiveUserID(ctx)); userID != "" {
		if strings.TrimSpace(in.UserId) != "" && !strings.EqualFold(strings.TrimSpace(in.UserId), userID) {
			return 0, data.ErrPermissionDenied
		}
		in.UserId = userID
		if in.Has == nil {
			in.Has = &toolapprovalqueuemodel.QueueTotalInputHas{}
		}
		in.Has.UserId = true
	}

	return s.countApprovalNative(ctx, in)

}

func (s *Service) ListToolApprovalOutcomes(ctx context.Context, in *toolapprovalqueuemodel.OutcomeRowsInput) ([]*toolapprovalqueuemodel.OutcomeRowView, error) {
	if s == nil || s.native == nil {
		return nil, errors.New("conversation service not configured: component invoker is nil")
	}
	if in == nil {
		in = &toolapprovalqueuemodel.OutcomeRowsInput{}
	}
	if userID := strings.TrimSpace(authctx.EffectiveUserID(ctx)); userID != "" {
		if strings.TrimSpace(in.UserId) != "" && !strings.EqualFold(strings.TrimSpace(in.UserId), userID) {
			return nil, data.ErrPermissionDenied
		}
		in.UserId = userID
		if in.Has == nil {
			in.Has = &toolapprovalqueuemodel.OutcomeRowsInputHas{}
		}
		in.Has.UserId = true
	}

	return s.listApprovalOutcomesNative(ctx, in)

}

func enforceToolApprovalQueueUserScope(ctx context.Context, in *toolapprovalqueuemodel.QueueRowsInput) error {
	if in == nil {
		return nil
	}
	userID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	if userID == "" {
		return nil
	}
	if strings.TrimSpace(in.UserId) != "" && !strings.EqualFold(strings.TrimSpace(in.UserId), userID) {
		return data.ErrPermissionDenied
	}
	in.UserId = userID
	if in.Has == nil {
		in.Has = &toolapprovalqueuemodel.QueueRowsInputHas{}
	}
	in.Has.UserId = true
	return nil
}

// ToolCallTraceByOp returns the persisted trace_id (LLM response.id anchor) for a tool call op_id
// scoped to a conversation. It returns an empty string when not found.
func (s *Service) ToolCallTraceByOp(ctx context.Context, conversationID, opID string) (string, error) {
	return s.callsStore().TraceByOp(ctx, conversationID, opID)
}

func (s *Service) PatchTurn(ctx context.Context, turn *convcli.MutableTurn) error {
	if s == nil || s.native == nil {
		return errors.New("conversation service not configured")
	}
	if turn == nil {
		return errors.New("invalid turn: nil")
	}
	logx.Infof("conversation", "PatchTurn start id=%q convo=%q status=%q queue_seq=%v", strings.TrimSpace(turn.Id), strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.Status), valueOrEmpty(turn.QueueSeq))

	if err := s.patchTurnNative(ctx, turn); err != nil {
		return err
	}
	s.publishTurnEvent(ctx, turn)
	return nil
}

func (s *Service) publishTurnEvent(ctx context.Context, turn *convcli.MutableTurn) {
	if s == nil || s.streamPub == nil || turn == nil || turn.Has == nil {
		return
	}
	status := strings.ToLower(strings.TrimSpace(turn.Status))
	if !turn.Has.Status || status == "" {
		return
	}
	conversationID := strings.TrimSpace(turn.ConversationID)
	if conversationID == "" {
		conversationID = strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	}
	if conversationID == "" {
		return
	}
	createdAt := turnEventCreatedAt(turn)
	userMessageID := strings.TrimSpace(valueOrEmptyStr(turn.StartedByMessageID))
	if userMessageID == "" {
		if turnMeta, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
			userMessageID = strings.TrimSpace(turnMeta.ParentMessageID)
		}
	}
	if status == "running" {
		patch := map[string]interface{}{
			"turnId":         strings.TrimSpace(turn.Id),
			"conversationId": conversationID,
			"status":         "running",
		}
		if userMessageID != "" {
			patch["userMessageId"] = userMessageID
		}
		agentIDUsed := strings.TrimSpace(valueOrEmptyStr(turn.AgentIDUsed))
		if agentIDUsed != "" {
			patch["agentIdUsed"] = agentIDUsed
		}
		if turn.Has.CreatedAt && turn.CreatedAt != nil && !turn.CreatedAt.IsZero() {
			patch["createdAt"] = turn.CreatedAt.Format(time.RFC3339Nano)
		}
		if turn.Has.RunID && turn.RunID != nil {
			patch["runId"] = strings.TrimSpace(*turn.RunID)
		}
		s.emitTimelineEvent(ctx, &streaming.Event{
			ID:             strings.TrimSpace(turn.Id),
			StreamID:       conversationID,
			ConversationID: conversationID,
			MessageID:      userMessageID,
			Type:           streaming.EventTypeControl,
			Op:             "turn_started",
			Patch:          patch,
			CreatedAt:      createdAt,
		}, "PatchTurn publish turn_started control")
		s.emitTimelineEvent(ctx, &streaming.Event{
			ID:             strings.TrimSpace(turn.Id),
			StreamID:       conversationID,
			ConversationID: conversationID,
			Type:           streaming.EventTypeTurnStarted,
			TurnID:         strings.TrimSpace(turn.Id),
			MessageID:      userMessageID,
			UserMessageID:  userMessageID,
			AgentIDUsed:    agentIDUsed,
			Status:         "running",
			CreatedAt:      createdAt,
		}, "PatchTurn publish turn_started")
		return
	}
	if status == "completed" || status == "succeeded" || status == "failed" || status == "canceled" || status == "cancelled" {
		eventType := streaming.EventTypeTurnCompleted
		switch status {
		case "failed":
			eventType = streaming.EventTypeTurnFailed
		case "canceled", "cancelled":
			eventType = streaming.EventTypeTurnCanceled
		}
		s.emitTimelineEvent(ctx, &streaming.Event{
			ID:             strings.TrimSpace(turn.Id),
			StreamID:       conversationID,
			ConversationID: conversationID,
			Type:           eventType,
			TurnID:         strings.TrimSpace(turn.Id),
			MessageID:      userMessageID,
			UserMessageID:  userMessageID,
			Status:         status,
			Error:          strings.TrimSpace(valueOrEmptyStr(turn.ErrorMessage)),
			CreatedAt:      createdAt,
		}, "PatchTurn publish "+string(eventType))
	}
}

// emitCanonicalAssistantEvents emits ONLY the `narration` event for
// interim assistant messages that carry narration text. It never emits
// a final-message event — the "final assistant message" concept has
// been removed (see publishMessagePatchEvent doc for rationale). Real
// messages (interim=0 with content) flow through emitMessageAppendedEvent.
func (s *Service) emitCanonicalAssistantEvents(ctx context.Context, message *convcli.MutableMessage, conversationID string) {
	if s == nil || s.streamPub == nil || message == nil || message.Has == nil {
		return
	}
	role := strings.ToLower(strings.TrimSpace(message.Role))
	if role != "assistant" {
		return
	}
	preamble := ""
	if message.Has.Narration && message.Narration != nil {
		preamble = strings.TrimSpace(*message.Narration)
	}
	content := ""
	if message.Has.Content && message.Content != nil {
		content = strings.TrimSpace(*message.Content)
	}
	isFinal := false
	if message.Has.Interim && message.Interim != nil {
		isFinal = *message.Interim == 0 && content != ""
	}

	turnID := ""
	if message.Has.TurnID && message.TurnID != nil {
		turnID = strings.TrimSpace(*message.TurnID)
	}
	if turnID == "" {
		if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
			turnID = strings.TrimSpace(turn.TurnID)
		}
	}

	// Emit narration event when we have narration text and the message is interim.
	// The text is carried in the dedicated `Narration` field (NOT `Content`) so
	// the TS reducer's `onAssistantNarration` handler — which reads
	// `event.narration` — sees it. Previously this was stuffed into
	// `Content`, leaving `event.narration` empty on the wire and causing the
	// client to silently drop the narration.
	if preamble != "" && !isFinal {
		logx.Infof("conversation", "emitCanonicalAssistantEvents narration convo=%q turn=%q msg=%q narration_len=%d", conversationID, turnID, strings.TrimSpace(message.Id), len(preamble))
		executionRole := strings.ToLower(strings.TrimSpace(valueOrEmptyStr(message.Mode)))
		if executionRole != "narrator" {
			executionRole = ""
		}
		// Narration source: runtime narrator when message.Mode == narrator,
		// otherwise the model itself wrote this text (pre-tool-call framing
		// or aggregated reasoning from the current iteration).
		narrationSource := "model"
		if executionRole == "narrator" {
			narrationSource = "narrator"
		}
		// For assistant-content events, `MessageID` is the canonical
		// assistant-bubble id. `AssistantMessageID` is intentionally not
		// populated — it's redundant on these events (the two coincide)
		// and its duplicate emission was the reason clients had to probe
		// both fields. The field remains on tool events where it
		// meaningfully differs from MessageID (parent bubble vs tool row).
		event := &streaming.Event{
			ID:              strings.TrimSpace(message.Id),
			StreamID:        conversationID,
			ConversationID:  conversationID,
			MessageID:       strings.TrimSpace(message.Id),
			PageID:          strings.TrimSpace(message.Id),
			Mode:            firstNonEmpty(strings.TrimSpace(valueOrEmptyStr(message.Mode)), requestModeForEvent(ctx)),
			Type:            streaming.EventTypeNarration,
			TurnID:          turnID,
			ExecutionRole:   executionRole,
			Narration:       preamble,
			NarrationSource: narrationSource,
			CreatedAt:       patchEventCreatedAt(message),
		}
		// Carry sequence + status on the narration event so the bubble
		// the client creates from narration can be ordered correctly
		// against sibling messages BEFORE the real `assistant` event
		// arrives for the same messageId. Sequence is DB-assigned and
		// stable from the moment the interim row is first persisted;
		// it will match whatever the later `assistant` event emits.
		narrationPatch := map[string]interface{}{}
		if message.Has != nil {
			if message.Has.Sequence && message.Sequence != nil {
				narrationPatch["sequence"] = *message.Sequence
			}
			if message.Has.Status && message.Status != nil {
				narrationPatch["status"] = strings.TrimSpace(*message.Status)
			}
		}
		if len(narrationPatch) > 0 {
			event.Patch = narrationPatch
		}
		applyIterationPage(event, message.Iteration)
		s.emitTimelineEvent(ctx, event, "PatchMessage publish narration")
	}
	// No final-message emission here. Non-interim assistant
	// messages flow through emitMessageAppendedEvent as
	// `message_appended`. End-of-turn is signaled separately by
	// `EventTypeTurnCompleted` / `EventTypeTurnFailed` /
	// `EventTypeTurnCanceled`.
	_ = content
	_ = isFinal
}

// emitCanonicalModelEvent emits a model_started or model_completed event
// alongside the legacy llm_request_started / llm_response events.
func (s *Service) emitCanonicalModelEvent(ctx context.Context, modelCall *convcli.MutableModelCall) {
	if s == nil || s.streamPub == nil || modelCall == nil {
		return
	}
	turn, _ := runtimerequestctx.TurnMetaFromContext(ctx)
	conversationID := strings.TrimSpace(turn.ConversationID)
	if conversationID == "" {
		conversationID = strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	}
	if conversationID == "" {
		logx.DebugCtxf(ctx, "conversation", "[emitCanonicalModelEvent] SKIP no conversationID msg=%q status=%q", modelCall.MessageID, modelCall.Status)
		return
	}
	status := strings.ToLower(strings.TrimSpace(modelCall.Status))
	mode := requestModeForEvent(ctx)
	modelCallID := strings.TrimSpace(valueOrEmptyStr(modelCall.TraceID))
	logx.DebugCtxf(ctx, "conversation", "[emitCanonicalModelEvent] convo=%q turn=%q msg=%q model_call=%q status=%q", conversationID, strings.TrimSpace(valueOrEmptyStr(modelCall.TurnID)), modelCall.MessageID, modelCallID, status)
	if status == "thinking" || status == "streaming" || status == "running" {
		event := &streaming.Event{
			ID:              strings.TrimSpace(modelCall.MessageID),
			StreamID:        conversationID,
			ConversationID:  conversationID,
			MessageID:       strings.TrimSpace(modelCall.MessageID),
			PageID:          strings.TrimSpace(modelCall.MessageID),
			Mode:            mode,
			Type:            streaming.EventTypeModelStarted,
			TurnID:          resolveTurnID(ctx, valueOrEmptyStr(modelCall.TurnID)),
			ParentMessageID: strings.TrimSpace(turn.ParentMessageID),
			ModelCallID:     modelCallID,
			Provider:        strings.TrimSpace(modelCall.Provider),
			ModelName:       strings.TrimSpace(modelCall.Model),
			Status:          strings.TrimSpace(modelCall.Status),
			Phase:           modelEventPhase(mode, modelCall.Iteration),
			CreatedAt:       time.Now(),
		}
		if modelCall.Model != "" || modelCall.Provider != "" {
			event.Model = &streaming.EventModel{
				Provider: strings.TrimSpace(modelCall.Provider),
				Model:    strings.TrimSpace(modelCall.Model),
				Kind:     strings.TrimSpace(modelCall.ModelKind),
			}
		}
		if modelCall.Has != nil && modelCall.Has.StartedAt && modelCall.StartedAt != nil && !modelCall.StartedAt.IsZero() {
			event.CreatedAt = *modelCall.StartedAt
			event.StartedAt = modelCall.StartedAt
		}
		if modelCall.Has != nil && modelCall.Has.RequestPayloadID && modelCall.RequestPayloadID != nil {
			event.RequestPayloadID = strings.TrimSpace(*modelCall.RequestPayloadID)
		}
		if modelCall.Has != nil && modelCall.Has.ProviderRequestPayloadID && modelCall.ProviderRequestPayloadID != nil {
			event.ProviderRequestPayloadID = strings.TrimSpace(*modelCall.ProviderRequestPayloadID)
		}
		if modelCall.Has != nil && modelCall.Has.StreamPayloadID && modelCall.StreamPayloadID != nil {
			event.StreamPayloadID = strings.TrimSpace(*modelCall.StreamPayloadID)
		}
		logx.Infof("conversation", "stream.model_started msg=%q turn=%q iteration=%d request_payload=%q provider_request_payload=%q stream_payload=%q provider=%q model=%q",
			strings.TrimSpace(modelCall.MessageID),
			strings.TrimSpace(event.TurnID),
			modelCall.Iteration,
			strings.TrimSpace(event.RequestPayloadID),
			strings.TrimSpace(event.ProviderRequestPayloadID),
			strings.TrimSpace(event.StreamPayloadID),
			strings.TrimSpace(event.Provider),
			strings.TrimSpace(event.ModelName),
		)
		applyIterationPage(event, modelCall.Iteration)
		s.emitTimelineEvent(ctx, event, "PatchModelCall publish model_started")
	} else if status == "completed" || status == "succeeded" || status == "failed" {
		now := time.Now()
		event := &streaming.Event{
			ID:              strings.TrimSpace(modelCall.MessageID),
			StreamID:        conversationID,
			ConversationID:  conversationID,
			MessageID:       strings.TrimSpace(modelCall.MessageID),
			PageID:          strings.TrimSpace(modelCall.MessageID),
			Mode:            mode,
			Type:            streaming.EventTypeModelCompleted,
			TurnID:          resolveTurnID(ctx, valueOrEmptyStr(modelCall.TurnID)),
			ParentMessageID: strings.TrimSpace(turn.ParentMessageID),
			ModelCallID:     modelCallID,
			Provider:        strings.TrimSpace(modelCall.Provider),
			ModelName:       strings.TrimSpace(modelCall.Model),
			Status:          strings.TrimSpace(modelCall.Status),
			Phase:           modelEventPhase(mode, modelCall.Iteration),
			CreatedAt:       now,
			CompletedAt:     &now,
		}
		if modelCall.Model != "" || modelCall.Provider != "" {
			event.Model = &streaming.EventModel{
				Provider: strings.TrimSpace(modelCall.Provider),
				Model:    strings.TrimSpace(modelCall.Model),
				Kind:     strings.TrimSpace(modelCall.ModelKind),
			}
		}
		event.Usage = modelCallUsageSnapshot(modelCall)
		if modelCall.Has != nil && modelCall.Has.StartedAt && modelCall.StartedAt != nil && !modelCall.StartedAt.IsZero() {
			event.StartedAt = modelCall.StartedAt
		}
		if modelCall.Has != nil && modelCall.Has.RequestPayloadID && modelCall.RequestPayloadID != nil {
			event.RequestPayloadID = strings.TrimSpace(*modelCall.RequestPayloadID)
		}
		if modelCall.Has != nil && modelCall.Has.ResponsePayloadID && modelCall.ResponsePayloadID != nil {
			event.ResponsePayloadID = strings.TrimSpace(*modelCall.ResponsePayloadID)
		}
		if modelCall.Has != nil && modelCall.Has.ProviderRequestPayloadID && modelCall.ProviderRequestPayloadID != nil {
			event.ProviderRequestPayloadID = strings.TrimSpace(*modelCall.ProviderRequestPayloadID)
		}
		if modelCall.Has != nil && modelCall.Has.ProviderResponsePayloadID && modelCall.ProviderResponsePayloadID != nil {
			event.ProviderResponsePayloadID = strings.TrimSpace(*modelCall.ProviderResponsePayloadID)
		}
		if modelCall.Has != nil && modelCall.Has.StreamPayloadID && modelCall.StreamPayloadID != nil {
			event.StreamPayloadID = strings.TrimSpace(*modelCall.StreamPayloadID)
		}
		logx.Infof("conversation", "stream.model_completed msg=%q turn=%q iteration=%d request_payload=%q response_payload=%q provider_request_payload=%q provider_response_payload=%q stream_payload=%q provider=%q model=%q status=%q",
			strings.TrimSpace(modelCall.MessageID),
			strings.TrimSpace(event.TurnID),
			modelCall.Iteration,
			strings.TrimSpace(event.RequestPayloadID),
			strings.TrimSpace(event.ResponsePayloadID),
			strings.TrimSpace(event.ProviderRequestPayloadID),
			strings.TrimSpace(event.ProviderResponsePayloadID),
			strings.TrimSpace(event.StreamPayloadID),
			strings.TrimSpace(event.Provider),
			strings.TrimSpace(event.ModelName),
			strings.TrimSpace(event.Status),
		)
		// Include LLM response data (content, preamble, finalResponse) when
		// available via context — makes model_completed self-sufficient.
		if meta, ok := runtimerequestctx.ModelCompletionMetaFromContext(ctx); ok {
			event.Content = meta.Content
			event.Narration = meta.Narration
			// Narration carried on a model_completed event is always the
			// model's own authoring (reasoning-delta aggregate or
			// pre-tool-call framing). Tag it so clients don't conflate
			// with runtime-narrator updates.
			if strings.TrimSpace(meta.Narration) != "" {
				event.NarrationSource = "model"
			}
			event.FinalResponse = meta.FinalResponse
		}
		applyIterationPage(event, modelCall.Iteration)
		s.emitTimelineEvent(ctx, event, "PatchModelCall publish model_completed")
	}
}

func modelCallUsageSnapshot(modelCall *convcli.MutableModelCall) *streaming.UsageSnapshot {
	if modelCall == nil || modelCall.Has == nil {
		return nil
	}
	hasUsage := modelCall.Has.PromptTokens || modelCall.Has.CompletionTokens || modelCall.Has.PromptCachedTokens ||
		modelCall.Has.CompletionReasoningTokens || modelCall.Has.TotalTokens
	if !hasUsage {
		return nil
	}
	input := intPointerValue(modelCall.PromptTokens)
	output := intPointerValue(modelCall.CompletionTokens)
	total := intPointerValue(modelCall.TotalTokens)
	if total <= 0 {
		total = input + output
	}
	embedding := total - input - output
	if embedding < 0 {
		embedding = 0
	}
	return &streaming.UsageSnapshot{
		Scope:             "model_call",
		InputTokens:       input,
		OutputTokens:      output,
		CachedInputTokens: intPointerValue(modelCall.PromptCachedTokens),
		ReasoningTokens:   intPointerValue(modelCall.CompletionReasoningTokens),
		EmbeddingTokens:   embedding,
		TotalTokens:       total,
	}
}

func intPointerValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func resolveTurnID(ctx context.Context, explicit string) string {
	if turnID := strings.TrimSpace(explicit); turnID != "" {
		return turnID
	}
	if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		return strings.TrimSpace(turn.TurnID)
	}
	return ""
}

// DeleteConversation removes a conversation through the canonical generated writer.
func (s *Service) DeleteConversation(ctx context.Context, id string) error {
	if s == nil || s.native == nil {
		return errors.New("conversation service not configured")
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("conversation id is required")
	}

	return (&convstore.Store{Invoker: s.native}).DeleteTrusted(ctx, id)
}

// DeleteMessage removes a single message through the canonical generated writer.
func (s *Service) DeleteMessage(ctx context.Context, conversationID, messageID string) error {
	if strings.TrimSpace(messageID) == "" {
		return errors.New("message id is required")
	}
	if s == nil || s.native == nil {
		return errors.New("conversation service not configured")
	}

	// Optional safety check: if conversationID provided, verify the message belongs to it.
	if strings.TrimSpace(conversationID) != "" {
		if got, _ := s.GetMessage(ctx, messageID); got != nil && strings.TrimSpace(got.ConversationId) != "" {
			if !strings.EqualFold(strings.TrimSpace(got.ConversationId), strings.TrimSpace(conversationID)) {
				return errors.New("message does not belong to the specified conversation")
			}
		}
	}

	return (&convstore.MessageStore{Invoker: s.native}).DeleteTrusted(ctx, messageID)
}
