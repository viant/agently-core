package tool_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	registry "github.com/viant/agently-core/internal/tool/registry"
	"github.com/viant/agently-core/protocol/mcp/manager"
	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

type internalExecutionTestService struct{}

func (s *internalExecutionTestService) Name() string { return "test/service" }

func (s *internalExecutionTestService) Methods() svc.Signatures {
	return svc.Signatures{
		{Name: "list", Description: "public", Input: reflect.TypeOf(&struct{}{}), Output: reflect.TypeOf(&map[string]string{})},
		{Name: "topology", Description: "internal", Internal: true, Input: reflect.TypeOf(&struct{}{}), Output: reflect.TypeOf(&map[string]string{})},
	}
}

func (s *internalExecutionTestService) Method(name string) (svc.Executable, error) {
	return func(ctx context.Context, input, output interface{}) error {
		out, ok := output.(*map[string]string)
		if !ok {
			return svc.NewInvalidOutputError(output)
		}
		*out = map[string]string{"method": name}
		return nil
	}, nil
}

func TestRegistry_Execute_BlocksInternalMethodsOutsidePlanMode(t *testing.T) {
	mgr, err := manager.New(nil)
	require.NoError(t, err)

	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	require.NoError(t, reg.AddInternalService(&internalExecutionTestService{}))

	out, err := reg.Execute(context.Background(), "test/service:list", map[string]interface{}{})
	require.NoError(t, err)
	require.JSONEq(t, `{"method":"list"}`, out)

	_, err = reg.Execute(context.Background(), "test/service:topology", map[string]interface{}{})
	require.EqualError(t, err, "tool not found: test/service:topology")
}

func TestRegistry_Execute_AllowsInternalMethodsInPlanMode(t *testing.T) {
	mgr, err := manager.New(nil)
	require.NoError(t, err)

	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	require.NoError(t, reg.AddInternalService(&internalExecutionTestService{}))

	out, err := reg.Execute(runtimerequestctx.WithRequestMode(context.Background(), "plan"), "test/service:topology", map[string]interface{}{})
	require.NoError(t, err)
	require.JSONEq(t, `{"method":"topology"}`, out)
}
