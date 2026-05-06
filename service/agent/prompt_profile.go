package agent

import (
	"context"
	"fmt"
	"strings"

	promptdef "github.com/viant/agently-core/protocol/prompt"
)

type selectedPromptProfileContextKey struct{}

func withSelectedPromptProfile(ctx context.Context, profile *promptdef.Profile) context.Context {
	if ctx == nil || profile == nil {
		return ctx
	}
	return context.WithValue(ctx, selectedPromptProfileContextKey{}, profile)
}

func selectedPromptProfileFromContext(ctx context.Context) *promptdef.Profile {
	if ctx == nil {
		return nil
	}
	profile, _ := ctx.Value(selectedPromptProfileContextKey{}).(*promptdef.Profile)
	return profile
}

func (s *Service) selectedPromptProfile(ctx context.Context, input *QueryInput) (*promptdef.Profile, error) {
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
		return nil, fmt.Errorf("load prompt profile %q: %w", profileID, err)
	}
	if profile == nil {
		return nil, fmt.Errorf("prompt profile %q not found", profileID)
	}
	return profile, nil
}

func ApplyPromptProfileExecutionDefaults(input *QueryInput, profile *promptdef.Profile) {
	if input == nil || profile == nil {
		return
	}
	if profile.ParallelToolCalls != nil && input.ParallelToolCalls == nil {
		value := *profile.ParallelToolCalls
		input.ParallelToolCalls = &value
	}
}
