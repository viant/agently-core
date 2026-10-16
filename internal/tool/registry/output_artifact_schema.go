package tool

import (
	"context"
	"strings"

	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcpname"

	"github.com/viant/agently-core/genai/llm"
)

func artifactOutputSchema() map[string]interface{} {
	properties := map[string]interface{}{}
	for _, name := range []string{"id", "uri", "name", "mimeType", "sha256", "downloadURI"} {
		properties[name] = map[string]interface{}{"type": "string"}
	}
	properties["sizeBytes"] = map[string]interface{}{"type": "integer", "minimum": 0}
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{"resources": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object", "properties": properties, "required": []string{"id", "uri", "name", "mimeType", "sizeBytes", "sha256", "downloadURI"}}}}, "required": []string{"resources"}}
}

// outputArtifactProjection shares only configuration preparation within one
// listing. It never retains actor-scoped options beyond the current operation.
type outputArtifactProjection struct {
	registry      *Registry
	options       map[string]*mcpcfg.MCPClient
	loaded        map[string]bool
	servers       []string
	serversLoaded bool
}

func (r *Registry) newOutputArtifactProjection() *outputArtifactProjection {
	return &outputArtifactProjection{registry: r, options: map[string]*mcpcfg.MCPClient{}, loaded: map[string]bool{}}
}

func (p *outputArtifactProjection) serverOptions(ctx context.Context, server string) *mcpcfg.MCPClient {
	if !p.loaded[server] {
		options, err := p.registry.mgr.Options(ctx, server)
		if err == nil {
			p.options[server] = options
		}
		p.loaded[server] = true
	}
	return p.options[server]
}

func (r *Registry) projectOutputArtifactDefinition(ctx context.Context, definition *llm.ToolDefinition) *llm.ToolDefinition {
	return r.newOutputArtifactProjection().project(ctx, definition)
}

func (p *outputArtifactProjection) project(ctx context.Context, definition *llm.ToolDefinition) *llm.ToolDefinition {
	r := p.registry
	if definition == nil || r.mgr == nil {
		return definition
	}
	if candidate, _ := splitToolName(definition.Name); r.isInternalServer(candidate) {
		return definition
	}
	// Most tools have no output-artifact policy. Check the configured candidate
	// before resolving its advertised identity; that resolution may refresh a
	// private tools/list catalog and must not run for every ordinary definition.
	if strings.Count(definition.Name, "/") <= 1 || strings.Contains(definition.Name, ":") {
		if candidate, method := splitToolName(definition.Name); candidate != "" && method != "" {
			if options := p.serverOptions(ctx, candidate); options != nil {
				if _, configured := options.OutputArtifacts[method]; !configured {
					return definition
				}
			}
		}
	}
	// Multi-segment names cannot be split safely. Inspect configured artifact
	// policies first; ordinary definitions need no remote identity discovery.
	if strings.Count(definition.Name, "/") > 1 && !strings.Contains(definition.Name, ":") {
		if !p.serversLoaded {
			var err error
			p.servers, err = r.listServers(ctx)
			if err != nil {
				return definition
			}
			p.serversLoaded = true
		}
		servers := p.servers
		candidate := false
		for _, server := range servers {
			options := p.serverOptions(ctx, server)
			if options == nil {
				continue
			}
			for method := range options.OutputArtifacts {
				full, colon := server+"/"+method, server+":"+method
				if definition.Name == full || definition.Name == colon || definition.Name == mcpname.Canonical(full) || definition.Name == mcpname.Canonical(colon) {
					candidate = true
					break
				}
			}
			if candidate {
				break
			}
		}
		if !candidate {
			return definition
		}
	}
	server, method, found, err := r.discoveredMCPIdentity(ctx, definition.Name)
	if err != nil || !found || r.isInternalServer(server) {
		return definition
	}
	options := p.serverOptions(ctx, server)
	if options == nil {
		return definition
	}
	policy, configured := options.OutputArtifacts[method]
	if !configured || policy.Validate() != nil {
		return definition
	}
	projected := *definition
	projected.OutputSchema = artifactOutputSchema()
	projected.Description += " Output is captured as an owned immutable artifact; results contain resource IDs, URIs, and download metadata. Use the artifact URI with resource inspection and extraction tools."
	return &projected
}

func (r *Registry) GetDefinitionWithContext(ctx context.Context, name string) (*llm.ToolDefinition, bool) {
	definition, found := r.GetRawDefinitionWithContext(ctx, name)
	return r.projectOutputArtifactDefinition(ctx, definition), found
}

func (r *Registry) DefinitionsWithContext(ctx context.Context) []llm.ToolDefinition {
	definitions := r.rawDefinitionsWithContext(ctx)
	projection := r.newOutputArtifactProjection()
	for i := range definitions {
		definitions[i] = *projection.project(ctx, &definitions[i])
	}
	return definitions
}

func (r *Registry) MatchDefinitionWithContextResult(ctx context.Context, pattern string) ([]*llm.ToolDefinition, error) {
	definitions, err := r.rawMatchDefinitionWithContextResult(ctx, pattern)
	projection := r.newOutputArtifactProjection()
	for i := range definitions {
		definitions[i] = projection.project(ctx, definitions[i])
	}
	return definitions, err
}
