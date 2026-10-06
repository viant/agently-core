// Package command composes resource mutations and AG-UI journal completion in
// one native Datly root transaction, through a trusted application capability.
package command

import (
	"context"
	"reflect"
	"time"

	store "github.com/viant/agently-core/app/store/agui"
	dexec "github.com/viant/datly/exec"
	rh "github.com/viant/datly/runtime/handler"
	"github.com/viant/datly/runtime/handler/custom"
	xdatly "github.com/viant/xdatly"
	"github.com/viant/xdatly/handler"
)

type CommandComponent struct {
	Contract xdatly.Component[store.CommandInput, store.CommandOutput] `component:"Command,path=/v1/internal/agently/ag-ui/command,method=POST,connector=agently,handler=NewCommand,internal=true"`
}

var CommandDatly = reflect.TypeFor[CommandComponent]()

type Command struct{}

func NewCommand() handler.Contract[store.CommandInput, store.CommandOutput] { return &Command{} }
func (CommandComponent) DatlyHandler(name string) func() (rh.TypedHandler, error) {
	if name != "NewCommand" {
		return nil
	}
	return custom.Factory(NewCommand)
}
func (*Command) Exec(ctx context.Context, session handler.Session, input *store.CommandInput, output *store.CommandOutput) error {
	deps := struct {
		Invoker  dexec.ComponentInvoker     `bind:"kind=component_invoker,required"`
		Starter  handler.TransactionStarter `bind:"kind=transactionStarter,required"`
		Executor store.CommandExecutor      `bind:"kind=aguiCommandExecutor,required"`
	}{}
	if err := session.Binder().Bind(ctx, &deps); err != nil {
		return err
	}
	ctx = dexec.WithTransactionIsolation(ctx, dexec.IsolationSerializable)
	if err := deps.Starter.Start(ctx); err != nil {
		return err
	}
	repository := store.New(deps.Invoker)
	record, err := repository.GetRun(ctx, input.Principal, input.ThreadID, input.RunID)
	if err != nil {
		return err
	}
	// Committed command retries return the stored terminal journal without invoking
	// the application capability again. No tentative outcome is adopted.
	if record.Status == store.StatusFinished {
		output.Run = record
		return nil
	}
	if record.Revision != input.ExpectedRevision || input.LeaseOwner == "" || record.LeaseOwner != input.LeaseOwner || record.LeaseUntil == nil || !record.LeaseUntil.After(time.Now().UTC()) {
		return store.ErrConflict
	}
	if record.Status != store.StatusAdmitted && record.Status != store.StatusRunning {
		return store.ErrInvalidTransition
	}
	events, err := deps.Executor.Execute(ctx, deps.Invoker, record)
	if err != nil {
		return err
	}
	next, err := repository.Append(ctx, input.Principal, input.ThreadID, input.RunID, record.Revision, events, &store.Change{LeaseOwner: input.LeaseOwner})
	if err != nil {
		return err
	}
	if next.Status != store.StatusFinished {
		return store.ErrInvalidTransition
	}
	output.Run = next
	return nil
}
