package datasource

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/forge/backend/types"
)

func TestAllMCPDatasourcePathsApplySharedArgumentProjectionOnce(t *testing.T) {
	for _, mode := range []string{"read", "published", "operation"} {
		t.Run(mode, func(t *testing.T) {
			executor := &publishedExecutor{}
			backend := &dsproto.Backend{Kind: dsproto.BackendMCPTool, Service: "example", Method: "search", Args: map[string]string{"term": "inputs.Query", "groupId": "inputs.Group", "optional": "inputs.Absent", "enabled": "inputs.Enabled"}, Pinned: map[string]interface{}{"action": "search"}}
			source := &dsproto.DataSource{ID: "example", DataSource: types.DataSource{Selectors: &types.Selectors{Data: "items"}}, Backend: backend, Operations: map[string]*dsproto.Operation{"search": {Backend: backend}}}
			store := NewMemoryStore()
			store.Put(source)
			service := New(Options{Store: store, Executor: executor})
			inputs := map[string]interface{}{"Query": "alpha", "Group": 7, "Enabled": false, "action": "replace", "ignored": "unused"}
			var err error
			switch mode {
			case "read":
				_, err = service.Fetch(context.Background(), source.ID, inputs, FetchOptions{})
			case "published":
				_, err = service.FetchPublished(context.Background(), source, inputs, nil)
			case "operation":
				_, err = service.Operate(context.Background(), source.ID, "search", inputs, FetchOptions{})
			}
			require.NoError(t, err)
			require.Equal(t, 1, executor.calls)
			require.Equal(t, map[string]interface{}{"term": "alpha", "groupId": 7, "enabled": false, "action": "search"}, executor.args)
			require.Equal(t, "alpha", inputs["Query"])
		})
	}
}
