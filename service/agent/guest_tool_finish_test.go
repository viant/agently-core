package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	iauth "github.com/viant/agently-core/internal/auth"
	convservice "github.com/viant/agently-core/internal/service/conversation"
)

func TestCompleteGuestToolPreservesOtherConversationWork(t *testing.T) {
	for _, otherStatus := range []string{"running", "queued", "waiting_for_user"} {
		t.Run(otherStatus, func(t *testing.T) {
			ctx := iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "owner"})
			runtime, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
			require.NoError(t, err)
			defer runtime.Shutdown(ctx)
			conv, err := convservice.New(ctx, runtime)
			require.NoError(t, err)
			row := apiconv.NewConversation()
			row.SetId("thread")
			row.SetCreatedByUserID("owner")
			row.SetStatus("running")
			require.NoError(t, conv.PatchConversations(ctx, row))
			svc := &Service{conversation: conv, dataService: data.NewService(runtime)}
			own, err := svc.ensureGuestTurn(ctx, "thread")
			require.NoError(t, err)
			other := apiconv.NewTurn()
			other.SetId("other")
			other.SetConversationID("thread")
			other.SetStatus(otherStatus)
			require.NoError(t, conv.PatchTurn(ctx, other))
			require.NoError(t, svc.CompleteGuestToolCall(ctx, "thread", own, "succeeded", nil))
			result, err := conv.GetConversation(ctx, "thread", apiconv.WithIncludeTranscript(true))
			require.NoError(t, err)
			require.NotNil(t, result.Status)
			require.Equal(t, otherStatus, *result.Status, "conversation retains the other work's live lifecycle")
			statuses := map[string]string{}
			for _, turn := range result.Transcript {
				statuses[turn.Id] = turn.Status
				if turn.Id == own {
					require.NotNil(t, turn.Origin)
					require.Equal(t, "host_request", *turn.Origin)
				}
			}
			require.Equal(t, "succeeded", statuses[own])
			require.Equal(t, otherStatus, statuses["other"])
			require.Error(t, svc.CompleteGuestToolCall(ctx, "thread", "other", "failed", nil), "normal turns are not host turns")
		})
	}
}
