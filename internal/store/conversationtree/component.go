package conversationtree

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	custom "github.com/viant/datly/runtime/handler/custom"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/x"
	xdatly "github.com/viant/xdatly"
	"github.com/viant/xdatly/handler"
)

type DeleteComponent struct {
	Contract xdatly.Component[DeleteInput, DeleteOutput] `component:"ConversationTreeDelete,path=/v1/internal/agently/conversation-tree/delete,method=POST,handler=NewDelete,internal=true"`
}

type DeleteInput struct {
	RootIDs []string `parameter:"RootIDs,kind=body,in=rootIds"`
	// Extra identities are authorized by the private schedule cascade parent.
	RunIDs         []string   `parameter:"RunIDs,kind=body,in=runIds"`
	ScheduleRunIDs []string   `parameter:"ScheduleRunIDs,kind=body,in=scheduleRunIds"`
	Now            *time.Time `parameter:"Now,kind=body,in=now"`
}

type DeleteOutput struct {
	DeletedConversations int `json:"deletedConversations"`
}

type Delete struct{}

func NewDelete() handler.Contract[DeleteInput, DeleteOutput] { return &Delete{} }

func Exports() (*x.Registry, error) {
	registry := x.NewRegistry()
	for _, typ := range []reflect.Type{reflect.TypeFor[DeleteInput](), reflect.TypeFor[DeleteOutput]()} {
		registry.Register(x.NewType(typ))
	}
	factory, err := x.NewFunction(reflect.TypeFor[DeleteComponent]().PkgPath(), "NewDelete", custom.Factory(NewDelete))
	if err != nil {
		return nil, err
	}
	if err = registry.RegisterFunctions(factory); err != nil {
		return nil, err
	}
	return registry, nil
}

func (*Delete) Exec(ctx context.Context, session handler.Session, input *DeleteInput, output *DeleteOutput) error {
	if session == nil || session.Binder() == nil || input == nil || output == nil {
		return fmt.Errorf("conversation deletion invocation is incomplete")
	}
	roots, runs, legacy := normalizeIDs(input.RootIDs), normalizeIDs(input.RunIDs), normalizeIDs(input.ScheduleRunIDs)
	if len(roots) == 0 && len(runs) == 0 && len(legacy) == 0 {
		return nil
	}
	if len(roots) > MaxConversations {
		return ErrTooLarge
	}
	deps := struct {
		Invoker dexec.ComponentInvoker     `bind:"kind=component_invoker,required"`
		Starter handler.TransactionStarter `bind:"kind=transactionStarter,required"`
		Owner   *string                    `bind:"kind=visibility,in=subject,required"`
		Schema  TableInspector             `bind:"kind=conversationtreeSchema,in=inspector,required"`
	}{}
	if err := session.Binder().Bind(ctx, &deps); err != nil {
		return err
	}
	if deps.Invoker == nil || deps.Starter == nil || deps.Owner == nil || deps.Schema == nil {
		return fmt.Errorf("conversation deletion capabilities are unavailable")
	}
	owner := strings.TrimSpace(*deps.Owner)
	// The schedule cascade parent authorizes extra run identities, including
	// historical ownerless schedules. Conversation roots require an owner.
	if owner == "" && len(roots) > 0 {
		return ErrPermissionDenied
	}
	ctx = dexec.WithTransactionIsolation(ctx, dexec.IsolationSerializable)
	if err := deps.Starter.Start(ctx); err != nil {
		return err
	}
	now := time.Now().UTC()
	if input.Now != nil {
		now = input.Now.UTC()
	}
	d := &Discoverer{Invoker: deps.Invoker, OwnerID: func(context.Context) string { return owner }, Schema: deps.Schema, LockDetachRows: true}
	graph, err := d.DiscoverAuthorized(ctx, roots...)
	if err != nil {
		return err
	}
	if err = d.LockConversationGraph(ctx, graph); err != nil {
		return err
	}
	// Recheck identity/ownership/topology after acquiring the exclusive locks.
	graph, err = d.DiscoverAuthorized(ctx, roots...)
	if err != nil {
		return err
	}
	if err = d.LockConversationGraph(ctx, graph); err != nil {
		return err
	}
	plan, err := d.CollectDeletePlan(ctx, graph, now, runs, legacy)
	if err != nil {
		return err
	}
	if err = d.LockDeletePlanRuns(ctx, plan); err != nil {
		return err
	}
	if err = d.RefreshDeletePlanRunEvidence(ctx, plan); err != nil {
		return err
	}
	if err = d.LockDeletePlanRuns(ctx, plan); err != nil {
		return err
	}
	if err = d.ValidateNonTerminalStatuses(ctx, graph); err != nil {
		return err
	}
	if err = (&RunEvidence{Current: plan.Runs, Legacy: plan.LegacyRuns}).Validate(now); err != nil {
		return err
	}
	if err = d.ValidateInboundLinks(ctx, graph); err != nil {
		return err
	}
	if _, err = d.ValidateExportReferences(ctx, graph); err != nil {
		return err
	}
	if err = (&Mutator{Invoker: deps.Invoker, OwnerID: d.OwnerID}).Apply(ctx, plan, InvestigationDelete); err != nil {
		return err
	}
	output.DeletedConversations = len(plan.ConversationIDs)
	return nil
}

var deleteTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[DeleteComponent]().PkgPath(), Name: "ConversationTreeDelete"},
	Route:     spec.RouteRef{Method: "POST", Path: "/v1/internal/agently/conversation-tree/delete"},
}

// InvokeDelete enters the private managed component. A cascade parent retains
// completion ownership; generated child writers share that transaction.
func InvokeDelete(ctx context.Context, invoker dexec.ComponentInvoker, input *DeleteInput) error {
	if invoker == nil || input == nil {
		return fmt.Errorf("conversation deletion runtime and input are required")
	}
	var providers []locator.Provider
	if schema, ok := invoker.(TableInspector); ok {
		providers = append(providers, provider.Named("conversationtreeSchema", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "inspector" {
				return schema, true, nil
			}
			return nil, false, nil
		}))
	}
	value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: deleteTarget, Input: input, Providers: providers})
	if err != nil {
		return err
	}
	if output, ok := value.(*DeleteOutput); !ok || output == nil {
		return fmt.Errorf("conversation deletion returned %T", value)
	}
	return nil
}
