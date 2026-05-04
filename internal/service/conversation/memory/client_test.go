package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	convcli "github.com/viant/agently-core/app/store/conversation"
	mem "github.com/viant/agently-core/internal/service/conversation/memory"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	msgw "github.com/viant/agently-core/pkg/agently/message/write"
	mcallw "github.com/viant/agently-core/pkg/agently/modelcall/write"
	toolw "github.com/viant/agently-core/pkg/agently/toolcall/write"
	turnw "github.com/viant/agently-core/pkg/agently/turn/write"
)

// dd-style data-driven test using testCase with input and expected output
func TestClient_GetConversation_DataDriven(t *testing.T) {
	ctx := context.Background()
	c := mem.New()

	// Arrange fixed timestamps
	t0 := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(1 * time.Minute)
	t2 := t0.Add(2 * time.Minute)

	// Seed conversation
	conv := convcli.NewConversation()
	conv.SetId("c1")
	conv.SetCreatedAt(t0)
	conv.SetAgentId("agentA")
	conv.SetTitle("A title")
	assert.NoError(t, c.PatchConversations(ctx, conv))

	// Seed turns
	turn1 := &turnw.Turn{Has: &turnw.TurnHas{}}
	turn1.SetId("t1")
	turn1.SetConversationID("c1")
	turn1.SetStatus("ok")
	turn1.SetCreatedAt(t1)
	assert.NoError(t, c.PatchTurn(ctx, (*convcli.MutableTurn)(turn1)))

	turn2 := &turnw.Turn{Has: &turnw.TurnHas{}}
	turn2.SetId("t2")
	turn2.SetConversationID("c1")
	turn2.SetStatus("ok")
	turn2.SetCreatedAt(t2)
	assert.NoError(t, c.PatchTurn(ctx, (*convcli.MutableTurn)(turn2)))

	// Seed messages
	m1 := &msgw.Message{Has: &msgw.MessageHas{}}
	m1.SetId("m1")
	m1.SetConversationID("c1")
	m1.SetTurnID("t1")
	m1.SetRole("user")
	m1.SetType("text")
	m1.SetContent("hello")
	m1.SetRawContent("hello raw")
	m1.SetCreatedAt(t1)
	assert.NoError(t, c.PatchMessage(ctx, (*convcli.MutableMessage)(m1)))

	m2 := &msgw.Message{Has: &msgw.MessageHas{}}
	m2.SetId("m2")
	m2.SetConversationID("c1")
	m2.SetTurnID("t2")
	m2.SetRole("assistant")
	m2.SetType("text")
	m2.SetContent("world")
	m2.SetCreatedAt(t2)
	assert.NoError(t, c.PatchMessage(ctx, (*convcli.MutableMessage)(m2)))

	// Attach model call to m2
	mc := &mcallw.ModelCall{Has: &mcallw.ModelCallHas{}}
	mc.SetMessageID("m2")
	mc.SetProvider("openai")
	mc.SetModel("gpt-4o")
	mc.SetModelKind("chat")
	mc.SetStatus("ok")
	assert.NoError(t, c.PatchModelCall(ctx, (*convcli.MutableModelCall)(mc)))

	// Attach tool call to m2 (as separate tool message)
	m3 := &msgw.Message{Has: &msgw.MessageHas{}}
	m3.SetId("m3")
	m3.SetConversationID("c1")
	m3.SetTurnID("t2")
	m3.SetParentMessageID("m2")
	m3.SetRole("assistant")
	m3.SetType("tool")
	m3.SetContent("call:toolX")
	m3.SetCreatedAt(t2)
	assert.NoError(t, c.PatchMessage(ctx, (*convcli.MutableMessage)(m3)))

	tc := &toolw.ToolCall{Has: &toolw.ToolCallHas{}}
	tc.SetMessageID("m3")
	tc.SetOpID("op-1")
	tc.SetAttempt(1)
	tc.SetToolName("toolX")
	tc.SetToolKind("http")
	tc.SetStatus("ok")
	assert.NoError(t, c.PatchToolCall(ctx, (*convcli.MutableToolCall)(tc)))

	srv := convcli.NewService(c)

	type testCase struct {
		name string
		req  convcli.GetRequest
		exp  *convcli.GetResponse
	}

	// Build expected base conversation using agconv then cast to client type
	agBase := &agconv.ConversationView{
		Id:         "c1",
		AgentId:    ptrS("agentA"),
		Title:      ptrS("A title"),
		Visibility: "private",
		CreatedAt:  t0,
		Transcript: []*agconv.TranscriptView{
			{Id: "t1", ConversationId: "c1", CreatedAt: t1, Status: "ok", Message: []*agconv.MessageView{{Id: "m1", ConversationId: "c1", TurnId: ptrS("t1"), Role: "user", Type: "text", Content: ptrS("hello"), RawContent: ptrS("hello raw"), CreatedAt: t1}}},
			{Id: "t2", ConversationId: "c1", CreatedAt: t2, Status: "ok", Message: []*agconv.MessageView{
				{Id: "m2", ConversationId: "c1", TurnId: ptrS("t2"), Role: "assistant", Type: "text", Content: ptrS("world"), CreatedAt: t2},
				{Id: "m3", ConversationId: "c1", TurnId: ptrS("t2"), ParentMessageId: ptrS("m2"), Role: "assistant", Type: "tool", Content: ptrS("call:toolX"), CreatedAt: t2},
			}},
		},
	}
	base := toClient(agBase)

	// Expected with model included
	withModel := toClient(cloneAg(agBase))
	withModel.Transcript[1].Message[0].ModelCall = &agconv.ModelCallView{MessageId: "m2", Provider: "openai", Model: "gpt-4o", ModelKind: "chat", Status: "ok"}

	// Expected with tool included
	withTool := toClient(cloneAg(agBase))
	withTool.Transcript[1].Message[0].ToolMessage = []*agconv.ToolMessageView{{
		Id:              "m3",
		ParentMessageId: ptrS("m2"),
		CreatedAt:       t2,
		Type:            "tool",
		Content:         ptrS("call:toolX"),
		ToolCall:        &agconv.ToolCallView{MessageId: "m3", OpId: "op-1", Attempt: 1, ToolName: "toolX", ToolKind: "http", Status: "ok"},
	}}

	// Expected since t2 only
	sinceT2 := toClient(cloneAg(agBase))
	sinceT2.Transcript = sinceT2.Transcript[1:]

	cases := []testCase{
		{
			name: "no options: no model/tool",
			req:  convcli.GetRequest{Id: "c1"},
			exp:  &convcli.GetResponse{Conversation: base},
		},
		{
			name: "include model",
			req:  convcli.GetRequest{Id: "c1", IncludeModelCall: true},
			exp:  &convcli.GetResponse{Conversation: withModel},
		},
		{
			name: "include tool",
			req:  convcli.GetRequest{Id: "c1", IncludeToolCall: true},
			exp:  &convcli.GetResponse{Conversation: withTool},
		},
		{
			name: "since t2",
			req:  convcli.GetRequest{Id: "c1", Since: "t2"},
			exp:  &convcli.GetResponse{Conversation: sinceT2},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := srv.Get(ctx, tc.req)
			assert.NoError(t, err)
			stripUsage(got)
			assert.EqualValues(t, tc.exp, got)
		})
	}
}

