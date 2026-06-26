package elicitation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	"github.com/viant/agently-core/protocol/agent/execution"
	memory "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/service/elicitation/router"
	mcpproto "github.com/viant/mcp-protocol/schema"
)

type patchCall struct {
	id             string
	conversationID string
	turnID         string
	seqProvided    bool
	seq            int
}

type seqRecordingConv struct {
	apiconv.Client

	childConversationID string
	conversations       map[string]*apiconv.Conversation

	byID      map[string]*apiconv.Message
	byElic    map[string]*apiconv.Message
	responses map[string]*apiconv.Message
	payloads  map[string]*apiconv.Payload

	patches []patchCall
	deleted []patchCall
	turns   []patchCall
}

func newSeqRecordingConv(childConversationID string) *seqRecordingConv {
	return &seqRecordingConv{
		childConversationID: childConversationID,
		conversations:       map[string]*apiconv.Conversation{},
		byID:                map[string]*apiconv.Message{},
		byElic:              map[string]*apiconv.Message{},
		responses:           map[string]*apiconv.Message{},
		payloads:            map[string]*apiconv.Payload{},
	}
}

type captureEventsPublisher struct {
	events []*streaming.Event
}

func (p *captureEventsPublisher) Publish(_ context.Context, event *streaming.Event) error {
	p.events = append(p.events, event)
	return nil
}

func (f *seqRecordingConv) GetConversation(ctx context.Context, id string, options ...apiconv.Option) (*apiconv.Conversation, error) {
	if c, ok := f.conversations[id]; ok {
		return c, nil
	}
	return &apiconv.Conversation{Id: id}, nil
}

func (f *seqRecordingConv) PatchConversations(ctx context.Context, conversations *apiconv.MutableConversation) error {
	if conversations == nil || conversations.Id == "" {
		return nil
	}
	conv := f.conversations[conversations.Id]
	if conv == nil {
		conv = &apiconv.Conversation{Id: conversations.Id}
		f.conversations[conversations.Id] = conv
	}
	if conversations.Status != nil {
		conv.Status = conversations.Status
	}
	return nil
}

func (f *seqRecordingConv) PatchTurn(ctx context.Context, turn *apiconv.MutableTurn) error {
	if turn == nil {
		return nil
	}
	f.turns = append(f.turns, patchCall{id: turn.Id, conversationID: turn.ConversationID, turnID: turn.Id})
	if turn.ConversationID == "" {
		return nil
	}
	conv := f.conversations[turn.ConversationID]
	if conv == nil {
		conv = &apiconv.Conversation{Id: turn.ConversationID}
		f.conversations[turn.ConversationID] = conv
	}
	for _, existing := range conv.Transcript {
		if existing == nil || existing.Id != turn.Id {
			continue
		}
		if turn.Status != "" {
			existing.Status = turn.Status
		}
		return nil
	}
	conv.Transcript = append(conv.Transcript, &agconv.TranscriptView{Id: turn.Id, Status: turn.Status})
	return nil
}

func (f *seqRecordingConv) PatchPayload(ctx context.Context, payload *apiconv.MutablePayload) error {
	if payload == nil {
		return nil
	}
	p := &apiconv.Payload{
		Id:          payload.Id,
		Kind:        payload.Kind,
		MimeType:    payload.MimeType,
		SizeBytes:   payload.SizeBytes,
		Storage:     payload.Storage,
		InlineBody:  payload.InlineBody,
		Compression: payload.Compression,
	}
	f.payloads[p.Id] = p
	return nil
}

func (f *seqRecordingConv) GetPayload(ctx context.Context, id string) (*apiconv.Payload, error) {
	return f.payloads[id], nil
}

func (f *seqRecordingConv) GetMessageByElicitation(ctx context.Context, conversationID, elicitationID string) (*apiconv.Message, error) {
	return f.byElic[conversationID+"/"+elicitationID], nil
}

func (f *seqRecordingConv) GetElicitationResponseMessage(ctx context.Context, conversationID, elicitationID string) (*apiconv.Message, error) {
	return f.responses[conversationID+"/"+elicitationID], nil
}

func (f *seqRecordingConv) GetMessageByParentAndElicitation(ctx context.Context, parentMessageID, elicitationID string) (*apiconv.Message, error) {
	for _, msg := range f.byID {
		if msg == nil || msg.ParentMessageId == nil || msg.ElicitationId == nil {
			continue
		}
		if *msg.ParentMessageId == parentMessageID && *msg.ElicitationId == elicitationID {
			return msg, nil
		}
	}
	return nil, nil
}

