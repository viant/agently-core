package tool

import (
	"strings"

	"github.com/viant/agently-core/genai/llm"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	promptdef "github.com/viant/agently-core/protocol/prompt"
	skillproto "github.com/viant/agently-core/protocol/skill"
)

// Selection is the canonical internal tool-surface shape used by the agent
// runtime regardless of whether the source was agent YAML, a prompt profile, or
// turn-level runtime state.
type Selection struct {
	Bundles []string
	Tools   []string
}

type Selections struct {
	Agent   Selection
	Profile Selection
	Skill   Selection
	Runtime Selection
}

type Effective struct {
	Selections Selections
	Final      Selection
}

func FromAgent(agent *agentmdl.Agent) Selection {
	if agent == nil {
		return Selection{}
	}
	out := FromAgentTool(agent.Tool)
	if len(agent.Skills) > 0 {
		out.Tools = append(out.Tools, skillproto.ListToolName, skillproto.ActivateToolName)
	}
	return Normalize(out)
}

func FromAgentTool(tool agentmdl.Tool) Selection {
	var out Selection
	if len(tool.Bundles) > 0 {
		out.Bundles = append(out.Bundles, tool.Bundles...)
	}
	for _, item := range tool.Items {
		if item == nil {
			continue
		}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = strings.TrimSpace(item.Definition.Name)
		}
		if name == "" {
			continue
		}
		out.Tools = append(out.Tools, name)
	}
	return Normalize(out)
}

func FromPromptProfile(profile *promptdef.Profile) Selection {
	if profile == nil {
		return Selection{}
	}
	return Normalize(Selection{
		Bundles: append([]string(nil), profile.ToolBundles...),
	})
}

func BuildEffective(selections Selections) Effective {
	return Effective{
		Selections: selections,
		Final: Merge(
			selections.Agent,
			selections.Profile,
			selections.Skill,
			selections.Runtime,
		),
	}
}

func Merge(items ...Selection) Selection {
	var out Selection
	for _, item := range items {
		if len(item.Bundles) > 0 {
			out.Bundles = append(out.Bundles, item.Bundles...)
		}
		if len(item.Tools) > 0 {
			out.Tools = append(out.Tools, item.Tools...)
		}
	}
	return Normalize(out)
}

func Normalize(in Selection) Selection {
	return Selection{
		Bundles: NormalizeBundleNames(in.Bundles),
		Tools:   NormalizeToolNames(in.Tools),
	}
}

func NormalizeBundleNames(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, raw := range in {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func NormalizeToolNames(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, raw := range in {
		value := strings.TrimSpace(mcpname.Canonical(raw))
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func ToolNames(defs []llm.ToolDefinition) []string {
	if len(defs) == 0 {
		return nil
	}
	out := make([]string, 0, len(defs))
	for _, def := range defs {
		if name := strings.TrimSpace(def.Name); name != "" {
			out = append(out, name)
		}
	}
	return out
}
