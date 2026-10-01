package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/binding"
	intent "github.com/viant/agently-core/protocol/intent"
)

func TestAppendKnowledgeMatchesUsesOriginalQueryAndGenericMapper(t *testing.T) {
	registry := &staticRegistry{result: `{"documents":[{"page_content":"matched fragment","metadata":{"path":"workspace://knowledge/document.md","document.title":"Document","source.url":"https://example.test/document","rootId":"docs"},"score":0.8}]}`}
	service := &Service{registry: registry}
	b := &binding.Binding{}
	minScore := 0.7
	err := service.appendKnowledgeMatches(context.Background(), &QueryInput{Query: "compare two settings"}, b, []intent.KnowledgeMatch{{RootIDs: []string{"docs"}, MinScore: &minScore, MaxDocuments: 1, Required: true}}, "profile-a")
	require.NoError(t, err)
	require.Equal(t, "compare two settings", registry.lastArgs["query"])
	require.Len(t, b.SystemDocuments.Items, 1)
	item := b.SystemDocuments.Items[0]
	require.Equal(t, "Document", item.Title)
	require.Equal(t, "https://example.test/document", item.Metadata["source.url"])
	require.Equal(t, "profile-a", item.Metadata["intent.profile"])
}

func TestApplyProfileExecutionRestrictions_DisableToolsClearsSurface(t *testing.T) {
	b := &binding.Binding{}
	b.Tools.Signatures = []*llm.ToolDefinition{
		{Name: "resources-match"},
		{Name: "steward-SpoParentOrgCube"},
	}
	applyProfileExecutionRestrictions(b, &intent.Profile{
		Execution: &intent.Execution{DisableTools: true},
	})
	require.Empty(t, b.Tools.Signatures)
}

func TestAppendKnowledgeMatchesRequiredFailure(t *testing.T) {
	service := &Service{registry: &failingProfileKnowledgeRegistry{}}
	err := service.appendKnowledgeMatches(context.Background(), &QueryInput{Query: "question"}, &binding.Binding{}, []intent.KnowledgeMatch{{RootIDs: []string{"docs"}, Required: true}}, "profile-a")
	require.ErrorContains(t, err, "knowledge[0] match failed")
}

type failingProfileKnowledgeRegistry struct{ staticRegistry }

func (*failingProfileKnowledgeRegistry) Execute(context.Context, string, map[string]interface{}) (string, error) {
	return "", errors.New("unavailable")
}
