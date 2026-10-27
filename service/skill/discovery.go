package skill

import (
	"context"
	"fmt"

	"github.com/viant/agently-core/internal/debugtrace"
)

// Discovery diagnostics belong to the current caller and request, never to the
// shared local registry. All list adapters use the existing diagnostics output.
type discoveryDiagnosticsKey struct{}

type discoveryDiagnostics struct{ messages []string }

func withDiscoveryDiagnostics(ctx context.Context) (context.Context, *discoveryDiagnostics) {
	if diagnostics, ok := ctx.Value(discoveryDiagnosticsKey{}).(*discoveryDiagnostics); ok {
		return ctx, diagnostics
	}
	diagnostics := &discoveryDiagnostics{}
	return context.WithValue(ctx, discoveryDiagnosticsKey{}, diagnostics), diagnostics
}

func recordDiscoveryFailure(ctx context.Context, catalog string) {
	// Do not expose upstream errors, which may contain credentials or payloads.
	message := fmt.Sprintf("MCP skill discovery unavailable for %s; catalog results are partial; retry discovery to refresh available providers", catalog)
	if diagnostics, ok := ctx.Value(discoveryDiagnosticsKey{}).(*discoveryDiagnostics); ok {
		diagnostics.messages = append(diagnostics.messages, message)
	}
	debugtrace.Write("skill", "discovery_unavailable", map[string]any{"catalog": catalog, "partial": true})
}

// Failure traces classify the boundary without logging credentials, provider
// payloads, manifests or other upstream error text.
func traceDiscoveryFailure(ctx context.Context, stage, server string, automatic bool, only string, err error) {
	debugtrace.Write("skill", "remote_discovery_failure", map[string]any{"stage": stage, "server": server, "automatic": automatic, "selected": only != "", "errorType": fmt.Sprintf("%T", err), "callerCanceled": ctx.Err() != nil})
}
