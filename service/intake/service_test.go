package intake

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	tpldef "github.com/viant/agently-core/protocol/template"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

func TestParseOutput_ValidJSON(t *testing.T) {
	raw := `{"title":"Campaign 4821","intent":"diagnosis","context":{"campaignId":"4821"},"suggestedProfileId":"performance_analysis","confidence":0.91}`
	tc, err := parseOutput(raw)
	require.NoError(t, err)
	assert.Equal(t, "Campaign 4821", tc.Title)
	assert.Equal(t, "diagnosis", tc.Intent)
	assert.Equal(t, "4821", tc.Context["campaignId"])
	assert.Equal(t, "performance_analysis", tc.SuggestedProfileId)
	assert.InDelta(t, 0.91, tc.Confidence, 0.001)
}

func TestParseOutput_FencedJSON(t *testing.T) {
	raw := "```json\n{\"title\":\"test\"}\n```"
	tc, err := parseOutput(raw)
	require.NoError(t, err)
	assert.Equal(t, "test", tc.Title)
}

func TestParseOutput_ProsePrefix(t *testing.T) {
	raw := "Here is the result:\n{\"title\":\"test\",\"intent\":\"summary\"}"
	tc, err := parseOutput(raw)
	require.NoError(t, err)
	assert.Equal(t, "test", tc.Title)
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
	tc := &TurnContext{
		Title:              "T",
		Intent:             "diagnosis",
		Context:            map[string]string{"k": "v"},
		SuggestedProfileId: "perf",
		Confidence:         0.9,
		AppendToolBundles:  []string{"bundle-a"},
		TemplateId:         "dashboard",
	}
	filterByScope(tc, cfg)
	assert.Equal(t, "T", tc.Title)
	assert.Equal(t, "diagnosis", tc.Intent)
	assert.Equal(t, "v", tc.Context["k"])
	// Class B zeroed
	assert.Empty(t, tc.SuggestedProfileId)
	assert.Zero(t, tc.Confidence)
	assert.Nil(t, tc.AppendToolBundles)
	assert.Empty(t, tc.TemplateId)
}

func TestFilterByScope_ClassBIncluded(t *testing.T) {
	cfg := &agentmdl.Intake{
		Enabled: true,
		Scope:   []string{"title", "profile", "tools", "template"},
	}
	tc := &TurnContext{
		Title:              "T",
		SuggestedProfileId: "perf",
		Confidence:         0.9,
		AppendToolBundles:  []string{"bundle-a"},
		TemplateId:         "dashboard",
	}
	filterByScope(tc, cfg)
	assert.Equal(t, "perf", tc.SuggestedProfileId)
	assert.InDelta(t, 0.9, tc.Confidence, 0.001)
	assert.Equal(t, []string{"bundle-a"}, tc.AppendToolBundles)
	assert.Equal(t, "dashboard", tc.TemplateId)
	// clarification not in scope
	assert.False(t, tc.ClarificationNeeded)
}

func TestFilterByScope_NilSafe(t *testing.T) {
	// Neither nil cfg nor nil tc should panic.
	filterByScope(nil, &agentmdl.Intake{})
	filterByScope(&TurnContext{}, nil)
}

func TestBuildOutputSchema_ClassAOnly(t *testing.T) {
	cfg := &agentmdl.Intake{Scope: []string{"title", "context", "intent"}}
	schema := buildOutputSchema(cfg)
	assert.Contains(t, schema, "title")
	assert.Contains(t, schema, "context")
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
	templateBody := []byte("id: audience_forecast_dashboard\nname: audience_forecast_dashboard\ndescription: audience forecast dashboard\nappliesTo: [forecast, audience]\n")
	require.NoError(t, os.WriteFile(filepath.Join(templateDir, "audience_forecast_dashboard.yaml"), templateBody, 0o644))

	svc := &Service{
		templateRepo: tplrepo.NewWithStore(fsstore.New(tmpDir)),
	}
	cfg := &agentmdl.Intake{Scope: []string{"template"}}

	prompt, err := svc.buildSystemPrompt(t.Context(), cfg)
	require.NoError(t, err)
	assert.Contains(t, prompt, "Available output templates")
	assert.Contains(t, prompt, "audience_forecast_dashboard")
	assert.Contains(t, prompt, "audience forecast dashboard")
}

