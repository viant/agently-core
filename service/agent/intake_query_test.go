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
	lastTurn *apiconv.MutableTurn
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
func (r *intakeRecordingConvClient) PatchMessage(context.Context, *apiconv.MutableMessage) error {
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
