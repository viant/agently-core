package modelcall

import (
	"context"
	"strings"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/internal/logx"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

func messageText(msg llm.Message) string {
	if s := strings.TrimSpace(msg.Content); s != "" {
		return s
	}
	items := msg.Items
	if len(items) == 0 {
		items = msg.ContentItems
	}
	var parts []string
	for _, it := range items {
		if strings.TrimSpace(string(it.Type)) != "" && it.Type != llm.ContentTypeText {
			continue
		}
		if s := strings.TrimSpace(it.Text); s != "" {
			parts = append(parts, s)
			continue
		}
		if s := strings.TrimSpace(it.Data); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func (o *recorderObserver) publishStreamDelta(ctx context.Context, data []byte) {
	pub, ok := StreamPublisherFromContext(ctx)
	if !ok || looksLikeElicitationDelta(data) {
		return
	}
	convID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	msgID := strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx))
	if convID == "" || msgID == "" {
		return
	}
	msg := &agconv.MessageView{
		Id:             msgID,
		ConversationId: convID,
		Role:           "assistant",
		Type:           "text",
	}
	if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		if strings.TrimSpace(turn.TurnID) != "" {
			msg.TurnId = &turn.TurnID
		}
		if strings.TrimSpace(turn.ParentMessageID) != "" {
			msg.ParentMessageId = &turn.ParentMessageID
		}
	}
	msg.Interim = 1
	content := map[string]interface{}{
		"delta": string(data),
		"meta":  map[string]interface{}{"kind": "stream_delta"},
	}
	_ = pub.Publish(ctx, &StreamEvent{
		ConversationID: convID,
		Message:        msg,
		ContentType:    "application/json",
		Content:        content,
	})
}

func looksLikeElicitationDelta(data []byte) bool {
	return looksLikeElicitationContent(string(data))
}

func looksLikeElicitationContent(content string) bool {
	if len(content) == 0 {
		return false
	}
	raw := strings.ToLower(strings.TrimSpace(content))
	if raw == "" {
		return false
	}
	if strings.Contains(raw, "\"requestedschema\"") && strings.Contains(raw, "\"type\"") && strings.Contains(raw, "elicitation") {
		return true
	}
	return strings.Contains(raw, "\"type\"") && strings.Contains(raw, "elicitation") && strings.HasPrefix(raw, "{")
}

func messagePhaseFromMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "router":
		return "intake"
	case "summary":
		return "summary"
	default:
		return ""
	}
}

