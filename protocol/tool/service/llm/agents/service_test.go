package agents

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	convcli "github.com/viant/agently-core/app/store/conversation"
	cancels "github.com/viant/agently-core/app/store/conversation/cancel"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	convmem "github.com/viant/agently-core/internal/service/conversation/memory"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	toolpol "github.com/viant/agently-core/protocol/tool"
	"github.com/viant/agently-core/runtime/memory"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
	coreauth "github.com/viant/agently-core/service/auth"
	linksvc "github.com/viant/agently-core/service/linking"
	executil "github.com/viant/agently-core/service/shared/executil"
	statussvc "github.com/viant/agently-core/service/toolstatus"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

func TestService_List_DataDriven(t *testing.T) {
	ctx := context.Background()
	testCases := []struct {
		name     string
		items    []ListItem
		expected *ListOutput
	}{
		{
			name:     "empty list",
			items:    nil,
			expected: expectedListOutput(nil),
		},
		{
			name:     "single item",
			items:    []ListItem{{ID: "coder", Name: "Coder", Description: "Writes code", Priority: 10, Tags: []string{"code"}}},
			expected: expectedListOutput([]ListItem{{ID: "coder", Name: "Coder", Description: "Writes code", Priority: 10, Tags: []string{"code"}}}),
		},
		{
			name: "multiple items",
			items: []ListItem{
				{ID: "researcher", Name: "Researcher", Description: "Finds info", Priority: 5, Tags: []string{"research"}},
				{ID: "coder", Name: "Coder", Description: "Writes code", Priority: 10, Tags: []string{"code"}},
			},
			expected: expectedListOutput([]ListItem{
				{ID: "researcher", Name: "Researcher", Description: "Finds info", Priority: 5, Tags: []string{"research"}},
				{ID: "coder", Name: "Coder", Description: "Writes code", Priority: 10, Tags: []string{"code"}},
			}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			dir := func() []ListItem { return tc.items }
			s := New(nil, WithDirectoryProvider(dir))

			// Act
			var out ListOutput
			err := s.list(ctx, &struct{}{}, &out)

			// Assert
			assert.NoError(t, err)
			assert.EqualValues(t, tc.expected, &out)
		})
	}
}

func expectedListOutput(items []ListItem) *ListOutput {
	return &ListOutput{
		Items:      items,
		ReuseNote:  "Reuse this directory for the rest of the current turn. Do not call llm/agents:list again unless the available agents changed.",
		RunUsage:   "To delegate, call llm/agents:run with {agentId, objective}. You may call multiple llm/agents:run in parallel within the same response to run agents concurrently.",
		NextAction: "",
	}
}

func TestService_Run_External_DataDriven(t *testing.T) {
	ctx := context.Background()
	testCases := []struct {
		name     string
		input    *RunInput
		runner   func(context.Context, string, string, map[string]interface{}) (string, string, string, string, bool, []string, error)
		expected *RunOutput
	}{
		{
			name:  "external returns task and context",
			input: &RunInput{AgentID: "researcher", Objective: "find info", Context: map[string]interface{}{"foo": "bar"}},
			runner: func(_ context.Context, agentID, objective string, payload map[string]interface{}) (string, string, string, string, bool, []string, error) {
				return "answer", "completed", "t-1", "ctx-1", true, []string{"warn-1"}, nil
			},
			expected: &RunOutput{Answer: "answer", Status: "completed", TaskID: "t-1", ContextID: "ctx-1", StreamSupported: true, Warnings: []string{"warn-1"}},
		},
		{
			name:  "external returns empty answer but terminal status",
			input: &RunInput{AgentID: "ext", Objective: "do"},
			runner: func(_ context.Context, agentID, objective string, payload map[string]interface{}) (string, string, string, string, bool, []string, error) {
				return "", "failed", "t-err", "ctx-x", false, nil, nil
			},
			expected: &RunOutput{Answer: "", Status: "failed", TaskID: "t-err", ContextID: "ctx-x", StreamSupported: false},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			s := New(nil, WithExternalRunner(tc.runner))

			// Act
			var out RunOutput
			err := s.run(ctx, tc.input, &out)

			// Assert
			assert.NoError(t, err)
			assert.EqualValues(t, tc.expected, &out)
		})
	}
}

// fakeAgentRuntime is a lightweight stub implementing agentRuntime so we can
// verify that llm/agents:run threads model preferences and reasoning effort
// through to the underlying agent.Query input.
type fakeAgentRuntime struct {
	lastInput  *agentsvc.QueryInput
	lastPolicy *toolpol.Policy
	finder     agentmdl.Finder
	queryFn    func(ctx context.Context, in *agentsvc.QueryInput, out *agentsvc.QueryOutput) error
}

func (f *fakeAgentRuntime) Query(ctx context.Context, in *agentsvc.QueryInput, out *agentsvc.QueryOutput) error {
	f.lastInput = in
	f.lastPolicy = toolpol.FromContext(ctx)
	if f.queryFn != nil {
		return f.queryFn(ctx, in, out)
	}
	if out != nil {
		out.Content = "ok"
	}
	return nil
}

// TestService_Run_Internal_RespectsTimeout verifies that the child agent
// context carries a deadline so a hung child doesn't block forever.
// TDD: this test FAILS until runInternal applies a timeout to the child context.
func TestService_Run_Internal_RespectsTimeout(t *testing.T) {
	ctx := context.Background()

	fake := &fakeAgentRuntime{
		finder: &fakeFinder{agents: map[string]*agentmdl.Agent{
			"slow": {Identity: agentmdl.Identity{ID: "slow"}},
		}},
		queryFn: func(ctx context.Context, in *agentsvc.QueryInput, out *agentsvc.QueryOutput) error {
			// The child context MUST have a deadline.
			_, hasDeadline := ctx.Deadline()
			assert.True(t, hasDeadline, "child agent context must have a deadline to prevent hanging forever")
			if out != nil {
				out.Content = "done"
			}
			return nil
		},
	}
	s := &Service{agent: fake}

	var out RunOutput
	err := s.run(ctx, &RunInput{AgentID: "slow", Objective: "test timeout"}, &out)
	assert.NoError(t, err)
	assert.Equal(t, "done", out.Answer)
}

