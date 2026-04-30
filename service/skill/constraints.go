package skill

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/viant/agently-core/genai/llm"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	skillproto "github.com/viant/agently-core/protocol/skill"
	"github.com/viant/agently-core/protocol/tool"
)

type Constraints struct {
	ToolPatterns        []string
	ExecFirstTokenAllow []string
}

func ConstraintMetadata(c *Constraints) map[string]interface{} {
	if c == nil {
		return nil
	}
	out := map[string]interface{}{}
	if len(c.ToolPatterns) > 0 {
		items := make([]string, len(c.ToolPatterns))
		copy(items, c.ToolPatterns)
		out["toolPatterns"] = items
	}
	if len(c.ExecFirstTokenAllow) > 0 {
		items := make([]string, len(c.ExecFirstTokenAllow))
		copy(items, c.ExecFirstTokenAllow)
		out["execFirstTokenAllow"] = items
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type constraintsKey struct{}
type runtimeKey struct{}

type RuntimeState struct {
	service *Service
	agent   *agentmdl.Agent
	mu      sync.RWMutex
	active  map[string]struct{}
}

func WithConstraints(ctx context.Context, c *Constraints) context.Context {
	if c == nil {
		return ctx
	}
	return context.WithValue(ctx, constraintsKey{}, c)
}

func ConstraintsFromContext(ctx context.Context) (*Constraints, bool) {
	if ctx == nil {
		return nil, false
	}
	v := ctx.Value(constraintsKey{})
	c, ok := v.(*Constraints)
	return c, ok && c != nil
}

func WithRuntimeState(ctx context.Context, svc *Service, agent *agentmdl.Agent, activeNames []string) context.Context {
	if svc == nil || agent == nil {
		return ctx
	}
	state := &RuntimeState{
		service: svc,
		agent:   agent,
		active:  map[string]struct{}{},
	}
	for _, name := range activeNames {
		if v := strings.TrimSpace(strings.ToLower(name)); v != "" {
			state.active[v] = struct{}{}
		}
	}
	return context.WithValue(ctx, runtimeKey{}, state)
}

func RuntimeStateFromContext(ctx context.Context) (*RuntimeState, bool) {
	if ctx == nil {
		return nil, false
	}
	v := ctx.Value(runtimeKey{})
	state, ok := v.(*RuntimeState)
	return state, ok && state != nil
}

func (s *RuntimeState) Activate(name string) {
	if s == nil {
		return
	}
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return
	}
	s.mu.Lock()
	s.active[name] = struct{}{}
	s.mu.Unlock()
}

func (s *RuntimeState) Constraints() *Constraints {
	if s == nil || s.service == nil || s.agent == nil {
		return nil
	}
	s.mu.RLock()
	names := make([]string, 0, len(s.active))
	for name := range s.active {
		names = append(names, name)
	}
	s.mu.RUnlock()
	return BuildConstraints(s.service.VisibleSkillsByName(s.agent, names))
}

func BuildConstraints(skills []*skillproto.Skill) *Constraints {
	if len(skills) == 0 {
		return nil
	}
	out := &Constraints{}
	seenTool := map[string]struct{}{}
	seenExec := map[string]struct{}{}
	for _, item := range skills {
		if item == nil {
			continue
		}
		for _, token := range skillproto.ParseAllowedTools(item.Frontmatter.AllowedTools) {
			if token.Raw == "" {
				continue
			}
			if token.BashCommand != "" {
				if _, ok := seenExec[token.BashCommand]; !ok {
					seenExec[token.BashCommand] = struct{}{}
					out.ExecFirstTokenAllow = append(out.ExecFirstTokenAllow, token.BashCommand)
				}
				if _, ok := seenTool["system/exec:execute"]; !ok {
					seenTool["system/exec:execute"] = struct{}{}
					out.ToolPatterns = append(out.ToolPatterns, "system/exec:execute")
				}
				continue
			}
			if token.ToolPattern != "" {
				if _, ok := seenTool[token.ToolPattern]; !ok {
					seenTool[token.ToolPattern] = struct{}{}
					out.ToolPatterns = append(out.ToolPatterns, token.ToolPattern)
				}
				if token.ToolPattern == "system/exec" || token.ToolPattern == "system/exec:*" {
					if _, ok := seenTool["system/exec:execute"]; !ok {
						seenTool["system/exec:execute"] = struct{}{}
						out.ToolPatterns = append(out.ToolPatterns, "system/exec:execute")
					}
				}
			}
		}
	}
	if len(out.ToolPatterns) == 0 && len(out.ExecFirstTokenAllow) == 0 {
		return nil
	}
	return out
}

func ExpandDefinitionsForConstraints(defs []*llm.ToolDefinition, reg tool.Registry, c *Constraints) []*llm.ToolDefinition {
	if c == nil || reg == nil || len(c.ToolPatterns) == 0 {
		return defs
	}
	out := make([]*llm.ToolDefinition, 0, len(defs))
	seen := map[string]struct{}{}
	appendDef := func(def *llm.ToolDefinition) {
		if def == nil {
			return
		}
		key := strings.TrimSpace(strings.ToLower(mcpname.Canonical(def.Name)))
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, def)
	}
	for _, def := range defs {
		appendDef(def)
	}
	for _, pattern := range c.ToolPatterns {
		for _, variant := range toolPatternVariants(pattern) {
			for _, def := range reg.MatchDefinition(variant) {
				appendDef(def)
			}
		}
	}
	return out
}

func ValidateExecution(ctx context.Context, toolName string, args map[string]interface{}) error {
	c, ok := ConstraintsFromContext(ctx)
	if (!ok || c == nil) && ctx != nil {
		if state, ok := RuntimeStateFromContext(ctx); ok {
			c = state.Constraints()
		}
	}
	if c == nil {
		return nil
	}
	toolName = strings.TrimSpace(mcpname.Canonical(toolName))
	if len(c.ToolPatterns) > 0 {
		allowed := false
		for _, pattern := range c.ToolPatterns {
			if toolPatternMatch(toolName, pattern) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("tool %q is not allowed by active skill constraints", toolName)
		}
	}
	if toolName == strings.TrimSpace(mcpname.Canonical("system/exec:execute")) && len(c.ExecFirstTokenAllow) > 0 {
		commands, _ := args["commands"].([]string)
		if len(commands) == 0 {
			if raw, ok := args["commands"].([]interface{}); ok {
				for _, item := range raw {
					if s, ok := item.(string); ok {
						commands = append(commands, s)
					}
				}
			}
		}
		for _, cmd := range commands {
			fields := strings.Fields(strings.TrimSpace(cmd))
			if len(fields) == 0 {
				continue
			}
			first := fields[0]
			match := false
			for _, allowed := range c.ExecFirstTokenAllow {
				if first == allowed {
					match = true
					break
				}
			}
			if !match {
				return fmt.Errorf("command %q is not allowed by active skill constraints", first)
			}
		}
	}
	return nil
}

func toolPatternMatch(name, pattern string) bool {
	name = strings.TrimSpace(mcpname.Canonical(name))
	pattern = strings.TrimSpace(mcpname.Canonical(pattern))
	switch {
	case pattern == "*":
		return true
	case strings.HasSuffix(pattern, "*"):
		return strings.HasPrefix(name, strings.TrimSuffix(pattern, "*"))
	default:
		return name == pattern
	}
}

func toolPatternVariants(pattern string) []string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil
	}
	var out []string
	seen := map[string]struct{}{}
	appendVariant := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	appendVariant(pattern)
	appendVariant(mcpname.Canonical(pattern))
	appendVariant(mcpname.Display(pattern))
	return out
}
