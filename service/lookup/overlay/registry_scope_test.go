package overlay

import (
	"github.com/stretchr/testify/require"
	lookup "github.com/viant/agently-core/protocol/lookup/overlay"
	identity "github.com/viant/agently-core/protocol/resource"
	"testing"
)

func TestRegistryScopedPreservesMappingsAndRejectsNamespaceCollision(t *testing.T) {
	makeOverlay := func(id, namespace, provider string, priority int) *lookup.Overlay {
		return &lookup.Overlay{ID: id, Priority: priority, Resource: &identity.ResolvedResource{URI: "lookup://" + namespace + "/" + id, ProviderIdentity: provider}, Bindings: []lookup.Binding{{Named: &lookup.NamedToken{Name: "order"}, Lookup: lookup.Lookup{DataSource: "orders", DialogId: "picker"}}}}
	}
	first := makeOverlay("original", "one", "source", 1)
	entries, err := New(capturedDefinitions{first}).RegistryScoped("window", "order")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "orders", entries[0].DataSource)
	require.Equal(t, "picker", entries[0].DialogId)
	require.Equal(t, "datasource://one/orders", entries[0].DataSourceResource.URI)
	require.Equal(t, "window://one/picker", entries[0].PickerResource.URI)
	_, err = New(capturedDefinitions{first, makeOverlay("other", "two", "source", 20)}).RegistryScoped("window", "order")
	require.Error(t, err, "priority must not select an unrelated namespace")
	_, err = New(capturedDefinitions{first, makeOverlay("other", "one", "foreign", 20)}).RegistryScoped("window", "order")
	require.Error(t, err, "priority must not select an unrelated provider")
	higher := makeOverlay("higher", "one", "source", 20)
	higher.Bindings[0].Lookup.DataSource = "datasource://one/explicit"
	entries, err = New(capturedDefinitions{first, higher}).RegistryScoped("window", "order")
	require.NoError(t, err)
	require.Equal(t, "datasource://one/explicit", entries[0].DataSourceResource.URI)
	higher.Bindings[0].Lookup.DataSource = "window://one/wrong"
	_, err = New(capturedDefinitions{higher}).RegistryScoped("window", "order")
	require.Error(t, err, "kind mismatch cannot become an ambient local ID")
	higher.Bindings[0].Lookup.DataSource = ""
	entries, err = New(capturedDefinitions{higher}).RegistryScoped("window", "order")
	require.NoError(t, err)
	require.Nil(t, entries[0].DataSourceResource)
}
