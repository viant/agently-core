package tool

import (
	"context"
	"encoding/json"
	"errors"
	ds "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	"testing"
)

func TestNumericMCPProvenanceSurvivesActualRegistryDispatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload any
		want    int
	}{{"numeric403", map[string]any{"code": 403}, 403}, {"numeric401", map[string]any{"statusCode": 401}, 401}, {"text only", map[string]any{"message": "403 forbidden"}, 0}, {"forbidden string", map[string]any{"status": "forbidden"}, 0}, {"mixed", map[string]any{"status": 401, "code": 403}, 401}, {"exact JSON number", map[string]any{"statusCode": json.Number("403")}, 403}, {"large numeric code", map[string]any{"code": json.Number("4294967699")}, 0}, {"fractional numeric code", map[string]any{"code": json.Number("403.5")}, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			failed := true
			cli := &scriptedCallClient{result: &schema.CallToolResult{IsError: &failed, StructuredContent: tc.payload}}
			mgr := &scriptedReconnectManager{clients: []mcpclient.Interface{cli}}
			registry := newReconnectTestRegistry(mgr)
			_, err := registry.Execute(context.Background(), "fixture:read", nil)
			var numeric *ds.UpstreamAuthorizationError
			if tc.want == 0 {
				if errors.As(err, &numeric) {
					t.Fatal("text created numeric provenance", err)
				}
			} else if !errors.As(err, &numeric) || numeric.Status != tc.want {
				t.Fatal("numeric status lost", err)
			}
		})
	}
}
