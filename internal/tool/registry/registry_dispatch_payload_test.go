package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/tool/dispatchpayload"
	"github.com/viant/agently-core/internal/tool/executionprotection"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

func TestDispatchPayloadExpandsOnlyExecutionCopyBeforeTypedBindingAndSanitizesEcho(t *testing.T) {
	macro := "${artifact[2f1c171e-0fe9-4aa7-8f17-5eb826f72042].payload}"
	original := map[string]interface{}{"body": macro}
	calls := 0
	registry := &Registry{virtualDefs: map[string]llm.ToolDefinition{}, virtualExec: map[string]Handler{"owned/upload": func(ctx context.Context, args map[string]interface{}) (string, error) {
		calls++
		raw, err := json.Marshal(args)
		require.NoError(t, err)
		var typed struct {
			Body []byte `json:"body"`
		}
		require.NoError(t, json.Unmarshal(raw, &typed))
		require.Equal(t, []byte{0, 255, 1}, typed.Body)
		return `{"count":3,"echo":"` + args["body"].(string) + `"}`, nil
	}}}
	_, err := registry.Execute(context.Background(), "owned/upload", original)
	require.ErrorContains(t, err, "resolver unavailable")
	require.Zero(t, calls)
	encoded := base64.StdEncoding.EncodeToString([]byte{0, 255, 1})
	ctx := dispatchpayload.WithDispatchPayloadResolver(context.Background(), func(context.Context, string, map[string]interface{}) (map[string]interface{}, func(string) string, error) {
		return map[string]interface{}{"body": encoded}, func(s string) string { return strings.ReplaceAll(s, encoded, "[artifact payload redacted]") }, nil
	})
	result, err := registry.Execute(ctx, "owned/upload", original)
	require.NoError(t, err)
	require.NotContains(t, result, encoded)
	require.Contains(t, result, `"count":3`)
	require.Equal(t, macro, original["body"])
	require.Equal(t, 1, calls)
	registry.virtualExec["owned/upload"] = func(context.Context, map[string]interface{}) (string, error) {
		return "", errors.New("echo " + encoded)
	}
	_, err = registry.Execute(ctx, "owned/upload", original)
	require.Error(t, err)
	require.NotContains(t, err.Error(), encoded)
}

type artifactRetryClient struct {
	scriptedCallClient
	calls int
}

func (c *artifactRetryClient) CallTool(ctx context.Context, params *mcpschema.CallToolRequestParams, options ...mcpclient.RequestOption) (*mcpschema.CallToolResult, error) {
	c.calls++
	return c.scriptedCallClient.CallTool(ctx, params, options...)
}

type artifactRetryManager struct {
	scriptedReconnectManager
	sensitive mcpclient.Interface
}

func (m *artifactRetryManager) NewSensitiveClient(context.Context, string, string) (mcpclient.Interface, error) {
	return m.sensitive, nil
}

func TestArtifactDispatchNeverReplaysAfterReconnectableFailure(t *testing.T) {
	for _, toolError := range []bool{false, true} {
		t.Run(map[bool]string{false: "transport", true: "tool"}[toolError], func(t *testing.T) {
			client := &artifactRetryClient{}
			if toolError {
				client.result = errorToolResult("connection reset by peer")
			} else {
				client.err = errors.New("connection reset by peer")
			}
			mgr := &artifactRetryManager{scriptedReconnectManager: scriptedReconnectManager{clients: []mcpclient.Interface{&scriptedCallClient{}}}, sensitive: client}
			reg := newReconnectTestRegistry(&mgr.scriptedReconnectManager)
			reg.mgr = mgr
			ctx, closeState := dispatchpayload.WithState(context.Background())
			defer closeState()
			ctx = dispatchpayload.WithDispatchPayloadResolver(ctx, func(context.Context, string, map[string]interface{}) (map[string]interface{}, func(string) string, error) {
				return map[string]interface{}{"body": "dispatch-only"}, func(s string) string { return s }, nil
			})
			args := map[string]interface{}{"body": "${artifact[2f1c171e-0fe9-4aa7-8f17-5eb826f72042].payload}"}
			_, err := reg.Execute(ctx, "service/upload", args)
			require.Error(t, err)
			require.Equal(t, 1, client.calls, "an ambiguous mutation must not be replayed")
			require.Empty(t, mgr.reconnectCalls, "a pooled reconnect cannot restore the isolated session")
			require.Equal(t, "${artifact[2f1c171e-0fe9-4aa7-8f17-5eb826f72042].payload}", args["body"])
		})
	}
}

func TestArtifactMacroProtectionUsesOriginalReferenceAndExpandsAfterClaim(t *testing.T) {
	resolutions, calls := 0, 0
	registry, _ := newProtectedVirtualRegistry(t, func(context.Context, map[string]interface{}) (string, error) { calls++; return "accepted", nil })
	ctx := dispatchpayload.WithDispatchPayloadResolver(protectedTurnContext("artifact-turn"), func(context.Context, string, map[string]interface{}) (map[string]interface{}, func(string) string, error) {
		resolutions++
		return map[string]interface{}{"body": "dispatch-only"}, func(s string) string { return s }, nil
	})
	args := map[string]interface{}{"body": "${artifact[2f1c171e-0fe9-4aa7-8f17-5eb826f72042].payload}"}
	_, err := registry.Execute(ctx, "service/tool", args)
	require.NoError(t, err)
	result, err := registry.Execute(ctx, "service/tool", args)
	require.NoError(t, err)
	require.Contains(t, result, "duplicate_suppressed")
	require.Equal(t, 1, calls)
	require.Equal(t, 1, resolutions, "suppressed original macro never reads artifact or dispatches")
	require.Equal(t, "${artifact[2f1c171e-0fe9-4aa7-8f17-5eb826f72042].payload}", args["body"])
}

func TestArtifactMacroDeniedProtectionNeverReadsOrDispatches(t *testing.T) {
	resolutions, calls := 0, 0
	registry := &Registry{virtualExec: map[string]Handler{"service/tool": func(context.Context, map[string]interface{}) (string, error) { calls++; return "", nil }}, virtualTimeout: map[string]timeoutSupport{}, cache: map[string]*toolCacheEntry{}, executionProtection: newProtectionGuard(t, executionprotection.NewComponentRepository(nil))}
	ctx := dispatchpayload.WithDispatchPayloadResolver(protectedTurnContext("denied-artifact"), func(context.Context, string, map[string]interface{}) (map[string]interface{}, func(string) string, error) {
		resolutions++
		return nil, nil, errors.New("unexpected resolver")
	})
	_, err := registry.Execute(ctx, "service/tool", map[string]interface{}{"body": "${artifact[2f1c171e-0fe9-4aa7-8f17-5eb826f72042].payload}"})
	require.Error(t, err)
	require.Zero(t, resolutions)
	require.Zero(t, calls)
}
