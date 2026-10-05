package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/viant/agently-core/runtime/mcpapps"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

// MCPAppsProxyRequest follows the pinned @ag-ui/mcp-apps-middleware contract.
type MCPAppsProxyRequest struct {
	ServerHash string                     `json:"serverHash"`
	ServerID   string                     `json:"serverId,omitempty"`
	Method     string                     `json:"method"`
	Params     map[string]json.RawMessage `json:"params,omitempty"`
}

// AGUIMCPAppBinding is host authority, never accepted from iframe JSON. The
// integration must authorize this exact registered app instance and principal.
type AGUIMCPAppBinding = mcpapps.AppBinding

// AGUIMCPAppsBindings requires the existing approval-aware tool caller. Raw MCP
// CallTool is deliberately absent, preventing a guest from bypassing approval.
type AGUIMCPAppsBindings struct {
	App            AGUIMCPAppBinding
	Authorize      func(context.Context, AGUIMCPAppBinding) error
	AuthorizeTool  func(context.Context, AGUIMCPAppBinding, string) error
	ToolCaller     MCPUIToolCaller
	ResourceReader func(context.Context, string, string) (*mcpschema.ReadResourceResult, error)
	Ping           func(context.Context, string) (any, error)
	Notification   func(context.Context, string, map[string]json.RawMessage) error
}
type mcpAppsBindingKey struct{}

