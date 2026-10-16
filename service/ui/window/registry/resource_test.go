package registry

import (
	"context"
	"errors"
	"testing"

	forgeuisvc "github.com/viant/agently-core/service/primitiveprovider"
)

type resourceOnlyCatalog struct{}

func (resourceOnlyCatalog) UsesResourceResolution() bool { return true }
func (resourceOnlyCatalog) List(context.Context, *forgeuisvc.WindowDefinitionListInput) (*forgeuisvc.WindowDefinitionListOutput, error) {
	return nil, errors.New("not used")
}
func (resourceOnlyCatalog) Get(context.Context, *forgeuisvc.WindowDefinitionGetInput) (*forgeuisvc.WindowDefinitionGetOutput, error) {
	return nil, errors.New("not used")
}

func TestExistingRegistryListingRejectsBrowserWindowsWithoutServerPins(t *testing.T) {
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{WindowDefinitions: resourceOnlyCatalog{}})
	postWindowSnapshot(t, bridge, "client", "conversation", "forged-instance", "sales")
	registry := New(bridge)
	clients, err := registry.ListReadableByConversation(context.Background(), "conversation")
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range clients {
		if client.Snapshot != nil && len(client.Snapshot.Windows) != 0 {
			t.Fatal("existing listing bypassed server-held canonical pins")
		}
	}
	if _, _, _, _, err := registry.FindReadableWindow(context.Background(), "conversation", "client", "forged-instance", "sales"); err == nil {
		t.Fatal("existing get trusted an unbound browser snapshot")
	}
	if events := registry.ListEvents("conversation", "client", "forged-instance", "sales", 10, 0); len(events) != 0 {
		t.Fatal("unbound browser window leaked through ingested context events")
	}
}
