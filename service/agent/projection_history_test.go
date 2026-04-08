package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/prompt"
	runtimeprojection "github.com/viant/agently-core/runtime/projection"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

func TestBuildHistory_UsesProjectionHiddenTurnsAndMessages(t *testing.T) {
	now := time.Now().UTC()
	transcript := apiconv.Transcript{
		&apiconv.Turn{
			Id: "turn-1",
			Message: []*agconv.MessageView{
				{
					Id:        "msg-1",
					TurnId:    strPtr("turn-1"),
					Role:      "user",
					Type:      "text",
					Content:   strPtr("visible"),
					CreatedAt: now,
				},
				{
					Id:        "msg-2",
					TurnId:    strPtr("turn-1"),
					Role:      "assistant",
					Type:      "text",
					Content:   strPtr("hide-message"),
					CreatedAt: now.Add(time.Second),
				},
			},
		},
		&apiconv.Turn{
			Id: "turn-2",
			Message: []*agconv.MessageView{
				{
					Id:        "msg-3",
					TurnId:    strPtr("turn-2"),
					Role:      "user",
					Type:      "text",
					Content:   strPtr("hide-turn"),
					CreatedAt: now.Add(2 * time.Second),
				},
			},
		},
	}

	ctx := runtimeprojection.WithState(context.Background())
	state, ok := runtimeprojection.StateFromContext(ctx)
	require.True(t, ok)
	state.HideMessages("msg-2")
	state.HideTurns("turn-2")

	svc := &Service{}
	history, err := svc.buildHistory(ctx, transcript)
	require.NoError(t, err)
	require.Len(t, history.Past, 1)
	require.Len(t, history.Past[0].Messages, 1)
	require.Equal(t, "visible", history.Past[0].Messages[0].Content)

	snapshot := state.Snapshot()
	require.Equal(t, []string{"turn-2"}, snapshot.HiddenTurnIDs)
	require.Contains(t, snapshot.HiddenMessageIDs, "msg-3")
}

func TestBuildHistory_PopulatesProjectionStateFromSupersession(t *testing.T) {
	now := time.Now().UTC()
	args := strPtr(`{"uri":"file.go"}`)
	transcript := apiconv.Transcript{
		&apiconv.Turn{
			Id: "turn-1",
			Message: []*agconv.MessageView{
				{
					Id:        "msg-user-1",
					TurnId:    strPtr("turn-1"),
					Role:      "user",
					Type:      "text",
					Content:   strPtr("read file once"),
					CreatedAt: now,
					ToolMessage: []*agconv.ToolMessageView{
						{
							Id:        "tool-msg-1",
							CreatedAt: now.Add(time.Second),
							ToolCall: &agconv.ToolCallView{
								OpId:           "op-1",
								ToolName:       "resources/read",
								RequestPayload: &agconv.ModelCallStreamPayloadView{InlineBody: args},
								ResponsePayload: &agconv.ModelCallStreamPayloadView{
									InlineBody: strPtr("old content"),
								},
							},
						},
					},
				},
			},
		},
		&apiconv.Turn{
			Id: "turn-2",
			Message: []*agconv.MessageView{
				{
					Id:        "msg-user-2",
					TurnId:    strPtr("turn-2"),
					Role:      "user",
					Type:      "text",
					Content:   strPtr("read file again"),
					CreatedAt: now.Add(2 * time.Second),
					ToolMessage: []*agconv.ToolMessageView{
						{
							Id:        "tool-msg-2",
							CreatedAt: now.Add(3 * time.Second),
							ToolCall: &agconv.ToolCallView{
								OpId:           "op-2",
								ToolName:       "resources/read",
								RequestPayload: &agconv.ModelCallStreamPayloadView{InlineBody: args},
								ResponsePayload: &agconv.ModelCallStreamPayloadView{
									InlineBody: strPtr("new content"),
								},
							},
						},
					},
				},
			},
		},
	}

	ctx := runtimeprojection.WithState(context.Background())
	state, ok := runtimeprojection.StateFromContext(ctx)
	require.True(t, ok)

	svc := &Service{
		defaults: &config.Defaults{
			Projection: config.Projection{
				ToolCallSupersession: &config.ToolCallSupersession{},
			},
		},
		registry: &stubCacheableRegistry{defs: map[string]*llm.ToolDefinition{
			"resources/read": {Name: "resources/read", Cacheable: true},
		}},
	}

	agentExposure := agentmdl.ToolCallExposure("conversation")
	result, err := svc.buildChronologicalHistory(ctx, transcript, &QueryInput{
		ToolCallExposure: &agentExposure,
	}, false)
	require.NoError(t, err)
	history := result.History

	snapshot := state.Snapshot()
	require.Contains(t, snapshot.HiddenMessageIDs, "tool-msg-1")
	require.NotContains(t, snapshot.HiddenMessageIDs, "tool-msg-2")
	require.Equal(t, "tool call supersession", snapshot.Reason)
	require.Greater(t, snapshot.TokensFreed, 0)

	require.Len(t, history.Past, 2)
	// One tool result should remain visible in history.
	toolResults := 0
	for _, turn := range history.Past {
		for _, msg := range turn.Messages {
			if msg.Kind == prompt.MessageKindToolResult {
				toolResults++
			}
		}
	}
	require.Equal(t, 1, toolResults)
}

