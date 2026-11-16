package localclient

import (
	"context"
	"encoding/json"
	"reflect"
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

type originalDefinitionService struct {
	testService
	received json.RawMessage
}
type originalDefinitionInput struct {
	Definition json.RawMessage `json:"definition"`
}

func (s *originalDefinitionService) Methods() svc.Signatures {
	return svc.Signatures{{Name: "read", Input: reflect.TypeOf(&originalDefinitionInput{}), Output: reflect.TypeOf(&map[string]string{})}}
}
func (s *originalDefinitionService) Method(string) (svc.Executable, error) {
	return func(_ context.Context, input, output any) error {
		s.received = input.(*originalDefinitionInput).Definition
		return nil
	}, nil
}
func TestServiceClientPreservesOpaqueDefinitionReceivedByMCP(t *testing.T) {
	service := &originalDefinitionService{}
	client, err := NewServiceClient(t.Context(), service)
	require.NoError(t, err)
	_, err = client.Initialize(t.Context())
	require.NoError(t, err)
	raw := json.RawMessage(`{"z":9007199254740993,"a":{"y":true,"b":false}}`)
	_, err = client.CallTool(t.Context(), &schema.CallToolRequestParams{Name: "read", Arguments: map[string]any{"definition": raw}})
	require.NoError(t, err)
	require.Equal(t, string(raw), string(service.received))
}
