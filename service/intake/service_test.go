package intake

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	agentmdl "github.com/viant/agently-core/protocol/agent"
)

func TestParseOutput_ValidJSON(t *testing.T) {
	raw := `{"title":"Campaign 4821","intent":"diagnosis","entities":{"campaignId":"4821"},"suggestedProfileId":"performance_analysis","confidence":0.91}`
	tc, err := parseOutput(raw)
	require.NoError(t, err)
	assert.Equal(t, "Campaign 4821", tc.Title)
	assert.Equal(t, "diagnosis", tc.Intent)
	assert.Equal(t, "4821", tc.Entities["campaignId"])
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
		Scope:   []string{"title", "entities", "intent"},
	}
	tc := &TurnContext{
		Title:              "T",
		Intent:             "diagnosis",
		Entities:           map[string]string{"k": "v"},
		SuggestedProfileId: "perf",
		Confidence:         0.9,
		AppendToolBundles:  []string{"bundle-a"},
		TemplateId:         "dashboard",
	}
	filterByScope(tc, cfg)
	assert.Equal(t, "T", tc.Title)
	assert.Equal(t, "diagnosis", tc.Intent)
	assert.Equal(t, "v", tc.Entities["k"])
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
	cfg := &agentmdl.Intake{Scope: []string{"title", "entities", "intent"}}
	schema := buildOutputSchema(cfg)
	assert.Contains(t, schema, "title")
	assert.Contains(t, schema, "entities")
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
