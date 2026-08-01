package tool_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	registry "github.com/viant/agently-core/internal/tool/registry"
	"github.com/viant/agently-core/protocol/mcp/manager"
	svc "github.com/viant/agently-core/protocol/tool/service"
)

type timeoutReplacementService struct {
	serviceTimeout time.Duration
	methodTimeouts map[string]time.Duration
}

func (s *timeoutReplacementService) Name() string { return "test/timeouts" }

func (s *timeoutReplacementService) Methods() svc.Signatures {
	input := reflect.TypeOf(&struct{}{})
	output := reflect.TypeOf(&map[string]string{})
	return svc.Signatures{
		{Name: "fast", Input: input, Output: output},
		{Name: "slow", Input: input, Output: output},
	}
}

func (s *timeoutReplacementService) Method(name string) (svc.Executable, error) {
	return func(_ context.Context, _, output interface{}) error {
		result, ok := output.(*map[string]string)
		if !ok {
			return svc.NewInvalidOutputError(output)
		}
		*result = map[string]string{"method": name}
		return nil
	}, nil
}

func (s *timeoutReplacementService) ToolTimeout() time.Duration {
	return s.serviceTimeout
}

func (s *timeoutReplacementService) MethodToolTimeout(method string) time.Duration {
	return s.methodTimeouts[strings.ToLower(strings.TrimSpace(method))]
}

func TestAddInternalServiceReplacesTimeoutMetadata(t *testing.T) {
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)

	require.NoError(t, reg.AddInternalService(&timeoutReplacementService{
		serviceTimeout: time.Minute,
		methodTimeouts: map[string]time.Duration{"slow": 2 * time.Minute},
	}))
	assertToolTimeout(t, reg, "test/timeouts:fast", time.Minute)
	assertToolTimeout(t, reg, "test/timeouts:slow", 2*time.Minute)

	require.NoError(t, reg.AddInternalService(&timeoutReplacementService{
		methodTimeouts: map[string]time.Duration{"slow": 3 * time.Minute},
	}))
	assertNoToolTimeout(t, reg, "test/timeouts:fast")
	assertToolTimeout(t, reg, "test/timeouts:slow", 3*time.Minute)

	require.NoError(t, reg.AddInternalService(&timeoutReplacementService{
		serviceTimeout: 4 * time.Minute,
	}))
	assertToolTimeout(t, reg, "test/timeouts:fast", 4*time.Minute)
	assertToolTimeout(t, reg, "test/timeouts:slow", 4*time.Minute)

	require.NoError(t, reg.AddInternalService(&timeoutReplacementService{}))
	assertNoToolTimeout(t, reg, "test/timeouts:fast")
	assertNoToolTimeout(t, reg, "test/timeouts:slow")
}

func assertToolTimeout(t *testing.T, reg *registry.Registry, name string, expected time.Duration) {
	t.Helper()
	actual, ok := reg.ToolTimeout(name)
	require.True(t, ok, name)
	require.Equal(t, expected, actual, name)
}

func assertNoToolTimeout(t *testing.T, reg *registry.Registry, name string) {
	t.Helper()
	actual, ok := reg.ToolTimeout(name)
	require.False(t, ok, name)
	require.Zero(t, actual, name)
}
