package agui

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	xhandler "github.com/viant/xdatly/handler"
)

const CommandExecutorKey xhandler.ValueKey = "aguiCommandExecutor"

// CommandExecutor is a trusted application capability. It receives the native
// scoped invoker, so all its generated child writes join the root transaction.
// It returns the events to commit atomically with those business mutations.
type CommandExecutor interface {
	Execute(context.Context, dexec.ComponentInvoker, *Run) ([]json.RawMessage, error)
}

type CommandInput struct {
	Principal        string `parameter:"Principal,kind=body,in=principal"`
	ThreadID         string `parameter:"ThreadID,kind=body,in=threadId"`
	RunID            string `parameter:"RunID,kind=body,in=runId"`
	ExpectedRevision int64  `parameter:"ExpectedRevision,kind=body,in=expectedRevision"`
	LeaseOwner       string `parameter:"LeaseOwner,kind=body,in=leaseOwner"`
}
type CommandOutput struct{ Run *Run }

// ExecuteCommand uses one native transaction for domain mutations and journal
// completion. It intentionally performs no adapter-level retry or publication.
func ExecuteCommand(ctx context.Context, invoker dexec.ComponentInvoker, input *CommandInput, executor CommandExecutor) (*Run, xhandler.Outcome, error) {
	var outcome xhandler.Outcome
	if invoker == nil || input == nil || executor == nil {
		return nil, outcome, fmt.Errorf("AG-UI command invocation is incomplete")
	}
	value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{
		Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: "github.com/viant/agently-core/internal/datly/agui/command", Name: "Command"}, Route: spec.RouteRef{Method: "POST", Path: "/v1/internal/agently/ag-ui/command"}},
		Input:  input, Providers: []locator.Provider{provider.Static(CommandExecutorKey, executor)}, Completion: func(actual xhandler.Outcome) { outcome = actual.Clone() },
	})
	if err != nil {
		return nil, outcome, err
	}
	output, ok := value.(*CommandOutput)
	if !ok || output == nil {
		return nil, outcome, fmt.Errorf("AG-UI command returned %T", value)
	}
	return output.Run, outcome, nil
}