func (f *seqRecordingConv) GetMessageByLinkedConversationAndElicitation(ctx context.Context, linkedConversationID, elicitationID string) (*apiconv.Message, error) {
	for _, msg := range f.byID {
		if msg == nil || msg.LinkedConversationId == nil || msg.ElicitationId == nil {
			continue
		}
		if *msg.LinkedConversationId == linkedConversationID && *msg.ElicitationId == elicitationID && msg.Type != "elicitation_response" {
			return msg, nil
		}
	}
	return nil, nil
}

func (f *seqRecordingConv) GetMessage(ctx context.Context, id string, _ ...apiconv.Option) (*apiconv.Message, error) {
	return f.byID[id], nil
}

func (f *seqRecordingConv) DeleteMessage(ctx context.Context, conversationID, messageID string) error {
	f.deleted = append(f.deleted, patchCall{id: messageID, conversationID: conversationID})
	if msg := f.byID[messageID]; msg != nil && msg.ElicitationId != nil {
		delete(f.byElic, msg.ConversationId+"/"+*msg.ElicitationId)
	}
	delete(f.byID, messageID)
	return nil
}

func (f *seqRecordingConv) PatchMessage(ctx context.Context, m *apiconv.MutableMessage) error {
	if m == nil {
		return nil
	}
	var turnID string
	if m.TurnID != nil {
		turnID = *m.TurnID
	}
	call := patchCall{
		id:             m.Id,
		conversationID: m.ConversationID,
		turnID:         turnID,
		seqProvided:    m.Sequence != nil,
	}
	if m.Sequence != nil {
		call.seq = *m.Sequence
	}
	f.patches = append(f.patches, call)

	// Simulate DB sequence assignment for the initial insert in the child conversation.
	if _, exists := f.byID[m.Id]; !exists {
		if m.ConversationID == f.childConversationID && m.Sequence == nil && m.TurnID != nil && *m.TurnID != "" {
			m.SetSequence(5)
		}
	}

	// Store a minimal read view for later lookups.
	if m.Id != "" {
		mv := f.byID[m.Id]
		if mv == nil {
			mv = &apiconv.Message{Id: m.Id}
			f.byID[m.Id] = mv
		}
		if m.ConversationID != "" {
			mv.ConversationId = m.ConversationID
		}
		if m.Role != "" {
			mv.Role = m.Role
		}
		if m.Type != "" {
			mv.Type = m.Type
		}
		if m.Content != nil {
			cpy := *m.Content
			mv.Content = &cpy
		}
		if m.TurnID != nil {
			mv.TurnId = m.TurnID
		}
		if m.ParentMessageID != nil {
			mv.ParentMessageId = m.ParentMessageID
		}
		if m.LinkedConversationID != nil {
			mv.LinkedConversationId = m.LinkedConversationID
		}
		if m.ElicitationID != nil {
			mv.ElicitationId = m.ElicitationID
		}
		if m.ElicitationPayloadID != nil {
			mv.ElicitationPayloadId = m.ElicitationPayloadID
		}
		if m.Status != nil {
			mv.Status = m.Status
		}
		if m.Sequence != nil {
			mv.Sequence = m.Sequence
		}
		if mv.ElicitationId != nil && *mv.ElicitationId != "" && mv.ConversationId != "" && mv.Type == "elicitation_response" && mv.Role == "user" {
			f.responses[mv.ConversationId+"/"+*mv.ElicitationId] = mv
		}
		if mv.ElicitationId != nil && *mv.ElicitationId != "" && mv.ConversationId != "" && mv.Type != "elicitation_response" {
			f.byElic[mv.ConversationId+"/"+*mv.ElicitationId] = mv
		}
	}
	return nil
}

