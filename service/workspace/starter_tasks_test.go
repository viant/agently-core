package workspace

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/mcp/manager"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/authz"
	"github.com/viant/forge/backend/artifactcohort"
)

type starterTaskPublisher struct{ *artifactcohort.Publisher }

func (p starterTaskPublisher) Discover(_ context.Context) (artifactcohort.Manifest, error) {
	return p.Snapshot()
}
func (p starterTaskPublisher) Fetch(_ context.Context, input artifactcohort.FetchInput) (*artifactcohort.Artifact, error) {
	return p.Get(input.Root)
}

type starterTaskRole struct{}
type starterTaskFacts struct{}

func (starterTaskFacts) Resolve(ctx context.Context) (authz.Facts, error) {
	role, _ := ctx.Value(starterTaskRole{}).(string)
	return authz.Facts{Subject: role, Issuer: "example", Tenant: "example", Roles: []string{role}, ValidUntil: time.Now().Add(time.Hour)}, nil
}

func TestWorkspaceNamespaceStarterTasksUseCurrentCohortAdmission(t *testing.T) {
	ctx := context.Background()
	publisher := starterTaskPublisher{artifactcohort.NewPublisher()}
	var roots []artifactcohort.RootRef
	for _, entry := range []struct{ namespace, raw string }{
		{"alpha", `{"schemaVersion":1,"categories":[{"id":"review","title":"Review"}],"tasks":[{"id":"review","title":"Alpha","prompt":"Review alpha.","categoryId":"review"}]}`},
		{"beta", `{"schemaVersion":1,"tasks":[{"id":"review","title":"Beta","prompt":"Review beta.","priority":10}]}`},
		{"empty", `{"schemaVersion":1,"tasks":[]}`},
	} {
		uri := "startertask://" + entry.namespace + "/default"
		artifact, _, err := publisher.Publish(ctx, artifactcohort.RootRef{URI: uri, Kind: "startertask", Version: 1}, json.RawMessage(entry.raw), func(context.Context, json.RawMessage) error { return nil })
		require.NoError(t, err)
		roots = append(roots, artifact.Root)
	}
	role := authz.Resource{Tenant: "example", Kind: "cohort", ID: "reader", Version: "logical"}
	policy := authz.Document{Resource: role, Revision: 1, Policies: map[string]authz.Policy{artifactcohort.PolicyAction: {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}
	cohorts := []artifactcohort.Cohort{{Tenant: "example", Name: "readers", PolicyResource: role, Roots: roots}}
	_, err := publisher.ReplaceCatalog(ctx, []authz.Document{policy}, cohorts, publisher.Generation())
	require.NoError(t, err)
	replica, err := artifactcohort.NewReplica(artifactcohort.Config{
		Enabled: true, Source: "example", Tenant: "example", Account: "publisher",
		Caller: publisher, Facts: starterTaskFacts{}, SessionKey: func(ctx context.Context) (string, error) {
			role, _ := ctx.Value(starterTaskRole{}).(string)
			return role, nil
		},
	})
	require.NoError(t, err)
	require.NoError(t, replica.Sync(ctx))
	namespaces := []string{"alpha", "beta", "empty"}
	source, err := artifactcohort.NewMetadataSource(replica, namespaces)
	require.NoError(t, err)
	trap := &layoutPublishedTrap{}
	pool, err := manager.New(trap)
	require.NoError(t, err)
	t.Cleanup(func() { pool.CloseConversation("") })
	actors, verify := resources.PublicReadActors()
	gateway := resources.NewGateway(pool, actors, verify, "host")
	t.Cleanup(gateway.Close)
	require.NoError(t, gateway.RegisterPublishedMetadataSource(resources.Connection{Name: "catalog", ProviderIdentity: "example"}, namespaces, source))
	handler := NewMetadataHandler(nil, nil, "test")
	handler.SetStarterTaskSources(gateway, nil)

	reader := context.WithValue(ctx, starterTaskRole{}, "reader")
	resp, err := handler.Metadata(reader)
	require.NoError(t, err)
	require.NotNil(t, resp.NamespaceStarterTasks)
	require.Len(t, resp.NamespaceStarterTasks.Tasks, 2)
	require.Equal(t, "startertask://beta/default#review", resp.NamespaceStarterTasks.Tasks[0].ID)
	require.Equal(t, "startertask://alpha/default#review", resp.NamespaceStarterTasks.Tasks[1].ID)
	require.Equal(t, "startertask://alpha/default#review", resp.NamespaceStarterTasks.Tasks[1].CategoryID)
	require.Equal(t, "startertask://alpha/default#review", resp.NamespaceStarterTasks.Categories[0].ID)
	require.Zero(t, trap.options.Load())

	resp, err = handler.Metadata(context.WithValue(ctx, starterTaskRole{}, "other"))
	require.NoError(t, err)
	require.Nil(t, resp.NamespaceStarterTasks, "an unadmitted set must not overwrite agent defaults")

	cohorts[0].Roots = roots[2:]
	_, err = publisher.ReplaceCatalog(ctx, []authz.Document{policy}, cohorts, publisher.Generation())
	require.NoError(t, err)
	require.NoError(t, replica.Sync(ctx))
	resp, err = handler.Metadata(reader)
	require.NoError(t, err)
	require.NotNil(t, resp.NamespaceStarterTasks, "the admitted empty set still overrides defaults")
	require.Empty(t, resp.NamespaceStarterTasks.Tasks)
	require.NotNil(t, resp.NamespaceStarterTasks.Tasks, "explicit empty is serialized as an array")
}
