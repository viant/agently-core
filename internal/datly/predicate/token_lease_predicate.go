package predicate

import (
	"context"
	"fmt"
	"reflect"

	"github.com/viant/datly/sql/fragment"
	"github.com/viant/xdatly/predicate"
)

// TokenLeasePredicate keeps the claim race guard on the database server clock.
// It is used only by the generated token writer's claim mode.
type TokenLeasePredicate struct{}

var LinkedTokenLeasePredicate = reflect.TypeFor[TokenLeasePredicate]()

func (*TokenLeasePredicate) Compute(ctx context.Context, value any) (*predicate.Criteria, error) {
	mode, ok := value.(string)
	if !ok || mode != "claim" {
		return nil, fmt.Errorf("token lease predicate requires claim mode")
	}
	clock, err := fragment.New(nil).WithDialect(fragment.Dialect(ctx)).UTCNow()
	if err != nil {
		return nil, err
	}
	return &predicate.Criteria{
		Expression: "(user_oauth_token.refresh_status = 'idle' OR user_oauth_token.lease_until IS NULL OR user_oauth_token.lease_until < " + clock + ")",
	}, nil
}
