package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/internal/logx"
	runtimectx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/jsonrpc"
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
	writeDiscoveryFailureTrace(ctx, stage, server, automatic, only, err)
	if ctx.Err() == nil {
		code, status := skillDiagnosticRPCFields(err)
		turn, _ := runtimectx.TurnMetaFromContext(ctx)
		logx.OperationalWarnf("resource", "skill.discovery.failed stage=%q server=%q convo=%q turn=%q errorType=%T rpcCode=%d rpcStatus=%d", stage, logx.SafeResourceIdentifier(server), runtimectx.ConversationIDFromContext(ctx), turn.TurnID, err, code, status)
	}
}

func traceSkippedDiscoveryFailure(ctx context.Context, stage, server string, automatic bool, only string, err error) {
	writeDiscoveryFailureTrace(ctx, stage, server, automatic, only, err)
}

func writeDiscoveryFailureTrace(ctx context.Context, stage, server string, automatic bool, only string, err error) {
	debugtrace.Write("skill", "remote_discovery_failure", map[string]any{"stage": stage, "server": server, "automatic": automatic, "selected": only != "", "errorType": fmt.Sprintf("%T", err), "callerCanceled": ctx.Err() != nil})
}

func skillDiagnosticRPCFields(err error) (code, status int) {
	var rpc *jsonrpc.Error
	if !errors.As(err, &rpc) {
		return 0, 0
	}
	code = rpc.Code
	var data struct {
		Status int `json:"status"`
	}
	if json.Unmarshal(rpc.Data, &data) == nil {
		status = data.Status
	}
	return code, status
}
