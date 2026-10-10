package read

import (
	"context"
	"fmt"

	"github.com/viant/agently-core/internal/datly/cleanupscope"
)

func (input *Input) Init(context.Context) error {
	if input == nil || input.Has == nil {
		return fmt.Errorf("cleanup run scope is required")
	}
	return cleanupscope.Validate(input.Trusted,
		cleanupscope.Predicate{Present: input.Has.IDs, IDs: input.IDs},
		cleanupscope.Predicate{Present: input.Has.ConversationIDs, IDs: input.ConversationIDs},
		cleanupscope.Predicate{Present: input.Has.TurnIDs, IDs: input.TurnIDs},
		cleanupscope.Predicate{Present: input.Has.ScheduleIDs, IDs: input.ScheduleIDs},
		cleanupscope.Predicate{Present: input.Has.ResumedFromRunIDs, IDs: input.ResumedFromRunIDs})
}
