package adoption

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	contextstore "github.com/viant/agently-core/internal/store/reporting/context"
	runstore "github.com/viant/agently-core/internal/store/reporting/run"
	dexec "github.com/viant/datly/exec"
	custom "github.com/viant/datly/runtime/handler/custom"
	"github.com/viant/x"
	xdatly "github.com/viant/xdatly"
	"github.com/viant/xdatly/handler"
)

var (
	ErrNotFound    = runstore.ErrNotFound
	ErrCASMismatch = runstore.ErrCASMismatch
	ErrImmutable   = runstore.ErrImmutable
)

// Component coordinates two generated table writers in one managed unit. It
// does not read or mutate tables directly and is private to the host.
type Component struct {
	Contract xdatly.Component[Input, Output] `component:"ReportAdoption,path=/v1/internal/forge/reporting/adopt,method=POST,handler=NewAdoption,internal=true"`
}

type Input struct {
	Run                     *runstore.Record     `parameter:"Run,kind=body,in=run,required=true"`
	Context                 *contextstore.Record `parameter:"Context,kind=body,in=context,required=true"`
	ExpectedRunRevision     int64                `parameter:"ExpectedRunRevision,kind=body,in=expectedRunRevision,required=true"`
	ExpectedContextRevision int64                `parameter:"ExpectedContextRevision,kind=body,in=expectedContextRevision,required=true"`
}

type Output struct {
	ReportRunID     string `json:"reportRunId"`
	ConversationID  string `json:"conversationId"`
	RunRevision     int64  `json:"runRevision"`
	ContextRevision int64  `json:"contextRevision"`
}

type Handler struct{}

func NewAdoption() handler.Contract[Input, Output] { return &Handler{} }

var _ handler.Contract[Input, Output] = (*Handler)(nil)

func Exports() (*x.Registry, error) {
	registry := x.NewRegistry()
	for _, typ := range []reflect.Type{reflect.TypeFor[Input](), reflect.TypeFor[Output](), reflect.TypeFor[runstore.Record](), reflect.TypeFor[contextstore.Record]()} {
		registry.Register(x.NewType(typ))
	}
	factory, err := x.NewFunction(reflect.TypeFor[Component]().PkgPath(), "NewAdoption", custom.Factory(NewAdoption))
	if err != nil {
		return nil, err
	}
	if err := registry.RegisterFunctions(factory); err != nil {
		return nil, err
	}
	return registry, nil
}

func (*Handler) Exec(ctx context.Context, session handler.Session, input *Input, output *Output) error {
	if session == nil || session.Binder() == nil || input == nil || output == nil {
		return fmt.Errorf("report adoption invocation is incomplete")
	}
	deps := struct {
		Invoker dexec.ComponentInvoker     `bind:"kind=component_invoker,required"`
		Starter handler.TransactionStarter `bind:"kind=transactionStarter,required"`
		Owner   *string                    `bind:"kind=visibility,in=subject,required"`
	}{}
	if err := session.Binder().Bind(ctx, &deps); err != nil {
		return err
	}
	if deps.Invoker == nil || deps.Starter == nil || deps.Owner == nil {
		return fmt.Errorf("report adoption capabilities are unavailable")
	}
	owner := strings.TrimSpace(*deps.Owner)
	run, pointer := input.Run, input.Context
	if owner == "" || run == nil || pointer == nil || owner != strings.TrimSpace(run.OwnerID) || owner != strings.TrimSpace(pointer.OwnerID) {
		return ErrNotFound
	}
	if run.Revision != input.ExpectedRunRevision+1 || pointer.Revision != input.ExpectedContextRevision+1 {
		return ErrCASMismatch
	}
	if strings.TrimSpace(run.ConversationID) == "" || run.ConversationID != pointer.ConversationID ||
		strings.TrimSpace(run.ReportRunID) == "" || run.ReportRunID != pointer.ActiveReportRunID {
		return ErrNotFound
	}
	if strings.TrimSpace(run.AdoptionSource) == "" || strings.TrimSpace(run.ActorID) != owner ||
		strings.TrimSpace(pointer.ActivationSource) == "" || strings.TrimSpace(pointer.ActorID) != owner {
		return ErrImmutable
	}
	// Both reads and child writers join this parent's managed database unit.
	if err := deps.Starter.Start(ctx); err != nil {
		return err
	}
	runs := &runstore.Store{Invoker: deps.Invoker, OwnerID: func(context.Context) string { return owner }}
	contexts := &contextstore.Store{Invoker: deps.Invoker, OwnerID: func(context.Context) string { return owner }}
	currentRun, err := runs.Get(ctx, run.ReportRunID)
	if err != nil {
		return err
	}
	if currentRun.Revision != input.ExpectedRunRevision {
		return ErrCASMismatch
	}
	currentPointer, err := contexts.Get(ctx, pointer.ConversationID)
	switch {
	case errors.Is(err, contextstore.ErrNotFound) && input.ExpectedContextRevision == 0:
		// An absent pointer is the expected create case.
	case errors.Is(err, contextstore.ErrNotFound):
		return ErrCASMismatch
	case err != nil:
		return err
	case currentPointer.Revision != input.ExpectedContextRevision:
		return ErrCASMismatch
	}
	if err := runs.AdoptCAS(ctx, run, input.ExpectedRunRevision); err != nil {
		return err
	}
	if err := contexts.PutCAS(ctx, pointer, input.ExpectedContextRevision); err != nil {
		if errors.Is(err, contextstore.ErrCASMismatch) {
			return ErrCASMismatch
		}
		if errors.Is(err, contextstore.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	output.ReportRunID, output.ConversationID = run.ReportRunID, run.ConversationID
	output.RunRevision, output.ContextRevision = run.Revision, pointer.Revision
	return nil
}