func TestElicit_RootDuplicateDoesNotReuseSequence(t *testing.T) {
	childID := "conv-child"
	parentID := "conv-parent"
	rootID := "conv-root"
	childTurnID := "turn-child"
	rootTurnID := "turn-root"

	fake := newSeqRecordingConv(childID)
	fake.conversations[childID] = &apiconv.Conversation{Id: childID, ConversationParentId: &parentID}
	fake.conversations[parentID] = &apiconv.Conversation{Id: parentID, ConversationParentId: &rootID}
	fake.conversations[rootID] = &apiconv.Conversation{Id: rootID, LastTurnId: &rootTurnID}

	r := router.New()
	srv := New(fake, nil, r, func() Awaiter { return acceptNoPayloadAwaiter{} })

	turn := &memory.TurnMeta{ConversationID: childID, TurnID: childTurnID}
	_, _, _, err := srv.Elicit(context.Background(), turn, "assistant", &execution.Elicitation{})
	assert.NoError(t, err)

	found := false
	rootPatchCount := 0
	for _, call := range fake.patches {
		if call.conversationID == rootID && call.turnID == rootTurnID && call.id != "" {
			rootPatchCount++
			found = true
			assert.False(t, call.seqProvided, "root-duplicated message must not provide sequence (turn_id, sequence is unique)")
		}
	}
	assert.True(t, found, "expected an additional message to be patched into the top-level conversation")
	assert.Equal(t, 1, rootPatchCount, "Elicit should use the shared Record proxy and not create a second duplicate")
}

func TestRecord_ProxiesToTopLevelConversation(t *testing.T) {
	childID := "conv-child"
	parentID := "conv-parent"
	rootID := "conv-root"
	childTurnID := "turn-child"
	rootTurnID := "turn-root"

	fake := newSeqRecordingConv(childID)
	fake.conversations[childID] = &apiconv.Conversation{Id: childID, ConversationParentId: &parentID}
	fake.conversations[parentID] = &apiconv.Conversation{Id: parentID, ConversationParentId: &rootID}
	fake.conversations[rootID] = &apiconv.Conversation{Id: rootID, LastTurnId: &rootTurnID}

	srv := New(fake, nil, router.New(), func() Awaiter { return acceptNoPayloadAwaiter{} })

	childParentMessageID := "child-parent-message"
	turn := &memory.TurnMeta{ConversationID: childID, TurnID: childTurnID, ParentMessageID: childParentMessageID}
	recorded, err := srv.Record(context.Background(), turn, "assistant", &execution.Elicitation{})
	assert.NoError(t, err)

	found := false
	var proxy *apiconv.Message
	for _, call := range fake.patches {
		if call.conversationID == rootID && call.turnID == rootTurnID && call.id != "" {
			found = true
			assert.False(t, call.seqProvided, "root-duplicated message must not provide sequence (turn_id, sequence is unique)")
			proxy = fake.byID[call.id]
		}
	}
	assert.True(t, found, "expected Record to proxy elicitation into the top-level conversation")
	child := fake.byID[recorded.Id]
	if assert.NotNil(t, child) && assert.NotNil(t, child.ParentMessageId) {
		assert.Equal(t, childParentMessageID, *child.ParentMessageId)
	}
	if assert.NotNil(t, proxy) {
		if assert.NotNil(t, proxy.LinkedConversationId) {
			assert.Equal(t, childID, *proxy.LinkedConversationId)
		}
		if assert.NotNil(t, proxy.ParentMessageId) {
			assert.Equal(t, "", *proxy.ParentMessageId)
		}
	}
}

func TestRecord_ProxiesElicitationEventToTopLevelConversation(t *testing.T) {
	childID := "conv-child"
	rootID := "conv-root"
	childTurnID := "turn-child"
	rootTurnID := "turn-root"

	fake := newSeqRecordingConv(childID)
	fake.conversations[childID] = &apiconv.Conversation{Id: childID, ConversationParentId: &rootID}
	fake.conversations[rootID] = &apiconv.Conversation{Id: rootID, LastTurnId: &rootTurnID}

	publisher := &captureEventsPublisher{}
	srv := New(fake, nil, router.New(), func() Awaiter { return acceptNoPayloadAwaiter{} })
	srv.SetStreamPublisher(publisher)

	turn := &memory.TurnMeta{ConversationID: childID, TurnID: childTurnID}
	_, err := srv.Record(context.Background(), turn, "assistant", &execution.Elicitation{ElicitRequestParams: mcpproto.ElicitRequestParams{
		Message: "Need input",
	}})
	assert.NoError(t, err)

	assert.Len(t, publisher.events, 2, "expected root proxy event and authoritative child event")
	assert.Equal(t, rootID, publisher.events[0].ConversationID)
	assert.Equal(t, rootID, publisher.events[0].StreamID)
	assert.Equal(t, rootTurnID, publisher.events[0].TurnID)
	assert.Equal(t, streaming.EventTypeElicitationRequested, publisher.events[0].Type)
	assert.Equal(t, childID, publisher.events[1].ConversationID)
	assert.Equal(t, childID, publisher.events[1].StreamID)
	assert.Equal(t, childTurnID, publisher.events[1].TurnID)
	assert.Equal(t, publisher.events[0].ElicitationID, publisher.events[1].ElicitationID)
}

