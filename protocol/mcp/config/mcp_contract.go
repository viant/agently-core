package config

import shared "github.com/viant/mcp/client/config"

// MCP owns transport and discovery configuration; these aliases preserve
// existing Core imports while all hosts share one implementation.
type (
	Group[T any]            = shared.Group[T]
	ResolvedServiceIdentity = shared.ResolvedServiceIdentity
	MCPClient               = shared.MCPClient
	BrowserTransport        = shared.BrowserTransport
	BrowserDescriptor       = shared.BrowserDescriptor
	OutputArtifact          = shared.OutputArtifact
	ResourceRoot            = shared.ResourceRoot
	ResourceMetadata        = shared.ResourceMetadata
	Upstream                = shared.Upstream
	SkillDiscovery          = shared.SkillDiscovery
	SkillToolBridge         = shared.SkillToolBridge
)

const (
	ToolsListVisibilityPublic  = shared.ToolsListVisibilityPublic
	ToolsListVisibilityPrivate = shared.ToolsListVisibilityPrivate
	ResourceCachePublic        = shared.ResourceCachePublic
	ResourceCacheUser          = shared.ResourceCacheUser
	ResourceCacheConversation  = shared.ResourceCacheConversation
)

func OutputArtifactPointer(path string) ([]string, error)       { return shared.OutputArtifactPointer(path) }
func ResourceRoots(meta map[string]interface{}) []ResourceRoot  { return shared.ResourceRoots(meta) }
func Upstreams(meta map[string]interface{}) map[string]Upstream { return shared.Upstreams(meta) }
func ResolveUpstream(meta map[string]interface{}, root ResourceRoot) (*Upstream, bool) {
	return shared.ResolveUpstream(meta, root)
}
func ValidateResourceRoots(meta map[string]interface{}) []string {
	return shared.ValidateResourceRoots(meta)
}
