package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	intakesvc "github.com/viant/agently-core/service/intake"
	forgeuisvc "github.com/viant/forge/backend/mcp/service"
)

type followupStubConversationClient struct {
	conversation *apiconv.Conversation
}

func (s *followupStubConversationClient) GetConversation(ctx context.Context, id string, options ...apiconv.Option) (*apiconv.Conversation, error) {
	if s == nil || s.conversation == nil {
		return nil, errors.New("not found")
	}
	return s.conversation, nil
}

func (s *followupStubConversationClient) GetConversations(ctx context.Context, input *apiconv.Input) ([]*apiconv.Conversation, error) {
	return nil, nil
}
func (s *followupStubConversationClient) PatchConversations(ctx context.Context, conversations *apiconv.MutableConversation) error {
	return nil
}
func (s *followupStubConversationClient) GetPayload(ctx context.Context, id string) (*apiconv.Payload, error) {
	return nil, nil
}
func (s *followupStubConversationClient) PatchPayload(ctx context.Context, payload *apiconv.MutablePayload) error {
	return nil
}
func (s *followupStubConversationClient) PatchMessage(ctx context.Context, message *apiconv.MutableMessage) error {
	return nil
}
func (s *followupStubConversationClient) GetMessage(ctx context.Context, id string, options ...apiconv.Option) (*apiconv.Message, error) {
	return nil, nil
}
func (s *followupStubConversationClient) GetMessageByElicitation(ctx context.Context, conversationID, elicitationID string) (*apiconv.Message, error) {
	return nil, nil
}
func (s *followupStubConversationClient) PatchModelCall(ctx context.Context, modelCall *apiconv.MutableModelCall) error {
	return nil
}
func (s *followupStubConversationClient) PatchToolCall(ctx context.Context, toolCall *apiconv.MutableToolCall) error {
	return nil
}
func (s *followupStubConversationClient) PatchTurn(ctx context.Context, turn *apiconv.MutableTurn) error {
	return nil
}
func (s *followupStubConversationClient) DeleteConversation(ctx context.Context, id string) error {
	return nil
}
func (s *followupStubConversationClient) DeleteMessage(ctx context.Context, conversationID, messageID string) error {
	return nil
}

func TestJaccardWordSimilarity(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		// We assert a range rather than exact float so the test stays stable
		// if tokenization is tweaked later.
		min, max float64
	}{
		{"identical", "analyze deal 146901 latency", "analyze deal 146901 latency", 0.99, 1.0},
		{"disjoint", "analyze deal 146901 latency", "explain feeders for project 42", 0.0, 0.15},
		{"mostly overlapping", "analyze deal 146901", "analyze deal 146901 impact", 0.6, 0.9},
		{"empty", "", "anything", 0.0, 0.0},
	}
	for _, tc := range cases {
		got := jaccardWordSimilarity(tc.a, tc.b)
		require.GreaterOrEqualf(t, got, tc.min, "%s: got %v", tc.name, got)
		require.LessOrEqualf(t, got, tc.max, "%s: got %v", tc.name, got)
	}
}

// TestShouldRunIntake_TriggerOff verifies that when TriggerOnTopicShift is
// false the sidecar fires on every turn, matching the pre-topic-shift
// default and the behaviour callers with the flag unset rely on.
func TestShouldRunIntake_TriggerOff(t *testing.T) {
	s := &Service{}
	cfg := &agentmdl.Intake{Enabled: true, TriggerOnTopicShift: false}
	got := s.shouldRunIntake(context.Background(), &QueryInput{Query: "anything"}, cfg)
	require.True(t, got, "trigger off must not suppress the sidecar")
}

// TestShouldRunIntake_Disabled: sanity check — disabled agent never runs
// regardless of other settings.
func TestShouldRunIntake_Disabled(t *testing.T) {
	s := &Service{}
	cfg := &agentmdl.Intake{Enabled: false, TriggerOnTopicShift: true}
	require.False(t, s.shouldRunIntake(context.Background(), &QueryInput{Query: "x"}, cfg))
}

// TestShouldRunIntake_TopicShift covers the branch logic once
// TriggerOnTopicShift is on. The conversation client is nil so
// previousUserMessage returns "" (first-turn branch) — we're asserting the
// decision tree, not the conversation fetch itself.
func TestShouldRunIntake_TopicShift_FirstTurn(t *testing.T) {
	s := &Service{}
	cfg := &agentmdl.Intake{Enabled: true, TriggerOnTopicShift: true, TopicShiftThreshold: 0.65}
	got := s.shouldRunIntake(context.Background(), &QueryInput{Query: "hello"}, cfg)
	require.True(t, got, "first turn (no previous) must run so we get baseline metadata")
}

func TestShouldRunIntake_ExplicitPromptProfileSkipsSidecar(t *testing.T) {
	s := &Service{}
	cfg := &agentmdl.Intake{Enabled: true, TriggerOnTopicShift: false}
	got := s.shouldRunIntake(context.Background(), &QueryInput{
		Query:           "delegated objective",
		PromptProfileId: "diagnostic_baseline",
	}, cfg)
	require.False(t, got, "explicit prompt profile should bypass agent-intake classification")
}

func TestShouldRunIntake_RerunsAfterPriorDirectActionEvenWithoutTopicShift(t *testing.T) {
	now := time.Now()
	intakeJSON := `{"classification":{"title":"Show ad order 2637048","intent":"troubleshoot_ad_order","confidence":0.92},"scope":{"values":{"adOrderId":"2637048"}},"directAction":{"toolName":"ui/view:open","inputJson":"{\"id\":\"order\",\"parameters\":{\"AdOrderId\":[2637048]}}","assistantText":"opened"}}`
	s := &Service{
		conversation: &stubProjectionBindingConversationClient{
			conversation: &apiconv.Conversation{Id: "conv-1", Transcript: []*agconv.TranscriptView{
				{
					Id: "turn-1",
					Message: []*agconv.MessageView{
						{Id: "msg-user-1", TurnId: strPtr("turn-1"), Role: "user", Type: "text", Content: strPtr("show my order 2637048"), CreatedAt: now},
						{Id: "msg-intake-1", TurnId: strPtr("turn-1"), Role: "assistant", Type: "text", Content: strPtr(intakeJSON), CreatedAt: now, Phase: strPtr("intake")},
					},
				},
			}},
		},
	}
	cfg := &agentmdl.Intake{Enabled: true, TriggerOnTopicShift: true, TopicShiftThreshold: 0.65}
	got := s.shouldRunIntake(context.Background(), &QueryInput{
		ConversationID: "conv-1",
		Query:          "show my order 2637048",
	}, cfg)
	require.True(t, got, "repeated deterministic UI-open asks must rerun intake when the prior turn carried a direct action")
}

func TestShouldRunIntake_RerunsAfterOlderMatchingDirectActionEvenIfImmediatePriorTurnDidNot(t *testing.T) {
	now := time.Now()
	intakeJSON := `{"classification":{"title":"Show ad order 2637048","intent":"troubleshoot_ad_order","confidence":0.92},"scope":{"values":{"adOrderId":"2637048"}},"directAction":{"toolName":"ui/view:open","inputJson":"{\"id\":\"order\",\"parameters\":{\"AdOrderId\":[2637048]}}","assistantText":"opened"}}`
	s := &Service{
		conversation: &stubProjectionBindingConversationClient{
			conversation: &apiconv.Conversation{Id: "conv-1", Transcript: []*agconv.TranscriptView{
				{
					Id: "turn-1",
					Message: []*agconv.MessageView{
						{Id: "msg-user-1", TurnId: strPtr("turn-1"), Role: "user", Type: "text", Content: strPtr("show my order 2637048"), CreatedAt: now},
						{Id: "msg-intake-1", TurnId: strPtr("turn-1"), Role: "assistant", Type: "text", Content: strPtr(intakeJSON), CreatedAt: now, Phase: strPtr("intake")},
					},
				},
				{
					Id: "turn-2",
					Message: []*agconv.MessageView{
						{Id: "msg-user-2", TurnId: strPtr("turn-2"), Role: "user", Type: "text", Content: strPtr("show my order 2637048"), CreatedAt: now.Add(time.Second)},
						{Id: "msg-assistant-2", TurnId: strPtr("turn-2"), Role: "assistant", Type: "text", Content: strPtr("I couldn't reopen it..."), CreatedAt: now.Add(time.Second)},
					},
				},
			}},
		},
	}
	cfg := &agentmdl.Intake{Enabled: true, TriggerOnTopicShift: true, TopicShiftThreshold: 0.65}
	got := s.shouldRunIntake(context.Background(), &QueryInput{
		ConversationID: "conv-1",
		Query:          "show my order 2637048",
	}, cfg)
	require.True(t, got, "repeated deterministic UI-open asks must rerun intake when an older matching turn carried a direct action")
}

