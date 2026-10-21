package conversationtree

import (
	"context"
	"encoding/json"
	"reflect"

	dexec "github.com/viant/datly/exec"
)

// planningReads belongs to one system-retention preparation attempt. Only
// fixed, host-only metadata projections are reusable. Mutations use the
// original invoker, and manual deletion never installs this snapshot.
type planningReads struct {
	delegate dexec.ComponentInvoker
	values   map[string]any
}

func (p *planningReads) InvokeComponent(ctx context.Context, request dexec.ComponentRequest) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := request.Target.Route.Path
	cacheable := request.Target.Route.Method == "GET" && request.Completion == nil && (request.ReaderOptions == nil || len(request.ReaderOptions.ForUpdate) == 0) && len(dexec.ReaderOptionsFromContext(ctx).ForUpdate) == 0
	switch path {
	case "/v1/internal/agently/conversation/graph", "/v1/internal/agently/conversation/cleanup",
		"/v1/internal/agently/turn/cleanup", "/v1/internal/agently/message/cleanup",
		"/v1/internal/agently/run/cleanup", "/v1/internal/agently/modelcall/cleanup", "/v1/internal/agently/toolcall/cleanup":
	default:
		cacheable = false
	}
	if !cacheable {
		return p.delegate.InvokeComponent(ctx, request)
	}
	// Datly omits Has from JSON; include it explicitly so absent predicates and
	// selected empty predicates cannot alias. Capture before binding mutates input.
	var marker any
	value := reflect.ValueOf(request.Input)
	if value.IsValid() && value.Kind() == reflect.Pointer && !value.IsNil() && value.Elem().Kind() == reflect.Struct {
		if field := value.Elem().FieldByName("Has"); field.IsValid() && field.CanInterface() {
			marker = field.Interface()
		}
	}
	encoded, err := json.Marshal(struct{ Input, Has any }{request.Input, marker})
	if err != nil {
		return nil, err
	}
	key := path + string(encoded)
	if cached, ok := p.values[key]; ok {
		return cached, nil
	}
	result, err := p.delegate.InvokeComponent(ctx, request)
	if err == nil {
		p.values[key] = result
	}
	return result, err
}
