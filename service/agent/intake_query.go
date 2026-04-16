package agent

import (
	"context"
	"strings"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/internal/logx"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	intakesvc "github.com/viant/agently-core/service/intake"
)

// maybeRunIntakeSidecar runs the pre-turn intake sidecar when the agent is
// configured for it.  It is a no-op when:
//   - the intake service is not wired
//   - the agent's Intake.Enabled is false
//   - this is not the first turn and TriggerOnTopicShift is false
//
// On success the TurnContext is stored in input.Context so the agent can read
// title, intent, entities, and (when in Class B scope) profile suggestions.
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
	tc := s.intakeSvc.Run(ctx, userMessage, cfg, strings.TrimSpace(input.UserId))
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

// shouldRunIntake decides whether the sidecar should fire for this turn.
//
// It fires on:
//   - first turn of a new conversation (no history yet)
//   - always (conservative: always run on every turn when enabled)
//
// Topic-shift detection is deferred to a future iteration; for now the sidecar
// runs on every turn when enabled, which mirrors the safest default.
func (s *Service) shouldRunIntake(_ context.Context, _ *QueryInput, _ *agentmdl.Intake) bool {
	return true
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

	// Merge entities into context so velty templates can access them.
	if len(tc.Entities) > 0 {
		for k, v := range tc.Entities {
			input.Context["intake.entity."+k] = v
		}
		input.Context["intake.entities"] = tc.Entities
	}

	// Class B: append tool bundles suggested by the sidecar.
	if cfg.HasScope(agentmdl.IntakeScopeTools) && len(tc.AppendToolBundles) > 0 {
		input.ToolBundles = append(input.ToolBundles, tc.AppendToolBundles...)
	}

	// Class B: store template suggestion.
	if cfg.HasScope(agentmdl.IntakeScopeTemplate) && strings.TrimSpace(tc.TemplateId) != "" {
		input.Context["intake.templateId"] = strings.TrimSpace(tc.TemplateId)
	}

	// Class B: store profile suggestion when confidence meets the threshold.
	if cfg.HasScope(agentmdl.IntakeScopeProfile) && strings.TrimSpace(tc.SuggestedProfileId) != "" {
		if tc.Confidence >= cfg.EffectiveConfidenceThreshold() {
			input.Context["intake.suggestedProfileId"] = strings.TrimSpace(tc.SuggestedProfileId)
		}
	}
}
