package workspace

import (
	"context"
	"github.com/viant/agently-core/runtime/requestctx"
	"testing"
)

func TestOriginAndPendingLifecycle(t *testing.T) {
	ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{TurnID: "turn-1"})
	ctx = requestctx.WithToolMessageID(ctx, "call-1")
	object := New(ctx, "resource-1", "conversation-1")
	if object.Origin.TurnID != "turn-1" || object.Origin.ToolCallID != "call-1" {
		t.Fatal(object.Origin)
	}
	if object.Lifecycle.State != "opening" {
		t.Fatal("unacknowledged object claimed success")
	}
	if object.ObjectID != "workspace:resource-1" || object.Version != 1 {
		t.Fatal(object)
	}
}
