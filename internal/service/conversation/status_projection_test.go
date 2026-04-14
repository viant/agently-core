package conversation

import (
	"context"
	"strings"
	"testing"
	"time"

	convcli "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	convwrite "github.com/viant/agently-core/pkg/agently/conversation/write"
	turnwrite "github.com/viant/agently-core/pkg/agently/turn/write"
	"github.com/viant/datly"
)

func TestGetConversation_UsesLatestTurnStatusOverStaleConversationStatus(t *testing.T) {
	ctx := context.Background()

	tmp := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", tmp)
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")

	dao, err := NewDatly(ctx)
	if err != nil {
		t.Fatalf("NewDatly: %v", err)
	}
	svc, err := New(ctx, dao)
	if err != nil {
		t.Fatalf("conversation.New: %v", err)
	}

	convID := "conv_status_projection"
	conv := &convcli.MutableConversation{}
	conv.Has = &convwrite.ConversationHas{}
	conv.SetId(convID)
	conv.SetStatus("running")
	conv.SetVisibility("private")
	if err := svc.PatchConversations(ctx, conv); err != nil {
		t.Fatalf("PatchConversations: %v", err)
	}

	turn := convcli.NewTurn()
	turn.Has = &turnwrite.TurnHas{}
	turn.SetId("turn_status_projection")
	turn.SetConversationID(convID)
	turn.SetStatus("succeeded")
	turn.SetCreatedAt(time.Now())
	if err := svc.PatchTurn(ctx, turn); err != nil {
		t.Fatalf("PatchTurn: %v", err)
	}

	got, err := svc.GetConversation(ctx, convID, convcli.WithIncludeTranscript(true))
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if got == nil || got.Status == nil {
		t.Fatalf("expected conversation status, got %#v", got)
	}
	if *got.Status != "succeeded" {
		t.Fatalf("expected conversation status succeeded from latest turn, got %q", *got.Status)
	}
}

func TestConversationOutput_DatlyAlreadyReturnsRelationAppliedStage_SQLite(t *testing.T) {
	ctx := context.Background()

	tmp := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", tmp)
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")

	dao, err := NewDatly(ctx)
	if err != nil {
		t.Fatalf("NewDatly: %v", err)
	}
	svc, err := New(ctx, dao)
	if err != nil {
		t.Fatalf("conversation.New: %v", err)
	}

	convID := "conv_manual_on_relation"
	conv := &convcli.MutableConversation{}
	conv.Has = &convwrite.ConversationHas{}
	conv.SetId(convID)
	conv.SetStatus("running")
	conv.SetVisibility("private")
	if err := svc.PatchConversations(ctx, conv); err != nil {
		t.Fatalf("PatchConversations: %v", err)
	}

	turn := convcli.NewTurn()
	turn.Has = &turnwrite.TurnHas{}
	turn.SetId("turn_manual_on_relation")
	turn.SetConversationID(convID)
	turn.SetStatus("succeeded")
	turn.SetCreatedAt(time.Now())
	if err := svc.PatchTurn(ctx, turn); err != nil {
		t.Fatalf("PatchTurn: %v", err)
	}

	in := agconv.ConversationInput{Id: convID, Has: &agconv.ConversationInputHas{Id: true, IncludeTranscript: true}}
	in.IncludeTranscript = true
	out := &agconv.ConversationOutput{}
	uri := strings.ReplaceAll(agconv.ConversationPathURI, "{id}", convID)
	if _, err := dao.Operate(ctx, datly.WithOutput(out), datly.WithURI(uri), datly.WithInput(&in)); err != nil {
		t.Fatalf("dao.Operate: %v", err)
	}
	if len(out.Data) != 1 {
		t.Fatalf("expected 1 row, got %d", len(out.Data))
	}
	if got := strings.TrimSpace(out.Data[0].Stage); got != "done" {
		t.Fatalf("expected raw datly output stage to already be done, got %q", got)
	}
	if out.Data[0].Status == nil || *out.Data[0].Status != "succeeded" {
		t.Fatalf("expected raw datly output status to already be succeeded, got %#v", out.Data[0].Status)
	}

	beforeStage := out.Data[0].Stage
	beforeStatus := *out.Data[0].Status
	out.Data[0].OnRelation(ctx)
	if out.Data[0].Stage != beforeStage {
		t.Fatalf("expected manual OnRelation to be idempotent for stage, got %q -> %q", beforeStage, out.Data[0].Stage)
	}
	if out.Data[0].Status == nil || *out.Data[0].Status != beforeStatus {
		t.Fatalf("expected manual OnRelation to be idempotent for status, got %q -> %#v", beforeStatus, out.Data[0].Status)
	}
}
