package resource

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"os"
	"path/filepath"
	"testing"
)

func TestLayoutMenuBindingPreservesCurrentStewardDefinition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layout.yaml")
	original := []byte("version: 1\ntopbar: {actions: [{id: hidden, action: {type: window, windowKey: hiddenTopbar}}]}\napplications:\n  - id: steward\n    title: Workspace\n    icon: applications\n    className: steward-navigation\n    menus:\n      - id: advertisers\n        title: Advertisers\n        icon: office\n        className: steward-nav-advertisers\n        action: {type: window, windowKey: advertiserList, parameters: {page: 1}}\n      - id: campaigns\n        title: Campaigns\n        icon: timeline-events\n        className: steward-nav-campaigns\n        action: {type: window, windowKey: campaignList}\n")
	require.NoError(t, os.WriteFile(path, original, 0600))
	p, _, actor, _ := menuFixtureProvider(t)
	binding, err := LayoutMenuBinding(path, "navigation://team/navigation", p.ProviderIdentity(), func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) (identity.ResourceRevisionPolicy, error) {
		return localFixturePolicy{actor: *actor}, nil
	})
	require.NoError(t, err)
	updateLocalProvider(t, p, func(config *LocalConfig) { config.Bindings = []LocalResourceBinding{binding} })
	got, err := p.Get(context.Background(), "navigation", primitive.GetRequest{URI: binding.URI})
	require.NoError(t, err)
	visible := string(got.MenuProjection.DefinitionBytes)
	require.Contains(t, visible, "advertiserList")
	require.Contains(t, visible, "campaignList")
	require.Contains(t, visible, `"parameters":{"page":1}`)
	require.NotContains(t, visible, "hiddenTopbar")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, after)
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(got.MenuProjection.DefinitionBytes, &envelope))
	require.Len(t, envelope, 2)
}
