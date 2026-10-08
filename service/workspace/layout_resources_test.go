package workspace

import (
	"context"
	"errors"
	"testing"

	forgeservice "github.com/viant/agently-core/service/primitiveprovider"
)

type layoutCatalogFixture struct {
	pages     int
	finishErr error
	finished  bool
}

func (*layoutCatalogFixture) UsesWindowResourceResolution() bool              { return true }
func (f *layoutCatalogFixture) MetadataScope() forgeservice.MetadataReadScope { return f }
func (f *layoutCatalogFixture) BeginMetadataRead(ctx context.Context) (context.Context, func() error, error) {
	return ctx, func() error { f.finished = true; return f.finishErr }, nil
}
func (*layoutCatalogFixture) WithoutMetadataRead(ctx context.Context) context.Context { return ctx }
func (f *layoutCatalogFixture) WindowDefinitionsList(_ context.Context, input *forgeservice.WindowDefinitionListInput) (*forgeservice.WindowDefinitionListOutput, error) {
	f.pages++
	if input.Offset == 0 {
		return &forgeservice.WindowDefinitionListOutput{Windows: []forgeservice.WindowDefinitionSummary{{WindowID: "reports", ResourceURI: "window://steward/reports"}}, HasMore: true}, nil
	}
	return &forgeservice.WindowDefinitionListOutput{Windows: []forgeservice.WindowDefinitionSummary{{WindowID: "chat/new", ResourceURI: "window://agently/chat-new"}}}, nil
}

func TestLayoutUsesCanonicalCatalogAndDiscardsRevokedResponse(t *testing.T) {
	layout := &Layout{Applications: []LayoutApplication{{ID: "workspace", Title: "Workspace", Menus: []LayoutMenu{
		{ID: "reports", Title: "Reports", Action: &LayoutAction{Type: "window", WindowKey: "reports"}},
		{ID: "secret", Title: "Restricted", Action: &LayoutAction{Type: "window", WindowKey: "unmapped"}},
	}}}}
	catalog := &layoutCatalogFixture{}
	result, err := filterLayoutContextWithRuntimes(context.Background(), layout, nil, nil, catalog)
	if err != nil || result == nil || len(result.Applications) != 1 || len(result.Applications[0].Menus) != 1 || result.Applications[0].Menus[0].ID != "reports" || catalog.pages != 2 || !catalog.finished {
		t.Fatalf("canonical layout filtering: %+v %v, pages=%d finished=%v", result, err, catalog.pages, catalog.finished)
	}
	revoked := errors.New("verified authority changed")
	catalog.finishErr, catalog.finished = revoked, false
	result, err = filterLayoutContextWithRuntimes(context.Background(), layout, nil, nil, catalog)
	if result != nil || !errors.Is(err, revoked) || !catalog.finished {
		t.Fatal("layout metadata escaped failed final identity check")
	}
}
