package workspace

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	producer "github.com/viant/forge/backend/resourcehost/producer"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

// Build the definition with the pinned producer contract: identities belong to
// a group, while display labels and IDs in other branches may coincide.
func projectedMenuIdentityFixture(t *testing.T) json.RawMessage {
	t.Helper()
	fields := producer.MenuRowFields{GroupID: "group", GroupLabel: "groupLabel", GroupOrder: "groupOrder", ItemID: "item", ItemLabel: "itemLabel", ItemOrder: "itemOrder"}
	rows := []map[string]any{
		{"group": "alpha", "groupLabel": "Category", "groupOrder": 1, "item": "alpha", "itemLabel": "Overview", "itemOrder": 1},
		{"group": "beta", "groupLabel": "Category", "groupOrder": 2, "item": "alpha", "itemLabel": "Overview", "itemOrder": 1},
		{"group": "beta", "groupLabel": "Category", "groupOrder": 2, "item": "beta", "itemLabel": "Overview", "itemOrder": 2},
	}
	bindings := map[producer.MenuRowIdentity]producer.MenuRowBinding{}
	for _, row := range rows {
		bindings[producer.MenuRowIdentity{GroupID: row["group"].(string), ItemID: row["item"].(string)}] = producer.MenuRowBinding{Action: json.RawMessage(`{"type":"url","url":"https://example.invalid/overview"}`)}
	}
	raw, err := producer.ProjectMenuRows(context.Background(), rows, fields, json.RawMessage(`{"id":"main","title":"Main"}`), bindings)
	require.NoError(t, err)
	return raw
}

func TestLayoutGatewayMenuIdentityUsesParentPath(t *testing.T) {
	ctx := context.Background()
	raw := projectedMenuIdentityFixture(t)
	actor, verify := resources.PublicReadActors()
	definition, err := resources.SnapshotEmbeddedLayoutMenuDefinition(raw, "navigation://example/main", nil)
	require.NoError(t, err)
	snapshot, err := resources.NewNativeAssetSnapshot(ctx, t.TempDir(), []resources.NativeSnapshotDefinition{definition})
	require.NoError(t, err)
	bindings, err := snapshot.Bindings("example", func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
		return capturedMenuPolicy{a}, nil
	})
	require.NoError(t, err)
	provider, err := resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: "example", Actor: actor, Verify: verify, Bindings: bindings,
		Authorize:  func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil },
		Validators: map[string]resources.LocalResourceValidator{"navigation": resources.ValidateMenu},
		MenuActionVisibility: func(context.Context, identity.VerifiedActor, identity.ResourceURI, json.RawMessage) (bool, error) {
			return true, nil
		},
	})
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation("") })
	require.NoError(t, mgr.RegisterLocal(ctx, "example", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "example"}, func(context.Context) (mcpclient.Interface, error) {
		return resources.NewLocalMCPClient(provider)
	}))
	gateway := resources.NewGateway(mgr, actor, verify, "host")
	t.Cleanup(func() { gateway.Close() })
	reader, err := ApplicationMenuReader(gateway, []byte(`applicationSources: [{connection: example, resource: "navigation://example/main", applicationId: main}]`))
	require.NoError(t, err)
	handler := NewMetadataHandler(nil, nil, "test")
	handler.SetAuthorizationRuntimes(nil, nil)
	require.NoError(t, handler.SetCapturedLayout(t.TempDir(), "embedded", []byte("version: 1\nid: shell\napplications: []\n"), func(context.Context) error { return nil }))
	handler.SetLayoutMenuReader(reader)
	for i := 0; i < 2; i++ {
		got, err := handler.Layout(ctx, &LayoutRequest{})
		require.NoError(t, err)
		require.Len(t, got.Layout.Applications, 1)
		groups := got.Layout.Applications[0].Menus
		require.Len(t, groups, 2)
		require.Equal(t, "alpha", groups[0].ID)
		require.Equal(t, "alpha", groups[0].Children[0].ID)
		require.Equal(t, "alpha", groups[1].Children[0].ID)
		require.Equal(t, groups[0].Children[0].Title, groups[1].Children[0].Title)
		require.Len(t, groups[1].Children, 2)
	}
}

