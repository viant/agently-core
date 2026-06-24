package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHTTPClient_ListAsyncOperations_QueryParams(t *testing.T) {
	client := newHandlerBackedHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/conversations/conv-1/async", r.URL.Path)
		require.Equal(t, "system/exec:execute", r.URL.Query().Get("tool"))
		require.Equal(t, "detach", r.URL.Query().Get("mode"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ops": []map[string]any{
				{
					"operationId":   "op-1",
					"tool":          "system/exec:execute",
					"executionMode": "detach",
					"state":         "running",
				},
			},
		})
	}))

	out, err := client.ListAsyncOperations(context.Background(), &ListAsyncOperationsInput{
		ConversationID: "conv-1",
		Tool:           "system/exec:execute",
		Mode:           "detach",
	})
	require.NoError(t, err)
	require.Len(t, out.Ops, 1)
	require.Equal(t, "op-1", out.Ops[0].OperationID)
	require.Equal(t, "system/exec:execute", out.Ops[0].Tool)
	require.Equal(t, "detach", out.Ops[0].ExecutionMode)
}

type spyAsyncClient struct {
	*HTTPClient
	input *ListAsyncOperationsInput
	out   *ListAsyncOperationsOutput
	err   error
}

func (s *spyAsyncClient) ListAsyncOperations(_ context.Context, input *ListAsyncOperationsInput) (*ListAsyncOperationsOutput, error) {
	s.input = input
	if s.out != nil {
		return s.out, s.err
	}
	return &ListAsyncOperationsOutput{}, s.err
}

func TestHandleListAsyncOperations_RoutesConversationAndFilters(t *testing.T) {
	base, err := NewHTTP("http://example.com")
	require.NoError(t, err)
	spy := &spyAsyncClient{
		HTTPClient: base,
		out: &ListAsyncOperationsOutput{
			Ops: nil,
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/conversations/conv-1/async?tool=system/exec:execute&mode=detach", nil)
	req.SetPathValue("id", "conv-1")
	rec := httptest.NewRecorder()

	handleListAsyncOperations(spy).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, spy.input)
	require.Equal(t, "conv-1", spy.input.ConversationID)
	require.Equal(t, "system/exec:execute", spy.input.Tool)
	require.Equal(t, "detach", spy.input.Mode)
}
