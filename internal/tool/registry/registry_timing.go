package tool

import (
	"context"

	"github.com/viant/agently-core/protocol/primitive/observe"
	schema "github.com/viant/mcp-protocol/schema"
)

// Registry timings deliberately contain no tool, provider, argument or result
// labels. The call duration is client elapsed time (including transport and
// upstream execution), not a measurement of upstream SQL. Decode measures the
// registry's result composition/selection, not datasource JSON parsing.
func startRegistryTiming(ctx context.Context, phase observe.Phase) observe.Span {
	return observe.Start(ctx, phase, observe.Labels{})
}
func finishRegistryTiming(span observe.Span, err error) {
	span.End(observe.OutcomeFor(err), observe.CacheUnknown)
}

// Each physical attempt is observed separately; callers retain retry policy,
// context, options and error handling. A tool-level failure is not transport
// success for diagnostic purposes, but its result is returned unchanged.
func timedMCPToolCall(ctx context.Context, call func() (*schema.CallToolResult, error)) (*schema.CallToolResult, error) {
	span := startRegistryTiming(ctx, "mcp.tool.call")
	result, err := call()
	outcome := observe.OutcomeFor(err)
	if err == nil && (result == nil || result.IsError != nil && *result.IsError) {
		outcome = observe.Error
	}
	span.End(outcome, observe.CacheUnknown)
	return result, err
}
