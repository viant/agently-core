package tool

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	memory "github.com/viant/agently-core/runtime/requestctx"
	"testing"
)

type legacyMetadataRegistry struct {
	captureRegistry
	definition llm.ToolDefinition
}

func (r *legacyMetadataRegistry) Definitions() []llm.ToolDefinition {
	return []llm.ToolDefinition{r.definition}
}

type contextualMetadataRegistry struct {
	legacyMetadataRegistry
	conversation string
}

func (r *contextualMetadataRegistry) DefinitionMetadataWithContext(ctx context.Context) []llm.ToolDefinition {
	r.conversation = memory.ConversationIDFromContext(ctx)
	return []llm.ToolDefinition{{Name: "example:measure", Description: "Synthetic measurement", SourceMCPServer: "example"}}
}
func TestScopedMetadataProjectionForwardsConversationAndSupportsLegacyRegistry(t *testing.T) {
	inner := &contextualMetadataRegistry{}
	scoped := WithConversation(inner, "example-chat")
	lister, ok := scoped.(ContextMetadataDefinitionLister)
	require.True(t, ok)
	require.Len(t, lister.DefinitionMetadataWithContext(context.Background()), 1)
	require.Equal(t, "example-chat", inner.conversation)
	legacy := &legacyMetadataRegistry{definition: llm.ToolDefinition{Name: "example:measure", Parameters: map[string]interface{}{"type": "object"}, Required: []string{"scope"}, OutputSchema: map[string]interface{}{"type": "object"}}}
	fallback := WithConversation(legacy, "example-chat").(ContextMetadataDefinitionLister).DefinitionMetadataWithContext(context.Background())
	require.Len(t, fallback, 1)
	require.Nil(t, fallback[0].Parameters)
	require.Nil(t, fallback[0].OutputSchema)
	require.Nil(t, fallback[0].Required)
	require.NotNil(t, legacy.definition.Parameters, "fallback projection does not mutate older provider definitions")
	require.NotNil(t, legacy.definition.OutputSchema)
	require.NotEmpty(t, legacy.definition.Required)
}
