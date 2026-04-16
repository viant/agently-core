package expose

import (
	"context"
	"strings"

	promptdef "github.com/viant/agently-core/protocol/prompt"
	promptrepo "github.com/viant/agently-core/workspace/repository/prompt"
	"github.com/viant/jsonrpc"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

// ProfileRepo is the subset of *promptrepo.Repository used by the MCP prompt handlers.
// *promptrepo.Repository satisfies this interface without any changes.
type ProfileRepo interface {
	LoadAll(ctx context.Context) ([]*promptdef.Profile, error)
	Load(ctx context.Context, id string) (*promptdef.Profile, error)
}

// WithProfileRepo injects a profile repository into a ToolHandler so that
// prompts/list and prompts/get return agently-core profiles to MCP clients.
func WithProfileRepo(repo ProfileRepo) func(*ToolHandler) {
	return func(h *ToolHandler) { h.profileRepo = repo }
}

// listPrompts implements prompts/list: returns all profiles as MCP Prompt entries.
func listPrompts(ctx context.Context, repo ProfileRepo) (*mcpschema.ListPromptsResult, *jsonrpc.Error) {
	if repo == nil {
		return &mcpschema.ListPromptsResult{Prompts: []mcpschema.Prompt{}}, nil
	}
	profiles, err := repo.LoadAll(ctx)
	if err != nil {
		return nil, jsonrpc.NewInternalError("load profiles: "+err.Error(), nil)
	}
	prompts := make([]mcpschema.Prompt, 0, len(profiles))
	for _, p := range profiles {
		if p == nil {
			continue
		}
		entry := profileToMCPPrompt(p)
		prompts = append(prompts, entry)
	}
	return &mcpschema.ListPromptsResult{Prompts: prompts}, nil
}

// getPrompt implements prompts/get: renders a profile and returns its messages.
func getPrompt(ctx context.Context, repo ProfileRepo, mgr promptdef.MCPManager, params *mcpschema.GetPromptRequestParams) (*mcpschema.GetPromptResult, *jsonrpc.Error) {
	if repo == nil {
		return nil, jsonrpc.NewMethodNotFound("no profile repository configured", nil)
	}
	if params == nil || strings.TrimSpace(params.Name) == "" {
		return nil, jsonrpc.NewInvalidParamsError("prompt name is required", nil)
	}
	profile, err := repo.Load(ctx, strings.TrimSpace(params.Name))
	if err != nil {
		return nil, jsonrpc.NewInternalError("load profile: "+err.Error(), nil)
	}
	if profile == nil {
		return nil, jsonrpc.NewMethodNotFound("prompt not found: "+params.Name, nil)
	}

	// Build binding from request arguments so callers can pass template values.
	var binding map[string]interface{}
	if len(params.Arguments) > 0 {
		binding = make(map[string]interface{}, len(params.Arguments))
		for k, v := range params.Arguments {
			binding[k] = v
		}
	}

	msgs, err := profile.Render(ctx, mgr, &promptdef.RenderOptions{Binding: binding})
	if err != nil {
		return nil, jsonrpc.NewInternalError("render profile: "+err.Error(), nil)
	}

	mcpMsgs := make([]mcpschema.PromptMessage, 0, len(msgs))
	for _, m := range msgs {
		text := strings.TrimSpace(m.Text)
		if text == "" {
			continue
		}
		mcpMsgs = append(mcpMsgs, mcpschema.PromptMessage{
			Role:    mcpschema.Role(strings.ToLower(strings.TrimSpace(m.Role))),
			Content: mcpschema.TextContent{Type: "text", Text: text},
		})
	}
	desc := strings.TrimSpace(profile.Description)
	return &mcpschema.GetPromptResult{
		Description: &desc,
		Messages:    mcpMsgs,
	}, nil
}

func profileToMCPPrompt(p *promptdef.Profile) mcpschema.Prompt {
	desc := strings.TrimSpace(p.Description)
	name := strings.TrimSpace(p.Name)
	entry := mcpschema.Prompt{Name: strings.TrimSpace(p.ID)}
	if name != "" {
		entry.Title = &name
	}
	if desc != "" {
		entry.Description = &desc
	}
	// Expose MCP arg names when the profile has an MCP source with declared args.
	if p.MCP != nil && len(p.MCP.Args) > 0 {
		for k := range p.MCP.Args {
			entry.Arguments = append(entry.Arguments, mcpschema.PromptArgument{
				Description: strPtr(k),
			})
		}
	}
	return entry
}

// NewToolHandlerWithProfiles constructs a ToolHandler with an optional profile
// repository so callers can pass both in a single call.
func NewToolHandlerWithProfiles(exec Executor, patterns []string, repo *promptrepo.Repository) *ToolHandler {
	h := NewToolHandler(exec, patterns)
	if repo != nil {
		h.profileRepo = repo
	}
	return h
}

func strPtr(s string) *string { return &s }