func TestShouldRunIntake_RerunsForConcreteOrderOpenEvenWhenOrderIdChangesWithoutTopicShift(t *testing.T) {
	now := time.Now()
	s := &Service{
		conversation: &stubProjectionBindingConversationClient{
			conversation: &apiconv.Conversation{Id: "conv-1", Transcript: []*agconv.TranscriptView{
				{
					Id: "turn-1",
					Message: []*agconv.MessageView{
						{Id: "msg-user-1", TurnId: strPtr("turn-1"), Role: "user", Type: "text", Content: strPtr("show order 2656980"), CreatedAt: now},
						{Id: "msg-assistant-1", TurnId: strPtr("turn-1"), Role: "assistant", Type: "text", Content: strPtr("opened"), CreatedAt: now},
					},
				},
			}},
		},
	}
	cfg := &agentmdl.Intake{Enabled: true, TriggerOnTopicShift: true, TopicShiftThreshold: 0.65}
	got := s.shouldRunIntake(context.Background(), &QueryInput{
		ConversationID: "conv-1",
		Query:          "show order 2609393",
	}, cfg)
	require.True(t, got, "concrete order-open asks must rerun intake even when only the order id changed")
}

func TestMaybeRunIntakeSidecar_InjectsWorkspaceFollowUpDirectActionForOrderTabs(t *testing.T) {
	now := time.Now()
	s := &Service{
		conversation: &stubProjectionBindingConversationClient{
			conversation: &apiconv.Conversation{
				Id: "conv-1",
				Transcript: []*agconv.TranscriptView{
					workspaceFollowUpFocusTurn(
						now,
						"turn-focus",
						"order_2656980",
						"order_2609393",
					),
				},
			},
		},
	}
	testCases := []struct {
		query string
		tabID string
	}{
		{query: "show kpi", tabID: "kpiTab"},
		{query: "show delivery", tabID: "deliveryTab"},
		{query: "show hh metrics", tabID: "hhMetricsTab"},
		{query: "show household metrics", tabID: "hhMetricsTab"},
		{query: "show pacing", tabID: "pacingTab"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.query, func(t *testing.T) {
			input := &QueryInput{
				ConversationID: "conv-1",
				Query:          testCase.query,
				Agent:          &agentmdl.Agent{Intake: agentmdl.Intake{Enabled: true, ActivationRules: testOrderFollowUpRules()}},
			}

			s.maybeRunIntakeSidecar(context.Background(), input)

			tc := intakesvc.FromContext(input.Context)
			require.NotNil(t, tc)
			require.Equal(t, "ui/window:selectTab", tc.DirectAction.ToolName)
			require.Equal(t, "order_2656980", tc.DirectAction.Input["windowId"])
			require.Equal(t, "order", tc.DirectAction.Input["windowKey"])
			require.Equal(t, testCase.tabID, tc.DirectAction.Input["tabId"])
			require.Equal(t, "client-1", tc.DirectAction.Input["clientId"])
			require.Contains(t, strings.ToLower(tc.DirectAction.AssistantText), strings.TrimSpace(strings.TrimPrefix(strings.ToLower(testCase.query), "show ")))
		})
	}
}

func TestMaybeRunIntakeSidecar_InjectsWorkspaceFollowUpDirectActionForOrderControls(t *testing.T) {
	now := time.Now()
	s := &Service{
		conversation: &stubProjectionBindingConversationClient{
			conversation: &apiconv.Conversation{
				Id: "conv-1",
				Transcript: []*agconv.TranscriptView{
					workspaceFollowUpControlTurn(
						now,
						"turn-control",
						"order_2656980",
					),
				},
			},
		},
	}
	testCases := []struct {
		query     string
		controlID string
		value     string
	}{
		{query: "show today", controlID: "periodView", value: "today"},
		{query: "show yesterday", controlID: "periodView", value: "yesterday"},
		{query: "show 7d", controlID: "periodView", value: "7d"},
		{query: "show 30d", controlID: "periodView", value: "30d"},
		{query: "show hour", controlID: "granularity", value: "hour"},
		{query: "switch to hour", controlID: "granularity", value: "hour"},
		{query: "show day", controlID: "granularity", value: "day"},
		{query: "switch to day", controlID: "granularity", value: "day"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.query, func(t *testing.T) {
			input := &QueryInput{
				ConversationID: "conv-1",
				Query:          testCase.query,
				Agent:          &agentmdl.Agent{Intake: agentmdl.Intake{Enabled: true, ActivationRules: testOrderFollowUpRules()}},
			}

			s.maybeRunIntakeSidecar(context.Background(), input)

			tc := intakesvc.FromContext(input.Context)
			require.NotNil(t, tc)
			require.Equal(t, "ui/control:setValue", tc.DirectAction.ToolName)
			require.Equal(t, "order_2656980", tc.DirectAction.Input["windowId"])
			require.Equal(t, "order", tc.DirectAction.Input["windowKey"])
			require.Equal(t, testCase.controlID, tc.DirectAction.Input["controlId"])
			require.Equal(t, testCase.value, tc.DirectAction.Input["value"])
			require.Equal(t, "client-1", tc.DirectAction.Input["clientId"])
			require.Contains(t, strings.ToLower(tc.DirectAction.AssistantText), strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(testCase.query), "show "), "switch to ")))
		})
	}
}

func testOrderFollowUpRules() []agentmdl.ActivationRule {
	return []agentmdl.ActivationRule{
		{
			ID:        "order_followup_tabs",
			Mode:      "followup",
			Source:    "either",
			WindowKey: "order",
			Match: agentmdl.ActivationMatch{
				Patterns: []string{`(?i)^show\s+(.+)$`},
			},
			Prompting: agentmdl.ActivationPrompting{SuggestedProfileID: "workspace_ui"},
			SurfaceMatch: &agentmdl.ActivationSurfaceMatch{
				TabAliases: map[string]string{
					"kpi":               "kpiTab",
					"kpis":              "kpiTab",
					"delivery":          "deliveryTab",
					"hh metrics":        "hhMetricsTab",
					"household metrics": "hhMetricsTab",
					"pacing":            "pacingTab",
				},
			},
			Response: agentmdl.ActivationResponse{AssistantText: "Updated the open order summary to $1."},
		},
		{
			ID:        "order_followup_period_and_granularity",
			Mode:      "followup",
			Source:    "either",
			WindowKey: "order",
			Match: agentmdl.ActivationMatch{
				Patterns: []string{`(?i)^show\s+(.+)$`, `(?i)^switch\s+to\s+(.+)$`},
			},
			Prompting: agentmdl.ActivationPrompting{SuggestedProfileID: "workspace_ui"},
			SurfaceMatch: &agentmdl.ActivationSurfaceMatch{
				ControlAliases: map[string]agentmdl.ActivationSurfaceControlAlias{
					"today":     {ControlID: "periodView", Value: "today", ValueLabel: "Today"},
					"yesterday": {ControlID: "periodView", Value: "yesterday", ValueLabel: "Yesterday"},
					"7d":        {ControlID: "periodView", Value: "7d", ValueLabel: "7D"},
					"30d":       {ControlID: "periodView", Value: "30d", ValueLabel: "30D"},
					"hour":      {ControlID: "granularity", Value: "hour", ValueLabel: "Hour"},
					"day":       {ControlID: "granularity", Value: "day", ValueLabel: "Day"},
				},
			},
			Response: agentmdl.ActivationResponse{AssistantText: "Updated the open order summary to $1."},
		},
	}
}

