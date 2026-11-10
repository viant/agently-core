package augmenter_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	base "github.com/viant/agently-core/genai/embedder/provider/base"
	agent "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/protocol/tool/service/resources"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	augmenter "github.com/viant/agently-core/service/augmenter"
	resource "github.com/viant/agently-core/service/resource"
	embopt "github.com/viant/embedius/matching/option"
	"github.com/viant/embedius/metadata"
	primitive "github.com/viant/forge/backend/provider/contract"
)

const corpusURI = "knowledge://example/guide"

type corpusSource struct {
	state  atomic.Pointer[primitive.ResourceState]
	denied atomic.Bool
	gets   atomic.Int64
}

func (s *corpusSource) AdmissionKey(context.Context) (string, error) {
	if s.denied.Load() {
		return "", identity.ErrResourceDenied
	}
	return "synthetic-admission", nil
}
func (s *corpusSource) CurrentHash(ctx context.Context, _ string) (string, error) {
	if _, e := s.AdmissionKey(ctx); e != nil {
		return "", e
	}
	return s.state.Load().ContentFingerprint, nil
}
func (s *corpusSource) Namespaces(context.Context) ([]primitive.NamespaceCapabilities, error) {
	return []primitive.NamespaceCapabilities{{ProviderIdentity: "example-source", Namespace: "example", Kinds: []primitive.KindSupport{{Kind: "knowledge", Operations: []string{"list", "get"}}}}}, nil
}
func (s *corpusSource) List(ctx context.Context, _, _ string) ([]primitive.ResourceState, error) {
	if _, e := s.AdmissionKey(ctx); e != nil {
		return nil, e
	}
	return []primitive.ResourceState{*s.state.Load()}, nil
}
func (s *corpusSource) Get(ctx context.Context, _ string) (*primitive.ResourceState, error) {
	if _, e := s.AdmissionKey(ctx); e != nil {
		return nil, e
	}
	s.gets.Add(1)
	copy := *s.state.Load()
	return &copy, nil
}
func (s *corpusSource) set(t *testing.T, text, citation string) {
	t.Helper()
	value := primitive.KnowledgeEnvelope{SchemaVersion: 1, Name: "Guide", Files: map[string]string{"guides/pixel.md": "---\ntitle: Pixel guide\nsourceUrl: " + citation + "\n---\n\n" + text + "\n"}, Metadata: primitive.KnowledgeMetadata{Extractor: "yaml-frontmatter", Fields: map[string]string{"document.title": "title", "source.url": "sourceUrl"}}}
	raw, e := json.Marshal(value)
	require.NoError(t, e)
	s.state.Store(&primitive.ResourceState{URI: corpusURI, Kind: "knowledge", Namespace: "example", Name: "guide", FormatVersion: 1, DefinitionBytes: raw, ContentFingerprint: identity.ContentFingerprint(raw)})
}

type corpusEmbedder struct {
	queryHook          func()
	documentEmbeddings atomic.Int64
	queryEmbeddings    atomic.Int64
}

func (e *corpusEmbedder) Embed(_ context.Context, texts []string) ([][]float32, int, error) {
	result := make([][]float32, len(texts))
	for i, text := range texts {
		if text == "pixel question" {
			e.queryEmbeddings.Add(1)
		} else {
			e.documentEmbeddings.Add(1)
		}
		if text == "pixel question" && e.queryHook != nil {
			e.queryHook()
		}
		if strings.Contains(text, "pixel") {
			result[i] = []float32{1, 0, 0}
		} else {
			result[i] = []float32{0, 1, 0}
		}
	}
	return result, 0, nil
}

type corpusEmbedderFinder struct{ embedder *corpusEmbedder }

func (f corpusEmbedderFinder) Find(context.Context, string) (base.Embedder, error) {
	return f.embedder, nil
}
func (f corpusEmbedderFinder) Ids() []string { return []string{"synthetic"} }

type corpusAgentFinder struct{ agent *agent.Agent }

