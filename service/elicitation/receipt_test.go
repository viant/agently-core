package elicitation

import (
	"context"
	"fmt"
	receiptstore "github.com/viant/agently-core/app/store/elicitationreceipt"
	dexec "github.com/viant/datly/exec"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	conv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/native"
	auth "github.com/viant/agently-core/internal/auth"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	"github.com/viant/agently-core/protocol/agent/execution"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/elicitation/router"
	"github.com/viant/mcp-protocol/schema"
)

func TestCheckedResolutionCommitsExactReceiptBeforeWakeAndRejectsConflict(t *testing.T) {
	ctx := auth.WithUserInfo(context.Background(), &auth.UserInfo{Subject: "owner"})
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	client, err := convservice.New(ctx, server)
	require.NoError(t, err)
	root := conv.NewConversation()
	root.SetId("thread")
	root.SetCreatedByUserID("owner")
	root.SetStatus("active")
	require.NoError(t, client.PatchConversations(ctx, root))
	turn := conv.NewTurn()
	turn.SetId("turn")
	turn.SetConversationID("thread")
	turn.SetStatus("waiting_for_user")
	require.NoError(t, client.PatchTurn(ctx, turn))
	r := router.New()
	service := New(client, nil, r, NoopAwaiterFactory())
	request := &execution.Elicitation{ElicitRequestParams: schema.ElicitRequestParams{Message: "Choose a color", ElicitationId: "ask"}}
	_, err = service.Record(ctx, &requestctx.TurnMeta{ConversationID: "thread", TurnID: "turn"}, "assistant", request)
	require.NoError(t, err)
	wake := make(chan *schema.ElicitResult, 1)
	r.RegisterByElicitationID("thread", "ask", wake)
	resolution, err := service.ResolveChecked(ctx, "thread", "ask", "accept", map[string]interface{}{"color": "red"}, "")
	require.NoError(t, err)
	require.Equal(t, ResolutionWokeExisting, resolution.Disposition)
	select {
	case answer := <-wake:
		require.Equal(t, "red", answer.Content["color"])
	case <-time.After(time.Second):
		t.Fatal("committed answer did not wake waiter")
	}
	receipt, err := service.InspectResolution(ctx, "thread", "ask", "accept", map[string]interface{}{"color": "red"}, "")
	require.NoError(t, err)
	require.NotNil(t, receipt)
	_, err = service.ResolveChecked(ctx, "thread", "ask", "accept", map[string]interface{}{"color": "red"}, "")
	require.NoError(t, err)
	_, err = service.ResolveChecked(ctx, "thread", "ask", "accept", map[string]interface{}{"color": "blue"}, "")
	require.Error(t, err)
	_, err = service.ResolveChecked(ctx, "thread", "ask", "cancel", nil, "")
	require.Error(t, err)
}
func TestCheckedResolutionHasOneAnswerAcrossNativeRuntimes(t *testing.T) {
	ctx := auth.WithUserInfo(context.Background(), &auth.UserInfo{Subject: "owner"})
	workspace := t.TempDir()
	first, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	defer first.Shutdown(ctx)
	second, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	defer second.Shutdown(ctx)
	c1, err := convservice.New(ctx, first)
	require.NoError(t, err)
	c2, err := convservice.New(ctx, second)
	require.NoError(t, err)
	root := conv.NewConversation()
	root.SetId("thread")
	root.SetCreatedByUserID("owner")
	root.SetStatus("active")
	require.NoError(t, c1.PatchConversations(ctx, root))
	turn := conv.NewTurn()
	turn.SetId("turn")
	turn.SetConversationID("thread")
	turn.SetStatus("waiting_for_user")
	require.NoError(t, c1.PatchTurn(ctx, turn))
	s1 := New(c1, nil, router.New(), NoopAwaiterFactory())
	s2 := New(c2, nil, router.New(), NoopAwaiterFactory())
	request := &execution.Elicitation{ElicitRequestParams: schema.ElicitRequestParams{Message: "Choose a color", ElicitationId: "ask"}}
	_, err = s1.Record(ctx, &requestctx.TurnMeta{ConversationID: "thread", TurnID: "turn"}, "assistant", request)
	require.NoError(t, err)
	ready := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for i, service := range []*Service{s1, s2} {
		group.Add(1)
		go func(index int, service *Service) {
			defer group.Done()
			<-ready
			color := []string{"red", "blue"}[index]
			_, err := service.ResolveChecked(ctx, "thread", "ask", "accept", map[string]interface{}{"color": color}, "")
			results <- err
		}(i, service)
	}
	close(ready)
	group.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		}
	}
	require.Equal(t, 1, wins)
	stored, err := readAnswerReceipt(ctx, c1, "thread", "ask")
	require.NoError(t, err)
	require.NotNil(t, stored)
}

type failingReceiptApplier struct{ base *receiptApplier }

func (a failingReceiptApplier) Apply(ctx context.Context, invoker dexec.ComponentInvoker, input *receiptstore.Input) (*receiptstore.Receipt, error) {
	if _, err := a.base.Apply(ctx, invoker, input); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("late failure after answer writes")
}
func TestCheckedResolutionLateFailureRollsBackReceiptAnswerAndDoesNotWake(t *testing.T) {
	ctx := auth.WithUserInfo(context.Background(), &auth.UserInfo{Subject: "owner"})
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	client, err := convservice.New(ctx, server)
	require.NoError(t, err)
	root := conv.NewConversation()
	root.SetId("thread")
	root.SetCreatedByUserID("owner")
	root.SetStatus("active")
	require.NoError(t, client.PatchConversations(ctx, root))
	turn := conv.NewTurn()
	turn.SetId("turn")
	turn.SetConversationID("thread")
	turn.SetStatus("waiting_for_user")
	require.NoError(t, client.PatchTurn(ctx, turn))
	r := router.New()
	service := New(client, nil, r, NoopAwaiterFactory())
	request := &execution.Elicitation{ElicitRequestParams: schema.ElicitRequestParams{Message: "Choose a color", ElicitationId: "ask"}}
	_, err = service.Record(ctx, &requestctx.TurnMeta{ConversationID: "thread", TurnID: "turn"}, "assistant", request)
	require.NoError(t, err)
	wake := make(chan *schema.ElicitResult, 1)
	r.RegisterByElicitationID("thread", "ask", wake)
	input := receiptInput(ctx, "thread", "ask", "accept", map[string]interface{}{"color": "red"}, "")
	_, _, err = receiptstore.Resolve(ctx, server, input, failingReceiptApplier{base: &receiptApplier{service: service, native: client}})
	require.ErrorContains(t, err, "late failure")
	receipt, err := readAnswerReceipt(ctx, client, "thread", "ask")
	require.NoError(t, err)
	require.Nil(t, receipt)
	persisted, err := client.GetMessageByElicitation(ctx, "thread", "ask")
	require.NoError(t, err)
	require.Equal(t, "pending", *persisted.Status)
	require.True(t, r.HasWaiter("thread", "ask"))
	select {
	case <-wake:
		t.Fatal("rolled back answer woke waiter")
	default:
	}
}
