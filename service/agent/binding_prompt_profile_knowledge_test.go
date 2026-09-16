package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	intake "github.com/viant/agently-core/protocol/intake"
	intakesvc "github.com/viant/agently-core/service/intake"
	embSchema "github.com/viant/embedius/schema"
)

func TestApplyProfileKnowledge_SelectedProfileOnlyAndMinScore(t *testing.T) {
	registry := &staticRegistry{result: `{
  "documents": [
    {"page_content":"Household ID uses address-based identity.","metadata":{"path":"workspace://knowledge/household-id.md","rootId":"viant-product-knowledge"},"score":0.82},
    {"page_content":"Unrelated low confidence content.","metadata":{"path":"workspace://knowledge/unrelated.md","rootId":"viant-product-knowledge"},"score":0.41}
  ]
}`}
	service := &Service{registry: registry}
	threshold := 0.55
	profile := &intake.Profile{
		ID: "viant_product_knowledge",
		Knowledge: []intake.KnowledgeMatch{{
			RootIDs:      []string{"viant-product-knowledge"},
			MaxDocuments: 4,
			MinScore:     &threshold,
			LimitBytes:   16000,
			Exclude:      []string{"**/manifest.json"},
			Required:     true,
		}},
	}
	b := &binding.Binding{}
	input := &QueryInput{Query: "What is Viant Household ID?"}

	require.NoError(t, service.applyProfileKnowledge(context.Background(), input, b, profile))
	require.Equal(t, 1, registry.calls)
	require.Equal(t, "resources:match", registry.lastName)
	require.Equal(t, []string{"viant-product-knowledge"}, registry.lastArgs["rootIds"])
	require.Equal(t, 4, registry.lastArgs["maxDocuments"])
	require.Equal(t, 16000, registry.lastArgs["limitBytes"])
	require.Len(t, b.SystemDocuments.Items, 2)
	require.Equal(t, "workspace://knowledge/household-id.md", b.SystemDocuments.Items[0].SourceURI)
	require.Equal(t, "profile_knowledge", b.SystemDocuments.Items[0].Metadata["kind"])
	require.Equal(t, "viant_product_knowledge", b.SystemDocuments.Items[0].Metadata["profile"])
}

func TestApplyProfileKnowledge_NoProfileKnowledgeDoesNotMatch(t *testing.T) {
	registry := &staticRegistry{result: `{}`}
	service := &Service{registry: registry}
	require.NoError(t, service.applyProfileKnowledge(context.Background(), &QueryInput{Query: "campaign performance"}, &binding.Binding{}, &intake.Profile{ID: "performance_analysis"}))
	require.Zero(t, registry.calls)
}

func TestApplyProfileKnowledge_RequiredFailureFailsClosed(t *testing.T) {
	registry := &failingProfileKnowledgeRegistry{staticRegistry: staticRegistry{}}
	service := &Service{registry: registry}
	profile := &intake.Profile{ID: "viant_product_knowledge", Knowledge: []intake.KnowledgeMatch{{RootIDs: []string{"viant-product-knowledge"}, Required: true}}}
	err := service.applyProfileKnowledge(context.Background(), &QueryInput{Query: "What is Viant?"}, &binding.Binding{}, profile)
	require.ErrorContains(t, err, "knowledge[0] match failed")
}

func TestSelectedPromptProfile_MessageGateDoesNotDisableKnowledgeResolution(t *testing.T) {
	agentConfig := &agentmdl.Agent{Prompts: agentmdl.PromptAccess{InjectSelectedProfile: boolPtr(false)}}
	require.False(t, agentConfig.Prompts.AllowsSelectedProfileInjection())
}

func TestProfileExecution_DisablesPlannerAndDelegation(t *testing.T) {
	profile := &intake.Profile{ID: "viant_product_knowledge", Execution: &intake.Execution{DisablePlanner: true, DisableDelegation: true}}
	tc := &intakesvc.Context{
		Routing: intakesvc.RoutingContext{Mode: intakesvc.ModePlanner},
		Planner: intakesvc.PlannerContext{Trigger: "open_ended", AgentID: "steward_planner"},
	}
	input := &QueryInput{
		PromptProfileId: profile.ID,
		Context:         map[string]interface{}{intakesvc.ContextKey: tc},
	}
	service := &Service{}
	ctx := withSelectedPromptProfile(context.Background(), profile)
	require.NoError(t, service.maybeRunPlannerPass(ctx, input))
	require.Equal(t, intakesvc.ModeRoute, tc.Routing.Mode)
	require.Empty(t, tc.Planner.Trigger)
	require.Empty(t, tc.Planner.AgentID)

	b := &binding.Binding{Tools: binding.Tools{Signatures: []*llm.ToolDefinition{
		{Name: "llm/agents:start"},
		{Name: "llm_agents-status"},
		{Name: "orchestration/plan:create"},
		{Name: "resources:read"},
	}}}
	applyProfileExecutionRestrictions(b, profile)
	require.Len(t, b.Tools.Signatures, 1)
	require.Equal(t, "resources:read", b.Tools.Signatures[0].Name)
}

func TestProfileKnowledgeCanonicalSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "article.md")
	require.NoError(t, os.WriteFile(path, []byte("---\ntitle: \"Household ID\"\nsourceUrl: \"https://help.viantinc.com/hc/en-us/articles/123\"\n---\nbody\n"), 0600))
	service := &Service{fs: afs.New()}
	title, sourceURL := service.profileKnowledgeCanonicalSource(context.Background(), path)
	require.Equal(t, "Household ID", title)
	require.Equal(t, "https://help.viantinc.com/hc/en-us/articles/123", sourceURL)
}

func TestProfileKnowledgeFullDocumentCanonicalLinkAndBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "article.md")
	body := "---\ntitle: \"Household ID\"\nsourceUrl: \"https://help.viantinc.com/hc/en-us/articles/123\"\n---\n\nFull article with [legacy link](https://help.adelphic.com/old).\n" + strings.Repeat("detail ", 100)
	require.NoError(t, os.WriteFile(path, []byte(body), 0600))
	registry := &staticRegistry{result: `{"documents":[{"page_content":"matched fragment","metadata":{"path":` + strconv.Quote(path) + `},"score":0.82}]}`}
	service := &Service{registry: registry, fs: afs.New()}
	profile := &intake.Profile{ID: "product", Knowledge: []intake.KnowledgeMatch{{
		RootIDs:             []string{"product"},
		DocumentMode:        "full",
		CanonicalSourceOnly: true,
		MaxTotalBytes:       500,
		Required:            true,
	}}}
	b := &binding.Binding{}
	require.NoError(t, service.applyProfileKnowledge(context.Background(), &QueryInput{Query: "household"}, b, profile))
	require.Len(t, b.SystemDocuments.Items, 1)
	content := b.SystemDocuments.Items[0].PageContent
	require.LessOrEqual(t, len(content), 500)
	require.Contains(t, content, "Full article")
	require.NotContains(t, content, "help.adelphic.com")
	require.Contains(t, content, "Canonical source article: [Household ID](https://help.viantinc.com/hc/en-us/articles/123)")
}

func TestProfileKnowledgeDocumentModeExplicit(t *testing.T) {
	require.False(t, profileKnowledgeUseFullDocument("explicit", "What is Household ID?"))
	require.True(t, profileKnowledgeUseFullDocument("explicit", "Show the full article about Household ID"))
	require.True(t, profileKnowledgeUseFullDocument("full", "What is Household ID?"))
	require.False(t, profileKnowledgeUseFullDocument("fragment", "Show the full article"))
}

func TestProfileKnowledgeWithNeighbors(t *testing.T) {
	docs := []embSchema.Document{
		{PageContent: "middle", Metadata: map[string]interface{}{"path": "article.md", "fragmentId": "article.md:10-20", "start": float64(10)}},
		{PageContent: "after", Metadata: map[string]interface{}{"path": "article.md", "fragmentId": "article.md:20-30", "start": float64(20)}},
		{PageContent: "before", Metadata: map[string]interface{}{"path": "article.md", "fragmentId": "article.md:0-10", "start": float64(0)}},
		{PageContent: "other", Metadata: map[string]interface{}{"path": "other.md", "fragmentId": "other.md:0-10", "start": float64(0)}},
	}
	got := profileKnowledgeWithNeighbors(docs, docs[0], 1, 1)
	require.Equal(t, "before\n\nmiddle\n\nafter", got)
}

func TestProfileKnowledgeQueriesAndInterleave(t *testing.T) {
	queries := profileKnowledgeQueries("Explain and compare Viant Household ID, Direct Access, and Foot Traffic Report.", "multi", 4)
	require.Equal(t, []string{"Viant Household ID", "Direct Access", "Foot Traffic Report"}, queries)

	groups := [][]embSchema.Document{
		{{PageContent: "a1"}, {PageContent: "a2"}},
		{{PageContent: "b1"}, {PageContent: "b2"}},
		{{PageContent: "c1"}},
	}
	got := interleaveProfileKnowledgeGroups(groups)
	require.Equal(t, []string{"a1", "b1", "c1", "a2", "b2"}, []string{got[0].PageContent, got[1].PageContent, got[2].PageContent, got[3].PageContent, got[4].PageContent})
	require.Equal(t, []int{6, 5, 5}, []int{
		profileKnowledgeFragmentBudget(16, 3, 0),
		profileKnowledgeFragmentBudget(16, 3, 1),
		profileKnowledgeFragmentBudget(16, 3, 2),
	})
}

func TestProfileKnowledgeCatalogQueriesExpandsShorthandFromManifest(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.json")
	require.NoError(t, os.WriteFile(manifestPath, []byte(`{"articles":[{"title":"Viant Household ID Technology Overview"},{"title":"Household Conversions"},{"title":"Forecasting"}]}`), 0o600))

	queries, err := profileKnowledgeCatalogQueries("how household works", []string{"how household works"}, &intake.QueryCatalog{Path: manifestPath, MaxMatches: 1}, 4)
	require.NoError(t, err)
	require.Equal(t, []string{
		"how household works",
		"Viant Household ID Technology Overview. User question: how household works",
	}, queries)
}

type failingProfileKnowledgeRegistry struct{ staticRegistry }

func (r *failingProfileKnowledgeRegistry) Execute(context.Context, string, map[string]interface{}) (string, error) {
	return "", errors.New("index unavailable")
}
