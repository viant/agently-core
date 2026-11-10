package executor_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	base "github.com/viant/agently-core/genai/embedder/provider/base"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	agent "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/tool"
	resources "github.com/viant/agently-core/protocol/tool/service/resources"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/sdk"
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

func knowledgeFixtureSubject(ctx context.Context) string {
	if subject := authctx.EffectiveUserID(ctx); subject != "" {
		return subject
	}
	return "reader"
}

func (builderKnowledgeFacts) Resolve(ctx context.Context) (authz.Facts, error) {
	subject := knowledgeFixtureSubject(ctx)
	role := "reader"
	if subject != "reader" {
		role = "outsider"
	}
	return authz.Facts{Subject: subject, Issuer: "https://identity.example.test", Tenant: "tenant", Roles: []string{role}, ValidUntil: time.Now().Add(time.Hour)}, nil
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

type knowledgeTestModelFinder struct{}

func (knowledgeTestModelFinder) Find(context.Context, string) (llm.Model, error) {
	return nil, fs.ErrNotExist
}

type knowledgeToolHTTPBackend struct {
	sdk.Backend
	registry tool.Registry
}

func (b knowledgeToolHTTPBackend) ExecuteTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	return b.registry.Execute(ctx, name, args)
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
	runtime, err := executor.NewBuilder().WithStore(fsstore.New(root)).WithKnowledgeStore(fsstore.NewKnowledgeStore(root)).WithStateStore(fsstore.NewStateStore(root)).WithModelFinder(knowledgeTestModelFinder{}).WithAgentFinder(&skillTestAgentFinder{agent: configured}).WithAugmenter(aug).WithPrimitiveAuthority(actor, verify, "local-workspace").WithPrimitiveGatewayIdentity("host").WithSkipRegistryInitialize(true).Build(context.Background())
	require.NoError(t, err)
	defer runtime.Close(context.Background())
	value := primitive.KnowledgeEnvelope{SchemaVersion: 1, Name: "Guide", Files: map[string]string{"guides/example.md": "---\ntitle: Example\nsourceUrl: https://example.test/guide\n---\n\nDocumented example guidance.\n", "guides/sibling.md": "Other guidance in the selected directory.\n", "other.md": "Excluded guidance outside the selected directory.\n", "nested/example.md": "Parent collision body.\n"}, Metadata: primitive.KnowledgeMetadata{Extractor: "yaml-frontmatter", Fields: map[string]string{"source.url": "sourceUrl"}}}
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
	const nestedURI = "knowledge://example/guide/nested"
	const independentURI = "knowledge://external/brief"
	refs := []artifactcohort.RootRef{{URI: rootRef.URI, Kind: rootRef.Kind}}
	for _, entry := range []struct{ uri, body string }{
		{nestedURI, "Nested corpus exact body.\n"},
		{independentURI, "Independently discovered corpus.\n"},
	} {
		definition, err := json.Marshal(primitive.KnowledgeEnvelope{SchemaVersion: 1, Name: "Example", Files: map[string]string{"example.md": entry.body}})
		require.NoError(t, err)
		ref := artifactcohort.RootRef{URI: entry.uri, Kind: primitive.KnowledgeKind, Version: 1}
		_, _, err = publisher.Publish(ctx, ref, definition, func(context.Context, json.RawMessage) error { return nil })
		require.NoError(t, err)
		refs = append(refs, artifactcohort.RootRef{URI: ref.URI, Kind: ref.Kind})
	}
	policyResource := authz.Resource{Kind: "cohort", ID: "readers", Version: "logical", Tenant: "tenant"}
	policies := []authz.Document{{Resource: policyResource, Revision: 1, Policies: map[string]authz.Policy{artifactcohort.PolicyAction: {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}}
	cohorts := []artifactcohort.Cohort{{Tenant: "tenant", Name: "readers", PolicyResource: policyResource, Roots: refs}}
	_, err = publisher.ReplaceCatalog(ctx, policies, cohorts, publisher.Generation())
	require.NoError(t, err)
	replica, err := artifactcohort.NewReplica(artifactcohort.Config{Enabled: true, Source: "example-source", Tenant: "tenant", Account: "account", Caller: caller, Facts: builderKnowledgeFacts{}, SessionKey: func(ctx context.Context) (string, error) { return knowledgeFixtureSubject(ctx), nil }})
	require.NoError(t, err)
	require.NoError(t, replica.Sync(ctx))
	source, err := artifactcohort.NewMetadataSource(replica, []string{"example", "external"})
	require.NoError(t, err)
	require.NoError(t, runtime.InstallPublishedMetadata(resource.Connection{Name: "published", ProviderIdentity: "example-source"}, []string{"example", "external"}, source))
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
	require.EqualValues(t, 3, caller.fetches.Load(), "lookup and warm match reuse the preloaded exact publication")

	delete(args, "path")
	body, err = runtime.Registry.Execute(ctx, "resources:match", args)
	require.NoError(t, err)
	result = resources.MatchOutput{}
	require.NoError(t, json.Unmarshal([]byte(body), &result))
	require.Len(t, result.Documents, 2, "the configured inclusion keeps only the child directory")

	args["path"] = "file:///guide/example.md"
	_, err = runtime.Registry.Execute(ctx, "resources:match", args)
	require.ErrorIs(t, err, identity.ErrResourceDenied, "a file URI must never replace the selected publication location")

	// Discovery uses only authorized published metadata and leaves the declared
	// alias and restrictions attached to the configured root.
	discovered := map[string]resources.Root{}
	cursor := ""
	for range 3 {
		listed, err := runtime.Registry.Execute(ctx, "resources:roots", map[string]interface{}{"maxRoots": 1, "cursor": cursor})
		require.NoError(t, err)
		var page resources.RootsOutput
		require.NoError(t, json.Unmarshal([]byte(listed), &page))
		require.Len(t, page.Roots, 1)
		discovered[page.Roots[0].URI] = page.Roots[0]
		cursor = page.NextCursor
	}
	require.Empty(t, cursor)
	require.Equal(t, "docs", discovered[rootRef.URI].ID)
	require.Equal(t, nestedURI, discovered[nestedURI].ID)
	require.Equal(t, independentURI, discovered[independentURI].ID)
	require.Equal(t, []string{"**/guides/"}, discovered[rootRef.URI].Match.Inclusions)
	require.EqualValues(t, 3, caller.fetches.Load(), "metadata paging never fetches a body")
	explicit, err := runtime.Registry.Execute(ctx, "resources:match", map[string]interface{}{"query": "documented guidance", "rootIds": []string{independentURI}, "model": "synthetic", "maxDocuments": 1})
	require.NoError(t, err)
	require.Contains(t, explicit, "Independently discovered corpus.")
	_, err = runtime.Registry.Execute(ctx, "resources:match", map[string]interface{}{"query": "documented guidance", "rootIds": []string{rootRef.URI}, "model": "synthetic", "match": map[string]interface{}{"inclusions": []string{"**/other.md"}}})
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	outsider := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "outsider"})
	hidden, err := runtime.Registry.Execute(outsider, "resources:roots", map[string]interface{}{})
	require.NoError(t, err)
	require.NotContains(t, hidden, "knowledge://")
	_, err = runtime.Registry.Execute(outsider, "resources:read", map[string]interface{}{"rootId": independentURI, "path": "example.md"})
	require.ErrorIs(t, err, artifactcohort.ErrDenied)

	handler := sdk.NewHandler(knowledgeToolHTTPBackend{registry: runtime.Registry})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The test exercises the SDK wire handler after trusted identity injection.
		ctx := requestctx.WithTurnMeta(r.Context(), requestctx.TurnMeta{Assistant: "reader"})
		if r.URL.Query().Get("fixture") == "outsider" {
			ctx = authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "outsider"})
		}
		handler.ServeHTTP(w, r.WithContext(ctx))
	}))
	defer httpServer.Close()
	for _, tc := range []struct {
		fixture, child, message string
		status                  int
	}{
		{"reader", "absent.md", "published knowledge document not found", http.StatusNotFound},
		{"outsider", "example.md", "managing server denied access", http.StatusForbidden},
	} {
		body := strings.NewReader(`{"rootId":"` + independentURI + `","path":"` + tc.child + `"}`)
		response, err := httpServer.Client().Post(httpServer.URL+"/v1/tools/resources:read/execute?fixture="+tc.fixture, "application/json", body)
		require.NoError(t, err)
		var envelope map[string]any
		decodeErr := json.NewDecoder(response.Body).Decode(&envelope)
		require.NoError(t, response.Body.Close())
		require.NoError(t, decodeErr)
		require.Equal(t, tc.status, response.StatusCode)
		require.Equal(t, map[string]any{"error": tc.message}, envelope)
	}

	const childURI = "knowledge://example/guide/guides/example.md"
	const documentPath = "guides/example.md"
	beforeRead := embedder.queries.Load()
	read := func(args map[string]interface{}) resources.ReadOutput {
		t.Helper()
		raw, err := runtime.Registry.Execute(ctx, "resources:read", args)
		require.NoError(t, err)
		var result resources.ReadOutput
		require.NoError(t, json.Unmarshal([]byte(raw), &result))
		return result
	}
	nested := read(map[string]interface{}{"uri": nestedURI + "/example.md"})
	require.Equal(t, "Nested corpus exact body.\n", nested.Content)
	independent := read(map[string]interface{}{"rootId": independentURI, "path": "example.md"})
	require.Equal(t, "Independently discovered corpus.\n", independent.Content)

	full := read(map[string]interface{}{"rootId": "docs", "path": documentPath, "maxBytes": 4096})
	require.Equal(t, childURI, full.URI)
	require.Equal(t, value.Files[documentPath], full.Content)
	bounded := read(map[string]interface{}{"uri": childURI, "maxBytes": 8})
	require.Equal(t, full.Version, bounded.Version)
	require.Equal(t, full.Content[:8], bounded.Content)
	require.Equal(t, 8, bounded.Returned)
	require.Positive(t, bounded.Remaining)
	next := read(map[string]interface{}{"uri": childURI, "bytesRange": map[string]interface{}{"offsetBytes": 8, "lengthBytes": 8}, "expectedVersion": full.Version})
	require.Equal(t, full.Content[8:16], next.Content)
	line := read(map[string]interface{}{"rootId": "docs", "path": documentPath, "startLine": 2, "lineCount": 1})
	require.Contains(t, line.Content, "title: Example")
	require.Equal(t, 2, line.StartLine)
	require.Equal(t, beforeRead, embedder.queries.Load(), "exact reads must not call the semantic index")
	require.EqualValues(t, 3, caller.fetches.Load(), "warm exact reads must reuse the admitted publication")

	for _, invalid := range []map[string]interface{}{
		{"rootId": "docs", "path": "../other.md"},
		{"uri": "knowledge://example/guide/guides/%2e%2e/other.md"},
		{"rootId": "docs", "uri": "knowledge://other/guide/guides/example.md"},
		{"rootId": "docs", "path": "file:///guide/example.md"},
		{"rootId": "docs", "path": "other.md"},
	} {
		_, err := runtime.Registry.Execute(ctx, "resources:read", invalid)
		require.ErrorIs(t, err, identity.ErrResourceDenied, "%v", invalid)
	}
	_, err = runtime.Registry.Execute(ctx, "resources:read", map[string]interface{}{"rootId": "docs", "path": "guides/absent.md"})
	require.ErrorIs(t, err, fs.ErrNotExist)
	_, err = runtime.Registry.Execute(ctx, "resources:read", map[string]interface{}{"rootId": "docs", "uri": "file:///guide/example.md", "representation": "text"})
	require.ErrorContains(t, err, "published knowledge read supports text ranges")

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
	_, err = runtime.Registry.Execute(ctx, "resources:read", map[string]interface{}{"uri": childURI})
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	_, err = runtime.Registry.Execute(ctx, "resources:read", map[string]interface{}{"rootId": "docs", "path": documentPath})
	require.ErrorIs(t, err, artifactcohort.ErrDenied)
	require.Equal(t, beforeRevocation, embedder.queries.Load())
	require.EqualValues(t, 3, caller.fetches.Load(), "the policy revision does not download corpus bytes")

}
