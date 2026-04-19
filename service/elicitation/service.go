package elicitation

// moved from genai/service/elicitation

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/agently-core/internal/textutil"
	"strings"
	"time"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/protocol/agent/plan"
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

func (s *Service) emitElicitationRequested(ctx context.Context, turn *runtimerequestctx.TurnMeta, elic *plan.Elicitation, messageID string) {
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
func (s *Service) Record(ctx context.Context, turn *runtimerequestctx.TurnMeta, role string, elic *plan.Elicitation) (*apiconv.MutableMessage, error) {
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
			var req plan.Elicitation
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

func (s *Service) storeElicitationRequestPayload(ctx context.Context, elic *plan.Elicitation) (string, error) {
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

func (s *Service) loadRecordedElicitation(ctx context.Context, msg *apiconv.Message) (plan.Elicitation, bool) {
	if msg == nil || msg.ElicitationPayloadId == nil || strings.TrimSpace(*msg.ElicitationPayloadId) == "" {
		return plan.Elicitation{}, false
	}
	payload, err := s.client.GetPayload(ctx, strings.TrimSpace(*msg.ElicitationPayloadId))
	if err != nil || payload == nil || payload.InlineBody == nil || len(*payload.InlineBody) == 0 {
		return plan.Elicitation{}, false
	}
	var req plan.Elicitation
	if err = json.Unmarshal(*payload.InlineBody, &req); err != nil {
		return plan.Elicitation{}, false
	}
	return req, true
}

// Elicit records a new elicitation control message and waits for a resolution via router/UI.
// Returns message id, normalized status (accepted/rejected/cancel) and optional payload.
func (s *Service) Elicit(ctx context.Context, turn *runtimerequestctx.TurnMeta, role string, req *plan.Elicitation) (string, string, map[string]interface{}, error) {
	if req == nil || turn == nil {
		return "", "", nil, fmt.Errorf("invalid input")
	}

	msg, err := s.Record(ctx, turn, role, req)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to record message: %w", err)
	}
	logx.Infof("conversation", "elicitation Elicit start convo=%q elicitation_id=%q message_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(req.ElicitationId), strings.TrimSpace(msg.Id))
	root := s.getRootConversation(ctx, turn.ConversationID)
	// Only duplicate into a different conversation. If getRootConversation returns
	// the same conversation (e.g. when at the root or due to lookup quirks), skip.
	if root != nil && strings.TrimSpace(root.Id) != "" && root.Id != turn.ConversationID {
		rootConversationMessage := *msg
		rootConversationMessage.SetId(uuid.New().String())
		if root.LastTurnId != nil {
			rootConversationMessage.SetTurnID(*root.LastTurnId)
			rootConversationMessage.SetConversationID(root.Id)
		}
		rootConversationMessage.Sequence = nil
		if err := s.client.PatchMessage(ctx, &rootConversationMessage); err != nil {
			return "", "", nil, fmt.Errorf("failed to root record message: %w", err)
		}

		// should be (simpler but the same) msg.SetParentMessageID(rootConversationMessage.Id)
		// 	_ = s.client.PatchMessage(ctx, cloneMsg)
		cloneMsg := apiconv.NewMessage()
		cloneMsg.SetId(msg.Id)
		cloneMsg.SetParentMessageID(rootConversationMessage.Id) //parent id will not exist after paranet_id msg removal in UpdateStatus
		_ = s.client.PatchMessage(ctx, cloneMsg)
	}

	status, payload, err := s.Wait(ctx, turn.ConversationID, req.ElicitationId)
	if err != nil {
		logx.Errorf("conversation", "elicitation Elicit error convo=%q elicitation_id=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(req.ElicitationId), err)
		return msg.Id, "", nil, err
	}
	logx.Infof("conversation", "elicitation Elicit done convo=%q elicitation_id=%q status=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(req.ElicitationId), strings.TrimSpace(status))
	return msg.Id, status, payload, nil
}

func (s *Service) getRootConversation(ctx context.Context, conversationId string) *apiconv.Conversation {
	var conv *apiconv.Conversation
	if parent, err := s.client.GetConversation(ctx, conversationId); err == nil && parent != nil {
		if parent.ConversationParentId != nil {
			conv = parent
			if ret := s.getRootConversation(ctx, *conv.ConversationParentId); ret != nil {
				return ret
			}
		}
	}
	return conv
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

	// delete duplicate elicitation msg in root conversation if any (current conversation is a child)
	root := s.getRootConversation(ctx, convID)
	if root != nil && strings.TrimSpace(root.Id) != "" && root.Id != convID && msg.ParentMessageId != nil {
		if dep, err := s.client.GetMessage(ctx, *msg.ParentMessageId); err == nil && dep != nil && dep.ConversationId == root.Id /* double check */ {
			return s.client.DeleteMessage(ctx, dep.ConversationId, dep.Id)
		}
	}
	logx.Infof("conversation", "elicitation update status ok convo=%q elicitation_id=%q status=%q", strings.TrimSpace(convID), strings.TrimSpace(elicitationID), strings.TrimSpace(st))
	return nil
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
		turn := runtimerequestctx.TurnMeta{TurnID: *msg.TurnId, ConversationID: msg.ConversationId, ParentMessageID: *msg.ParentMessageId}
		if err := s.AddUserResponseMessage(ctx, &turn, elicitationID, payload); err != nil {
			return err
		}
		return nil
	}
	upd.SetElicitationPayloadID(pid)
	return s.client.PatchMessage(ctx, upd)
}

func (s *Service) AddUserResponseMessage(ctx context.Context, turn *runtimerequestctx.TurnMeta, elicitationID string, payload map[string]interface{}) error {
	raw, _ := json.Marshal(payload)
	_, err := apiconv.AddMessage(ctx, s.client, turn,
		apiconv.WithId(uuid.New().String()),
		apiconv.WithRole("user"),
		apiconv.WithType("elicitation_response"),
		apiconv.WithElicitationID(elicitationID),
		apiconv.WithContent(string(raw)),
		apiconv.WithRawContent(string(raw)),
	)
	return err
}

func enrichApprovalPayload(payload map[string]interface{}, req *plan.Elicitation) map[string]interface{} {
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
	logx.Infof("conversation", "[elicitation] resolve conv=%s id=%s action=%s payloadKeys=%v", convID, elicitationID, act, PayloadKeys(payload))
	logx.Infof("conversation", "elicitation resolve convo=%q elicitation_id=%q action=%q payload_keys=%v", strings.TrimSpace(convID), strings.TrimSpace(elicitationID), strings.TrimSpace(act), PayloadKeys(payload))
	// No logging; caller/UI can inspect status via DAO and router.
	if err := s.UpdateStatus(ctx, convID, elicitationID, act); err != nil {
		return err
	}
	if elact.ToStatus(act) == elact.StatusAccepted && payload != nil {
		if err := s.StorePayload(ctx, convID, elicitationID, payload); err != nil {
			return err
		}
	} else if elact.ToStatus(act) == elact.StatusRejected && strings.TrimSpace(reason) != "" {
		if err := s.StoreDeclineReason(ctx, convID, elicitationID, reason); err != nil {
			return err
		}
	} else if elact.ToStatus(act) == elact.StatusCancel && strings.TrimSpace(reason) != "" {
		if err := s.StoreCancelReason(ctx, convID, elicitationID, reason); err != nil {
			return err
		}
	}
	s.emitElicitationResolved(ctx, convID, elicitationID, elact.ToStatus(act), payload)
	out := &schema.ElicitResult{Action: schema.ElicitResultAction(act), Content: payload}
	s.router.AcceptByElicitation(convID, elicitationID, out)
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
	turn := runtimerequestctx.TurnMeta{TurnID: *msg.TurnId, ConversationID: msg.ConversationId, ParentMessageID: *msg.ParentMessageId}
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
	turn := runtimerequestctx.TurnMeta{TurnID: *msg.TurnId, ConversationID: msg.ConversationId, ParentMessageID: *msg.ParentMessageId}
	payload := map[string]interface{}{
		"cancelReason": reason,
		"message":      "User did not respond before the elicitation timeout.",
	}
	return s.AddUserResponseMessage(ctx, &turn, elicitationID, payload)
}