func workspaceFollowUpFocusTurn(now time.Time, turnID, selectedWindowID, previousWindowID string) *agconv.TranscriptView {
	return &agconv.TranscriptView{
		Id: turnID,
		Message: []*agconv.MessageView{
			{
				Id:        "msg-user-" + turnID,
				TurnId:    strPtr(turnID),
				Role:      "user",
				Type:      "text",
				Content:   strPtr("focus on 2656980"),
				CreatedAt: now,
			},
			{
				Id:        "msg-assistant-" + turnID,
				TurnId:    strPtr(turnID),
				Role:      "assistant",
				Type:      "text",
				Content:   strPtr("Focused on ad order 2656980."),
				CreatedAt: now.Add(time.Second),
				ToolMessage: []*agconv.ToolMessageView{
					{
						Id:        "msg-tool-list-" + turnID,
						Type:      "tool_op",
						Content:   strPtr(`{"clientId":"client-1","focusedWindowId":"` + previousWindowID + `","items":[{"windowId":"` + selectedWindowID + `","windowKey":"order","presentation":"hosted","region":"chat.top","parentKey":"chat/new"},{"windowId":"` + previousWindowID + `","windowKey":"order","presentation":"hosted","region":"chat.top","parentKey":"chat/new"}]}`),
						CreatedAt: now.Add(2 * time.Second),
						ToolName:  strPtr("ui/window:list"),
						ToolCall: &agconv.ToolCallView{
							ToolName:       "ui/window:list",
							Status:         "completed",
							RequestPayload: &agconv.ModelCallStreamPayloadView{InlineBody: strPtr(`{"clientId":"client-1"}`), Compression: "none"},
							ResponsePayload: &agconv.ModelCallStreamPayloadView{
								InlineBody:  strPtr(`{"clientId":"client-1","focusedWindowId":"` + previousWindowID + `","items":[{"windowId":"` + selectedWindowID + `","windowKey":"order","presentation":"hosted","region":"chat.top","parentKey":"chat/new"},{"windowId":"` + previousWindowID + `","windowKey":"order","presentation":"hosted","region":"chat.top","parentKey":"chat/new"}]}`),
								Compression: "none",
							},
						},
					},
					{
						Id:        "msg-tool-show-" + turnID,
						Type:      "tool_op",
						Content:   strPtr(`{"clientId":"client-1","ok":true}`),
						CreatedAt: now.Add(3 * time.Second),
						ToolName:  strPtr("ui/window:show"),
						ToolCall: &agconv.ToolCallView{
							ToolName:       "ui/window:show",
							Status:         "completed",
							RequestPayload: &agconv.ModelCallStreamPayloadView{InlineBody: strPtr(`{"clientId":"client-1","windowId":"` + selectedWindowID + `","windowKey":"order"}`), Compression: "none"},
							ResponsePayload: &agconv.ModelCallStreamPayloadView{
								InlineBody:  strPtr(`{"clientId":"client-1","ok":true}`),
								Compression: "none",
							},
						},
					},
				},
			},
		},
	}
}

func workspaceFollowUpControlTurn(now time.Time, turnID, selectedWindowID string) *agconv.TranscriptView {
	return &agconv.TranscriptView{
		Id: turnID,
		Message: []*agconv.MessageView{
			{
				Id:        "msg-user-" + turnID,
				TurnId:    strPtr(turnID),
				Role:      "user",
				Type:      "text",
				Content:   strPtr("show 7d"),
				CreatedAt: now,
			},
			{
				Id:        "msg-assistant-" + turnID,
				TurnId:    strPtr(turnID),
				Role:      "assistant",
				Type:      "text",
				Content:   strPtr("Switched the open order summary period to 7D."),
				CreatedAt: now.Add(time.Second),
				ToolMessage: []*agconv.ToolMessageView{
					{
						Id:        "msg-tool-control-" + turnID,
						Type:      "tool_op",
						Content:   strPtr(`{"clientId":"client-1","ok":true}`),
						CreatedAt: now.Add(2 * time.Second),
						ToolName:  strPtr("ui/control:setValue"),
						ToolCall: &agconv.ToolCallView{
							ToolName:       "ui/control:setValue",
							Status:         "completed",
							RequestPayload: &agconv.ModelCallStreamPayloadView{InlineBody: strPtr(`{"clientId":"client-1","windowId":"` + selectedWindowID + `","windowKey":"order","controlId":"periodView","scope":"windowForm","value":"7d"}`), Compression: "none"},
							ResponsePayload: &agconv.ModelCallStreamPayloadView{
								InlineBody:  strPtr(`{"clientId":"client-1","ok":true}`),
								Compression: "none",
							},
						},
					},
				},
			},
		},
	}
}

// TestApplyTurnContext_CopiesTemplateIdToInput closes the gap where the
// sidecar suggested a template but `applySelectedTemplate` read input.TemplateId
// and never saw the suggestion because it was only stored under
// input.Context["intake.templateId"].
func TestApplyTurnContext_CopiesTemplateIdToInput(t *testing.T) {
	cfg := &agentmdl.Intake{
		Enabled: true,
		Scope:   []string{agentmdl.IntakeScopeTemplate},
	}
	input := &QueryInput{}
	tc := &intakesvc.Context{Prompting: intakesvc.PromptingContext{TemplateID: "report_v2"}}
	applyTurnContext(input, tc, cfg)

	require.Equal(t, "report_v2", input.TemplateId, "intake suggestion must land on input.TemplateId")
	require.Equal(t, "report_v2", input.Context["intake.templateId"], "context observability key must still be populated")
}

// TestApplyTurnContext_TemplateIdDoesNotOverrideCaller: if the caller has
// already chosen a template, the intake suggestion must not silently
// override that choice.
func TestApplyTurnContext_TemplateIdDoesNotOverrideCaller(t *testing.T) {
	cfg := &agentmdl.Intake{
		Enabled: true,
		Scope:   []string{agentmdl.IntakeScopeTemplate},
	}
	input := &QueryInput{TemplateId: "caller_choice"}
	tc := &intakesvc.Context{Prompting: intakesvc.PromptingContext{TemplateID: "sidecar_suggestion"}}
	applyTurnContext(input, tc, cfg)

	require.Equal(t, "caller_choice", input.TemplateId, "caller-chosen template must win")
	require.Equal(t, "sidecar_suggestion", input.Context["intake.templateId"], "context still records the sidecar's suggestion for observability")
}

// TestApplyTurnContext_ProfileSuggestionGated verifies that profile
// suggestions below the confidence threshold do NOT leak into context and
// that high-confidence ones do.
func TestApplyTurnContext_ProfileSuggestionGated(t *testing.T) {
	cfg := &agentmdl.Intake{
		Enabled:             true,
		Scope:               []string{agentmdl.IntakeScopeProfile},
		ConfidenceThreshold: 0.8,
	}

	// Low confidence — suggestion is suppressed.
	low := &QueryInput{}
	applyTurnContext(low, &intakesvc.Context{
		Prompting:      intakesvc.PromptingContext{SuggestedProfileID: "deal_impact"},
		Classification: intakesvc.ClassificationContext{Confidence: 0.5},
	}, cfg)
	_, hasLow := low.Context["intake.suggestedProfileId"]
	require.False(t, hasLow, "below-threshold suggestions must not land in context")

	// High confidence — suggestion + confidence surface in context.
	high := &QueryInput{}
	applyTurnContext(high, &intakesvc.Context{
		Prompting:      intakesvc.PromptingContext{SuggestedProfileID: "deal_impact"},
		Classification: intakesvc.ClassificationContext{Confidence: 0.9},
	}, cfg)
	require.Equal(t, "deal_impact", high.Context["intake.suggestedProfileId"])
	require.InDelta(t, 0.9, high.Context["intake.suggestedProfileConfidence"], 0.001)
	require.Equal(t, "deal_impact", high.PromptProfileId)
}

