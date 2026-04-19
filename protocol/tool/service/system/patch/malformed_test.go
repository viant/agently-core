package patch_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	patch "github.com/viant/agently-core/protocol/tool/service/system/patch"
)

func TestSession_ApplyPatch_RejectsMalformedPatch(t *testing.T) {
	t.Parallel()

	session, err := patch.NewSession()
	require.NoError(t, err)

	err = session.ApplyPatch(context.Background(), "BROKEN PATCH")
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse patch")

	changes, snapErr := session.Snapshot(context.Background())
	require.NoError(t, snapErr)
	require.Empty(t, changes)
}
