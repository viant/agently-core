package agent

import (
	"context"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/mcpname"
	"github.com/viant/agently-core/service/browsermcp"
)

// modelVisibleTools reuses the registry's model-exposure contract. This is
// independent of execution authorization and must precede intake discovery.
func (s *Service) modelVisibleTools(ctx context.Context, tools []llm.Tool) ([]llm.Tool, error) {
	visibility, ok := s.registry.(interface {
		MCPToolVisible(context.Context, string, string) (bool, error)
	})
	if !ok {
		return tools, nil
	}
	browserDefinitions := browsermcp.Definitions(ctx)
	var visible map[string]bool
	if batch, ok := s.registry.(interface {
		MCPToolsVisible(context.Context, []string, string) (map[string]bool, error)
	}); ok {
		names := make([]string, 0, len(tools))
		for _, tool := range tools {
			if _, browser := browserDefinitions[mcpname.Canonical(tool.Definition.Name)]; !browser {
				names = append(names, tool.Definition.Name)
			}
		}
		var err error
		visible, err = batch.MCPToolsVisible(ctx, names, "model")
		if err != nil {
			return nil, err
		}
	}
	filtered := make([]llm.Tool, 0, len(tools))
	for _, tool := range tools {
		// Browser definitions already passed their originating catalog's model
		// visibility check and have no server-side discovery/dispatch path.
		if _, browser := browserDefinitions[mcpname.Canonical(tool.Definition.Name)]; browser {
			filtered = append(filtered, tool)
			continue
		}
		allowed := visible[tool.Definition.Name]
		var err error
		if visible == nil {
			allowed, err = visibility.MCPToolVisible(ctx, tool.Definition.Name, "model")
		}
		if err != nil {
			return nil, err
		}
		if allowed {
			filtered = append(filtered, tool)
		}
	}
	return filtered, nil
}

func (s *Service) modelVisibleDefinitions(ctx context.Context, definitions []llm.ToolDefinition) ([]llm.ToolDefinition, error) {
	tools, err := s.modelVisibleTools(ctx, defsToTools(definitions))
	if err != nil {
		return nil, err
	}
	out := make([]llm.ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		out = append(out, tool.Definition)
	}
	return out, nil
}
