package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	apiconv "github.com/viant/agently-core/app/store/conversation"
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
		{"identical", "analyze deal 146901 pacing", "analyze deal 146901 pacing", 0.99, 1.0},
		{"disjoint", "analyze deal 146901 pacing", "explain feeders for campaign 42", 0.0, 0.15},
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
	tc := &intakesvc.TurnContext{TemplateId: "report_v2"}
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
	tc := &intakesvc.TurnContext{TemplateId: "sidecar_suggestion"}
	applyTurnContext(input, tc, cfg)

	require.Equal(t, "caller_choice", input.TemplateId, "caller-chosen template must win")
	require.Equal(t, "sidecar_suggestion", input.Context["intake.templateId"], "context still records the sidecar's suggestion for observability")
}

// TestApplyTurnContext_ClarificationSurfaced verifies the gap fix: the
// sidecar's ClarificationNeeded / ClarificationQuestion used to be parsed
// and then silently dropped. They now land on input.Context under explicit
// keys so templates and downstream handlers can act on them.
func TestApplyTurnContext_ClarificationSurfaced(t *testing.T) {
	cfg := &agentmdl.Intake{
		Enabled: true,
		Scope:   []string{agentmdl.IntakeScopeClarification},
	}
	input := &QueryInput{}
	tc := &intakesvc.TurnContext{
		ClarificationNeeded:   true,
		ClarificationQuestion: "Which order are you referring to?",
	}
	applyTurnContext(input, tc, cfg)

	require.Equal(t, true, input.Context["intake.clarificationNeeded"])
	require.Equal(t, "Which order are you referring to?", input.Context["intake.clarificationQuestion"])
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
	applyTurnContext(low, &intakesvc.TurnContext{SuggestedProfileId: "deal_impact", Confidence: 0.5}, cfg)
	_, hasLow := low.Context["intake.suggestedProfileId"]
	require.False(t, hasLow, "below-threshold suggestions must not land in context")

	// High confidence — suggestion + confidence surface in context.
	high := &QueryInput{}
	applyTurnContext(high, &intakesvc.TurnContext{SuggestedProfileId: "deal_impact", Confidence: 0.9}, cfg)
	require.Equal(t, "deal_impact", high.Context["intake.suggestedProfileId"])
	require.InDelta(t, 0.9, high.Context["intake.suggestedProfileConfidence"], 0.001)
	require.Equal(t, "deal_impact", high.PromptProfileId)
}

func TestApplyTurnContext_PromptProfileDoesNotOverrideCaller(t *testing.T) {
	cfg := &agentmdl.Intake{
		Enabled:             true,
		Scope:               []string{agentmdl.IntakeScopeProfile},
		ConfidenceThreshold: 0.8,
	}
	input := &QueryInput{PromptProfileId: "caller_choice"}
	tc := &intakesvc.TurnContext{SuggestedProfileId: "repo_analysis", Confidence: 0.95}
	applyTurnContext(input, tc, cfg)

	require.Equal(t, "caller_choice", input.PromptProfileId)
	require.Equal(t, "repo_analysis", input.Context["intake.suggestedProfileId"])
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
	lastTurn    *apiconv.MutableTurn
	lastMessage *apiconv.MutableMessage
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
func (r *intakeRecordingConvClient) PatchMessage(_ context.Context, m *apiconv.MutableMessage) error {
	r.lastMessage = m
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
	})
}

// TestMaybeRunIntakeSidecar_CallerProvidedOverride verifies skip rule §2.c:
// when input.Context already holds a TurnContext with
// Source=SourceCallerProvided, the sidecar must skip its LLM call entirely
// (no panic on nil intakeSvc) and still apply the merge logic so that
// suggested template / profile / bundles take effect.
func TestMaybeRunIntakeSidecar_CallerProvidedOverride(t *testing.T) {
	t.Run("skips sidecar and applies override", func(t *testing.T) {
		// Service with intakeSvc==nil. If our skip rule fires correctly we
		// never enter the sidecar branch, so nil is safe. If the skip rule
		// is broken we panic on nil.intakeSvc.Run.
		s := &Service{}

		override := &intakesvc.TurnContext{
			Title:              "caller-supplied",
			Intent:             "forecast_review",
			SelectedAgentID:    "steward",
			Mode:               intakesvc.ModeRoute,
			Source:             intakesvc.SourceCallerProvided,
			TemplateId:         "audience_forecast_dashboard",
			SuggestedProfileId: "steward-forecast",
			Confidence:         0.94,
			AppendToolBundles:  []string{"forecasting-cube"},
		}

		input := &QueryInput{
			Agent: &agentmdl.Agent{
				Intake: agentmdl.Intake{
					Enabled: true,
					Scope:   []string{agentmdl.IntakeScopeTemplate, agentmdl.IntakeScopeProfile, agentmdl.IntakeScopeTools},
				},
			},
			Query:   "forecast order 2652067",
			Context: map[string]interface{}{intakesvc.ContextKey: override},
		}

		// Should not panic and should apply the override.
		s.maybeRunIntakeSidecar(context.Background(), input)

		require.Equal(t, "audience_forecast_dashboard", input.TemplateId,
			"caller-provided template must land on input.TemplateId")
		require.Contains(t, input.ToolBundles, "forecasting-cube",
			"caller-provided AppendToolBundles must merge into input.ToolBundles")
	})

	t.Run("non-caller-provided source does not trigger skip path", func(t *testing.T) {
		// A TurnContext with a different Source (e.g. agent-side cached
		// reuse) must NOT trip the caller-provided early return — that path
		// is reserved for explicit caller overrides only.
		s := &Service{} // nil intakeSvc means non-caller-provided falls through to "intakeSvc == nil" return

		other := &intakesvc.TurnContext{
			Title:  "from-elsewhere",
			Source: intakesvc.SourceReused, // not "caller-provided"
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
		original := &intakesvc.TurnContext{
			SelectedAgentID: "forecaster",
			Mode:            intakesvc.ModeRoute,
			Title:           "do the thing",
			Source:          "", // not yet annotated
		}
		ctxMap, stored := intakesvc.StoreCallerProvided(nil, original)
		require.NotNil(t, ctxMap)
		require.NotNil(t, stored)
		require.Equal(t, intakesvc.SourceCallerProvided, stored.Source,
			"stored copy must be annotated as caller-provided")
		require.Equal(t, "", original.Source,
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
		original := &intakesvc.TurnContext{Title: "t", Source: ""}
		ctxMap, _ = intakesvc.StoreCallerProvided(ctxMap, original)
		got := intakesvc.FromContext(ctxMap)
		require.NotNil(t, got)
		require.Equal(t, "t", got.Title)
		require.Equal(t, intakesvc.SourceCallerProvided, got.Source)
	})

	t.Run("FromContext nil safety", func(t *testing.T) {
		require.Nil(t, intakesvc.FromContext(nil))
		require.Nil(t, intakesvc.FromContext(map[string]any{}))
		require.Nil(t, intakesvc.FromContext(map[string]any{"other": "value"}))
	})
}
