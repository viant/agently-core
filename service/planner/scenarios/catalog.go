package scenarios

import (
	"fmt"
	"sort"
	"strings"

	promptdef "github.com/viant/agently-core/protocol/prompt"
	skillproto "github.com/viant/agently-core/protocol/skill"
	promptrepo "github.com/viant/agently-core/workspace/repository/prompt"
)

func Catalog(profiles []*promptdef.Profile, allow []string) string {
	profiles = promptrepo.FilterAllowedProfiles(profiles, allow)
	if len(profiles) == 0 {
		return ""
	}
	sort.SliceStable(profiles, func(i, j int) bool {
		if profiles[i] == nil || profiles[j] == nil {
			return false
		}
		return strings.ToLower(strings.TrimSpace(profiles[i].ID)) < strings.ToLower(strings.TrimSpace(profiles[j].ID))
	})
	var b strings.Builder
	b.WriteString("Available profile knowledge:")
	for _, profile := range profiles {
		if profile == nil {
			continue
		}
		id := strings.TrimSpace(profile.ID)
		if id == "" {
			continue
		}
		b.WriteString("\n\n## Profile `")
		b.WriteString(id)
		b.WriteString("`")
		if name := strings.TrimSpace(profile.Name); name != "" {
			b.WriteString("\n- Name: ")
			b.WriteString(name)
		}
		if desc := strings.TrimSpace(profile.Description); desc != "" {
			b.WriteString("\n- Description: ")
			b.WriteString(desc)
		}
		if len(profile.AppliesTo) > 0 {
			b.WriteString("\n- AppliesTo: ")
			b.WriteString(strings.Join(profile.AppliesTo, ", "))
		}
		if len(profile.ToolBundles) > 0 {
			b.WriteString("\n- ToolBundles: ")
			b.WriteString(strings.Join(profile.ToolBundles, ", "))
		}
		if len(profile.PreferredTools) > 0 {
			b.WriteString("\n- PreferredTools: ")
			b.WriteString(strings.Join(profile.PreferredTools, ", "))
		}
		if len(profile.Resources) > 0 {
			b.WriteString("\n- Resources: ")
			b.WriteString(strings.Join(profile.Resources, ", "))
		}
		if templates := profileTemplates(profile); len(templates) > 0 {
			b.WriteString("\n- Templates: ")
			b.WriteString(strings.Join(templates, ", "))
		}
		if profile.MCP != nil {
			if server := strings.TrimSpace(profile.MCP.Server); server != "" || strings.TrimSpace(profile.MCP.Prompt) != "" {
				b.WriteString("\n- MCP Prompt: ")
				if server != "" {
					b.WriteString(server)
					b.WriteString("/")
				}
				b.WriteString(strings.TrimSpace(profile.MCP.Prompt))
			}
		}
		if profile.ParallelToolCalls != nil {
			b.WriteString("\n- ParallelToolCalls: ")
			b.WriteString(fmt.Sprintf("%t", *profile.ParallelToolCalls))
		}
		if profile.Expansion != nil {
			b.WriteString("\n- Expansion: ")
			b.WriteString(strings.TrimSpace(profile.Expansion.Mode))
			if model := strings.TrimSpace(profile.Expansion.Model); model != "" {
				b.WriteString(" (model=")
				b.WriteString(model)
				if profile.Expansion.MaxTokens > 0 {
					b.WriteString(fmt.Sprintf(", maxTokens=%d", profile.Expansion.MaxTokens))
				}
				b.WriteString(")")
			} else if profile.Expansion.MaxTokens > 0 {
				b.WriteString(fmt.Sprintf(" (maxTokens=%d)", profile.Expansion.MaxTokens))
			}
		}
		messages := profile.EffectiveMessages()
		if len(messages) == 0 {
			continue
		}
		b.WriteString("\n\n### Prompt Messages")
		for i, msg := range messages {
			text := strings.TrimSpace(msg.Text)
			uri := strings.TrimSpace(msg.URI)
			if text == "" && uri == "" {
				continue
			}
			b.WriteString("\n\n#### ")
			role := strings.TrimSpace(msg.Role)
			if role == "" {
				role = "message"
			}
			b.WriteString(role)
			b.WriteString(" ")
			b.WriteString(fmt.Sprintf("%d", i+1))
			if uri != "" {
				b.WriteString("\n- URI: ")
				b.WriteString(uri)
			}
			if text != "" {
				b.WriteString("\n```text\n")
				b.WriteString(text)
				if !strings.HasSuffix(text, "\n") {
					b.WriteString("\n")
				}
				b.WriteString("```")
			}
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "Available profile knowledge:" {
		return ""
	}
	return out
}

func SkillCatalog(skills []*skillproto.Skill) string {
	if len(skills) == 0 {
		return ""
	}
	sort.SliceStable(skills, func(i, j int) bool {
		if skills[i] == nil || skills[j] == nil {
			return false
		}
		return strings.ToLower(strings.TrimSpace(skills[i].Frontmatter.Name)) < strings.ToLower(strings.TrimSpace(skills[j].Frontmatter.Name))
	})
	var b strings.Builder
	b.WriteString("Available skill knowledge:")
	for _, skill := range skills {
		if skill == nil {
			continue
		}
		name := strings.TrimSpace(skill.Frontmatter.Name)
		if name == "" {
			continue
		}
		b.WriteString("\n\n## Skill `")
		b.WriteString(name)
		b.WriteString("`")
		if desc := strings.TrimSpace(skill.Frontmatter.Description); desc != "" {
			b.WriteString("\n- Description: ")
			b.WriteString(desc)
		}
		if mode := strings.TrimSpace(skill.Frontmatter.ContextMode()); mode != "" {
			b.WriteString("\n- ExecutionMode: ")
			b.WriteString(mode)
		}
		if allowed := strings.TrimSpace(skill.Frontmatter.AllowedTools); allowed != "" {
			b.WriteString("\n- AllowedTools: ")
			b.WriteString(allowed)
		}
		if body := strings.TrimSpace(skill.Body); body != "" {
			b.WriteString("\n\n### Skill Body\n```text\n")
			b.WriteString(body)
			if !strings.HasSuffix(body, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("```")
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "Available skill knowledge:" {
		return ""
	}
	return out
}

func profileTemplates(profile *promptdef.Profile) []string {
	if profile == nil {
		return nil
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, 1+len(profile.Templates))
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	add(profile.Template)
	for _, value := range profile.Templates {
		add(value)
	}
	return result
}
