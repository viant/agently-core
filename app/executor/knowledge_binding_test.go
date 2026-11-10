package executor

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	base "github.com/viant/agently-core/genai/embedder/provider/base"
	agent "github.com/viant/agently-core/protocol/agent"
	resources "github.com/viant/agently-core/protocol/tool/service/resources"
	"github.com/viant/agently-core/runtime/requestctx"
	augmenter "github.com/viant/agently-core/service/augmenter"
	resource "github.com/viant/agently-core/service/resource"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
	"github.com/viant/authz"
	embopt "github.com/viant/embedius/matching/option"
	"github.com/viant/forge/backend/artifactcohort"
	primitive "github.com/viant/forge/backend/provider/contract"
	identity "github.com/viant/forge/backend/resource"
)

type builderKnowledgeCaller struct {
	publisher *artifactcohort.Publisher
	discovers atomic.Int64
	fetches   atomic.Int64
}

func (s *builderKnowledgeCaller) Discover(context.Context) (artifactcohort.Manifest, error) {
	s.discovers.Add(1)
	return s.publisher.Snapshot()
}
func (s *builderKnowledgeCaller) Fetch(_ context.Context, input artifactcohort.FetchInput) (*artifactcohort.Artifact, error) {
	s.fetches.Add(1)
	return s.publisher.Get(input.Root)
}

type builderKnowledgeFacts struct{}

func (builderKnowledgeFacts) Resolve(context.Context) (authz.Facts, error) {
	return authz.Facts{Subject: "reader", Issuer: "https://identity.example.test", Tenant: "tenant", Roles: []string{"reader"}, ValidUntil: time.Now().Add(time.Hour)}, nil
}

type builderKnowledgeEmbedder struct {
	documents atomic.Int64
	queries   atomic.Int64
}

func (e *builderKnowledgeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, int, error) {
	result := make([][]float32, len(texts))
	for i, text := range texts {
		if text == "documented guidance" {
			e.queries.Add(1)
		} else {
			e.documents.Add(1)
		}
		result[i] = []float32{1, 0, 0}
	}
	return result, 0, nil
}

type builderKnowledgeEmbedderFinder struct{ embedder *builderKnowledgeEmbedder }

func (f builderKnowledgeEmbedderFinder) Find(context.Context, string) (base.Embedder, error) {
	return f.embedder, nil
}
func (builderKnowledgeEmbedderFinder) Ids() []string { return []string{"synthetic"} }

type builderKnowledgeAgentFinder struct{ value *agent.Agent }