func TestBuildHistory_ProjectionAndSupersessionCompose(t *testing.T) {
	now := time.Now().UTC()
	args := strPtr(`{"uri":"file.go"}`)
	transcript := apiconv.Transcript{
		&apiconv.Turn{
			Id: "turn-1",
			Message: []*agconv.MessageView{
				{
					Id:        "msg-user-1",
					TurnId:    strPtr("turn-1"),
					Role:      "user",
					Type:      "text",
					Content:   strPtr("keep user"),
					CreatedAt: now,
				},
				{
					Id:        "msg-assistant-1",
					TurnId:    strPtr("turn-1"),
					Role:      "assistant",
					Type:      "text",
					Content:   strPtr("pre-hidden"),
					CreatedAt: now.Add(time.Second),
				},
				{
					Id:        "msg-user-tool-1",
					TurnId:    strPtr("turn-1"),
					Role:      "user",
					Type:      "text",
					Content:   strPtr("read file once"),
					CreatedAt: now.Add(2 * time.Second),
					ToolMessage: []*agconv.ToolMessageView{
						{
							Id:        "tool-msg-1",
							CreatedAt: now.Add(3 * time.Second),
							ToolCall: &agconv.ToolCallView{
								OpId:           "op-1",
								ToolName:       "resources/read",
								RequestPayload: &agconv.ModelCallStreamPayloadView{InlineBody: args},
								ResponsePayload: &agconv.ModelCallStreamPayloadView{
									InlineBody: strPtr("old content"),
								},
							},
						},
					},
				},
			},
		},
		&apiconv.Turn{
			Id: "turn-2",
			Message: []*agconv.MessageView{
				{
					Id:        "msg-user-tool-2",
					TurnId:    strPtr("turn-2"),
					Role:      "user",
					Type:      "text",
					Content:   strPtr("read file again"),
					CreatedAt: now.Add(4 * time.Second),
					ToolMessage: []*agconv.ToolMessageView{
						{
							Id:        "tool-msg-2",
							CreatedAt: now.Add(5 * time.Second),
							ToolCall: &agconv.ToolCallView{
								OpId:           "op-2",
								ToolName:       "resources/read",
								RequestPayload: &agconv.ModelCallStreamPayloadView{InlineBody: args},
								ResponsePayload: &agconv.ModelCallStreamPayloadView{
									InlineBody: strPtr("new content"),
								},
							},
						},
					},
				},
			},
		},
	}

	ctx := runtimeprojection.WithState(context.Background())
	state, ok := runtimeprojection.StateFromContext(ctx)
	require.True(t, ok)
	state.HideMessages("msg-assistant-1")

	svc := &Service{
		defaults: &config.Defaults{
			Projection: config.Projection{
				ToolCallSupersession: &config.ToolCallSupersession{},
			},
		},
		registry: &stubCacheableRegistry{defs: map[string]*llm.ToolDefinition{
			"resources/read": {Name: "resources/read", Cacheable: true},
		}},
	}

	agentExposure := agentmdl.ToolCallExposure("conversation")
	result, err := svc.buildChronologicalHistory(ctx, transcript, &QueryInput{
		ToolCallExposure: &agentExposure,
	}, false)
	require.NoError(t, err)
	history := result.History

	snapshot := state.Snapshot()
	require.Contains(t, snapshot.HiddenMessageIDs, "msg-assistant-1")
	require.Contains(t, snapshot.HiddenMessageIDs, "tool-msg-1")
	require.Equal(t, "tool call supersession", snapshot.Reason)

	var contents []string
	for _, turn := range history.Past {
		for _, msg := range turn.Messages {
			contents = append(contents, msg.Content)
		}
	}
	require.NotContains(t, contents, "pre-hidden")
	require.NotContains(t, contents, "old content")
	require.Contains(t, contents, "new content")
}

