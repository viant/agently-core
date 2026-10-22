package tool

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/primitive/observe"
	schema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type registryTimingContextKey struct{}
type registryTimingClient struct {
	exactDispatchClient
	t         *testing.T
	calls     int
	result    *schema.CallToolResult
	failure   error
	failFirst bool
	deadline  time.Time
	arguments map[string]interface{}
}

func (c *registryTimingClient) CallTool(ctx context.Context, request *schema.CallToolRequestParams, options ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	c.calls++
	require.Equal(c.t, "private-context-value", ctx.Value(registryTimingContextKey{}))
	require.Equal(c.t, c.arguments, request.Arguments)
	require.Equal(c.t, "read", request.Name)
	deadline, ok := ctx.Deadline()
	require.True(c.t, ok)
	require.Equal(c.t, c.deadline, deadline)
	if c.failFirst && c.calls == 1 {
		return nil, errors.New("broken pipe")
	}
	return c.result, c.failure
}
func TestRegistryMCPExecutionTimings(t *testing.T) {
	for _, scenario := range []string{"success", "transport-error", "tool-error", "decode-error", "retry"} {
		t.Run(scenario, func(t *testing.T) {
			var events []observe.Event
			ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), registryTimingContextKey{}, "private-context-value"), time.Minute)
			defer cancel()
			deadline, _ := ctx.Deadline()
			ctx = authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "private-user"})
			ctx = observe.WithObserver(ctx, observe.ObserverFunc(func(e observe.Event) { events = append(events, e) }))
			args := map[string]interface{}{"secret": "private-argument"}
			remote := &registryTimingClient{t: t, deadline: deadline, arguments: args, result: &schema.CallToolResult{StructuredContent: map[string]interface{}{"value": "private-result"}}}
			switch scenario {
			case "transport-error":
				remote.failure = errors.New("private-transport-error")
			case "tool-error":
				failed := true
				remote.result = &schema.CallToolResult{IsError: &failed, StructuredContent: map[string]interface{}{"message": "private-tool-error"}}
			case "decode-error":
				remote.result = &schema.CallToolResult{StructuredContent: map[string]interface{}{"cursor": float64(42)}}
			case "retry":
				remote.failFirst = true
			}
			manager := &discoveryManagerStub{getFunc: func(_, server string) (mcpclient.Interface, error) {
				require.Equal(t, "external", server)
				return remote, nil
			}, reconnectFunc: func(_, server string) (mcpclient.Interface, error) {
				require.Equal(t, "external", server)
				return remote, nil
			}}
			registry := &Registry{mgr: manager, cache: map[string]*toolCacheEntry{}, internal: map[string]mcpclient.Interface{}}
			cursorPath := ""
			if scenario == "decode-error" {
				cursorPath = "cursor"
			}
			output, err := registry.ExecuteExactJSON(ctx, "external:read", args, cursorPath)
			failed := scenario == "transport-error" || scenario == "tool-error" || scenario == "decode-error"
			if failed {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.JSONEq(t, `{"value":"private-result"}`, output)
			}
			require.Equal(t, map[string]interface{}{"secret": "private-argument"}, args)
			calls := 1
			if scenario == "retry" {
				calls = 2
			}
			require.Equal(t, calls, remote.calls)
			phases := map[observe.Phase][]observe.Event{}
			for _, event := range events {
				require.Equal(t, observe.Labels{}, event.Labels)
				phases[event.Phase] = append(phases[event.Phase], event)
				require.GreaterOrEqual(t, event.Duration, time.Duration(0))
			}
			require.Len(t, phases["tool.registry.execute"], 1)
			require.Len(t, phases["mcp.tool.call"], calls)
			totalOutcome := observe.OK
			if failed {
				totalOutcome = observe.Error
			}
			require.Equal(t, totalOutcome, phases["tool.registry.execute"][0].Outcome)
			if scenario == "transport-error" || scenario == "tool-error" || scenario == "retry" {
				require.Equal(t, observe.Error, phases["mcp.tool.call"][0].Outcome)
			} else {
				require.Equal(t, observe.OK, phases["mcp.tool.call"][0].Outcome)
			}
			if scenario == "transport-error" || scenario == "tool-error" {
				require.Empty(t, phases["mcp.tool.decode"])
			} else {
				require.Len(t, phases["mcp.tool.decode"], 1)
				expected := observe.OK
				if scenario == "decode-error" {
					expected = observe.Error
				}
				require.Equal(t, expected, phases["mcp.tool.decode"][0].Outcome)
			}
			if scenario == "retry" {
				require.Equal(t, observe.OK, phases["mcp.tool.call"][1].Outcome)
				require.Len(t, manager.reconnectCallsSnapshot(), 1)
			} else {
				require.Empty(t, manager.reconnectCallsSnapshot())
			}
		})
	}
}

func TestMCPCallTimingPreservesCanceledContextAndResult(t *testing.T) {
	var events []observe.Event
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx = observe.WithObserver(ctx, observe.ObserverFunc(func(e observe.Event) { events = append(events, e) }))
	calls := 0
	result, err := timedMCPToolCall(ctx, func() (*schema.CallToolResult, error) { calls++; return nil, ctx.Err() })
	require.Nil(t, result)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls)
	require.Len(t, events, 1)
	require.Equal(t, observe.Canceled, events[0].Outcome)
}
