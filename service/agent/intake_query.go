package agent

import (
	"context"
	"strings"
	"unicode"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/internal/logx"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	intakesvc "github.com/viant/agently-core/service/intake"
)

// maybeRunIntakeSidecar runs the pre-turn intake sidecar when the agent is
// configured for it.  It is a no-op when:
//   - the intake service is not wired
//   - the agent's Intake.Enabled is false
//   - this is not the first turn and TriggerOnTopicShift is false
//
// On success the TurnContext is stored in input.Context so the agent can read
// title, intent, context, and (when in Class B scope) profile suggestions.
// AppendToolBundles are merged into input.ToolBundles.
// A high-confidence SuggestedProfileId is stored as a hint under a well-known
// context key — the orchestrator may use it or override it.
func (s *Service) maybeRunIntakeSidecar(ctx context.Context, input *QueryInput) {
	if s == nil || s.intakeSvc == nil || input == nil || input.Agent == nil {
		return
	}
	cfg := &input.Agent.Intake
	if !cfg.Enabled {
		return
	}
	if !s.shouldRunIntake(ctx, input, cfg) {
		return
	}
	userMessage := strings.TrimSpace(input.Query)
	if userMessage == "" {
		return
	}
	runCtx := s.intakeTrackedContext(ctx, input)
	tc := s.intakeSvc.Run(runCtx, userMessage, cfg, strings.TrimSpace(input.UserId))
	if tc == nil {
		return
	}
	logx.Infof("conversation", "intake.done convo=%q agent=%q title=%q intent=%q confidence=%.2f profile=%q",
		strings.TrimSpace(input.ConversationID),
		strings.TrimSpace(input.Agent.ID),
		strings.TrimSpace(tc.Title),
		strings.TrimSpace(tc.Intent),
		tc.Confidence,
		strings.TrimSpace(tc.SuggestedProfileId),
	)
	applyTurnContext(input, tc, cfg)
	s.maybeSetConversationTitle(ctx, input.ConversationID, tc.Title)
}

func (s *Service) intakeTrackedContext(ctx context.Context, input *QueryInput) context.Context {
	if s == nil || input == nil {
		return ctx
	}
	preferredTurnID := ""
	if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		preferredTurnID = strings.TrimSpace(turn.TurnID)
	}
	runCtx := s.ensureRunTrackedLLMContext(ctx, strings.TrimSpace(input.ConversationID), "intake_sidecar", preferredTurnID)
	runCtx = runtimerequestctx.WithRequestMode(runCtx, "router")
	return runCtx
}

// shouldRunIntake decides whether the sidecar should fire for this turn.
//
// Behaviour:
//   - TriggerOnTopicShift == false → always run when the sidecar is enabled
//     (legacy default; the sidecar is cheap and every turn benefits).
//   - TriggerOnTopicShift == true  → run on the first turn of a conversation,
//     and on subsequent turns only when the current user message diverges
//     from the previous one by more than TopicShiftThreshold. Divergence is
//     measured as 1 − Jaccard(tokens(current), tokens(prev)).
//
// The Jaccard heuristic is cheap, deterministic, and good enough to spot the
// usual "user pivoted to a completely different task" case without paying
// for an embedding model. Threshold defaults to 0.65.
func (s *Service) shouldRunIntake(ctx context.Context, input *QueryInput, cfg *agentmdl.Intake) bool {
	if cfg == nil || !cfg.Enabled {
		return false
	}
	if !cfg.TriggerOnTopicShift {
		return true
	}
	current := strings.TrimSpace(input.Query)
	if current == "" {
		return true
	}
	previous := s.previousUserMessage(ctx, input.ConversationID)
	if previous == "" {
		// First turn — no prior user message to compare against; run so the
		// caller gets baseline Class A metadata.
		return true
	}
	threshold := cfg.TopicShiftThreshold
	if threshold <= 0 {
		threshold = 0.65
	}
	similarity := jaccardWordSimilarity(previous, current)
	divergence := 1.0 - similarity
	return divergence >= threshold
}

// previousUserMessage returns the trimmed content of the most recent user
// message on the conversation, excluding the current turn's message. Empty
// result means "no prior history available" — caller treats that as first
// turn.
func (s *Service) previousUserMessage(ctx context.Context, convID string) string {
	if s == nil || s.conversation == nil {
		return ""
	}
	convID = strings.TrimSpace(convID)
	if convID == "" {
		return ""
	}
	conv, err := s.conversation.GetConversation(ctx, convID, apiconv.WithIncludeTranscript(true))
	if err != nil || conv == nil {
		return ""
	}
	turns := conv.GetTranscript()
	// Walk backwards and pick the newest user message. The tail of the
	// transcript may be the turn we're currently starting — skip any
	// assistant-only tail and grab the latest persisted user input.
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		if turn == nil {
			continue
		}
		for j := len(turn.Message) - 1; j >= 0; j-- {
			msg := turn.Message[j]
			if msg == nil {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
				if msg.Content != nil {
					if text := strings.TrimSpace(*msg.Content); text != "" {
						return text
					}
				}
			}
		}
	}
	return ""
}

