package expose

import (
	"context"

	"github.com/viant/agently-core/genai/llm"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

// LLMCore exposes tool definitions required by MCP tool listing.
type LLMCore interface {
	ToolDefinitions() []llm.ToolDefinition
}

// Executor is the minimal runtime contract needed by MCP expose handlers.
type Executor interface {
	LLMCore() LLMCore
	ExecuteTool(ctx context.Context, name string, args map[string]interface{}, timeoutSec int) (interface{}, error)
}

// ResourceProvider is an optional executor-side extension for exposing MCP
// resources in addition to tools.
type ResourceProvider interface {
	ListResources(ctx context.Context) ([]mcpschema.Resource, error)
	ReadResource(ctx context.Context, uri string) (*mcpschema.ReadResourceResult, error)
}

// ServerConfig defines MCP server exposure options.
type ServerConfig struct {
	Port      int      `yaml:"port"`
	ToolItems []string `yaml:"toolItems"`
}

func (c *ServerConfig) ToolPatterns() []string {
	if c == nil || len(c.ToolItems) == 0 {
		return nil
	}
	out := make([]string, 0, len(c.ToolItems))
	for _, item := range c.ToolItems {
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}
