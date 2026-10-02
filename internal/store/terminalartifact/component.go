package terminalartifact

import (
	"context"
	"fmt"

	dexec "github.com/viant/datly/exec"
	rh "github.com/viant/datly/runtime/handler"
	custom "github.com/viant/datly/runtime/handler/custom"
	xdatly "github.com/viant/xdatly"
	"github.com/viant/xdatly/handler"
)

type CleanupComponent struct {
	Contract xdatly.Component[Input, Output] `component:"TerminalArtifactCleanup,path=/v1/internal/agently/terminal-artifacts/cleanup,method=POST,handler=NewCleanup,internal=true"`
}
type Cleanup struct{}

func NewCleanup() handler.Contract[Input, Output] { return &Cleanup{} }

// DatlyHandler binds the holder's declared handler to its typed implementation.
// Linked package discovery calls this provider without a host registration list.
func (CleanupComponent) DatlyHandler(name string) func() (rh.TypedHandler, error) {
	if name != "NewCleanup" {
		return nil
	}
	return custom.Factory(NewCleanup)
}

type dependencies struct {
	Invoker  dexec.ComponentInvoker     `bind:"kind=component_invoker,required"`
	Starter  handler.TransactionStarter `bind:"kind=transactionStarter,required"`
	Reporter dexec.MutationReporter     `bind:"kind=mutationReporter,required"`
}

func (*Cleanup) Exec(ctx context.Context, session handler.Session, input *Input, output *Output) error {
	if session == nil || session.Binder() == nil || input == nil || output == nil {
		return fmt.Errorf("terminal artifact cleanup invocation is incomplete")
	}
	if len(input.Candidates) == 0 {
		output.Dispositions = []Disposition{}
		return nil
	}
	deps := dependencies{}
	if err := session.Binder().Bind(ctx, &deps); err != nil {
		return err
	}
	if deps.Invoker == nil || deps.Starter == nil || deps.Reporter == nil {
		return fmt.Errorf("terminal artifact cleanup capabilities unavailable")
	}
	ctx = dexec.WithTransactionIsolation(ctx, dexec.IsolationSerializable)
	if err := deps.Starter.Start(ctx); err != nil {
		return err
	}
	return cleanup(ctx, deps, input, output)
}
