package elicitation

// moved from genai/service/elicitation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/textutil"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/protocol/agent/execution"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	elact "github.com/viant/agently-core/service/elicitation/action"
	elicrouter "github.com/viant/agently-core/service/elicitation/router"
	toolapproval "github.com/viant/agently-core/service/shared/toolapproval"
	"github.com/viant/mcp-protocol/schema"
)

type Refiner interface {
	RefineRequestedSchema(rs *schema.ElicitRequestParamsRequestedSchema)
}

type Service struct {
	client         apiconv.Client
	refiner        Refiner
	router         elicrouter.ElicitationRouter
	awaiterFactory func() Awaiter
	streamPub      streaming.Publisher
}

type parentElicitationMessageGetter interface {
	GetMessageByParentAndElicitation(ctx context.Context, parentMessageID, elicitationID string) (*apiconv.Message, error)
}

type linkedElicitationProxyGetter interface {
	GetMessageByLinkedConversationAndElicitation(ctx context.Context, linkedConversationID, elicitationID string) (*apiconv.Message, error)
}

type elicitationResponseMessageGetter interface {
	GetElicitationResponseMessage(ctx context.Context, conversationID, elicitationID string) (*apiconv.Message, error)
}

type elicitationResolutionTarget struct {
	submitted     *apiconv.Message
	authoritative *apiconv.Message
	proxy         *apiconv.Message
}

// New constructs the elicitation service with all collaborators.
// The refiner is defaulted to a workspace preset implementation when nil.
// Router and awaiter factory must be supplied by the caller to ensure proper wiring.
func New(client apiconv.Client, refiner Refiner, router elicrouter.ElicitationRouter, awaiterFactory func() Awaiter) *Service {
	if refiner == nil {
		refiner = DefaultRefiner{}
	}
	return &Service{client: client, refiner: refiner, router: router, awaiterFactory: awaiterFactory}
}

// SetStreamPublisher wires a streaming publisher so the service can emit
// canonical elicitation events to the SSE bus.
func (s *Service) SetStreamPublisher(p streaming.Publisher) {
	if s == nil {
		return
	}
	s.streamPub = p
}

func (s *Service) emitElicitationRequested(ctx context.Context, turn *runtimerequestctx.TurnMeta, elic *execution.Elicitation, messageID string) {
	if s == nil || s.streamPub == nil || turn == nil || elic == nil {
		return
	}
	logx.Infof("conversation", "emitElicitationRequested convo=%q turn=%q elicitation_id=%q message_id=%q message=%q callback=%q", turn.ConversationID, turn.TurnID, elic.ElicitationId, messageID, elic.Message, elic.CallbackURL)
	// Marshal the full ElicitRequestParams (schema, mode, url) into elicData
	// so the UI can detect OOB elicitations and render the correct form/URL dialog.
	elicData := map[string]interface{}{}
	if raw, err := json.Marshal(elic.ElicitRequestParams); err == nil {
		_ = json.Unmarshal(raw, &elicData)
		logx.Infof("conversation", "[elicit-data] raw=%s", string(raw))
	}
	// Remove redundant fields already on the Event struct.
	delete(elicData, "message")
	delete(elicData, "elicitationId")
	delete(elicData, "_meta")
	logx.Infof("conversation", "[elicit-data] mode=%v url=%v schemaType=%v propsCount=%v",
		elicData["mode"], elicData["url"],
		elicData["requestedSchema"],
		func() int {
			if rs, ok := elicData["requestedSchema"].(map[string]interface{}); ok {
				if p, ok := rs["properties"].(map[string]interface{}); ok {
					return len(p)
				}
			}
			return -1
		}())
	now := time.Now()
	event := &streaming.Event{
		ID:                 strings.TrimSpace(messageID),
		StreamID:           strings.TrimSpace(turn.ConversationID),
		ConversationID:     strings.TrimSpace(turn.ConversationID),
		TurnID:             strings.TrimSpace(turn.TurnID),
		MessageID:          strings.TrimSpace(messageID),
		AssistantMessageID: strings.TrimSpace(messageID),
		Type:               streaming.EventTypeElicitationRequested,
		ElicitationID:      strings.TrimSpace(elic.ElicitationId),
		Content:            strings.TrimSpace(elic.Message),
		ElicitationData:    elicData,
		CallbackURL:        strings.TrimSpace(elic.CallbackURL),
		Status:             "pending",
		CreatedAt:          now,
	}
	event.NormalizeIdentity(strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
	if err := s.streamPub.Publish(ctx, event); err != nil {
		logx.Warnf("conversation", "elicitation_requested publish error convo=%q elicitation_id=%q err=%v", turn.ConversationID, elic.ElicitationId, err)
	}
	logx.Infof("conversation", "emitElicitationRequested ok convo=%q elicitation_id=%q", turn.ConversationID, elic.ElicitationId)
}

func (s *Service) emitElicitationResolved(ctx context.Context, convID, elicitationID, status string, payload map[string]interface{}) {
	if s == nil || s.streamPub == nil {
		return
	}
	now := time.Now()
	turnID := ""
	if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		turnID = strings.TrimSpace(turn.TurnID)
	}
	messageID := strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(ctx))
	if messageID == "" {
		messageID = strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx))
	}
	if (turnID == "" || messageID == "") && s.client != nil {
		if msg, err := s.client.GetMessageByElicitation(ctx, strings.TrimSpace(convID), strings.TrimSpace(elicitationID)); err == nil && msg != nil {
			if turnID == "" && msg.TurnId != nil {
				turnID = strings.TrimSpace(*msg.TurnId)
			}
			if messageID == "" {
				messageID = strings.TrimSpace(msg.Id)
			}
		}
	}
	event := &streaming.Event{
		StreamID:        strings.TrimSpace(convID),
		ConversationID:  strings.TrimSpace(convID),
		TurnID:          turnID,
		MessageID:       messageID,
		Type:            streaming.EventTypeElicitationResolved,
		ElicitationID:   strings.TrimSpace(elicitationID),
		Status:          strings.TrimSpace(status),
		ResponsePayload: payload,
		CreatedAt:       now,
		CompletedAt:     &now,
	}
	event.NormalizeIdentity(strings.TrimSpace(convID), turnID)
	if err := s.streamPub.Publish(ctx, event); err != nil {
		logx.Warnf("conversation", "elicitation_resolved publish error convo=%q elicitation_id=%q err=%v", convID, elicitationID, err)
	}
}

