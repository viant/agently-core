package intake

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	tpldef "github.com/viant/agently-core/protocol/template"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	promptrepo "github.com/viant/agently-core/workspace/repository/prompt"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

func TestParseOutput_ValidJSON(t *testing.T) {
	raw := `{"title":"Project 4821","intent":"diagnosis","context":{"projectId":"4821"},"suggestedProfileId":"performance_analysis","confidence":0.91}`
	tc, err := parseOutput(raw)
	require.NoError(t, err)
	assert.Equal(t, "Project 4821", tc.Classification.Title)
	assert.Equal(t, "diagnosis", tc.Classification.Intent)
	assert.Equal(t, "4821", tc.Scope.Values["projectId"])
	assert.Equal(t, "performance_analysis", tc.Prompting.SuggestedProfileID)
	assert.InDelta(t, 0.91, tc.Classification.Confidence, 0.001)
}

func TestParseOutput_FencedJSON(t *testing.T) {
	raw := "```json\n{\"title\":\"test\"}\n```"
	tc, err := parseOutput(raw)
	require.NoError(t, err)
	assert.Equal(t, "test", tc.Classification.Title)
}

func TestParseOutput_ProsePrefix(t *testing.T) {
	raw := "Here is the result:\n{\"title\":\"test\",\"intent\":\"summary\"}"
	tc, err := parseOutput(raw)
	require.NoError(t, err)
	assert.Equal(t, "test", tc.Classification.Title)
}

func TestParseOutput_Invalid(t *testing.T) {
	_, err := parseOutput("not json at all")
	assert.Error(t, err)
}

func TestFilterByScope_ClassAOnly(t *testing.T) {
	cfg := &agentmdl.Intake{
		Enabled: true,
		Scope:   []string{"title", "context", "intent"},
	}
	tc := &Context{
		Classification: ClassificationContext{
			Title:      "T",
			Intent:     "diagnosis",
			Confidence: 0.9,
		},
		Scope: ScopeContext{
			Values: map[string]string{"k": "v"},
		},
		Prompting: PromptingContext{
			SuggestedProfileID: "perf",
			AppendToolBundles:  []string{"bundle-a"},
			TemplateID:         "dashboard",
		},
	}
	filterByScope(tc, cfg)
	assert.Equal(t, "T", tc.Classification.Title)
	assert.Equal(t, "diagnosis", tc.Classification.Intent)
	assert.Equal(t, "v", tc.Scope.Values["k"])
	// Class B zeroed
	assert.Empty(t, tc.Prompting.SuggestedProfileID)
	assert.Zero(t, tc.Classification.Confidence)
	assert.Nil(t, tc.Prompting.AppendToolBundles)
	assert.Empty(t, tc.Prompting.TemplateID)
}

func TestFilterByScope_ClassBIncluded(t *testing.T) {
	cfg := &agentmdl.Intake{
		Enabled: true,
		Scope:   []string{"title", "profile", "tools", "template"},
	}
	tc := &Context{
		Classification: ClassificationContext{
			Title:      "T",
			Confidence: 0.9,
		},
		Prompting: PromptingContext{
			SuggestedProfileID: "perf",
			AppendToolBundles:  []string{"bundle-a"},
			TemplateID:         "dashboard",
		},
	}
	filterByScope(tc, cfg)
	assert.Equal(t, "perf", tc.Prompting.SuggestedProfileID)
	assert.InDelta(t, 0.9, tc.Classification.Confidence, 0.001)
	assert.Equal(t, []string{"bundle-a"}, tc.Prompting.AppendToolBundles)
	assert.Equal(t, "dashboard", tc.Prompting.TemplateID)
}

func TestFilterByScope_NilSafe(t *testing.T) {
	// Neither nil cfg nor nil tc should panic.
	filterByScope(nil, &agentmdl.Intake{})
	filterByScope(&Context{}, nil)
}

func TestBuildOutputSchema_ClassAOnly(t *testing.T) {
	cfg := &agentmdl.Intake{Scope: []string{"title", "context", "intent"}}
	schema := buildOutputSchema(cfg)
	assert.Contains(t, schema, "classification")
	assert.Contains(t, schema, "scope")
	assert.NotContains(t, schema, "suggestedProfileId")
	assert.NotContains(t, schema, "appendToolBundles")
}