// TestService_Run_Internal_HungChildTimesOut verifies that a child agent
// that blocks indefinitely is terminated by the timeout.
func TestService_Run_Internal_HungChildTimesOut(t *testing.T) {
	ctx := context.Background()

	fake := &fakeAgentRuntime{
		finder: &fakeFinder{agents: map[string]*agentmdl.Agent{
			"hung": {Identity: agentmdl.Identity{ID: "hung"}},
		}},
		queryFn: func(ctx context.Context, in *agentsvc.QueryInput, out *agentsvc.QueryOutput) error {
			// Simulate a hung tool call — block until context is cancelled.
			<-ctx.Done()
			return ctx.Err()
		},
	}
	s := &Service{agent: fake, ChildTimeout: 500 * time.Millisecond}

	var out RunOutput
	start := time.Now()
	err := s.run(ctx, &RunInput{AgentID: "hung", Objective: "will hang"}, &out)
	elapsed := time.Since(start)
	// Should return an error (context deadline exceeded), NOT hang forever.
	assert.Error(t, err, "expected timeout error for hung child agent")
	// Must complete within a reasonable time (well under 10 minutes).
	assert.Less(t, elapsed, 5*time.Second, "should time out quickly, not hang")
}

// Finder is unused in this test; return nil to satisfy the interface.
func (f *fakeAgentRuntime) Finder() agentmdl.Finder { return f.finder }

func TestService_Run_Internal_ThreadsModelPrefsAndReasoning(t *testing.T) {
	ctx := context.Background()
	streaming := false
	reasoning := "medium"
	prefs := &llm.ModelPreferences{
		IntelligencePriority: 0.7,
		SpeedPriority:        0.7,
		CostPriority:         0.7,
	}
	in := &RunInput{
		AgentID:          "dev_reviewer",
		Objective:        "review changes",
		Streaming:        &streaming,
		ModelPreferences: prefs,
		ReasoningEffort:  &reasoning,
		Context:          map[string]interface{}{"foo": "bar"},
	}

	fake := &fakeAgentRuntime{
		finder: &fakeFinder{agents: map[string]*agentmdl.Agent{
			"dev_reviewer": {Identity: agentmdl.Identity{ID: "dev_reviewer"}, ModelSelection: llm.ModelSelection{Model: "openai_gpt4o_mini"}},
		}},
	}
	s := &Service{agent: fake}
	var out RunOutput
	err := s.run(ctx, in, &out)
	assert.NoError(t, err)
	if assert.NotNil(t, fake.lastInput, "expected QueryInput to be passed to agent runtime") {
		assert.Equal(t, in.AgentID, fake.lastInput.AgentID)
		assert.Equal(t, in.Objective, fake.lastInput.Query)
		assert.Equal(t, in.Context, fake.lastInput.Context)
		assert.Nil(t, fake.lastInput.ModelPreferences)
		assert.Equal(t, &reasoning, fake.lastInput.ReasoningEffort)
		if assert.NotNil(t, fake.lastInput.Agent) {
			assert.Equal(t, "dev_reviewer", fake.lastInput.Agent.ID)
			assert.Equal(t, "openai_gpt4o_mini", fake.lastInput.Agent.Model)
		}
	}
}

func TestService_Run_Internal_RebindsChildConversationContext(t *testing.T) {
	ctx := memory.WithConversationID(context.Background(), "parent-conv")
	ctx = memory.WithTurnMeta(ctx, memory.TurnMeta{
		ConversationID:  "parent-conv",
		TurnID:          "parent-turn",
		ParentMessageID: "parent-msg",
		Assistant:       "parent-agent",
	})

	fake := &fakeAgentRuntime{
		finder: &fakeFinder{agents: map[string]*agentmdl.Agent{
			"child-agent": {Identity: agentmdl.Identity{ID: "child-agent"}},
		}},
		queryFn: func(ctx context.Context, in *agentsvc.QueryInput, out *agentsvc.QueryOutput) error {
			assert.Equal(t, "child-conv", memory.ConversationIDFromContext(ctx), "child run should not inherit parent conversation id")
			turnMeta, ok := memory.TurnMetaFromContext(ctx)
			require.True(t, ok, "child run should have turn metadata seeded")
			assert.Equal(t, "child-conv", turnMeta.ConversationID)
			assert.NotEqual(t, "parent-turn", turnMeta.TurnID)
			if out != nil {
				out.Content = "done"
				out.ConversationID = "child-conv"
			}
			return nil
		},
	}

	s := &Service{agent: fake}
	runCtx := linkedRun{
		parent: memory.TurnMeta{
			ConversationID:  "parent-conv",
			TurnID:          "parent-turn",
			ParentMessageID: "parent-msg",
		},
		childConversationID: "child-conv",
	}
	qi := &agentsvc.QueryInput{
		AgentID:        "child-agent",
		ConversationID: "child-conv",
		Query:          "delegate",
	}
	qo := &agentsvc.QueryOutput{}

	result := s.executeChildRun(ctx, qi, qo, runCtx)
	require.NoError(t, result.err)
	assert.Equal(t, "child-conv", result.conversationID)
	assert.Equal(t, "done", result.answer)
}

func TestService_Run_Internal_InheritsParentWorkdir(t *testing.T) {
	ctx := executil.WithWorkdir(context.Background(), "/tmp/poly")
	streaming := false
	in := &RunInput{
		AgentID:   "dev_reviewer",
		Objective: "review repo",
		Streaming: &streaming,
	}

	fake := &fakeAgentRuntime{
		finder: &fakeFinder{agents: map[string]*agentmdl.Agent{
			"dev_reviewer": {Identity: agentmdl.Identity{ID: "dev_reviewer"}, ModelSelection: llm.ModelSelection{Model: "openai_gpt4o_mini"}},
		}},
	}
	s := &Service{agent: fake}
	var out RunOutput
	err := s.run(ctx, in, &out)
	assert.NoError(t, err)
	if assert.NotNil(t, fake.lastInput) && assert.NotNil(t, fake.lastInput.Context) {
		assert.Equal(t, "/tmp/poly", fake.lastInput.Context["workdir"])
		assert.Equal(t, "/tmp/poly", fake.lastInput.Context["resolvedWorkdir"])
	}
}

