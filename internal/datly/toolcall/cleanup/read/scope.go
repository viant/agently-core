package read

import (
	"context"
	"fmt"
	"github.com/viant/agently-core/internal/datly/cleanupscope"
)

// Init restricts this reader to host-authorized, bounded deletion requests.
func (input *Input) Init(context.Context) error {
	if input == nil || input.Has == nil {
		return fmt.Errorf("cleanup toolcall scope is required")
	}
	return cleanupscope.Validate(input.Trusted,
		cleanupscope.Predicate{Present: input.Has.MessageIDs, IDs: input.MessageIDs},
		cleanupscope.Predicate{Present: input.Has.TurnIDs, IDs: input.TurnIDs},
	)
}
