package write

import (
	"context"
	"fmt"
	base "github.com/viant/agently-core/internal/datly/message/base"
	read "github.com/viant/agently-core/internal/datly/message/read"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"reflect"
	"sync"
)

type turnCounter struct {
	sync.Mutex
	ready bool
	next  int
}

var turnCounters sync.Map

func nextTurnSequence(ctx context.Context, invoker dexec.ComponentInvoker, turn string) (int, error) {
	v, _ := turnCounters.LoadOrStore(turn, &turnCounter{})
	counter := v.(*turnCounter)
	counter.Lock()
	defer counter.Unlock()
	if !counter.ready {
		if invoker == nil {
			return 0, fmt.Errorf("message sequence requires component invoker")
		}
		q := &read.MessagesInput{}
		q.SetTurnId(turn)
		q.SetFields([]string{"sequence"})
		q.SetLimit(1)
		q.SetOrderBy("sequence DESC")
		q.SetInternal(true)
		q.SetReadMode("rows")
		owner := ""
		q.SetVisibilitySubject(&owner)
		value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[base.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/message/base"}}, Input: q, Providers: []locator.Provider{provider.Named("messageaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "internal" {
				return true, true, nil
			}
			if name == "mode" {
				return "rows", true, nil
			}
			return nil, false, nil
		}), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil })}})
		if err != nil {
			return 0, err
		}
		out, ok := value.(*base.MessagesOutput)
		if !ok {
			return 0, fmt.Errorf("unexpected sequence reader output %T", value)
		}
		counter.next = 1
		if len(out.Data) > 0 && out.Data[0].Sequence != nil && *out.Data[0].Sequence >= 1 {
			counter.next = *out.Data[0].Sequence + 1
		}
		counter.ready = true
	}
	n := counter.next
	counter.next++
	return n, nil
}

func sequenceCollision(ctx context.Context, invoker dexec.ComponentInvoker, entity *Message) (bool, error) {
	for offset := 0; ; offset += 1000 {
		q := &read.MessagesInput{}
		q.SetTurnId(*entity.TurnId)
		q.SetFields([]string{"id", "sequence"})
		q.SetLimit(1000)
		q.SetOffset(offset)
		q.SetOrderBy("sequence DESC")
		owner := ""
		value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[base.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/message/base"}}, Input: q, Providers: []locator.Provider{provider.Named("messageaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "internal" {
				return true, true, nil
			}
			if name == "mode" {
				return "rows", true, nil
			}
			return nil, false, nil
		}), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil })}})
		if err != nil {
			return false, err
		}
		out := value.(*base.MessagesOutput)
		for _, row := range out.Data {
			if row.Id != entity.Id && row.Sequence != nil && *row.Sequence == *entity.Sequence {
				return true, nil
			}
		}
		if len(out.Data) < 1000 {
			return false, nil
		}
	}
}
