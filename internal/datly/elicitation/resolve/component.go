package resolve

import (
	"context"
	"reflect"

	"github.com/viant/agently-core/app/store/elicitationreceipt"
	dexec "github.com/viant/datly/exec"
	rh "github.com/viant/datly/runtime/handler"
	"github.com/viant/datly/runtime/handler/custom"
	"github.com/viant/xdatly"
	"github.com/viant/xdatly/handler"
)

type ResolveComponent struct {
	Contract xdatly.Component[elicitationreceipt.Input, elicitationreceipt.Output] `component:"Resolve,path=/v1/internal/agently/elicitation/resolve,method=POST,connector=agently,handler=NewResolve,internal=true"`
}

var ResolveDatly = reflect.TypeFor[ResolveComponent]()

type Resolve struct{}

func NewResolve() handler.Contract[elicitationreceipt.Input, elicitationreceipt.Output] {
	return &Resolve{}
}
func (ResolveComponent) DatlyHandler(name string) func() (rh.TypedHandler, error) {
	if name != "NewResolve" {
		return nil
	}
	return custom.Factory(NewResolve)
}
func (*Resolve) Exec(ctx context.Context, session handler.Session, input *elicitationreceipt.Input, output *elicitationreceipt.Output) error {
	deps := struct {
		Invoker dexec.ComponentInvoker     `bind:"kind=component_invoker,required"`
		Starter handler.TransactionStarter `bind:"kind=transactionStarter,required"`
		Applier elicitationreceipt.Applier `bind:"kind=elicitationReceiptApplier,required"`
	}{}
	if err := session.Binder().Bind(ctx, &deps); err != nil {
		return err
	}
	ctx = dexec.WithTransactionIsolation(ctx, dexec.IsolationSerializable)
	if err := deps.Starter.Start(ctx); err != nil {
		return err
	}
	receipt, err := deps.Applier.Apply(ctx, deps.Invoker, input)
	if err != nil {
		return err
	}
	output.Receipt = receipt
	return nil
}
