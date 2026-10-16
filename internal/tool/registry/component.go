package tool

import (
	"context"
	"encoding/json"
	"fmt"
	windowprotocol "github.com/viant/agently-core/protocol/window"
	"github.com/viant/authz/gating"
)

// Deprecated: ordinary MCP calls are admitted by their schema and existing
// authorization. Producer classification cannot impose a client wire pin.
func (r *Registry) SetComponentProducerClassifier(func(string) bool) {}

// Deprecated: component authority belongs to provider-owned resource execution.
// Registry authorization remains configured through its normal action guard.
func (r *Registry) SetComponentAuthorityResolver(func(context.Context) (gating.Principal, error)) {}

// ObserveNativeComponent no longer infers a resource revision from MCP tool
// annotations. Callers must resolve the provider-owned resource/datasource.
func (r *Registry) ObserveNativeComponent(context.Context, string, string) (windowprotocol.ComponentBinding, error) {
	return windowprotocol.ComponentBinding{}, fmt.Errorf("client-bound MCP component transport is unsupported; use provider-owned resource/datasource execution")
}

// ExecuteNativeComponent rejects legacy pinned intent without sending a tool
// call or silently executing an unpinned current revision.
func (r *Registry) ExecuteNativeComponent(context.Context, string, string, windowprotocol.ComponentBinding, map[string]interface{}) (json.RawMessage, error) {
	return nil, fmt.Errorf("client-bound MCP component transport is unsupported; use provider-owned resource/datasource execution")
}
