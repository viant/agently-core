package tool

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/runtime/mcpapps"
	mcpschema "github.com/viant/mcp-protocol/schema"
	"testing"
)

func TestMCPVisibilityNarrowsNativeAndAppDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, visibility string
		host             bool
		allowed          bool
	}{
		{"app excluded from model", `["app"]`, false, false},
		{"app allowed in host", `["app"]`, true, true},
		{"model excluded from host", `["model"]`, true, false},
		{"model allowed", `["model"]`, false, true},
		{"neither", `[]`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &hostResultClient{}
			registry, _ := newAdvertisedAppCaptureRegistry(client)
			var definition mcpschema.Tool
			require.NoError(t, json.Unmarshal([]byte(`{"name":"tool","inputSchema":{"type":"object"},"_meta":{"ui":{"visibility":`+tc.visibility+`}}}`), &definition))
			registry.replaceServerTools("service", []mcpschema.Tool{definition})
			ctx := protectedTurnContext("turn")
			if tc.host {
				ctx, _ = mcpapps.WithCapture(ctx, "service", "tool", "host-op")
			}
			_, err := registry.Execute(ctx, "service/tool", nil)
			if tc.allowed {
				require.NoError(t, err)
				require.Equal(t, 1, client.calls)
			} else {
				require.Error(t, err)
				require.Zero(t, client.calls)
			}
		})
	}
}