// patchInterimRequestMessage creates an interim assistant message capturing the request payload.
func (o *recorderObserver) patchInterimRequestMessage(ctx context.Context, turn runtimerequestctx.TurnMeta, msgID string, payload []byte, mode string) error {
	logx.Infof("conversation", "patchInterimRequestMessage start convo=%q turn=%q msg=%q mode=%q payload_bytes=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(msgID), strings.TrimSpace(mode), len(payload))
	opts := []apiconv.MessageOption{
		apiconv.WithId(msgID),
		apiconv.WithMode(mode),
		apiconv.WithRole("assistant"),
		apiconv.WithType("text"),
		apiconv.WithCreatedByUserID(turn.Assistant),
		apiconv.WithInterim(1),
	}
	if runMeta, ok := runtimerequestctx.RunMetaFromContext(ctx); ok && runMeta.Iteration > 0 {
		opts = append(opts, apiconv.WithIteration(runMeta.Iteration))
	}
	if phase := messagePhaseFromMode(mode); phase != "" {
		opts = append(opts, apiconv.WithPhase(phase))
	}
	_, err := apiconv.AddMessage(ctx, o.client, &turn, opts...)
	if err != nil {
		logx.Errorf("conversation", "patchInterimRequestMessage error convo=%q turn=%q msg=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(msgID), err)
	} else {
		logx.Infof("conversation", "patchInterimRequestMessage ok convo=%q turn=%q msg=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(msgID))
	}
	return err
}

// patchInterimFlag marks an existing message as interim.
func (o *recorderObserver) patchInterimFlag(ctx context.Context, msgID string) error {
	msg := apiconv.NewMessage()
	msg.SetId(msgID)
	if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok && strings.TrimSpace(turn.ConversationID) != "" {
		msg.SetConversationID(turn.ConversationID)
	}
	msg.SetInterim(1)
	err := o.client.PatchMessage(ctx, msg)
	if err != nil {
		logx.Errorf("conversation", "patchInterimFlag error msg=%q err=%v", strings.TrimSpace(msgID), err)
	} else {
		logx.Infof("conversation", "patchInterimFlag ok msg=%q", strings.TrimSpace(msgID))
	}
	return err
}

// beginModelCall persists the initial model call and associated request payloads.
func (o *recorderObserver) beginModelCall(ctx context.Context, msgID string, turn runtimerequestctx.TurnMeta, info Info) error {
	logx.Infof("conversation", "beginModelCall start convo=%q turn=%q msg=%q provider=%q model=%q kind=%q req_bytes=%d provider_req_bytes=%d", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(msgID), strings.TrimSpace(info.Provider), strings.TrimSpace(info.Model), strings.TrimSpace(info.ModelKind), len(info.Payload), len(info.RequestJSON))
	mc := apiconv.NewModelCall()
	mc.SetMessageID(msgID)
	if turn.TurnID != "" {
		mc.SetTurnID(turn.TurnID)
	}
	if runMeta, ok := runtimerequestctx.RunMetaFromContext(ctx); ok {
		if strings.TrimSpace(runMeta.RunID) != "" {
			mc.SetRunID(runMeta.RunID)
		}
		if runMeta.Iteration > 0 {
			mc.SetIteration(runMeta.Iteration)
		}
	}
	mc.SetProvider(info.Provider)
	mc.SetModel(info.Model)
	if strings.TrimSpace(info.ModelKind) != "" {
		mc.SetModelKind(info.ModelKind)
	}
	mc.SetStatus("thinking")
	mc.SetStartedAt(o.start.StartedAt)
	if len(info.Payload) > 0 {
		reqID, err := o.upsertInlinePayload(ctx, "", "model_request", "application/json", info.Payload)
		if err != nil {
			logx.Errorf("conversation", "beginModelCall request payload error msg=%q err=%v", strings.TrimSpace(msgID), err)
			return err
		}
		mc.SetRequestPayloadID(reqID)
		o.requestPayloadID = reqID
	}
	if len(info.RequestJSON) > 0 {
		prID, err := o.upsertInlinePayload(ctx, "", "provider_request", "application/json", info.RequestJSON)
		if err != nil {
			logx.Errorf("conversation", "beginModelCall provider payload error msg=%q err=%v", strings.TrimSpace(msgID), err)
			return err
		}
		mc.SetProviderRequestPayloadID(prID)
		o.providerRequestPayloadID = prID
		_ = debugtrace.WritePayload("llm-provider-request", msgID, info.RequestJSON)
	}
	if err := o.client.PatchModelCall(ctx, mc); err != nil {
		logx.Errorf("conversation", "beginModelCall patch model call error msg=%q err=%v", strings.TrimSpace(msgID), err)
		return err
	}
	logx.Infof("conversation", "beginModelCall ok convo=%q turn=%q msg=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(msgID))
	return nil
}

func (o *recorderObserver) upsertInlinePayload(ctx context.Context, id, kind, mime string, body []byte) (string, error) {
	if strings.TrimSpace(id) == "" {
		id = uuid.New().String()
	}
	sizeBytes := len(body)
	logPayloadDebug := sizeBytes%512 == 0
	if logPayloadDebug {
		logx.Infof("conversation", "upsertInlinePayload start id=%q kind=%q mime=%q size_bytes=%d", strings.TrimSpace(id), strings.TrimSpace(kind), strings.TrimSpace(mime), sizeBytes)
	}
	pw := apiconv.NewPayload()
	pw.SetId(id)
	pw.SetKind(kind)
	pw.SetMimeType(mime)
	pw.SetSizeBytes(sizeBytes)
	pw.SetStorage("inline")
	pw.SetInlineBody(body)
	if err := o.client.PatchPayload(ctx, pw); err != nil {
		logx.Errorf("conversation", "upsertInlinePayload error id=%q err=%v", strings.TrimSpace(id), err)
		return "", err
	}
	if logPayloadDebug {
		logx.Infof("conversation", "upsertInlinePayload ok id=%q", strings.TrimSpace(id))
	}
	return id, nil
}
