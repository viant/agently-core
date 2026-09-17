package scenarios

import (
	"strings"
	"testing"

	intent "github.com/viant/agently-core/protocol/intent"
	skillproto "github.com/viant/agently-core/protocol/skill"
)

func TestCatalog_IncludesResolvedProfileKnowledge(t *testing.T) {
	parallel := true
	profiles := []*intent.Profile{
		{
			ID:                "performance_analysis",
			Name:              "Performance Analysis",
			Description:       "Use for performance and KPI diagnosis.",
			AppliesTo:         []string{"performance", "performance"},
			ToolBundles:       []string{"analyst-performance-tools"},
			PreferredTools:    []string{"workspace-MetricsCube"},
			Template:          "analytics_dashboard",
			Templates:         []string{"analytics_dashboard", "analytics_dashboard"},
			ParallelToolCalls: &parallel,
			Expansion:         &intent.Expansion{Mode: "llm", Model: "openai_gpt-5_mini", MaxTokens: 600},
			Messages: []intent.Message{
				{Role: "system", Text: "Hard rules:\n- MUST emit DATA:performance_summary"},
				{Role: "user", Text: "Analyze performance and metric health."},
			},
		},
	}

	got := Catalog(profiles, []string{"performance_analysis"})
	for _, want := range []string{
		"Available profile knowledge:",
		"## Profile `performance_analysis`",
		"- ToolBundles: analyst-performance-tools",
		"- PreferredTools: workspace-MetricsCube",
		"- Templates: analytics_dashboard",
		"- ParallelToolCalls: true",
		"- Expansion: llm (model=openai_gpt-5_mini, maxTokens=600)",
		"#### system 1",
		"MUST emit DATA:performance_summary",
		"#### user 2",
		"Analyze performance and metric health.",
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
				AllowedTools: "workspace-ForecastCube workspace-TargetingProfile",
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
		"- AllowedTools: workspace-ForecastCube workspace-TargetingProfile",
		"### Skill Body",
		"Use the normalized targeting stack.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("skill catalog missing %q\n%s", want, got)
		}
	}
}