func TestNormalizeIntakeTurnContext_SuppressesTemplateIdsInSuggestedProfile(t *testing.T) {
	svc := &Service{}
	cfg := &agentmdl.Intake{
		Enabled:             true,
		Scope:               []string{agentmdl.IntakeScopeProfile, agentmdl.IntakeScopeTemplate},
		ConfidenceThreshold: 0.8,
	}
	input := &QueryInput{
		ConversationID: "conv-1",
		Agent: &agentmdl.Agent{
			Identity: agentmdl.Identity{ID: "steward"},
			Prompts: agentmdl.PromptAccess{
				Bundles: []string{"inventory_diagnosis", "performance_analysis"},
			},
		},
	}
	tc := &intakesvc.Context{
		Prompting: intakesvc.PromptingContext{
			SuggestedProfileID: "spo_path_planner",
			TemplateID:         "spo_path_planner",
		},
		Classification: intakesvc.ClassificationContext{Confidence: 0.91},
	}

	svc.normalizeIntakeTurnContext(context.Background(), input, tc, cfg)

	require.Empty(t, tc.Prompting.SuggestedProfileID, "template ids must not survive as prompt-profile suggestions")
	require.Zero(t, tc.Classification.Confidence, "invalid profile suggestions should not retain routing confidence")
	require.Equal(t, "spo_path_planner", tc.Prompting.TemplateID, "template choice remains valid")
}

func TestApplyTurnContext_PromptProfileDoesNotOverrideCaller(t *testing.T) {
	cfg := &agentmdl.Intake{
		Enabled:             true,
		Scope:               []string{agentmdl.IntakeScopeProfile},
		ConfidenceThreshold: 0.8,
	}
	input := &QueryInput{PromptProfileId: "caller_choice"}
	tc := &intakesvc.Context{
		Prompting:      intakesvc.PromptingContext{SuggestedProfileID: "repo_analysis"},
		Classification: intakesvc.ClassificationContext{Confidence: 0.95},
	}
	applyTurnContext(input, tc, cfg)

	require.Equal(t, "caller_choice", input.PromptProfileId)
	require.Equal(t, "repo_analysis", input.Context["intake.suggestedProfileId"])
}

func TestApplyTurnContext_PreservesWorkspaceMode(t *testing.T) {
	cfg := &agentmdl.Intake{
		Enabled: true,
		Scope:   []string{agentmdl.IntakeScopeTitle, agentmdl.IntakeScopeProfile},
	}
	input := &QueryInput{
		Context: map[string]interface{}{
			intakesvc.ContextKey: &intakesvc.Context{
				Routing: intakesvc.RoutingContext{
					Mode:            intakesvc.ModePlanner,
					SelectedAgentID: "coder",
					Source:          intakesvc.SourceWorkspace,
				},
				Planner: intakesvc.PlannerContext{
					AgentID: "steward_planner",
				},
			},
		},
	}
	tc := &intakesvc.Context{
		Classification: intakesvc.ClassificationContext{
			Title:      "repo diagnosis",
			Confidence: 0.95,
		},
		Prompting: intakesvc.PromptingContext{
			SuggestedProfileID: "repo_analysis",
		},
	}
	applyTurnContext(input, tc, cfg)

	stored := intakesvc.FromContext(input.Context)
	require.NotNil(t, stored)
	require.Equal(t, intakesvc.ModePlanner, stored.Routing.Mode)
	require.Equal(t, "coder", stored.Routing.SelectedAgentID)
	require.Equal(t, intakesvc.SourceWorkspace, stored.Routing.Source)
	require.Equal(t, "steward_planner", stored.Planner.AgentID)
	require.Equal(t, "repo diagnosis", stored.Classification.Title)
	require.Equal(t, "repo_analysis", stored.Prompting.SuggestedProfileID)
}

func TestApplyTurnContext_PreservesExplicitPlannerModeFromIntake(t *testing.T) {
	cfg := &agentmdl.Intake{
		Enabled:                  true,
		PlannerEnabled:           true,
		PlannerAgentID:           "steward_planner",
		PlannerOnCreativeRequest: true,
		PlannerTriggerPhrases:    []string{"exploratory", "multi-angle"},
		Scope:                    []string{agentmdl.IntakeScopeContext, agentmdl.IntakeScopeProfile, agentmdl.IntakeScopeTemplate},
		ConfidenceThreshold:      0.8,
	}
	input := &QueryInput{
		ConversationID: "conv-exploratory",
		AgentID:        "steward",
		Query:          "review audience targeting strategy, use exploratory strategy",
		Agent: &agentmdl.Agent{
			Identity: agentmdl.Identity{ID: "steward"},
		},
	}
	tc := &intakesvc.Context{
		Classification: intakesvc.ClassificationContext{
			Title:      "Review audience targeting strategy with exploratory approach",
			Confidence: 0.86,
		},
		Prompting: intakesvc.PromptingContext{
			SuggestedProfileID: "diagnostic_baseline",
			TemplateID:         "analytics_dashboard",
		},
		Routing: intakesvc.RoutingContext{
			Mode:            intakesvc.ModePlanner,
			SelectedAgentID: "steward",
			Source:          intakesvc.SourceAgent,
		},
		Planner: intakesvc.PlannerContext{
			Trigger: "exploratory_strategy",
		},
		Scope: intakesvc.ScopeContext{
			Values: map[string]string{
				"use_exploratory_strategy": "true",
				"approach":                 "exploratory",
			},
		},
	}

	applyTurnContext(input, tc, cfg)

	stored := intakesvc.FromContext(input.Context)
	require.NotNil(t, stored)
	require.Equal(t, intakesvc.ModePlanner, stored.Routing.Mode)
	require.Equal(t, "steward", stored.Routing.SelectedAgentID)
	require.Equal(t, intakesvc.SourceAgent, stored.Routing.Source)
	require.Equal(t, "steward_planner", stored.Planner.AgentID)
	require.Equal(t, "exploratory_strategy", stored.Planner.Trigger)
	require.Equal(t, "diagnostic_baseline", input.PromptProfileId)
	require.Equal(t, "analytics_dashboard", input.TemplateId)
}

func TestApplyTurnContext_SkipsPlannerModeForCreativeConcreteTroubleshoot(t *testing.T) {
	cfg := &agentmdl.Intake{
		Enabled:                  true,
		PlannerEnabled:           true,
		PlannerAgentID:           "steward_planner",
		PlannerOnCreativeRequest: true,
		PlannerTriggerPhrases:    []string{"exploratory", "multi-angle"},
		Scope:                    []string{agentmdl.IntakeScopeContext, agentmdl.IntakeScopeProfile, agentmdl.IntakeScopeTemplate},
		ConfidenceThreshold:      0.8,
	}
	input := &QueryInput{
		ConversationID: "conv-troubleshoot-creative",
		AgentID:        "steward",
		Query:          "troubleshoot order 2657966, use exploratory strategy",
		Agent: &agentmdl.Agent{
			Identity: agentmdl.Identity{ID: "steward"},
		},
	}
	tc := &intakesvc.Context{
		Classification: intakesvc.ClassificationContext{
			Title:      "Troubleshoot ad order delivery",
			Intent:     "troubleshoot_ad_order",
			Confidence: 0.92,
		},
		Prompting: intakesvc.PromptingContext{
			SuggestedProfileID: "diagnostic_baseline",
			TemplateID:         "analytics_dashboard",
		},
		DirectAction: intakesvc.DirectActionContext{
			ToolName: "ui/view:open",
			Input: map[string]interface{}{
				"id": "order",
			},
		},
		Scope: intakesvc.ScopeContext{
			Values: map[string]string{
				"use_exploratory_strategy": "true",
				"approach":                 "exploratory",
			},
		},
	}

	applyTurnContext(input, tc, cfg)

	stored := intakesvc.FromContext(input.Context)
	require.NotNil(t, stored)
	require.NotEqual(t, intakesvc.ModePlanner, stored.Routing.Mode)
	require.Empty(t, stored.Planner.Trigger)
	require.Equal(t, "diagnostic_baseline", input.PromptProfileId)
	require.Equal(t, "analytics_dashboard", input.TemplateId)
}