func (f corpusAgentFinder) Find(context.Context, string) (*agent.Agent, error) { return f.agent, nil }
func knowledgeFixture(t *testing.T) (*corpusSource, *resource.Gateway, *corpusEmbedder, string) {
	t.Helper()
	source := &corpusSource{}
	source.set(t, "A pixel records documented events.", "https://example.test/articles/pixel")
	owner, e := manager.New(nil)
	require.NoError(t, e)
	actor, verify := resource.PublicReadActors()
	gateway := resource.NewGateway(owner, actor, verify, "host")
	t.Cleanup(gateway.Close)
	require.NoError(t, gateway.RegisterPublishedMetadataSource(resource.Connection{Name: "publication", ProviderIdentity: "example-source"}, []string{"example"}, source))
	return source, gateway, &corpusEmbedder{}, t.TempDir()
}
func newKnowledgeAugmenter(t *testing.T, gateway *resource.Gateway, embedder *corpusEmbedder, root string, async bool) *augmenter.Service {
	t.Helper()
	service := augmenter.New(corpusEmbedderFinder{embedder}, augmenter.WithIndexPathTemplate(root), augmenter.WithIndexAsync(async))
	service.SetKnowledgeGateway(gateway)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	return service
}
func knowledgeInput() *augmenter.AugmentDocsInput {
	return &augmenter.AugmentDocsInput{Query: "pixel question", Locations: []string{corpusURI}, Model: "synthetic", MaxDocuments: 1, IncludeFile: true}
}
func TestPublishedKnowledgeColdWarmUpdatesAndOwnerRevocation(t *testing.T) {
	source, gateway, embedder, indexRoot := knowledgeFixture(t)
	service := newKnowledgeAugmenter(t, gateway, embedder, indexRoot, false)
	var first augmenter.AugmentDocsOutput
	require.NoError(t, service.AugmentDocs(context.Background(), knowledgeInput(), &first))
	require.Len(t, first.Documents, 1)
	require.Contains(t, first.Content, "A pixel records documented events.")
	bound := resources.BindingDocuments(first.Documents, resources.DocumentSelection{MaxDocuments: 1})
	require.Len(t, bound, 1)
	require.Equal(t, "https://example.test/articles/pixel", bound[0].Metadata["source.url"])
	require.EqualValues(t, 1, source.gets.Load(), "the existing owner decodes one immutable corpus")
	coldEmbeddings := embedder.documentEmbeddings.Load()
	require.Positive(t, coldEmbeddings)
	warm := newKnowledgeAugmenter(t, gateway, embedder, indexRoot, true)
	var again augmenter.AugmentDocsOutput
	require.NoError(t, warm.AugmentDocs(context.Background(), knowledgeInput(), &again))
	require.Len(t, again.Documents, 1)
	require.EqualValues(t, 1, source.gets.Load(), "warm requests reuse the native publication cache")
	require.Equal(t, coldEmbeddings, embedder.documentEmbeddings.Load(), "warm requests reuse the persisted native vector index")
	require.EqualValues(t, 2, embedder.queryEmbeddings.Load())
	source.denied.Store(true)
	require.Error(t, warm.AugmentDocs(context.Background(), knowledgeInput(), &again))
	require.Empty(t, again.Documents)
	require.Empty(t, again.Content)
	source.denied.Store(false)
	source.set(t, "Updated pixel documentation.", "https://example.test/articles/updated-pixel")
	var updated augmenter.AugmentDocsOutput
	require.NoError(t, service.AugmentDocs(context.Background(), knowledgeInput(), &updated))
	require.Len(t, updated.Documents, 1)
	require.Contains(t, updated.Documents[0].PageContent, "Updated pixel documentation.")
	require.NotContains(t, updated.Documents[0].PageContent, "A pixel records")
	require.Equal(t, "https://example.test/articles/updated-pixel", updated.Documents[0].Metadata["source.url"])
	var markdown int
	require.NoError(t, filepath.WalkDir(indexRoot, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			markdown++
		}
		return nil
	}))
	require.Zero(t, markdown, "the source corpus is never materialized as local article files")
}
func TestPublishedKnowledgeFullFileUsesOriginalURIWhenDisplayPathIsTrimmed(t *testing.T) {
	_, gateway, embedder, indexRoot := knowledgeFixture(t)
	service := newKnowledgeAugmenter(t, gateway, embedder, indexRoot, false)
	input := knowledgeInput()
	input.TrimPath = corpusURI + "/"
	var output augmenter.AugmentDocsOutput
	require.NoError(t, service.AugmentDocs(context.Background(), input, &output))
	require.Contains(t, output.Content, "file: guides/pixel.md")
	require.Contains(t, output.Content, "A pixel records documented events.")
}

