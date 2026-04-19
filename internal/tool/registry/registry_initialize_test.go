package tool_test

import (
	"context"
	"os"
	"strings"
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

func TestRegistry_Initialize_DoesNotBlockOnConfiguredRemoteServers(t *testing.T) {
	testCases := []struct {
		name       string
		extraMCP   string
		expectTool string
	}{
		{
			name:       "remote discovery names do not block internal warmup",
			extraMCP:   "steward,operation,forecasting",
			expectTool: "system/exec:status",
		},
	}

	original := os.Getenv("AGENTLY_MCP_SERVERS")
	defer func() {
		if original == "" {
			_ = os.Unsetenv("AGENTLY_MCP_SERVERS")
			return
		}
		_ = os.Setenv("AGENTLY_MCP_SERVERS", original)
	}()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, os.Setenv("AGENTLY_MCP_SERVERS", tc.extraMCP))

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
				t.Fatal("registry.Initialize blocked on remote MCP discovery")
			}

			require.Eventually(t, func() bool {
				def, ok := reg.GetDefinition(tc.expectTool)
				return ok && def != nil
			}, time.Second, 25*time.Millisecond)
		})
	}
}

func TestRegistry_Initialize_DoesNotStartRemoteRefreshMonitors(t *testing.T) {
	original := os.Getenv("AGENTLY_MCP_SERVERS")
	defer func() {
		if original == "" {
			_ = os.Unsetenv("AGENTLY_MCP_SERVERS")
			return
		}
		_ = os.Setenv("AGENTLY_MCP_SERVERS", original)
	}()
	require.NoError(t, os.Setenv("AGENTLY_MCP_SERVERS", "remote"))

	mgr, err := manager.New(nil)
	require.NoError(t, err)

	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	require.NoError(t, reg.AddInternalService(agents.New(nil)))
	require.NoError(t, reg.AddInternalService(execsvc.New()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg.Initialize(ctx)
	time.Sleep(150 * time.Millisecond)

	warnings := reg.LastWarnings()
	for _, warning := range warnings {
		if strings.Contains(warning, "remote") {
			t.Fatalf("unexpected remote refresh warning: %s", warning)
		}
	}

	require.Eventually(t, func() bool {
		def, ok := reg.GetDefinition("system/exec:status")
		return ok && def != nil
	}, time.Second, 25*time.Millisecond)
}