func TestService_Run_Internal_InheritsParentAuthUserAndTokens(t *testing.T) {
	base := context.Background()
	base = coreauth.InjectUser(base, "oauth-user-42")
	base = authctx.WithUserInfo(base, &authctx.UserInfo{
		Subject: "oauth-user-42",
		Email:   "user@example.com",
	})
	base = coreauth.InjectTokens(base, &scyauth.Token{
		Token: oauth2.Token{
			AccessToken:  "access-token-123",
			RefreshToken: "refresh-token-123",
		},
		IDToken: "id-token-123",
	})

	fake := &fakeAgentRuntime{
		finder: &fakeFinder{agents: map[string]*agentmdl.Agent{
			"child": {Identity: agentmdl.Identity{ID: "child"}},
		}},
		queryFn: func(ctx context.Context, in *agentsvc.QueryInput, out *agentsvc.QueryOutput) error {
			assert.Equal(t, "oauth-user-42", coreauth.EffectiveUserID(ctx))
			if ui := authctx.User(ctx); assert.NotNil(t, ui) {
				assert.Equal(t, "oauth-user-42", ui.Subject)
				assert.Equal(t, "user@example.com", ui.Email)
			}
			if tok := authctx.TokensFromContext(ctx); assert.NotNil(t, tok) {
				assert.Equal(t, "access-token-123", tok.AccessToken)
				assert.Equal(t, "id-token-123", tok.IDToken)
				assert.Equal(t, "refresh-token-123", tok.RefreshToken)
			}
			if out != nil {
				out.Content = "ok"
			}
			return nil
		},
	}

	s := &Service{agent: fake}
	var out RunOutput
	err := s.run(base, &RunInput{AgentID: "child", Objective: "verify child auth inheritance"}, &out)
	require.NoError(t, err)
	assert.Equal(t, "ok", out.Answer)
}

func TestService_Run_Internal_DoesNotInheritParentToolAllowList(t *testing.T) {
	streaming := false
	in := &RunInput{
		AgentID:   "dev_reviewer",
		Objective: "check status",
		Streaming: &streaming,
		Context:   map[string]interface{}{"foo": "bar"},
	}

	testCases := []struct {
		name           string
		ctx            context.Context
		expectedPolicy *toolpol.Policy
	}{
		{
			name:           "no_parent_policy",
			ctx:            context.Background(),
			expectedPolicy: nil,
		},
		{
			name: "parent_policy_is_cleared",
			ctx: toolpol.WithPolicy(context.Background(), &toolpol.Policy{
				Mode:      toolpol.ModeAuto,
				AllowList: []string{"llm/agents:run"},
			}),
			expectedPolicy: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeAgentRuntime{}
			s := &Service{agent: fake}

			var out RunOutput
			err := s.run(tc.ctx, in, &out)

			assert.NoError(t, err)
			if assert.NotNil(t, fake.lastInput) {
				assert.EqualValues(t, []string{}, fake.lastInput.ToolsAllowed)
			}
			assert.EqualValues(t, tc.expectedPolicy, fake.lastPolicy)
		})
	}
}

func TestService_Run_Internal_RepoAnalysisUsesBoundedToolAllowList(t *testing.T) {
	streaming := false
	in := &RunInput{
		AgentID:   "coder",
		Objective: "analyze /Users/awitas/go/src/github.com/viant/xdatly",
		Streaming: &streaming,
		Context: map[string]interface{}{
			"workdir":      "/Users/awitas/go/src/github.com/viant/xdatly",
			"repoAnalysis": true,
		},
	}

	fake := &fakeAgentRuntime{
		finder: &fakeFinder{agents: map[string]*agentmdl.Agent{
			"coder": {Identity: agentmdl.Identity{ID: "coder"}},
		}},
	}
	s := &Service{agent: fake}

	var out RunOutput
	err := s.run(context.Background(), in, &out)

	require.NoError(t, err)
	require.NotNil(t, fake.lastInput)
	assert.Contains(t, fake.lastInput.Query, "Use at most one `resources-list` call on the repo root")
	assert.EqualValues(t, []string{
		"resources:list",
		"resources-list",
		"resources:read",
		"resources-read",
		"resources:grepFiles",
		"resources-grepFiles",
		"resources:roots",
		"resources-roots",
		"resources:match",
		"resources-match",
		"resources:matchDocuments",
		"resources-matchDocuments",
		"system/exec:execute",
		"system_exec-execute",
		"system/os:getEnv",
		"system_os-getEnv",
		"message:show",
		"message-show",
		"internal/message:show",
		"internal_message-show",
		"message:summarize",
		"message-summarize",
		"internal/message:summarize",
		"internal_message-summarize",
		"message:match",
		"message-match",
		"internal/message:match",
		"internal_message-match",
	}, fake.lastInput.ToolsAllowed)
}

