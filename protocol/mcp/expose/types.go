package expose

import (
	"context"
	"fmt"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

// LLMCore exposes tool definitions required by MCP tool listing.
type LLMCore interface {
	ToolDefinitions() []llm.ToolDefinition
}

// ContextLLMCore is an optional extension for cores that can list tool
// definitions using request-scoped context.
type ContextLLMCore interface {
	ToolDefinitionsWithContext(ctx context.Context) []llm.ToolDefinition
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
	Addr      string   `yaml:"addr"`
	Port      int      `yaml:"port"`
	ToolItems []string `yaml:"toolItems"`
}

const DefaultPort = 5000

// Enabled reports whether MCP exposure is configured on this server config.
// When tool items are present and no explicit port or addr is supplied, the
// server falls back to the default loopback port.
func (c *ServerConfig) Enabled() bool {
	if c == nil {
		return false
	}
	return strings.TrimSpace(c.Addr) != "" || c.Port != 0 || len(c.ToolPatterns()) > 0
}

// EffectivePort returns the configured MCP port or the default port when the
// config is enabled but no explicit port was supplied.
func (c *ServerConfig) EffectivePort() int {
	if c == nil {
		return 0
	}
	if c.Port > 0 {
		return c.Port
	}
	if !c.Enabled() || strings.TrimSpace(c.Addr) != "" {
		return 0
	}
	return DefaultPort
}

// ListenAddr returns the effective bind address for the MCP server. If addr is
// explicitly configured it is used as-is; otherwise the server binds to the
// loopback interface on the effective port.
func (c *ServerConfig) ListenAddr() string {
	if c == nil {
		return ""
	}
	if addr := strings.TrimSpace(c.Addr); addr != "" {
		return addr
	}
	port := c.EffectivePort()
	if port <= 0 {
		return ""
	}
	return fmt.Sprintf("127.0.0.1:%d", port)
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
