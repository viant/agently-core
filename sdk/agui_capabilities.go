package sdk

import (
	"context"
	"github.com/viant/agently-core/protocol/agui"
)

// Discovery reflects configured backend bindings. Rendering and unfinished
// lifecycle features are intentionally omitted from the advertised contract.
func aguiDurableCapabilities(ctx context.Context, client Client) agui.Event {
	operations := []string{"chat", "capabilities", "state.get", "state.patch", "run.get", "run.events.list", "run.attach"}
	if aguiConversationBootstrapAvailable(client) {
		operations = append(operations, "conversation.bootstrap")
	}
	if backend, ok := client.(*backendClient); ok {
		operations = append(operations, "run.cancel")
		if backend.goalInvoker != nil && backend.conv != nil && backend.data != nil {
			operations = append(operations, "approval.decide")
		}
		if backend.store != nil {
			operations = append(operations, "workspace.resource.list", "workspace.resource.get", "workspace.resource.save", "workspace.resource.delete", "workspace.resource.export", "workspace.resource.import")
		}
		if backend.goalRepo != nil && backend.goalInvoker != nil && ensureGoalsFeatureEnabled() == nil {
			operations = append(operations, "goal.get", "goal.create", "goal.update", "goal.clear", "goal.pause", "goal.resume")
			if backend.streaming != nil {
				operations = append(operations, "goal.subscribe")
			}
		}
		if backend.datasourceSvc != nil {
			operations = append(operations, "datasource.fetch", "datasource.cache.invalidate")
			if backend.overlaySvc != nil {
				operations = append(operations, "lookup.registry")
			}
		}
		if backend.feeds != nil {
			operations = append(operations, "feed.list", "feed.get")
			if backend.streaming != nil {
				operations = append(operations, "feed.subscribe")
			}
		}
	}
	bindings, _ := ctx.Value(aguiWorkspaceBindingsKey{}).(AGUIWorkspaceBindings)
	host := bindings.MCPApps
	if host == nil {
		if provider, ok := client.(interface{ aguiMCPAppsHost() *AGUIMCPAppsHost }); ok {
			host = provider.aguiMCPAppsHost()
		}
	}
	methods := host.methods()
	if methods == nil {
		methods = []string{}
	}
	tools := false
	for _, method := range methods {
		if method == "tools/call" {
			tools = true
		}
	}
	enabled := len(methods) > 0
	mcpApps := map[string]any{"version": "1", "enabled": enabled, "methods": methods, "scopedBindings": enabled, "approvalResume": tools, "hostResults": enabled, "uncertainOutcomes": tools}
	if bindings.Metadata != nil {
		operations = append(operations, "workspace.metadata.get", "workspace.publicagents.list", "workspace.layout.get", "workspace.tools.list", "workspace.models.list", "workspace.model.get", "workspace.model.save")
	}
	return agui.Event{Type: "CUSTOM", Name: "agently.capabilities", Value: agui.CapabilitiesValue{Version: agui.ExtensionVersion, Capabilities: agui.AgentCapabilities{
		"transport": map[string]bool{"streaming": true, "resumable": true, "websocket": false, "httpBinary": false},
		"state":     map[string]bool{"snapshots": true, "deltas": true, "persistentState": true},
		"custom":    map[string]any{"agently": map[string]any{"version": agui.ExtensionVersion, "operations": operations, "execution": aguiExecutionCapabilities(), "presentation": map[string]any{"version": "1", "nativeIdentity": true}, "history": "server", "disconnect": "detach", "replay": true, "toolIdentity": "turn-scoped-v1", "mcpApps": mcpApps}}}}}
}
