package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
)

func TestBindEffectiveUserFromInput(t *testing.T) {
	t.Run("sets user from query input when context has no user", func(t *testing.T) {
		ctx := bindEffectiveUserFromInput(context.Background(), &QueryInput{UserId: "e2e-queue-user"})
		require.Equal(t, "e2e-queue-user", authctx.EffectiveUserID(ctx))
	})

	t.Run("keeps existing context user", func(t *testing.T) {
		base := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "ctx-user"})
		ctx := bindEffectiveUserFromInput(base, &QueryInput{UserId: "input-user"})
		require.Equal(t, "ctx-user", authctx.EffectiveUserID(ctx))
	})
}