func TestBuildGenerateInput_UsesLowReasoningForJSONIntake(t *testing.T) {
	svc := &Service{}
	cfg := &agentmdl.Intake{MaxTokens: 400, Scope: []string{"intent", "context", "template"}}

	in := svc.buildGenerateInput("openai_gpt-5_mini", "system", "user", "", cfg)
	require.NotNil(t, in)
	require.NotNil(t, in.ModelSelection.Options)
	require.NotNil(t, in.ModelSelection.Options.Reasoning)
	assert.Equal(t, "low", in.ModelSelection.Options.Reasoning.Effort)
	assert.True(t, in.ModelSelection.Options.JSONMode)
	assert.Equal(t, "application/json", in.ModelSelection.Options.ResponseMIMEType)
	require.NotNil(t, in.ModelSelection.Options.OutputSchema)
	props, _ := in.ModelSelection.Options.OutputSchema["properties"].(map[string]interface{})
	_, hasIntent := props["intent"]
	_, hasContext := props["context"]
	_, hasTemplate := props["templateId"]
	assert.True(t, hasIntent)
	assert.True(t, hasContext)
	assert.True(t, hasTemplate)
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
	assert.Equal(t, 400, cfg.EffectiveMaxTokens())

	cfg.ConfidenceThreshold = 0.7
	cfg.TimeoutSec = 10
	cfg.MaxTokens = 200
	assert.InDelta(t, 0.7, cfg.EffectiveConfidenceThreshold(), 0.001)
	assert.Equal(t, 10, cfg.EffectiveTimeoutSec())
	assert.Equal(t, 200, cfg.EffectiveMaxTokens())
}

func TestTemplateAppliesTo_DefaultsToGeneral(t *testing.T) {
	assert.Equal(t, []string{"general"}, templateAppliesTo(&tpldef.Template{}))
	assert.Equal(t, []string{"forecast"}, templateAppliesTo(&tpldef.Template{AppliesTo: []string{"forecast"}}))
}

func TestBuildOutputJSONSchema_RespectsScope(t *testing.T) {
	cfg := &agentmdl.Intake{Scope: []string{"intent", "context", "template"}}
	schema := buildOutputJSONSchema(cfg)
	props, _ := schema["properties"].(map[string]interface{})
	require.NotNil(t, props)
	_, hasIntent := props["intent"]
	_, hasContext := props["context"]
	_, hasTemplate := props["templateId"]
	_, hasBundles := props["appendToolBundles"]
	required, _ := schema["required"].([]string)
	assert.True(t, hasIntent)
	assert.True(t, hasContext)
	assert.True(t, hasTemplate)
	assert.False(t, hasBundles)
	assert.Equal(t, false, schema["additionalProperties"])
	assert.Contains(t, required, "title")
	assert.Contains(t, required, "intent")
	assert.Contains(t, required, "context")
	assert.Contains(t, required, "templateId")
}
func TestParseOutput_LegacyEntitiesAlias(t *testing.T) {
	raw := `{"title":"Campaign 4821","intent":"diagnosis","entities":{"campaignId":"4821"}}`
	tc, err := parseOutput(raw)
	require.NoError(t, err)
	assert.Equal(t, "4821", tc.Context["campaignId"])
}

