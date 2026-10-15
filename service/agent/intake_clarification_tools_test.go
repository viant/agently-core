package agent

import (
	"context"
	"github.com/stretchr/testify/require"
	intakesvc "github.com/viant/agently-core/service/intake"
	"testing"
)

func TestClarificationDoesNotDiscoverOrExposeExecutionTools(t *testing.T) {
	// No registry or profile loader: either would fail if clarification attempted discovery.
	service := &Service{}
	input := &QueryInput{PromptProfileId: "unused-profile", ToolsAllowed: []string{"remote/write"}, ToolBundles: []string{"remote-tools"}, Context: map[string]interface{}{
		intakesvc.ContextKey: &intakesvc.Context{Routing: intakesvc.RoutingContext{Mode: intakesvc.ModeClarify}},
	}}
	tools, err := service.resolveTools(context.Background(), input)
	require.NoError(t, err)
	require.Empty(t, tools)
	require.Equal(t, []string{"remote/write"}, input.ToolsAllowed)
	require.Equal(t, []string{"remote-tools"}, input.ToolBundles)
}