func TestService_Run_Internal_ChildFailureReturnsToolResult(t *testing.T) {
	ctx := context.Background()
	conv := convmem.New()

	parentConv := convcli.NewConversation()
	parentConv.SetId("parent-conv")
	require.NoError(t, conv.PatchConversations(ctx, parentConv))

	parentTurn := convcli.NewTurn()
	parentTurn.SetId("turn-1")
	parentTurn.SetConversationID("parent-conv")
	require.NoError(t, conv.PatchTurn(ctx, parentTurn))

	runCtx := memory.WithTurnMeta(ctx, memory.TurnMeta{
		ConversationID: "parent-conv",
		TurnID:         "turn-1",
	})

	fake := &fakeAgentRuntime{
		finder: &fakeFinder{agents: map[string]*agentmdl.Agent{
			"coder": {Identity: agentmdl.Identity{ID: "coder"}},
		}},
		queryFn: func(ctx context.Context, in *agentsvc.QueryInput, out *agentsvc.QueryOutput) error {
			childConvID := in.ConversationID
			require.NotEmpty(t, childConvID)

			childTurn := convcli.NewTurn()
			childTurn.SetId("child-turn-1")
			childTurn.SetConversationID(childConvID)
			childTurn.SetStatus("failed")
			errMsg := "child stream failed"
			childTurn.SetErrorMessage(errMsg)
			require.NoError(t, conv.PatchTurn(ctx, childTurn))

			childMsg := convcli.NewMessage()
			childMsg.SetId("child-msg-1")
			childMsg.SetConversationID(childConvID)
			childMsg.SetTurnID("child-turn-1")
			childMsg.SetRole("assistant")
			childMsg.SetType("text")
			childMsg.SetContent("partial child summary")
			require.NoError(t, conv.PatchMessage(ctx, childMsg))

			return assert.AnError
		},
	}

	svc := New(nil, WithConversationClient(conv))
	svc.agent = fake

	var out RunOutput
	err := svc.run(runCtx, &RunInput{
		AgentID:   "coder",
		Objective: "analyze /Users/awitas/go/src/github.com/viant/xdatly",
		Context: map[string]interface{}{
			"workdir":      "/Users/awitas/go/src/github.com/viant/xdatly",
			"repoAnalysis": true,
		},
	}, &out)

	require.NoError(t, err)
	assert.Equal(t, "failed", out.Status)
	assert.NotEmpty(t, out.ConversationID)
	assert.Contains(t, out.Answer, "ended with status failed")
	assert.Contains(t, out.Answer, "partial child summary")
}

func TestService_Run_Internal_CanceledParentButSucceededChildReturnsSuccess(t *testing.T) {
	ctx := context.Background()
	conv := convmem.New()

	parentConv := convcli.NewConversation()
	parentConv.SetId("parent-conv")
	require.NoError(t, conv.PatchConversations(ctx, parentConv))

	parentTurn := convcli.NewTurn()
	parentTurn.SetId("turn-1")
	parentTurn.SetConversationID("parent-conv")
	require.NoError(t, conv.PatchTurn(ctx, parentTurn))

	runCtx := memory.WithTurnMeta(ctx, memory.TurnMeta{
		ConversationID: "parent-conv",
		TurnID:         "turn-1",
	})

	fake := &fakeAgentRuntime{
		finder: &fakeFinder{agents: map[string]*agentmdl.Agent{
			"coder": {Identity: agentmdl.Identity{ID: "coder"}},
		}},
		queryFn: func(ctx context.Context, in *agentsvc.QueryInput, out *agentsvc.QueryOutput) error {
			childConvID := in.ConversationID
			require.NotEmpty(t, childConvID)

			childPatch := convcli.NewConversation()
			childPatch.SetId(childConvID)
			childPatch.SetStatus("succeeded")
			require.NoError(t, conv.PatchConversations(ctx, childPatch))

			childTurn := convcli.NewTurn()
			childTurn.SetId("child-turn-1")
			childTurn.SetConversationID(childConvID)
			childTurn.SetStatus("succeeded")
			require.NoError(t, conv.PatchTurn(ctx, childTurn))

			childMsg := convcli.NewMessage()
			childMsg.SetId("child-msg-1")
			childMsg.SetConversationID(childConvID)
			childMsg.SetTurnID("child-turn-1")
			childMsg.SetRole("assistant")
			childMsg.SetType("text")
			childMsg.SetContent("child completed successfully")
			require.NoError(t, conv.PatchMessage(ctx, childMsg))

			return context.Canceled
		},
	}

	svc := New(nil, WithConversationClient(conv))
	svc.agent = fake

	var out RunOutput
	err := svc.run(runCtx, &RunInput{
		AgentID:   "coder",
		Objective: "analyze /Users/awitas/go/src/github.com/viant/xdatly",
		Context:   map[string]interface{}{"workdir": "/Users/awitas/go/src/github.com/viant/xdatly"},
	}, &out)

	require.NoError(t, err)
	assert.Equal(t, "succeeded", out.Status)
	assert.Equal(t, "child completed successfully", out.Answer)
	assert.NotEmpty(t, out.ConversationID)
}

func TestService_Run_Internal_CanceledChildReturnsFailedToolResult(t *testing.T) {
	ctx := context.Background()
	conv := convmem.New()

	parentConv := convcli.NewConversation()
	parentConv.SetId("parent-conv")
	require.NoError(t, conv.PatchConversations(ctx, parentConv))

	parentTurn := convcli.NewTurn()
	parentTurn.SetId("turn-1")
	parentTurn.SetConversationID("parent-conv")
	require.NoError(t, conv.PatchTurn(ctx, parentTurn))

	runCtx := memory.WithTurnMeta(ctx, memory.TurnMeta{
		ConversationID: "parent-conv",
		TurnID:         "turn-1",
	})

	fake := &fakeAgentRuntime{
		finder: &fakeFinder{agents: map[string]*agentmdl.Agent{
			"coder": {Identity: agentmdl.Identity{ID: "coder"}},
		}},
		queryFn: func(ctx context.Context, in *agentsvc.QueryInput, out *agentsvc.QueryOutput) error {
			childConvID := in.ConversationID
			require.NotEmpty(t, childConvID)

			childPatch := convcli.NewConversation()
			childPatch.SetId(childConvID)
			childPatch.SetStatus("canceled")
			require.NoError(t, conv.PatchConversations(ctx, childPatch))

			childTurn := convcli.NewTurn()
			childTurn.SetId("child-turn-1")
			childTurn.SetConversationID(childConvID)
			childTurn.SetStatus("canceled")
			errMsg := "child execution canceled after downstream error"
			childTurn.SetErrorMessage(errMsg)
			require.NoError(t, conv.PatchTurn(ctx, childTurn))

			childMsg := convcli.NewMessage()
			childMsg.SetId("child-msg-1")
			childMsg.SetConversationID(childConvID)
			childMsg.SetTurnID("child-turn-1")
			childMsg.SetRole("assistant")
			childMsg.SetType("text")
			childMsg.SetContent("partial child output")
			require.NoError(t, conv.PatchMessage(ctx, childMsg))

			return context.Canceled
		},
	}

	svc := New(nil, WithConversationClient(conv))
	svc.agent = fake

	var out RunOutput
	err := svc.run(runCtx, &RunInput{
		AgentID:   "coder",
		Objective: "analyze /Users/awitas/go/src/github.com/viant/xdatly",
		Context:   map[string]interface{}{"workdir": "/Users/awitas/go/src/github.com/viant/xdatly"},
	}, &out)

	require.NoError(t, err)
	assert.Equal(t, "failed", out.Status)
	assert.NotEmpty(t, out.ConversationID)
	assert.Contains(t, out.Answer, "ended with status canceled")
	assert.Contains(t, out.Answer, "partial child output")
}

