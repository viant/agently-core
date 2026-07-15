package tool

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/mcp/manager"
	svc "github.com/viant/agently-core/protocol/tool/service"
)

type internalPreferredExecutionService struct{}

func (s *internalPreferredExecutionService) Name() string { return "reporting" }

func (s *internalPreferredExecutionService) Methods() svc.Signatures {
	return svc.Signatures{
		{Name: "submit_export", Description: "submit export", Input: reflect.TypeOf(&map[string]interface{}{}), Output: reflect.TypeOf(new(map[string]string))},
	}
}

func (s *internalPreferredExecutionService) Method(name string) (svc.Executable, error) {
	return func(ctx context.Context, input, output interface{}) error {
		out, ok := output.(*map[string]string)
		if !ok {
			return svc.NewInvalidOutputError(output)
		}
		*out = map[string]string{"source": "internal", "method": name}
		return nil
	}, nil
}

func TestRegistryExecute_PrefersInternalServiceOverCachedExecutable(t *testing.T) {
	mgr, err := manager.New(nil)
	require.NoError(t, err)

	reg, err := NewWithManager(mgr)
	require.NoError(t, err)
	require.NoError(t, reg.AddInternalService(&internalPreferredExecutionService{}))

	reg.mu.Lock()
	reg.cache["reporting:submit_export"] = &toolCacheEntry{
		exec: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return `{"source":"cached","method":"submit_export"}`, nil
		},
	}
	reg.mu.Unlock()

	out, err := reg.Execute(context.Background(), "reporting:submit_export", map[string]interface{}{})
	require.NoError(t, err)
	require.JSONEq(t, `{"source":"internal","method":"submit_export"}`, out)
}
