package template

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/primitive"
	tpldef "github.com/viant/agently-core/protocol/template"
	"github.com/viant/agently-core/runtime/requestctx"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
)

type catalogTemplates struct {
	items []*tpldef.Template
	reads int
}

func (r *catalogTemplates) LoadAll(context.Context) ([]*tpldef.Template, error) { return r.items, nil }
func (r *catalogTemplates) Load(_ context.Context, name string) (*tpldef.Template, error) {
	r.reads++
	for _, item := range r.items {
		if strings.EqualFold(item.Name, name) || item.ResourceURI == name {
			return item, nil
		}
	}
	return nil, nil
}

func TestTemplateListUsesSharedGroupedCatalogAndBoundCursors(t *testing.T) {
	reader := &catalogTemplates{items: []*tpldef.Template{
		{ID: "alpha", Name: "Alpha [Sales]", Description: "Quarterly Sales growth", ResourceURI: "template://north/alpha", Instructions: "PRIVATE BODY"},
		{ID: "beta", Name: "Beta Sales", Description: "Sales forecast", ResourceURI: "template://north/beta"},
		{ID: "gamma", Name: "Gamma", Description: "Inventory", ResourceURI: "template://south/gamma"},
		{ID: "literal", Name: "Pattern .*", Description: "Literal + symbols", ResourceURI: "template://south/literal"},
	}}
	s := New(tplrepo.NewWithReader(reader), nil)
	ctx := requestctx.WithTurnMeta(auth.WithUserInfo(context.Background(), &auth.UserInfo{Subject: "alice"}), requestctx.TurnMeta{ConversationID: "conversation", TurnID: "turn"})
	grouped := &ListOutput{}
	require.NoError(t, s.list(ctx, &ListInput{Namespace: "north", NameTerms: []string{"ALP", "beta"}, DescriptionTerms: []string{"SALES", "missing"}, Keywords: []string{"GROWTH"}}, grouped))
	require.Equal(t, []ListItem{{Name: "Alpha [Sales]", Description: "Quarterly Sales growth", URI: "template://north/alpha"}}, grouped.Items)
	literal := &ListOutput{}
	require.NoError(t, s.list(ctx, &ListInput{NameTerms: []string{".*"}}, literal))
	require.Len(t, literal.Items, 1)
	require.Equal(t, "Pattern .*", literal.Items[0].Name)
	query := &ListInput{Namespace: "north", NameTerms: []string{"sales"}, Limit: 1}
	first := &ListOutput{}
	require.NoError(t, s.list(ctx, query, first))
	require.False(t, first.Complete)
	require.Equal(t, 2, first.Total)
	require.NotEmpty(t, first.NextCursor)
	again := &ListOutput{}
	require.NoError(t, s.list(ctx, query, again))
	require.Equal(t, first.NextCursor, again.NextCursor)
	query.Cursor = first.NextCursor
	second := &ListOutput{}
	require.NoError(t, s.list(ctx, query, second))
	require.True(t, second.Complete)
	require.Equal(t, "Beta Sales", second.Items[0].Name)
	query.NameTerms = []string{"alpha"}
	require.ErrorIs(t, s.list(ctx, query, &ListOutput{}), primitive.ErrCatalogCursor)
	query.NameTerms = []string{"sales"}
	bob := auth.WithUserInfo(ctx, &auth.UserInfo{Subject: "bob"})
	require.ErrorIs(t, s.list(bob, query, &ListOutput{}), primitive.ErrCatalogCursor)
	otherTurn := requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: "conversation", TurnID: "other"})
	require.ErrorIs(t, s.list(otherTurn, query, &ListOutput{}), primitive.ErrCatalogCursor)
	legacy := &ListOutput{}
	require.NoError(t, s.list(ctx, &ListInput{}, legacy))
	require.Len(t, legacy.Items, 4)
	require.True(t, legacy.Complete)
	require.Equal(t, 4, legacy.Total)
	require.Zero(t, reader.reads, "list must not load template instructions")
	require.Error(t, s.get(ctx, &GetInput{Name: "Alpha"}, &GetOutput{}), "get remains exact, not contains")
	get := &GetOutput{}
	require.NoError(t, s.get(ctx, &GetInput{Name: "ALPHA [SALES]"}, get))
	require.Equal(t, "Alpha [Sales]", get.Name)
}