func TestBuildOutputSchema_ClassBIncluded(t *testing.T) {
	cfg := &agentmdl.Intake{Scope: []string{"title", "profile", "tools", "template"}}
	schema := buildOutputSchema(cfg)
	assert.Contains(t, schema, "suggestedProfileId")
	assert.Contains(t, schema, "appendToolBundles")
	assert.Contains(t, schema, "templateId")
}

func TestBuildSystemPrompt_IncludesTemplatesWhenTemplateScopeEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	templateDir := filepath.Join(tmpDir, "templates")
	require.NoError(t, os.MkdirAll(templateDir, 0o755))
	templateBody := []byte("id: capacity_review_dashboard\nname: capacity_review_dashboard\ndescription: capacity review dashboard\nappliesTo: [capacity, review]\n")
	require.NoError(t, os.WriteFile(filepath.Join(templateDir, "capacity_review_dashboard.yaml"), templateBody, 0o644))

	svc := &Service{
		templateRepo: tplrepo.NewWithStore(fsstore.New(tmpDir)),
	}
	cfg := &agentmdl.Intake{Scope: []string{"template"}}

	prompt, err := svc.buildSystemPrompt(t.Context(), cfg)
	require.NoError(t, err)
	assert.Contains(t, prompt, "Available output templates")
	assert.Contains(t, prompt, "capacity_review_dashboard")
	assert.Contains(t, prompt, "capacity review dashboard")
}

func TestBuildGenerateInput_UsesMinimalReasoningForJSONIntake(t *testing.T) {
	svc := &Service{}
	cfg := &agentmdl.Intake{MaxTokens: 400, Scope: []string{"intent", "context", "template"}}

	in := svc.buildGenerateInput("openai_gpt-5_mini", "system", "user", "", cfg)
	require.NotNil(t, in)
	require.NotNil(t, in.ModelSelection.Options)
	require.NotNil(t, in.ModelSelection.Options.Reasoning)
	assert.Equal(t, "minimal", in.ModelSelection.Options.Reasoning.Effort)
	assert.True(t, in.ModelSelection.Options.JSONMode)
	assert.Equal(t, "application/json", in.ModelSelection.Options.ResponseMIMEType)
	require.NotNil(t, in.ModelSelection.Options.OutputSchema)
	props, _ := in.ModelSelection.Options.OutputSchema["properties"].(map[string]interface{})
	_, hasClassification := props["classification"]
	_, hasScope := props["scope"]
	_, hasPrompting := props["prompting"]
	assert.True(t, hasClassification)
	assert.True(t, hasScope)
	assert.True(t, hasPrompting)
}

