package predicate

import (
	"context"
	"github.com/viant/xdatly/predicate"
	"reflect"
)

// Presence controls activation. The supplied boolean does not change the legacy
// meaning of these named business filters.
type MessageTurnTask struct{}
type MessageAssistantFinal struct{}
type MessageAssistantStatus struct{}

var LinkedMessageTypes = []reflect.Type{reflect.TypeFor[MessageTurnTask](), reflect.TypeFor[MessageAssistantFinal](), reflect.TypeFor[MessageAssistantStatus]()}

func (*MessageTurnTask) Compute(context.Context, any) (*predicate.Criteria, error) {
	return &predicate.Criteria{Expression: "m.role = 'user' AND m.interim = 0 AND (m.type = 'task' OR m.mode = 'task')"}, nil
}
func (*MessageAssistantFinal) Compute(context.Context, any) (*predicate.Criteria, error) {
	return &predicate.Criteria{Expression: "m.role = 'assistant' AND m.interim = 0 AND (m.mode IS NULL OR LOWER(m.mode) <> 'router') AND m.content IS NOT NULL AND TRIM(m.content) <> ''"}, nil
}
func (*MessageAssistantStatus) Compute(context.Context, any) (*predicate.Criteria, error) {
	return &predicate.Criteria{Expression: "m.role = 'assistant' AND (m.mode IS NULL OR LOWER(m.mode) <> 'router') AND ((m.preamble IS NOT NULL AND TRIM(m.preamble) <> '') OR (m.interim <> 0 AND m.content IS NOT NULL AND TRIM(m.content) <> ''))"}, nil
}
