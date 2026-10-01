package read

import (
	"context"
	"fmt"
	"strings"
)

func (input *GoalInput) Init(context.Context) error {
	if input.GraphMode {
		if input.Has == nil || ((!input.Has.ConversationIDs || len(input.ConversationIDs) == 0) && (!input.Has.Ids || len(input.Ids) == 0)) {
			return fmt.Errorf("goal graph read requires conversation IDs or goal IDs")
		}
		if (input.Has.ConversationIDs && len(input.ConversationIDs) == 0) || (input.Has.Ids && len(input.Ids) == 0) {
			return fmt.Errorf("goal graph read requires nonempty supplied batches")
		}
		return nil
	}
	if input.Has == nil || !input.Has.ConversationID || strings.TrimSpace(input.ConversationID) == "" {
		return fmt.Errorf("goal read requires a conversation ID")
	}
	return nil
}