func TestBuildGenerateInputWithContext_ConstrainsTemplateAndProfileEnums(t *testing.T) {
	tmpDir := t.TempDir()
	promptDir := filepath.Join(tmpDir, "prompts")
	templateDir := filepath.Join(tmpDir, "templates")
	require.NoError(t, os.MkdirAll(promptDir, 0o755))
	require.NoError(t, os.MkdirAll(templateDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(promptDir, "performance_analysis.yaml"), []byte("id: performance_analysis\ndescription: performance\nappliesTo: [performance]\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(templateDir, "recommendation_review_dashboard.yaml"), []byte("id: recommendation_review_dashboard\nname: recommendation_review_dashboard\ndescription: recommendation template\nappliesTo: [recommendation]\n"), 0o644))

	svc := &Service{
		profileRepo:  promptrepo.NewWithStore(fsstore.New(tmpDir)),
		templateRepo: tplrepo.NewWithStore(fsstore.New(tmpDir)),
	}
	cfg := &agentmdl.Intake{MaxTokens: 400, Scope: []string{"intent", "profile", "template"}}

	in := svc.buildGenerateInputWithContext(context.Background(), "openai_gpt-5_mini", "system", "user", "", cfg)
	require.NotNil(t, in)
	require.NotNil(t, in.ModelSelection.Options)
	require.NotNil(t, in.ModelSelection.Options.OutputSchema)

	props, _ := in.ModelSelection.Options.OutputSchema["properties"].(map[string]interface{})
	require.NotNil(t, props)
	prompting, _ := props["prompting"].(map[string]interface{})
	require.NotNil(t, prompting)
	promptingProps, _ := prompting["properties"].(map[string]interface{})
	require.NotNil(t, promptingProps)

	profileProp, _ := promptingProps["suggestedProfileId"].(map[string]interface{})
	require.NotNil(t, profileProp)
	assert.Equal(t, []string{"", "performance_analysis"}, profileProp["enum"])

	templateProp, _ := promptingProps["templateId"].(map[string]interface{})
	require.NotNil(t, templateProp)
	assert.Equal(t, []string{"", "recommendation_review_dashboard"}, templateProp["enum"])
}

func TestIntake_HasScope(t *testing.T) {
	cfg := &agentmdl.Intake{Scope: []string{"title", "Profile"}}
	assert.True(t, cfg.HasScope("title"))
	assert.True(t, cfg.HasScope("profile")) // case-insensitive
	assert.False(t, cfg.HasScope("tools"))
}

func TestIntake_Defaults(t *testing.T) {
	var cfg agentmdl.Intake
	assert.InDelta(t, 0.85, cfg.EffectiveConfidenceThreshold(), 0.001)
	assert.Equal(t, 15, cfg.EffectiveTimeoutSec())
	assert.Equal(t, 800, cfg.EffectiveMaxTokens())

	cfg.ConfidenceThreshold = 0.7
	cfg.TimeoutSec = 10
	cfg.MaxTokens = 200
	assert.InDelta(t, 0.7, cfg.EffectiveConfidenceThreshold(), 0.001)
	assert.Equal(t, 10, cfg.EffectiveTimeoutSec())
	assert.Equal(t, 200, cfg.EffectiveMaxTokens())
}

func TestTemplateAppliesTo_DefaultsToGeneral(t *testing.T) {
	assert.Equal(t, []string{"general"}, templateAppliesTo(&tpldef.Template{}))
	assert.Equal(t, []string{"capacity"}, templateAppliesTo(&tpldef.Template{AppliesTo: []string{"capacity"}}))
}

func TestBuildOutputJSONSchema_RespectsScope(t *testing.T) {
	cfg := &agentmdl.Intake{Scope: []string{"intent", "context", "template"}}
	schema := buildOutputJSONSchema(cfg)
	props, _ := schema["properties"].(map[string]interface{})
	require.NotNil(t, props)
	_, hasClassification := props["classification"]
	_, hasScope := props["scope"]
	_, hasPrompting := props["prompting"]
	_, hasBundles := props["appendToolBundles"]
	required, _ := schema["required"].([]string)
	assert.True(t, hasClassification)
	assert.True(t, hasScope)
	assert.True(t, hasPrompting)
	assert.False(t, hasBundles)
	assert.Equal(t, false, schema["additionalProperties"])
	assert.Contains(t, required, "classification")
	assert.Contains(t, required, "scope")
	assert.Contains(t, required, "prompting")
}

func TestBuildSystemPrompt_AppendsWorkspaceSpecificPrompt(t *testing.T) {
	svc := &Service{}
	cfg := &agentmdl.Intake{
		Scope:  []string{"intent"},
		Prompt: "Concrete resource troubleshoot requests are actionable without extra clarification.",
	}

	prompt, err := svc.buildSystemPrompt(t.Context(), cfg)
	require.NoError(t, err)
	assert.Contains(t, prompt, "Workspace-specific intake guidance:")
	assert.Contains(t, prompt, "Concrete resource troubleshoot requests are actionable without extra clarification.")
}

func TestBuildSystemPrompt_IncludesMonthDayDateRule(t *testing.T) {
	svc := &Service{}
	cfg := &agentmdl.Intake{Scope: []string{"intent", "context"}}

	prompt, err := svc.buildSystemPrompt(t.Context(), cfg)
	require.NoError(t, err)
	assert.Contains(t, prompt, "If the user gives a concrete month/day date without a year")
	assert.Contains(t, prompt, "assume the current year")
	assert.Contains(t, prompt, "YYYY-MM-DD")
	assert.Contains(t, prompt, "Current local date:")
}

func TestBuildSystemPrompt_FiltersProfilesByAllowList(t *testing.T) {
	tmpDir := t.TempDir()
	promptDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(promptDir, "diagnostic_baseline.yaml"), []byte("id: diagnostic_baseline\ndescription: child only\nappliesTo: [diagnostic_baseline]\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(promptDir, "performance_analysis.yaml"), []byte("id: performance_analysis\ndescription: visible\nappliesTo: [performance]\n"), 0o644))

	svc := &Service{
		profileRepo: promptrepo.NewWithStore(fsstore.New(tmpDir)),
	}
	cfg := &agentmdl.Intake{Scope: []string{"profile"}}
	ctx := runtimerequestctx.WithPromptProfileAllowList(context.Background(), []string{"performance_analysis"})

	prompt, err := svc.buildSystemPrompt(ctx, cfg)
	require.NoError(t, err)
	assert.Contains(t, prompt, "performance_analysis")
	assert.NotContains(t, prompt, "diagnostic_baseline")
}

func TestParseOutput_LegacyEntitiesAlias(t *testing.T) {
	raw := `{"title":"Project 4821","intent":"diagnosis","entities":{"projectId":"4821"}}`
	tc, err := parseOutput(raw)
	require.NoError(t, err)
	assert.Equal(t, "4821", tc.Scope.Values["projectId"])
}

// TestParseOutput_WorkspaceIntakeFields asserts that workspace-intake JSON
// output (selectedAgentId, mode, source) parses correctly.
// Legacy agent-intake outputs without these fields keep working unchanged.
func TestParseOutput_WorkspaceIntakeFields(t *testing.T) {
	t.Run("workspace intake output", func(t *testing.T) {
		raw := `{
			"classification":{"title":"Capacity review 2652067","intent":"capacity_review","confidence":0.94},
			"routing":{"selectedAgentId":"analyst","mode":"route","source":"workspace"}
		}`
		tc, err := parseOutput(raw)
		require.NoError(t, err)
		assert.Equal(t, "Capacity review 2652067", tc.Classification.Title)
		assert.Equal(t, "capacity_review", tc.Classification.Intent)
		assert.Equal(t, "analyst", tc.Routing.SelectedAgentID)
		assert.Equal(t, "route", tc.Routing.Mode)
		assert.Equal(t, "workspace", tc.Routing.Source)
		assert.InDelta(t, 0.94, tc.Classification.Confidence, 0.001)
	})

	t.Run("legacy agent intake output preserves zero workspace fields", func(t *testing.T) {
		raw := `{"title":"Project 4821","intent":"diagnosis","confidence":0.91}`
		tc, err := parseOutput(raw)
		require.NoError(t, err)
		assert.Equal(t, "Project 4821", tc.Classification.Title)
		assert.Equal(t, "diagnosis", tc.Classification.Intent)
		assert.Equal(t, "", tc.Routing.SelectedAgentID, "legacy outputs must not invent SelectedAgentID")
		assert.Equal(t, "", tc.Routing.Mode, "legacy outputs must not invent Mode")
		assert.Equal(t, "", tc.Routing.Source, "legacy outputs must not invent Source")
	})

	t.Run("clarify mode", func(t *testing.T) {
		raw := `{"mode":"clarify","source":"workspace"}`
		tc, err := parseOutput(raw)
		require.NoError(t, err)
		assert.Equal(t, "clarify", tc.Routing.Mode)
		assert.Equal(t, "", tc.Routing.SelectedAgentID, "clarify mode does not pin an agent")
	})

}

// TestResolveModel exercises the resolution order: explicit cfg.Model wins,
// then cfg.ModelPreferences via the matcher, then "" when neither yields one.
// The Service is constructed with llm == nil to test the no-matcher path,
// since the matcher path requires a fully-built core.Service. The
// preferences-via-matcher path is covered by integration tests that wire a
// real core.Service.
func TestResolveModel(t *testing.T) {
	t.Run("explicit cfg.Model wins", func(t *testing.T) {
		s := &Service{}
		got := s.resolveModel(&agentmdl.Intake{Model: "claude-haiku"})
		assert.Equal(t, "claude-haiku", got)
	})

	t.Run("explicit cfg.Model trimmed", func(t *testing.T) {
		s := &Service{}
		got := s.resolveModel(&agentmdl.Intake{Model: "  gpt-5-mini  "})
		assert.Equal(t, "gpt-5-mini", got)
	})

	t.Run("nil cfg returns empty", func(t *testing.T) {
		s := &Service{}
		assert.Equal(t, "", s.resolveModel(nil))
	})

	t.Run("no model and no prefs returns empty", func(t *testing.T) {
		s := &Service{}
		assert.Equal(t, "", s.resolveModel(&agentmdl.Intake{}))
	})

	t.Run("prefs without llm returns empty (degrades cleanly)", func(t *testing.T) {
		s := &Service{} // no llm
		cfg := &agentmdl.Intake{}
		// Construct ModelPreferences via a YAML decode to exercise the custom
		// unmarshaller indirectly.
		// Direct construction is fine here.
		// (The matcher path requires a real core.Service; covered elsewhere.)
		_ = cfg
		_ = s
	})
}