func TestService_Status_ByConversationID(t *testing.T) {
	ctx := context.Background()
	conv := convmem.New()

	childConv := convcli.NewConversation()
	childConv.SetId("child-conv")
	childConv.SetAgentId("coder")
	childConv.SetStatus("succeeded")
	parentID := "parent-conv"
	parentTurnID := "turn-1"
	childConv.SetConversationParentId(parentID)
	childConv.SetConversationParentTurnId(parentTurnID)
	require.NoError(t, conv.PatchConversations(ctx, childConv))

	childTurn := convcli.NewTurn()
	childTurn.SetId("child-turn-1")
	childTurn.SetConversationID("child-conv")
	childTurn.SetStatus("succeeded")
	require.NoError(t, conv.PatchTurn(ctx, childTurn))

	preamble := convcli.NewMessage()
	preamble.SetId("child-msg-1")
	preamble.SetConversationID("child-conv")
	preamble.SetTurnID("child-turn-1")
	preamble.SetRole("assistant")
	preamble.SetType("text")
	preamble.SetInterim(1)
	preamble.SetPreamble("calling tools")
	preamble.SetContent("calling tools")
	require.NoError(t, conv.PatchMessage(ctx, preamble))

	final := convcli.NewMessage()
	final.SetId("child-msg-2")
	final.SetConversationID("child-conv")
	final.SetTurnID("child-turn-1")
	final.SetRole("assistant")
	final.SetType("text")
	final.SetInterim(0)
	final.SetContent("final child answer")
	require.NoError(t, conv.PatchMessage(ctx, final))

	svc := New(nil, WithConversationClient(conv))
	var out StatusOutput
	err := svc.statusMethod(ctx, &StatusInput{ConversationID: "child-conv"}, &out)
	require.NoError(t, err)
	require.Len(t, out.Items, 1)
	assert.Equal(t, "child-conv", out.Items[0].ConversationID)
	assert.Equal(t, "coder", out.Items[0].AgentID)
	assert.Equal(t, "succeeded", out.Items[0].Status)
	assert.Equal(t, "calling tools", out.Items[0].LastAssistantPreamble)
	assert.Equal(t, "final child answer", out.Items[0].LastAssistantResponse)
	assert.True(t, out.Items[0].HasFinalResponse)
	assert.Equal(t, parentID, out.Items[0].ParentConversationID)
	assert.Equal(t, parentTurnID, out.Items[0].ParentTurnID)
}

func TestService_Status_ByParentConversationAndTurn(t *testing.T) {
	ctx := context.Background()
	conv := convmem.New()

	for i, id := range []string{"child-a", "child-b"} {
		childConv := convcli.NewConversation()
		childConv.SetId(id)
		childConv.SetAgentId("agent-" + string(rune('a'+i)))
		childConv.SetStatus("running")
		childConv.SetConversationParentId("parent-conv")
		childConv.SetConversationParentTurnId("turn-1")
		require.NoError(t, conv.PatchConversations(ctx, childConv))
	}

	svc := New(nil, WithConversationClient(conv))
	var out StatusOutput
	err := svc.statusMethod(ctx, &StatusInput{ParentConversationID: "parent-conv", ParentTurnID: "turn-1"}, &out)
	require.NoError(t, err)
	require.Len(t, out.Items, 2)
	assert.Equal(t, "parent-conv", out.Items[0].ParentConversationID)
	assert.Equal(t, "turn-1", out.Items[0].ParentTurnID)
	assert.Equal(t, "parent-conv", out.Items[1].ParentConversationID)
	assert.Equal(t, "turn-1", out.Items[1].ParentTurnID)
}

func TestService_Cancel_CancelsConversation(t *testing.T) {
	ctx := context.Background()
	reg := cancels.NewMemory()
	canceled := false
	reg.Register("child-conv", "turn-1", func() { canceled = true })

	svc := New(nil, WithCancelRegistry(reg))
	var out CancelOutput
	err := svc.cancelMethod(ctx, &CancelInput{ConversationID: "child-conv"}, &out)
	require.NoError(t, err)
	assert.True(t, canceled)
	assert.Equal(t, "canceled", out.Status)
}

func TestService_Cancel_ReturnsConversationStatusWhenAlreadyTerminal(t *testing.T) {
	ctx := context.Background()
	conv := convmem.New()

	childConv := convcli.NewConversation()
	childConv.SetId("child-conv")
	childConv.SetStatus("succeeded")
	require.NoError(t, conv.PatchConversations(ctx, childConv))
	childTurn := convcli.NewTurn()
	childTurn.SetId("child-turn-1")
	childTurn.SetConversationID("child-conv")
	childTurn.SetStatus("succeeded")
	require.NoError(t, conv.PatchTurn(ctx, childTurn))

	svc := New(nil, WithConversationClient(conv), WithCancelRegistry(cancels.NewMemory()))
	var out CancelOutput
	err := svc.cancelMethod(ctx, &CancelInput{ConversationID: "child-conv"}, &out)
	require.NoError(t, err)
	assert.Equal(t, "succeeded", out.Status)
}

