package executor

import (
	"github.com/stretchr/testify/require"
	windowprotocol "github.com/viant/agently-core/protocol/window"
	"testing"
)

func TestLegacyProducerClassificationDoesNotPreventHostStartup(t *testing.T) {
	b := NewBuilder().WithNativeComponentProducers("old-native")
	require.NoError(t, b.configureComponentTransport(&Runtime{}))
	require.Empty(t, b.nativeComponentServices)
}
func TestProviderLocalComponentDispatcherDoesNotConfigureMCPWirePolicy(t *testing.T) {
	dispatcher, err := windowprotocol.NewDeclaredComponentDispatcher([]string{"provider-local"}, nil)
	require.NoError(t, err)
	runtime := &Runtime{ComponentDispatcher: dispatcher}
	require.NoError(t, NewBuilder().configureComponentTransport(runtime))
	require.Same(t, dispatcher, runtime.ComponentDispatcher)
}
