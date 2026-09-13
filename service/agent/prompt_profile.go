package agent

import (
	"context"
	"fmt"
	"strings"

	intake "github.com/viant/agently-core/protocol/intake"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	policy "github.com/viant/agently-core/service/policy"
)

type selectedPromptProfileContextKey struct{}

func withSelectedPromptProfile(ctx context.Context, profile *intake.Profile) context.Context {
	if ctx == nil || profile == nil {
		return ctx
	}
	return context.WithValue(ctx, selectedPromptProfileContextKey{}, profile)
}

func selectedPromptProfileFromContext(ctx context.Context) *intake.Profile {
	if ctx == nil {
		return nil
	}
	profile, _ := ctx.Value(selectedPromptProfileContextKey{}).(*intake.Profile)
	return profile
}

func (s *Service) selectedPromptProfile(ctx context.Context, input *QueryInput) (*intake.Profile, error) {
	if s == nil || input == nil || s.promptRepo == nil {
		return nil, nil
	}
	if profile := selectedPromptProfileFromContext(ctx); profile != nil {
		return profile, nil
	}
	if input.Agent != nil && !input.Agent.Prompts.AllowsSelectedProfileInjection() {
		return nil, nil
	}
	profileID := strings.TrimSpace(input.PromptProfileId)
	if profileID == "" {
		return nil, nil
	}
	profile, err := s.promptRepo.Load(ctx, profileID)
	if err != nil {
		return nil, fmt.Errorf("load intake profile %q: %w", profileID, err)
	}
	if profile == nil {
		return nil, fmt.Errorf("intake profile %q not found", profileID)
	}
	if s.authorizationPolicy != nil {
		if err := s.authorizationPolicy.Authorize(ctx, policy.OperationIntentView,
			runtimerequestctx.ConversationIDFromContext(ctx),
			policy.Candidate{ID: profileID, Kind: "promptProfile"},
			map[string]any{"agentId": strings.TrimSpace(input.AgentID)},
		); err != nil {
			return nil, fmt.Errorf("intake profile %q not found: %w", profileID, err)
		}
	}
	return profile, nil
}

func ApplyPromptProfileExecutionDefaults(input *QueryInput, profile *intake.Profile) {
	if input == nil || profile == nil {
		return
	}
	if profile.ParallelToolCalls != nil && input.ParallelToolCalls == nil {
		value := *profile.ParallelToolCalls
		input.ParallelToolCalls = &value
	}
}
