package skill

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	skillproto "github.com/viant/agently-core/protocol/skill"
	"github.com/viant/agently-core/protocol/tool"
)

// matchSkillTool preserves existing exact/service/pattern matching before
// attempting an unqualified method lookup. It never chooses between servers.
func matchSkillTool(ctx context.Context, reg tool.Registry, pattern string) []*llm.ToolDefinition {
	if reg == nil {
		return nil
	}
	matches := matchKnownSkillTool(ctx, reg, pattern)
	if len(matches) > 0 || strings.ContainsAny(pattern, ":/.*?[]|") {
		return matches
	}
	var defs []llm.ToolDefinition
	if lister, ok := reg.(tool.ContextDefinitionLister); ok {
		defs = lister.DefinitionsWithContext(ctx)
	} else {
		defs = reg.Definitions()
	}
	seen := map[string]bool{}
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		method := ""
		if i := strings.LastIndex(name, ":"); i >= 0 {
			method = name[i+1:]
		} else {
			method = mcpname.Name(mcpname.Canonical(name)).Method()
		}
		if !strings.EqualFold(method, pattern) {
			continue
		}
		key := strings.ToLower(mcpname.Canonical(name))
		if seen[key] {
			continue
		}
		seen[key] = true
		copy := def
		matches = append(matches, &copy)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Name < matches[j].Name })
	return matches
}

func matchKnownSkillTool(ctx context.Context, reg tool.Registry, pattern string) []*llm.ToolDefinition {
	if matcher, ok := reg.(tool.ContextMatcher); ok {
		return matcher.MatchDefinitionWithContext(ctx, pattern)
	}
	return reg.MatchDefinition(pattern)
}

// SetToolRegistry supplies request-scoped discovery for skill activation.
func (s *Service) SetToolRegistry(reg tool.Registry) { s.toolRegistry = reg }

// resolvedToolSkill returns a private copy: resolving one caller's registry
// must never mutate the shared skill catalog or another caller's permissions.
func (s *Service) resolvedToolSkill(ctx context.Context, item *skillproto.Skill) *skillproto.Skill {
	if item == nil || s.toolRegistry == nil || strings.TrimSpace(item.Body) == "" {
		return item
	}
	copy := *item
	var tokens, guidance []string
	for _, token := range skillproto.ParseAllowedTools(item.Frontmatter.AllowedTools) {
		pattern := token.ToolPattern
		if pattern == "" || strings.ContainsAny(pattern, ":/.*?[]|") {
			tokens = append(tokens, token.Raw)
			continue
		}
		// Bare service names and existing direct aliases retain their original meaning.
		if len(matchKnownSkillTool(ctx, s.toolRegistry, pattern)) > 0 {
			tokens = append(tokens, token.Raw)
			continue
		}
		matches := matchSkillTool(ctx, s.toolRegistry, pattern)
		if len(matches) == 0 {
			tokens = append(tokens, token.Raw)
			guidance = append(guidance, fmt.Sprintf("Tool %q was not found in the current registry. Do not invent a server or claim this tool ran.", pattern))
			continue
		}
		names := make([]string, 0, len(matches))
		for _, def := range matches {
			names = append(names, def.Name)
		}
		tokens = append(tokens, names...)
		if len(names) == 1 {
			guidance = append(guidance, fmt.Sprintf("Resolve tool %q to %q.", pattern, names[0]))
		} else {
			guidance = append(guidance, fmt.Sprintf("Tool reference %q matches: %s. Before using it, use the existing elicitation flow to ask the user which MCP server/tool they want. Do not choose a default or call all matches. Use only the user's selected qualified tool; if they cancel or do not choose, leave this action unresolved.", pattern, strings.Join(names, ", ")))
		}
	}
	copy.Frontmatter.AllowedTools = strings.Join(tokens, " ")
	if len(guidance) > 0 {
		copy.Body += "\n\nTool resolution:\n- " + strings.Join(guidance, "\n- ")
	}
	return &copy
}