func (s *Service) RefineRequestedSchema(rs *schema.ElicitRequestParamsRequestedSchema) {
	if rs == nil {
		return
	}
	if s == nil || s.refiner == nil {
		DefaultRefiner{}.RefineRequestedSchema(rs)
		return
	}
	s.refiner.RefineRequestedSchema(rs)
}

// Record persists an elicitation control message and returns its message id.
func (s *Service) Record(ctx context.Context, turn *runtimerequestctx.TurnMeta, role string, elic *execution.Elicitation) (*apiconv.MutableMessage, error) {
	if strings.TrimSpace(elic.ElicitationId) == "" {
		elic.ElicitationId = uuid.New().String()
	}
	logx.Infof("conversation", "elicitation record start convo=%q turn=%q elicitation_id=%q role=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(elic.ElicitationId), strings.TrimSpace(role))
	s.RefineRequestedSchema(&elic.RequestedSchema)
	// Provide a unified callback URL when not already set
	if strings.TrimSpace(elic.CallbackURL) == "" && turn != nil {
		elic.CallbackURL = fmt.Sprintf("/v1/api/conversations/%s/elicitation/%s", turn.ConversationID, elic.ElicitationId)
	}
	payloadID, err := s.storeElicitationRequestPayload(ctx, elic)
	if err != nil {
		logx.Errorf("conversation", "elicitation request payload error convo=%q elicitation_id=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(elic.ElicitationId), err)
		return nil, err
	}
	messageType := "control"
	if role == llm.RoleAssistant.String() {
		messageType = "text"
	}
	content := strings.TrimSpace(elic.Message)
	if content == "" {
		content = "Additional input required."
	}
	msgID := uuid.New().String()
	if role == llm.RoleAssistant.String() {
		if existingID := strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx)); existingID != "" {
			msgID = existingID
		} else if turn != nil {
			if existingID := strings.TrimSpace(runtimerequestctx.TurnModelMessageID(turn.TurnID)); existingID != "" {
				msgID = existingID
			}
		}
	}
	msg := apiconv.NewMessage()
	msg.SetId(msgID)
	msg.SetRole(role)
	msg.SetType(messageType)
	msg.SetElicitationID(elic.ElicitationId)
	msg.SetElicitationPayloadID(payloadID)
	msg.SetStatus("pending")
	msg.SetContent(content)
	if turn != nil {
		msg.SetConversationID(turn.ConversationID)
		if strings.TrimSpace(turn.TurnID) != "" {
			msg.SetTurnID(turn.TurnID)
		}
		if strings.TrimSpace(turn.ParentMessageID) != "" {
			msg.SetParentMessageID(turn.ParentMessageID)
		}
	}
	if err := s.client.PatchMessage(ctx, msg); err != nil {
		logx.Errorf("conversation", "elicitation record error convo=%q elicitation_id=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(elic.ElicitationId), err)
		return nil, err
	}
	if err := s.proxyElicitationToTopLevelConversation(ctx, turn, msg, elic); err != nil {
		return nil, err
	}
	// MutableMessage return mirrors the persisted row id for downstream callers.
	ret := apiconv.NewMessage()
	ret.SetId(msgID)
	ret.SetConversationID(turn.ConversationID)
	ret.SetRole(role)
	ret.SetType(messageType)
	ret.SetContent(content)
	ret.SetElicitationID(elic.ElicitationId)
	ret.SetElicitationPayloadID(payloadID)
	ret.SetStatus("pending")
	logx.Infof("conversation", "elicitation record ok convo=%q elicitation_id=%q message_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(elic.ElicitationId), strings.TrimSpace(ret.Id))
	s.emitElicitationRequested(ctx, turn, elic, ret.Id)
	return ret, nil
}

// Wait blocks until an elicitation is accepted/declined via router/UI or optional local awaiter.
// On accept, it best-effort persists payload and status. It returns (accepted, payload, error).
func (s *Service) Wait(ctx context.Context, convID, elicitationID string) (string, map[string]interface{}, error) {
	if s.router == nil {
		return "", nil, fmt.Errorf("elicitation router not configured")
	}
	if strings.TrimSpace(convID) == "" || strings.TrimSpace(elicitationID) == "" {
		return "", nil, fmt.Errorf("conversation and elicitation id required")
	}
	if msg, err := s.client.GetMessageByElicitation(ctx, convID, elicitationID); err == nil && msg != nil {
		status := ""
		if msg.Status != nil {
			status = strings.TrimSpace(*msg.Status)
		}
		if status != "" && !strings.EqualFold(status, "pending") {
			act := elact.FromStatus(status)
			var payload map[string]interface{}
			if msg.ElicitationPayloadId != nil && strings.TrimSpace(*msg.ElicitationPayloadId) != "" {
				if p, pErr := s.client.GetPayload(ctx, *msg.ElicitationPayloadId); pErr == nil && p != nil && p.InlineBody != nil && len(*p.InlineBody) > 0 {
					_ = json.Unmarshal(*p.InlineBody, &payload)
				}
			}
			logx.Infof("conversation", "elicitation wait short-circuit convo=%q elicitation_id=%q status=%q action=%q", strings.TrimSpace(convID), strings.TrimSpace(elicitationID), status, act)
			return act, payload, nil
		}
	}
	logx.Infof("conversation", "elicitation wait start convo=%q elicitation_id=%q", strings.TrimSpace(convID), strings.TrimSpace(elicitationID))
	ch := make(chan *schema.ElicitResult, 1)
	s.router.RegisterByElicitationID(convID, elicitationID, ch)
	defer s.router.RemoveByElicitation(convID, elicitationID)

	// Spawn local awaiter if configured. Retrieve original elicitation schema to prompt properly.
	if s.awaiterFactory != nil {
		go func() {
			var req execution.Elicitation
			if msg, err := s.client.GetMessageByElicitation(ctx, convID, elicitationID); err == nil && msg != nil {
				if loaded, ok := s.loadRecordedElicitation(ctx, msg); ok {
					req = loaded
				} else if msg.Content != nil {
					if c := strings.TrimSpace(*msg.Content); c != "" {
						_ = json.Unmarshal([]byte(c), &req)
					}
				}
			}
			// Ensure ElicitationId is present
			req.ElicitRequestParams.ElicitationId = elicitationID
			aw := s.awaiterFactory()
			res, err := aw.AwaitElicitation(ctx, &req)
			if err != nil || res == nil {
				return
			}
			// Persist when accepted and notify router
			action := strings.ToLower(string(res.Action))
			switch action {
			case elact.Accept:
				if res.Payload != nil {
					_ = s.StorePayload(ctx, convID, elicitationID, res.Payload)
					_ = s.UpdateStatus(ctx, convID, elicitationID, elact.Accept)
				}
				// If accepted without payload, do not mark declined; UI callback should resolve.
			default: // decline or other
				_ = s.UpdateStatus(ctx, convID, elicitationID, elact.Decline)
				if strings.TrimSpace(res.Reason) != "" {
					_ = s.StoreDeclineReason(ctx, convID, elicitationID, res.Reason)
				}
			}
			out := &schema.ElicitResult{Action: schema.ElicitResultAction(elact.Normalize(string(res.Action))), Content: res.Payload}
			s.router.AcceptByElicitation(convID, elicitationID, out)
		}()
	}

	select {
	case <-ctx.Done():
		if msg, err := s.client.GetMessageByElicitation(context.Background(), convID, elicitationID); err == nil && msg != nil {
			status := ""
			if msg.Status != nil {
				status = strings.TrimSpace(*msg.Status)
			}
			if status != "" && !strings.EqualFold(status, "pending") {
				act := elact.FromStatus(status)
				var payload map[string]interface{}
				if msg.ElicitationPayloadId != nil && strings.TrimSpace(*msg.ElicitationPayloadId) != "" {
					if p, pErr := s.client.GetPayload(context.Background(), *msg.ElicitationPayloadId); pErr == nil && p != nil && p.InlineBody != nil && len(*p.InlineBody) > 0 {
						_ = json.Unmarshal(*p.InlineBody, &payload)
					}
				}
				logx.Infof("conversation", "elicitation wait canceled but persisted convo=%q elicitation_id=%q status=%q action=%q", strings.TrimSpace(convID), strings.TrimSpace(elicitationID), status, act)
				return act, payload, nil
			}
		}
		logx.Warnf("conversation", "elicitation wait canceled convo=%q elicitation_id=%q err=%v", strings.TrimSpace(convID), strings.TrimSpace(elicitationID), ctx.Err())
		return "", nil, ctx.Err()
	case res := <-ch:
		if res == nil {
			logx.Warnf("conversation", "elicitation wait empty result convo=%q elicitation_id=%q", strings.TrimSpace(convID), strings.TrimSpace(elicitationID))
			return elact.Decline, nil, nil
		}
		act := elact.Normalize(string(res.Action))
		logx.Infof("conversation", "elicitation wait result convo=%q elicitation_id=%q action=%q payload_keys=%v", strings.TrimSpace(convID), strings.TrimSpace(elicitationID), strings.TrimSpace(act), PayloadKeys(res.Content))
		return act, res.Content, nil
	}
}

func (s *Service) storeElicitationRequestPayload(ctx context.Context, elic *execution.Elicitation) (string, error) {
	raw, err := json.Marshal(elic)
	if err != nil {
		return "", err
	}
	pid := uuid.New().String()
	payload := apiconv.NewPayload()
	payload.SetId(pid)
	payload.SetKind("elicitation_request")
	payload.SetMimeType("application/json")
	payload.SetSizeBytes(len(raw))
	payload.SetStorage("inline")
	payload.SetInlineBody(raw)
	if err = s.client.PatchPayload(ctx, payload); err != nil {
		return "", err
	}
	return pid, nil
}

func (s *Service) loadRecordedElicitation(ctx context.Context, msg *apiconv.Message) (execution.Elicitation, bool) {
	if msg == nil || msg.ElicitationPayloadId == nil || strings.TrimSpace(*msg.ElicitationPayloadId) == "" {
		return execution.Elicitation{}, false
	}
	payload, err := s.client.GetPayload(ctx, strings.TrimSpace(*msg.ElicitationPayloadId))
	if err != nil || payload == nil || payload.InlineBody == nil || len(*payload.InlineBody) == 0 {
		return execution.Elicitation{}, false
	}
	var req execution.Elicitation
	if err = json.Unmarshal(*payload.InlineBody, &req); err != nil {
		return execution.Elicitation{}, false
	}
	return req, true
}

// Elicit records a new elicitation control message and waits for a resolution via router/UI.
// Returns message id, normalized status (accepted/rejected/cancel) and optional payload.
func (s *Service) Elicit(ctx context.Context, turn *runtimerequestctx.TurnMeta, role string, req *execution.Elicitation) (string, string, map[string]interface{}, error) {
	if req == nil || turn == nil {
		return "", "", nil, fmt.Errorf("invalid input")
	}

	msg, err := s.Record(ctx, turn, role, req)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to record message: %w", err)
	}
	logx.Infof("conversation", "elicitation Elicit start convo=%q elicitation_id=%q message_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(req.ElicitationId), strings.TrimSpace(msg.Id))

	status, payload, err := s.Wait(ctx, turn.ConversationID, req.ElicitationId)
	if err != nil {
		logx.Errorf("conversation", "elicitation Elicit error convo=%q elicitation_id=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(req.ElicitationId), err)
		return msg.Id, "", nil, err
	}
	logx.Infof("conversation", "elicitation Elicit done convo=%q elicitation_id=%q status=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(req.ElicitationId), strings.TrimSpace(status))
	return msg.Id, status, payload, nil
}

func (s *Service) proxyElicitationToTopLevelConversation(ctx context.Context, turn *runtimerequestctx.TurnMeta, msg *apiconv.MutableMessage, elic *execution.Elicitation) error {
	if s == nil || s.client == nil || turn == nil || msg == nil {
		return nil
	}
	root := s.getTopLevelConversation(ctx, turn.ConversationID)
	if root == nil || strings.TrimSpace(root.Id) == "" || root.Id == turn.ConversationID {
		return nil
	}
	rootTurnID := ""
	if root.LastTurnId != nil {
		rootTurnID = strings.TrimSpace(*root.LastTurnId)
	}

	rootConversationMessage := *msg
	rootConversationMessage.SetId(uuid.New().String())
	rootConversationMessage.SetConversationID(root.Id)
	rootConversationMessage.SetLinkedConversationID(turn.ConversationID)
	rootConversationMessage.SetParentMessageID("")
	if root.LastTurnId != nil {
		rootConversationMessage.SetTurnID(*root.LastTurnId)
	}
	rootConversationMessage.Sequence = nil
	if err := s.client.PatchMessage(ctx, &rootConversationMessage); err != nil {
		return fmt.Errorf("failed to proxy elicitation to top-level conversation: %w", err)
	}
	if elic != nil {
		rootTurn := &runtimerequestctx.TurnMeta{
			ConversationID: root.Id,
			TurnID:         rootTurnID,
			ParentMessageID: func() string {
				if rootConversationMessage.ParentMessageID == nil {
					return ""
				}
				return strings.TrimSpace(*rootConversationMessage.ParentMessageID)
			}(),
		}
		s.emitElicitationRequested(ctx, rootTurn, elic, rootConversationMessage.Id)
	}
	return nil
}

func (s *Service) getTopLevelConversation(ctx context.Context, conversationID string) *apiconv.Conversation {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" || s == nil || s.client == nil {
		return nil
	}
	var top *apiconv.Conversation
	seen := map[string]bool{}
	for conversationID != "" {
		if seen[conversationID] {
			return top
		}
		seen[conversationID] = true
		conv, err := s.client.GetConversation(ctx, conversationID)
		if err != nil || conv == nil {
			return top
		}
		top = conv
		if conv.ConversationParentId == nil {
			return top
		}
		parentID := strings.TrimSpace(*conv.ConversationParentId)
		if parentID == "" {
			return top
		}
		conversationID = parentID
	}
	return top
}

func (s *Service) UpdateStatus(ctx context.Context, convID, elicitationID, action string) error {
	st := elact.ToStatus(action)
	logx.Infof("conversation", "elicitation update status start convo=%q elicitation_id=%q status=%q", strings.TrimSpace(convID), strings.TrimSpace(elicitationID), strings.TrimSpace(st))
	msg, err := s.client.GetMessageByElicitation(ctx, convID, elicitationID)
	if err != nil {
		logx.Errorf("conversation", "elicitation update status get error convo=%q elicitation_id=%q err=%v", strings.TrimSpace(convID), strings.TrimSpace(elicitationID), err)
		return err
	}
	if msg == nil {
		logx.Errorf("conversation", "elicitation update status missing message convo=%q elicitation_id=%q", strings.TrimSpace(convID), strings.TrimSpace(elicitationID))
		return fmt.Errorf("elicitation message not found")
	}
	upd := apiconv.NewMessage()
	upd.SetId(msg.Id)
	upd.SetStatus(st)
	if err := s.client.PatchMessage(ctx, upd); err != nil {
		logx.Errorf("conversation", "elicitation update status patch error convo=%q elicitation_id=%q err=%v", strings.TrimSpace(convID), strings.TrimSpace(elicitationID), err)
		return err
	}
	logx.Infof("conversation", "elicitation update status ok convo=%q elicitation_id=%q status=%q", strings.TrimSpace(convID), strings.TrimSpace(elicitationID), strings.TrimSpace(st))
	return nil
}

func (s *Service) resolveElicitationTarget(ctx context.Context, convID, elicitationID string) (*elicitationResolutionTarget, error) {
	msg, err := s.client.GetMessageByElicitation(ctx, convID, elicitationID)
	if err != nil {
		return nil, err
	}
	if msg == nil {
		return nil, fmt.Errorf("elicitation message not found")
	}
	target := &elicitationResolutionTarget{submitted: msg, authoritative: msg}
	if msg.LinkedConversationId != nil {
		linkedConversationID := strings.TrimSpace(*msg.LinkedConversationId)
		if linkedConversationID != "" && linkedConversationID != strings.TrimSpace(msg.ConversationId) {
			child, err := s.client.GetMessageByElicitation(ctx, linkedConversationID, elicitationID)
			if err != nil {
				return nil, err
			}
			if child != nil && strings.TrimSpace(child.ConversationId) != "" && child.ConversationId != msg.ConversationId {
				target.authoritative = child
				target.proxy = msg
				return target, nil
			}
		}
	}
	if msg.ParentMessageId != nil && strings.TrimSpace(*msg.ParentMessageId) != "" {
		parentMessageID := strings.TrimSpace(*msg.ParentMessageId)
		parent, err := s.client.GetMessage(ctx, parentMessageID)
		if err != nil {
			return nil, err
		}
		if isMatchingProxyMessage(parent, elicitationID, msg.ConversationId) {
			target.proxy = parent
			return target, nil
		}
	}
	if getter, ok := s.client.(linkedElicitationProxyGetter); ok {
		proxy, err := getter.GetMessageByLinkedConversationAndElicitation(ctx, msg.ConversationId, elicitationID)
		if err != nil {
			return nil, err
		}
		if isMatchingProxyMessage(proxy, elicitationID, msg.ConversationId) {
			target.proxy = proxy
			return target, nil
		}
	}
	getter, ok := s.client.(parentElicitationMessageGetter)
	if !ok {
		return target, nil
	}
	child, err := getter.GetMessageByParentAndElicitation(ctx, msg.Id, elicitationID)
	if err != nil {
		return nil, err
	}
	if child == nil || strings.TrimSpace(child.ConversationId) == "" || child.ConversationId == msg.ConversationId {
		return target, nil
	}
	target.authoritative = child
	target.proxy = msg
	return target, nil
}

func isMatchingProxyMessage(msg *apiconv.Message, elicitationID, childConversationID string) bool {
	if msg == nil || msg.ElicitationId == nil {
		return false
	}
	if strings.TrimSpace(*msg.ElicitationId) != strings.TrimSpace(elicitationID) {
		return false
	}
	return strings.TrimSpace(msg.ConversationId) != "" && strings.TrimSpace(msg.ConversationId) != strings.TrimSpace(childConversationID)
}

func (s *Service) deleteResolvedProxy(ctx context.Context, target *elicitationResolutionTarget) {
	if s == nil || target == nil || target.proxy == nil || target.authoritative == nil {
		return
	}
	proxyConversationID := strings.TrimSpace(target.proxy.ConversationId)
	proxyMessageID := strings.TrimSpace(target.proxy.Id)
	if proxyConversationID == "" || proxyMessageID == "" {
		return
	}
	if proxyConversationID == strings.TrimSpace(target.authoritative.ConversationId) && proxyMessageID == strings.TrimSpace(target.authoritative.Id) {
		return
	}
	if err := s.client.DeleteMessage(ctx, proxyConversationID, proxyMessageID); err != nil {
		return
	}
	s.clearResolvedProxyWaitState(ctx, target.proxy)
}

func (s *Service) clearResolvedProxyWaitState(ctx context.Context, proxy *apiconv.Message) {
	if s == nil || s.client == nil || proxy == nil {
		return
	}
	proxyConversationID := strings.TrimSpace(proxy.ConversationId)
	if proxyConversationID == "" {
		return
	}
	proxyTurnID := ""
	if proxy.TurnId != nil {
		proxyTurnID = strings.TrimSpace(*proxy.TurnId)
	}
	if proxyTurnID == "" {
		return
	}
	conv, err := s.client.GetConversation(ctx, proxyConversationID, apiconv.WithIncludeTranscript(true))
	if err != nil || conv == nil {
		return
	}
	var turnStatus string
	var hasPending bool
	for _, turn := range conv.GetTranscript() {
		if turn == nil || strings.TrimSpace(turn.Id) != proxyTurnID {
			continue
		}
		turnStatus = strings.TrimSpace(turn.Status)
		for _, msg := range turn.GetMessages() {
			if msg == nil || strings.TrimSpace(msg.Id) == strings.TrimSpace(proxy.Id) {
				continue
			}
			if statusAwaitingUser(stringValue(msg.Status)) {
				hasPending = true
				break
			}
		}
		break
	}
	if hasPending {
		return
	}
	conversationStatus := stringValue(conv.Status)
	if !strings.EqualFold(strings.TrimSpace(turnStatus), "waiting_for_user") && !strings.EqualFold(strings.TrimSpace(conversationStatus), "waiting_for_user") {
		return
	}
	turn := apiconv.NewTurn()
	turn.SetId(proxyTurnID)
	turn.SetConversationID(proxyConversationID)
	turn.SetStatus("succeeded")
	if err := s.client.PatchTurn(ctx, turn); err != nil {
		return
	}
	conversation := apiconv.NewConversation()
	conversation.SetId(proxyConversationID)
	conversation.SetStatus("succeeded")
	if err := s.client.PatchConversations(ctx, conversation); err != nil {
		return
	}
}

func statusAwaitingUser(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "pending", "waiting_for_user":
		return true
	default:
		return false
	}
}

func (s *Service) existingElicitationResponse(ctx context.Context, convID, elicitationID string) (*apiconv.Message, error) {
	convID = strings.TrimSpace(convID)
	elicitationID = strings.TrimSpace(elicitationID)
	if convID == "" || elicitationID == "" || s == nil || s.client == nil {
		return nil, nil
	}
	if getter, ok := s.client.(elicitationResponseMessageGetter); ok {
		return getter.GetElicitationResponseMessage(ctx, convID, elicitationID)
	}
	conv, err := s.client.GetConversation(ctx, convID, apiconv.WithIncludeTranscript(true))
	if err != nil || conv == nil {
		return nil, err
	}
	var latest *apiconv.Message
	for _, turn := range conv.GetTranscript() {
		if turn == nil {
			continue
		}
		for _, msg := range turn.GetMessages() {
			if !isElicitationResponseMessage(msg, elicitationID) {
				continue
			}
			if latest == nil || msg.CreatedAt.After(latest.CreatedAt) {
				latest = msg
			}
		}
	}
	return latest, nil
}

func isElicitationResponseMessage(msg *apiconv.Message, elicitationID string) bool {
	if msg == nil || msg.ElicitationId == nil {
		return false
	}
	if strings.TrimSpace(*msg.ElicitationId) != strings.TrimSpace(elicitationID) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(msg.Role), llm.RoleUser.String()) &&
		strings.EqualFold(strings.TrimSpace(msg.Type), "elicitation_response")
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Service) StorePayload(ctx context.Context, convID, elicitationID string, payload map[string]interface{}) error {
	msg, err := s.client.GetMessageByElicitation(ctx, convID, elicitationID)
	if err != nil {
		return err
	}
	if msg == nil {
		return fmt.Errorf("elicitation message not found")
	}
	raw, _ := json.Marshal(payload)
	logx.Infof("conversation", "[elicitation] store conv=%s id=%s payload=%s", convID, elicitationID, string(raw))
	logx.Infof("conversation", "elicitation store payload convo=%q elicitation_id=%q payload_len=%d payload_head=%q payload_tail=%q", strings.TrimSpace(convID), strings.TrimSpace(elicitationID), len(raw), textutil.Head(string(raw), 512), textutil.Tail(string(raw), 512))
	pid := uuid.New().String()
	p := apiconv.NewPayload()
	p.SetId(pid)
	p.SetKind("elicitation_response")
	p.SetMimeType("application/json")
	p.SetSizeBytes(len(raw))
	p.SetStorage("inline")
	p.SetInlineBody(raw)
	if err := s.client.PatchPayload(ctx, p); err != nil {
		return err
	}
	upd := apiconv.NewMessage()
	upd.SetId(msg.Id)
	if msg.Role == llm.RoleAssistant.String() {
		if loaded, ok := s.loadRecordedElicitation(ctx, msg); ok {
			payload = enrichApprovalPayload(payload, &loaded)
		}
		turn := runtimerequestctx.TurnMeta{TurnID: stringValue(msg.TurnId), ConversationID: msg.ConversationId, ParentMessageID: strings.TrimSpace(msg.Id)}
		if err := s.AddUserResponseMessage(ctx, &turn, elicitationID, payload, pid); err != nil {
			return err
		}
		return nil
	}
	upd.SetElicitationPayloadID(pid)
	return s.client.PatchMessage(ctx, upd)
}

func (s *Service) AddUserResponseMessage(ctx context.Context, turn *runtimerequestctx.TurnMeta, elicitationID string, payload map[string]interface{}, payloadID ...string) error {
	raw, _ := json.Marshal(payload)
	options := []apiconv.MessageOption{
		apiconv.WithId(uuid.New().String()),
		apiconv.WithRole("user"),
		apiconv.WithType("elicitation_response"),
		apiconv.WithElicitationID(elicitationID),
		apiconv.WithContent(string(raw)),
		apiconv.WithRawContent(string(raw)),
	}
	if len(payloadID) > 0 && strings.TrimSpace(payloadID[0]) != "" {
		options = append(options, apiconv.WithElicitationPayloadID(strings.TrimSpace(payloadID[0])))
	}
	_, err := apiconv.AddMessage(ctx, s.client, turn, options...)
	return err
}

func enrichApprovalPayload(payload map[string]interface{}, req *execution.Elicitation) map[string]interface{} {
	if len(payload) == 0 || req == nil {
		return payload
	}
	properties := req.RequestedSchema.Properties
	if len(properties) == 0 {
		return payload
	}
	rawMeta, ok := properties["_approvalMeta"].(map[string]interface{})
	if !ok {
		return payload
	}
	constValue, _ := rawMeta["const"].(string)
	constValue = strings.TrimSpace(constValue)
	if constValue == "" {
		return payload
	}
	var meta toolapproval.View
	if err := json.Unmarshal([]byte(constValue), &meta); err != nil {
		return payload
	}
	editedFields, ok := payload["editedFields"].(map[string]interface{})
	if !ok || len(editedFields) == 0 {
		return payload
	}
	fields := map[string]interface{}{}
	partial := false
	for _, editor := range meta.Editors {
		if editor == nil || strings.TrimSpace(editor.Name) == "" {
			continue
		}
		selectedRaw, ok := editedFields[editor.Name]
		if !ok {
			continue
		}
		selectedIDs := normalizeApprovalSelectionSet(selectedRaw)
		if len(selectedIDs) == 0 && len(editor.Options) == 0 {
			continue
		}
		selected := make([]string, 0, len(selectedIDs))
		denied := make([]string, 0)
		for _, option := range editor.Options {
			if option == nil || strings.TrimSpace(option.ID) == "" {
				continue
			}
			if _, ok := selectedIDs[option.ID]; ok {
				selected = append(selected, option.ID)
				continue
			}
			denied = append(denied, option.ID)
		}
		if len(denied) > 0 {
			partial = true
		}
		fields[editor.Name] = map[string]interface{}{
			"approved": selected,
			"denied":   denied,
		}
	}
	if len(fields) == 0 {
		return payload
	}
	enriched := map[string]interface{}{}
	for key, value := range payload {
		enriched[key] = value
	}
	enriched["approvalDecision"] = map[string]interface{}{
		"type":        "tool_approval",
		"toolName":    strings.TrimSpace(meta.ToolName),
		"title":       strings.TrimSpace(meta.Title),
		"isPartial":   partial,
		"fields":      fields,
		"instruction": "Only the approved selection was allowed by the user. Do not request denied items again in this turn.",
	}
	return enriched
}

func normalizeApprovalSelectionSet(raw interface{}) map[string]struct{} {
	result := map[string]struct{}{}
	switch actual := raw.(type) {
	case []interface{}:
		for _, item := range actual {
			key := strings.TrimSpace(fmt.Sprintf("%v", item))
			if key != "" {
				result[key] = struct{}{}
			}
		}
	case []string:
		for _, item := range actual {
			key := strings.TrimSpace(item)
			if key != "" {
				result[key] = struct{}{}
			}
		}
	default:
		key := strings.TrimSpace(fmt.Sprintf("%v", actual))
		if key != "" {
			result[key] = struct{}{}
		}
	}
	return result
}

// NormalizeAction is kept for backward compatibility; use action.Normalize.
func NormalizeAction(a string) string { return elact.Normalize(a) }

// HandleCallback processes an elicitation decision end-to-end:
// - normalizes the action
// - updates message status
// - stores payload (when accepted)
// - notifies any registered router waiter
func (s *Service) HandleCallback(ctx context.Context, convID, elicitationID, action string, payload map[string]interface{}) error {
	// Deprecated: prefer Resolve
	return s.Resolve(ctx, convID, elicitationID, action, payload, "")
}

// Resolve processes an elicitation decision end-to-end:
// - normalizes the action
// - updates message status
// - stores payload (when accepted)
// - notifies any registered router waiter
func (s *Service) Resolve(ctx context.Context, convID, elicitationID, action string, payload map[string]interface{}, reason string) error {
	if strings.TrimSpace(convID) == "" || strings.TrimSpace(elicitationID) == "" {
		return fmt.Errorf("conversation and elicitation id required")
	}
	act := elact.Normalize(action)
	status := elact.ToStatus(act)
	logx.Infof("conversation", "[elicitation] resolve conv=%s id=%s action=%s payloadKeys=%v", convID, elicitationID, act, PayloadKeys(payload))
	logx.Infof("conversation", "elicitation resolve convo=%q elicitation_id=%q action=%q payload_keys=%v", strings.TrimSpace(convID), strings.TrimSpace(elicitationID), strings.TrimSpace(act), PayloadKeys(payload))
	target, err := s.resolveElicitationTarget(ctx, strings.TrimSpace(convID), strings.TrimSpace(elicitationID))
	if err != nil {
		return err
	}
	authoritativeConvID := strings.TrimSpace(target.authoritative.ConversationId)
	if authoritativeConvID == "" {
		authoritativeConvID = strings.TrimSpace(convID)
	}
	proxyConvID := ""
	if target.proxy != nil {
		proxyConvID = strings.TrimSpace(target.proxy.ConversationId)
	}
	if existing, err := s.existingElicitationResponse(ctx, authoritativeConvID, elicitationID); err != nil {
		return err
	} else if existing != nil {
		logx.Infof("conversation", "elicitation resolve idempotent existing response convo=%q elicitation_id=%q message_id=%q payload_id=%q", authoritativeConvID, strings.TrimSpace(elicitationID), strings.TrimSpace(existing.Id), stringValue(existing.ElicitationPayloadId))
		return nil
	}
	if err := s.UpdateStatus(ctx, authoritativeConvID, elicitationID, act); err != nil {
		return err
	}
	if status == elact.StatusAccepted && payload != nil {
		if err := s.StorePayload(ctx, authoritativeConvID, elicitationID, payload); err != nil {
			return err
		}
	} else if status == elact.StatusRejected && strings.TrimSpace(reason) != "" {
		if err := s.StoreDeclineReason(ctx, authoritativeConvID, elicitationID, reason); err != nil {
			return err
		}
	} else if status == elact.StatusCancel && strings.TrimSpace(reason) != "" {
		if err := s.StoreCancelReason(ctx, authoritativeConvID, elicitationID, reason); err != nil {
			return err
		}
	}
	s.emitElicitationResolved(ctx, authoritativeConvID, elicitationID, status, payload)
	if proxyConvID != "" && proxyConvID != authoritativeConvID {
		s.emitElicitationResolved(ctx, proxyConvID, elicitationID, status, payload)
	}
	s.deleteResolvedProxy(ctx, target)
	out := &schema.ElicitResult{Action: schema.ElicitResultAction(act), Content: payload}
	s.router.AcceptByElicitation(authoritativeConvID, elicitationID, out)
	return nil
}

// StoreDeclineReason persists a user-decline reason as a user message so the agent can react.
func (s *Service) StoreDeclineReason(ctx context.Context, convID, elicitationID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return nil
	}
	msg, err := s.client.GetMessageByElicitation(ctx, convID, elicitationID)
	if err != nil {
		return err
	}
	if msg == nil {
		return fmt.Errorf("elicitation message not found")
	}
	// Only add a user response message when the elicitation originated from an assistant message
	if msg.Role != llm.RoleAssistant.String() {
		return nil
	}
	turn := runtimerequestctx.TurnMeta{TurnID: stringValue(msg.TurnId), ConversationID: msg.ConversationId, ParentMessageID: strings.TrimSpace(msg.Id)}
	payload := map[string]interface{}{"declineReason": reason}
	return s.AddUserResponseMessage(ctx, &turn, elicitationID, payload)
}

// StoreCancelReason persists a user-cancel reason as a user message so the agent can react.
func (s *Service) StoreCancelReason(ctx context.Context, convID, elicitationID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return nil
	}
	msg, err := s.client.GetMessageByElicitation(ctx, convID, elicitationID)
	if err != nil {
		return err
	}
	if msg == nil {
		return fmt.Errorf("elicitation message not found")
	}
	if msg.Role != llm.RoleAssistant.String() {
		return nil
	}
	turn := runtimerequestctx.TurnMeta{TurnID: stringValue(msg.TurnId), ConversationID: msg.ConversationId, ParentMessageID: strings.TrimSpace(msg.Id)}
	payload := map[string]interface{}{
		"cancelReason": reason,
		"message":      "User did not respond before the elicitation timeout.",
	}
	return s.AddUserResponseMessage(ctx, &turn, elicitationID, payload)
}
