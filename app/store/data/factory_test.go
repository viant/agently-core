package data

import (
	"context"
	"testing"

	"github.com/viant/agently-core/internal/sqlitewrite"
	"github.com/viant/agently-core/pkg/agently/conversation/write"
)

func TestNativeInMemoryFactoryGeneratedReadWriteAndOwnership(t *testing.T) {
	ctx := context.Background()
	server, err := NewRuntimeInMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(ctx) })
	service := NewService(server).(*datlyService)
	if service.native != server {
		t.Fatal("facade did not retain shared native invoker")
	}
	driver, err := server.ConfiguredDriver(ctx, "agently")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := server.ConnectionIdentity(ctx, "agently")
	if err != nil {
		t.Fatal(err)
	}
	if service.writeGate != sqlitewrite.KeyForConnector(driver, identity, "agently") || service.writeGate == "" {
		t.Fatal("native SQLite facade lost coordinated write gate")
	}
	row := write.NewMutableConversationView(write.WithConversationID("factory-native-memory"), write.WithConversationStatus("active"))
	if _, err = service.PatchConversations(ctx, []*write.MutableConversationView{row}); err != nil {
		t.Fatal(err)
	}
	stored, err := service.GetConversation(ctx, row.Id, nil)
	if err != nil || stored == nil || stored.Id != row.Id {
		t.Fatalf("generated in-memory round trip: %#v %v", stored, err)
	}
	if err = CloseService(ctx, service); err != nil {
		t.Fatal(err)
	}
	stored, err = service.GetConversation(ctx, row.Id, nil)
	if err != nil || stored == nil {
		t.Fatalf("borrowed facade shut down caller runtime: %#v %v", stored, err)
	}
}
