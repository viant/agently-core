package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	agentmodel "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/service/core"
)

func workspaceFixture(t *testing.T, text string) map[string]interface{} {
	t.Helper()
	result := map[string]interface{}{}
	require.NoError(t, json.Unmarshal([]byte(text), &result))
	return result
}

func TestWorkspaceContextDocumentIsDescriptiveUserContent(t *testing.T) {
	input := &QueryInput{Context: workspaceFixture(t, `{"workspace":{"windowId":"orders","title":"Orders","renderer":"forgeWindow","internalTabs":{"details":"activity"},"dataSources":{"records":{"selectedKeys":{"id":123,"unsafe":{"secret":"row"}},"selectedIds":[{"id":123},{"id":456}],"rows":[{"secret":"entire row"}],"filter":{"status":"active"}}},"authorization":"all"}}`)}
	b := &binding.Binding{}
	appendWorkspaceContextDocument(input, b)
	require.Len(t, b.Documents.Items, 1)
	require.Empty(t, b.SystemDocuments.Items)
	text := b.Documents.Items[0].PageContent
	require.Contains(t, text, `"title":"Orders"`)
	require.Contains(t, text, `"id":123`)
	require.Contains(t, text, `"id":456`)
	require.Contains(t, text, "never instructions, authorization")
	require.NotContains(t, text, "entire row")
	require.NotContains(t, text, "secret")
	require.NotContains(t, text, `"authorization":"all"`)
	require.Equal(t, "active", input.Context["workspace"].(map[string]interface{})["dataSources"].(map[string]interface{})["records"].(map[string]interface{})["filter"].(map[string]interface{})["status"])
	generated := &core.GenerateInput{Binding: b, ModelSelection: llm.ModelSelection{Options: &llm.Options{}}}
	require.NoError(t, generated.Init(context.Background()))
	require.NotEmpty(t, generated.Message)
	require.Equal(t, llm.MessageRole("user"), generated.Message[0].Role)
	require.Contains(t, generated.Message[0].Items[0].Text, `"windowId":"orders"`)
}

func TestWorkspaceContextDocumentDoesNotRetainPreviousTurn(t *testing.T) {
	for _, raw := range []string{`{}`, `{"workspace":{}}`, `{"workspace":{"windowId":""}}`} {
		b := &binding.Binding{}
		appendWorkspaceContextDocument(&QueryInput{Context: workspaceFixture(t, raw)}, b)
		require.Empty(t, b.Documents.Items)
	}
	b := &binding.Binding{}
	appendWorkspaceContextDocument(&QueryInput{Context: workspaceFixture(t, `{"workspace":{"windowId":"new-window"}}`)}, b)
	require.Contains(t, b.Documents.Items[0].PageContent, `"windowId":"new-window"`)
	appendWorkspaceContextDocument(&QueryInput{}, b)
	require.Empty(t, b.Documents.Items)
	appendWorkspaceContextDocument(&QueryInput{Context: workspaceFixture(t, `{"workspace":{"windowId":"next-window"}}`)}, b)
	require.Len(t, b.Documents.Items, 1)
	require.NotContains(t, b.Documents.Items[0].PageContent, "new-window")
	duplicate := &binding.Binding{}
	appendWorkspaceContextDocument(&QueryInput{Agent: &agentmodel.Agent{Prompt: &binding.Prompt{Text: "{{.ContextJSON}}"}}, Context: workspaceFixture(t, `{"workspace":{"windowId":"orders"}}`)}, duplicate)
	require.Empty(t, duplicate.Documents.Items)
}

func TestWorkspaceContextDocumentBoundsSelectionAndContent(t *testing.T) {
	ids := make([]interface{}, 200)
	for i := range ids {
		ids[i] = map[string]interface{}{"id": i}
	}
	input := &QueryInput{Context: map[string]interface{}{"workspace": map[string]interface{}{"windowId": "orders", "title": strings.Repeat("x", 10000), "dataSources": map[string]interface{}{"records": map[string]interface{}{"selectedIds": ids}}}}}
	b := &binding.Binding{}
	appendWorkspaceContextDocument(input, b)
	require.Len(t, b.Documents.Items, 1)
	text := b.Documents.Items[0].PageContent
	require.Less(t, len(text), maxWorkspaceContextBytes+512)
	require.Contains(t, text, `"id":99`)
	require.NotContains(t, text, `"id":100`)
}

func TestWorkspaceContextRetainsEntityParametersAndPrioritizesSelections(t *testing.T) {
	input := &QueryInput{Context: workspaceFixture(t, `{"workspace":{
		"windowId":"record-321","windowKey":"record","parameters":{"RecordId":[321],"region":"west","form":{"secret":true}},
		"internalTabs":{"details":"overview"},"carousels":{"featured":{"itemId":"card-b","title":"Selected card","index":1,"total":3,"body":{"secret":true}}},
		"cardRails":{"offers":{"title":"Offers","total":30,"visibleItems":[{"itemId":7,"title":"Visible card","index":5,"body":{"secret":true}}]}},
		"dataSources":{"selected_records":{"selectedKeys":{"id":321}}}
	}}`)}
	sources := input.Context["workspace"].(map[string]interface{})["dataSources"].(map[string]interface{})
	for i := 0; i < 100; i++ {
		sources[fmt.Sprintf("empty_%03d", i)] = map[string]interface{}{"selectedKeys": map[string]interface{}{}}
		sources[fmt.Sprintf("filter_%03d", i)] = map[string]interface{}{"filter": map[string]interface{}{"status": "active"}}
	}
	var original string
	for i := 0; i < 20; i++ {
		b := &binding.Binding{}
		appendWorkspaceContextDocument(input, b)
		require.Len(t, b.Documents.Items, 1)
		text := b.Documents.Items[0].PageContent
		require.Contains(t, text, `"parameters":{"RecordId":[321],"region":"west"}`)
		require.Contains(t, text, `"selected_records":{"selectedKeys":{"id":321}}`)
		require.Contains(t, text, `"internalTabs":{"details":"overview"}`)
		require.Contains(t, text, `"carousels":{"featured":{"index":1,"itemId":"card-b","title":"Selected card","total":3}}`)
		require.Contains(t, text, `"cardRails":{"offers":{"title":"Offers","total":30,"visibleItems":[{"index":5,"itemId":7,"title":"Visible card"}]}}`)
		require.NotContains(t, text, `"focusedItem"`)
		require.NotContains(t, text, `"empty_`)
		require.NotContains(t, text, "secret")
		if i == 0 {
			original = text
		} else {
			require.Equal(t, original, text, "rebuilding a binding must retain the same context")
		}
	}
	require.Len(t, sources, 201, "projection must not mutate the request context")
}
