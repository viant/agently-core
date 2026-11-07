package skill

import (
	"context"
	"github.com/stretchr/testify/require"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"testing"
)

func TestCurrentAgentUsesOwnedDynamicRuntime(t *testing.T) {
	service := &Service{}
	dynamic := &agentmdl.Agent{Identity: agentmdl.Identity{ID: "skill/generated/review"}, Skills: []string{"skill://example/review"}}
	ctx := WithRuntimeState(context.Background(), service, dynamic, nil)
	got, err := service.currentAgent(ctx)
	require.NoError(t, err)
	require.Same(t, dynamic, got)
	output := &ListOutput{}
	require.NoError(t, service.list(ctx, &ListInput{}, output), "native skill listing must not reload a generated agent from disk")
	_, err = (&Service{}).currentAgent(ctx)
	require.Error(t, err, "another service must not adopt this runtime")
}