func TestClient_GetConversations_ListSummary(t *testing.T) {
	ctx := context.Background()
	c := mem.New()

	conv := convcli.NewConversation()
	conv.SetId("c1")
	conv.SetAgentId("agentA")
	conv.SetCreatedAt(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	assert.NoError(t, c.PatchConversations(ctx, conv))

	// Add a turn but list should not include transcript
	turn := &turnw.Turn{Has: &turnw.TurnHas{}}
	turn.SetId("t1")
	turn.SetConversationID("c1")
	turn.SetStatus("ok")
	assert.NoError(t, c.PatchTurn(ctx, (*convcli.MutableTurn)(turn)))

	items, err := c.GetConversations(ctx, &convcli.Input{})
	assert.NoError(t, err)
	expected := []*convcli.Conversation{{
		Id:         "c1",
		AgentId:    ptrS("agentA"),
		Visibility: "private",
		CreatedAt:  time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}}
	assert.EqualValues(t, expected, items)
}

func TestClient_PatchMessage_PreservesModeAndNarration(t *testing.T) {
	ctx := context.Background()
	c := mem.New()

	conv := convcli.NewConversation()
	conv.SetId("c-mode")
	require.NoError(t, c.PatchConversations(ctx, conv))

	turn := &turnw.Turn{Has: &turnw.TurnHas{}}
	turn.SetId("t-mode")
	turn.SetConversationID("c-mode")
	require.NoError(t, c.PatchTurn(ctx, (*convcli.MutableTurn)(turn)))

	msg := &msgw.Message{Has: &msgw.MessageHas{}}
	msg.SetId("m-mode")
	msg.SetConversationID("c-mode")
	msg.SetTurnID("t-mode")
	msg.SetRole("assistant")
	msg.SetType("text")
	msg.SetMode("router")
	msg.SetNarration("delegating")
	msg.SetContent("delegating")
	msg.SetInterim(1)
	require.NoError(t, c.PatchMessage(ctx, (*convcli.MutableMessage)(msg)))

	got, err := c.GetConversation(ctx, "c-mode", convcli.WithIncludeTranscript(true))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.Transcript, 1)
	require.Len(t, got.Transcript[0].Message, 1)
	assert.Equal(t, "router", ptrVal(got.Transcript[0].Message[0].Mode))
	assert.Equal(t, "delegating", ptrVal(got.Transcript[0].Message[0].Narration))
}

