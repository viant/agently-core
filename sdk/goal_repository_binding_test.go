package sdk

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/internal/service/conversation/memory"
	"github.com/viant/agently-core/service/agent"
	goals "github.com/viant/agently-core/service/goal"
	"testing"
)

type injectedSDKGoalRepository struct {
	goals.Repository
	reads int
}

func (r *injectedSDKGoalRepository) Get(_ context.Context, conversationID string) (*goals.Record, error) {
	r.reads++
	return &goals.Record{ID: "injected-goal", ConversationID: conversationID, Objective: "injected SDK repository", Status: "active"}, nil
}

type controllerOnlySDKGoalStore struct{ goals.Store }

func TestBackendRuntimeRetainsInjectedGoalRepository(t *testing.T) {
	server, _ := linkedGoalRepoForSDK(t, "injected-sdk-conversation")
	repository := &injectedSDKGoalRepository{}
	rt := &executor.Runtime{Native: server, Data: data.NewService(server), GoalStore: repository, Agent: &agent.Service{}, Conversation: memory.New()}
	client, err := newBackendFromRuntime(rt)
	require.NoError(t, err)
	require.Same(t, repository, client.goalRepo)
	output, err := client.GetGoal(context.Background(), "injected-sdk-conversation")
	require.NoError(t, err)
	require.Equal(t, "injected SDK repository", output.Objective)
	require.Equal(t, 1, repository.reads)
}
func TestBackendRuntimeDoesNotOverrideControllerOnlyGoalStore(t *testing.T) {
	server, _ := linkedGoalRepoForSDK(t, "readonly-sdk-conversation")
	rt := &executor.Runtime{Native: server, Data: data.NewService(server), GoalStore: &controllerOnlySDKGoalStore{}, Agent: &agent.Service{}, Conversation: memory.New()}
	client, err := newBackendFromRuntime(rt)
	require.NoError(t, err)
	require.Nil(t, client.goalRepo)
	require.Same(t, server, client.goalInvoker)
}
