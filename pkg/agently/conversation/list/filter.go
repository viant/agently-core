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
	if input.Has != nil && input.Has.DefaultPredicate && strings.TrimSpace(input.DefaultPredicate) == "1" {
		return trueCriteria, nil
	}

	userID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	if userID == "" {
		return &codec.Criteria{
			Expression:   "COALESCE(c.visibility, '') <> ?",
			Placeholders: []interface{}{"private"},
		}, nil
	}
	return &codec.Criteria{
		Expression:   "(COALESCE(c.visibility, '') <> ? OR c.created_by_user_id = ?)",
		Placeholders: []interface{}{"private", userID},
	}, nil
}