func TestClient_GetConversations_AppliesListFilters(t *testing.T) {
	ctx := context.Background()
	c := mem.New()

	parent := convcli.NewConversation()
	parent.SetId("parent-1")
	parent.SetCreatedAt(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	parent.SetVisibility("public")
	require.NoError(t, c.PatchConversations(ctx, parent))

	parentTurn := &turnw.Turn{Has: &turnw.TurnHas{}}
	parentTurn.SetId("turn-1")
	parentTurn.SetConversationID("parent-1")
	parentTurn.SetStatus("completed")
	require.NoError(t, c.PatchTurn(ctx, (*convcli.MutableTurn)(parentTurn)))

	root := convcli.NewConversation()
	root.SetId("root-1")
	root.SetAgentId("agent-a")
	root.SetTitle("Favorite Colors")
	root.SetStatus("active")
	root.SetCreatedAt(time.Date(2025, 1, 1, 0, 1, 0, 0, time.UTC))
	root.SetVisibility("public")
	require.NoError(t, c.PatchConversations(ctx, root))

	scheduled := convcli.NewConversation()
	scheduled.SetId("scheduled-1")
	scheduled.SetAgentId("agent-a")
	scheduled.SetTitle("Favorite Colors Scheduled")
	scheduled.SetStatus("active")
	scheduled.SetScheduleId("sched-1")
	scheduled.SetCreatedAt(time.Date(2025, 1, 1, 0, 2, 0, 0, time.UTC))
	scheduled.SetVisibility("public")
	require.NoError(t, c.PatchConversations(ctx, scheduled))

	child := convcli.NewConversation()
	child.SetId("child-1")
	child.SetAgentId("agent-a")
	child.SetTitle("Favorite Colors Child")
	child.SetStatus("active")
	child.SetConversationParentId("parent-1")
	child.SetConversationParentTurnId("turn-1")
	child.SetCreatedAt(time.Date(2025, 1, 1, 0, 3, 0, 0, time.UTC))
	child.SetVisibility("public")
	require.NoError(t, c.PatchConversations(ctx, child))

	query := &convcli.Input{
		AgentId:          "agent-a",
		ExcludeChildren:  true,
		ExcludeScheduled: true,
		Query:            "favorite",
		StatusFilter:     "active",
		Has: &agconv.ConversationInputHas{
			AgentId:          true,
			ExcludeChildren:  true,
			ExcludeScheduled: true,
			Query:            true,
			StatusFilter:     true,
		},
	}
	items, err := c.GetConversations(ctx, query)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "root-1", items[0].Id)
}

