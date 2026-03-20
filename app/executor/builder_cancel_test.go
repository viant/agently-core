package executor_test

import (
	"context"
	"testing"

	"github.com/viant/agently-core/app/executor"
	cancelstore "github.com/viant/agently-core/app/store/conversation/cancel"
	"github.com/viant/agently-core/genai/llm"
	agentproto "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/sdk"
)

type stubAgentFinder struct{}

func (stubAgentFinder) Find(context.Context, string) (*agentproto.Agent, error) {
	return &agentproto.Agent{}, nil
}

type stubModelFinder struct{}

func (stubModelFinder) Find(context.Context, string) (llm.Model, error) {
	return stubModel{}, nil
}

type stubModel struct{}

func (stubModel) Generate(context.Context, *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	return &llm.GenerateResponse{}, nil
}

func (stubModel) Implements(string) bool { return false }

func TestBuilderBuild_DefaultCancelRegistrySharedWithEmbeddedClient(t *testing.T) {
	rt, err := executor.NewBuilder().
		WithAgentFinder(stubAgentFinder{}).
		WithModelFinder(stubModelFinder{}).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if rt.CancelRegistry == nil {
		t.Fatalf("Runtime.CancelRegistry is nil")
	}

	client, err := sdk.NewEmbeddedFromRuntime(rt)
	if err != nil {
		t.Fatalf("NewEmbeddedFromRuntime() error = %v", err)
	}

	called := false
	rt.CancelRegistry.Register("conv-1", "turn-1", func() { called = true })

	cancelled, err := client.CancelTurn(context.Background(), "turn-1")
	if err != nil {
		t.Fatalf("CancelTurn() error = %v", err)
	}
	if !cancelled {
		t.Fatalf("CancelTurn() = false, want true")
	}
	if !called {
		t.Fatalf("CancelTurn() did not invoke registered cancel function")
	}
}

func TestBuilderBuild_CustomCancelRegistryPreserved(t *testing.T) {
	reg := cancelstore.NewMemory()

	rt, err := executor.NewBuilder().
		WithAgentFinder(stubAgentFinder{}).
		WithModelFinder(stubModelFinder{}).
		WithCancelRegistry(reg).
		Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if rt.CancelRegistry != reg {
		t.Fatalf("Runtime.CancelRegistry = %p, want %p", rt.CancelRegistry, reg)
	}
}
