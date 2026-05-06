package localclient

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

type testService struct{}

func (t *testService) Name() string { return "test/service" }
func (t *testService) Methods() svc.Signatures {
	return svc.Signatures{
		{Name: "list", Description: "public", Input: reflect.TypeOf(&struct{}{}), Output: reflect.TypeOf(&struct{}{})},
		{Name: "topology", Description: "planner-only", Internal: true, Input: reflect.TypeOf(&struct{}{}), Output: reflect.TypeOf(&struct{}{})},
	}
}
func (t *testService) Method(name string) (svc.Executable, error) {
	return func(ctx context.Context, input, output interface{}) error { return nil }, nil
}

func TestServiceHandler_ListTools_HidesInternalPlannerOnlyMethodsOutsidePlanMode(t *testing.T) {
	h := &serviceHandler{service: &testService{}}
	h.init()

	result := h.listTools(context.Background())
	var names []string
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	require.Contains(t, names, "list")
	require.NotContains(t, names, "topology")

	planCtx := runtimerequestctx.WithRequestMode(context.Background(), "plan")
	result = h.listTools(planCtx)
	names = names[:0]
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	require.Contains(t, names, "list")
	require.Contains(t, names, "topology")
}