func TestBuildHistory_SetsProjectionScopeFromToolExposure(t *testing.T) {
	now := time.Now().UTC()
	transcript := apiconv.Transcript{
		&apiconv.Turn{
			Id: "turn-1",
			Message: []*agconv.MessageView{
				{
					Id:        "msg-1",
					TurnId:    strPtr("turn-1"),
					Role:      "user",
					Type:      "text",
					Content:   strPtr("visible"),
					CreatedAt: now,
				},
			},
		},
	}

	ctx := runtimeprojection.WithState(context.Background())
	state, ok := runtimeprojection.StateFromContext(ctx)
	require.True(t, ok)

	svc := &Service{}
	agentExposure := agentmdl.ToolCallExposure("conversation")
	_, err := svc.buildChronologicalHistory(ctx, transcript, &QueryInput{
		ToolCallExposure: &agentExposure,
	}, false)
	require.NoError(t, err)

	snapshot := state.Snapshot()
	require.Equal(t, "conversation", snapshot.Scope)
}

func TestBuildBinding_ExposesProjectionInContext(t *testing.T) {
	now := time.Now().UTC()
	ctx := runtimeprojection.WithState(context.Background())
	state, ok := runtimeprojection.StateFromContext(ctx)
	require.True(t, ok)
	state.SetScope("conversation")
	state.HideTurns("turn-1")
	state.HideMessages("msg-1")
	state.SetReason("projection active")
	state.AddTokensFreed(12)
	ctx = memory.WithTurnMeta(ctx, memory.TurnMeta{TurnID: "turn-1"})

	convClient := &stubProjectionBindingConversationClient{
		conversation: &apiconv.Conversation{Id: "conv-1", Transcript: []*agconv.TranscriptView{
			{
				Id: "turn-1",
				Message: []*agconv.MessageView{
					{Id: "msg-1", TurnId: strPtr("turn-1"), Role: "user", Type: "text", Content: strPtr("query"), CreatedAt: now},
				},
			},
		}},
	}
	svc := &Service{
		conversation: convClient,
		registry:     &stubCacheableRegistry{},
		defaults:     &config.Defaults{},
	}
	ag := &agentmdl.Agent{}
	exposure := agentmdl.ToolCallExposure("conversation")
	binding, err := svc.BuildBinding(ctx, &QueryInput{
		ConversationID:   "conv-1",
		Agent:            ag,
		Context:          map[string]interface{}{},
		ToolCallExposure: &exposure,
	})
	require.NoError(t, err)
	require.NotNil(t, binding)
	proj, ok := binding.Context["Projection"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "conversation", proj["scope"])
	require.Equal(t, "projection active", proj["reason"])
	require.Equal(t, 12, proj["tokensFreed"])
}

type stubProjectionBindingConversationClient struct {
	conversation *apiconv.Conversation
}

func (s *stubProjectionBindingConversationClient) GetPayload(context.Context, string) (*apiconv.Payload, error) {
	return nil, nil
}
func (s *stubProjectionBindingConversationClient) GetConversation(context.Context, string, ...apiconv.Option) (*apiconv.Conversation, error) {
	return s.conversation, nil
}
func (s *stubProjectionBindingConversationClient) GetConversations(context.Context, *apiconv.Input) ([]*apiconv.Conversation, error) {
	return nil, nil
}
func (s *stubProjectionBindingConversationClient) PatchConversations(context.Context, *apiconv.MutableConversation) error {
	return nil
}
func (s *stubProjectionBindingConversationClient) PatchPayload(context.Context, *apiconv.MutablePayload) error {
	return nil
}
func (s *stubProjectionBindingConversationClient) PatchMessage(context.Context, *apiconv.MutableMessage) error {
	return nil
}
func (s *stubProjectionBindingConversationClient) GetMessage(context.Context, string, ...apiconv.Option) (*apiconv.Message, error) {
	return nil, nil
}
func (s *stubProjectionBindingConversationClient) GetMessageByElicitation(context.Context, string, string) (*apiconv.Message, error) {
	return nil, nil
}
func (s *stubProjectionBindingConversationClient) PatchModelCall(context.Context, *apiconv.MutableModelCall) error {
	return nil
}
func (s *stubProjectionBindingConversationClient) PatchToolCall(context.Context, *apiconv.MutableToolCall) error {
	return nil
}
func (s *stubProjectionBindingConversationClient) PatchTurn(context.Context, *apiconv.MutableTurn) error {
	return nil
}
func (s *stubProjectionBindingConversationClient) DeleteConversation(context.Context, string) error {
	return nil
}
func (s *stubProjectionBindingConversationClient) DeleteMessage(context.Context, string, string) error {
	return nil
}

