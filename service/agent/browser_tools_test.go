package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/service/browsermcp"
)

func TestBrowserMCPDefinitionsStillRequireToolSelection(t *testing.T) {
	store := convmem.New()
	ctx := context.Background()
	conv := apiconv.NewConversation()
	conv.SetId("browser-fixture")
	require.NoError(t, store.PatchConversations(ctx, conv))
	definition := llm.ToolDefinition{Name: "device-safe_read", Description: "Fixture", Parameters: map[string]interface{}{"type": "object"}}
	svc := &Service{conversation: store, registry: &fakeRegistry{defs: []llm.ToolDefinition{{Name: "server-lookup"}}}}
	for _, selected := range []bool{false, true} {
		session, err := clienttool.NewSession([]llm.ToolDefinition{definition})
		require.NoError(t, err)
		scoped := browsermcp.WithDefinitions(clienttool.WithSession(ctx, session), map[string]llm.ToolDefinition{definition.Name: definition})
		input := &QueryInput{ConversationID: "browser-fixture", Agent: &agentmdl.Agent{Identity: agentmdl.Identity{ID: "agent"}}}
		if selected {
			input.ToolsAllowed = []string{definition.Name}
		}
		bound, err := svc.BuildBinding(scoped, input)
		require.NoError(t, err)
		if selected {
			require.Contains(t, bindingToolNames(bound.Tools.Signatures), definition.Name)
		} else {
			require.NotContains(t, bindingToolNames(bound.Tools.Signatures), definition.Name)
		}
		_, available := session.Lookup(definition.Name)
		require.Equal(t, selected, available)
	}
}
