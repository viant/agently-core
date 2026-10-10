package read

import (
	"context"
	"fmt"
	"github.com/viant/agently-core/internal/datly/cleanupscope"
)

// Init restricts this reader to host-authorized, bounded deletion requests.
func (input *Input) Init(context.Context) error {
	if input == nil || input.Has == nil {
		return fmt.Errorf("cleanup message scope is required")
	}
	return cleanupscope.Validate(input.Trusted,
		cleanupscope.Predicate{Present: input.Has.IDs, IDs: input.IDs},
		cleanupscope.Predicate{Present: input.Has.ConversationIDs, IDs: input.ConversationIDs},
		cleanupscope.Predicate{Present: input.Has.LinkedConversationIDs, IDs: input.LinkedConversationIDs},
		cleanupscope.Predicate{Present: input.Has.ParentMessageIDs, IDs: input.ParentMessageIDs},
		cleanupscope.Predicate{Present: input.Has.SupersededByIDs, IDs: input.SupersededByIDs},
	)
}
