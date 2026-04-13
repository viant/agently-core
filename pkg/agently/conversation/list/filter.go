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

var trueCriteria = &codec.Criteria{Expression: "1=1"}

// Compute enforces conversation visibility using effective user context.
// Anonymous callers can see only non-private conversations.
// Authenticated callers can see non-private conversations plus their own private ones.
func (f *Filter) Compute(ctx context.Context, value interface{}) (*codec.Criteria, error) {
	defaultPredicate, _ := value.(string)
	if strings.TrimSpace(defaultPredicate) == "1" {
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
