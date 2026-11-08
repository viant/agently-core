package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	primitive "github.com/viant/agently-core/protocol/primitive"
	toolctx "github.com/viant/agently-core/protocol/tool"
	bundle "github.com/viant/agently-core/protocol/tool/bundle"
	memory "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/toolselection"
	"github.com/viant/agently-core/service/browsermcp"
)

type projectedSchemaRegistry struct {
	fakeRegistry
	metadataReads int
	fullLists     int
	hydrated      []string
	revoked       string
}

func (r *projectedSchemaRegistry) Definitions() []llm.ToolDefinition {
	r.fullLists++
	return r.fakeRegistry.Definitions()
}
func (r *projectedSchemaRegistry) DefinitionMetadataWithContext(ctx context.Context) []llm.ToolDefinition {
	r.metadataReads++
	var metadata []llm.ToolDefinition
	for _, definition := range r.defs {
		if definition.DiscoveryNamespace == "private" && authctx.EffectiveUserID(ctx) != "alice" {
			continue
		}
		definition.Parameters, definition.OutputSchema, definition.Required = nil, nil, nil
		metadata = append(metadata, definition)
	}
	return metadata
}
func (r *projectedSchemaRegistry) MatchDefinitionWithContextResult(ctx context.Context, name string) ([]*llm.ToolDefinition, error) {
	requireExact := !strings.ContainsAny(name, "*?[]")
	if !requireExact {
		return nil, fmt.Errorf("fixture requires selected schema lookup")
	}
	if name == r.revoked {
		return nil, nil
	}
	found := r.fakeRegistry.MatchDefinition(name)
	for _, definition := range found {
		if definition.DiscoveryNamespace == "private" && authctx.EffectiveUserID(ctx) != "alice" {
			return nil, fmt.Errorf("fixture denied selected definition")
		}
		r.hydrated = append(r.hydrated, definition.Name)
	}
	return found, nil
}
func projectionFixture() (*Service, *projectedSchemaRegistry, *QueryInput, context.Context, []string) {
	definitions := make([]llm.ToolDefinition, 519)
	for i := range definitions {
		namespace := "private"
		if i < 100 {
			namespace = "public"
		}
		definitions[i] = llm.ToolDefinition{Name: fmt.Sprintf("fixture:tool_%03d", i), Description: fmt.Sprintf("Synthetic capacity measurement operation %03d", i), DiscoveryNamespace: namespace, SourceMCPServer: "fixture", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"scope": map[string]interface{}{"type": "string"}, "page": map[string]interface{}{"type": "integer"}}}, Required: []string{"scope"}, OutputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"rows": map[string]interface{}{"type": "array"}}, "required": []string{"rows"}}}
	}
	registry := &projectedSchemaRegistry{fakeRegistry: fakeRegistry{defs: definitions}}
	service := &Service{registry: registry, toolBundles: func(context.Context) ([]*bundle.Bundle, error) { return nil, nil }}
	input := &QueryInput{Agent: &agentmdl.Agent{Tool: agentmdl.Tool{Items: []*llm.Tool{{Name: "fixture:*", Tier: llm.ToolTierOnDemand}}}}}
	ctx := authctx.WithBearer(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "fixture-alice-credential")
	ctx = memory.WithTurnMeta(ctx, memory.TurnMeta{ConversationID: "projection-chat", TurnID: "projection-turn"})
	ctx = toolselection.WithState(ctx, defsToTools(definitions))
	names := []string{"fixture:tool_001", "fixture:tool_002", "fixture:tool_100", "fixture:tool_200", "fixture:tool_500", "fixture:tool_518"}
	state, _ := toolselection.FromContext(ctx)
	err := state.Activate(names)
	if err != nil {
		panic(err)
	}
	return service, registry, input, ctx, names
}
func TestMetadataCandidatesHydrateOnlyCurrentSelectedSchemas(t *testing.T) {
	service, registry, input, ctx, names := projectionFixture()
	candidates, err := service.discoverableToolDefinitions(ctx, input)
	require.NoError(t, err)
	require.Len(t, candidates, 519)
	for _, candidate := range candidates {
		require.Nil(t, candidate.Parameters)
		require.Nil(t, candidate.OutputSchema)
		require.Empty(t, candidate.Required)
	}
	state, _ := toolselection.FromContext(ctx)
	legacy := *service
	legacy.registry = &registry.fakeRegistry
	fullCandidates, err := legacy.discoverableToolDefinitions(ctx, input)
	require.NoError(t, err)
	state.SetCandidates(defsToTools(fullCandidates))
	canonicalNames := make([]string, len(names))
	for i, name := range names {
		canonicalNames[i] = strings.Replace(name, "fixture:", "fixture-", 1)
	}
	require.NoError(t, state.Activate(canonicalNames))
	first, err := state.ListPage(ctx, primitive.CatalogQuery{Search: "capacity measurement", Limit: 5}, 0)
	require.NoError(t, err)
	state.SetCandidates(defsToTools(candidates))
	second, err := state.ListPage(ctx, primitive.CatalogQuery{Search: "capacity measurement", Limit: 5}, 0)
	require.NoError(t, err)
	require.Equal(t, first, second, "metadata projection preserves ranking and cursor identity")
	next, err := state.ListPage(ctx, primitive.CatalogQuery{Search: "capacity measurement", Limit: 5, Cursor: first.NextCursor}, 0)
	require.NoError(t, err)
	require.NotEmpty(t, next.Items)
	signatures, err := service.buildToolSignatures(ctx, input)
	require.NoError(t, err)
	require.Len(t, signatures, 6)
	require.Len(t, registry.hydrated, 6, "only final selected definitions load schemas")
	require.Zero(t, registry.fullLists)
	require.Greater(t, registry.metadataReads, 0)
	for _, definition := range signatures {
		require.Len(t, definition.Parameters["properties"], 2)
		require.Equal(t, []string{"scope"}, definition.Required)
		require.ElementsMatch(t, []string{"rows"}, definition.OutputSchema["required"])
		require.Contains(t, names, strings.Replace(definition.Name, "fixture-", "fixture:", 1))
	}
	registry.revoked = "fixture-tool_518"
	_, err = service.buildToolSignatures(ctx, input)
	require.ErrorContains(t, err, "required tool unavailable")
}
func TestMetadataCandidateScopeDoesNotExposeAnotherCallerOrConversation(t *testing.T) {
	service, registry, input, ctx, _ := projectionFixture()
	for _, change := range []func(context.Context) context.Context{
		func(c context.Context) context.Context {
			return authctx.WithUserInfo(c, &authctx.UserInfo{Subject: "bob"})
		},
		func(c context.Context) context.Context { return memory.WithConversationID(c, "another-chat") },
		func(c context.Context) context.Context { return authctx.WithProvider(c, "another-provider") },
	} {
		changed := change(ctx)
		require.Nil(t, func() *toolselection.State { state, _ := toolselection.FromContext(changed); return state }())
		tools, err := service.buildToolSignatures(changed, input)
		require.NoError(t, err)
		require.Empty(t, tools)
	}
	require.Empty(t, registry.hydrated)
}