func TestResolve_ChildSubmissionClearsRootProxyAndEmitsBothStreams(t *testing.T) {
	childID := "conv-child"
	rootID := "conv-root"
	childTurnID := "turn-child"
	rootTurnID := "turn-root"

	fake := newSeqRecordingConv(childID)
	fake.conversations[childID] = &apiconv.Conversation{Id: childID, ConversationParentId: &rootID}
	fake.conversations[rootID] = &apiconv.Conversation{Id: rootID, LastTurnId: &rootTurnID}

	publisher := &captureEventsPublisher{}
	srv := New(fake, nil, router.New(), nil)
	srv.SetStreamPublisher(publisher)

	turn := &memory.TurnMeta{ConversationID: childID, TurnID: childTurnID}
	recorded, err := srv.Record(context.Background(), turn, "assistant", &execution.Elicitation{ElicitRequestParams: mcpproto.ElicitRequestParams{
		Message: "Need input",
	}})
	assert.NoError(t, err)
	elicID := *recorded.ElicitationID

	err = srv.Resolve(context.Background(), childID, elicID, "accept", map[string]interface{}{"answer": "ok"}, "")
	assert.NoError(t, err)

	child := fake.byID[recorded.Id]
	if assert.NotNil(t, child) && assert.NotNil(t, child.Status) {
		assert.Equal(t, "accepted", *child.Status)
	}
	response := fake.responses[childID+"/"+elicID]
	if assert.NotNil(t, response) && assert.NotNil(t, response.ParentMessageId) {
		assert.Equal(t, recorded.Id, *response.ParentMessageId)
	}
	assertProxyDeleted(t, fake, rootID)
	assertResolvedEvent(t, publisher.events, childID, elicID)
	assertResolvedEvent(t, publisher.events, rootID, elicID)
}

func TestResolve_AcceptRetryDoesNotCreateDuplicateResponsePayload(t *testing.T) {
	childID := "conv-child"
	childTurnID := "turn-child"

	fake := newSeqRecordingConv(childID)
	fake.conversations[childID] = &apiconv.Conversation{Id: childID}

	srv := New(fake, nil, router.New(), nil)
	turn := &memory.TurnMeta{ConversationID: childID, TurnID: childTurnID}
	recorded, err := srv.Record(context.Background(), turn, "assistant", &execution.Elicitation{ElicitRequestParams: mcpproto.ElicitRequestParams{
		Message: "Need input",
	}})
	assert.NoError(t, err)
	elicID := *recorded.ElicitationID

	err = srv.Resolve(context.Background(), childID, elicID, "accept", map[string]interface{}{"answer": "ok"}, "")
	assert.NoError(t, err)
	assert.Len(t, fake.responses, 1)
	assert.Len(t, fake.payloads, 2, "expected request payload and one response payload")

	firstResponse := fake.responses[childID+"/"+elicID]
	if !assert.NotNil(t, firstResponse) || !assert.NotNil(t, firstResponse.ElicitationPayloadId) {
		return
	}
	firstPayloadID := *firstResponse.ElicitationPayloadId

	err = srv.Resolve(context.Background(), childID, elicID, "accept", map[string]interface{}{"answer": "ok"}, "")
	assert.NoError(t, err)
	assert.Len(t, fake.responses, 1)
	assert.Len(t, fake.payloads, 2, "retry must not create another response payload")
	assert.Equal(t, firstPayloadID, *fake.responses[childID+"/"+elicID].ElicitationPayloadId)
}

