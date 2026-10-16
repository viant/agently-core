package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/primitive"
	adapter "github.com/viant/agently-core/protocol/tool/adapter/mcp"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/toolselection"
)

func TestListSelectsOnlyReturnedPageWithoutExtraActivation(t *testing.T) {
	ctx := requestctx.WithTurnMeta(auth.WithUserInfo(context.Background(), &auth.UserInfo{Subject: "alice"}), requestctx.TurnMeta{ConversationID: "conversation", TurnID: "turn"})
	ctx = toolselection.WithState(ctx, []llm.Tool{{Definition: llm.ToolDefinition{Name: "north-Alpha", Description: "Sales"}}, {Definition: llm.ToolDefinition{Name: "north-Beta", Description: "Sales"}}, {Definition: llm.ToolDefinition{Name: "south-Gamma", Description: "Inventory"}}})
	list, _ := New().Method("list")
	input := &ListInput{CatalogQuery: primitive.CatalogQuery{Namespace: "north", NameTerms: []string{"alpha", "beta"}}, Limit: 1}
	first := &ListOutput{}
	require.NoError(t, list(ctx, input, first))
	state, _ := toolselection.FromContext(ctx)
	require.Equal(t, []string{"north-Alpha"}, state.SelectedNames(), "select the returned page, not all matching tools")
	require.NoError(t, list(ctx, &ListInput{Query: "no-match"}, &ListOutput{}))
	require.Equal(t, []string{"north-Alpha"}, state.SelectedNames())
	bob := auth.WithUserInfo(ctx, &auth.UserInfo{Subject: "bob"})
	require.Error(t, list(bob, &ListInput{Query: "Beta"}, &ListOutput{}))
	require.Equal(t, []string{"north-Alpha"}, state.SelectedNames())
	input.Cursor = first.NextCursor
	require.NoError(t, list(ctx, input, &ListOutput{}))
	require.Equal(t, []string{"north-Alpha", "north-Beta"}, state.SelectedNames())
	require.NotContains(t, state.SelectedNames(), "south-Gamma")
}

func TestToolsListSharedFiltersPaginationAndLegacyInput(t *testing.T) {
	ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{ConversationID: "conversation", TurnID: "turn"})
	ctx = toolselection.WithState(ctx, []llm.Tool{
		{Definition: llm.ToolDefinition{Name: "north-Alpha", Description: "Sales growth .* literal", Parameters: map[string]interface{}{"private_schema": true}}},
		{Definition: llm.ToolDefinition{Name: "north-Beta", Description: "Sales forecast"}},
		{Definition: llm.ToolDefinition{Name: "south-Gamma", Description: "Inventory"}},
	})
	list, _ := New().Method("list")
	input := &ListInput{}
	require.NoError(t, json.Unmarshal([]byte(`{"namespace":"north","nameTerms":["ALPHA","beta"],"descriptionTerms":["SALES","missing"],"keywords":["growth"],"limit":1}`), input))
	out := &ListOutput{}
	require.NoError(t, list(ctx, input, out))
	require.Len(t, out.Tools, 1)
	require.Equal(t, "north-Alpha", out.Tools[0].Name)
	require.True(t, out.Complete)
	input = &ListInput{CatalogQuery: primitive.CatalogQuery{DescriptionTerms: []string{".*"}}}
	require.NoError(t, list(ctx, input, out))
	require.Len(t, out.Tools, 1)
	input = &ListInput{CatalogQuery: primitive.CatalogQuery{Namespace: "north", NameTerms: []string{"alpha", "BETA"}}, Limit: 1}
	first := &ListOutput{}
	require.NoError(t, list(ctx, input, first))
	require.Equal(t, 2, first.Total)
	require.True(t, first.HasMore)
	require.NotEmpty(t, first.NextCursor)
	input.Cursor = first.NextCursor
	require.NoError(t, list(ctx, input, out))
	require.Equal(t, "north-Beta", out.Tools[0].Name)
	require.True(t, out.Complete)
	input.NameTerms = []string{"alpha"}
	require.ErrorIs(t, list(ctx, input, &ListOutput{}), primitive.ErrCatalogCursor)
	legacy := &ListOutput{}
	require.NoError(t, list(ctx, &ListInput{Query: "SaLeS", Offset: 1, Limit: 1}, legacy))
	require.Equal(t, "north-Beta", legacy.Tools[0].Name)
	require.False(t, legacy.HasMore)
	require.Error(t, list(ctx, &ListInput{Offset: 1, CatalogQuery: primitive.CatalogQuery{Cursor: first.NextCursor}}, out))
	encoded, err := json.Marshal(legacy)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private_schema")
	for _, tool := range adapter.FromService(New()) {
		if tool.Name == "list" {
			for _, name := range []string{"query", "offset", "limit", "namespace", "nameTerms", "descriptionTerms", "keywords", "cursor"} {
				require.Contains(t, tool.InputSchema.Properties, name)
			}
		}
	}
}
