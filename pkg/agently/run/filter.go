package run

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
	core.RegisterType("runfilter", "Filter", reflect.TypeOf(Filter{}), checksum.GeneratedTime)
}

type Filter struct{}

var (
	trueCriteria  = &codec.Criteria{Expression: "1=1"}
	falseCriteria = &codec.Criteria{Expression: "0=1"}
)

// Compute enforces run visibility using effective_user_id:
// - anonymous: only runs with empty effective_user_id (public/system)
// - authenticated: empty effective_user_id OR own effective_user_id
func (f *Filter) Compute(ctx context.Context, value interface{}) (*codec.Criteria, error) {
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
		return &codec.Criteria{Expression: "COALESCE(t.effective_user_id, '') = ''"}, nil
	}
	return &codec.Criteria{
		Expression:   "(COALESCE(t.effective_user_id, '') = '' OR t.effective_user_id = ?)",
		Placeholders: []interface{}{userID},
	}, nil
}
