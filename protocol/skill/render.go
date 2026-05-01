package skill

import (
	"fmt"
	"strings"
)

const DefaultPromptBudgetChars = 4000

func RenderPrompt(skills []Metadata, budgetChars int) string {
	if len(skills) == 0 {
		return ""
	}
	if budgetChars <= 0 {
		budgetChars = DefaultPromptBudgetChars
	}
	lines := []string{
		"<skills_instructions>",
		"## Skills",
		"A skill is a set of local instructions stored in a `SKILL.md` file. Below is the list of skills visible to this agent. Each entry shows a name and description. Activate a skill to see its full instructions.",
		"",
		"### Available skills",
	}
	truncated := 0
	for _, item := range skills {
		mode := strings.TrimSpace(item.ExecutionMode)
		if mode == "" {
			mode = "inline"
		}
		line := fmt.Sprintf("- %s (%s): %s", strings.TrimSpace(item.Name), mode, strings.TrimSpace(item.Description))
		candidate := append(lines, line)
		text := strings.Join(candidate, "\n")
		if len(text)+len("\n### How to use skills\n- Use `llm/skills-list` to re-inspect the visible skills.\n- Use `llm/skills-activate` with `name` and optional `args` to activate one skill.\n<!-- truncated 999 -->\n</skills_instructions>") > budgetChars {
			truncated++
			continue
		}
		lines = candidate
	}
	lines = append(lines,
		"",
		"### How to use skills",
		"- Use `llm/skills:list` to re-inspect the visible skills.",
		"- Use `llm/skills:activate` with `name` and optional `args` to activate one skill.",
		"- Each skill shows its default execution mode in parentheses: `inline`, `fork`, or `detach`.",
		"- Do not set `input.mode` unless you intentionally need to override the skill's default execution mode.",
		"- Activate a skill only when it is relevant to the current task.",
	)
	if truncated > 0 {
		lines = append(lines, fmt.Sprintf("<!-- truncated %d -->", truncated))
	}
	lines = append(lines, "</skills_instructions>")
	return strings.Join(lines, "\n")
}