func TestResolve_ChildSubmissionClearsWaitingRootProxyTurn(t *testing.T) {
	childID := "conv-child"
	rootID := "conv-root"
	childTurnID := "turn-child"
	rootTurnID := "turn-root"

	fake := newSeqRecordingConv(childID)
	fake.conversations[childID] = &apiconv.Conversation{Id: childID, ConversationParentId: &rootID}
	fake.conversations[rootID] = &apiconv.Conversation{Id: rootID, LastTurnId: &rootTurnID}

	srv := New(fake, nil, router.New(), nil)
	turn := &memory.TurnMeta{ConversationID: childID, TurnID: childTurnID}
	recorded, err := srv.Record(context.Background(), turn, "assistant", &execution.Elicitation{ElicitRequestParams: mcpproto.ElicitRequestParams{
		Message: "Need input",
	}})
	assert.NoError(t, err)
	elicID := *recorded.ElicitationID

	var proxy *apiconv.Message
	for _, msg := range fake.byID {
		if msg != nil && msg.ConversationId == rootID && msg.ElicitationId != nil && *msg.ElicitationId == elicID {
			proxy = msg
			break
		}
	}
	if !assert.NotNil(t, proxy) {
		return
	}
	fake.conversations[rootID].Status = strPtr("waiting_for_user")
	fake.conversations[rootID].Transcript = []*agconv.TranscriptView{{
		Id:      rootTurnID,
		Status:  "waiting_for_user",
		Message: []*agconv.MessageView{(*agconv.MessageView)(proxy)},
	}}

	err = srv.Resolve(context.Background(), childID, elicID, "accept", map[string]interface{}{"answer": "ok"}, "")
	assert.NoError(t, err)
	assert.Equal(t, "succeeded", fake.conversations[rootID].Transcript[0].Status)
	assert.NotNil(t, fake.conversations[rootID].Status)
	assert.Equal(t, "succeeded", *fake.conversations[rootID].Status)
}

func TestResolve_RootProxySubmissionResolvesAuthoritativeChild(t *testing.T) {
	childID := "conv-child"
	rootID := "conv-root"
	childTurnID := "turn-child"
	rootTurnID := "turn-root"

	fake := newSeqRecordingConv(childID)
	fake.conversations[childID] = &apiconv.Conversation{Id: childID, ConversationParentId: &rootID}
	fake.conversations[rootID] = &apiconv.Conversation{Id: rootID, LastTurnId: &rootTurnID}

	publisher := &captureEventsPublisher{}
	srv := New(fake, nil, router.New(), nil)
	srv.SetStreamPublisher(publisher)

	turn := &memory.TurnMeta{ConversationID: childID, TurnID: childTurnID}
	recorded, err := srv.Record(context.Background(), turn, "assistant", &execution.Elicitation{ElicitRequestParams: mcpproto.ElicitRequestParams{
		Message: "Need input",
	}})
	assert.NoError(t, err)
	elicID := *recorded.ElicitationID
	var proxy *apiconv.Message
	for _, msg := range fake.byID {
		if msg != nil && msg.ConversationId == rootID && msg.ElicitationId != nil && *msg.ElicitationId == elicID {
			proxy = msg
			break
		}
	}
	assert.NotNil(t, proxy)

	err = srv.Resolve(context.Background(), rootID, elicID, "accept", map[string]interface{}{"answer": "ok"}, "")
	assert.NoError(t, err)

	child := fake.byID[recorded.Id]
	if assert.NotNil(t, child) && assert.NotNil(t, child.Status) {
		assert.Equal(t, "accepted", *child.Status)
	}
	response := fake.responses[childID+"/"+elicID]
	if assert.NotNil(t, response) && assert.NotNil(t, response.ParentMessageId) {
		assert.Equal(t, recorded.Id, *response.ParentMessageId)
	}
	assertProxyDeleted(t, fake, rootID)
	assertResolvedEvent(t, publisher.events, childID, elicID)
	assertResolvedEvent(t, publisher.events, rootID, elicID)
}

func assertProxyDeleted(t *testing.T, fake *seqRecordingConv, rootID string) {
	t.Helper()
	found := false
	for _, item := range fake.deleted {
		if item.conversationID == rootID && item.id != "" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected root proxy message to be deleted")
}

func assertResolvedEvent(t *testing.T, events []*streaming.Event, conversationID, elicitationID string) {
	t.Helper()
	found := false
	for _, event := range events {
		if event == nil {
			continue
		}
		if event.Type == streaming.EventTypeElicitationResolved && event.ConversationID == conversationID && event.ElicitationID == elicitationID {
			found = true
			break
		}
	}
	assert.True(t, found, "expected resolved event for conversation %s", conversationID)
}

func strPtr(value string) *string {
	return &value
}
