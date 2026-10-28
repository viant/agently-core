package reporting

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrepareRuntimeDoesNotResolveReports(t *testing.T) {
	service := New(Options{Store: NewMemoryStore()})
	// No report catalog, resource resolver, or dataset executor is installed.
	require.NoError(t, service.PrepareRuntime(context.Background()))
	require.NoError(t, service.PrepareRuntime(context.Background()))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, service.PrepareRuntime(ctx), context.Canceled)
}
