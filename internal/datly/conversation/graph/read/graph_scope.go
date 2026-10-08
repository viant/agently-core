package read

import (
	"context"
	"fmt"
	"strings"
)

// This reader is host-only and must never become an unbounded conversation
// scan. Authorization of every returned node belongs to the deletion parent.
func (input *Input) Init(context.Context) error {
	if input == nil || !input.Trusted || input.Has == nil {
		return fmt.Errorf("authorized conversation graph scope is required")
	}
	bounded := false
	for _, predicate := range []struct {
		present bool
		ids     []string
	}{
		{input.Has.IDs, input.IDs},
		{input.Has.ParentIDs, input.ParentIDs},
		{input.Has.ParentTurnIDs, input.ParentTurnIDs},
	} {
		if !predicate.present {
			continue
		}
		bounded = true
		if len(predicate.ids) == 0 {
			return fmt.Errorf("conversation graph predicate requires nonempty IDs")
		}
		for _, id := range predicate.ids {
			if strings.TrimSpace(id) == "" {
				return fmt.Errorf("conversation graph identity is empty")
			}
		}
	}
	if !bounded {
		return fmt.Errorf("conversation graph read requires a bounded predicate")
	}
	return nil
}