func ptrVal(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func TestClient_DeleteConversation_DataDriven(t *testing.T) {
	ctx := context.Background()
	c := mem.New()

	// Seed conversation with a message
	conv := convcli.NewConversation()
	conv.SetId("c-del")
	conv.SetAgentId("agentB")
	conv.SetCreatedAt(time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC))
	assert.NoError(t, c.PatchConversations(ctx, conv))

	turn := &turnw.Turn{Has: &turnw.TurnHas{}}
	turn.SetId("t-del")
	turn.SetConversationID("c-del")
	turn.SetStatus("ok")
	assert.NoError(t, c.PatchTurn(ctx, (*convcli.MutableTurn)(turn)))

	m := &msgw.Message{Has: &msgw.MessageHas{}}
	m.SetId("m-del")
	m.SetConversationID("c-del")
	m.SetTurnID("t-del")
	m.SetRole("user")
	m.SetType("text")
	m.SetContent("bye")
	m.SetCreatedAt(time.Date(2025, 2, 1, 1, 0, 0, 0, time.UTC))
	assert.NoError(t, c.PatchMessage(ctx, (*convcli.MutableMessage)(m)))

	// Delete and verify
	assert.NoError(t, c.DeleteConversation(ctx, "c-del"))

	// Get by id should return nil
	got, err := c.GetConversation(ctx, "c-del")
	assert.NoError(t, err)
	assert.EqualValues(t, (*convcli.Conversation)(nil), got)

	// List should not include the deleted conversation
	items, err := c.GetConversations(ctx, &convcli.Input{})
	assert.NoError(t, err)
	assert.EqualValues(t, []*convcli.Conversation{}, items)
}

func TestClient_MessageRawContentRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	type testCase struct {
		name     string
		rawInput *string
		expected *string
	}
	cases := []testCase{
		{name: "raw preserved", rawInput: ptrS("user raw input"), expected: ptrS("user raw input")},
		{name: "empty raw dropped", rawInput: ptrS(""), expected: nil},
		{name: "raw never set remains nil", rawInput: nil, expected: nil},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := mem.New()
			conv := convcli.NewConversation()
			conv.SetId("c-raw")
			require.NoError(t, c.PatchConversations(ctx, conv))
			msg := &msgw.Message{Has: &msgw.MessageHas{}}
			msg.SetId("m-raw")
			msg.SetConversationID("c-raw")
			msg.SetRole("user")
			msg.SetType("text")
			msg.SetContent("expanded")
			if tc.rawInput != nil {
				msg.SetRawContent(*tc.rawInput)
			}
			require.NoError(t, c.PatchMessage(ctx, (*convcli.MutableMessage)(msg)), tc.name)
			stored, err := c.GetMessage(ctx, "m-raw")
			require.NoError(t, err, tc.name)
			require.NotNil(t, stored, tc.name)
			if tc.expected == nil {
				assert.Nil(t, stored.RawContent, tc.name)
			} else {
				require.NotNil(t, stored.RawContent, tc.name)
				assert.EqualValues(t, *tc.expected, *stored.RawContent, tc.name)
			}
			if stored.Content == nil {
				t.Fatalf("content nil for case %s", tc.name)
			}
			assert.EqualValues(t, "expanded", *stored.Content, tc.name)
		})
	}
}

func TestClient_PatchMessage_PartialUpdate_DataDriven(t *testing.T) {
	ctx := context.Background()
	type testCase struct {
		name       string
		build      func() *msgw.Message
		expectErr  string
		wantStatus *string
	}
	cases := []testCase{
		{
			name: "update existing message status without conversation id",
			build: func() *msgw.Message {
				m := &msgw.Message{Has: &msgw.MessageHas{}}
				m.SetId("m1")
				m.SetStatus("accepted")
				return m
			},
			wantStatus: ptrS("accepted"),
		},
		{
			name: "new message without conversation id returns missing conversation id",
			build: func() *msgw.Message {
				m := &msgw.Message{Has: &msgw.MessageHas{}}
				m.SetId("m-new")
				m.SetStatus("accepted")
				return m
			},
			expectErr: "missing conversation id",
		},
		{
			name: "missing id returns missing message id",
			build: func() *msgw.Message {
				return &msgw.Message{Has: &msgw.MessageHas{}}
			},
			expectErr: "missing message id",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			c := mem.New()

			conv := convcli.NewConversation()
			conv.SetId("c1")
			require.NoError(t, c.PatchConversations(ctx, conv))

			seed := &msgw.Message{Has: &msgw.MessageHas{}}
			seed.SetId("m1")
			seed.SetConversationID("c1")
			seed.SetRole("tool")
			seed.SetType("control")
			seed.SetStatus("pending")
			require.NoError(t, c.PatchMessage(ctx, (*convcli.MutableMessage)(seed)))

			err := c.PatchMessage(ctx, (*convcli.MutableMessage)(tc.build()))
			if tc.expectErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectErr)
				return
			}
			require.NoError(t, err)
			got, getErr := c.GetMessage(ctx, "m1")
			require.NoError(t, getErr)
			require.NotNil(t, got)
			require.NotNil(t, got.Status)
			assert.EqualValues(t, *tc.wantStatus, *got.Status)
		})
	}
}

