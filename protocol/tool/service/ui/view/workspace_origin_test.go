package view

import (
	"context"
	workspaceproto "github.com/viant/agently-core/protocol/ui/workspace"
	"github.com/viant/agently-core/runtime/requestctx"
	uireg "github.com/viant/agently-core/service/ui/window/registry"
	forgeuisvc "github.com/viant/forge/backend/mcp/service"
	"testing"
)

func TestWorkspaceDescriptorKeepsOriginAndRecordsLatestActivation(t *testing.T) {
	service := New(nil, forgeuisvc.NewService(&forgeuisvc.Config{}))
	original := workspaceproto.New(requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{TurnID: "original-turn"}), "resource-1", "origin-test-conversation")
	original.Lifecycle.State = "ready"
	service.reg.RecordConversationEvent("origin-test-conversation", uireg.UIEvent{ConversationID: "origin-test-conversation", WindowID: "resource-1", Kind: "view.open", Actor: "agent", Detail: map[string]interface{}{"workspaceObject": original}})
	ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{TurnID: "latest-turn"})
	next := service.workspaceDescriptor(ctx, "resource-1", "origin-test-conversation", &ListItem{WindowKey: "resource"}, map[string]interface{}{"id": 1})
	if next.Origin.TurnID != "original-turn" || next.LastActivatedBy.TurnID != "latest-turn" {
		t.Fatalf("origin/activation lost: %+v", next)
	}
	if next.Revision != original.Revision+1 || next.Lifecycle.CreatedAt != original.Lifecycle.CreatedAt {
		t.Fatal("revision or creation date lost")
	}
	if next.Lifecycle.State != "opening" {
		t.Fatal("unacknowledged repeat open claimed ready")
	}
}

func TestHostedViewDefaultsToIndependentWorkspace(t *testing.T) {
	options := buildOpenWindowOptions(&ListItem{Presentation: "hosted", Region: "chat.top"}, "conversation-1", "")
	if options["replaceHostedRegion"] != false || options["parentKey"] != "chat/new" {
		t.Fatalf("wrong workspace defaults: %#v", options)
	}
	overridden := buildOpenWindowOptions(&ListItem{Presentation: "hosted", Region: "chat.top"}, "conversation-1", "replace")
	if overridden["replaceHostedRegion"] != true {
		t.Fatal("explicit replacement override ignored")
	}
}