func TestService_AsyncConfig(t *testing.T) {
	svc := New(nil)
	cfg := svc.AsyncConfig("llm/agents:run")
	require.NotNil(t, cfg)
	assert.Equal(t, "llm/agents:run", cfg.Run.Tool)
	assert.Equal(t, "conversationId", cfg.Run.OperationIDPath)
	assert.Equal(t, "llm/agents:status", cfg.Status.Tool)
	assert.Equal(t, "conversationId", cfg.Status.OperationIDArg)
	if assert.NotNil(t, cfg.Cancel) {
		assert.Equal(t, "llm/agents:cancel", cfg.Cancel.Tool)
	}
	assert.Nil(t, svc.AsyncConfig("llm/agents:list"))
}

func TestService_Run_Internal_AsyncReturnsConversationHandle(t *testing.T) {
	ctx := context.Background()
	conv := convmem.New()

	parentConv := convcli.NewConversation()
	parentConv.SetId("parent-conv")
	require.NoError(t, conv.PatchConversations(ctx, parentConv))

	parentTurn := convcli.NewTurn()
	parentTurn.SetId("turn-1")
	parentTurn.SetConversationID("parent-conv")
	require.NoError(t, conv.PatchTurn(ctx, parentTurn))

	runCtx := memory.WithTurnMeta(ctx, memory.TurnMeta{
		ConversationID: "parent-conv",
		TurnID:         "turn-1",
	})

	asyncFlag := true
	fake := &fakeAgentRuntime{
		finder: &fakeFinder{agents: map[string]*agentmdl.Agent{
			"coder": {Identity: agentmdl.Identity{ID: "coder"}},
		}},
		queryFn: func(ctx context.Context, in *agentsvc.QueryInput, out *agentsvc.QueryOutput) error {
			time.Sleep(200 * time.Millisecond)
			if out != nil {
				out.Content = "done"
				out.ConversationID = in.ConversationID
			}
			return nil
		},
	}

	svc := New(nil, WithConversationClient(conv))
	svc.agent = fake

	var out RunOutput
	err := svc.run(runCtx, &RunInput{
		AgentID:   "coder",
		Objective: "do async work",
		Async:     &asyncFlag,
	}, &out)

	require.NoError(t, err)
	assert.Equal(t, "running", out.Status)
	assert.NotEmpty(t, out.ConversationID)
}

type fakeFinder struct {
	agents map[string]*agentmdl.Agent
}

func (f *fakeFinder) Find(_ context.Context, id string) (*agentmdl.Agent, error) {
	if f == nil || f.agents == nil {
		return nil, nil
	}
	return f.agents[id], nil
}

func TestService_Run_PrefersInternalWhenResolvable(t *testing.T) {
	ctx := context.Background()
	calledExternal := false

	testCases := []struct {
		name           string
		intendedSource string
		expectExternal bool
	}{
		{name: "unknown_route_prefers_internal", intendedSource: "", expectExternal: false},
		{name: "explicit_external_route_uses_external", intendedSource: "external", expectExternal: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			calledExternal = false
			runner := func(_ context.Context, agentID, objective string, payload map[string]interface{}) (string, string, string, string, bool, []string, error) {
				calledExternal = true
				return "ext-answer", "completed", "t-1", "ctx-1", true, nil, nil
			}
			in := &RunInput{AgentID: "coder", Objective: "do work"}
			fake := &fakeAgentRuntime{
				finder: &fakeFinder{agents: map[string]*agentmdl.Agent{"coder": {Identity: agentmdl.Identity{ID: "coder"}}}},
			}
			s := &Service{
				agent:       fake,
				runExternal: runner,
				allowed:     map[string]string{"coder": tc.intendedSource},
			}

			var out RunOutput
			err := s.run(ctx, in, &out)

			assert.NoError(t, err)
			assert.EqualValues(t, tc.expectExternal, calledExternal)
			if tc.expectExternal {
				assert.EqualValues(t, "ext-answer", out.Answer)
			} else {
				assert.EqualValues(t, "ok", out.Answer)
			}
		})
	}
}

func TestService_Run_ExternalDirectoryEntry_NeverFallsBackToLocal(t *testing.T) {
	ctx := context.Background()

	t.Run("external directory entry uses external runner", func(t *testing.T) {
		calledExternal := false
		s := New(nil,
			WithDirectoryProvider(func() []ListItem {
				return []ListItem{{ID: "guardian", Name: "Guardian", Source: "external"}}
			}),
			WithExternalRunner(func(_ context.Context, agentID, objective string, payload map[string]interface{}) (string, string, string, string, bool, []string, error) {
				calledExternal = true
				return "remote-answer", "completed", "task-1", "ctx-1", false, nil, nil
			}),
		)
		s.agent = &fakeAgentRuntime{
			finder: &fakeFinder{agents: map[string]*agentmdl.Agent{}},
		}

		var out RunOutput
		err := s.run(ctx, &RunInput{AgentID: "guardian", Objective: "diagnose"}, &out)
		require.NoError(t, err)
		assert.True(t, calledExternal)
		assert.Equal(t, "remote-answer", out.Answer)
		assert.Equal(t, "completed", out.Status)
	})

	t.Run("external directory entry fails explicitly when external route unavailable", func(t *testing.T) {
		s := New(nil, WithDirectoryProvider(func() []ListItem {
			return []ListItem{{ID: "guardian", Name: "Guardian", Source: "external"}}
		}))
		s.agent = &fakeAgentRuntime{
			finder: &fakeFinder{agents: map[string]*agentmdl.Agent{}},
		}

		var out RunOutput
		err := s.run(ctx, &RunInput{AgentID: "guardian", Objective: "diagnose"}, &out)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "external agent route unavailable")
	})
}

func TestService_Run_Strict_AllowsInternalNotListedInDirectory(t *testing.T) {
	ctx := context.Background()
	in := &RunInput{AgentID: "internal-only", Objective: "do work"}

	testCases := []struct {
		name    string
		allowed map[string]string
		items   []ListItem
		wantErr bool
	}{
		{
			name:    "internal_allowed_but_not_listed",
			allowed: map[string]string{"internal-only": "internal"},
			items:   []ListItem{{ID: "public", Name: "Public", Source: "internal"}},
			wantErr: false,
		},
		{
			name:    "not_allowed_fails_in_strict",
			allowed: map[string]string{},
			items:   []ListItem{{ID: "public", Name: "Public", Source: "internal"}},
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeAgentRuntime{}
			s := New(nil,
				WithDirectoryProvider(func() []ListItem { return tc.items }),
				WithAllowedIDs(tc.allowed),
				WithStrict(true),
			)
			s.agent = fake

			var out RunOutput
			err := s.run(ctx, in, &out)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.EqualValues(t, "ok", out.Answer)
		})
	}
}

