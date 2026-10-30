package executor

import (
	"fmt"
	resources "github.com/viant/forge/backend/resourcehost"
)

// InstallPublishedMetadata is called after successful cohort preload and before
// serving requests. Both resource consumers and dynamic MCP configuration use
// the same authoritative local source. Actual data transports remain managed
// by MCPManager and are not replaced with a metadata proxy.
func (r *Runtime) InstallPublishedMetadata(connection resources.Connection, namespaces []string, source resources.PublishedMetadataSource, aliases ...string) error {
	if r == nil || r.PrimitiveProviders == nil || r.mcpDefinitions == nil || r.mcpDefinitions.gateway == nil {
		return fmt.Errorf("published metadata runtime is not initialized")
	}
	if err := r.PrimitiveProviders.RegisterPublishedMetadataSource(connection, namespaces, source, aliases...); err != nil {
		return err
	}
	if err := r.mcpDefinitions.gateway.RegisterPublishedMetadataSource(connection, namespaces, source, aliases...); err != nil {
		return err
	}
	if registry, ok := r.Registry.(interface{ RegisterMetadataConnections(...string) }); ok {
		registry.RegisterMetadataConnections(append([]string{connection.Name}, aliases...)...)
	}
	return nil
}