func TestNavigationMenuIdentityValidationPreservesSiblingConstraints(t *testing.T) {
	raw := projectedMenuIdentityFixture(t)
	for _, tc := range []struct {
		name   string
		mutate func(*Layout)
		want   string
	}{
		{"distinct-paths", func(*Layout) {}, ""},
		{"repeated-id-at-each-depth", func(l *Layout) {
			leaf := l.Applications[0].Menus[0].Children[0]
			l.Applications[0].Menus[0].Children = []LayoutMenu{{ID: "alpha", Title: "Nested", Children: []LayoutMenu{leaf}}}
		}, ""},
		{"duplicate-deep-child", func(l *Layout) {
			leaf := l.Applications[0].Menus[0].Children[0]
			l.Applications[0].Menus[0].Children = []LayoutMenu{{ID: "alpha", Title: "Nested", Children: []LayoutMenu{leaf, leaf}}}
		}, `duplicate menu "alpha"`},
		{"duplicate-child", func(l *Layout) { m := &l.Applications[0].Menus[1]; m.Children = append(m.Children, m.Children[0]) }, `duplicate menu "alpha"`},
		{"duplicate-group", func(l *Layout) { l.Applications[0].Menus = append(l.Applications[0].Menus, l.Applications[0].Menus[0]) }, `duplicate menu "alpha"`},
		{"duplicate-application", func(l *Layout) { l.Applications = append(l.Applications, l.Applications[0]) }, `duplicate application "main"`},
		{"nested-invalid-url", func(l *Layout) { l.Applications[0].Menus[1].Children[0].Action.URL = "javascript:invalid" }, "invalid URL action"},
		{"nested-invalid-report-uri", func(l *Layout) {
			l.Applications[0].Menus[1].Children[0].Action = &LayoutAction{Type: "report", ResourceURI: "window://example/overview"}
		}, "invalid report destination"},
		{"nested-invalid-condition", func(l *Layout) {
			l.Applications[0].Menus[1].Children[0].VisibleWhen = map[string]any{"source": "authorization", "field": "principal.roles", "unknown": "operator"}
		}, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var copy struct {
				Applications []LayoutApplication `json:"applications"`
			}
			require.NoError(t, json.Unmarshal(raw, &copy))
			layout := &Layout{Version: 1, ID: "shell", Applications: copy.Applications}
			tc.mutate(layout)
			encoded, err := json.Marshal(layout)
			require.NoError(t, err)
			_, definitionErr := validateNavigationDefinition(encoded)
			layoutErr := validateLayout(layout)
			for _, err := range []error{definitionErr, layoutErr} {
				if tc.want == "" {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, tc.want)
				}
			}
			applications, err := json.Marshal(layout.Applications)
			require.NoError(t, err)
			handler := NewMetadataHandler(nil, nil, "test")
			handler.SetAuthorizationRuntimes(nil, nil)
			require.NoError(t, handler.SetCapturedLayout(t.TempDir(), "embedded", []byte("version: 1\nid: shell\napplications: []\n"), func(context.Context) error { return nil }))
			handler.SetLayoutMenuReader(func(context.Context) (*LayoutMenuRead, error) {
				return &LayoutMenuRead{Applications: applications, Revision: "fixture", Revalidate: func(context.Context) error { return nil }}, nil
			})
			got, err := handler.Layout(context.Background(), &LayoutRequest{})
			if tc.want == "" {
				require.NoError(t, err)
				require.Len(t, got.Layout.Applications, 1)
			} else {
				require.Error(t, err)
				require.Nil(t, got)
			}
		})
	}
}