func TestAttachLinkedConversation_AttachesToStatusAndToolMessage(t *testing.T) {
	ctx := context.Background()
	conv := convmem.New()

	parentConv := convcli.NewConversation()
	parentConv.SetId("parent-conv")
	require.NoError(t, conv.PatchConversations(ctx, parentConv))

	turn := convcli.NewTurn()
	turn.SetId("turn-1")
	turn.SetConversationID("parent-conv")
	require.NoError(t, conv.PatchTurn(ctx, turn))

	statusMsg := convcli.NewMessage()
	statusMsg.SetId("status-msg")
	statusMsg.SetConversationID("parent-conv")
	statusMsg.SetTurnID("turn-1")
	statusMsg.SetRole("assistant")
	statusMsg.SetType("status")
	require.NoError(t, conv.PatchMessage(ctx, statusMsg))

	toolMsg := convcli.NewMessage()
	toolMsg.SetId("tool-msg")
	toolMsg.SetConversationID("parent-conv")
	toolMsg.SetTurnID("turn-1")
	toolMsg.SetRole("tool")
	toolMsg.SetType("tool_op")
	require.NoError(t, conv.PatchMessage(ctx, toolMsg))

	ctx = memory.WithToolMessageID(ctx, "tool-msg")
	parent := memory.TurnMeta{ConversationID: "parent-conv", TurnID: "turn-1"}
	attachLinkedConversation(ctx, conv, parent, "status-msg", "child-conv")

	gotStatus, err := conv.GetMessage(ctx, "status-msg")
	require.NoError(t, err)
	require.NotNil(t, gotStatus)
	require.NotNil(t, gotStatus.LinkedConversationId)
	assert.Equal(t, "child-conv", *gotStatus.LinkedConversationId)

	gotTool, err := conv.GetMessage(ctx, "tool-msg")
	require.NoError(t, err)
	require.NotNil(t, gotTool)
	require.NotNil(t, gotTool.LinkedConversationId)
	assert.Equal(t, "child-conv", *gotTool.LinkedConversationId)
}

func TestService_Run_External_DoesNotPersistObjectiveEchoPreview(t *testing.T) {
	ctx := context.Background()
	conv := convmem.New()

	parentConv := convcli.NewConversation()
	parentConv.SetId("parent-conv")
	require.NoError(t, conv.PatchConversations(ctx, parentConv))

	turn := convcli.NewTurn()
	turn.SetId("turn-1")
	turn.SetConversationID("parent-conv")
	require.NoError(t, conv.PatchTurn(ctx, turn))

	runCtx := memory.WithTurnMeta(ctx, memory.TurnMeta{
		ConversationID: "parent-conv",
		TurnID:         "turn-1",
	})

	objective := "Analyze project /Users/awitas/go/src/github.com/viant/xdatly and summarize the structure."
	svc := New(nil,
		WithConversationClient(conv),
		WithExternalRunner(func(_ context.Context, agentID, objective string, payload map[string]interface{}) (string, string, string, string, bool, []string, error) {
			return "done", "completed", "task-1", "ctx-1", false, nil, nil
		}),
	)
	svc.agent = &fakeAgentRuntime{}

	var out RunOutput
	err := svc.run(runCtx, &RunInput{
		AgentID:   "coder",
		Objective: objective,
	}, &out)
	require.NoError(t, err)

	got, err := conv.GetConversation(ctx, "parent-conv")
	require.NoError(t, err)
	require.NotNil(t, got)

	var foundObjectiveEcho bool
	var foundLinkedStatus bool
	for _, transcriptTurn := range got.Transcript {
		if transcriptTurn == nil {
			continue
		}
		for _, msg := range transcriptTurn.Message {
			if msg == nil {
				continue
			}
			if msg.Role == "assistant" && msg.Content != nil && *msg.Content == objective {
				foundObjectiveEcho = true
			}
			if msg.Role == "assistant" && msg.ToolName != nil && (*msg.ToolName == "llm/agents:run" || *msg.ToolName == "llm/agents-run" || *msg.ToolName == "llm/agents/run") && msg.LinkedConversationId != nil && *msg.LinkedConversationId != "" {
				foundLinkedStatus = true
			}
		}
	}

	assert.False(t, foundObjectiveEcho, "parent conversation should not persist an assistant echo preview for delegation objective")
	// External A2A agents host their own conversation on a remote server.
	// A local linked-conversation stub must NOT be created — it would produce
	// a dead UI card pointing to an empty local conversation.
	assert.False(t, foundLinkedStatus, "external run must not set linked_conversation_id — remote conversation cannot be navigated locally")
}

func TestStartRunStatus_EmitsLinkedConversationAttachedForToolMessageID(t *testing.T) {
	ctx := context.Background()
	conv := convmem.New()
	bus := streaming.NewMemoryBus(8)
	sub, err := bus.Subscribe(ctx, nil)
	require.NoError(t, err)
	defer sub.Close()

	parentConv := convcli.NewConversation()
	parentConv.SetId("parent-conv")
	require.NoError(t, conv.PatchConversations(ctx, parentConv))

	parentTurn := convcli.NewTurn()
	parentTurn.SetId("turn-1")
	parentTurn.SetConversationID("parent-conv")
	require.NoError(t, conv.PatchTurn(ctx, parentTurn))

	svc := &Service{
		conv:   conv,
		status: statussvc.New(conv),
		linker: linksvc.New(conv),
	}
	svc.linker.SetStreamPublisher(bus)

	ctx = memory.WithToolMessageID(ctx, "tool-msg-123")
	parent := memory.TurnMeta{ConversationID: "parent-conv", TurnID: "turn-1"}
	statusMsgID := svc.startRunStatus(ctx, parent, "child-conv", "guardian", "external")
	require.NotEmpty(t, statusMsgID)

	var linkedEvent *streaming.Event
	timeout := time.After(2 * time.Second)
	for linkedEvent == nil {
		select {
		case ev := <-sub.C():
			if ev != nil && ev.Type == streaming.EventTypeLinkedConversationAttached {
				linkedEvent = ev
			}
		case <-timeout:
			t.Fatalf("timed out waiting for linked conversation event")
		}
	}

	require.NotNil(t, linkedEvent)
	assert.Equal(t, "tool-msg-123", linkedEvent.ToolCallID)
	assert.Equal(t, "child-conv", linkedEvent.LinkedConversationID)
	assert.Equal(t, "guardian", linkedEvent.LinkedConversationAgentID)
}

