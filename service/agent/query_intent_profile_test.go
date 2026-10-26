package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestQueryInput_PlaybookContractAndLegacyDecode(t *testing.T) {
	for name, raw := range map[string]string{
		"canonical":     `{"playbookId":"product_knowledge"}`,
		"legacy intent": `{"intentProfileId":"product_knowledge"}`,
		"legacy prompt": `{"promptProfileId":"product_knowledge"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var input QueryInput
			require.NoError(t, json.Unmarshal([]byte(raw), &input))
			require.Equal(t, "product_knowledge", input.EffectiveIntentProfileID())
			encoded, err := json.Marshal(&input)
			require.NoError(t, err)
			require.Contains(t, string(encoded), `"playbookId":"product_knowledge"`)
			require.NotContains(t, string(encoded), "intentProfileId")
			require.NotContains(t, string(encoded), "promptProfileId")
		})
	}

	var yamlInput QueryInput
	require.NoError(t, yaml.Unmarshal([]byte("promptProfileId: product_knowledge\n"), &yamlInput))
	require.Equal(t, "product_knowledge", yamlInput.EffectiveIntentProfileID())
	var conflict QueryInput
	require.ErrorContains(t, json.Unmarshal([]byte(`{"playbookId":"a","intentProfileId":"b"}`), &conflict), "conflicting")
}
