package tool_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	registry "github.com/viant/agently-core/internal/tool/registry"
	"github.com/viant/agently-core/protocol/mcp/manager"
	agents "github.com/viant/agently-core/protocol/tool/service/llm/agents"
	execsvc "github.com/viant/agently-core/protocol/tool/service/system/exec"
)

func TestRegistry_Initialize_InternalCacheableServicesDoesNotDeadlock(t *testing.T) {
	mgr, err := manager.New(nil)
	require.NoError(t, err)

	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	require.NoError(t, reg.AddInternalService(agents.New(nil)))
	require.NoError(t, reg.AddInternalService(execsvc.New()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		reg.Initialize(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("registry.Initialize deadlocked for internal cacheable services")
	}

	require.Eventually(t, func() bool {
		def, ok := reg.GetDefinition("system/exec:status")
		return ok && def != nil
	}, time.Second, 25*time.Millisecond)
}
