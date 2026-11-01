package localclient

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	svc "github.com/viant/agently-core/protocol/tool/service"
	"github.com/viant/mcp-protocol/schema"
)

type typedServiceFailure struct{}

func (*typedServiceFailure) Error() string       { return "End date cannot be in the future." }
func (*typedServiceFailure) HTTPStatusCode() int { return 400 }

type failingService struct {
	testService
	failure error
}

func (s *failingService) Method(string) (svc.Executable, error) {
	return func(context.Context, any, any) error { return s.failure }, nil
}

func TestServiceClientPreservesTypedInputFailure(t *testing.T) {
	original := &typedServiceFailure{}
	client, err := NewServiceClient(t.Context(), &failingService{failure: original})
	require.NoError(t, err)
	_, err = client.Initialize(t.Context())
	require.NoError(t, err)
	result, err := client.CallTool(t.Context(), &schema.CallToolRequestParams{Name: "list", Arguments: map[string]any{}})
	require.Nil(t, result)
	require.Same(t, original, err, "in-process tool wrapping must retain the producer error and its diagnostic interfaces")
	require.Equal(t, 400, err.(interface{ HTTPStatusCode() int }).HTTPStatusCode())
}
