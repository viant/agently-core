package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/agently-core/app/store/native"
	"strings"

	mcpschema "github.com/viant/mcp-protocol/schema"
)

// AGUIMCPAppsHost contains trusted host services. Request properties cannot
// install callbacks or authorize an app instance.
type AGUIMCPAppsHost struct {
	bind           func(context.Context, AGUIMCPAppBinding) AGUIMCPAppsBindings
	Authorize      func(context.Context, AGUIMCPAppBinding) error
	AuthorizeTool  func(context.Context, AGUIMCPAppBinding, string) error
	ToolCaller     MCPUIToolCaller
	ResourceReader func(context.Context, string, string) (*mcpschema.ReadResourceResult, error)
	Ping           func(context.Context, string) (any, error)
	Notification   func(context.Context, string, map[string]json.RawMessage) error
}

func (h *AGUIMCPAppsHost) methods() []string {
	if h == nil {
		return nil
	}
	if h.bind != nil {
		return []string{"resources/read", "tools/call", "ping"}
	}
	if h.Authorize == nil {
		return nil
	}
	var methods []string
	if h.ResourceReader != nil {
		methods = append(methods, "resources/read")
	}
	if h.ToolCaller != nil && h.AuthorizeTool != nil {
		methods = append(methods, "tools/call")
	}
	if h.Ping != nil {
		methods = append(methods, "ping")
	}
	if h.Notification != nil {
		methods = append(methods, "notifications/message")
	}
	return methods
}

func (h *AGUIMCPAppsHost) Bind(ctx context.Context, app AGUIMCPAppBinding) AGUIMCPAppsBindings {
	if h == nil {
		return AGUIMCPAppsBindings{App: app}
	}
	if h.bind != nil {
		return h.bind(ctx, app)
	}
	return AGUIMCPAppsBindings{App: app, Authorize: h.Authorize, AuthorizeTool: h.AuthorizeTool,
		ToolCaller: h.ToolCaller, ResourceReader: h.ResourceReader, Ping: h.Ping, Notification: h.Notification}
}

// WithAGUIMCPAppsHost supplies an explicitly authorized host integration.
func WithAGUIMCPAppsHost(host *AGUIMCPAppsHost) HandlerOption {
	return func(config *handlerConfig) { config.mcpAppsHost = host }
}

// aguiMCPAppsHost exists only when the native runtime can enforce visibility,
// current agent policy and the configured MCP manager on every operation.
func (c *backendClient) aguiMCPAppsHost() *AGUIMCPAppsHost {
	if c != nil && c.approvalMCPHost != nil {
		return c.approvalMCPHost
	}
	if c == nil || c.goalInvoker == nil || c.conv == nil || c.agent == nil || c.agent.Finder() == nil || c.mcpMgr == nil {
		return nil
	}
	return &AGUIMCPAppsHost{bind: func(_ context.Context, app AGUIMCPAppBinding) AGUIMCPAppsBindings {
		authorize := func(ctx context.Context, candidate AGUIMCPAppBinding) error {
			if candidate != app || app.AppInstanceID == "" || app.ThreadID == "" || app.ServerID == "" || app.ResourceURI == "" {
				return fmt.Errorf("native MCP app binding is incomplete or changed")
			}
			if err := native.RequireVisibleConversation(ctx, c.goalInvoker, app.ThreadID); err != nil {
				return err
			}
			options, err := c.mcpMgr.Options(ctx, app.ServerID)
			if err != nil {
				return err
			}
			if options == nil || options.ClientOptions == nil {
				return fmt.Errorf("native MCP app server configuration is unavailable")
			}
			// STDIO has no middleware endpoint identity. It requires a separately supplied
			// trusted integration rather than inventing authority from a command hash.
			hash, err := MCPAppsServerHash(options.Transport.Type, options.Transport.URL)
			if err != nil {
				return err
			}
			if hash != app.ServerHash {
				return fmt.Errorf("native MCP app server configuration changed")
			}
			return nil
		}
		policy := func(ctx context.Context, name string) ([]string, error) {
			if err := authorize(ctx, app); err != nil {
				return nil, err
			}
			conv, err := c.conv.GetConversation(ctx, app.ThreadID)
			if err != nil {
				return nil, err
			}
			if conv == nil || conv.Id != app.ThreadID || conv.AgentId == nil || strings.TrimSpace(*conv.AgentId) == "" {
				return nil, fmt.Errorf("native MCP app conversation agent is unavailable")
			}
			ag, err := c.agent.Finder().Find(ctx, *conv.AgentId)
			if err != nil {
				return nil, err
			}
			return c.agent.MCPAppToolPolicy(ctx, ag, app.ServerID+"/"+name)
		}
		binding := c.BindMCPApp(app, authorize, func(ctx context.Context, candidate AGUIMCPAppBinding, name string) error {
			if candidate != app {
				return fmt.Errorf("native MCP app binding changed")
			}
			_, err := policy(ctx, name)
			return err
		}, nil)
		reader := binding.ResourceReader
		binding.ResourceReader = func(ctx context.Context, server, uri string) (*mcpschema.ReadResourceResult, error) {
			if err := authorize(ctx, app); err != nil {
				return nil, err
			}
			if server != app.ServerID || uri != app.ResourceURI {
				return nil, fmt.Errorf("native MCP app resource binding changed")
			}
			return reader(ctx, server, uri)
		}
		ping := binding.Ping
		binding.Ping = func(ctx context.Context, server string) (any, error) {
			if err := authorize(ctx, app); err != nil {
				return nil, err
			}
			return ping(ctx, server)
		}
		binding.ToolCaller = func(ctx context.Context, in *MCPUIToolCallInput) (*MCPUIToolCallOutput, error) {
			if in == nil || in.ConversationID != app.ThreadID {
				return nil, fmt.Errorf("native MCP app conversation binding changed")
			}
			prefix := app.ServerID + "/"
			if !strings.HasPrefix(in.ToolName, prefix) {
				return nil, fmt.Errorf("native MCP app tool server binding changed")
			}
			method := strings.TrimPrefix(in.ToolName, prefix)
			if method == "" {
				return nil, fmt.Errorf("native MCP app tool name unavailable")
			}
			bundles, err := policy(ctx, method)
			if err != nil {
				return nil, err
			}
			conv, err := c.conv.GetConversation(ctx, app.ThreadID)
			if err != nil {
				return nil, err
			}
			if conv == nil || conv.AgentId == nil {
				return nil, fmt.Errorf("native MCP app conversation agent unavailable")
			}
			agent, err := c.agent.Finder().Find(ctx, *conv.AgentId)
			if err != nil {
				return nil, err
			}
			preparedCtx, err := c.agent.PrepareMCPAppToolContext(ctx, agent, in.ToolName)
			if err != nil {
				return nil, err
			}
			trusted := *in
			trusted.ToolBundles = bundles
			trusted.AssistantText = ""
			return c.ExecuteMCPUIToolCall(preparedCtx, &trusted)
		}
		return binding
	}}
}
