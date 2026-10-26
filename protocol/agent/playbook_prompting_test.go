package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestActivationPromptingPrefersSuggestedPlaybookAndDecodesLegacy(t *testing.T) {
	for _, raw := range []string{
		`{"suggestedPlaybookId":"review"}`,
		`{"suggestedIntentProfileId":"review"}`,
		`{"suggestedProfileId":"review"}`,
	} {
		var value ActivationPrompting
		require.NoError(t, json.Unmarshal([]byte(raw), &value))
		require.Equal(t, "review", value.EffectivePlaybookID())
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		require.Contains(t, string(encoded), `"suggestedPlaybookId":"review"`)
		require.NotContains(t, string(encoded), "suggestedIntentProfileId")
		require.NotContains(t, string(encoded), "suggestedProfileId")
	}
	var fromYAML ActivationPrompting
	require.NoError(t, yaml.Unmarshal([]byte("suggestedIntentProfileId: review\n"), &fromYAML))
	require.Equal(t, "review", fromYAML.EffectivePlaybookID())
	var conflict ActivationPrompting
	require.ErrorContains(t, json.Unmarshal([]byte(`{"suggestedPlaybookId":"one","suggestedIntentProfileId":"two"}`), &conflict), "conflicting")
}