func TestPublishedKnowledgeRevokedOrReplacedDuringQueryReleasesNoBody(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "revoked", true: "replaced"}[replace], func(t *testing.T) {
			source, gateway, embedder, indexRoot := knowledgeFixture(t)
			service := newKnowledgeAugmenter(t, gateway, embedder, indexRoot, false)
			var output augmenter.AugmentDocsOutput
			require.NoError(t, service.AugmentDocs(context.Background(), knowledgeInput(), &output))
			embedder.queryHook = func() {
				if replace {
					source.set(t, "Replacement pixel documentation.", "https://example.test/articles/replacement")
				} else {
					source.denied.Store(true)
				}
			}
			output = augmenter.AugmentDocsOutput{}
			require.Error(t, service.AugmentDocs(context.Background(), knowledgeInput(), &output))
			require.Empty(t, output.Documents)
			require.Empty(t, output.Content)
		})
	}
}
func TestPublishedKnowledgeRootAliasUsesExistingResourcesMatch(t *testing.T) {
	_, gateway, embedder, indexRoot := knowledgeFixture(t)
	augmentation := newKnowledgeAugmenter(t, gateway, embedder, indexRoot, false)
	finder := corpusAgentFinder{&agent.Agent{Identity: agent.Identity{ID: "reader"}, Resources: []*agent.Resource{{ID: "docs", URI: corpusURI, Role: "system", Description: "Published guide"}}}}
	service := resources.New(augmentation, resources.WithAgentFinder(finder), resources.WithDefaultEmbedder("synthetic"))
	ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{Assistant: "reader"})
	rootsMethod, e := service.Method("roots")
	require.NoError(t, e)
	var roots resources.RootsOutput
	require.NoError(t, rootsMethod(ctx, &resources.RootsInput{}, &roots))
	require.Len(t, roots.Roots, 1)
	require.Equal(t, corpusURI, roots.Roots[0].URI)
	require.True(t, roots.Roots[0].AllowedSemanticSearch)
	require.False(t, roots.Roots[0].AllowedGrepSearch)
	method, e := service.Method("match")
	require.NoError(t, e)
	var output resources.MatchOutput
	require.NoError(t, method(ctx, &resources.MatchInput{Query: "pixel question", RootIDs: []string{"docs"}, MaxDocuments: 1}, &output))
	require.Len(t, output.Documents, 1)
	require.Equal(t, "docs", output.Documents[0].Metadata["rootId"])
	require.Equal(t, "https://example.test/articles/pixel", output.Documents[0].Metadata["source.url"])
}
func TestPublishedKnowledgeBindingKeepsOrdinaryWorkspaceRetrieval(t *testing.T) {
	source, gateway, embedder, indexRoot := knowledgeFixture(t)
	source.denied.Store(true)
	docs := t.TempDir()
	path := filepath.Join(docs, "legacy.md")
	require.NoError(t, os.WriteFile(path, []byte("---\nsourceUrl: https://example.test/legacy\n---\n\nLegacy pixel guidance.\n"), 0600))
	uri := "file://" + docs
	service := newKnowledgeAugmenter(t, gateway, embedder, indexRoot, false)
	ctx := augmenter.WithLocalRoots(context.Background(), []augmenter.LocalRoot{{ID: "legacy", URI: uri, Metadata: metadata.Config{Extractor: "yaml-frontmatter", Fields: map[string]string{"source.url": "sourceUrl"}}}})
	var output augmenter.AugmentDocsOutput
	require.NoError(t, service.AugmentDocs(ctx, &augmenter.AugmentDocsInput{Query: "pixel question", Locations: []string{uri}, Model: "synthetic", IncludeFile: true, MaxDocuments: 1}, &output))
	require.Len(t, output.Documents, 1)
	require.Contains(t, output.Content, "Legacy pixel guidance.")
	require.Equal(t, "https://example.test/legacy", output.Documents[0].Metadata["source.url"])
}

func TestPublishedKnowledgeDeniedRootCannotFallBackToOrdinaryPartialMatch(t *testing.T) {
	source, gateway, embedder, indexRoot := knowledgeFixture(t)
	source.denied.Store(true)
	docs := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(docs, "legacy.md"), []byte("Legacy pixel guidance."), 0600))
	service := newKnowledgeAugmenter(t, gateway, embedder, indexRoot, false)
	var output augmenter.AugmentDocsOutput
	require.Error(t, service.AugmentDocs(context.Background(), &augmenter.AugmentDocsInput{Query: "pixel question", Locations: []string{corpusURI, "file://" + docs}, Model: "synthetic", AllowPartial: true}, &output))
	require.Empty(t, output.Documents)
	require.Empty(t, output.Content)
}

func TestPublishedKnowledgeWarmMatchPreservesExclusions(t *testing.T) {
	_, gateway, embedder, indexRoot := knowledgeFixture(t)
	service := newKnowledgeAugmenter(t, gateway, embedder, indexRoot, false)
	var baseline augmenter.AugmentDocsOutput
	require.NoError(t, service.AugmentDocs(context.Background(), knowledgeInput(), &baseline))
	require.Len(t, baseline.Documents, 1)
	input := knowledgeInput()
	input.Match = &embopt.Options{Exclusions: []string{"**/pixel.md"}}
	var excluded augmenter.AugmentDocsOutput
	require.NoError(t, service.AugmentDocs(context.Background(), input, &excluded))
	require.Empty(t, excluded.Documents, "another source selection cannot borrow rows from the prior index")
}

func TestPublishedKnowledgeKeepsConfiguredNamespacesDistinct(t *testing.T) {
	_, gateway, embedder, indexRoot := knowledgeFixture(t)
	service := newKnowledgeAugmenter(t, gateway, embedder, indexRoot, false)
	ctx := augmenter.WithLocalRoots(context.Background(), []augmenter.LocalRoot{{ID: "foreign", URI: "knowledge://foreign/guide"}, {ID: "docs", URI: corpusURI}})
	var output augmenter.AugmentDocsOutput
	require.NoError(t, service.AugmentDocs(ctx, knowledgeInput(), &output))
	require.Len(t, output.Documents, 1)
	require.Equal(t, "https://example.test/articles/pixel", output.Documents[0].Metadata["source.url"])
}