func WithAGUIMCPAppsBindings(ctx context.Context, binding AGUIMCPAppsBindings) context.Context {
	return context.WithValue(ctx, mcpAppsBindingKey{}, binding)
}
func ParseMCPAppsProxyRequest(forwardedProps json.RawMessage) (*MCPAppsProxyRequest, bool, error) {
	forwardedProps = bytes.TrimSpace(forwardedProps)
	if len(forwardedProps) == 0 || forwardedProps[0] != '{' {
		return nil, false, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(forwardedProps, &envelope); err != nil {
		return nil, false, err
	}
	raw, exists := envelope["__proxiedMCPRequest"]
	if !exists {
		return nil, false, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var input MCPAppsProxyRequest
	if err := decoder.Decode(&input); err != nil {
		return nil, true, err
	}
	if strings.TrimSpace(input.ServerHash) == "" || input.Method == "" {
		return nil, true, fmt.Errorf("MCP Apps proxy requires serverHash and method")
	}
	switch input.Method {
	case "tools/call", "resources/read", "notifications/message", "ping":
	default:
		return nil, true, fmt.Errorf("MCP Apps proxy method is not allowed")
	}
	return &input, true, nil
}

// dispatchMCPAppsProxy returns the exact MCP result for RUN_FINISHED.result.
// A queued approval returns a separate pending result and must not be reported
// as successful MCP completion by the enclosing protocol run.
func dispatchMCPAppsProxy(ctx context.Context, operationID string, input *MCPAppsProxyRequest) (result json.RawMessage, pending *MCPUIToolCallOutput, err error) {
	binding, ok := ctx.Value(mcpAppsBindingKey{}).(AGUIMCPAppsBindings)
	if !ok || input == nil || binding.App.AppInstanceID == "" || binding.App.ThreadID == "" || binding.App.ServerID == "" || binding.Authorize == nil {
		return nil, nil, fmt.Errorf("trusted MCP app instance binding is required")
	}
	if input.ServerHash != binding.App.ServerHash || input.ServerID != "" && input.ServerID != mcpAppPublicServerID(binding.App) {
		return nil, nil, fmt.Errorf("MCP Apps proxy server differs from registered app instance")
	}
	if err := binding.Authorize(ctx, binding.App); err != nil {
		return nil, nil, err
	}
	switch input.Method {
	case "resources/read":
		var uri string
		if err := json.Unmarshal(input.Params["uri"], &uri); err != nil {
			return nil, nil, err
		}
		if uri == "" || uri != binding.App.ResourceURI || binding.ResourceReader == nil {
			return nil, nil, fmt.Errorf("MCP app resource URI is outside its registered binding")
		}
		value, err := binding.ResourceReader(ctx, binding.App.ServerID, uri)
		if err != nil {
			return nil, nil, err
		}
		result, err := json.Marshal(value)
		return result, nil, err
	case "ping":
		if binding.Ping == nil {
			return nil, nil, fmt.Errorf("MCP app ping capability is not configured")
		}
		value, err := binding.Ping(ctx, binding.App.ServerID)
		if err != nil {
			return nil, nil, err
		}
		result, err := json.Marshal(value)
		return result, nil, err
	case "notifications/message":
		if binding.Notification == nil {
			return nil, nil, fmt.Errorf("MCP app notification capability is not configured")
		}
		if err := binding.Notification(ctx, binding.App.ServerID, input.Params); err != nil {
			return nil, nil, err
		}
		return json.RawMessage(`{"success":true}`), nil, nil
	case "tools/call":
		if binding.ToolCaller == nil {
			return nil, nil, fmt.Errorf("approval-aware MCP app tool caller is not configured")
		}
		var name string
		if err := json.Unmarshal(input.Params["name"], &name); err != nil || strings.TrimSpace(name) == "" {
			return nil, nil, fmt.Errorf("MCP app tool name is required")
		}
		if binding.AuthorizeTool == nil {
			return nil, nil, fmt.Errorf("MCP app tool permission binding is required")
		}
		if err := binding.AuthorizeTool(ctx, binding.App, name); err != nil {
			return nil, nil, err
		}
		var arguments map[string]any
		if raw, exists := input.Params["arguments"]; exists {
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if err := decoder.Decode(&arguments); err != nil || arguments == nil {
				return nil, nil, fmt.Errorf("MCP app arguments must be an object")
			}
		}
		captureContext, capture := mcpapps.WithCapture(ctx, binding.App.ServerID, name, operationID)
		output, callErr := binding.ToolCaller(captureContext, &MCPUIToolCallInput{ConversationID: binding.App.ThreadID, ToolName: binding.App.ServerID + "/" + name, Arguments: arguments})
		if output != nil && output.Approval != nil {
			return nil, output, nil
		}
		if output != nil && (output.Status == "queued" || output.Status == "pending") {
			return nil, output, nil
		}
		raw, _ := capture.Snapshot()
		if len(raw) > 0 {
			return raw, nil, nil
		}
		if callErr != nil {
			return nil, nil, callErr
		}
		return nil, nil, fmt.Errorf("canonical MCP caller did not expose a full host result")
	default:
		return nil, nil, fmt.Errorf("MCP Apps proxy method is not allowed")
	}
}

// MCPAppsActivity preserves the middleware's exact builtin renderer vocabulary.
func MCPAppsActivity(messageID string, binding AGUIMCPAppBinding, result json.RawMessage, toolInput json.RawMessage) (json.RawMessage, error) {
	return mcpapps.Activity(messageID, binding, result, toolInput)
}

// BindMCPApp uses an already configured server through its existing manager.
// The caller must supply app-instance authorization; a server ID/hash alone is
// insufficient authority to create an iframe origin or choose a conversation.
func (c *backendClient) BindMCPApp(app AGUIMCPAppBinding, authorize func(context.Context, AGUIMCPAppBinding) error, authorizeTool func(context.Context, AGUIMCPAppBinding, string) error, notification func(context.Context, string, map[string]json.RawMessage) error) AGUIMCPAppsBindings {
	return AGUIMCPAppsBindings{
		App: app, Authorize: authorize, AuthorizeTool: authorizeTool, ToolCaller: c.ExecuteMCPUIToolCall,
		ResourceReader: func(ctx context.Context, server, uri string) (*mcpschema.ReadResourceResult, error) {
			if c == nil || c.mcpMgr == nil || server != app.ServerID {
				return nil, fmt.Errorf("configured MCP app server is unavailable")
			}
			client, err := c.mcpMgr.Get(ctx, app.ThreadID, server)
			if err != nil {
				return nil, err
			}
			return client.ReadResource(ctx, &mcpschema.ReadResourceRequestParams{Uri: uri})
		},
		Ping: func(ctx context.Context, server string) (any, error) {
			if c == nil || c.mcpMgr == nil || server != app.ServerID {
				return nil, fmt.Errorf("configured MCP app server is unavailable")
			}
			client, err := c.mcpMgr.Get(ctx, app.ThreadID, server)
			if err != nil {
				return nil, err
			}
			return client.Ping(ctx, nil)
		}, Notification: notification,
	}
}

// MCPAppsServerHash matches the pinned middleware's public endpoint hash. It is
// a routing reference only, never app authorization or a credential token.
func MCPAppsServerHash(transport, endpoint string) (string, error) {
	return mcpapps.ServerHash(transport, endpoint)
}
