package reactor

import (
	"context"
	"fmt"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/runtime/recovery"
	core "github.com/viant/agently-core/service/core"
)

// CompactProactively delegates to the existing LLM recovery path, with a
// validated removal scope and without its stale post-removal generation.
func (s *Service) CompactProactively(ctx context.Context, conv *apiconv.Conversation, input *core.GenerateInput, scope *recovery.CompactionScope, tokens, capacity int, percent float64) error {
	if s == nil || conv == nil || scope == nil || len(scope.EligibleIDs) == 0 {
		return fmt.Errorf("missing proactive compaction context")
	}
	ctx = recovery.WithProactive(ctx, scope)
	ctx = recovery.WithFullHistory(ctx, true)
	description := fmt.Sprintf("Proactive context usage: %d input tokens / %d context capacity (%.2f%%); configured trigger %.2f%%", tokens, capacity, 100*float64(tokens)/float64(capacity), percent)
	if err := s.compactHistoryLLM(ctx, conv, description, input, 0); err != nil {
		return err
	}
	return ctx.Err()
}
