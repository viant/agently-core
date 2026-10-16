package agent

import (
	"context"
	"encoding/json"
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
