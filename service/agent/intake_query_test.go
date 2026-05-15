package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	intakesvc "github.com/viant/agently-core/service/intake"
)

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
	meta, err := json.Marshal(ConversationMetadata{
		Context: map[string]interface{}{
			"uiClientId": "client-1",
		},
		Workspace: &WorkspaceWindowMetadata{
			WindowID:  "order_123",
			WindowKey: "order",
		},
	})
	require.NoError(t, err)
	s := &Service{
		conversation: &stubProjectionBindingConversationClient{
			conversation: &apiconv.Conversation{
				Id:       "conv-1",
				Metadata: strPtr(string(meta)),
			},
		},
	}
	testCases := []struct {
		query         string
		tabID         string
		assistantText string
	}{
		{query: "show kpi", tabID: "kpiTab", assistantText: "Switched the open order summary to the KPIs tab."},
		{query: "show delivery", tabID: "deliveryTab", assistantText: "Switched the open order summary to the Delivery tab."},
		{query: "show hh metrics", tabID: "hhMetricsTab", assistantText: "Switched the open order summary to the HH Metrics tab."},
		{query: "show household metrics", tabID: "hhMetricsTab", assistantText: "Switched the open order summary to the HH Metrics tab."},
		{query: "show pacing", tabID: "pacingTab", assistantText: "Switched the open order summary to the Pacing tab."},
	}

	for _, testCase := range testCases {
		t.Run(testCase.query, func(t *testing.T) {
			input := &QueryInput{
				ConversationID: "conv-1",
				Query:          testCase.query,
				Agent:          &agentmdl.Agent{Intake: agentmdl.Intake{Enabled: true}},
			}

			s.maybeRunIntakeSidecar(context.Background(), input)

			tc := intakesvc.FromContext(input.Context)
			require.NotNil(t, tc)
			require.Equal(t, "ui/window/selectTab", tc.DirectAction.ToolName)
			require.Equal(t, "order_123", tc.DirectAction.Input["windowId"])
			require.Equal(t, testCase.tabID, tc.DirectAction.Input["tabId"])
			require.Equal(t, "client-1", tc.DirectAction.Input["clientId"])
			require.Equal(t, testCase.assistantText, tc.DirectAction.AssistantText)
		})
	}
}

func TestMaybeRunIntakeSidecar_InjectsWorkspaceFollowUpDirectActionForOrderControls(t *testing.T) {
	meta, err := json.Marshal(ConversationMetadata{
		Context: map[string]interface{}{
			"uiClientId": "client-1",
		},
		Workspace: &WorkspaceWindowMetadata{
			WindowID:  "order_123",
			WindowKey: "order",
		},
	})
	require.NoError(t, err)
	s := &Service{
		conversation: &stubProjectionBindingConversationClient{
			conversation: &apiconv.Conversation{
				Id:       "conv-1",
				Metadata: strPtr(string(meta)),
			},
		},
	}
	testCases := []struct {
		query         string
		controlID     string
		value         string
		assistantText string
	}{
		{query: "show today", controlID: "periodView", value: "today", assistantText: "Switched the open order summary period to Today."},
		{query: "show yesterday", controlID: "periodView", value: "yesterday", assistantText: "Switched the open order summary period to Yesterday."},
		{query: "show 7d", controlID: "periodView", value: "7d", assistantText: "Switched the open order summary period to 7D."},
		{query: "show 30d", controlID: "periodView", value: "30d", assistantText: "Switched the open order summary period to 30D."},
		{query: "show hour", controlID: "granularity", value: "hour", assistantText: "Switched the open order summary granularity to Hour."},
		{query: "switch to hour", controlID: "granularity", value: "hour", assistantText: "Switched the open order summary granularity to Hour."},
		{query: "show day", controlID: "granularity", value: "day", assistantText: "Switched the open order summary granularity to Day."},
		{query: "switch to day", controlID: "granularity", value: "day", assistantText: "Switched the open order summary granularity to Day."},
	}

	for _, testCase := range testCases {
		t.Run(testCase.query, func(t *testing.T) {
			input := &QueryInput{
				ConversationID: "conv-1",
				Query:          testCase.query,
				Agent:          &agentmdl.Agent{Intake: agentmdl.Intake{Enabled: true}},
			}

			s.maybeRunIntakeSidecar(context.Background(), input)

			tc := intakesvc.FromContext(input.Context)
			require.NotNil(t, tc)
			require.Equal(t, "ui/control:setValue", tc.DirectAction.ToolName)
			require.Equal(t, "order_123", tc.DirectAction.Input["windowId"])
			require.Equal(t, testCase.controlID, tc.DirectAction.Input["controlId"])
			require.Equal(t, "windowForm", tc.DirectAction.Input["scope"])
			require.Equal(t, testCase.value, tc.DirectAction.Input["value"])
			require.Equal(t, "client-1", tc.DirectAction.Input["clientId"])
			require.Equal(t, testCase.assistantText, tc.DirectAction.AssistantText)
		})
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

func TestApplyTurnContext_EnablesPlannerModeForCreativeDirectAgentRequest(t *testing.T) {
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
		Scope: intakesvc.ScopeContext{
			Values: map[string]string{
				"adOrderId":                "2657966",
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
		Scope: intakesvc.ScopeContext{
			Values: map[string]string{
				"adOrderId":                "2657966",
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
		Scope: intakesvc.ScopeContext{
			Values: map[string]string{"audience_id": "7268995"},
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
		Scope: intakesvc.ScopeContext{
			Values: map[string]string{
				"line_id": "7272328",
			},
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
