package sdk

import (
	"context"
	"testing"

	cancelstore "github.com/viant/agently-core/app/store/conversation/cancel"
)

func TestEmbeddedClient_CancelTurn_NoRegistry(t *testing.T) {
	client := &backendClient{}

	cancelled, err := client.CancelTurn(context.Background(), "missing-turn")
	if err != nil {
		t.Fatalf("CancelTurn() unexpected error: %v", err)
	}
	if cancelled {
		t.Fatalf("CancelTurn() = %v, want false", cancelled)
	}
}

func TestEmbeddedClient_CancelTurn_WithRegistry(t *testing.T) {
	reg := cancelstore.NewMemory()
	called := false
	reg.Register("conv-1", "turn-1", func() { called = true })

	client := &backendClient{cancelRegistry: reg}

	cancelled, err := client.CancelTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatalf("CancelTurn() unexpected error: %v", err)
	}
	if !cancelled {
		t.Fatalf("CancelTurn() = %v, want true", cancelled)
	}
	if !called {
		t.Fatalf("CancelTurn() did not invoke cancel function")
	}
}