func TestApplyTurnContext_SkipsPlannerModeForCreativeConcreteTroubleshoot_LowConfidence(t *testing.T) {
	cfg := &agentmdl.Intake{
		Enabled:                  true,
		PlannerEnabled:           true,
		PlannerAgentID:           "steward_planner",
		PlannerOnCreativeRequest: true,
		PlannerTriggerPhrases:    []string{"exploratory", "multi-angle"},
		Scope:                    []string{agentmdl.IntakeScopeContext, agentmdl.IntakeScopeProfile, agentmdl.IntakeScopeTemplate},
		ConfidenceThreshold:      0.8,
	}
	input := &QueryInput{
		ConversationID: "conv-troubleshoot-creative-low",
		AgentID:        "steward",
		Query:          "troubleshoot order 2657966, use exploratory strategy",
		Agent: &agentmdl.Agent{
			Identity: agentmdl.Identity{ID: "steward"},
		},
	}
	tc := &intakesvc.Context{
		Classification: intakesvc.ClassificationContext{
			Title:      "Troubleshoot ad order with exploratory strategy",
			Intent:     "troubleshoot_ad_order",
			Confidence: 0.66,
		},
		Prompting: intakesvc.PromptingContext{
			SuggestedProfileID: "creative_recommendation",
			TemplateID:         "analytics_dashboard",
		},
		DirectAction: intakesvc.DirectActionContext{
			ToolName: "ui/view:open",
			Input: map[string]interface{}{
				"id": "order",
			},
		},
		Scope: intakesvc.ScopeContext{
			Values: map[string]string{
				"use_exploratory_strategy": "true",
				"approach":                 "exploratory",
			},
		},
	}

	applyTurnContext(input, tc, cfg)

	stored := intakesvc.FromContext(input.Context)
	require.NotNil(t, stored)
	require.NotEqual(t, intakesvc.ModePlanner, stored.Routing.Mode)
	require.Empty(t, stored.Planner.Trigger)
}

func TestApplyTurnContext_SkipsPlannerModeForExploratoryBoundedTopN(t *testing.T) {
	cfg := &agentmdl.Intake{
		Enabled:                  true,
		PlannerEnabled:           true,
		PlannerAgentID:           "steward_planner",
		PlannerOnCreativeRequest: true,
		PlannerTriggerPhrases:    []string{"exploratory", "multi-angle"},
		Scope:                    []string{agentmdl.IntakeScopeContext, agentmdl.IntakeScopeProfile, agentmdl.IntakeScopeTemplate},
		ConfidenceThreshold:      0.8,
	}
	input := &QueryInput{
		ConversationID: "conv-topn-exploratory",
		AgentID:        "steward",
		Query:          "show the most 3 impactful deal ids in the last 2 days, use exploratory strategy",
		Agent: &agentmdl.Agent{
			Identity: agentmdl.Identity{ID: "steward"},
		},
	}
	tc := &intakesvc.Context{
		Classification: intakesvc.ClassificationContext{
			Title:      "Top 3 impactful deal ids in last 2 days",
			Intent:     "identify_top_deals_by_impact",
			Confidence: 0.84,
		},
		Prompting: intakesvc.PromptingContext{
			SuggestedProfileID: "supply_kpi",
			TemplateID:         "analytics_dashboard",
		},
		DirectAction: intakesvc.DirectActionContext{
			ToolName: "ui/view:open",
			Input: map[string]interface{}{
				"id": "supply-kpi-dashboard",
			},
		},
		Scope: intakesvc.ScopeContext{
			Values: map[string]string{
				"use_exploratory_strategy": "true",
				"approach":                 "exploratory",
				"request_type":             "top_deals_rank",
				".metric":                  "impactful_deals",
			},
		},
	}

	applyTurnContext(input, tc, cfg)

	stored := intakesvc.FromContext(input.Context)
	require.NotNil(t, stored)
	require.NotEqual(t, intakesvc.ModePlanner, stored.Routing.Mode)
	require.Empty(t, stored.Planner.Trigger)
	require.Equal(t, "supply_kpi", input.PromptProfileId)
	require.Equal(t, "analytics_dashboard", input.TemplateId)
}

func TestApplyTurnContext_DoesNotEnablePlannerModeForLowConfidenceDirectAgentRequest(t *testing.T) {
	cfg := &agentmdl.Intake{
		Enabled:                  true,
		PlannerEnabled:           true,
		PlannerAgentID:           "steward_planner",
		PlannerFallbackThreshold: 0.7,
		PlannerOnCreativeRequest: true,
		Scope:                    []string{agentmdl.IntakeScopeContext, agentmdl.IntakeScopeProfile},
		ConfidenceThreshold:      0.8,
	}
	input := &QueryInput{
		ConversationID: "conv-low-confidence",
		AgentID:        "steward",
		Query:          "run forecast for audience 7268995 with a non-standard review",
		Agent: &agentmdl.Agent{
			Identity: agentmdl.Identity{ID: "steward"},
		},
	}
	tc := &intakesvc.Context{
		Classification: intakesvc.ClassificationContext{
			Title:      "Run forecast for audience",
			Confidence: 0.52,
		},
		DirectAction: intakesvc.DirectActionContext{
			ToolName: "ui/view:open",
			Input: map[string]interface{}{
				"id": "audience-forecast",
			},
		},
		Scope: intakesvc.ScopeContext{
			Values: map[string]string{},
		},
	}

	applyTurnContext(input, tc, cfg)

	stored := intakesvc.FromContext(input.Context)
	require.NotNil(t, stored)
	require.NotEqual(t, intakesvc.ModePlanner, stored.Routing.Mode)
	require.Empty(t, stored.Planner.Trigger)
}

func TestApplyTurnContext_SkipsPlannerModeForConcreteForecast_LowConfidence(t *testing.T) {
	cfg := &agentmdl.Intake{
		Enabled:                  true,
		PlannerEnabled:           true,
		PlannerAgentID:           "steward_planner",
		PlannerFallbackThreshold: 0.7,
		PlannerOnCreativeRequest: true,
		Scope:                    []string{agentmdl.IntakeScopeContext, agentmdl.IntakeScopeProfile, agentmdl.IntakeScopeTemplate},
		ConfidenceThreshold:      0.8,
	}
	input := &QueryInput{
		ConversationID: "conv-forecast-low-confidence",
		AgentID:        "steward",
		Query:          "forecase line 7272328",
		Agent: &agentmdl.Agent{
			Identity: agentmdl.Identity{ID: "steward"},
		},
	}
	tc := &intakesvc.Context{
		Classification: intakesvc.ClassificationContext{
			Title:      "Forecast request for line 7272328",
			Intent:     "forecast",
			Confidence: 0.6,
		},
		Prompting: intakesvc.PromptingContext{
			TemplateID: "audience_forecast_dashboard",
		},
		DirectAction: intakesvc.DirectActionContext{
			ToolName: "ui/view:open",
			Input: map[string]interface{}{
				"id": "audience_forecast_dashboard",
			},
		},
		Scope: intakesvc.ScopeContext{
			Values: map[string]string{},
		},
	}

	applyTurnContext(input, tc, cfg)

	stored := intakesvc.FromContext(input.Context)
	require.NotNil(t, stored)
	require.NotEqual(t, intakesvc.ModePlanner, stored.Routing.Mode)
	require.Empty(t, stored.Planner.Trigger)
	require.Equal(t, "audience_forecast_dashboard", input.TemplateId)
}

func TestApplyTurnContext_SkipsPlannerModeForClarification_LowConfidence(t *testing.T) {
	cfg := &agentmdl.Intake{
		Enabled:                  true,
		PlannerEnabled:           true,
		PlannerAgentID:           "steward_planner",
		PlannerFallbackThreshold: 0.7,
		PlannerOnCreativeRequest: true,
		Scope:                    []string{agentmdl.IntakeScopeContext, agentmdl.IntakeScopeProfile},
		ConfidenceThreshold:      0.8,
	}
	input := &QueryInput{
		ConversationID: "conv-clarification-low-confidence",
		AgentID:        "steward",
		Query:          "did you listed all order ?",
		Agent: &agentmdl.Agent{
			Identity: agentmdl.Identity{ID: "steward"},
		},
	}
	tc := &intakesvc.Context{
		Classification: intakesvc.ClassificationContext{
			Title:      "Confirm whether all orders were listed",
			Intent:     "clarification",
			Confidence: 0.66,
		},
	}

	applyTurnContext(input, tc, cfg)

	stored := intakesvc.FromContext(input.Context)
	require.NotNil(t, stored)
	require.NotEqual(t, intakesvc.ModePlanner, stored.Routing.Mode)
	require.Empty(t, stored.Planner.Trigger)
}

