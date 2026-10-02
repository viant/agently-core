package window

import (
	"context"
	workspaceproto "github.com/viant/agently-core/protocol/ui/workspace"
	"github.com/viant/agently-core/runtime/requestctx"
	registry "github.com/viant/agently-core/service/ui/window/registry"
	"testing"
)

func TestShowCreatesTurnReferenceForMenuWorkspace(t *testing.T) {
	ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{ConversationID: "c", TurnID: "turn-2"})
	window := &registry.WindowSnapshot{WindowID: "menu-advertisers", WindowKey: "advertiserList", WindowTitle: "Advertisers", ConversationID: "c", Presentation: "hosted", Region: "chat.top"}
	descriptor := workspaceForShow(ctx, window)
	if descriptor == nil || descriptor.Origin.TurnID != "turn-2" || descriptor.LastActivatedBy.TurnID != "turn-2" || descriptor.Content.WindowKey != "advertiserList" {
		t.Fatalf("missing reference: %+v", descriptor)
	}
	if window.WorkspaceObject != nil {
		t.Fatal("must not mutate the registry snapshot")
	}
	descriptor.Origin = workspaceproto.Origin{TurnID: "original"}
	window.WorkspaceObject = descriptor
	next := workspaceForShow(ctx, window)
	if next.Origin.TurnID != "original" || next.LastActivatedBy.TurnID != "turn-2" {
		t.Fatal("lost original origin or latest activation")
	}
	if workspaceForShow(ctx, &registry.WindowSnapshot{WindowID: "dialog", WindowKey: "dialog"}) != nil {
		t.Fatal("ordinary dialogs must not become workspace references")
	}
}