func TestClient_PatchTurn_PartialUpdate_DataDriven(t *testing.T) {
	ctx := context.Background()
	type testCase struct {
		name       string
		build      func() *turnw.Turn
		expectErr  string
		wantStatus string
	}
	cases := []testCase{
		{
			name: "update existing turn status without conversation id",
			build: func() *turnw.Turn {
				t := &turnw.Turn{Has: &turnw.TurnHas{}}
				t.SetId("t1")
				t.SetStatus("completed")
				return t
			},
			wantStatus: "completed",
		},
		{
			name: "new turn without conversation id returns missing conversation id",
			build: func() *turnw.Turn {
				t := &turnw.Turn{Has: &turnw.TurnHas{}}
				t.SetId("t-new")
				t.SetStatus("completed")
				return t
			},
			expectErr: "missing conversation id",
		},
		{
			name: "missing id returns missing turn id",
			build: func() *turnw.Turn {
				return &turnw.Turn{Has: &turnw.TurnHas{}}
			},
			expectErr: "missing turn id",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			c := mem.New()
			conv := convcli.NewConversation()
			conv.SetId("c1")
			require.NoError(t, c.PatchConversations(ctx, conv))

			seed := &turnw.Turn{Has: &turnw.TurnHas{}}
			seed.SetId("t1")
			seed.SetConversationID("c1")
			seed.SetStatus("running")
			require.NoError(t, c.PatchTurn(ctx, (*convcli.MutableTurn)(seed)))

			err := c.PatchTurn(ctx, (*convcli.MutableTurn)(tc.build()))
			if tc.expectErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectErr)
				return
			}
			require.NoError(t, err)

			got, getErr := c.GetConversation(ctx, "c1")
			require.NoError(t, getErr)
			require.NotNil(t, got)
			found := false
			for _, tr := range got.Transcript {
				if tr != nil && tr.Id == "t1" {
					assert.EqualValues(t, tc.wantStatus, tr.Status)
					found = true
					break
				}
			}
			require.True(t, found, "seed turn t1 not found")
		})
	}
}

// Helpers for building expected data
func ptrS(s string) *string { return &s }

// toClient casts agconv view to client view
func toClient(v *agconv.ConversationView) *convcli.Conversation {
	c := convcli.Conversation(*v)
	return &c
}

// cloneAg deep copies an agconv conversation
func cloneAg(in *agconv.ConversationView) *agconv.ConversationView {
	if in == nil {
		return nil
	}
	cp := *in
	if in.Transcript != nil {
		cp.Transcript = make([]*agconv.TranscriptView, len(in.Transcript))
		for i, tv := range in.Transcript {
			if tv == nil {
				continue
			}
			tt := *tv
			if tv.Message != nil {
				tt.Message = make([]*agconv.MessageView, len(tv.Message))
				for j, mv := range tv.Message {
					if mv == nil {
						continue
					}
					mm := *mv
					if mv.ModelCall != nil {
						tmp := *mv.ModelCall
						mm.ModelCall = &tmp
					}
					if mv.ToolMessage != nil {
						mm.ToolMessage = make([]*agconv.ToolMessageView, len(mv.ToolMessage))
						for k, tm := range mv.ToolMessage {
							if tm == nil {
								continue
							}
							tt := *tm
							if tm.ToolCall != nil {
								tc := *tm.ToolCall
								tt.ToolCall = &tc
							}
							mm.ToolMessage[k] = &tt
						}
					}
					tt.Message[j] = &mm
				}
			}
			cp.Transcript[i] = &tt
		}
	}
	return &cp
}

// stripUsage normalizes dynamic usage aggregation so that structural comparisons
// in tests focus on transcript content and metadata.
func stripUsage(resp *convcli.GetResponse) {
	if resp == nil || resp.Conversation == nil {
		return
	}
	resp.Conversation.Usage = nil
}
