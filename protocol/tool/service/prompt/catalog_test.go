package prompt

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	auth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/intent"
	"github.com/viant/agently-core/protocol/primitive"
	"github.com/viant/agently-core/service/policy"
	repo "github.com/viant/agently-core/workspace/repository/intake"
)

type catalogProfiles struct {
	profiles []*intent.Profile
	loads    int
}

func (r *catalogProfiles) LoadAll(context.Context) ([]*intent.Profile, error) { return r.profiles, nil }
func (r *catalogProfiles) Load(context.Context, string) (*intent.Profile, error) {
	r.loads++
	return nil, fmt.Errorf("body not needed for listing")
}
func TestPlaybookCatalogProgressiveSearchAndExclusion(t *testing.T) {
	reader := &catalogProfiles{}
	for i := 0; i < 12; i++ {
		reader.profiles = append(reader.profiles, &intent.Profile{ID: fmt.Sprintf("guide%02d", i), Name: "Calendar scheduling", Description: "Plan events and manage calendar invitations", ResourceURI: fmt.Sprintf("playbook://planning/guide%02d", i), Instructions: "private-body-marker"})
	}
	reader.profiles = append(reader.profiles, &intent.Profile{ID: "archived", Name: "Calendar scheduling archived", Description: "Old events guide", ResourceURI: "playbook://planning/archived"})
	s := NewPlaybook(repo.NewWithReader(reader))
	ctx := auth.WithCanonicalUserID(context.Background(), "alice")
	out := &ListOutput{}
	require.NoError(t, s.list(ctx, &ListInput{}, out))
	require.Len(t, out.Profiles, 7)
	require.Equal(t, 13, out.Total)
	require.NotEmpty(t, out.NextCursor)
	query := ListInput{CatalogQuery: primitive.CatalogQuery{Search: "calendar invitations", Namespace: "planning", Keywords: []string{"calendar", "events"}, ExcludeKeywords: []string{"archived", "obsolete"}, Limit: 4}}
	require.NoError(t, s.list(ctx, &query, out))
	require.Len(t, out.Profiles, 4)
	require.Equal(t, 12, out.Total)
	first := out.Profiles[0].ID
	query.Cursor = out.NextCursor
	require.NoError(t, s.list(ctx, &query, out))
	require.NotEqual(t, first, out.Profiles[0].ID)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private-body-marker")
	require.Zero(t, reader.loads)
	query.Search = "different"
	require.ErrorIs(t, s.list(ctx, &query, out), primitive.ErrCatalogCursor)
}
func TestPlaybookCatalogFiltersBeforeRankingAndCounting(t *testing.T) {
	reader := &catalogProfiles{profiles: []*intent.Profile{{ID: "allowed", Description: "calendar guide"}, {ID: "denied", Description: "calendar guide secret"}}}
	s := New(repo.NewWithReader(reader), WithAuthorizationPolicy(policy.NewRuntime(promptPolicyResolver{}, policy.OperationIntentView)))
	out := &ListOutput{}
	require.NoError(t, s.list(context.Background(), &ListInput{CatalogQuery: primitive.CatalogQuery{Search: "calendar"}}, out))
	require.Equal(t, 1, out.Total)
	require.Len(t, out.Profiles, 1)
	require.Equal(t, "allowed", out.Profiles[0].ID)
}
