package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentAllowListSerializationUsesPreferredPlaybookField(t *testing.T) {
	canonical := Agent{Identity: Identity{ID: "reviewer"}, Playbooks: PromptAccess{Bundles: []string{"review"}}, Prompts: PromptAccess{Bundles: []string{"review"}}}
	encoded, err := json.Marshal(canonical)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"playbooks":{"bundles":["review"]}`)
	require.NotContains(t, string(encoded), `"prompts"`)
	legacy := Agent{Identity: Identity{ID: "legacy"}, Prompts: PromptAccess{Bundles: []string{"review"}}}
	encoded, err = json.Marshal(legacy)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"prompts":{"bundles":["review"]}`)
	require.NotContains(t, string(encoded), `"playbooks":{"bundles"`)
}
