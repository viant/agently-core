package scenarios

import (
	"strings"
	"testing"

	promptdef "github.com/viant/agently-core/protocol/prompt"
	skillproto "github.com/viant/agently-core/protocol/skill"
)

func TestCatalog_IncludesResolvedProfileKnowledge(t *testing.T) {
	parallel := true
	profiles := []*promptdef.Profile{
		{
			ID:                "performance_analysis",
			Name:              "Performance Analysis",
			Description:       "Use for pacing and KPI diagnosis.",
			AppliesTo:         []string{"performance", "pacing"},
			ToolBundles:       []string{"analyst-performance-tools"},
			PreferredTools:    []string{"steward-MetricsAdCube"},
			Template:          "analytics_dashboard",
			Templates:         []string{"analytics_dashboard", "analytics_dashboard"},
			ParallelToolCalls: &parallel,
			Expansion:         &promptdef.Expansion{Mode: "llm", Model: "openai_gpt-5_mini", MaxTokens: 600},
			Messages: []promptdef.Message{
				{Role: "system", Text: "Hard rules:\n- MUST emit DATA:pacing_campaign"},
				{Role: "user", Text: "Analyze pacing and KPI health."},
			},
		},
	}

	got := Catalog(profiles, []string{"performance_analysis"})
	for _, want := range []string{
		"Available profile knowledge:",
		"## Profile `performance_analysis`",
		"- ToolBundles: analyst-performance-tools",
		"- PreferredTools: steward-MetricsAdCube",
		"- Templates: analytics_dashboard",
		"- ParallelToolCalls: true",
		"- Expansion: llm (model=openai_gpt-5_mini, maxTokens=600)",
		"#### system 1",
		"MUST emit DATA:pacing_campaign",
		"#### user 2",
		"Analyze pacing and KPI health.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("catalog missing %q\n%s", want, got)
		}
	}
}

func TestSkillCatalog_IncludesVisibleSkillBusinessKnowledge(t *testing.T) {
	skills := []*skillproto.Skill{
		{
			Frontmatter: skillproto.Frontmatter{
				Name:         "forecast",
				Description:  "Single canonical forecasting skill.",
				AllowedTools: "steward-ForecastingCube steward-AdTargetingProfile",
				Agently:      &skillproto.AgentlyMetadata{Context: "inline"},
			},
			Body: "# Forecast\n\nUse the normalized targeting stack.\n",
		},
	}

	got := SkillCatalog(skills)
	for _, want := range []string{
		"Available skill knowledge:",
		"## Skill `forecast`",
		"- ExecutionMode: inline",
		"- AllowedTools: steward-ForecastingCube steward-AdTargetingProfile",
		"### Skill Body",
		"Use the normalized targeting stack.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("skill catalog missing %q\n%s", want, got)
		}
	}
}
