package manager

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/authz"
	cohort "github.com/viant/forge/backend/artifactcohort"
	primitive "github.com/viant/forge/backend/provider/contract"
	resources "github.com/viant/forge/backend/resourcehost"
)

type generationPublisherCaller struct {
	publisher *cohort.Publisher
	reads     atomic.Int64
}

func (c *generationPublisherCaller) Discover(context.Context) (cohort.Manifest, error) {
	c.reads.Add(1)
	return c.publisher.Snapshot()
}
func (c *generationPublisherCaller) Fetch(_ context.Context, input cohort.FetchInput) (*cohort.Artifact, error) {
	c.reads.Add(1)
	return c.publisher.Get(input.Root)
}

type generationFacts struct{ calls atomic.Int64 }

func (f *generationFacts) Resolve(context.Context) (authz.Facts, error) {
	f.calls.Add(1)
	return authz.Facts{Issuer: "verified", Subject: "reader", Tenant: "tenant", Roles: []string{"reader"}, ValidUntil: time.Now().Add(time.Hour)}, nil
}

type generationMetadata struct {
	*cohort.MetadataSource
	gets, lists atomic.Int64
}

func (s *generationMetadata) Get(ctx context.Context, uri string) (*primitive.ResourceState, error) {
	s.gets.Add(1)
	return s.MetadataSource.Get(ctx, uri)
}
func (s *generationMetadata) List(ctx context.Context, kind, namespace string) ([]primitive.ResourceState, error) {
	s.lists.Add(1)
	return s.MetadataSource.List(ctx, kind, namespace)
}

func TestManagerOptionsMemoFencesPublishedMCPUpdateAndRevocationWithinRequest(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	t.Setenv("AGENTLY_RUNTIME_ROOT", filepath.Join(root, "state"))
	ctx := WithNamesMemo(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "reader"}))
	publisher := cohort.NewPublisher()
	name := "mcp://example/data"
	publish := func(version uint64, endpoint string) {
		_, _, err := publisher.Publish(ctx, cohort.RootRef{URI: name, Kind: "mcp", Version: version}, json.RawMessage(`{"name":"data","transport":{"type":"streamable","url":"`+endpoint+`"}}`), func(context.Context, json.RawMessage) error { return nil })
		require.NoError(t, err)
	}
	publish(1, "https://first.example.invalid/mcp")
	policy := authz.Resource{Tenant: "tenant", Kind: "cohort", ID: "readers", Version: "logical"}
	policies := []authz.Document{{Resource: policy, Revision: 1, Policies: map[string]authz.Policy{cohort.PolicyAction: {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}}
	groups := []cohort.Cohort{{Tenant: "tenant", Name: "readers", PolicyResource: policy, Roots: []cohort.RootRef{{URI: name, Kind: "mcp"}}}}
	_, err := publisher.ReplaceCatalog(ctx, policies, groups, publisher.Generation())
	require.NoError(t, err)
	caller := &generationPublisherCaller{publisher: publisher}
	facts := &generationFacts{}
	replica, err := cohort.NewReplica(cohort.Config{Enabled: true, Source: "publisher", Tenant: "tenant", Account: "account", Caller: caller, Facts: facts, SessionKey: func(context.Context) (string, error) { return "verified-session", nil }})
	require.NoError(t, err)
	require.NoError(t, replica.Sync(ctx))
	local, err := cohort.NewMetadataSource(replica, []string{"example"})
	require.NoError(t, err)
	source := &generationMetadata{MetadataSource: local}
	provider := NewRepoProvider()
	pool, err := New(provider)
	require.NoError(t, err)
	defer pool.CloseConversation("")
	actor, verify := resources.PublicReadActors()
	gateway := resources.NewGateway(pool, actor, verify, "host")
	defer gateway.Close()
	require.NoError(t, gateway.RegisterPublishedMetadataSource(resources.Connection{Name: "catalog", ProviderIdentity: "publisher"}, []string{"example"}, source))
	reader, err := resources.NewMCPReferenceReader(gateway, resources.MCPDefinitionSource{ConnectionName: "catalog", ProviderIdentity: "publisher", Namespace: "example"}, nil)
	require.NoError(t, err)
	bound, err := pool.BindDefinitionReader(reader)
	require.NoError(t, err)
	require.True(t, bound)
	first, err := pool.Options(ctx, name)
	require.NoError(t, err)
	require.Equal(t, "https://first.example.invalid/mcp", first.Transport.URL)
	reads, lists, roleCalls := source.gets.Load(), source.lists.Load(), facts.calls.Load()
	same, err := pool.Options(ctx, name)
	require.NoError(t, err)
	require.Equal(t, first.Transport.URL, same.Transport.URL)
	require.Equal(t, reads, source.gets.Load())
	require.Equal(t, lists, source.lists.Load(), "generation checks never enumerate the catalog")
	require.Equal(t, roleCalls, facts.calls.Load())
	before := reader.DefinitionContentGeneration(ctx)
	require.NoError(t, replica.MarkUpdated(name))
	require.NotEqual(t, before, reader.DefinitionContentGeneration(ctx))
	_, err = pool.Options(ctx, name)
	require.Error(t, err, "notification must defeat old same-request Options before sync")
	require.Equal(t, roleCalls, facts.calls.Load())
	publish(2, "https://second.example.invalid/mcp")
	require.NoError(t, replica.Sync(ctx))
	second, err := pool.Options(ctx, name)
	require.NoError(t, err)
	require.Equal(t, "https://second.example.invalid/mcp", second.Transport.URL)
	require.Equal(t, roleCalls, facts.calls.Load())
	policies[0].Revision++
	policies[0].Policies[cohort.PolicyAction] = authz.Policy{Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "different-role"}}
	_, err = publisher.ReplaceCatalog(ctx, policies, groups, publisher.Generation())
	require.NoError(t, err)
	require.NoError(t, replica.Sync(ctx))
	_, err = pool.Options(ctx, name)
	require.Error(t, err, "policy revocation must not reuse the updated request memo")
}
