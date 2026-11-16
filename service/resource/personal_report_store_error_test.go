package resource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	reportfs "github.com/viant/agently-core/app/store/reporting/fs"
	"github.com/viant/agently-core/service/reporting"
	userprovider "github.com/viant/agently-core/service/resource/provider/user"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

type unavailableReportState struct {
	workspace.StateStore
	cause error
}

func (s unavailableReportState) StatePath(context.Context, string) (string, error) {
	return "", fmt.Errorf("private store connection not found: %w", s.cause)
}

func TestPersonalReportStoreFailureRetainsStatusThroughMCP(t *testing.T) {
	ctx := zeroMetadataUserContext("reader")
	for _, tc := range []struct {
		cause  error
		status int
	}{
		{jsonrpc.NewHTTPStatusError(http.StatusServiceUnavailable), http.StatusServiceUnavailable},
		{context.DeadlineExceeded, http.StatusGatewayTimeout},
		{context.Canceled, 499},
		{errors.New("unexpected dependency failure"), http.StatusInternalServerError},
	} {
		store := reporting.NewStoreAdapter(reportfs.New(unavailableReportState{cause: tc.cause}))
		source, err := userprovider.NewReports(store, "user-source")
		require.NoError(t, err)
		actor, verify := PublicReadActors()
		p, err := NewLocalProvider(LocalConfig{ProviderIdentity: "user-source", Actor: actor, Verify: verify, Catalog: source, Authoring: source, Authorize: userprovider.Authorize, Validators: map[string]LocalResourceValidator{"report": ValidateReportEnvelope}})
		require.NoError(t, err)
		client, err := NewLocalMCPClient(p)
		require.NoError(t, err)
		for _, op := range []struct {
			name string
			args map[string]any
		}{
			{"reports/list", map[string]any{"namespace": "users"}},
			{"reports/get", map[string]any{"uri": "report://users/reader/example"}},
		} {
			t.Run(fmt.Sprintf("%s/%d", op.name, tc.status), func(t *testing.T) {
				result, err := client.CallTool(ctx, &schema.CallToolRequestParams{Name: op.name, Arguments: op.args})
				require.NoError(t, err)
				require.NotNil(t, result.IsError)
				require.True(t, *result.IsError)
				status, ok := schema.ToolErrorHTTPStatus(result)
				require.True(t, ok)
				require.Equal(t, tc.status, status)
				wire, err := json.Marshal(result)
				require.NoError(t, err)
				require.NotContains(t, string(wire), "private store connection")
			})
		}
	}
}
