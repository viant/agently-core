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

func TestLayoutMenuBindingPreservesCurrentLibraryDefinition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layout.yaml")
	original := []byte("version: 1\ntopbar: {actions: [{id: hidden, action: {type: window, windowKey: hiddenTopbar}}]}\napplications:\n  - id: library\n    title: Workspace\n    icon: applications\n    className: library-navigation\n    menus:\n      - id: collections\n        title: Collections\n        icon: office\n        className: library-nav-collections\n        action: {type: window, windowKey: collectionList, parameters: {page: 1}}\n      - id: entries\n        title: Entries\n        icon: timeline-events\n        className: library-nav-entries\n        action: {type: window, windowKey: entryList}\n")
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
	require.Contains(t, visible, "collectionList")
	require.Contains(t, visible, "entryList")
	require.Contains(t, visible, `"parameters":{"page":1}`)
	require.NotContains(t, visible, "hiddenTopbar")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, after)
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(got.MenuProjection.DefinitionBytes, &envelope))
	require.Len(t, envelope, 2)
}
