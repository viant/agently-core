package resource

import (
	"context"
	"encoding/json"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/forge/backend/provider/contract"
	identity "github.com/viant/forge/backend/resource"
	shared "github.com/viant/forge/backend/resourcehost"
)

type knowledgePublication struct {
	state  primitive.ResourceState
	denied bool
	reads  int
}

func (s *knowledgePublication) AdmissionKey(context.Context) (string, error) {
	if s.denied {
		return "", identity.ErrResourceDenied
	}
	return "admitted", nil
}
func (s *knowledgePublication) CurrentHash(ctx context.Context, _ string) (string, error) {
	if _, err := s.AdmissionKey(ctx); err != nil {
		return "", err
	}
	return s.state.ContentFingerprint, nil
}
func (s *knowledgePublication) Namespaces(context.Context) ([]primitive.NamespaceCapabilities, error) {
	return nil, nil
}
func (s *knowledgePublication) List(context.Context, string, string) ([]primitive.ResourceState, error) {
	return []primitive.ResourceState{s.state}, nil
}
func (s *knowledgePublication) Get(ctx context.Context, _ string) (*primitive.ResourceState, error) {
	if _, err := s.AdmissionKey(ctx); err != nil {
		return nil, err
	}
	s.reads++
	value := s.state
	return &value, nil
}

func TestKnowledgeReaderUsesNativePublicationSelectionAndRevocation(t *testing.T) {
	ctx := context.Background()
	manager, err := manager.New(nil)
	require.NoError(t, err)
	actor, verify := shared.PublicReadActors()
	gateway := NewGateway(manager, actor, verify, "reader")
	t.Cleanup(gateway.Close)
	const uri = "knowledge://example/guide"
	publication := &knowledgePublication{}
	set := func(body string) {
		value := primitive.KnowledgeEnvelope{SchemaVersion: 1, Name: "Guide", Files: map[string]string{"guides/example.md": body}, Metadata: primitive.KnowledgeMetadata{Extractor: "yaml-frontmatter", Fields: map[string]string{"source.url": "sourceUrl"}}, Sources: []primitive.KnowledgeSource{{ID: "reference", Paths: []string{"guides/example.md"}}}}
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		publication.state = primitive.ResourceState{URI: uri, Kind: primitive.KnowledgeKind, Namespace: "example", Name: "guide", FormatVersion: 1, DefinitionBytes: raw, ContentFingerprint: identity.ContentFingerprint(raw)}
	}
	const original = "---\ntitle: Example\nsourceUrl: https://example.test/guide\n---\n\nExact  document.\n"
	set(original)
	require.NoError(t, gateway.RegisterPublishedMetadataSource(shared.Connection{Name: "publication", ProviderIdentity: "publisher"}, []string{"example"}, publication))
	first, err := ReadKnowledge(ctx, gateway, uri)
	require.NoError(t, err)
	require.Equal(t, original, first.Value.Files["guides/example.md"])
	first.Value.Files["guides/example.md"] = "mutated"
	first.Value.Sources[0].Paths[0] = "mutated"
	second, err := ReadKnowledge(ctx, gateway, uri)
	require.NoError(t, err)
	require.Equal(t, original, second.Value.Files["guides/example.md"])
	require.Equal(t, "guides/example.md", second.Value.Sources[0].Paths[0])
	for range 4 {
		exact, err := ReadKnowledgeFile(ctx, gateway, uri, "guides/example.md")
		require.NoError(t, err)
		require.Equal(t, original, string(exact))
		exact[0] = 'x'
	}
	_, err = ReadKnowledgeFile(ctx, gateway, uri, "guides/missing.md")
	require.ErrorIs(t, err, fs.ErrNotExist)
	require.Equal(t, 1, publication.reads, "corpus and warm file reads share one immutable publication decode")
	set("Updated document.\n")
	updated, err := ReadKnowledge(ctx, gateway, uri)
	require.NoError(t, err)
	require.Equal(t, "Updated document.\n", updated.Value.Files["guides/example.md"])
	require.NotEqual(t, second.Resource.ContentFingerprint, updated.Resource.ContentFingerprint)
	publication.denied = true
	_, err = ReadKnowledge(ctx, gateway, uri)
	require.Error(t, err, "an existing decoded corpus never bypasses revoked admission")
	_, err = ReadKnowledgeFile(ctx, gateway, uri, "guides/missing.md")
	require.ErrorIs(t, err, identity.ErrResourceDenied, "revoked callers cannot distinguish a missing file")
}