func TestIntakeTrackedContext_UsesRouterModeAndTrackedTurn(t *testing.T) {
	recorder := &intakeRecordingConvClient{}
	svc := &Service{conversation: recorder}

	ctx := context.Background()
	ctx = runtimerequestctx.WithConversationID(ctx, "conv-1")
	ctx = runtimerequestctx.WithTurnMeta(ctx, runtimerequestctx.TurnMeta{
		ConversationID: "conv-1",
		TurnID:         "turn-1",
	})

	runCtx := svc.intakeTrackedContext(ctx, &QueryInput{ConversationID: "conv-1"})

	require.Equal(t, "router", runtimerequestctx.RequestModeFromContext(runCtx))
	turn, ok := runtimerequestctx.TurnMetaFromContext(runCtx)
	require.True(t, ok)
	require.Equal(t, "conv-1", turn.ConversationID)
	require.Equal(t, "turn-1", turn.TurnID)
	require.Equal(t, "intake_sidecar", turn.Assistant)

	require.NotNil(t, recorder.lastTurn)
	require.Equal(t, "turn-1", recorder.lastTurn.Id)
	require.Equal(t, "conv-1", recorder.lastTurn.ConversationID)
}

type intakeRecordingConvClient struct {
	lastTurn       *apiconv.MutableTurn
	lastMessage    *apiconv.MutableMessage
	lastMessageAdd bool
}

func (r *intakeRecordingConvClient) GetConversation(context.Context, string, ...apiconv.Option) (*apiconv.Conversation, error) {
	return nil, nil
}
func (r *intakeRecordingConvClient) GetConversations(context.Context, *apiconv.Input) ([]*apiconv.Conversation, error) {
	return nil, nil
}
func (r *intakeRecordingConvClient) PatchConversations(context.Context, *apiconv.MutableConversation) error {
	return nil
}
func (r *intakeRecordingConvClient) GetPayload(context.Context, string) (*apiconv.Payload, error) {
	return nil, nil
}
func (r *intakeRecordingConvClient) PatchPayload(context.Context, *apiconv.MutablePayload) error {
	return nil
}
func (r *intakeRecordingConvClient) PatchMessage(ctx context.Context, m *apiconv.MutableMessage) error {
	r.lastMessage = m
	r.lastMessageAdd = runtimerequestctx.MessageAddEventFromContext(ctx)
	return nil
}
func (r *intakeRecordingConvClient) GetMessage(context.Context, string, ...apiconv.Option) (*apiconv.Message, error) {
	return nil, nil
}
func (r *intakeRecordingConvClient) GetMessageByElicitation(context.Context, string, string) (*apiconv.Message, error) {
	return nil, nil
}
func (r *intakeRecordingConvClient) PatchModelCall(context.Context, *apiconv.MutableModelCall) error {
	return nil
}
func (r *intakeRecordingConvClient) PatchToolCall(context.Context, *apiconv.MutableToolCall) error {
	return nil
}
func (r *intakeRecordingConvClient) PatchTurn(_ context.Context, turn *apiconv.MutableTurn) error {
	r.lastTurn = turn
	return nil
}
func (r *intakeRecordingConvClient) DeleteConversation(context.Context, string) error {
	return nil
}
func (r *intakeRecordingConvClient) DeleteMessage(context.Context, string, string) error {
	return nil
}

// TestPublishPresetAssistantMessage verifies that the workspace-intake preset
// short-circuit ("ONE LLM call for capability turns") writes the assistant
// message through the standard conversation client. PatchMessage is the
// canonical write path — it persists to the DB and emits the
// streaming.EventTypeAssistant SSE event automatically (verified at
// internal/service/conversation/service.go:800–822). Asserting the message
// shape proves both DB write and SSE wiring are correct.
func TestPublishPresetAssistantMessage(t *testing.T) {
	t.Run("answer kind writes assistant message with status hint", func(t *testing.T) {
		recorder := &intakeRecordingConvClient{}
		s := &Service{conversation: recorder}
		input := &QueryInput{
			ConversationID: "conv-1",
			MessageID:      "turn-1",
		}
		err := s.publishPresetAssistantMessage(context.Background(), input, "## Summary\nWorkspace can do X.", "answer")
		require.NoError(t, err)
		require.NotNil(t, recorder.lastMessage)
		require.NotEmpty(t, recorder.lastMessage.Id, "message id must be set")
		require.Equal(t, "conv-1", recorder.lastMessage.ConversationID)
		require.NotNil(t, recorder.lastMessage.TurnID)
		require.Equal(t, "turn-1", *recorder.lastMessage.TurnID)
		require.Equal(t, "assistant", recorder.lastMessage.Role)
		require.Equal(t, "text", recorder.lastMessage.Type)
		require.NotNil(t, recorder.lastMessage.Content)
		require.Equal(t, "## Summary\nWorkspace can do X.", *recorder.lastMessage.Content)
		require.NotNil(t, recorder.lastMessage.Status)
		require.Equal(t, "intake.answer", *recorder.lastMessage.Status)
		require.True(t, recorder.lastMessageAdd, "preset assistant write must be marked as an explicit message add for SSE")
	})

	t.Run("clarify kind writes with intake.clarify status", func(t *testing.T) {
		recorder := &intakeRecordingConvClient{}
		s := &Service{conversation: recorder}
		input := &QueryInput{ConversationID: "c", MessageID: "t"}
		err := s.publishPresetAssistantMessage(context.Background(), input, "Which order?", "clarify")
		require.NoError(t, err)
		require.NotNil(t, recorder.lastMessage)
		require.NotNil(t, recorder.lastMessage.Content)
		require.Equal(t, "Which order?", *recorder.lastMessage.Content)
		require.NotNil(t, recorder.lastMessage.Status)
		require.Equal(t, "intake.clarify", *recorder.lastMessage.Status)
		require.True(t, recorder.lastMessageAdd, "clarification assistant write must be marked as an explicit message add for SSE")
	})

	t.Run("nil conversation client is a no-op (callers still get output.Content)", func(t *testing.T) {
		s := &Service{conversation: nil}
		input := &QueryInput{ConversationID: "c", MessageID: "t"}
		err := s.publishPresetAssistantMessage(context.Background(), input, "x", "answer")
		require.NoError(t, err, "missing conversation client must not break the short-circuit")
	})

	t.Run("empty text is a no-op", func(t *testing.T) {
		recorder := &intakeRecordingConvClient{}
		s := &Service{conversation: recorder}
		input := &QueryInput{ConversationID: "c", MessageID: "t"}
		err := s.publishPresetAssistantMessage(context.Background(), input, "  ", "answer")
		require.NoError(t, err)
		require.Nil(t, recorder.lastMessage, "empty text must not write a stub message")
	})

	t.Run("kind missing leaves status unset", func(t *testing.T) {
		recorder := &intakeRecordingConvClient{}
		s := &Service{conversation: recorder}
		input := &QueryInput{ConversationID: "c", MessageID: "t"}
		err := s.publishPresetAssistantMessage(context.Background(), input, "answer text", "")
		require.NoError(t, err)
		require.NotNil(t, recorder.lastMessage)
		require.Nil(t, recorder.lastMessage.Status, "no kind hint → no status field")
		require.True(t, recorder.lastMessageAdd, "assistant write without kind must still be marked as an explicit message add for SSE")
	})
}