// TestService_Run_Internal_InheritsParentModel verifies that the child agent
// inherits the parent conversation's default model when the child agent has
// no explicitly configured model. This prevents the child from falling back
// to the system default (e.g., gpt-4o-mini) when the user selected gpt-5.2.
func TestService_Run_Internal_InheritsParentModel(t *testing.T) {
	conv := convmem.New()
	ctx := context.Background()

	// Set up parent conversation with a specific default model (simulating
	// user selecting gpt-5.2 in the UI).
	parentConv := convcli.NewConversation()
	parentConv.SetId("parent-conv")
	parentModel := "openai/gpt-5.2"
	parentConv.SetDefaultModel(parentModel)
	require.NoError(t, conv.PatchConversations(ctx, parentConv))

	// Parent turn context
	runCtx := memory.WithTurnMeta(
		memory.WithConversationID(ctx, "parent-conv"),
		memory.TurnMeta{ConversationID: "parent-conv", TurnID: "turn-1"},
	)

	// Child agent with NO explicit model configured
	fake := &fakeAgentRuntime{
		finder: &fakeFinder{agents: map[string]*agentmdl.Agent{
			"analyzer": {Identity: agentmdl.Identity{ID: "analyzer"}},
		}},
	}
	s := &Service{agent: fake, conv: conv}

	var out RunOutput
	err := s.run(runCtx, &RunInput{AgentID: "analyzer", Objective: "analyze code"}, &out)
	assert.NoError(t, err)

	// The child's QueryInput should inherit the parent's model.
	require.NotNil(t, fake.lastInput, "expected QueryInput to be captured")
	assert.Equal(t, parentModel, fake.lastInput.ModelOverride,
		"child agent should inherit parent conversation's default model")
}

// TestService_Run_Internal_DoesNotOverrideChildModel verifies that when the
// child agent has its own explicitly configured model, the parent's model
// does NOT override it.
func TestService_Run_Internal_DoesNotOverrideChildModel(t *testing.T) {
	conv := convmem.New()
	ctx := context.Background()

	parentConv := convcli.NewConversation()
	parentConv.SetId("parent-conv")
	parentModel := "openai/gpt-5.2"
	parentConv.SetDefaultModel(parentModel)
	require.NoError(t, conv.PatchConversations(ctx, parentConv))

	runCtx := memory.WithTurnMeta(
		memory.WithConversationID(ctx, "parent-conv"),
		memory.TurnMeta{ConversationID: "parent-conv", TurnID: "turn-1"},
	)

	// Child agent WITH an explicit model configured
	fake := &fakeAgentRuntime{
		finder: &fakeFinder{agents: map[string]*agentmdl.Agent{
			"specialist": {
				Identity:       agentmdl.Identity{ID: "specialist"},
				ModelSelection: llm.ModelSelection{Model: "anthropic/claude-sonnet"},
			},
		}},
	}
	s := &Service{agent: fake, conv: conv}

	var out RunOutput
	err := s.run(runCtx, &RunInput{AgentID: "specialist", Objective: "specialize"}, &out)
	assert.NoError(t, err)

	require.NotNil(t, fake.lastInput, "expected QueryInput to be captured")
	// Child has its own model — parent's model should NOT override.
	assert.Empty(t, fake.lastInput.ModelOverride,
		"child with explicit model should not get parent's model override")
}

// TestService_Run_StatusToolNameFormat verifies that the status message tool
// name uses colon separator (llm/agents:run) instead of dash (llm/agents-run)
// so the UI groups them consistently.
func TestService_Run_StatusToolNameFormat(t *testing.T) {
	conv := convmem.New()
	ctx := context.Background()

	parentConv := convcli.NewConversation()
	parentConv.SetId("parent-conv")
	require.NoError(t, conv.PatchConversations(ctx, parentConv))

	turn := convcli.NewTurn()
	turn.SetId("turn-1")
	turn.SetConversationID("parent-conv")
	require.NoError(t, conv.PatchTurn(ctx, turn))

	runCtx := memory.WithTurnMeta(
		memory.WithConversationID(ctx, "parent-conv"),
		memory.TurnMeta{ConversationID: "parent-conv", TurnID: "turn-1"},
	)

	fake := &fakeAgentRuntime{
		finder: &fakeFinder{agents: map[string]*agentmdl.Agent{
			"worker": {Identity: agentmdl.Identity{ID: "worker"}},
		}},
	}
	s := &Service{agent: fake, conv: conv}
	s.linker = nil // no linker → no child conversation created
	// Set up status service to capture the tool name
	// (we verify via the persisted message's ToolName field)

	var out RunOutput
	err := s.run(runCtx, &RunInput{AgentID: "worker", Objective: "work"}, &out)
	assert.NoError(t, err)

	// Verify no messages with dash-separator tool name exist
	gotConv, err := conv.GetConversation(ctx, "parent-conv")
	require.NoError(t, err)
	for _, tr := range gotConv.Transcript {
		if tr == nil {
			continue
		}
		for _, msg := range tr.Message {
			if msg == nil || msg.ToolName == nil {
				continue
			}
			assert.NotContains(t, *msg.ToolName, "agents-run",
				"status message tool name should use colon separator, not dash")
		}
	}
}
