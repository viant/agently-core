package tool

import (
	"context"

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

func (r *Registry) projectOutputArtifactDefinition(ctx context.Context, definition *llm.ToolDefinition) *llm.ToolDefinition {
	if definition == nil || r.mgr == nil {
		return definition
	}
	server, method, found, err := r.discoveredMCPIdentity(ctx, definition.Name)
	if err != nil || !found || r.isInternalServer(server) {
		return definition
	}
	options, err := r.mgr.Options(ctx, server)
	if err != nil || options == nil {
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
	for i := range definitions {
		definitions[i] = *r.projectOutputArtifactDefinition(ctx, &definitions[i])
	}
	return definitions
}

func (r *Registry) MatchDefinitionWithContextResult(ctx context.Context, pattern string) ([]*llm.ToolDefinition, error) {
	definitions, err := r.rawMatchDefinitionWithContextResult(ctx, pattern)
	for i := range definitions {
		definitions[i] = r.projectOutputArtifactDefinition(ctx, definitions[i])
	}
	return definitions, err
}
