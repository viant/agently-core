package auth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	iauth "github.com/viant/agently-core/internal/auth"
)

func TestVerifiedContextSnapshotPreservesOnlyIngressAuth(t *testing.T) {
	user := ownerFixture()
	user.CanonicalUserID = ""
	type requestOnlyKey struct{}
	request, cancel := context.WithCancel(context.WithValue(context.Background(), requestOnlyKey{}, "request-only"))
	request = withRuntimeAuthUser(request, &user)
	before, err := LocalStateOwner(request)
	require.NoError(t, err)
	snapshot, err := CaptureVerifiedContext(request)
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	cancel()
	type workerKey struct{}
	worker, stop := context.WithCancel(context.WithValue(context.Background(), workerKey{}, "worker"))
	defer stop()
	restored, err := snapshot.Restore(worker)
	require.NoError(t, err)
	after, err := LocalStateOwner(restored)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Nil(t, restored.Value(requestOnlyKey{}))
	require.Equal(t, "worker", restored.Value(workerKey{}))
	require.Empty(t, iauth.CanonicalUserID(restored))
	stop()
	require.ErrorIs(t, restored.Err(), context.Canceled)

	_, err = snapshot.Restore(InjectUser(context.Background(), "other"))
	require.Error(t, err)
	snapshot.user.ValidUntil = time.Now().Add(-time.Second)
	_, err = snapshot.Restore(context.Background())
	require.Error(t, err)

	public := InjectTokens(InjectUser(context.Background(), user.Subject), user.Tokens)
	snapshot, err = CaptureVerifiedContext(public)
	require.NoError(t, err)
	require.Nil(t, snapshot)
}
