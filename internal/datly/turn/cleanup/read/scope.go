package read

import (
	"context"
	"fmt"
	"github.com/viant/agently-core/internal/datly/cleanupscope"
)

// Init restricts this reader to host-authorized, bounded deletion requests.
func (input *Input) Init(context.Context) error {
	if input == nil || input.Has == nil {
		return fmt.Errorf("cleanup turn scope is required")
	}
	return cleanupscope.Validate(input.Trusted,
		cleanupscope.Predicate{Present: input.Has.IDs, IDs: input.IDs},
		cleanupscope.Predicate{Present: input.Has.ConversationIDs, IDs: input.ConversationIDs},
		cleanupscope.Predicate{Present: input.Has.GoalIDs, IDs: input.GoalIDs},
		cleanupscope.Predicate{Present: input.Has.StartedByMessageIDs, IDs: input.StartedByMessageIDs},
		cleanupscope.Predicate{Present: input.Has.RetryOfIDs, IDs: input.RetryOfIDs},
	)
}
