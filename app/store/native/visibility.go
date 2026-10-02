package native

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	read "github.com/viant/agently-core/internal/datly/conversation/read"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

var ErrConversationNotVisible = errors.New("conversation not found")

var visibleConversationTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/api/agently/conversation/{id}"},
}

// RequireVisibleConversation authorizes one public caller against the generated
// conversation visibility predicate. Its list scope enforces owner visibility;
// a missing row and a private row owned by another user return the same error.
func RequireVisibleConversation(ctx context.Context, invoker dexec.ComponentInvoker, conversationID string) error {
	if invoker == nil || strings.TrimSpace(conversationID) == "" {
		return ErrConversationNotVisible
	}
	input := &read.ConversationInput{}
	input.SetId(strings.TrimSpace(conversationID))
	value, err := invoker.InvokeComponent(WithAccess(ctx, Access{ListMode: true}), dexec.ComponentRequest{Target: visibleConversationTarget, Input: input})
	if err != nil {
		return err
	}
	output, ok := value.(*read.ConversationOutput)
	if !ok || output == nil {
		return fmt.Errorf("conversation reader returned %T", value)
	}
	if len(output.Data) == 0 {
		return ErrConversationNotVisible
	}
	return nil
}
