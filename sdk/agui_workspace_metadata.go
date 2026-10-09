package sdk

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/viant/agently-core/service/browsermcp"
	workspace "github.com/viant/agently-core/service/workspace"
)

type AGUIWorkspaceBindings struct {
	BrowserMCP *browsermcp.Registry
	Metadata   *workspace.MetadataHandler
	MCPApps    *AGUIMCPAppsHost
}
type aguiWorkspaceBindingsKey struct{}

// WithAGUIWorkspaceBindings injects configured typed services, never values from
// forwarded properties. HTTP and AG-UI share the same policy implementation.
func WithAGUIWorkspaceBindings(ctx context.Context, bindings AGUIWorkspaceBindings) context.Context {
	return context.WithValue(ctx, aguiWorkspaceBindingsKey{}, bindings)
}
func dispatchAGUIWorkspaceMetadata(ctx context.Context, operation string, payload json.RawMessage) (any, error) {
	bindings, _ := ctx.Value(aguiWorkspaceBindingsKey{}).(AGUIWorkspaceBindings)
	if bindings.Metadata == nil {
		return nil, fmt.Errorf("workspace metadata capability is not configured")
	}
	switch operation {
	case "workspace.metadata.get":
		return bindings.Metadata.Metadata(ctx)
	case "workspace.publicagents.list":
		return bindings.Metadata.PublicAgents(ctx)
	case "workspace.layout.get":
		return bindings.Metadata.Layout(ctx, &workspace.LayoutRequest{})
	case "workspace.tools.list":
		var input struct {
			Pattern string `json:"pattern"`
		}
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &input); err != nil {
				return nil, err
			}
		}
		return bindings.Metadata.Tools(ctx, input.Pattern)
	case "workspace.models.list":
		return bindings.Metadata.Models(ctx)
	case "workspace.model.get":
		var input struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(payload, &input); err != nil {
			return nil, err
		}
		return bindings.Metadata.Model(ctx, input.ID)
	case "workspace.model.save":
		var input struct {
			ID     string         `json:"id"`
			Update map[string]any `json:"update"`
		}
		if err := json.Unmarshal(payload, &input); err != nil {
			return nil, err
		}
		return bindings.Metadata.SaveModel(ctx, input.ID, input.Update)
	}
	return nil, fmt.Errorf("unsupported workspace metadata operation")
}
