package predicate

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/viant/xdatly/predicate"
)

// ConversationSearch preserves the public page search across the ID, title,
// and summary. Values remain bound parameters on every database dialect.
type ConversationSearch struct{}

var LinkedConversationSearch = reflect.TypeFor[ConversationSearch]()

func (*ConversationSearch) Compute(_ context.Context, value any) (*predicate.Criteria, error) {
	query, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("conversation search requires a string")
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return &predicate.Criteria{Expression: "1=1"}, nil
	}
	pattern := "%" + query + "%"
	return &predicate.Criteria{
		Expression:   "(LOWER(t.id) LIKE ? OR LOWER(COALESCE(t.title, '')) LIKE ? OR LOWER(COALESCE(t.summary, '')) LIKE ?)",
		Placeholders: []any{pattern, pattern, pattern},
	}, nil
}
