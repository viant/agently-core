// Package elicitationreceipt provides native transaction ownership for exact
// human-answer persistence and defers wakeups until confirmed commit.
package elicitationreceipt

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/handler"
)

const ApplierKey handler.ValueKey = "elicitationReceiptApplier"

type Input struct {
	ConversationID string          `parameter:"ConversationID,kind=body,in=conversationId"`
	ElicitationID  string          `parameter:"ElicitationID,kind=body,in=elicitationId"`
	Principal      string          `parameter:"Principal,kind=body,in=principal"`
	Action         string          `parameter:"Action,kind=body,in=action"`
	Payload        json.RawMessage `parameter:"Payload,kind=body,in=payload"`
	Reason         string          `parameter:"Reason,kind=body,in=reason"`
}
type Receipt struct {
	AuthoritativeConversationID string          `json:"authoritativeConversationId"`
	Version                     string          `json:"version"`
	Principal                   string          `json:"principal"`
	ConversationID              string          `json:"conversationId"`
	ElicitationID               string          `json:"elicitationId"`
	RequestMessageID            string          `json:"requestMessageId"`
	Action                      string          `json:"action"`
	Payload                     json.RawMessage `json:"payload,omitempty"`
	Reason                      string          `json:"reason,omitempty"`
	ProxyConversationID         string          `json:"proxyConversationId,omitempty"`
}
type Output struct{ Receipt *Receipt }
type Applier interface {
	Apply(context.Context, dexec.ComponentInvoker, *Input) (*Receipt, error)
}

func Resolve(ctx context.Context, invoker dexec.ComponentInvoker, input *Input, applier Applier) (*Receipt, handler.Outcome, error) {
	var outcome handler.Outcome
	if invoker == nil || input == nil || applier == nil {
		return nil, outcome, fmt.Errorf("atomic elicitation resolution is unavailable")
	}
	value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: "github.com/viant/agently-core/internal/datly/elicitation/resolve", Name: "Resolve"}, Route: spec.RouteRef{Method: "POST", Path: "/v1/internal/agently/elicitation/resolve"}}, Input: input, Providers: []locator.Provider{provider.Static(ApplierKey, applier)}, Completion: func(actual handler.Outcome) { outcome = actual.Clone() }})
	if err != nil {
		return nil, outcome, err
	}
	output, ok := value.(*Output)
	if !ok || output == nil || output.Receipt == nil {
		return nil, outcome, fmt.Errorf("invalid atomic elicitation output")
	}
	if !outcome.CommitConfirmed() {
		return nil, outcome, fmt.Errorf("elicitation resolution did not commit; waiter remains blocked")
	}
	return output.Receipt, outcome, nil
}
