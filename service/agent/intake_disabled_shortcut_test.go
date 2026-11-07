package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	intakesvc "github.com/viant/agently-core/service/intake"
)

func TestDisabledModelIntakeRetainsAuthoredShortcut(t *testing.T) {
	// No classifier is installed: configured exact shortcuts do not need one.
	service := &Service{}
	agent := &agentmdl.Agent{Intake: agentmdl.Intake{Enabled: false, ActivationRules: []agentmdl.ActivationRule{{
		ID:     "open_summary",
		Match:  agentmdl.ActivationMatch{Pattern: `(?i)^open summary$`},
		Action: agentmdl.ActivationAction{Tool: "ui/window:show", Input: map[string]interface{}{"windowId": "summary"}},
	}}}}
	input := &QueryInput{Agent: agent, Query: "Open summary"}
	service.maybeRunIntakeSidecar(context.Background(), input)
	selected := intakesvc.FromContext(input.Context)
	require.NotNil(t, selected)
	require.Equal(t, "ui/window:show", selected.DirectAction.ToolName)
	require.Equal(t, "summary", selected.DirectAction.Input["windowId"])
	require.False(t, selected.DirectActionExecution.Executed, "selection does not bypass normal execution")

	unmatched := &QueryInput{Agent: agent, Query: "Explain the summary"}
	service.maybeRunIntakeSidecar(context.Background(), unmatched)
	require.Nil(t, intakesvc.FromContext(unmatched.Context), "unmatched input remains for the normal agent loop")
	require.Equal(t, "Explain the summary", unmatched.Query)
}
