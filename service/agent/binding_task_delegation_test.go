package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	agentmdl "github.com/viant/agently-core/protocol/agent"
)

func TestBuildTaskBinding_AddsRuntimeDelegationDirectiveForTopLevelCoderRepoAnalysis(t *testing.T) {
	workdir := t.TempDir()
	service := &Service{}
	input := &QueryInput{
		Agent: &agentmdl.Agent{
			Identity: agentmdl.Identity{ID: "coder"},
			Delegation: &agentmdl.Delegation{
				Enabled:  true,
				MaxDepth: 2,
			},
		},
		Query: "Analyze project " + workdir,
	}

	task := service.buildTaskBinding(input)

	require.Contains(t, task.Prompt, "Runtime directive:")
	require.Contains(t, task.Prompt, "llm/agents:run")
	require.Contains(t, task.Prompt, workdir)
	require.True(t, strings.Contains(task.Prompt, "User request:\nAnalyze project "+workdir))
}

func TestBuildTaskBinding_SkipsRuntimeDelegationDirectiveForDelegatedCoderChild(t *testing.T) {
	workdir := t.TempDir()
	service := &Service{}
	input := &QueryInput{
		Agent: &agentmdl.Agent{
			Identity: agentmdl.Identity{ID: "coder"},
			Delegation: &agentmdl.Delegation{
				Enabled:  true,
				MaxDepth: 2,
			},
		},
		Context: map[string]interface{}{
			"DelegationDepths": map[string]interface{}{
				"coder": 1,
			},
		},
		Query: "Analyze project " + workdir,
	}

	task := service.buildTaskBinding(input)

	require.Equal(t, "Analyze project "+workdir, task.Prompt)
}