// TestMaybeRunIntakeSidecar_CallerProvidedOverride verifies skip rule §2.c:
// when input.Context already holds an intake Context with
// Source=SourceCallerProvided, the sidecar must skip its LLM call entirely
// (no panic on nil intakeSvc) and still apply the merge logic so that
// suggested template / profile / bundles take effect.
func TestMaybeRunIntakeSidecar_CallerProvidedOverride(t *testing.T) {
	t.Run("skips sidecar and applies override", func(t *testing.T) {
		// Service with intakeSvc==nil. If our skip rule fires correctly we
		// never enter the sidecar branch, so nil is safe. If the skip rule
		// is broken we panic on nil.intakeSvc.Run.
		s := &Service{}

		override := &intakesvc.Context{
			Classification: intakesvc.ClassificationContext{
				Title:      "caller-supplied",
				Intent:     "capacity_review",
				Confidence: 0.94,
			},
			Routing: intakesvc.RoutingContext{
				SelectedAgentID: "analyst",
				Mode:            intakesvc.ModeRoute,
				Source:          intakesvc.SourceCallerProvided,
			},
			Prompting: intakesvc.PromptingContext{
				TemplateID:         "capacity_review_dashboard",
				SuggestedProfileID: "analyst-forecast",
				AppendToolBundles:  []string{"forecast"},
			},
		}

		input := &QueryInput{
			Agent: &agentmdl.Agent{
				Intake: agentmdl.Intake{
					Enabled: true,
					Scope:   []string{agentmdl.IntakeScopeTemplate, agentmdl.IntakeScopeProfile, agentmdl.IntakeScopeTools},
				},
			},
			Query:   "review task 2652067",
			Context: map[string]interface{}{intakesvc.ContextKey: override},
		}

		// Should not panic and should apply the override.
		s.maybeRunIntakeSidecar(context.Background(), input)

		require.Equal(t, "capacity_review_dashboard", input.TemplateId,
			"caller-provided template must land on input.TemplateId")
		require.Contains(t, input.ToolBundles, "forecast",
			"caller-provided AppendToolBundles must merge into input.ToolBundles")
	})

	t.Run("non-caller-provided source does not trigger skip path", func(t *testing.T) {
		// An intake Context with a different Source (e.g. agent-side cached
		// reuse) must NOT trip the caller-provided early return — that path
		// is reserved for explicit caller overrides only.
		s := &Service{} // nil intakeSvc means non-caller-provided falls through to "intakeSvc == nil" return

		other := &intakesvc.Context{
			Classification: intakesvc.ClassificationContext{Title: "from-elsewhere"},
			Routing:        intakesvc.RoutingContext{Source: intakesvc.SourceReused}, // not "caller-provided"
		}
		input := &QueryInput{
			Agent: &agentmdl.Agent{
				Intake: agentmdl.Intake{Enabled: true},
			},
			Query:      "hello",
			Context:    map[string]interface{}{intakesvc.ContextKey: other},
			TemplateId: "preset",
		}

		// Must not panic; falls through and exits because intakeSvc is nil.
		// Importantly: it does NOT call applyTurnContext, so input.TemplateId
		// stays at "preset" rather than being overridden by the non-caller TC.
		s.maybeRunIntakeSidecar(context.Background(), input)
		require.Equal(t, "preset", input.TemplateId,
			"non-caller-provided source must not enter the override apply path")
	})

	t.Run("nil context is a no-op", func(t *testing.T) {
		s := &Service{}
		input := &QueryInput{
			Agent: &agentmdl.Agent{
				Intake: agentmdl.Intake{Enabled: true},
			},
			Query:   "hello",
			Context: nil,
		}
		s.maybeRunIntakeSidecar(context.Background(), input)
		// No panic, no template applied.
		require.Equal(t, "", input.TemplateId)
	})
}

// TestStoreCallerProvided_AnnotatesSourceAndStores verifies the helper
// produces an isolated copy with Source=SourceCallerProvided and stores it
// under the well-known key. This is the primary contract that
// run_support.go relies on.
func TestStoreCallerProvided_AnnotatesSourceAndStores(t *testing.T) {
	t.Run("populates ContextKey with copy", func(t *testing.T) {
		original := &intakesvc.Context{
			Routing: intakesvc.RoutingContext{
				SelectedAgentID: "forecaster",
				Mode:            intakesvc.ModeRoute,
				Source:          "", // not yet annotated
			},
			Classification: intakesvc.ClassificationContext{Title: "do the thing"},
		}
		ctxMap, stored := intakesvc.StoreCallerProvided(nil, original)
		require.NotNil(t, ctxMap)
		require.NotNil(t, stored)
		require.Equal(t, intakesvc.SourceCallerProvided, stored.Routing.Source,
			"stored copy must be annotated as caller-provided")
		require.Equal(t, "", original.Routing.Source,
			"original caller struct must not be mutated")
		require.Same(t, stored, ctxMap[intakesvc.ContextKey],
			"stored value must be findable under ContextKey")
	})

	t.Run("nil override leaves map untouched", func(t *testing.T) {
		ctxMap, stored := intakesvc.StoreCallerProvided(map[string]any{"existing": "value"}, nil)
		require.Nil(t, stored)
		require.Equal(t, "value", ctxMap["existing"])
		require.NotContains(t, ctxMap, intakesvc.ContextKey)
	})

	t.Run("FromContext round-trips", func(t *testing.T) {
		ctxMap := map[string]any{}
		original := &intakesvc.Context{
			Classification: intakesvc.ClassificationContext{Title: "t"},
			Routing:        intakesvc.RoutingContext{Source: ""},
		}
		ctxMap, _ = intakesvc.StoreCallerProvided(ctxMap, original)
		got := intakesvc.FromContext(ctxMap)
		require.NotNil(t, got)
		require.Equal(t, "t", got.Classification.Title)
		require.Equal(t, intakesvc.SourceCallerProvided, got.Routing.Source)
	})

	t.Run("FromContext nil safety", func(t *testing.T) {
		require.Nil(t, intakesvc.FromContext(nil))
		require.Nil(t, intakesvc.FromContext(map[string]any{}))
		require.Nil(t, intakesvc.FromContext(map[string]any{"other": "value"}))
	})
}

func TestResolveWorkspaceActivationProfileOverride_MultiOrderCompare(t *testing.T) {
	svc := &Service{}
	input := &QueryInput{
		Query: "show me order 2656980 and 2609393",
		Agent: &agentmdl.Agent{
			Intake: agentmdl.Intake{
				ActivationRules: []agentmdl.ActivationRule{
					{
						ID: "open_order_windows",
						Match: agentmdl.ActivationMatch{
							Patterns: []string{`^show me order\s+(.+)$`},
							Extractors: map[string]agentmdl.ActivationExtractor{
								"ids": {Type: "regex_all", Source: "$1", Pattern: `\d+`},
							},
						},
						Classification: agentmdl.ActivationClassification{Intent: "troubleshoot_ad_order"},
						Prompting:      agentmdl.ActivationPrompting{SuggestedProfileID: "workspace_ui"},
						Scope: agentmdl.ActivationScope{Values: map[string]string{
							"uiTarget":    "order",
							"workspaceUI": "activation",
							"action":      "open",
							"adOrderIds":  "$ids",
						}},
						Action: agentmdl.ActivationAction{
							Tool:    "ui/view:open",
							Foreach: "ids",
							Input:   map[string]interface{}{"timeoutMs": 600000},
							Item: map[string]interface{}{
								"id":       "order",
								"openMode": "append",
								"parameters": map[string]interface{}{
									"AdOrderId": []interface{}{"$item:int"},
								},
							},
						},
						Response: agentmdl.ActivationResponse{AssistantText: "The requested orders are now open."},
					},
				},
			},
		},
	}
	override := svc.resolveWorkspaceActivationProfileOverride(input)
	require.NotNil(t, override)
	require.Equal(t, "workspace_ui", override.Prompting.SuggestedProfileID)
	require.Equal(t, "order", override.Scope.Values["uiTarget"])
	require.Equal(t, "open", override.Scope.Values["action"])
	require.Equal(t, "2656980,2609393", override.Scope.Values["adOrderIds"])
	require.Equal(t, "ui/view:open", override.DirectAction.ToolName)
	require.Len(t, override.DirectAction.Input["items"], 2)
}

