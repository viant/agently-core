package technicalmaintenance

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/viant/agently-core/internal/store/maintenancediag"
	"github.com/viant/agently-core/internal/store/maintenancelease"
	dexec "github.com/viant/datly/exec"
	rh "github.com/viant/datly/runtime/handler"
	custom "github.com/viant/datly/runtime/handler/custom"
	"github.com/viant/datly/spec"
	xdatly "github.com/viant/xdatly"
	"github.com/viant/xdatly/handler"
)

type Component struct {
	Contract xdatly.Component[Request, Result] `component:"TechnicalMaintenance,path=/v1/internal/agently/technical-maintenance,method=POST,handler=NewMaintain,internal=true"`
}
type Maintain struct{}

func NewMaintain() handler.Contract[Request, Result] { return &Maintain{} }

// DatlyHandler binds the holder's declared handler to its typed implementation.
// Linked package discovery calls this provider without a host registration list.
func (Component) DatlyHandler(name string) func() (rh.TypedHandler, error) {
	if name != "NewMaintain" {
		return nil
	}
	return custom.Factory(NewMaintain)
}
func (*Maintain) Exec(ctx context.Context, session handler.Session, input *Request, output *Result) (retErr error) {
	ctx, trace := maintenancediag.Begin(ctx, "technicalmaintenance")
	defer func() { trace.Finish(retErr) }()
	if session == nil || session.Binder() == nil || input == nil || output == nil {
		return fmt.Errorf("technical maintenance invocation is incomplete")
	}
	input.RecordID = strings.TrimSpace(input.RecordID)
	input.OlderThan = input.OlderThan.UTC()
	input.EvaluatedAt = input.EvaluatedAt.UTC()
	if err := validateRequest(*input); err != nil {
		return err
	}
	*output = Result{Kind: input.Kind, Scope: input.Scope, RecordID: input.RecordID, Mode: input.Mode}
	deps := struct {
		Invoker dexec.ComponentInvoker     `bind:"kind=component_invoker,required"`
		Starter handler.TransactionStarter `bind:"kind=transactionStarter,required"`
	}{}
	if err := session.Binder().Bind(ctx, &deps); err != nil {
		return err
	}
	if deps.Invoker == nil || deps.Starter == nil {
		return fmt.Errorf("technical maintenance capabilities are unavailable")
	}
	deps.Invoker = maintenancediag.Wrap(ctx, deps.Invoker)
	ctx = dexec.WithTransactionIsolation(ctx, dexec.IsolationSerializable)
	if err := deps.Starter.Start(ctx); err != nil {
		return err
	}
	if input.Mode == Delete {
		if _, err := (&maintenancelease.Store{Invoker: deps.Invoker}).Fence(ctx, input.Lease); err != nil {
			return err
		}
	}
	store := &Store{Invoker: deps.Invoker}
	if input.Mode == Delete {
		exists, err := store.lockRecord(ctx, input.Kind, input.RecordID)
		if err != nil {
			return err
		}
		if !exists {
			output.Reason = "no_longer_eligible"
			return nil
		}
	}
	r, _ := ruleFor(input.Kind)
	candidates, err := store.candidates(ctx, r, CandidateRequest{Scope: input.Scope, OlderThan: input.OlderThan, EvaluatedAt: input.EvaluatedAt, Limit: 1}, "", input.RecordID, false)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		output.Reason = "no_longer_eligible"
		return nil
	}
	output.Eligible = true
	if input.Mode == DryRun {
		output.Reason = "eligible"
		return nil
	}
	output.DeletedRows, err = store.deleteRecord(ctx, *input)
	if err != nil {
		return err
	}
	if output.DeletedRows <= 0 {
		return fmt.Errorf("technical maintenance kind=%s record=%q deleted no rows after successful recheck", input.Kind, input.RecordID)
	}
	output.Deleted = true
	output.Reason = "deleted"
	return nil
}

var target = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[Component]().PkgPath(), Name: "TechnicalMaintenance"}, Route: spec.RouteRef{Method: "POST", Path: "/v1/internal/agently/technical-maintenance"}}

func (s *Store) Maintain(ctx context.Context, input Request) (result *Result, retErr error) {
	ctx, trace := maintenancediag.Begin(ctx, "technicalmaintenance")
	defer func() { trace.Finish(retErr) }()
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("technical maintenance invoker is required")
	}
	if err := validateRequest(input); err != nil {
		return nil, err
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: target, Input: &input})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*Result)
	if !ok || out == nil {
		return nil, fmt.Errorf("technical maintenance returned %T", value)
	}
	return out, nil
}
