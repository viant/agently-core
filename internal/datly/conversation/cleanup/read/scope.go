package read

import (
	"context"
	"fmt"

	"github.com/viant/agently-core/internal/datly/cleanupscope"
)

func (input *Input) Init(context.Context) error {
	if input == nil || input.Has == nil {
		return fmt.Errorf("cleanup conversation scope is required")
	}
	return cleanupscope.Validate(input.Trusted,
		cleanupscope.Predicate{Present: input.Has.IDs, IDs: input.IDs},
		cleanupscope.Predicate{Present: input.Has.ParentIDs, IDs: input.ParentIDs},
		cleanupscope.Predicate{Present: input.Has.ParentTurnIDs, IDs: input.ParentTurnIDs},
		cleanupscope.Predicate{Present: input.Has.ScheduleIDs, IDs: input.ScheduleIDs},
		cleanupscope.Predicate{Present: input.Has.ScheduleRunIDs, IDs: input.ScheduleRunIDs})
}