func TestResolveWorkspaceUIIntentOverride_LiveTabMatch(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bridge.Hub().ServeWS(w, r)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, conn.WriteJSON(map[string]interface{}{
		"type":     "ui.hello",
		"clientId": "client-1",
	}))
	require.NoError(t, conn.WriteJSON(map[string]interface{}{
		"type":     "ui.snapshot",
		"clientId": "client-1",
		"data": map[string]interface{}{
			"clientId":       "client-1",
			"conversationId": "conv-1",
			"selected": map[string]interface{}{
				"windowId": "order__conv-1",
			},
			"windows": []interface{}{
				map[string]interface{}{
					"windowId":       "order__conv-1",
					"windowKey":      "order",
					"windowTitle":    "Order Summary",
					"conversationId": "conv-1",
					"presentation":   "hosted",
					"region":         "chat.top",
					"parentKey":      "chat/new",
					"viewState": map[string]interface{}{
						"tabs": map[string]interface{}{
							"analysisPane": "deliveryTab",
						},
					},
					"metadata": map[string]interface{}{
						"view": map[string]interface{}{
							"tabs": []interface{}{
								map[string]interface{}{"containerId": "analysisPane", "tabId": "deliveryTab", "title": "Delivery"},
								map[string]interface{}{"containerId": "analysisPane", "tabId": "kpiTab", "title": "KPIs"},
							},
						},
					},
				},
			},
		},
	}))

	svc := &Service{}
	svc.SetUIBridge(bridge)
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	input := &QueryInput{
		ConversationID: "conv-1",
		Query:          "show KPI",
		Agent: &agentmdl.Agent{Intake: agentmdl.Intake{
			ActivationRules: []agentmdl.ActivationRule{
				{
					ID:        "order_followup_tabs",
					Mode:      "followup",
					Source:    "either",
					WindowKey: "order",
					Match: agentmdl.ActivationMatch{
						Patterns: []string{`(?i)^show\s+(.+)$`},
					},
					Prompting: agentmdl.ActivationPrompting{SuggestedProfileID: "workspace_ui"},
					SurfaceMatch: &agentmdl.ActivationSurfaceMatch{
						TabAliases: map[string]string{
							"kpi":  "kpiTab",
							"kpis": "kpiTab",
						},
					},
					Response: agentmdl.ActivationResponse{AssistantText: "Updated the open order summary to $1."},
				},
			},
		}},
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		override := svc.resolveWorkspaceUIIntentOverride(ctx, input)
		if override != nil {
			require.Equal(t, "ui/window:selectTab", override.DirectAction.ToolName)
			require.Equal(t, "kpiTab", override.DirectAction.Input["tabId"])
			require.Contains(t, strings.ToLower(override.DirectAction.AssistantText), "kpi")
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected live workspace tab match for show KPI")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestResolveWorkspaceUIIntentOverride_FollowUpRuleTranscriptOrderControl(t *testing.T) {
	svc := &Service{
		conversation: &followupStubConversationClient{
			conversation: &apiconv.Conversation{
				Transcript: []*agconv.TranscriptView{
					{
						Message: []*agconv.MessageView{
							{
								ToolMessage: []*agconv.ToolMessageView{
									{
										Content: strPtr(`{"clientId":"client-1","selectedWindowId":"order__conv-1","items":[{"windowId":"order__conv-1","windowKey":"order"}]}`),
										ToolCall: &agconv.ToolCallView{
											ToolName: "ui/view:open",
											Status:   "completed",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	input := &QueryInput{
		ConversationID: "conv-1",
		Query:          "show 7d",
		Agent: &agentmdl.Agent{Intake: agentmdl.Intake{
			ActivationRules: []agentmdl.ActivationRule{
				{
					ID:        "order_followup_period",
					Mode:      "followup",
					Source:    "either",
					WindowKey: "order",
					Match: agentmdl.ActivationMatch{
						Patterns: []string{`(?i)^show\s+(.+)$`},
					},
					Prompting: agentmdl.ActivationPrompting{SuggestedProfileID: "workspace_ui"},
					SurfaceMatch: &agentmdl.ActivationSurfaceMatch{
						ControlAliases: map[string]agentmdl.ActivationSurfaceControlAlias{
							"7d": {ControlID: "periodView", Value: "7d", ValueLabel: "7D"},
						},
					},
					Response: agentmdl.ActivationResponse{AssistantText: "Updated the open order summary to $1."},
				},
			},
		}},
	}

	override := svc.resolveWorkspaceUIIntentOverride(context.Background(), input)
	require.NotNil(t, override)
	require.Equal(t, "ui/control:setValue", override.DirectAction.ToolName)
	require.Equal(t, "periodView", override.DirectAction.Input["controlId"])
	require.Equal(t, "7d", override.DirectAction.Input["value"])
}

func TestMaybeRunIntakeSidecar_AppliesWorkspaceActivationProfileOverride(t *testing.T) {
	svc := &Service{}
	input := &QueryInput{
		ConversationID: "conv-1",
		Query:          "open metric report builder",
		Agent: &agentmdl.Agent{Intake: agentmdl.Intake{
			Enabled:             true,
			ConfidenceThreshold: 0.5,
			Scope:               []string{"profile", "template"},
			ActivationRules: []agentmdl.ActivationRule{
				{
					ID: "open_metric_report_builder",
					Match: agentmdl.ActivationMatch{
						Patterns: []string{`^open metric report builder$`},
					},
					Prompting: agentmdl.ActivationPrompting{SuggestedProfileID: "workspace_ui"},
					Scope: agentmdl.ActivationScope{Values: map[string]string{
						"uiTarget":    "metricReportBuilder",
						"workspaceUI": "activation",
						"action":      "open",
					}},
					Action: agentmdl.ActivationAction{
						Tool:  "ui/view:open",
						Input: map[string]interface{}{"id": "metricReportBuilder", "timeoutMs": 600000},
					},
					Response: agentmdl.ActivationResponse{AssistantText: "The Performance Metrics workspace is now open."},
				},
			},
		}},
	}

	svc.maybeRunIntakeSidecar(context.Background(), input)

	require.Equal(t, "workspace_ui", input.PromptProfileId)
	stored := intakesvc.FromContext(input.Context)
	require.NotNil(t, stored)
	require.Equal(t, "workspace_ui", stored.Prompting.SuggestedProfileID)
	require.Equal(t, "metricReportBuilder", stored.Scope.Values["uiTarget"])
	require.Equal(t, "activation", stored.Scope.Values["workspaceUI"])
	require.Equal(t, "ui/view:open", stored.DirectAction.ToolName)
	require.Equal(t, "metricReportBuilder", stored.DirectAction.Input["id"])
}

func TestMaybeRunIntakeSidecar_AppliesWorkspaceActivationProfileOverride_WithoutDirectAction(t *testing.T) {
	svc := &Service{}
	input := &QueryInput{
		ConversationID: "conv-1",
		Query:          "set forecast for line 7272328",
		Agent: &agentmdl.Agent{Intake: agentmdl.Intake{
			Enabled:             true,
			ConfidenceThreshold: 0.5,
			Scope:               []string{"profile", "template", "context"},
			ActivationRules: []agentmdl.ActivationRule{
				{
					ID: "forecast_builder_assist",
					Match: agentmdl.ActivationMatch{
						Patterns: []string{`(?i)^set\s+forecast\s+for\s+line\s+(\d+)$`},
					},
					Classification: agentmdl.ActivationClassification{Intent: "forecast_builder_assist"},
					Prompting:      agentmdl.ActivationPrompting{SuggestedProfileID: "workspace_ui"},
					Scope: agentmdl.ActivationScope{Values: map[string]string{
						"uiTarget":    "forecastingCubeBuilder",
						"workspaceUI": "builder_assist",
						"action":      "open",
						"audienceId":  "$1",
						"audienceIds": "$1",
					}},
				},
			},
		}},
	}

	svc.maybeRunIntakeSidecar(context.Background(), input)

	require.Equal(t, "workspace_ui", input.PromptProfileId)
	stored := intakesvc.FromContext(input.Context)
	require.NotNil(t, stored)
	require.Equal(t, "forecast_builder_assist", stored.Classification.Intent)
	require.Equal(t, "workspace_ui", stored.Prompting.SuggestedProfileID)
	require.Equal(t, "forecastingCubeBuilder", stored.Scope.Values["uiTarget"])
	require.Equal(t, "builder_assist", stored.Scope.Values["workspaceUI"])
	require.Equal(t, "7272328", stored.Scope.Values["audienceId"])
	require.Equal(t, "7272328", stored.Scope.Values["audienceIds"])
	require.Empty(t, stored.DirectAction.ToolName)
	require.Nil(t, stored.DirectAction.Input)
}