func (f builderKnowledgeAgentFinder) Find(context.Context, string) (*agent.Agent, error) {
	return f.value, nil
}
func TestBuilderConnectsPublishedKnowledgeToExistingMatcher(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTLY_RUNTIME_ROOT", root)
	t.Setenv("AGENTLY_DB_DRIVER", "sqlite")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", filepath.Join(root, "runtime.db"))
	t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")
	configured := &agent.Agent{Identity: agent.Identity{ID: "reader"}, Resources: []*agent.Resource{{ID: "docs", URI: "knowledge://example/guide", Role: "system", Match: &embopt.Options{Inclusions: []string{"**/guides/"}}}}}
	embedder := &builderKnowledgeEmbedder{}
	aug := augmenter.New(builderKnowledgeEmbedderFinder{embedder}, augmenter.WithIndexPathTemplate(filepath.Join(root, "index")), augmenter.WithIndexAsync(false))
	actor, verify := resource.PublicReadActors()
	runtime, err := NewBuilder().WithStore(fsstore.New(root)).WithKnowledgeStore(fsstore.NewKnowledgeStore(root)).WithStateStore(fsstore.NewStateStore(root)).WithModelFinder(noWindowModelFinder{}).WithAgentFinder(builderKnowledgeAgentFinder{configured}).WithAugmenter(aug).WithPrimitiveAuthority(actor, verify, "local-workspace").WithPrimitiveGatewayIdentity("host").WithSkipRegistryInitialize(true).Build(context.Background())
	require.NoError(t, err)
	defer runtime.Close(context.Background())
	value := primitive.KnowledgeEnvelope{SchemaVersion: 1, Name: "Guide", Files: map[string]string{"guides/example.md": "---\ntitle: Example\nsourceUrl: https://example.test/guide\n---\n\nDocumented example guidance.\n", "guides/sibling.md": "Other guidance in the selected directory.\n", "other.md": "Excluded guidance outside the selected directory.\n"}, Metadata: primitive.KnowledgeMetadata{Extractor: "yaml-frontmatter", Fields: map[string]string{"source.url": "sourceUrl"}}}
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{Assistant: "reader"})
	publisher := artifactcohort.NewPublisher()
	caller := &builderKnowledgeCaller{publisher: publisher}
	rootRef := artifactcohort.RootRef{URI: "knowledge://example/guide", Kind: primitive.KnowledgeKind, Version: 1}
	_, _, err = publisher.Publish(ctx, rootRef, raw, func(_ context.Context, body json.RawMessage) error {
		return json.Unmarshal(body, &primitive.KnowledgeEnvelope{})
	})
	require.NoError(t, err)
	policyResource := authz.Resource{Kind: "cohort", ID: "readers", Version: "logical", Tenant: "tenant"}
	policies := []authz.Document{{Resource: policyResource, Revision: 1, Policies: map[string]authz.Policy{artifactcohort.PolicyAction: {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}}
	cohorts := []artifactcohort.Cohort{{Tenant: "tenant", Name: "readers", PolicyResource: policyResource, Roots: []artifactcohort.RootRef{{URI: rootRef.URI, Kind: rootRef.Kind}}}}
	_, err = publisher.ReplaceCatalog(ctx, policies, cohorts, publisher.Generation())
	require.NoError(t, err)
	replica, err := artifactcohort.NewReplica(artifactcohort.Config{Enabled: true, Source: "example-source", Tenant: "tenant", Account: "account", Caller: caller, Facts: builderKnowledgeFacts{}, SessionKey: func(context.Context) (string, error) { return "reader-session", nil }})
	require.NoError(t, err)
	require.NoError(t, replica.Sync(ctx))
	source, err := artifactcohort.NewMetadataSource(replica, []string{"example"})
	require.NoError(t, err)
	require.NoError(t, runtime.InstallPublishedMetadata(resource.Connection{Name: "published", ProviderIdentity: "example-source"}, []string{"example"}, source))
	args := map[string]interface{}{"query": "documented guidance", "rootIds": []string{"docs"}, "model": "synthetic", "maxDocuments": 8}
	args["path"] = "guides/example.md"
	body, err := runtime.Registry.Execute(ctx, "resources:match", args)
	require.NoError(t, err)
	var result resources.MatchOutput
	require.NoError(t, json.Unmarshal([]byte(body), &result))
	require.Len(t, result.Documents, 1)
	require.Equal(t, "https://example.test/guide", result.Documents[0].Metadata["source.url"])
	require.Equal(t, "docs", result.Documents[0].Metadata["rootId"])
	require.Equal(t, "knowledge://example/guide/guides/example.md", result.Documents[0].Metadata["path"])

	coldEmbeddings := embedder.documents.Load()
	require.Positive(t, coldEmbeddings)
	warmBody, err := runtime.Registry.Execute(ctx, "resources:match", args)
	require.NoError(t, err)
	require.JSONEq(t, body, warmBody)
	require.Equal(t, coldEmbeddings, embedder.documents.Load(), "the warm query reuses indexed documents")
	require.EqualValues(t, 2, embedder.queries.Load())
	require.EqualValues(t, 1, caller.discovers.Load())
	require.EqualValues(t, 1, caller.fetches.Load(), "lookup and warm match reuse the preloaded exact publication")

	delete(args, "path")
	body, err = runtime.Registry.Execute(ctx, "resources:match", args)
	require.NoError(t, err)
	result = resources.MatchOutput{}
	require.NoError(t, json.Unmarshal([]byte(body), &result))
	require.Len(t, result.Documents, 2, "the configured inclusion keeps only the child directory")

	args["path"] = "file:///guide/example.md"
	_, err = runtime.Registry.Execute(ctx, "resources:match", args)
	require.ErrorIs(t, err, identity.ErrResourceDenied, "a file URI must never replace the selected publication location")

	policies[0].Revision++
	policies[0].Policies[artifactcohort.PolicyAction] = authz.Policy{Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reviewer"}}
	_, err = publisher.ReplaceCatalog(ctx, policies, cohorts, publisher.Generation())
	require.NoError(t, err)
	require.NoError(t, replica.Sync(ctx))
	delete(args, "path")
	beforeRevocation := embedder.queries.Load()
	body, err = runtime.Registry.Execute(ctx, "resources:match", args)
	require.ErrorIs(t, err, artifactcohort.ErrDenied)
	require.NotContains(t, body, "Documented example guidance.")
	require.Equal(t, beforeRevocation, embedder.queries.Load())
	require.EqualValues(t, 1, caller.fetches.Load(), "the policy revision does not download corpus bytes")

}
