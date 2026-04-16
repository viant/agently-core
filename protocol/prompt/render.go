package prompt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"github.com/viant/afs"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

// MCPManager is the subset of *mcpmgr.Manager used by Render.
// *mcpmgr.Manager satisfies this interface without any changes.
type MCPManager interface {
	Get(ctx context.Context, convID, serverName string) (mcpclient.Interface, error)
}

// RenderOptions carries optional context needed during rendering.
type RenderOptions struct {
	// ConversationID is used to retrieve the MCP client from the manager.
	ConversationID string
	// Binding is an arbitrary key-value map rendered into message text and
	// into MCP arg values via Go's text/template engine.
	Binding map[string]interface{}
	// FS is used to load messages whose source is a URI.
	// When nil, afs.New() is used.
	FS afs.Service
}

// Render resolves the profile's instruction source and returns []Message.
//
// Source priority:
//  1. If p.MCP is set: call the MCP server's GetPrompt; convert the result.
//  2. If p.Messages is set: render each message (URI loaded, text templated).
//  3. If p.Instructions is set: wrap as a single system message.
//
// mgr may be nil; MCP-sourced profiles will return an error in that case.
// opts may be nil; defaults are applied.
func (p *Profile) Render(ctx context.Context, mgr MCPManager, opts *RenderOptions) ([]Message, error) {
	if opts == nil {
		opts = &RenderOptions{}
	}
	if p.MCP != nil {
		return p.renderMCP(ctx, mgr, opts)
	}
	return p.renderLocal(ctx, opts)
}

// renderMCP calls the MCP server and converts its PromptMessages to []Message.
func (p *Profile) renderMCP(ctx context.Context, mgr MCPManager, opts *RenderOptions) ([]Message, error) {
	if mgr == nil {
		return nil, fmt.Errorf("profile %q: MCP source requires a manager but none was provided", p.ID)
	}
	cli, err := mgr.Get(ctx, opts.ConversationID, p.MCP.Server)
	if err != nil {
		return nil, fmt.Errorf("profile %q: get MCP client for server %q: %w", p.ID, p.MCP.Server, err)
	}

	// Render arg values as Go templates against Binding.
	args, err := renderArgs(p.MCP.Args, opts.Binding)
	if err != nil {
		return nil, fmt.Errorf("profile %q: render MCP args: %w", p.ID, err)
	}

	result, err := cli.GetPrompt(ctx, &mcpschema.GetPromptRequestParams{
		Name:      p.MCP.Prompt,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("profile %q: MCP GetPrompt %q: %w", p.ID, p.MCP.Prompt, err)
	}
	if result == nil {
		return nil, nil
	}
	return convertMCPMessages(result.Messages), nil
}

// renderLocal renders inline (text/URI) messages with Go-template substitution.
func (p *Profile) renderLocal(ctx context.Context, opts *RenderOptions) ([]Message, error) {
	raw := p.EffectiveMessages()
	if len(raw) == 0 {
		return nil, nil
	}
	fs := opts.FS
	if fs == nil {
		fs = afs.New()
	}
	out := make([]Message, 0, len(raw))
	for _, m := range raw {
		text, err := resolveMessageText(ctx, m, fs, opts.Binding)
		if err != nil {
			return nil, fmt.Errorf("profile %q: message role=%s: %w", p.ID, m.Role, err)
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, Message{Role: m.Role, Text: text})
	}
	return out, nil
}

// resolveMessageText returns the final text for a message:
//   - URI messages: load from filesystem then template-render.
//   - Text messages: template-render directly.
func resolveMessageText(ctx context.Context, m Message, fs afs.Service, binding map[string]interface{}) (string, error) {
	raw := strings.TrimSpace(m.Text)
	if uri := strings.TrimSpace(m.URI); uri != "" {
		data, err := fs.DownloadWithURL(ctx, uri)
		if err != nil {
			return "", fmt.Errorf("load URI %q: %w", uri, err)
		}
		raw = strings.TrimSpace(string(data))
	}
	if raw == "" || len(binding) == 0 {
		return raw, nil
	}
	return renderTemplate(raw, binding)
}

// renderTemplate applies Go's text/template engine to src with data.
func renderTemplate(src string, data map[string]interface{}) (string, error) {
	t, err := template.New("msg").Option("missingkey=zero").Parse(src)
	if err != nil {
		// Not a valid template — return as-is rather than failing.
		return src, nil
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return src, nil
	}
	return buf.String(), nil
}

// renderArgs renders each value in args as a Go template against binding.
func renderArgs(args map[string]string, binding map[string]interface{}) (map[string]string, error) {
	if len(args) == 0 {
		return args, nil
	}
	out := make(map[string]string, len(args))
	for k, v := range args {
		if len(binding) == 0 || !strings.Contains(v, "{{") {
			out[k] = v
			continue
		}
		rendered, err := renderTemplate(v, binding)
		if err != nil {
			return nil, fmt.Errorf("arg %q: %w", k, err)
		}
		out[k] = rendered
	}
	return out, nil
}

// convertMCPMessages converts MCP PromptMessages to our local Message type.
// Only text content is supported; other content types are skipped.
func convertMCPMessages(in []mcpschema.PromptMessage) []Message {
	out := make([]Message, 0, len(in))
	for _, pm := range in {
		role := strings.ToLower(strings.TrimSpace(string(pm.Role)))
		text := extractTextFromMCPContent(pm.Content)
		if text == "" {
			continue
		}
		out = append(out, Message{Role: role, Text: text})
	}
	return out
}

// extractTextFromMCPContent extracts text from a PromptMessageContent value.
// MCP content is interface{} and may be a TextContent struct, a map, or JSON.
func extractTextFromMCPContent(content mcpschema.PromptMessageContent) string {
	if content == nil {
		return ""
	}
	// Concrete TextContent struct (most common after unmarshalling)
	if tc, ok := content.(mcpschema.TextContent); ok {
		return strings.TrimSpace(tc.Text)
	}
	if tc, ok := content.(*mcpschema.TextContent); ok && tc != nil {
		return strings.TrimSpace(tc.Text)
	}
	// Map representation (e.g. from JSON decode into interface{})
	if m, ok := content.(map[string]interface{}); ok {
		if t, ok := m["text"].(string); ok {
			return strings.TrimSpace(t)
		}
	}
	// Fallback: JSON-encode and try to extract "text" field
	b, err := json.Marshal(content)
	if err != nil {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return ""
	}
	if t, ok := m["text"].(string); ok {
		return strings.TrimSpace(t)
	}
	return ""
}