// jaccardWordSimilarity returns |A ∩ B| / |A ∪ B| over lowercased word
// tokens. Empty inputs → 0.
func jaccardWordSimilarity(a, b string) float64 {
	aTokens := tokenizeWords(a)
	bTokens := tokenizeWords(b)
	if len(aTokens) == 0 || len(bTokens) == 0 {
		return 0
	}
	union := make(map[string]struct{}, len(aTokens)+len(bTokens))
	intersection := 0
	for tok := range aTokens {
		union[tok] = struct{}{}
		if _, ok := bTokens[tok]; ok {
			intersection++
		}
	}
	for tok := range bTokens {
		union[tok] = struct{}{}
	}
	if len(union) == 0 {
		return 0
	}
	return float64(intersection) / float64(len(union))
}

// tokenizeWords lowercases the input and splits on any non-letter / non-digit
// rune. Tokens shorter than 2 runes are dropped — they are usually
// punctuation residue or single-letter noise that pollutes the overlap.
func tokenizeWords(s string) map[string]struct{} {
	out := map[string]struct{}{}
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		token := strings.ToLower(b.String())
		b.Reset()
		if len([]rune(token)) < 2 {
			return
		}
		out[token] = struct{}{}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// maybeSetConversationTitle persists the intake-extracted title to the
// conversation store and relies on PatchConversations emitting the
// conversation_meta_updated SSE event so connected clients update their
// sidebar / header without polling.
func (s *Service) maybeSetConversationTitle(ctx context.Context, convID, title string) {
	title = strings.TrimSpace(title)
	convID = strings.TrimSpace(convID)
	if title == "" || convID == "" || s == nil || s.conversation == nil {
		return
	}
	patch := apiconv.NewConversation()
	patch.SetId(convID)
	patch.SetTitle(title)
	if err := s.conversation.PatchConversations(ctx, patch); err != nil {
		logx.Warnf("conversation", "intake: set title convo=%q err=%v", convID, err)
	}
}

// applyTurnContext writes TurnContext fields back into QueryInput so the
// downstream pipeline can use them.
func applyTurnContext(input *QueryInput, tc *intakesvc.TurnContext, cfg *agentmdl.Intake) {
	if input == nil || tc == nil {
		return
	}
	if input.Context == nil {
		input.Context = make(map[string]interface{})
	}

	// Always store the full context under the well-known key.
	input.Context[intakesvc.ContextKey] = tc

	// Surface title for conversation labelling.
	if t := strings.TrimSpace(tc.Title); t != "" {
		input.Context["intake.title"] = t
	}

	// Merge intake context into QueryInput.Context so templates and routing can
	// access normalized scope hints without treating them as authoritative data.
	if len(tc.Context) > 0 {
		for k, v := range tc.Context {
			input.Context["intake.context."+k] = v
		}
		input.Context["intake.context"] = tc.Context
		// Backward-compatible alias for existing templates/workspaces.
		input.Context["intake.entities"] = tc.Context
	}

	// Class A: surface clarification signal so templates and downstream
	// handlers can see when the request is too ambiguous to act on. Stored
	// under explicit context keys rather than left inside the TurnContext
	// struct, because templates that only poke `input.Context` would
	// otherwise miss it.
	if cfg.HasScope(agentmdl.IntakeScopeClarification) && tc.ClarificationNeeded {
		input.Context["intake.clarificationNeeded"] = true
		if q := strings.TrimSpace(tc.ClarificationQuestion); q != "" {
			input.Context["intake.clarificationQuestion"] = q
		}
	}

	// Class B: append tool bundles suggested by the sidecar.
	if cfg.HasScope(agentmdl.IntakeScopeTools) && len(tc.AppendToolBundles) > 0 {
		input.ToolBundles = append(input.ToolBundles, tc.AppendToolBundles...)
	}

	// Class B: apply template suggestion. The context entry remains for
	// observability, but we also populate input.TemplateId — the field
	// applySelectedTemplate actually reads — when the caller has not
	// already chosen a template. Never overwrite an explicit caller choice.
	if cfg.HasScope(agentmdl.IntakeScopeTemplate) {
		if id := strings.TrimSpace(tc.TemplateId); id != "" {
			input.Context["intake.templateId"] = id
			if strings.TrimSpace(input.TemplateId) == "" {
				input.TemplateId = id
			}
		}
	}

	// Class B: store profile suggestion when confidence meets the threshold.
	// Profile selection is explicit turn state. We record it for observability
	// and populate QueryInput.PromptProfileId when the caller did not already
	// choose one.
	if cfg.HasScope(agentmdl.IntakeScopeProfile) && strings.TrimSpace(tc.SuggestedProfileId) != "" {
		if tc.Confidence >= cfg.EffectiveConfidenceThreshold() {
			suggested := strings.TrimSpace(tc.SuggestedProfileId)
			input.Context["intake.suggestedProfileId"] = suggested
			input.Context["intake.suggestedProfileConfidence"] = tc.Confidence
			if strings.TrimSpace(input.PromptProfileId) == "" {
				input.PromptProfileId = suggested
			}
		}
	}
}
