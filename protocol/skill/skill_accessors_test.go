package skill

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
)

// Accessors should read the canonical Agently metadata only. Legacy bare-key
// normalization belongs to parser.go, not the runtime Frontmatter shape.
func TestFrontmatter_Accessors_ReadCanonicalAgentlyMetadata(t *testing.T) {
	temp := 0.7
	maxTokens := 16000
	preprocess := true
	timeout := 30
	prefs := &llm.ModelPreferences{
		Hints:                []string{"claude-haiku", "gpt-5-mini"},
		IntelligencePriority: 0.2,
		SpeedPriority:        0.9,
	}
	f := Frontmatter{
		Agently: &AgentlyMetadata{
			Context:              "fork",
			AgentID:              "steward/specialist",
			Model:                "claude-opus",
			Effort:               "high",
			Temperature:          &temp,
			MaxTokens:            &maxTokens,
			Preprocess:           &preprocess,
			PreprocessTimeoutSec: &timeout,
			AsyncNarratorPrompt:  "Keep updates terse.",
			ModelPreferences:     prefs,
		},
	}

	assert.Equal(t, "fork", f.ContextMode())
	assert.Equal(t, "steward/specialist", f.AgentIDValue())
	assert.Equal(t, "claude-opus", f.ModelValue())
	assert.Equal(t, "high", f.EffortValue())
	if got := f.TemperatureValue(); assert.NotNil(t, got) {
		assert.InDelta(t, 0.7, *got, 0.0001)
	}
	assert.Equal(t, 16000, f.MaxTokensValue())
	assert.True(t, f.PreprocessEnabled())
	assert.Equal(t, 30, f.PreprocessTimeoutValue())
	assert.Equal(t, "Keep updates terse.", f.AsyncNarratorPromptValue())
	if got := f.ModelPreferencesValue(); assert.NotNil(t, got) {
		assert.Equal(t, []string{"claude-haiku", "gpt-5-mini"}, got.Hints)
		assert.InDelta(t, 0.2, got.IntelligencePriority, 0.0001)
		assert.InDelta(t, 0.9, got.SpeedPriority, 0.0001)
	}
}

func TestFrontmatter_Accessors_ZeroValuesWithoutAgentlyMetadata(t *testing.T) {
	f := Frontmatter{}
	assert.Equal(t, "inline", f.ContextMode())
	assert.Equal(t, "", f.AgentIDValue())
	assert.Equal(t, "", f.ModelValue())
	assert.Equal(t, "", f.EffortValue())
	assert.Nil(t, f.TemperatureValue())
	assert.Equal(t, 0, f.MaxTokensValue())
	assert.False(t, f.PreprocessEnabled())
	assert.Equal(t, 0, f.PreprocessTimeoutValue())
	assert.Equal(t, "", f.AsyncNarratorPromptValue())
	assert.Nil(t, f.ModelPreferencesValue())
}