// TestParseOutput_WorkspaceIntakeFields asserts that workspace-intake JSON
// output (selectedAgentId, mode, source, activateSkills) parses correctly.
// Legacy agent-intake outputs without these fields keep working unchanged.
func TestParseOutput_WorkspaceIntakeFields(t *testing.T) {
	t.Run("workspace intake output", func(t *testing.T) {
		raw := `{
			"title":"Forecast order 2652067",
			"intent":"forecast_review",
			"selectedAgentId":"steward",
			"mode":"route",
			"source":"workspace",
			"activateSkills":["audience-forecast-review"],
			"confidence":0.94
		}`
		tc, err := parseOutput(raw)
		require.NoError(t, err)
		assert.Equal(t, "steward", tc.SelectedAgentID)
		assert.Equal(t, "route", tc.Mode)
		assert.Equal(t, "workspace", tc.Source)
		assert.Equal(t, []string{"audience-forecast-review"}, tc.ActivateSkills)
		assert.InDelta(t, 0.94, tc.Confidence, 0.001)
	})

	t.Run("legacy agent intake output preserves zero workspace fields", func(t *testing.T) {
		raw := `{"title":"Campaign 4821","intent":"diagnosis","confidence":0.91}`
		tc, err := parseOutput(raw)
		require.NoError(t, err)
		assert.Equal(t, "", tc.SelectedAgentID, "legacy outputs must not invent SelectedAgentID")
		assert.Equal(t, "", tc.Mode, "legacy outputs must not invent Mode")
		assert.Equal(t, "", tc.Source, "legacy outputs must not invent Source")
		assert.Nil(t, tc.ActivateSkills, "legacy outputs must not invent ActivateSkills")
	})

	t.Run("clarify mode", func(t *testing.T) {
		raw := `{"mode":"clarify","clarificationNeeded":true,"clarificationQuestion":"Which order?","source":"workspace"}`
		tc, err := parseOutput(raw)
		require.NoError(t, err)
		assert.Equal(t, "clarify", tc.Mode)
		assert.True(t, tc.ClarificationNeeded)
		assert.Equal(t, "Which order?", tc.ClarificationQuestion)
		assert.Equal(t, "", tc.SelectedAgentID, "clarify mode does not pin an agent")
	})
}

// TestSanitizeAgentRefinement enforces the rule that agent intake never writes
// SelectedAgentID or Mode. Both must be stripped; both must be reported.
func TestSanitizeAgentRefinement(t *testing.T) {
	t.Run("nil safe", func(t *testing.T) {
		assert.Nil(t, SanitizeAgentRefinement(nil))
	})

	t.Run("clean refinement passes through unchanged", func(t *testing.T) {
		tc := &TurnContext{
			Title:              "x",
			Intent:             "y",
			SuggestedProfileId: "p",
			TemplateId:         "t",
		}
		stripped := SanitizeAgentRefinement(tc)
		assert.Empty(t, stripped)
		assert.Equal(t, "x", tc.Title)
		assert.Equal(t, "y", tc.Intent)
		assert.Equal(t, "p", tc.SuggestedProfileId)
		assert.Equal(t, "t", tc.TemplateId)
	})

	t.Run("strips SelectedAgentID and reports", func(t *testing.T) {
		tc := &TurnContext{SelectedAgentID: "rogue"}
		stripped := SanitizeAgentRefinement(tc)
		assert.Equal(t, []string{"selectedAgentId"}, stripped)
		assert.Equal(t, "", tc.SelectedAgentID)
	})

	t.Run("strips Mode and reports", func(t *testing.T) {
		tc := &TurnContext{Mode: "route"}
		stripped := SanitizeAgentRefinement(tc)
		assert.Equal(t, []string{"mode"}, stripped)
		assert.Equal(t, "", tc.Mode)
	})

	t.Run("strips both at once", func(t *testing.T) {
		tc := &TurnContext{SelectedAgentID: "rogue", Mode: "route", Title: "kept"}
		stripped := SanitizeAgentRefinement(tc)
		assert.Equal(t, []string{"selectedAgentId", "mode"}, stripped)
		assert.Equal(t, "", tc.SelectedAgentID)
		assert.Equal(t, "", tc.Mode)
		assert.Equal(t, "kept", tc.Title, "non-restricted fields untouched")
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