func TestMetadataBrowserCandidatesHydrateThroughTheirOwningCatalog(t *testing.T) {
	service, registry, input, ctx, _ := projectionFixture()
	registry.defs = nil
	definition := llm.ToolDefinition{Name: "device-measure", Description: "Synthetic browser measurement", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"scope": map[string]interface{}{"type": "string"}}}, Required: []string{"scope"}, OutputSchema: map[string]interface{}{"type": "object"}}
	ctx = browsermcp.WithDefinitions(ctx, map[string]llm.ToolDefinition{definition.Name: definition, "device-status": {Name: "device-status", Description: "Synthetic status"}})
	input.Agent.Tool.Items = []*llm.Tool{{Name: "device-*", Tier: llm.ToolTierOnDemand}}
	candidates, err := service.discoverableToolDefinitions(ctx, input)
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	for _, candidate := range candidates {
		require.Nil(t, candidate.Parameters)
		require.Nil(t, candidate.OutputSchema)
	}
	state, _ := toolselection.FromContext(ctx)
	state.SetCandidates(defsToTools(candidates))
	require.NoError(t, state.Activate([]string{"device-measure", "device-status"}))
	for i := 0; i < 2; i++ {
		signatures, err := service.buildToolSignatures(ctx, input)
		require.NoError(t, err)
		require.Len(t, signatures, 2)
		for _, selected := range signatures {
			if selected.Name == definition.Name {
				require.Equal(t, definition.Parameters["properties"], selected.Parameters["properties"])
				require.Equal(t, definition.Required, selected.Required)
				require.Equal(t, definition.OutputSchema["type"], selected.OutputSchema["type"])
			}
		}
	}
	require.Empty(t, registry.hydrated, "browser schemas never probe the backend registry")
}

func TestMetadataScopedLegacyProviderKeepsSelectedArguments(t *testing.T) {
	service, registry, input, ctx, names := projectionFixture()
	service.registry = toolctx.WithConversation(&registry.fakeRegistry, "projection-chat")
	candidates, err := service.discoverableToolDefinitions(ctx, input)
	require.NoError(t, err)
	state, _ := toolselection.FromContext(ctx)
	state.SetCandidates(defsToTools(candidates))
	var selectedNames []string
	for _, name := range names {
		selectedNames = append(selectedNames, strings.Replace(name, "fixture:", "fixture-", 1))
	}
	require.NoError(t, state.Activate(selectedNames))
	signatures, err := service.buildToolSignatures(ctx, input)
	require.NoError(t, err)
	require.Len(t, signatures, 6)
	for _, selected := range signatures {
		require.Len(t, selected.Parameters["properties"], 2)
		require.Equal(t, []string{"scope"}, selected.Required)
		require.NotEmpty(t, selected.OutputSchema)
	}
	require.NotNil(t, registry.defs[0].Parameters)
}
