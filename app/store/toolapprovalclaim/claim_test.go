package toolapprovalclaim_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/app/store/toolapprovalclaim"
	iauth "github.com/viant/agently-core/internal/auth"
	conversation "github.com/viant/agently-core/internal/service/conversation"
	model "github.com/viant/agently-core/model/toolapprovalqueue"
	dexec "github.com/viant/datly/exec"
)

func TestApprovalClaimHasOneWinnerAcrossNativeRuntimes(t *testing.T) {
	ctx := iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "owner"})
	workspace := t.TempDir()
	first, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	defer first.Shutdown(ctx)
	second, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	defer second.Shutdown(ctx)
	service, err := conversation.New(ctx, first)
	require.NoError(t, err)
	now := time.Now().UTC()
	row := &model.ToolApprovalQueue{Id: "approval", UserId: "owner", ToolName: "test/tool", Arguments: []byte(`{"safe":true}`), Status: "pending", CreatedAt: &now, UpdatedAt: &now, Has: &model.ToolApprovalQueueHas{Id: true, UserId: true, ToolName: true, Arguments: true, Status: true, CreatedAt: true, UpdatedAt: true}}
	require.NoError(t, service.PatchToolApprovalQueue(ctx, row))
	rows, err := service.ListToolApprovalQueues(ctx, &model.QueueRowsInput{Id: "approval", Has: &model.QueueRowsInputHas{Id: true}})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	ready := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, runtime := range []dexec.ComponentInvoker{first, second} {
		workers.Add(1)
		go func(invoker dexec.ComponentInvoker) {
			defer workers.Done()
			<-ready
			_, err := toolapprovalclaim.Claim(ctx, invoker, rows[0], "owner", "approve")
			results <- err
		}(runtime)
	}
	close(ready)
	workers.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		}
	}
	require.Equal(t, 1, wins)
	updated, err := service.ListToolApprovalQueues(ctx, &model.QueueRowsInput{Id: "approval", Has: &model.QueueRowsInputHas{Id: true}})
	require.NoError(t, err)
	require.Equal(t, "approved", updated[0].Status)
	_, err = toolapprovalclaim.Claim(ctx, first, rows[0], "other", "approve")
	require.Error(t, err)
}

func TestApprovalClaimRejectsExpiredQueueAtomically(t *testing.T) {
	ctx := iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "owner"})
	runtime, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer runtime.Shutdown(ctx)
	service, err := conversation.New(ctx, runtime)
	require.NoError(t, err)
	expires := time.Now().UTC().Add(-time.Second)
	row := &model.ToolApprovalQueue{Id: "expired", UserId: "owner", ToolName: "test/tool", Arguments: []byte(`{}`), Status: "pending", ExpiresAt: &expires, Has: &model.ToolApprovalQueueHas{Id: true, UserId: true, ToolName: true, Arguments: true, Status: true, ExpiresAt: true}}
	require.NoError(t, service.PatchToolApprovalQueue(ctx, row))
	rows, err := service.ListToolApprovalQueues(ctx, &model.QueueRowsInput{Id: row.Id, Has: &model.QueueRowsInputHas{Id: true}})
	require.NoError(t, err)
	_, err = toolapprovalclaim.Claim(ctx, runtime, rows[0], "owner", "approve")
	require.Error(t, err)
	after, err := service.ListToolApprovalQueues(ctx, &model.QueueRowsInput{Id: row.Id, Has: &model.QueueRowsInputHas{Id: true}})
	require.NoError(t, err)
	require.Equal(t, "pending", after[0].Status)
	_, err = toolapprovalclaim.Claim(ctx, runtime, after[0], "owner", "timeout")
	require.NoError(t, err)
	_, err = toolapprovalclaim.Claim(ctx, runtime, after[0], "owner", "timeout")
	require.Error(t, err)
	future := time.Now().UTC().Add(time.Minute)
	row.ExpiresAt = &future
	require.NoError(t, service.PatchToolApprovalQueue(ctx, row))
	rows, err = service.ListToolApprovalQueues(ctx, &model.QueueRowsInput{Id: row.Id, Has: &model.QueueRowsInputHas{Id: true}})
	require.NoError(t, err)
	_, err = toolapprovalclaim.Claim(ctx, runtime, rows[0], "owner", "approve")
	require.NoError(t, err)
}