func TestBuildHistory_PopulatesProjectionStateFromRelevanceSelector(t *testing.T) {
	now := time.Now().UTC()
	transcript := apiconv.Transcript{
		&apiconv.Turn{
			Id: "turn-1",
			Message: []*agconv.MessageView{
				{Id: "msg-1", TurnId: strPtr("turn-1"), Role: "user", Type: "text", Content: strPtr("old irrelevant"), CreatedAt: now},
			},
		},
		&apiconv.Turn{
			Id: "turn-2",
			Message: []*agconv.MessageView{
				{Id: "msg-2", TurnId: strPtr("turn-2"), Role: "user", Type: "text", Content: strPtr("recent protected"), CreatedAt: now.Add(time.Second)},
			},
		},
		&apiconv.Turn{
			Id: "turn-3",
			Message: []*agconv.MessageView{
				{Id: "msg-3", TurnId: strPtr("turn-3"), Role: "user", Type: "text", Content: strPtr("current task"), CreatedAt: now.Add(2 * time.Second)},
			},
		},
	}

	ctx := runtimeprojection.WithState(context.Background())
	ctx = memory.WithTurnMeta(ctx, memory.TurnMeta{TurnID: "turn-3"})
	state, ok := runtimeprojection.StateFromContext(ctx)
	require.True(t, ok)

	protected := 1
	svc := &Service{
		defaults: &config.Defaults{
			Projection: config.Projection{
				Relevance: &config.RelevanceProjection{
					Enabled:              boolPtr(true),
					ProtectedRecentTurns: &protected,
				},
			},
		},
		relevanceSelector: func(_ context.Context, input relevanceSelectorInput) (*relevanceSelectorOutput, error) {
			require.Equal(t, "current query", input.CurrentTask)
			require.Equal(t, []string{"turn-2"}, input.ProtectedTurnIDs)
			require.Len(t, input.Candidates, 1)
			require.Equal(t, "turn-1", input.Candidates[0].TurnID)
			return &relevanceSelectorOutput{
				TurnIDs: []string{"turn-1"},
				Reason:  "relevance projection",
			}, nil
		},
	}

	exposure := agentmdl.ToolCallExposure("conversation")
	result, err := svc.buildChronologicalHistory(ctx, transcript, &QueryInput{
		Query:            "current query",
		ToolCallExposure: &exposure,
	}, false)
	require.NoError(t, err)

	snapshot := state.Snapshot()
	require.Equal(t, "conversation", snapshot.Scope)
	require.Equal(t, []string{"turn-1"}, snapshot.HiddenTurnIDs)
	require.Contains(t, snapshot.HiddenMessageIDs, "msg-1")
	require.Contains(t, snapshot.Reason, "relevance projection")
	require.Greater(t, snapshot.TokensFreed, 0)

	require.Len(t, result.History.Past, 2)
	var contents []string
	for _, turn := range result.History.Past {
		for _, msg := range turn.Messages {
			contents = append(contents, msg.Content)
		}
	}
	require.NotContains(t, contents, "old irrelevant")
	require.Contains(t, contents, "recent protected")
	require.Contains(t, contents, "current task")
}

func TestBuildRelevanceSelectorInput_ProtectedRecentTurns(t *testing.T) {
	now := time.Now().UTC()
	transcript := apiconv.Transcript{
		&apiconv.Turn{Id: "turn-1", Message: []*agconv.MessageView{{Id: "msg-1", TurnId: strPtr("turn-1"), Role: "user", Type: "text", Content: strPtr("first"), CreatedAt: now}}},
		&apiconv.Turn{Id: "turn-2", Message: []*agconv.MessageView{{Id: "msg-2", TurnId: strPtr("turn-2"), Role: "user", Type: "text", Content: strPtr("second"), CreatedAt: now.Add(time.Second)}}},
		&apiconv.Turn{Id: "turn-3", Message: []*agconv.MessageView{{Id: "msg-3", TurnId: strPtr("turn-3"), Role: "user", Type: "text", Content: strPtr("third"), CreatedAt: now.Add(2 * time.Second)}}},
	}

	candidates, protected, total := buildRelevanceSelectorInput(transcript, "turn-3", 1)
	require.Equal(t, []string{"turn-2"}, protected)
	require.Len(t, candidates, 1)
	require.Equal(t, "turn-1", candidates[0].TurnID)
	require.Greater(t, total, 0)
}

func boolPtr(v bool) *bool {
	return &v
}
