package conversation

import (
	"context"
	"reflect"
	"strings"

	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/xdatly/codec"
	"github.com/viant/xdatly/types/core"
	checksum "github.com/viant/xdatly/types/custom/dependency/checksum"
)

func init() {
	core.RegisterType("conversationlist", "Filter", reflect.TypeOf(Filter{}), checksum.GeneratedTime)
}

type Filter struct{}

var (
	trueCriteria  = &codec.Criteria{Expression: "1=1"}
	falseCriteria = &codec.Criteria{Expression: "0=1"}
)

// Compute applies default conversation visibility constraints for list queries:
// - if disabled explicitly (DefaultPredicate=1), do nothing
// - if user present: allow public OR owned private
// - if anonymous: allow public only
func (a *Filter) Compute(ctx context.Context, value interface{}) (*codec.Criteria, error) {
	_ = value
	input := InputFromContext(ctx)
	if input == nil {
		return falseCriteria, nil
	}
	// Exclude child conversations unless explicitly querying by parentId.
	excludeChildren := ""
	if input.Has == nil || !input.Has.ParentId {
		excludeChildren = " AND c.conversation_parent_id IS NULL"
	}
	requireValidParent := ""
	if input.Has != nil && (input.Has.ParentId || input.Has.ParentTurnId) {
		requireValidParent = " AND EXISTS (SELECT 1 FROM conversation p WHERE p.id = c.conversation_parent_id)"
		requireValidParent += " AND (c.conversation_parent_turn_id IS NULL OR EXISTS (SELECT 1 FROM turn pt WHERE pt.id = c.conversation_parent_turn_id))"
	}
	excludeScheduled := ""
	if input.Has != nil && input.Has.ExcludeScheduled && input.ExcludeScheduled {
		excludeScheduled = " AND c.schedule_id IS NULL"
	}
	structural := excludeChildren + requireValidParent + excludeScheduled

	if input.Has != nil && input.Has.DefaultPredicate && strings.TrimSpace(input.DefaultPredicate) == "1" {
		if structural == "" {
			return trueCriteria, nil
		}
		return &codec.Criteria{Expression: "1=1" + structural}, nil
	}

	userID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	if userID == "" {
		return &codec.Criteria{
			Expression:   "COALESCE(c.visibility, '') <> ?" + structural,
			Placeholders: []interface{}{"private"},
		}, nil
	}
	return &codec.Criteria{
		Expression:   "(COALESCE(c.visibility, '') <> ? OR c.created_by_user_id = ?)" + structural,
		Placeholders: []interface{}{"private", userID},
	}, nil
}
